// go:build demo

package demo

import (
	"context"
	_ "embed"
	"errors"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	rlock "github.com/wangdahao-wh/redis-lock"
)

type Client struct {
	Client redis.Cmdable
	s      singleflight.Group
}

var (
	ErrFailedToPreemptLock = errors.New("加锁失败")
	ErrLockNotHold         = errors.New("未持有锁")
	// go:embed unlock.lua
	luaUnlock string
	// go:embed refresh.lua
	luaRefresh string
	// go:embed lock.lua
	luaLock string
)

func NewClient(c redis.Cmdable) *Client {
	return &Client{
		Client: c,
	}
}

// 高并发场景下加锁
func (c *Client) SingleflightLock(ctx context.Context, key string, expiration time.Duration,
	retry rlock.RetryStrategy, timeout time.Duration) (*Lock, error) {
	for {
		flag := false
		// singleflight.Group 可以防止多个goroutine同时对同一个资源进行冗余的操作
		// DoChan方法会确保对于相同的key，只有一个goroutine执行传入的函数
		resCh := c.s.DoChan(key, func() (interface{}, error) {
			flag = true
			return c.Lock(ctx, key, expiration, retry, timeout)
		})
		select {
		case res := <-resCh:
			if flag {
				if res.Err != nil {
					return nil, res.Err
				}
				return res.Val.(*Lock), nil
			}
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func (c *Client) Lock(ctx context.Context, key string, expiration time.Duration,
	retry rlock.RetryStrategy, timeout time.Duration) (*Lock, error) {
	val := uuid.New().String()
	var timer *time.Timer
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	for {
		rctx, cancel := context.WithTimeout(ctx, timeout)
		res, err := c.Client.
			Eval(rctx, luaLock, []string{key}, val, expiration).
			Bool()
		cancel()
		if err != nil && !errors.Is(err, context.DeadlineExceeded) {
			// DeadlineExceeded表示单次操作超时
			// 注意：如果是单次操作超时，我们将其视为获取锁失败，会继续重试。
			return nil, err
		}
		if res {
			// 加锁成功
			return NewLock(c.Client, key, val, expiration), nil
		}
		//重试
		interval, ok := retry.Next()
		if !ok {
			return nil, ErrFailedToPreemptLock
		}
		if timer == nil {
			timer = time.NewTimer(interval)
		}
		timer.Reset(interval)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
			// 继续for循环
		}
	}
}

func (c *Client) TryLock(ctx context.Context, key string,
	expiration time.Duration) (*Lock, error) {
	val := uuid.New().String()
	ok, err := c.Client.SetNX(ctx, key, val, expiration).Result()
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrFailedToPreemptLock
	}

	return NewLock(c.Client, key, val, expiration), nil
}

type Lock struct {
	client     redis.Cmdable
	Key        string
	Value      string
	Expiration time.Duration
	unlock     chan struct{}
}

func NewLock(client redis.Cmdable, key string, value string,
	expiration time.Duration) *Lock {
	return &Lock{
		client:     client,
		Key:        key,
		Value:      value,
		Expiration: expiration,
		unlock:     make(chan struct{}, 1),
	}
}

// 自动续约
func (l *Lock) AutoRefresh(interval, timeout time.Duration) error {
	ch := make(chan struct{}, 1)
	defer close(ch)
	ticker := time.NewTicker(interval)

	for {
		select {
		case <-ch:
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			err := l.Refresh(ctx)
			cancel()
			if err == context.DeadlineExceeded {
				ch <- struct{}{}
				continue
			}
			if err != nil {
				return err
			}
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			err := l.Refresh(ctx)
			cancel()
			if err == context.DeadlineExceeded {
				ch <- struct{}{}
				continue
			}
			if err != nil {
				return err
			}
		case <-l.unlock:
			return nil
		}
	}
}

// 续约
func (l *Lock) Refresh(ctx context.Context) error {
	res, err := l.client.
		Eval(ctx, luaRefresh, []string{l.Key}, l.Value, l.Expiration.Milliseconds()).
		Int64()
	// 键不存在
	// 在 Redis 中没有找到相应的键。具体来说，当一个键在 Redis 中不存在时，某些命令（如 GET、HGET 等）会返回 redis.Nil 错误。
	if err == redis.Nil {
		return ErrLockNotHold
	}
	if err != nil {
		return err
	}
	if res != int64(1) {
		return ErrLockNotHold
	}
	return nil
}

func (l *Lock) Unlock(ctx context.Context) error {
	defer func() {
		l.unlock <- struct{}{}
		close(l.unlock)
	}()
	res, err := l.client.
		Eval(ctx, luaUnlock, []string{l.Key}, l.Value).
		Int64()
	if err == redis.Nil {
		return ErrLockNotHold
	}
	if err != nil {
		return err
	}

	if res == 0 {
		return ErrLockNotHold
	}
	return nil
}
