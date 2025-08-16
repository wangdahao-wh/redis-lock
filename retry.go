package rlock

import "time"

type RetryStrategy interface {
	// Next 返回下一次重试的间隔，如果不需要继续重试，那么第二个参数返回 false
	Next() (time.Duration, bool)
}

type FixIntervalRetry struct {
	Interval time.Duration
	Max      int
	cnt      int
}

func (f *FixIntervalRetry) Next() (time.Duration, bool) {
	f.cnt++
	return f.Interval, f.cnt <= f.Max
}
