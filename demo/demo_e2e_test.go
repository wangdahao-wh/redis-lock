// go:build demo

package demo

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClient_TryLock_e2e(t *testing.T) {
	rdb := redis.NewClient(&redis.Options{
		Addr:     "localhost:6379",
		Password: "", // no password set
		DB:       0,
	})
	c := NewClient(rdb)
	c.Wait()

	testCases := []struct {
		name string

		// 准备数据
		before func()
		// 校验 redis 数据并且清理数据
		after func()
		// 输入
		key        string
		expiration time.Duration

		wantErr  error
		wantLock *Lock
	}{
		{
			name:   "locked",
			before: func() {},
			after: func() {
				res, err := rdb.Del(context.Background(), "locked-key").Result()
				require.NoError(t, err)
				require.Equal(t, int64(1), res)
			},
			key:        "locked-key",
			expiration: time.Minute,
			wantLock: &Lock{
				Key: "locked-key",
			},
		},
		{
			name: "failed to lock",
			key:  "failed-key",

			before: func() {
				val, err := rdb.Set(context.Background(), "failed-key", "123", time.Minute).Result()
				require.NoError(t, err)
				require.Equal(t, "OK", val)
			},
			after: func() {
				val, err := rdb.Get(context.Background(), "failed-key").Result()
				require.NoError(t, err)
				require.Equal(t, "123", val)
			},
			expiration: time.Minute,
			wantErr:    ErrFailedToPreemptLock,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tc.before()
			l, err := c.TryLock(context.Background(), tc.key, tc.expiration)
			assert.Equal(t, tc.wantErr, err)
			if err != nil {
				return
			}
			tc.after()
			assert.NotNil(t, l.client)
			assert.Equal(t, tc.wantLock.Key, l.Key)
			assert.NotEmpty(t, l.Value)
		})
	}
}

func (c *Client) Wait() {
	for c.Client.Ping(context.Background()) != nil {

	}
}
