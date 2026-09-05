package store

import (
	"context"
	"fmt"
	"math/rand"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kepthx/gym-tracker/internal/db"
)

var testNow = time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)

// Timestamp for an operation.
//
// The workout happens in the past relative to "now": the server clamps client time at a
// ceiling of now+1 minute, so timestamps in the future would all collapse to one point
// and the last-write-wins tests would be checking the wrong thing.
func at(offsetSec int64) int64 {
	return testNow.Add(-3*time.Hour + time.Duration(offsetSec)*time.Second).UnixMilli()
}

func uuidN(n int) string { return fmt.Sprintf("00000000-0000-7000-8000-%012d", n) }

func ptr[T any](v T) *T { return &v }

const (
	programA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	programB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	ctx := context.Background()

	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("открыть базу: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	if err := d.Migrate(ctx); err != nil {
		t.Fatalf("миграции: %v", err)
	}
	return New(d)
}

// seedUser creates a user with a program attached — the minimum needed to start a workout.
func seedUser(t *testing.T, s *Store, username, programHash string) int64 {
	t.Helper()
	ctx := context.Background()

	if _, err := s.db.W.ExecContext(ctx,
		`INSERT INTO programs(hash, json, created_at) VALUES (?, '{"version":1}', 0)
		 ON CONFLICT(hash) DO NOTHING`, programHash); err != nil {
		t.Fatalf("вставить программу: %v", err)
	}
	res, err := s.db.W.ExecContext(ctx,
		`INSERT INTO users(username, password_hash, created_at, current_program_hash)
		 VALUES (?, 'x', 0, ?)`, username, programHash)
	if err != nil {
		t.Fatalf("вставить пользователя %s: %v", username, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("id пользователя: %v", err)
	}
	return id
}

func opStart(n int, sessionID, dayID string, startedAt, ts int64, hash string) Op {
	return Op{
		OpID: uuidN(n), TS: ts, Type: OpSessionStart, SessionID: sessionID,
		Date: "2026-07-28", DayID: dayID, StartedAt: startedAt, ProgramHash: hash,
	}
}

func opSet(n int, sessionID, exerciseID string, idx int64, done bool, weight *float64, reps *string, ts int64) Op {
	return Op{
		OpID: uuidN(n), TS: ts, Type: OpSetUpsert, SessionID: sessionID,
		ExerciseID: exerciseID, Idx: &idx, Done: &done, Weight: weight, Reps: reps,
	}
}

func opFinish(n int, sessionID string, finishedAt, ts int64) Op {
	return Op{OpID: uuidN(n), TS: ts, Type: OpSessionFinish, SessionID: sessionID, FinishedAt: finishedAt}
}

func opDelete(n int, sessionID string, ts int64) Op {
	return Op{OpID: uuidN(n), TS: ts, Type: OpSessionDelete, SessionID: sessionID}
}

func apply(t *testing.T, s *Store, userID int64, device string, ops []Op) []OpResult {
	t.Helper()
	res, err := s.ApplyBatch(context.Background(), userID, device, ops, testNow)
	if err != nil {
		t.Fatalf("применить батч: %v", err)
	}
	return res
}

// dumpState prints a user's state in a deterministic form.
//
// rev is deliberately left out of the dump: it depends on the order operations were
// applied, and what we are checking is precisely that the CONTENT does not.
func dumpState(t *testing.T, s *Store, userID int64) string {
	t.Helper()
	ctx := context.Background()
	var b strings.Builder

	rows, err := s.db.R.QueryContext(ctx,
		`SELECT id, date, day_id, program_hash, started_at, COALESCE(finished_at, -1),
		        deleted, note, updated_ts, updated_by
		 FROM sessions WHERE user_id = ? ORDER BY id`, userID)
	if err != nil {
		t.Fatalf("выбрать тренировки: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, date, dayID, hash, note, updatedBy string
		var startedAt, finishedAt, updatedTS int64
		var deleted int
		if err := rows.Scan(&id, &date, &dayID, &hash, &startedAt, &finishedAt,
			&deleted, &note, &updatedTS, &updatedBy); err != nil {
			t.Fatalf("прочитать тренировку: %v", err)
		}
		fmt.Fprintf(&b, "session %s date=%s day=%s prog=%s start=%d finish=%d del=%d note=%q uts=%d uby=%s\n",
			id, date, dayID, hash[:4], startedAt, finishedAt, deleted, note, updatedTS, updatedBy)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("выбрать тренировки: %v", err)
	}

	setRows, err := s.db.R.QueryContext(ctx,
		`SELECT st.session_id, st.exercise_id, st.idx, st.done,
		        COALESCE(CAST(st.weight AS TEXT), 'null'), COALESCE(st.reps, 'null'),
		        st.deleted, st.updated_ts, st.updated_by
		 FROM sets st JOIN sessions se ON se.id = st.session_id
		 WHERE se.user_id = ?
		 ORDER BY st.session_id, st.exercise_id, st.idx`, userID)
	if err != nil {
		t.Fatalf("выбрать подходы: %v", err)
	}
	defer setRows.Close()
	for setRows.Next() {
		var sessionID, exerciseID, weight, reps, updatedBy string
		var idx, updatedTS int64
		var done, deleted int
		if err := setRows.Scan(&sessionID, &exerciseID, &idx, &done, &weight, &reps,
			&deleted, &updatedTS, &updatedBy); err != nil {
			t.Fatalf("прочитать подход: %v", err)
		}
		fmt.Fprintf(&b, "set %s/%s/%d done=%d w=%s reps=%s del=%d uts=%d uby=%s\n",
			sessionID, exerciseID, idx, done, weight, reps, deleted, updatedTS, updatedBy)
	}
	if err := setRows.Err(); err != nil {
		t.Fatalf("выбрать подходы: %v", err)
	}

	return b.String()
}

func countRows(t *testing.T, s *Store, table string) int {
	t.Helper()
	var n int
	if err := s.db.R.QueryRowContext(context.Background(),
		"SELECT count(*) FROM "+table).Scan(&n); err != nil {
		t.Fatalf("посчитать %s: %v", table, err)
	}
	return n
}

func statuses(results []OpResult) []OpStatus {
	out := make([]OpStatus, len(results))
	for i, r := range results {
		out[i] = r.Status
	}
	return out
}

// workout is a typical workout: a start, five sets, a finish.
func workout(session string, firstOp int, hash string) []Op {
	return workoutAt(session, firstOp, hash, 0)
}

// workoutAt shifts a whole workout in time. Needed where there are several workouts:
// they must not overlap, or the "a workout cannot run past the start of the next one"
// rule fires and pulls the data in.
func workoutAt(session string, firstOp int, hash string, offset int64) []Op {
	return []Op{
		opStart(firstOp, session, "d1", at(offset), at(offset), hash),
		opSet(firstOp+1, session, "bench_bb", 0, true, ptr(80.0), ptr("5"), at(offset+60)),
		opSet(firstOp+2, session, "bench_bb", 1, true, ptr(80.0), ptr("5"), at(offset+180)),
		opSet(firstOp+3, session, "bench_bb", 2, true, ptr(82.5), ptr("4"), at(offset+300)),
		opSet(firstOp+4, session, "plank", 0, true, nil, ptr("40с"), at(offset+420)),
		opSet(firstOp+5, session, "plank", 1, true, nil, ptr("40с"), at(offset+480)),
		opFinish(firstOp+6, session, at(offset+600), at(offset+600)),
	}
}

// --- idempotence -----------------------------------------------------------

func TestBatchIsIdempotent(t *testing.T) {
	s := newTestStore(t)
	userID := seedUser(t, s, "igor", programA)
	ops := workout(uuidN(100), 1, programA)

	first := apply(t, s, userID, "phone", ops)
	for i, r := range first {
		if r.Status != StatusApplied {
			t.Fatalf("операция %d: статус %s, ожидался applied (%s)", i, r.Status, r.Reason)
		}
	}
	before := dumpState(t, s, userID)

	second := apply(t, s, userID, "phone", ops)
	for i, r := range second {
		if r.Status != StatusDuplicate {
			t.Errorf("повтор операции %d: статус %s, ожидался duplicate", i, r.Status)
		}
	}
	if after := dumpState(t, s, userID); after != before {
		t.Errorf("повторный батч изменил состояние:\n--- было ---\n%s\n--- стало ---\n%s", before, after)
	}
}

// --- order independence -----------------------------------------------------

// A workout cannot be recorded before it starts: a set.upsert against a nonexistent
// workout is rejected deliberately, because inventing data is worse than refusing.
// So the real property is what gets checked: any order of operations that does not break
// that causal link yields the same final state.
func TestReorderingConverges(t *testing.T) {
	build := func() (setup, rest []Op) {
		s1, s2 := uuidN(100), uuidN(200)
		setup = []Op{
			opStart(1, s1, "d1", at(0), at(0), programA),
			opFinish(2, s1, at(600), at(600)),
			opStart(3, s2, "d2", at(700), at(700), programA),
		}
		rest = []Op{
			opSet(10, s1, "bench_bb", 0, true, ptr(80.0), ptr("5"), at(60)),
			opSet(11, s1, "bench_bb", 0, true, ptr(82.5), ptr("5"), at(120)), // overwrite of the same key
			opSet(12, s1, "bench_bb", 1, true, ptr(80.0), ptr("5"), at(180)),
			opSet(13, s1, "plank", 0, true, nil, ptr("40с"), at(240)),
			opSet(14, s2, "row_bb", 0, true, ptr(60.0), ptr("8"), at(760)),
			opSet(15, s2, "row_bb", 1, false, ptr(60.0), nil, at(820)),
			opSet(16, s2, "pullup", 0, true, nil, ptr("8"), at(880)),
			opFinish(17, s2, at(1200), at(1200)),
			opFinish(18, s2, at(1500), at(1500)), // a later finish must not win
		}
		return
	}

	var want string
	rng := rand.New(rand.NewSource(20260728))

	for attempt := 0; attempt < 12; attempt++ {
		s := newTestStore(t)
		userID := seedUser(t, s, "igor", programA)

		setup, rest := build()
		apply(t, s, userID, "phone", setup)

		rng.Shuffle(len(rest), func(i, j int) { rest[i], rest[j] = rest[j], rest[i] })
		apply(t, s, userID, "phone", rest)

		got := dumpState(t, s, userID)
		if attempt == 0 {
			want = got
			continue
		}
		if got != want {
			t.Fatalf("порядок %d дал другое состояние:\n--- эталон ---\n%s\n--- получено ---\n%s",
				attempt, want, got)
		}
	}
}

func TestBatchSplittingDoesNotMatter(t *testing.T) {
	whole := newTestStore(t)
	wholeUser := seedUser(t, whole, "igor", programA)
	apply(t, whole, wholeUser, "phone", workout(uuidN(100), 1, programA))
	want := dumpState(t, whole, wholeUser)

	rng := rand.New(rand.NewSource(1))
	for attempt := 0; attempt < 8; attempt++ {
		split := newTestStore(t)
		splitUser := seedUser(t, split, "igor", programA)

		ops := workout(uuidN(100), 1, programA)
		for len(ops) > 0 {
			n := 1 + rng.Intn(len(ops))
			apply(t, split, splitUser, "phone", ops[:n])
			ops = ops[n:]
		}

		if got := dumpState(t, split, splitUser); got != want {
			t.Fatalf("разбиение %d дало другое состояние:\n--- одним батчем ---\n%s\n--- по частям ---\n%s",
				attempt, want, got)
		}
	}
}

// --- atomicity ---------------------------------------------------------------

// The trigger produces a genuine database error mid-batch — something an invalid
// operation cannot stand in for, because that one is rejected individually and by design.
func TestBatchIsAtomicOnDatabaseError(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	userID := seedUser(t, s, "igor", programA)

	if _, err := s.db.W.ExecContext(ctx,
		`CREATE TRIGGER boom BEFORE INSERT ON sets WHEN NEW.exercise_id = 'plank'
		 BEGIN SELECT RAISE(FAIL, 'подстроенный отказ базы'); END`); err != nil {
		t.Fatalf("создать триггер: %v", err)
	}

	ops := workout(uuidN(100), 1, programA)
	if _, err := s.ApplyBatch(ctx, userID, "phone", ops, testNow); err == nil {
		t.Fatal("отказ базы не вернул ошибку")
	}

	// Everything has to roll back — both the writes and the idempotence ledger — or a
	// retry of the batch would get duplicate for operations that never actually applied.
	if n := countRows(t, s, "sessions"); n != 0 {
		t.Errorf("после отката осталось тренировок: %d", n)
	}
	if n := countRows(t, s, "sets"); n != 0 {
		t.Errorf("после отката осталось подходов: %d", n)
	}
	if n := countRows(t, s, "applied_ops"); n != 0 {
		t.Errorf("после отката осталось записей в журнале: %d", n)
	}

	if _, err := s.db.W.ExecContext(ctx, `DROP TRIGGER boom`); err != nil {
		t.Fatalf("убрать триггер: %v", err)
	}

	// A retry of the same batch has to go through in full and exactly once.
	for i, r := range apply(t, s, userID, "phone", ops) {
		if r.Status != StatusApplied {
			t.Errorf("повтор после отказа, операция %d: статус %s (%s)", i, r.Status, r.Reason)
		}
	}
	if n := countRows(t, s, "sets"); n != 5 {
		t.Errorf("подходов после повтора: %d, ожидалось 5", n)
	}
}

// --- the poison-message rule -------------------------------------------------

func TestBadOpDoesNotSinkTheBatch(t *testing.T) {
	s := newTestStore(t)
	userID := seedUser(t, s, "igor", programA)
	session := uuidN(100)

	ops := workout(session, 1, programA)
	poison := opSet(50, session, "ЖИМ ЛЁЖА", 0, true, nil, nil, at(90)) // id breaks the format
	// Put the broken operation in the middle: if the whole batch is rejected, the tail is lost.
	ops = append(ops[:3], append([]Op{poison}, ops[3:]...)...)

	results := apply(t, s, userID, "phone", ops)

	var rejectedCount, appliedCount int
	for _, r := range results {
		switch r.Status {
		case StatusRejected:
			rejectedCount++
		case StatusApplied:
			appliedCount++
		}
	}
	if rejectedCount != 1 {
		t.Errorf("отклонено операций: %d, ожидалась 1", rejectedCount)
	}
	if appliedCount != len(ops)-1 {
		t.Errorf("применено операций: %d, ожидалось %d", appliedCount, len(ops)-1)
	}

	// The tail of the batch after the broken operation has to be applied.
	if n := countRows(t, s, "sets"); n != 5 {
		t.Errorf("подходов: %d, ожидалось 5 — операции после битой потерялись", n)
	}
	// A rejected operation must not stay in the ledger: otherwise a retry of it would get
	// duplicate instead of an honest reason for the refusal.
	if n := countRows(t, s, "applied_ops"); n != len(ops)-1 {
		t.Errorf("записей в журнале: %d, ожидалось %d", n, len(ops)-1)
	}
}

func TestValidationRejects(t *testing.T) {
	s := newTestStore(t)
	userID := seedUser(t, s, "igor", programA)
	session := uuidN(100)
	apply(t, s, userID, "phone", []Op{opStart(1, session, "d1", at(0), at(0), programA)})

	cases := []struct {
		name string
		op   Op
		want string
	}{
		{"пустой op_id", Op{TS: at(0), Type: OpSetUpsert, SessionID: session}, "пустой op_id"},
		{"op_id не UUID", Op{OpID: "abc", TS: at(0), Type: OpSetUpsert, SessionID: session}, "не UUID"},
		{"нет ts", Op{OpID: uuidN(900), Type: OpSetUpsert, SessionID: session}, "ts не задан"},
		{"session_id не UUID", Op{OpID: uuidN(901), TS: at(0), Type: OpSetUpsert, SessionID: "x"}, "session_id не UUID"},
		{"неизвестный тип", Op{OpID: uuidN(902), TS: at(0), Type: "set.explode", SessionID: session}, "неизвестный тип"},
		{
			"нет idx",
			Op{OpID: uuidN(903), TS: at(0), Type: OpSetUpsert, SessionID: session,
				ExerciseID: "bench_bb", Done: ptr(true)},
			"не задан idx",
		},
		{
			"нет done",
			Op{OpID: uuidN(904), TS: at(0), Type: OpSetUpsert, SessionID: session,
				ExerciseID: "bench_bb", Idx: ptr(int64(0))},
			"не задан done",
		},
		{
			"дикий вес",
			opSet(905, session, "bench_bb", 0, true, ptr(99999.0), nil, at(0)),
			"вне разумных пределов",
		},
		{
			"кривая дата",
			Op{OpID: uuidN(906), TS: at(0), Type: OpSessionStart, SessionID: uuidN(101),
				Date: "28.07.2026", DayID: "d1", StartedAt: at(0), ProgramHash: programA},
			"ГГГГ-ММ-ДД",
		},
		{
			"хеш программы не sha256",
			Op{OpID: uuidN(907), TS: at(0), Type: OpSessionStart, SessionID: uuidN(102),
				Date: "2026-07-28", DayID: "d1", StartedAt: at(0), ProgramHash: "короткий"},
			"sha256",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := apply(t, s, userID, "phone", []Op{tc.op})
			if res[0].Status != StatusRejected {
				t.Fatalf("статус %s, ожидался rejected", res[0].Status)
			}
			if !strings.Contains(res[0].Reason, tc.want) {
				t.Fatalf("причина %q не содержит %q", res[0].Reason, tc.want)
			}
		})
	}
}

// --- skewed clocks ----------------------------------------------------------

func TestFutureClockIsClamped(t *testing.T) {
	s := newTestStore(t)
	userID := seedUser(t, s, "igor", programA)
	session := uuidN(100)

	tenYears := testNow.AddDate(10, 0, 0).UnixMilli()
	apply(t, s, userID, "phone", []Op{
		opStart(1, session, "d1", at(0), at(0), programA),
		opSet(2, session, "bench_bb", 0, true, ptr(80.0), ptr("5"), tenYears),
	})

	var updatedTS int64
	if err := s.db.R.QueryRowContext(context.Background(),
		`SELECT updated_ts FROM sets WHERE session_id = ?`, session).Scan(&updatedTS); err != nil {
		t.Fatalf("прочитать подход: %v", err)
	}
	maxAllowed := testNow.Add(clampFuture).UnixMilli()
	if updatedTS > maxAllowed {
		t.Fatalf("время %d не зажато, потолок %d", updatedTS, maxAllowed)
	}

	// The key check: a phone whose clock is ten years ahead must not win forever. Five
	// minutes of real time later, an ordinary write has to override it.
	later := testNow.Add(5 * time.Minute)
	if _, err := s.ApplyBatch(context.Background(), userID, "phone", []Op{
		opSet(3, session, "bench_bb", 0, true, ptr(85.0), ptr("6"), later.UnixMilli()),
	}, later); err != nil {
		t.Fatalf("применить более позднюю запись: %v", err)
	}

	var weight float64
	if err := s.db.R.QueryRowContext(context.Background(),
		`SELECT weight FROM sets WHERE session_id = ?`, session).Scan(&weight); err != nil {
		t.Fatalf("прочитать подход: %v", err)
	}
	if weight != 85 {
		t.Fatalf("вес %v, ожидался 85 — запись из будущего осталась непобедимой", weight)
	}
}

// A stale operation must lose, not be promoted: a phone that spent a month offline must
// not overwrite what another device recorded in the meantime.
func TestStaleOpLosesToFresherWrite(t *testing.T) {
	s := newTestStore(t)
	userID := seedUser(t, s, "igor", programA)
	session := uuidN(100)

	apply(t, s, userID, "tablet", []Op{
		opStart(1, session, "d1", at(0), at(0), programA),
		opSet(2, session, "bench_bb", 0, true, ptr(85.0), ptr("6"), testNow.AddDate(0, 0, -10).UnixMilli()),
	})
	// Arrives now, but was recorded a month ago — before the tablet's write.
	apply(t, s, userID, "phone", []Op{
		opSet(3, session, "bench_bb", 0, true, ptr(80.0), ptr("5"), testNow.AddDate(0, -1, 0).UnixMilli()),
	})

	var weight float64
	if err := s.db.R.QueryRowContext(context.Background(),
		`SELECT weight FROM sets WHERE session_id = ?`, session).Scan(&weight); err != nil {
		t.Fatalf("прочитать подход: %v", err)
	}
	if weight != 85 {
		t.Fatalf("вес %v, ожидался 85 — устаревшая запись победила свежую", weight)
	}
}

// --- last-write-wins -------------------------------------------------------

func TestLastWriteWins(t *testing.T) {
	s := newTestStore(t)
	userID := seedUser(t, s, "igor", programA)
	session := uuidN(100)
	apply(t, s, userID, "phone", []Op{opStart(1, session, "d1", at(0), at(0), programA)})

	// The later write beats the earlier one regardless of delivery order.
	apply(t, s, userID, "phone", []Op{opSet(2, session, "bench_bb", 0, true, ptr(90.0), ptr("3"), at(300))})
	apply(t, s, userID, "tablet", []Op{opSet(3, session, "bench_bb", 0, true, ptr(80.0), ptr("5"), at(100))})

	var weight float64
	var updatedBy string
	if err := s.db.R.QueryRowContext(context.Background(),
		`SELECT weight, updated_by FROM sets WHERE session_id = ?`, session).Scan(&weight, &updatedBy); err != nil {
		t.Fatalf("прочитать подход: %v", err)
	}
	if weight != 90 || updatedBy != "phone" {
		t.Fatalf("вес=%v устройство=%s, ожидалось 90/phone", weight, updatedBy)
	}
}

func TestTiesBreakByDevice(t *testing.T) {
	if !newer(100, "phone", 100, "ipad") {
		t.Error("при равном времени должно побеждать устройство с большим идентификатором")
	}
	if newer(100, "ipad", 100, "phone") {
		t.Error("сравнение устройств не симметрично")
	}
	if newer(100, "phone", 100, "phone") {
		t.Error("операция не может быть новее самой себя")
	}
}

// --- two open workouts -------------------------------------------------------

// An offline client may start a second workout without closing the first. The batch is
// not rejected for it — that would lose real data — instead the conflict is resolved
// deterministically and identically under any delivery order.
func TestTwoOpenSessionsResolveTheSameBothOrders(t *testing.T) {
	early, late := uuidN(100), uuidN(200)

	startEarly := opStart(1, early, "d1", at(0), at(0), programA)
	startLate := opStart(2, late, "d2", at(3600), at(3600), programA)
	// A set belonging to the earlier workout: it has to survive the auto-close. The
	// operation is identical in both runs; only the delivery order of the starts changes.
	setEarly := opSet(300, early, "bench_bb", 0, true, ptr(80.0), ptr("5"), at(60))

	run := func(t *testing.T, ops []Op) (string, []OpResult) {
		t.Helper()
		s := newTestStore(t)
		userID := seedUser(t, s, "igor", programA)

		var last []OpResult
		for _, op := range ops {
			last = apply(t, s, userID, "phone", []Op{op})
		}
		return dumpState(t, s, userID), last
	}

	forward, forwardRes := run(t, []Op{startEarly, setEarly, startLate})
	backward, _ := run(t, []Op{startLate, startEarly, setEarly})

	if forward != backward {
		t.Fatalf("порядок повлиял на результат:\n--- ранняя, потом поздняя ---\n%s\n--- поздняя, потом ранняя ---\n%s",
			forward, backward)
	}

	if forwardRes[0].Warning != WarnAutoClosed {
		t.Errorf("предупреждение %q, ожидалось %q", forwardRes[0].Warning, WarnAutoClosed)
	}
	if forwardRes[0].ClosedSessionID != early {
		t.Errorf("закрыта тренировка %s, ожидалась %s", forwardRes[0].ClosedSessionID, early)
	}

	// The one started later stays open, the earlier one is closed at the later one's start
	// time, and its set is still there.
	if !strings.Contains(forward, fmt.Sprintf("session %s date=2026-07-28 day=d1 prog=aaaa start=%d finish=%d",
		early, at(0), at(3600))) {
		t.Errorf("ранняя тренировка закрыта не временем начала поздней:\n%s", forward)
	}
	if !strings.Contains(forward, fmt.Sprintf("session %s date=2026-07-28 day=d2 prog=aaaa start=%d finish=-1",
		late, at(3600))) {
		t.Errorf("поздняя тренировка не осталась открытой:\n%s", forward)
	}
	if !strings.Contains(forward, fmt.Sprintf("set %s/bench_bb/0 done=1 w=80", early)) {
		t.Errorf("подход ранней тренировки потерян при автозакрытии:\n%s", forward)
	}
}

func TestExplicitFinishAndAutoCloseConverge(t *testing.T) {
	first, second := uuidN(100), uuidN(200)

	run := func(t *testing.T, ops []Op) string {
		t.Helper()
		s := newTestStore(t)
		userID := seedUser(t, s, "igor", programA)
		apply(t, s, userID, "phone", []Op{opStart(1, first, "d1", at(0), at(0), programA)})
		for _, op := range ops {
			apply(t, s, userID, "phone", []Op{op})
		}
		return dumpState(t, s, userID)
	}

	autoClose := opStart(2, second, "d2", at(3600), at(3600), programA)
	explicit := opFinish(3, first, at(5400), at(5400))

	// A finish is a monotone merge on the minimum, so an explicit finish and an auto-close
	// converge on one result under any order.
	if a, b := run(t, []Op{autoClose, explicit}), run(t, []Op{explicit, autoClose}); a != b {
		t.Fatalf("порядок повлиял на время завершения:\n--- автозакрытие первым ---\n%s\n--- явное первым ---\n%s", a, b)
	}
}

func TestDeleteAndFinishConverge(t *testing.T) {
	session := uuidN(100)

	run := func(t *testing.T, ops []Op) string {
		t.Helper()
		s := newTestStore(t)
		userID := seedUser(t, s, "igor", programA)
		apply(t, s, userID, "phone", []Op{opStart(1, session, "d1", at(0), at(0), programA)})
		for _, op := range ops {
			apply(t, s, userID, "phone", []Op{op})
		}
		return dumpState(t, s, userID)
	}

	del := opDelete(2, session, at(3000))
	fin := opFinish(3, session, at(1000), at(1000))

	if a, b := run(t, []Op{del, fin}), run(t, []Op{fin, del}); a != b {
		t.Fatalf("удаление и завершение разошлись по порядку:\n--- удаление первым ---\n%s\n--- завершение первым ---\n%s", a, b)
	}
}

func TestDeletedSessionFreesTheOpenSlot(t *testing.T) {
	s := newTestStore(t)
	userID := seedUser(t, s, "igor", programA)
	first, second := uuidN(100), uuidN(200)

	apply(t, s, userID, "phone", []Op{
		opStart(1, first, "d1", at(0), at(0), programA),
		opDelete(2, first, at(60)),
		// The new workout starts EARLIER than the deleted one: if the deleted one still held
		// the slot, auto-close would fire here and the new one would be stored already finished.
		opStart(3, second, "d2", at(30), at(120), programA),
	})

	state := dumpState(t, s, userID)
	if !strings.Contains(state, fmt.Sprintf("session %s date=2026-07-28 day=d2 prog=aaaa start=%d finish=-1", second, at(30))) {
		t.Fatalf("после удаления черновика новая тренировка не осталась открытой:\n%s", state)
	}
}

// --- user isolation ----------------------------------------------------------

func TestUserIsolation(t *testing.T) {
	s := newTestStore(t)
	igor := seedUser(t, s, "igor", programA)
	lena := seedUser(t, s, "lena", programB)

	session := uuidN(100)
	apply(t, s, igor, "phone", workout(session, 1, programA))
	before := dumpState(t, s, igor)

	// Lena references Igor's workout by its direct identifier.
	intrusions := []Op{
		opSet(500, session, "bench_bb", 0, true, ptr(200.0), ptr("1"), at(9000)),
		opFinish(501, session, at(9000), at(9000)),
		opDelete(502, session, at(9000)),
		opStart(503, session, "d1", at(9000), at(9000), programB),
	}
	for _, op := range intrusions {
		res := apply(t, s, lena, "phone-lena", []Op{op})
		if res[0].Status != StatusRejected {
			t.Errorf("операция %s чужой тренировки: статус %s, ожидался rejected", op.Type, res[0].Status)
		}
		if !strings.Contains(res[0].Reason, "другому пользователю") {
			t.Errorf("операция %s: причина %q не про чужого владельца", op.Type, res[0].Reason)
		}
	}

	if after := dumpState(t, s, igor); after != before {
		t.Errorf("данные Игоря изменились:\n--- было ---\n%s\n--- стало ---\n%s", before, after)
	}
	if n := countRows(t, s, "sessions"); n != 1 {
		t.Errorf("тренировок в базе: %d, ожидалась 1", n)
	}
}

func TestProgramIsolation(t *testing.T) {
	s := newTestStore(t)
	igor := seedUser(t, s, "igor", programA)
	lena := seedUser(t, s, "lena", programB)

	// Matching exercise_id between different people's programs is harmless: the queries
	// are filtered by user_id, so the histories do not mix.
	apply(t, s, igor, "phone", []Op{
		opStart(1, uuidN(100), "d1", at(0), at(0), programA),
		opSet(2, uuidN(100), "bench_bb", 0, true, ptr(80.0), ptr("5"), at(60)),
	})
	apply(t, s, lena, "phone-lena", []Op{
		opStart(10, uuidN(200), "d1", at(0), at(0), programB),
		opSet(11, uuidN(200), "bench_bb", 0, true, ptr(35.0), ptr("8"), at(60)),
	})

	if got := dumpState(t, s, igor); !strings.Contains(got, "w=80") || strings.Contains(got, "w=35") {
		t.Errorf("в данных Игоря чужие подходы:\n%s", got)
	}
	if got := dumpState(t, s, lena); !strings.Contains(got, "w=35") || strings.Contains(got, "w=80") {
		t.Errorf("в данных Лены чужие подходы:\n%s", got)
	}

	// Another user's program hash is not accepted even with one's own session_id.
	res := apply(t, s, lena, "phone-lena", []Op{
		opStart(20, uuidN(300), "d1", at(3600), at(3600), programA),
	})
	if res[0].Status != StatusRejected || !strings.Contains(res[0].Reason, "не принадлежит пользователю") {
		t.Errorf("старт по чужой программе: статус %s, причина %q", res[0].Status, res[0].Reason)
	}
}

func TestOpIDCannotBeReusedByAnotherUser(t *testing.T) {
	s := newTestStore(t)
	igor := seedUser(t, s, "igor", programA)
	lena := seedUser(t, s, "lena", programB)

	apply(t, s, igor, "phone", []Op{opStart(1, uuidN(100), "d1", at(0), at(0), programA)})

	res := apply(t, s, lena, "phone-lena", []Op{opStart(1, uuidN(200), "d1", at(0), at(0), programB)})
	if res[0].Status != StatusRejected {
		t.Fatalf("статус %s, ожидался rejected", res[0].Status)
	}
	if n := countRows(t, s, "sessions"); n != 1 {
		t.Errorf("тренировок: %d, ожидалась 1", n)
	}
}

// --- miscellaneous ------------------------------------------------------------

func TestSetForUnknownSessionIsRejected(t *testing.T) {
	s := newTestStore(t)
	userID := seedUser(t, s, "igor", programA)

	res := apply(t, s, userID, "phone", []Op{
		opSet(1, uuidN(999), "bench_bb", 0, true, ptr(80.0), ptr("5"), at(0)),
	})
	if res[0].Status != StatusRejected || !strings.Contains(res[0].Reason, "нет такой тренировки") {
		t.Fatalf("статус %s, причина %q", res[0].Status, res[0].Reason)
	}
}

func TestBatchTooLarge(t *testing.T) {
	s := newTestStore(t)
	userID := seedUser(t, s, "igor", programA)

	ops := make([]Op, MaxOpsPerBatch+1)
	_, err := s.ApplyBatch(context.Background(), userID, "phone", ops, testNow)
	if err == nil {
		t.Fatal("слишком большой батч принят")
	}
}

func TestRepeatedStartDoesNotResurrectFinishedSession(t *testing.T) {
	s := newTestStore(t)
	userID := seedUser(t, s, "igor", programA)
	session := uuidN(100)

	apply(t, s, userID, "phone", []Op{
		opStart(1, session, "d1", at(0), at(0), programA),
		opFinish(2, session, at(600), at(600)),
	})
	// A repeated start with a later timestamp and a different op_id: it updates the start
	// fields, but it has no right to reopen a finished workout.
	apply(t, s, userID, "phone", []Op{opStart(3, session, "d1", at(0), at(1200), programA)})

	var finishedAt *int64
	if err := s.db.R.QueryRowContext(context.Background(),
		`SELECT finished_at FROM sessions WHERE id = ?`, session).Scan(&finishedAt); err != nil {
		t.Fatalf("прочитать тренировку: %v", err)
	}
	if finishedAt == nil {
		t.Fatal("повторный старт воскресил завершённую тренировку")
	}
}

// The writer pool is limited to a single connection, so SQLITE_BUSY on a write is
// structurally impossible. This test keeps that property under watch.
func TestConcurrentBatchesNeverHitBusy(t *testing.T) {
	s := newTestStore(t)
	userID := seedUser(t, s, "igor", programA)

	const goroutines = 8
	var wg sync.WaitGroup
	errs := make(chan error, goroutines)

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			session := uuidN(1000 + g)
			ops := []Op{
				opStart(2000+g*10, session, "d1", at(int64(g)), at(int64(g)), programA),
				opSet(2001+g*10, session, "bench_bb", 0, true, ptr(80.0), ptr("5"), at(int64(g)+60)),
				opFinish(2002+g*10, session, at(int64(g)+600), at(int64(g)+600)),
			}
			if _, err := s.ApplyBatch(context.Background(), userID, fmt.Sprintf("dev%d", g), ops, testNow); err != nil {
				errs <- err
			}
		}(g)
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("параллельный батч: %v", err)
	}
	if n := countRows(t, s, "sessions"); n != goroutines {
		t.Errorf("тренировок: %d, ожидалось %d", n, goroutines)
	}
}

func TestClampBoundaries(t *testing.T) {
	cases := []struct {
		name string
		in   int64
		want int64
	}{
		{"внутри окна", at(0), at(0)},
		{"чуть в будущем", testNow.Add(30 * time.Second).UnixMilli(), testNow.Add(30 * time.Second).UnixMilli()},
		{"далеко в будущем", testNow.Add(time.Hour).UnixMilli(), testNow.Add(clampFuture).UnixMilli()},
		{"далеко в прошлом", testNow.Add(-30 * 24 * time.Hour).UnixMilli(), testNow.Add(-30 * 24 * time.Hour).UnixMilli()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := clampTS(tc.in, testNow); got != tc.want {
				t.Fatalf("зажато в %d, ожидалось %d", got, tc.want)
			}
		})
	}
}

func TestStartsLaterIsDeterministic(t *testing.T) {
	if !startsLater(200, "a", 100, "z") {
		t.Error("побеждать должна тренировка, начатая позже")
	}
	if !startsLater(100, "z", 100, "a") {
		t.Error("при равном времени побеждает больший идентификатор")
	}
	if startsLater(100, "a", 100, "z") {
		t.Error("сравнение не симметрично")
	}
	// Exactly one of the two sides has to win — otherwise conflict resolution loops forever.
	for _, c := range [][4]any{{int64(100), "a", int64(200), "b"}, {int64(100), "a", int64(100), "b"}} {
		aStart, aID := c[0].(int64), c[1].(string)
		bStart, bID := c[2].(int64), c[3].(string)
		if startsLater(aStart, aID, bStart, bID) == startsLater(bStart, bID, aStart, aID) {
			t.Errorf("для (%d,%s) и (%d,%s) победитель не определён однозначно", aStart, aID, bStart, bID)
		}
	}
}

func TestStatusesHelperCoversWholeBatch(t *testing.T) {
	s := newTestStore(t)
	userID := seedUser(t, s, "igor", programA)
	ops := workout(uuidN(100), 1, programA)

	got := statuses(apply(t, s, userID, "phone", ops))
	if len(got) != len(ops) {
		t.Fatalf("результатов %d на %d операций — ответ обязан покрывать весь батч", len(got), len(ops))
	}
	sorted := append([]OpStatus(nil), got...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	for _, st := range sorted {
		if st != StatusApplied {
			t.Fatalf("неожиданный статус %s", st)
		}
	}
}

// A replayed start that moves a workout's start time earlier must pull in the finish of a
// neighbour that now runs past it: no workout continues past the start of the next one,
// and a corrected start is no exception.
func TestMovedStartClampsOverlappingNeighbour(t *testing.T) {
	s := newTestStore(t)
	userID := seedUser(t, s, "igor", programA)
	first, second := uuidN(100), uuidN(200)

	apply(t, s, userID, "phone", []Op{
		opStart(1, first, "d1", at(0), at(0), programA),
		opFinish(2, first, at(3600), at(3600)),
		opStart(3, second, "d2", at(7200), at(7200), programA),
	})
	// A fresher start for the second workout says it actually began inside the first one.
	apply(t, s, userID, "tablet", []Op{
		opStart(4, second, "d2", at(1800), at(9000), programA),
	})

	var finishedAt int64
	if err := s.db.R.QueryRowContext(context.Background(),
		`SELECT finished_at FROM sessions WHERE id = ?`, first).Scan(&finishedAt); err != nil {
		t.Fatalf("прочитать тренировку: %v", err)
	}
	if finishedAt != at(1800) {
		t.Fatalf("первая тренировка заканчивается в %d, ожидалось %d — новое начало соседа не подтянуло её завершение",
			finishedAt, at(1800))
	}
}
