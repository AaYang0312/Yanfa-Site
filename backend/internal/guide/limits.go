package guide

import (
	"sync"
	"time"
	"yuanfa-official/backend/internal/contract"
)

// Limits applies a rolling one-minute IP budget to all guide requests and a
// non-blocking, separately acquired inference semaphore. IDs use only Allow.
// Expired IP records are removed; neither histories nor requirements are stored.
type Limits struct {
	mu    sync.Mutex
	rpm   int
	byIP  map[string][]time.Time
	prune time.Time
	slots chan struct{}
}

func NewLimits(rpm, concurrency int) *Limits {
	if rpm <= 0 {
		rpm = 10
	}
	if concurrency <= 0 {
		concurrency = 4
	}
	return &Limits{rpm: rpm, byIP: map[string][]time.Time{}, slots: make(chan struct{}, concurrency)}
}
func limited() error {
	return contract.NewBusinessError(429, "RATE_LIMITED", "请求较多，请稍后重试。")
}
func (l *Limits) Allow(ip string) error { return l.allowAt(ip, time.Now()) }
func (l *Limits) allowAt(ip string, now time.Time) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	cutoff := now.Add(-time.Minute)
	if now.Sub(l.prune) >= time.Minute {
		for key, times := range l.byIP {
			if len(times) == 0 || !times[len(times)-1].After(cutoff) {
				delete(l.byIP, key)
			}
		}
		l.prune = now
	}
	times := l.byIP[ip]
	first := 0
	for first < len(times) && !times[first].After(cutoff) {
		first++
	}
	if first > 0 {
		copy(times, times[first:])
		times = times[:len(times)-first]
	}
	if len(times) >= l.rpm {
		return limited()
	}
	l.byIP[ip] = append(times, now)
	return nil
}

// Acquire never queues a fifth model request. Release must be deferred until
// Consult has drained the Eino run, including cancellation of in-flight tools.
func (l *Limits) Acquire() (release func(), err error) {
	select {
	case l.slots <- struct{}{}:
		return func() { <-l.slots }, nil
	default:
		return nil, limited()
	}
}
