// go:build demo

package demo

import (
	"context"
	"demo_redis/mocks"
	_ "embed"
	"errors"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestClient_TryLock(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	testCases := []struct {
		name string
		mock func() redis.Cmdable
		// 输入
		key        string
		expiration time.Duration

		wantErr  error
		wantLock *Lock
	}{
		{
			name:       "locked",
			key:        "locked-key",
			expiration: time.Minute,
			mock: func() redis.Cmdable {
				rdb := mocks.NewMockCmdable(ctrl)
				res := redis.NewBoolResult(true, nil)
				rdb.EXPECT().
					SetNX(gomock.Any(), "locked-key", gomock.Any(), time.Minute).
					Return(res)
				return rdb
			},
			wantLock: &Lock{
				Key: "locked-key",
			},
		},
		{
			name:       "network-error",
			key:        "network-key",
			expiration: time.Minute,
			mock: func() redis.Cmdable {
				rdb := mocks.NewMockCmdable(ctrl)
				res := redis.NewBoolResult(false, errors.New("network error"))
				rdb.EXPECT().
					SetNX(gomock.Any(), "network-key", gomock.Any(), time.Minute).
					Return(res)
				return rdb
			},
			wantErr: errors.New("network error"),
		},
		{
			name:       "failed to lock",
			key:        "failed-key",
			expiration: time.Minute,
			mock: func() redis.Cmdable {
				rdb := mocks.NewMockCmdable(ctrl)
				res := redis.NewBoolResult(false, nil)
				rdb.EXPECT().
					SetNX(gomock.Any(), "failed-key", gomock.Any(), time.Minute).
					Return(res)
				return rdb
			},
			wantErr: ErrFailedToPreemptLock,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			c := NewClient(tc.mock())
			l, err := c.TryLock(context.Background(), tc.key, tc.expiration)
			assert.Equal(t, tc.wantErr, err)
			if err != nil {
				return
			}

			assert.NotNil(t, l.client)
			assert.Equal(t, tc.wantLock.Key, l.Key)
			assert.NotEmpty(t, l.Value)
		})
	}
}

// func TestClient_TryLock(t *testing.T) {
// 	type fields struct {
// 		Client redis.Cmdable
// 	}
// 	type args struct {
// 		ctx        context.Context
// 		key        string
// 		expiration time.Duration
// 	}
// 	tests := []struct {
// 		name    string
// 		fields  fields
// 		args    args
// 		want    *Lock
// 		wantErr bool
// 	}{
// 		// TODO: Add test cases.
// 	}
// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			c := &Client{
// 				Client: tt.fields.Client,
// 			}
// 			got, err := c.TryLock(tt.args.ctx, tt.args.key, tt.args.expiration)
// 			if (err != nil) != tt.wantErr {
// 				t.Errorf("Client.TryLock() error = %v, wantErr %v", err, tt.wantErr)
// 				return
// 			}
// 			if !reflect.DeepEqual(got, tt.want) {
// 				t.Errorf("Client.TryLock() = %v, want %v", got, tt.want)
// 			}
// 		})
// 	}
// }

func TestLock_Unlock(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	testCases := []struct {
		name    string
		mock    func() redis.Cmdable
		wantErr error
	}{
		{
			// 解锁成功
			name: "unlock",
			mock: func() redis.Cmdable {
				rdb := mocks.NewMockCmdable(ctrl)
				cmd := redis.NewCmd(context.Background())
				cmd.SetVal(int64(1))
				rdb.EXPECT().
					Eval(gomock.Any(), luaUnlock, gomock.Any(), gomock.Any()).
					Return(cmd)
				return rdb
			},
			wantErr: nil,
		},
		{
			// 解锁失败，因为网络问题
			name: "network error",
			mock: func() redis.Cmdable {
				rdb := mocks.NewMockCmdable(ctrl)
				cmd := redis.NewCmd(context.Background())
				cmd.SetErr(errors.New("network error"))
				rdb.EXPECT().
					Eval(gomock.Any(), luaUnlock, gomock.Any(), gomock.Any()).
					Return(cmd)
				return rdb
			},
			wantErr: errors.New("network error"),
		},
		{
			// 解锁失败，锁过期了，或者被人删了
			// 或者是别人的锁
			name: "lock not exist",
			mock: func() redis.Cmdable {
				rdb := mocks.NewMockCmdable(ctrl)
				cmd := redis.NewCmd(context.Background())
				cmd.SetVal(int64(0))
				rdb.EXPECT().
					Eval(gomock.Any(), luaUnlock, gomock.Any(), gomock.Any()).
					Return(cmd)
				return rdb
			},
			wantErr: ErrLockNotHold,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			l := NewLock(tc.mock(), "mock-key", "mock value", time.Minute)
			err := l.Unlock(context.Background())
			require.Equal(t, tc.wantErr, err)

		})

	}

}
