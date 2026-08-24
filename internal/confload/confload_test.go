package confload

import (
	"strings"
	"testing"
)

// A streaming decoder stops at the first JSON value and says nothing about the rest of the
// file. That is exactly how a merge conflict or a double paste gets past a loader whose
// whole job is to refuse a half-read file, so every one of these shapes has to be rejected.
func TestDecodeRefusesTrailingContent(t *testing.T) {
	const good = `{"version":1,"name":"a"}`

	accepted := map[string]string{
		"clean":              good,
		"leading whitespace": "\n\t" + good,
		"trailing newline":   good + "\n",
	}
	for name, raw := range accepted {
		t.Run("принимает: "+name, func(t *testing.T) {
			var dst struct {
				Version int    `json:"version"`
				Name    string `json:"name"`
			}
			if err := Decode([]byte(raw), &dst); err != nil {
				t.Fatalf("отвергнут корректный файл: %v", err)
			}
			if dst.Version != 1 || dst.Name != "a" {
				t.Fatalf("разобрано неверно: %+v", dst)
			}
		})
	}

	rejected := map[string]string{
		"мусор после объекта":  good + " garbage here",
		"файл вставлен дважды": good + "\n" + good,
		"маркер конфликта":     good + "\n<<<<<<< HEAD\n",
		"второй объект":        good + `{"version":1,"name":"b"}`,
		"неизвестное поле":     `{"version":1,"name":"a","nope":1}`,
	}
	for name, raw := range rejected {
		t.Run("отвергает: "+name, func(t *testing.T) {
			var dst struct {
				Version int    `json:"version"`
				Name    string `json:"name"`
			}
			if err := Decode([]byte(raw), &dst); err == nil {
				t.Fatal("файл принят, хотя в нём есть лишнее")
			}
		})
	}
}

func TestValidationErrorListsEveryProblem(t *testing.T) {
	err := &ValidationError{
		Headline: "справочник x.json не прошёл проверку",
		Source:   "x.json",
		Problems: []string{"первая", "вторая"},
	}
	got := err.Error()
	for _, want := range []string{"справочник x.json не прошёл проверку", "- первая", "- вторая"} {
		if !strings.Contains(got, want) {
			t.Errorf("в сообщении нет %q:\n%s", want, got)
		}
	}
}

// Reformatting a file must not change its hash: that is what keeps a program snapshot from
// breeding rows and a guides ETag from missing.
func TestCanonicalIgnoresFormatting(t *testing.T) {
	type file struct {
		Version int               `json:"version"`
		Items   map[string]string `json:"items"`
	}
	var a, b file
	if err := Decode([]byte(`{"version":1,"items":{"x":"1","y":"2"}}`), &a); err != nil {
		t.Fatal(err)
	}
	if err := Decode([]byte("{\n  \"items\": {\n    \"y\": \"2\",\n    \"x\": \"1\"\n  },\n  \"version\": 1\n}"), &b); err != nil {
		t.Fatal(err)
	}
	hashA, _, err := Canonical(&a)
	if err != nil {
		t.Fatal(err)
	}
	hashB, canonicalB, err := Canonical(&b)
	if err != nil {
		t.Fatal(err)
	}
	if hashA != hashB {
		t.Fatalf("хеш зависит от форматирования: %s != %s", hashA, hashB)
	}
	if string(canonicalB) != `{"version":1,"items":{"x":"1","y":"2"}}` {
		t.Fatalf("канонический вид не нормализован: %s", canonicalB)
	}
}
