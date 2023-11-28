package redis_lock

import (
	redismocks "Prove/mocks"
	"context"
	"errors"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
	"testing"
	"time"
)

func TestRedisLock(t *testing.T) {
	testCases := []struct {
		name       string
		mock       func(controller *gomock.Controller) redis.Cmdable
		key        string
		expiration time.Duration
		wantLock   *Lock
		wantErr    error
	}{
		{
			name:       "加锁成功！",
			key:        "123",
			expiration: time.Minute,
			mock: func(ctrl *gomock.Controller) redis.Cmdable {
				cmd := redismocks.NewMockCmdable(ctrl)
				res := redis.NewBoolResult(true, nil)
				cmd.EXPECT().SetNX(gomock.Any(), "123",
					gomock.Any(), time.Minute).Return(res)
				return cmd
			},
			wantLock: &Lock{key: "123"},
		},
		{
			name:       "加锁失败！",
			key:        "234",
			expiration: time.Minute,
			mock: func(ctrl *gomock.Controller) redis.Cmdable {
				cmd := redismocks.NewMockCmdable(ctrl)
				res := redis.NewBoolResult(false, errors.New("加锁失败！"))
				cmd.EXPECT().SetNX(gomock.Any(), "234",
					gomock.Any(), time.Minute).Return(res)
				return cmd
			},
			wantErr: errors.New("加锁失败！"),
		},
		{
			name:       "抢锁失败！",
			key:        "456",
			expiration: time.Minute,
			mock: func(ctrl *gomock.Controller) redis.Cmdable {
				cmd := redismocks.NewMockCmdable(ctrl)
				res := redis.NewBoolResult(false, nil)
				cmd.EXPECT().SetNX(gomock.Any(), "456",
					gomock.Any(), time.Minute).Return(res)
				return cmd
			},
			wantErr: ErrFailedToPreemptLock,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()
			c := NewClient(tc.mock(ctrl))

			l, err := c.TryLock(context.Background(), tc.key, tc.expiration)
			assert.Equal(t, tc.wantErr, err)
			if err != nil {
				return
			}
			assert.NotNil(t, l.client)
			assert.Equal(t, tc.wantLock.key, l.key)
			assert.NotEmpty(t, l.val)
		})
	}
}
