package api

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// ipLimiter is the first brute-force layer: a per-IP token bucket in process memory.
//
// It resets on restart, which is acceptable because the second layer — the failure counter
// in the database — survives a restart. Together they cover both fast brute force and the
// slow kind that restarts the service between attempts.
type ipLimiter struct {
	mu       sync.Mutex
	buckets  map[string]*ipBucket
	every    time.Duration
	burst    int
	idleTTL  time.Duration
	lastSwep time.Time
}

type ipBucket struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

func newIPLimiter(every time.Duration, burst int) *ipLimiter {
	return &ipLimiter{
		buckets: make(map[string]*ipBucket),
		every:   every,
		burst:   burst,
		idleTTL: time.Hour,
	}
}

// allow debits one attempt from this IP's bucket.
func (l *ipLimiter) allow(ip string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.sweep(now)

	b, ok := l.buckets[ip]
	if !ok {
		b = &ipBucket{limiter: rate.NewLimiter(rate.Every(l.every), l.burst)}
		l.buckets[ip] = b
	}
	b.lastSeen = now
	return b.limiter.AllowN(now, 1)
}

// sweep drops addresses that have not been seen for a while, so the map does not grow
// without bound. No separate goroutine is needed: the cleanup rides on the same calls as
// the check itself.
func (l *ipLimiter) sweep(now time.Time) {
	if now.Sub(l.lastSwep) < l.idleTTL {
		return
	}
	l.lastSwep = now
	for ip, b := range l.buckets {
		if now.Sub(b.lastSeen) > l.idleTTL {
			delete(l.buckets, ip)
		}
	}
}
