package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// mergeCases is the truth table shared with the client. Vitest reads the same file
// (web/src/db/merge.test.ts). Code cannot be shared between Go and TypeScript, but the
// truth can be, and a divergence between the merge implementations is caught on both sides.
type mergeCases struct {
	LWW []struct {
		Name      string `json:"name"`
		TS        int64  `json:"ts"`
		Device    string `json:"device"`
		CurTS     int64  `json:"cur_ts"`
		CurDevice string `json:"cur_device"`
		Newer     bool   `json:"newer"`
	} `json:"lww"`

	Sets []struct {
		Name     string  `json:"name"`
		Current  *SetRow `json:"current"`
		Incoming SetRow  `json:"incoming"`
		Expected SetRow  `json:"expected"`
	} `json:"sets"`

	Sessions []struct {
		Name     string      `json:"name"`
		Current  *SessionRow `json:"current"`
		Incoming SessionRow  `json:"incoming"`
		Expected SessionRow  `json:"expected"`
	} `json:"sessions"`
}

func loadMergeCases(t *testing.T) mergeCases {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "merge_cases.json"))
	if err != nil {
		t.Fatalf("прочитать таблицу истины: %v", err)
	}
	var cases mergeCases
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatalf("разобрать таблицу истины: %v", err)
	}
	if len(cases.LWW) == 0 || len(cases.Sets) == 0 || len(cases.Sessions) == 0 {
		t.Fatal("таблица истины пуста — тест ничего не проверяет")
	}
	return cases
}

func TestSharedLWWTable(t *testing.T) {
	for _, tc := range loadMergeCases(t).LWW {
		t.Run(tc.Name, func(t *testing.T) {
			if got := newer(tc.TS, tc.Device, tc.CurTS, tc.CurDevice); got != tc.Newer {
				t.Fatalf("newer(%d,%q,%d,%q) = %v, ожидалось %v",
					tc.TS, tc.Device, tc.CurTS, tc.CurDevice, got, tc.Newer)
			}
		})
	}
}

func TestSharedSetMergeTable(t *testing.T) {
	for _, tc := range loadMergeCases(t).Sets {
		t.Run(tc.Name, func(t *testing.T) {
			got := MergeSet(tc.Current, tc.Incoming)
			if !reflect.DeepEqual(got, tc.Expected) {
				t.Fatalf("слияние дало\n%+v\nожидалось\n%+v", got, tc.Expected)
			}
		})
	}
}

func TestSharedSessionMergeTable(t *testing.T) {
	for _, tc := range loadMergeCases(t).Sessions {
		t.Run(tc.Name, func(t *testing.T) {
			got := MergeSession(tc.Current, tc.Incoming)
			if !sessionsEqual(got, tc.Expected) {
				t.Fatalf("слияние дало\n%s\nожидалось\n%s", showSession(got), showSession(tc.Expected))
			}
		})
	}
}

// The merge has to be commutative: otherwise the delivery order of batches would change history.
func TestSessionMergeIsCommutative(t *testing.T) {
	for _, tc := range loadMergeCases(t).Sessions {
		if tc.Current == nil {
			continue
		}
		t.Run(tc.Name, func(t *testing.T) {
			forward := MergeSession(tc.Current, tc.Incoming)
			backward := MergeSession(&tc.Incoming, *tc.Current)
			if !sessionsEqual(forward, backward) {
				t.Fatalf("слияние зависит от порядка:\n%s\nи\n%s",
					showSession(forward), showSession(backward))
			}
		})
	}
}

func TestSetMergeIsCommutative(t *testing.T) {
	for _, tc := range loadMergeCases(t).Sets {
		if tc.Current == nil {
			continue
		}
		t.Run(tc.Name, func(t *testing.T) {
			forward := MergeSet(tc.Current, tc.Incoming)
			backward := MergeSet(&tc.Incoming, *tc.Current)
			if !reflect.DeepEqual(forward, backward) {
				t.Fatalf("слияние зависит от порядка:\n%+v\nи\n%+v", forward, backward)
			}
		})
	}
}

func sessionsEqual(a, b SessionRow) bool {
	if (a.FinishedAt == nil) != (b.FinishedAt == nil) {
		return false
	}
	if a.FinishedAt != nil && *a.FinishedAt != *b.FinishedAt {
		return false
	}
	a.FinishedAt, b.FinishedAt = nil, nil
	return reflect.DeepEqual(a, b)
}

func showSession(s SessionRow) string {
	raw, _ := json.Marshal(s)
	return string(raw)
}
