package store

import (
	"context"
	"encoding/json"
	"testing"
)

// A backup that has never been restored from is not a backup. This test makes the
// completeness of the export verifiable: if anything is missing, the round trip fails.
func TestExportImportRoundTrip(t *testing.T) {
	ctx := context.Background()

	source := newTestStore(t)
	userID := seedUser(t, source, "igor", programA)

	apply(t, source, userID, "phone", workoutAt(uuidN(100), 1, programA, -2*86400))
	apply(t, source, userID, "phone", workoutAt(uuidN(101), 20, programA, -86400))
	// An unfinished workout and a deleted one have to survive the round trip too.
	apply(t, source, userID, "phone", []Op{
		opStart(40, uuidN(102), "d2", at(0), at(0), programA),
		opSet(41, uuidN(102), "row_bb", 0, true, ptr(60.0), ptr("8"), at(60)),
	})
	apply(t, source, userID, "phone", []Op{
		opStart(50, uuidN(103), "d3", at(-7200), at(-7200), programA),
		opDelete(51, uuidN(103), at(-7100)),
	})

	want := dumpState(t, source, userID)

	data, err := source.Export(ctx, userID, testNow)
	if err != nil {
		t.Fatalf("собрать выгрузку: %v", err)
	}

	// Through JSON rather than directly: this exercises the same path as the "export
	// everything" button.
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("сериализовать выгрузку: %v", err)
	}
	var restored Export
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatalf("разобрать выгрузку: %v", err)
	}

	target := newTestStore(t)
	targetUser := seedUser(t, target, "igor", programA)

	result, err := target.Import(ctx, targetUser, &restored, testNow)
	if err != nil {
		t.Fatalf("влить выгрузку: %v", err)
	}
	if result.Skipped > 0 {
		t.Errorf("пропущено строк при импорте: %d", result.Skipped)
	}

	if got := dumpState(t, target, targetUser); got != want {
		t.Fatalf("после круга состояние отличается:\n--- исходное ---\n%s\n--- восстановленное ---\n%s",
			want, got)
	}
}

// Import has to be repeatable: a restore that was interrupted and started again must
// neither duplicate data nor clobber fresher data.
func TestImportIsIdempotent(t *testing.T) {
	ctx := context.Background()

	source := newTestStore(t)
	userID := seedUser(t, source, "igor", programA)
	apply(t, source, userID, "phone", workout(uuidN(100), 1, programA))

	data, err := source.Export(ctx, userID, testNow)
	if err != nil {
		t.Fatalf("собрать выгрузку: %v", err)
	}

	target := newTestStore(t)
	targetUser := seedUser(t, target, "igor", programA)

	if _, err := target.Import(ctx, targetUser, data, testNow); err != nil {
		t.Fatalf("первый импорт: %v", err)
	}
	first := dumpState(t, target, targetUser)

	if _, err := target.Import(ctx, targetUser, data, testNow); err != nil {
		t.Fatalf("повторный импорт: %v", err)
	}
	if second := dumpState(t, target, targetUser); second != first {
		t.Fatalf("повторный импорт изменил состояние:\n--- было ---\n%s\n--- стало ---\n%s",
			first, second)
	}
}

// Import on top of a non-empty database merges by the same rules as sync and does not
// clobber fresher data.
func TestImportDoesNotOverwriteNewerData(t *testing.T) {
	ctx := context.Background()

	source := newTestStore(t)
	userID := seedUser(t, source, "igor", programA)
	apply(t, source, userID, "phone", []Op{
		opStart(1, uuidN(100), "d1", at(0), at(0), programA),
		opSet(2, uuidN(100), "bench_bb", 0, true, ptr(80.0), ptr("5"), at(60)),
	})
	data, err := source.Export(ctx, userID, testNow)
	if err != nil {
		t.Fatalf("собрать выгрузку: %v", err)
	}

	target := newTestStore(t)
	targetUser := seedUser(t, target, "igor", programA)
	apply(t, target, targetUser, "phone", []Op{
		opStart(1, uuidN(100), "d1", at(0), at(0), programA),
		// A later edit of the same set: an older export has no right to clobber it.
		opSet(2, uuidN(100), "bench_bb", 0, true, ptr(95.0), ptr("3"), at(3600)),
	})

	if _, err := target.Import(ctx, targetUser, data, testNow); err != nil {
		t.Fatalf("импорт: %v", err)
	}

	var weight float64
	if err := target.db.R.QueryRowContext(ctx,
		`SELECT weight FROM sets WHERE session_id = ?`, uuidN(100)).Scan(&weight); err != nil {
		t.Fatalf("прочитать подход: %v", err)
	}
	if weight != 95 {
		t.Fatalf("вес %v — старая выгрузка затёрла более свежую запись", weight)
	}
}

// One user's export must not contain another user's data.
func TestExportIsScopedToUser(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	igor := seedUser(t, s, "igor", programA)
	lena := seedUser(t, s, "lena", programB)

	apply(t, s, igor, "phone", workout(uuidN(100), 1, programA))
	apply(t, s, lena, "phone-lena", workout(uuidN(200), 50, programB))

	data, err := s.Export(ctx, igor, testNow)
	if err != nil {
		t.Fatalf("собрать выгрузку: %v", err)
	}

	if data.User.Username != "igor" {
		t.Errorf("в выгрузке чужой пользователь: %s", data.User.Username)
	}
	for _, session := range data.Sessions {
		if session.ID != uuidN(100) {
			t.Errorf("в выгрузке чужая тренировка %s", session.ID)
		}
	}
	for _, set := range data.Sets {
		if set.SessionID != uuidN(100) {
			t.Errorf("в выгрузке чужой подход из %s", set.SessionID)
		}
	}
	for _, p := range data.Programs {
		if p.Hash != programA {
			t.Errorf("в выгрузке чужая программа %s", p.Hash)
		}
	}
}

// Import must not be a way to reach another user's workouts by direct id.
func TestImportCannotTouchAnotherUsersData(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	igor := seedUser(t, s, "igor", programA)
	lena := seedUser(t, s, "lena", programB)

	apply(t, s, igor, "phone", workout(uuidN(100), 1, programA))
	before := dumpState(t, s, igor)

	stolen, err := s.Export(ctx, igor, testNow)
	if err != nil {
		t.Fatalf("собрать выгрузку: %v", err)
	}

	result, err := s.Import(ctx, lena, stolen, testNow)
	if err != nil {
		t.Fatalf("импорт: %v", err)
	}
	if result.Skipped == 0 {
		t.Error("чужие строки не были пропущены")
	}
	if after := dumpState(t, s, igor); after != before {
		t.Fatalf("данные Игоря изменились:\n--- было ---\n%s\n--- стало ---\n%s", before, after)
	}
}
