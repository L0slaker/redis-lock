package redis_lock

import (
	"context"
	_ "embed"
	"errors"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"
	"time"
)

// 分布式锁几个需要重点关注的地方
// 1.加锁：可能需要重试，重试次数？过期时间如何设置，设置的值要唯一
// 2.持有锁：考虑是否续约
// 3.解锁：确认锁的持有情况

var (
	//go:embed unlock.lua
	luaUnlock string
	//go:embed refresh.lua
	luaRefresh string
	//go:embed lock.lua
	luaLock string
)

var (
	ErrFailedToPreemptLock = errors.New("抢锁失败！")
	ErrLockNotHold         = errors.New("未持有锁！")
)

type Client struct {
	client redis.Cmdable
	g      singleflight.Group
	valuer func() string // 用于生成值
}

func NewClient(client redis.Cmdable) *Client {
	return &Client{client: client}
}

func (c *Client) SingleflightLock(ctx context.Context, key string, expiration time.Duration,
	retry RetryStrategy, timeout time.Duration) (*Lock, error) {
	for {
		flag := false // 确认是谁拿到了锁
		resCh := c.g.DoChan(key, func() (interface{}, error) {
			flag = true // 只有拿到了锁的 goroutine 是true
			return c.Lock(ctx, key, expiration, retry, timeout)
		})
		select {
		case res := <-resCh:
			// 有人拿到了锁
			if flag {
				if res.Err != nil {
					return nil, res.Err
				}
				return res.Val.(*Lock), nil
			}
		case <-ctx.Done():
			// 超时了，没人拿到锁
			return nil, ctx.Err()
		}
	}
}

// TryLock 不带有重试的加锁
func (c *Client) TryLock(ctx context.Context, key string, expiration time.Duration) (*Lock, error) {
	val := uuid.New().String()
	res, err := c.client.SetNX(ctx, key, val, expiration).Result()
	if err != nil {
		return nil, err
	}
	if !res {
		return nil, ErrFailedToPreemptLock
	}
	return newLock(c.client, key, val, expiration), nil
}

// Lock 重试也要分情况
// 1.如果是偶发性的超时，可以直接加锁
// 2.别人持有了锁，我们需要等待别人释放锁
func (c *Client) Lock(ctx context.Context, key string, expiration time.Duration,
	retry RetryStrategy, timeout time.Duration) (*Lock, error) {
	val := uuid.New().String()
	var timer *time.Timer
	for {
		lctx, cancel := context.WithTimeout(ctx, timeout)
		res, err := c.client.Eval(lctx, luaLock, []string{key}, val, expiration).Bool()
		cancel()
		// 不是加锁失败或者不是偶发性的超时，就不需要重试了
		if err != nil && errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		// 加锁成功，返回一个锁
		if res {
			return newLock(c.client, key, val, expiration), nil
		}

		// 进行重试，每次重试都 reset 定时器
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
		}
	}
}

type Lock struct {
	client     redis.Cmdable
	key        string
	val        string
	expiration time.Duration
	unlock     chan struct{} // 解决重复续约
}

func newLock(client redis.Cmdable, key string, val string, expiration time.Duration) *Lock {
	return &Lock{
		client:     client,
		key:        key,
		val:        val,
		expiration: expiration,
		unlock:     make(chan struct{}, 1),
	}
}

func (l *Lock) Unlock(ctx context.Context) error {
	defer func() {
		l.unlock <- struct{}{}
		close(l.unlock)
	}()
	// 释放锁之前，要确保仍然是锁的持有者
	// 让lua脚本帮我们一并完成 Get 和 Del 两个动作,如果不是同一时刻的操作
	// Get的时候可能还是拥有这把锁的，但是到Del的时候就不是了
	res, err := l.client.Eval(ctx, luaUnlock, []string{l.key}, l.val).Int64()
	if err == redis.Nil {
		return ErrLockNotHold
	}
	if err != nil {
		return err
	}
	// 拿到了别人的锁 或者 这个锁不存在
	if res == 0 {
		return ErrLockNotHold
	}
	return nil
}

// Refresh 如果确定仍然是锁的持有者，可以续约这把锁
func (l *Lock) Refresh(ctx context.Context) error {
	res, err := l.client.Eval(ctx, luaRefresh, []string{l.key}, l.val, l.expiration.Milliseconds()).Int64()
	if err == redis.Nil {
		return ErrLockNotHold
	}
	if err != nil {
		return err
	}
	// 存在锁，但不是自己的锁
	if res != 1 {
		return ErrLockNotHold
	}
	return nil
}

// AutoRefresh 自动续约
func (l *Lock) AutoRefresh(interval time.Duration, timeout time.Duration) error {
	retryCh := make(chan struct{}, 1)
	defer close(retryCh)
	ticker := time.NewTicker(interval)
	for {
		select {
		case <-retryCh:
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			err := l.Refresh(ctx)
			cancel()
			if err == context.DeadlineExceeded {
				// 立刻重试
				retryCh <- struct{}{}
				continue
			}
			if err != nil {
				return err
			}
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			err := l.Refresh(ctx)
			cancel()
			// 续约失败，立刻重试
			if err == context.DeadlineExceeded {
				retryCh <- struct{}{}
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
