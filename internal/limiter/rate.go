package limiter

import (
	"sync"
	"time"
)

// Giới hạn số lượng request trong 1 giây
type RateLimiter struct {
	requestTimes []time.Time
	maxRequests  int
	mu           sync.Mutex
}

func NewRateLimiter(maxRequests int) *RateLimiter {
	return &RateLimiter{
		requestTimes: make([]time.Time, 0, maxRequests),
		maxRequests:  maxRequests,
	}
}

// Check tra xem có thể thực hiện request mới hay không
func (r *RateLimiter) Allow() bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	oneSecondAgo := now.Add(-1 * time.Second)

	// Xóa các request cũ hơn 1 giây
	validTimes := make([]time.Time, 0, len(r.requestTimes))
	for _, t := range r.requestTimes {
		if t.After(oneSecondAgo) {
			validTimes = append(validTimes, t)
		}
	}
	r.requestTimes = validTimes

	// Nếu số lượng request trong 1 giây vừa qua nhỏ hơn giới hạn thì add request mới và cho phép thực hiện
	if len(r.requestTimes) < r.maxRequests {
		r.requestTimes = append(r.requestTimes, now)
		return true
	}

	return false
}
