// go:build demo

package demo

import (
	"context"
	_ "embed"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type Client struct {
	Client redis.Cmdable
}

var (
	ErrFailedToPreemptLock = errors.New("加锁失败")
	ErrLockNotHold         = errors.New("未持有锁")
	// go:embed unlock.lua
	luaUnlock string
	// go:embed refresh.lua
	luaRefresh string
)

func NewClient(c redis.Cmdable) *Client {
	return &Client{
		Client: c,
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
