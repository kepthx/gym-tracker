package store

// Merging of ready-made rows. In normal operation the server merges rows directly in
// SQL; these functions are for where rows arrive in bulk from outside — importing an
// export.
//
// The rules here are the same as in ApplyBatch, and both sides — Go and the client-side
// TypeScript — are checked against one truth table, testdata/merge_cases.json.

// MergeSet picks the winner between two versions of a set row.
func MergeSet(current *SetRow, incoming SetRow) SetRow {
	if current == nil {
		return incoming
	}
	if newer(incoming.UpdatedTS, incoming.UpdatedBy, current.UpdatedTS, current.UpdatedBy) {
		return incoming
	}
	return *current
}

// MergeSession merges two versions of a workout.
//
// The start fields go by last-write-wins, while finish and deletion merge monotonically:
// the smallest finish time wins, and a deletion is never undone. Such a merge is
// commutative, so the outcome does not depend on order.
func MergeSession(current *SessionRow, incoming SessionRow) SessionRow {
	if current == nil {
		return incoming
	}

	merged := *current
	if newer(incoming.UpdatedTS, incoming.UpdatedBy, current.UpdatedTS, current.UpdatedBy) {
		merged = incoming
	}

	merged.FinishedAt = earliest(current.FinishedAt, incoming.FinishedAt)
	merged.Deleted = current.Deleted || incoming.Deleted
	merged.Rev = max(current.Rev, incoming.Rev)
	return merged
}

func earliest(a, b *int64) *int64 {
	switch {
	case a == nil:
		return b
	case b == nil:
		return a
	case *a <= *b:
		return a
	default:
		return b
	}
}
