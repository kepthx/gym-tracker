package store

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
)

func changes(t *testing.T, s *Store, userID, since int64, limit int, known []string) *ChangesResult {
	t.Helper()
	res, err := s.Changes(context.Background(), userID, since, limit, known)
	if err != nil {
		t.Fatalf("выбрать изменения: %v", err)
	}
	return res
}

// keysOf reduces a page of changes to a sorted list of row keys with their contents, so
// that pages can be added together and compared against a full query.
func keysOf(cs ChangeSet) []string {
	out := make([]string, 0, len(cs.Sessions)+len(cs.Sets))
	for _, s := range cs.Sessions {
		finish := int64(-1)
		if s.FinishedAt != nil {
			finish = *s.FinishedAt
		}
		out = append(out, fmt.Sprintf("session %s day=%s finish=%d del=%v", s.ID, s.DayID, finish, s.Deleted))
	}
	for _, s := range cs.Sets {
		weight := "null"
		if s.Weight != nil {
			weight = fmt.Sprintf("%v", *s.Weight)
		}
		out = append(out, fmt.Sprintf("set %s/%s/%d done=%v w=%s", s.SessionID, s.ExerciseID, s.Idx, s.Done, weight))
	}
	sort.Strings(out)
	return out
}

func TestChangesFullSync(t *testing.T) {
	s := newTestStore(t)
	userID := seedUser(t, s, "igor", programA)
	apply(t, s, userID, "phone", workout(uuidN(100), 1, programA))

	got := changes(t, s, userID, 0, 0, nil)

	if len(got.Changes.Sessions) != 1 {
		t.Errorf("тренировок: %d, ожидалась 1", len(got.Changes.Sessions))
	}
	if len(got.Changes.Sets) != 5 {
		t.Errorf("подходов: %d, ожидалось 5", len(got.Changes.Sets))
	}
	if got.HasMore {
		t.Error("полная выборка не должна сообщать о продолжении")
	}
	if got.Cursor <= 0 {
		t.Errorf("курсор %d, ожидался положительный", got.Cursor)
	}
	if len(got.Changes.Programs) != 1 || got.Changes.Programs[0].Hash != programA {
		t.Errorf("снапшоты программ: %v", got.Changes.Programs)
	}
}

func TestChangesDeltaAfterCursor(t *testing.T) {
	s := newTestStore(t)
	userID := seedUser(t, s, "igor", programA)
	session := uuidN(100)
	apply(t, s, userID, "phone", workout(session, 1, programA))

	first := changes(t, s, userID, 0, 0, nil)

	// Nothing changed — the delta has to be empty.
	empty := changes(t, s, userID, first.Cursor, 0, []string{programA})
	if len(empty.Changes.Sessions) != 0 || len(empty.Changes.Sets) != 0 {
		t.Errorf("дельта без изменений не пуста: %v", keysOf(empty.Changes))
	}
	if len(empty.Changes.Programs) != 0 {
		t.Error("уже известный снапшот программы прислан повторно")
	}

	next := uuidN(200)
	apply(t, s, userID, "phone", []Op{
		opStart(50, next, "d2", at(7200), at(7200), programA),
		opSet(51, next, "row_bb", 0, true, ptr(60.0), ptr("8"), at(7260)),
	})

	delta := changes(t, s, userID, first.Cursor, 0, []string{programA})
	if len(delta.Changes.Sessions) != 1 || delta.Changes.Sessions[0].ID != next {
		t.Errorf("в дельте не та тренировка: %v", keysOf(delta.Changes))
	}
	if len(delta.Changes.Sets) != 1 {
		t.Errorf("подходов в дельте: %d, ожидался 1", len(delta.Changes.Sets))
	}
}

// The central property of deltas: however many pages it takes, their union has to match
// the full query. A row lost at a page boundary would never come back.
func TestChangesPaginationLosesNothing(t *testing.T) {
	s := newTestStore(t)
	userID := seedUser(t, s, "igor", programA)

	// Workouts on different days: overlapping ones would be pulled in by the "a workout
	// cannot run past the start of the next one" rule and would skew the comparison.
	for i := 0; i < 4; i++ {
		apply(t, s, userID, "phone", workoutAt(uuidN(100+i), 1+i*10, programA, -int64(i)*86400))
	}

	full := changes(t, s, userID, 0, 0, nil)
	want := keysOf(full.Changes)

	for _, limit := range []int{1, 2, 3, 5, 7} {
		t.Run(fmt.Sprintf("порция %d", limit), func(t *testing.T) {
			var got []string
			cursor := int64(0)
			for round := 0; ; round++ {
				if round > 200 {
					t.Fatal("выборка не сходится: слишком много порций")
				}
				page := changes(t, s, userID, cursor, limit, nil)
				got = append(got, keysOf(page.Changes)...)
				cursor = page.Cursor
				if !page.HasMore {
					break
				}
			}
			sort.Strings(got)

			if cursor != full.Cursor {
				t.Errorf("итоговый курсор %d, ожидался %d", cursor, full.Cursor)
			}
			if strings.Join(got, "\n") != strings.Join(want, "\n") {
				t.Errorf("порции не сложились в полную выборку:\n--- ожидалось ---\n%s\n--- получено ---\n%s",
					strings.Join(want, "\n"), strings.Join(got, "\n"))
			}
		})
	}
}

func TestChangesAreIsolatedPerUser(t *testing.T) {
	s := newTestStore(t)
	igor := seedUser(t, s, "igor", programA)
	lena := seedUser(t, s, "lena", programB)

	apply(t, s, igor, "phone", workout(uuidN(100), 1, programA))
	apply(t, s, lena, "phone-lena", workout(uuidN(200), 50, programB))

	igorChanges := changes(t, s, igor, 0, 0, nil)
	for _, session := range igorChanges.Changes.Sessions {
		if session.ID != uuidN(100) {
			t.Errorf("в выборке Игоря чужая тренировка %s", session.ID)
		}
	}
	for _, set := range igorChanges.Changes.Sets {
		if set.SessionID != uuidN(100) {
			t.Errorf("в выборке Игоря чужой подход из %s", set.SessionID)
		}
	}
	if len(igorChanges.Changes.Programs) != 1 || igorChanges.Changes.Programs[0].Hash != programA {
		t.Errorf("Игорю прислана не его программа: %v", igorChanges.Changes.Programs)
	}

	lenaChanges := changes(t, s, lena, 0, 0, nil)
	if len(lenaChanges.Changes.Sessions) != 1 || lenaChanges.Changes.Sessions[0].ID != uuidN(200) {
		t.Errorf("в выборке Лены не её данные: %v", keysOf(lenaChanges.Changes))
	}
	if len(lenaChanges.Changes.Programs) != 1 || lenaChanges.Changes.Programs[0].Hash != programB {
		t.Errorf("Лене прислана не её программа: %v", lenaChanges.Changes.Programs)
	}
}

// The client has to receive the snapshot of the program a workout was recorded against,
// even if the program was replaced long ago: otherwise there is nothing to render history with.
func TestChangesShipProgramSnapshotsForHistory(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	userID := seedUser(t, s, "igor", programA)

	apply(t, s, userID, "phone", workout(uuidN(100), 1, programA))

	// Program change: the old snapshot stays in the database, the workout references it.
	if _, err := s.db.W.ExecContext(ctx,
		`INSERT INTO programs(hash, json, created_at) VALUES (?, '{"version":1}', 0)`, programB); err != nil {
		t.Fatalf("вставить новую программу: %v", err)
	}
	if _, err := s.db.W.ExecContext(ctx,
		`UPDATE users SET current_program_hash = ? WHERE id = ?`, programB, userID); err != nil {
		t.Fatalf("сменить программу: %v", err)
	}

	got := changes(t, s, userID, 0, 0, nil)

	hashes := map[string]bool{}
	for _, p := range got.Changes.Programs {
		hashes[p.Hash] = true
	}
	if !hashes[programA] {
		t.Error("не прислан снапшот программы, по которой записана история")
	}
	if !hashes[programB] {
		t.Error("не прислан снапшот текущей программы")
	}
}

func TestChangesLimitIsBounded(t *testing.T) {
	s := newTestStore(t)
	userID := seedUser(t, s, "igor", programA)
	apply(t, s, userID, "phone", workout(uuidN(100), 1, programA))

	// A negative or out-of-range limit is replaced by the default rather than turning into
	// a single query over the whole database.
	for _, limit := range []int{-1, 0, MaxChangesLimit + 1} {
		got := changes(t, s, userID, 0, limit, nil)
		if got.HasMore {
			t.Errorf("лимит %d: выборка обрезана, хотя данных мало", limit)
		}
	}
}

// A request carrying the id of a nonexistent user has to return an empty result rather
// than an error: otherwise an invalid token turns into a 500 instead of a 401.
func TestChangesForUnknownUserIsEmpty(t *testing.T) {
	s := newTestStore(t)
	seedUser(t, s, "igor", programA)
	apply(t, s, 1, "phone", workout(uuidN(100), 1, programA))

	got := changes(t, s, 999, 0, 0, nil)
	if len(got.Changes.Sessions) != 0 || len(got.Changes.Sets) != 0 || len(got.Changes.Programs) != 0 {
		t.Fatalf("несуществующему пользователю отдали данные: %v", keysOf(got.Changes))
	}
}

func TestDeletedSessionsReachTheClient(t *testing.T) {
	s := newTestStore(t)
	userID := seedUser(t, s, "igor", programA)
	session := uuidN(100)

	apply(t, s, userID, "phone", workout(session, 1, programA))
	cursor := changes(t, s, userID, 0, 0, nil).Cursor

	apply(t, s, userID, "phone", []Op{opDelete(50, session, at(7200))})

	// The tombstone has to reach the client: otherwise a deleted workout stays visible on
	// the other device forever.
	delta := changes(t, s, userID, cursor, 0, []string{programA})
	if len(delta.Changes.Sessions) != 1 || !delta.Changes.Sessions[0].Deleted {
		t.Fatalf("тумбстоун не пришёл в дельте: %v", keysOf(delta.Changes))
	}
}
