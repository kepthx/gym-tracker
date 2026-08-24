package guide

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kepthx/gym-tracker/internal/program"
)

const validFile = `{
  "version": 1,
  "exercises": {
    "squat_bb": {
      "summary": "Штанга на спине, таз ниже колен.",
      "cues": ["Гриф на задних дельтах", "Колени по линии носков"],
      "mistakes": ["Пятки отрываются от пола"],
      "video": {"youtube_id": "7Yg2YVNdd8c", "start_sec": 42, "title": "Присед", "author": "Кто-то"}
    },
    "plank": {
      "summary": "Тело прямой линией от пяток до макушки.",
      "cues": ["Локти под плечами"]
    }
  }
}`

func TestParseValid(t *testing.T) {
	set, err := Parse("test.json", []byte(validFile))
	if err != nil {
		t.Fatalf("разбор: %v", err)
	}
	if len(set.File.Exercises) != 2 {
		t.Fatalf("упражнений %d, ожидалось 2", len(set.File.Exercises))
	}

	squat := set.File.Exercises["squat_bb"]
	if squat.Video == nil || squat.Video.YouTubeID != "7Yg2YVNdd8c" || squat.Video.StartSec != 42 {
		t.Fatalf("видео разобрано неверно: %+v", squat.Video)
	}
	// A guide without a video is normal: not every exercise needs one.
	if set.File.Exercises["plank"].Video != nil {
		t.Fatal("у планки не было видео, а оно появилось")
	}
	if set.Hash == "" || len(set.Canonical) == 0 {
		t.Fatal("пустой хеш или канонический вид")
	}
}

// Reformatting the file must not change the ETag, or every whitespace fix would cost the
// phone a full re-download of the reference.
func TestHashSurvivesReformatting(t *testing.T) {
	first, err := Parse("test.json", []byte(validFile))
	if err != nil {
		t.Fatalf("разбор: %v", err)
	}

	var reshaped any
	if err := json.Unmarshal([]byte(validFile), &reshaped); err != nil {
		t.Fatalf("перечитать: %v", err)
	}
	compact, err := json.Marshal(reshaped)
	if err != nil {
		t.Fatalf("сжать: %v", err)
	}

	second, err := Parse("test.json", compact)
	if err != nil {
		t.Fatalf("разбор сжатого: %v", err)
	}
	if first.Hash != second.Hash {
		t.Fatalf("хеш изменился от переформатирования: %s != %s", first.Hash, second.Hash)
	}
}

func TestParseRejects(t *testing.T) {
	cases := []struct {
		name string
		file string
		want string
	}{
		{"чужая версия", `{"version":2,"exercises":{}}`, "version=2"},
		{"неизвестное поле", `{"version":1,"exercises":{},"note":"x"}`, "не разбирается"},
		{"плохой id", `{"version":1,"exercises":{"Squat BB":{"summary":"с","cues":["к"]}}}`, "id не подходит"},
		{"пустое summary", `{"version":1,"exercises":{"squat_bb":{"summary":"  ","cues":["к"]}}}`, "пустое summary"},
		{"нет техники", `{"version":1,"exercises":{"squat_bb":{"summary":"с","cues":[]}}}`, "нет ни одного пункта"},
		{"пустой пункт", `{"version":1,"exercises":{"squat_bb":{"summary":"с","cues":["к",""]}}}`, "пустой пункт техники 2"},
		{"пустая ошибка", `{"version":1,"exercises":{"squat_bb":{"summary":"с","cues":["к"],"mistakes":[" "]}}}`, "пустая ошибка 1"},
		{
			"пустой title у видео",
			`{"version":1,"exercises":{"squat_bb":{"summary":"с","cues":["к"],"video":{"youtube_id":"7Yg2YVNdd8c","title":""}}}}`,
			"пустой title",
		},
		{
			"отрицательный start_sec",
			`{"version":1,"exercises":{"squat_bb":{"summary":"с","cues":["к"],"video":{"youtube_id":"7Yg2YVNdd8c","title":"т","start_sec":-1}}}}`,
			"start_sec=-1",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Parse("test.json", []byte(c.file))
			if err == nil {
				t.Fatal("ошибки нет, а должна быть")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("в ошибке нет %q:\n%v", c.want, err)
			}
		})
	}
}

// The YouTube id rule is implemented twice — here and in web/src/ui/youtube.ts — and the
// value ends up in the src of an iframe. Code cannot be shared between Go and TypeScript, but
// the truth can, exactly as testdata/merge_cases.json does it for the merge rules: one table
// read by both sides means a one-sided edit fails on the other side.
func TestSharedYouTubeIDTable(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "youtube_ids.json"))
	if err != nil {
		t.Fatalf("прочитать таблицу истины: %v", err)
	}
	var table struct {
		Accepted []string `json:"accepted"`
		Rejected []string `json:"rejected"`
	}
	if err := json.Unmarshal(raw, &table); err != nil {
		t.Fatalf("разобрать таблицу истины: %v", err)
	}
	if len(table.Accepted) == 0 || len(table.Rejected) == 0 {
		t.Fatal("таблица истины пуста — тест ничего не проверяет")
	}

	fileWith := func(id string) []byte {
		return []byte(`{"version":1,"exercises":{"squat_bb":{"summary":"с","cues":["к"],"video":{"youtube_id":` +
			mustQuote(id) + `,"title":"т","author":"а"}}}}`)
	}

	for _, id := range table.Accepted {
		if _, err := Parse("test.json", fileWith(id)); err != nil {
			t.Errorf("youtube_id %q отвергнут, а должен приниматься: %v", id, err)
		}
	}
	for _, id := range table.Rejected {
		if _, err := Parse("test.json", fileWith(id)); err == nil {
			t.Errorf("youtube_id %q принят, а не должен", id)
		}
	}
}

// Every problem at once: fixing a file one error per restart is a poor way to spend an evening.
func TestValidationCollectsEveryProblem(t *testing.T) {
	file := `{"version":9,"exercises":{"squat_bb":{"summary":"","cues":[]}}}`
	_, err := Parse("test.json", []byte(file))
	if err == nil {
		t.Fatal("ошибки нет, а должна быть")
	}
	for _, want := range []string{"version=9", "пустое summary", "нет ни одного пункта"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("в ошибке нет %q:\n%v", want, err)
		}
	}
}

func TestLoadMissingFileIsNotAnError(t *testing.T) {
	set, err := Load(filepath.Join(t.TempDir(), "нет-такого.json"))
	if err != nil {
		t.Fatalf("отсутствующий файл стал ошибкой: %v", err)
	}
	if len(set.File.Exercises) != 0 {
		t.Fatalf("упражнений %d, ожидалось 0", len(set.File.Exercises))
	}
	if set.Hash == "" {
		t.Fatal("у пустого набора нет хеша — ETag не с чем сравнивать")
	}
}

func TestLoadBrokenFileFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "exercises.json")
	if err := os.WriteFile(path, []byte(`{"version":1,`), 0o644); err != nil {
		t.Fatalf("записать файл: %v", err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("битый файл загрузился")
	}
}

// The guides shipped with the repository have to parse, and every exercise in every program
// has to have one: a card with nothing to expand is a silent hole in the reference.
//
// The programs are read through program.LoadDir rather than one hardcoded file parsed into a
// local struct. A hand-copied idea of the program's shape would fill zero days without
// erroring if the format ever moved exercises, and the test would then report full coverage
// over nothing — the exact silent hole it exists to prevent. Walking the directory also means
// a second user's program is covered the day it is added.
func TestShippedGuidesCoverEveryProgram(t *testing.T) {
	set, err := Load(filepath.Join("..", "..", "guides", "exercises.json"))
	if err != nil {
		t.Fatalf("справочник репозитория не грузится: %v", err)
	}

	snapshots, err := program.LoadDir(filepath.Join("..", "..", "programs"))
	if err != nil {
		t.Fatalf("программы репозитория не грузятся: %v", err)
	}
	if len(snapshots) == 0 {
		t.Fatal("в programs/ нет ни одной программы — тест ничего не проверяет")
	}

	for username, snapshot := range snapshots {
		for _, day := range snapshot.Program.Days {
			for _, ex := range day.Exercises {
				if _, ok := set.File.Exercises[ex.ID]; !ok {
					t.Errorf("нет справки для упражнения %q (программа %s)", ex.ID, username)
				}
			}
		}
	}
}

func mustQuote(s string) string {
	quoted, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(quoted)
}
