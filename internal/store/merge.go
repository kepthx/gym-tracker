package store

import "time"

// The window client timestamps are clamped into.
//
// Without the clamp, a phone whose clock is a year ahead wins every subsequent
// last-write-wins comparison forever — including comparisons against data that is
// demonstrably fresher. The clamp costs one line and does not affect normal operation.
const (
	clampPast   = 7 * 24 * time.Hour
	clampFuture = time.Minute
)

// clampTS brings a client timestamp into a sane window around server time.
func clampTS(clientTS int64, now time.Time) int64 {
	lo := now.Add(-clampPast).UnixMilli()
	hi := now.Add(clampFuture).UnixMilli()
	switch {
	case clientTS < lo:
		return lo
	case clientTS > hi:
		return hi
	default:
		return clientTS
	}
}

// newer compares (timestamp, device) pairs lexicographically.
//
// Comparing by timestamp alone diverges when two devices write within the same
// millisecond. Using the device as the second key makes the winner identical on every
// node and under any arrival order.
func newer(ts int64, device string, curTS int64, curDevice string) bool {
	if ts != curTS {
		return ts > curTS
	}
	return device > curDevice
}

// startsLater decides which of two workouts stays open when an offline client started
// a second one without closing the first.
//
// The rule is commutative: applying (S1, then S2) and (S2, then S1) yields the same
// final state, so the delivery order of batches does not matter.
func startsLater(aStartedAt int64, aID string, bStartedAt int64, bID string) bool {
	if aStartedAt != bStartedAt {
		return aStartedAt > bStartedAt
	}
	return aID > bID
}
