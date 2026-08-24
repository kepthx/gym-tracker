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
      "media": {"kind": "clip", "credit": "FitnessScape", "license": "CC BY 3.0",
                "source": "https://commons.wikimedia.org/wiki/File:Squat.webm"}
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
	if squat.Media == nil || squat.Media.Kind != KindClip || squat.Media.Credit != "FitnessScape" {
		t.Fatalf("медиа разобрано неверно: %+v", squat.Media)
	}
	// A guide with no demonstration is normal: nothing openly licensed shows every movement.
	if set.File.Exercises["plank"].Media != nil {
		t.Fatal("у планки не было медиа, а оно появилось")
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
			"неизвестный kind",
			`{"version":1,"exercises":{"squat_bb":{"summary":"с","cues":["к"],"media":{"kind":"gif","credit":"к","license":"л","source":"https://x/y"}}}}`,
			`kind="gif"`,
		},
		{
			"пустой credit",
			`{"version":1,"exercises":{"squat_bb":{"summary":"с","cues":["к"],"media":{"kind":"clip","credit":"","license":"л","source":"https://x/y"}}}}`,
			"пустой credit",
		},
		{
			"пустая license",
			`{"version":1,"exercises":{"squat_bb":{"summary":"с","cues":["к"],"media":{"kind":"clip","credit":"к","license":" ","source":"https://x/y"}}}}`,
			"пустой license",
		},
		{
			"credit в пробелах",
			`{"version":1,"exercises":{"squat_bb":{"summary":"с","cues":["к"],"media":{"kind":"clip","credit":" к ","license":"л","source":"https://x/y"}}}}`,
			"окружён пробелами",
		},
		{
			"source не https",
			`{"version":1,"exercises":{"squat_bb":{"summary":"с","cues":["к"],"media":{"kind":"clip","credit":"к","license":"л","source":"http://x/y"}}}}`,
			"нужна https-ссылка",
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
	set, err := Load(filepath.Join(t.TempDir(), "нет-такого.json"), t.TempDir())
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
	if _, err := Load(path, t.TempDir()); err == nil {
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
	set, err := Load(filepath.Join("..", "..", "guides", "exercises.json"),
		filepath.Join("..", "..", "media"))
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

// A guide that promises a demonstration with no file behind it must not load. The guides file
// and the media directory are edited by hand, separately, which is how they drift apart — and
// the result on screen is a broken player, which reads as a broken application.
func TestLoadRejectsMissingMedia(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "exercises.json")
	file := `{"version":1,"exercises":{"squat_bb":{"summary":"с","cues":["к"],` +
		`"media":{"kind":"clip","credit":"к","license":"л","source":"https://x/y"}}}}`
	if err := os.WriteFile(path, []byte(file), 0o644); err != nil {
		t.Fatalf("записать файл: %v", err)
	}

	media := t.TempDir()
	if _, err := Load(path, media); err == nil {
		t.Fatal("справочник с обещанным, но отсутствующим клипом загрузился")
	} else if !strings.Contains(err.Error(), "squat_bb.mp4") {
		t.Fatalf("в ошибке не назван недостающий файл:\n%v", err)
	}

	// With the file in place the same guide loads.
	if err := os.WriteFile(filepath.Join(media, "squat_bb.mp4"), []byte("x"), 0o644); err != nil {
		t.Fatalf("записать клип: %v", err)
	}
	if _, err := Load(path, media); err != nil {
		t.Fatalf("справочник с клипом на месте не загрузился: %v", err)
	}
}

// Frames need both halves: one of the two is a crossfade with nothing to fade to.
func TestLoadRejectsHalfOfAFramePair(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "exercises.json")
	file := `{"version":1,"exercises":{"plank":{"summary":"с","cues":["к"],` +
		`"media":{"kind":"frames","credit":"к","license":"л","source":"https://x/y"}}}}`
	if err := os.WriteFile(path, []byte(file), 0o644); err != nil {
		t.Fatalf("записать файл: %v", err)
	}

	media := t.TempDir()
	if err := os.WriteFile(filepath.Join(media, "plank-0.jpg"), []byte("x"), 0o644); err != nil {
		t.Fatalf("записать кадр: %v", err)
	}
	if _, err := Load(path, media); err == nil {
		t.Fatal("недостаёт второго кадра, а справочник загрузился")
	} else if !strings.Contains(err.Error(), "plank-1.jpg") {
		t.Fatalf("в ошибке не назван недостающий кадр:\n%v", err)
	}
}
