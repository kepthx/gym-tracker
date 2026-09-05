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
//
// The returned refund hands the attempt back. Only failures should count — a successful
// login is not an attempt to brute-force anything, and the database layer already counts
// failures alone — but the debit has to happen up front, or a burst of parallel guesses
// would all pass the check before any of them is charged. So the token is reserved here
// and returned by the caller once the password has checked out.
func (l *ipLimiter) allow(ip string, now time.Time) (ok bool, refund func()) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.sweep(now)

	b, found := l.buckets[ip]
	if !found {
		b = &ipBucket{limiter: rate.NewLimiter(rate.Every(l.every), l.burst)}
		l.buckets[ip] = b
	}
	b.lastSeen = now

	r := b.limiter.ReserveN(now, 1)
	if !r.OK() || r.DelayFrom(now) > 0 {
		// Not allowed now. A reservation with a delay would still be charged, so give it
		// back: the request is refused, it must not also eat into the next one.
		r.CancelAt(now)
		return false, func() {}
	}
	return true, func() { r.CancelAt(now) }
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
