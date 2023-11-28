package redis_lock

import "time"

type RetryStrategy interface {
	Next() (time.Duration, bool)
}

type FixIntervalRetry struct {
	Interval time.Duration // 重试间隔
	Max      int           // 最大次数
	cnt      int
}

func (f *FixIntervalRetry) Next() (time.Duration, bool) {
	f.cnt++
	return f.Interval, f.cnt <= f.Max
}
