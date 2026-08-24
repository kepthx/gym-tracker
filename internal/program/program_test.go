package program

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const minimalValid = `{
  "version": 1,
  "name": "Тест",
  "days": [{
    "id": "d1", "name": "Жим", "muscles": "Грудь",
    "exercises": [
      {"id":"bench_bb","name":"Жим лёжа","scheme":"4×5","sets":4,"default_reps":"5","weighted":true},
      {"id":"plank","name":"Планка","scheme":"3×40 с","sets":3,"default_reps":"40с","weighted":false}
    ]
  }]
}`

func TestParseValid(t *testing.T) {
	snapshot, err := Parse("тест", []byte(minimalValid))
	if err != nil {
		t.Fatalf("валидная программа отклонена: %v", err)
	}
	if len(snapshot.Hash) != 64 {
		t.Errorf("хеш %q не похож на sha256", snapshot.Hash)
	}
	if got := snapshot.Program.Days[0].TotalSets(); got != 7 {
		t.Errorf("всего подходов = %d, ожидалось 7", got)
	}
	if _, ok := snapshot.Program.Exercise("plank"); !ok {
		t.Error("упражнение plank не находится по id")
	}
	if _, ok := snapshot.Program.Day("d1"); !ok {
		t.Error("день d1 не находится по id")
	}
}

func TestParseRejects(t *testing.T) {
	cases := []struct {
		name string
		json string
		want string // substring that has to appear in the error text
	}{
		{
			name: "чужая версия формата",
			json: `{"version":2,"name":"x","days":[{"id":"d1","name":"Д","exercises":[
				{"id":"a","name":"A","scheme":"1×1","sets":1,"default_reps":"1"}]}]}`,
			want: "version=2",
		},
		{
			name: "нет дней",
			json: `{"version":1,"name":"x","days":[]}`,
			want: "нет ни одного дня",
		},
		{
			name: "день без упражнений",
			json: `{"version":1,"name":"x","days":[{"id":"d1","name":"Д","exercises":[]}]}`,
			want: "нет упражнений",
		},
		{
			name: "повтор id дня",
			json: `{"version":1,"name":"x","days":[
				{"id":"d1","name":"Д","exercises":[{"id":"a","name":"A","scheme":"1×1","sets":1,"default_reps":"1"}]},
				{"id":"d1","name":"Е","exercises":[{"id":"b","name":"B","scheme":"1×1","sets":1,"default_reps":"1"}]}]}`,
			want: "id уже занят днём",
		},
		{
			// The most expensive violation: one id, two different exercises — a squat and a
			// bench press get glued into one chart and the history becomes meaningless.
			name: "повтор id упражнения между днями",
			json: `{"version":1,"name":"x","days":[
				{"id":"d1","name":"Д","exercises":[{"id":"a","name":"Присед","scheme":"1×1","sets":1,"default_reps":"1"}]},
				{"id":"d2","name":"Е","exercises":[{"id":"a","name":"Жим","scheme":"1×1","sets":1,"default_reps":"1"}]}]}`,
			want: "id уже занят в дне",
		},
		{
			name: "id упражнения не по формату",
			json: `{"version":1,"name":"x","days":[{"id":"d1","name":"Д","exercises":[
				{"id":"Bench-BB","name":"A","scheme":"1×1","sets":1,"default_reps":"1"}]}]}`,
			want: "id не подходит",
		},
		{
			name: "нулевое число подходов",
			json: `{"version":1,"name":"x","days":[{"id":"d1","name":"Д","exercises":[
				{"id":"a","name":"A","scheme":"1×1","sets":0,"default_reps":"1"}]}]}`,
			want: "sets=0",
		},
		{
			name: "пустые повторы по умолчанию",
			json: `{"version":1,"name":"x","days":[{"id":"d1","name":"Д","exercises":[
				{"id":"a","name":"A","scheme":"1×1","sets":1,"default_reps":""}]}]}`,
			want: "пустое default_reps",
		},
		{
			name: "пустое имя упражнения",
			json: `{"version":1,"name":"x","days":[{"id":"d1","name":"Д","exercises":[
				{"id":"a","name":"  ","scheme":"1×1","sets":1,"default_reps":"1"}]}]}`,
			want: "пустое name",
		},
		{
			// A typo in a field name must not pass silently: "sets_" would turn into sets=0
			// and drop the day to zero sets.
			name: "неизвестное поле",
			json: `{"version":1,"name":"x","days":[{"id":"d1","name":"Д","exercises":[
				{"id":"a","name":"A","scheme":"1×1","sets":1,"default_reps":"1","setz":3}]}]}`,
			want: "не разбирается",
		},
		{
			name: "не JSON",
			json: `{`,
			want: "не разбирается",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse("тест", []byte(tc.json))
			if err == nil {
				t.Fatal("ошибки нет, а должна быть")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("текст ошибки %q не содержит %q", err.Error(), tc.want)
			}
		})
	}
}

// The hash is computed over the canonical form rather than the file's bytes:
// reformatting must not breed snapshots in the database.
func TestHashIgnoresFormatting(t *testing.T) {
	compact := `{"version":1,"name":"Тест","days":[{"id":"d1","name":"Жим","muscles":"Грудь",` +
		`"exercises":[{"id":"bench_bb","name":"Жим лёжа","scheme":"4×5","sets":4,` +
		`"default_reps":"5","weighted":true},{"id":"plank","name":"Планка","scheme":"3×40 с",` +
		`"sets":3,"default_reps":"40с","weighted":false}]}]}`

	pretty, err := Parse("pretty", []byte(minimalValid))
	if err != nil {
		t.Fatalf("разобрать с отступами: %v", err)
	}
	flat, err := Parse("compact", []byte(compact))
	if err != nil {
		t.Fatalf("разобрать без отступов: %v", err)
	}
	if pretty.Hash != flat.Hash {
		t.Fatalf("хеши разошлись из-за форматирования: %s против %s", pretty.Hash, flat.Hash)
	}
}

func TestHashChangesWithContent(t *testing.T) {
	base, err := Parse("base", []byte(minimalValid))
	if err != nil {
		t.Fatalf("базовая программа: %v", err)
	}
	renamed, err := Parse("renamed", []byte(strings.Replace(minimalValid, "Жим лёжа", "Жим лёжа узким", 1)))
	if err != nil {
		t.Fatalf("переименованная программа: %v", err)
	}
	if base.Hash == renamed.Hash {
		t.Fatal("переименование упражнения не изменило хеш — история отрисуется новым названием")
	}
}

func TestLoadDir(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "igor.json", minimalValid)
	write(t, dir, "заметка.txt", "не программа")

	got, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("загрузить каталог: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("загружено программ: %d, ожидалась 1", len(got))
	}
	if _, ok := got["igor"]; !ok {
		t.Fatalf("нет программы для igor, есть: %v", keys(got))
	}
}

func TestLoadDirFailsOnBrokenFile(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "igor.json", minimalValid)
	write(t, dir, "lena.json", `{"version":1,"name":"x","days":[]}`)

	if _, err := LoadDir(dir); err == nil {
		t.Fatal("битый файл программы не остановил загрузку")
	}
}

// A missing directory is a normal situation, not a failure: the service has to start.
func TestLoadDirMissingIsEmpty(t *testing.T) {
	got, err := LoadDir(filepath.Join(t.TempDir(), "нет-такого"))
	if err != nil {
		t.Fatalf("отсутствующий каталог дал ошибку: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("загружено программ: %d, ожидалось 0", len(got))
	}
}

// The real program from the repository has to pass the same checks.
func TestRepositoryProgramIsValid(t *testing.T) {
	snapshots, err := LoadDir(filepath.Join("..", "..", "programs"))
	if err != nil {
		t.Fatalf("программы репозитория не загрузились: %v", err)
	}
	snapshot, ok := snapshots["igor"]
	if !ok {
		t.Fatalf("нет programs/igor.json, есть: %v", keys(snapshots))
	}
	if len(snapshot.Program.Days) != 4 {
		t.Errorf("дней в программе: %d, ожидалось 4", len(snapshot.Program.Days))
	}
	for _, day := range snapshot.Program.Days {
		if day.Muscles == "" {
			t.Errorf("день %q без подзаголовка мышечных групп", day.ID)
		}
		for _, ex := range day.Exercises {
			if len(ex.Groups) == 0 {
				t.Errorf("упражнение %q без groups — недельный объём по группам не посчитается", ex.ID)
			}
			if ex.RestSec == 0 {
				t.Errorf("упражнение %q без rest_sec — таймеру отдыха нечего брать", ex.ID)
			}
		}
	}
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatalf("записать %s: %v", name, err)
	}
}

func keys(m map[string]*Snapshot) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestLowerIsBetterDoesNotDisturbExistingHashes pins the omitempty on the field: a program
// that never mentions lower_is_better has to hash exactly as it did before the field was
// added. Otherwise introducing it would silently re-snapshot every existing program and
// detach recorded history from the program it was trained by.
func TestLowerIsBetterDoesNotDisturbExistingHashes(t *testing.T) {
	absent, err := Parse("без флага", []byte(minimalValid))
	if err != nil {
		t.Fatalf("программа без флага отклонена: %v", err)
	}

	explicitFalse := strings.Replace(minimalValid,
		`"weighted":true}`,
		`"weighted":true,"lower_is_better":false}`, 1)
	same, err := Parse("с явным false", []byte(explicitFalse))
	if err != nil {
		t.Fatalf("программа с явным false отклонена: %v", err)
	}
	if same.Hash != absent.Hash {
		t.Errorf("явный lower_is_better=false изменил хеш:\n  без флага %s\n  с флагом  %s",
			absent.Hash, same.Hash)
	}

	explicitTrue := strings.Replace(minimalValid,
		`"weighted":true}`,
		`"weighted":true,"lower_is_better":true}`, 1)
	differs, err := Parse("с true", []byte(explicitTrue))
	if err != nil {
		t.Fatalf("программа с lower_is_better=true отклонена: %v", err)
	}
	if differs.Hash == absent.Hash {
		t.Error("lower_is_better=true обязан давать другой снапшот, а хеш совпал")
	}
	ex, ok := differs.Program.Exercise("bench_bb")
	if !ok || !ex.LowerIsBetter {
		t.Error("флаг не долетел до разобранного упражнения")
	}
}
