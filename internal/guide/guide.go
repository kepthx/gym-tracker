// Package guide loads and validates the exercise technique guides.
//
// A guide is reference text plus one demonstration for an exercise the user already trains
// by — a short silent clip, or the two end positions of the movement. It
// lives in its own file rather than inside programs/<username>.json on purpose: a program is
// canonicalised and hashed, every session stores the program_hash it was recorded against,
// and history renders from that snapshot. Putting prose into the program would mean a new
// snapshot on every comma fixed in a technique cue. Guides are keyed by exercise_id, which
// is forever, so they need neither hashing nor a database.
package guide

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/kepthx/gym-tracker/internal/confload"
)

// sourceRe keeps `source` an https URL. It is rendered as a link under the demonstration,
// and a licence that requires crediting a page is poorly served by a link to anywhere else.
var sourceRe = regexp.MustCompile(`^https://[^\s"<>]+$`)

// SupportedVersion is the version of the guides file format.
const SupportedVersion = 1

// The two ways an exercise is shown. Openly licensed video of gym exercises barely exists,
// so most exercises get the two end positions of the movement instead of a clip; the client
// crossfades between them.
const (
	KindClip   = "clip"
	KindFrames = "frames"
)

// Media is the demonstration for one exercise.
//
// It carries no file name. The files are derived from the exercise id — <id>.mp4 for a clip,
// <id>-0.jpg and <id>-1.jpg for frames — so nothing in the guides file can name a path, and
// the media handler needs no defence against one.
//
// Credit and License are not decoration: the clips are CC BY and the frames public domain,
// and attribution is a condition of the former. They are validated like any required field.
type Media struct {
	Kind    string `json:"kind"`
	Credit  string `json:"credit"`
	License string `json:"license"`
	Source  string `json:"source"`
}

// Files lists the media files an exercise needs on disk.
func (m *Media) Files(exerciseID string) []string {
	if m.Kind == KindClip {
		return []string{exerciseID + ".mp4"}
	}
	return []string{exerciseID + "-0.jpg", exerciseID + "-1.jpg"}
}

type Guide struct {
	Summary  string   `json:"summary"`
	Cues     []string `json:"cues"`
	Mistakes []string `json:"mistakes,omitempty"`
	Media    *Media   `json:"media,omitempty"`
}

type File struct {
	Version int `json:"version"`
	// Exercises is keyed by exercise_id.
	Exercises map[string]Guide `json:"exercises"`
}

// Set is a guides file together with its canonical form and hash. The hash serves as an
// ETag and nothing else — unlike a program snapshot, nothing in history refers to it.
type Set struct {
	Hash      string
	Canonical []byte
	File      *File
}

// validationError reports every problem in one error. Only the wording lives here: the
// Russian verb agrees with "справочник", which is why confload takes the headline rather
// than composing it.
func validationError(source string, problems []string) error {
	return &confload.ValidationError{
		Headline: fmt.Sprintf("справочник %s не прошёл проверку", source),
		Source:   source,
		Problems: problems,
	}
}

// Empty is the set served when there is no guides file at all: the app works, the exercise
// cards simply have nothing to expand.
func Empty() *Set {
	return mustCanonical(&File{Version: SupportedVersion, Exercises: map[string]Guide{}})
}

// Parse parses and validates a guides file, returning a set with canonical JSON and a hash.
func Parse(source string, raw []byte) (*Set, error) {
	var f File
	if err := confload.Decode(raw, &f); err != nil {
		return nil, validationError(source, []string{
			fmt.Sprintf("не разбирается как JSON справочника: %v", err),
		})
	}
	if f.Exercises == nil {
		f.Exercises = map[string]Guide{}
	}

	if problems := validate(&f); len(problems) > 0 {
		return nil, validationError(source, problems)
	}

	// Canonicalisation by re-marshalling: map keys are sorted by encoding/json, field order
	// comes from the struct declaration, and the file's indentation is discarded. So
	// reformatting the file does not change the ETag and does not cost traffic at the gym.
	hash, canonical, err := confload.Canonical(&f)
	if err != nil {
		return nil, fmt.Errorf("канонизировать справочник %s: %w", source, err)
	}

	return &Set{Hash: hash, Canonical: canonical, File: &f}, nil
}

func validate(f *File) []string {
	var problems []string

	if f.Version != SupportedVersion {
		problems = append(problems, fmt.Sprintf(
			"version=%d, поддерживается только %d", f.Version, SupportedVersion))
	}

	// Sorted, so the list of problems is the same on every run and diffs against itself.
	ids := make([]string, 0, len(f.Exercises))
	for id := range f.Exercises {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for _, id := range ids {
		g := f.Exercises[id]
		where := fmt.Sprintf("упражнение %q", id)

		if !confload.IDRe.MatchString(id) {
			problems = append(problems, fmt.Sprintf("%s: id не подходит под %s", where, confload.IDRe))
		}
		if strings.TrimSpace(g.Summary) == "" {
			problems = append(problems, where+": пустое summary")
		}
		if len(g.Cues) == 0 {
			problems = append(problems, where+": нет ни одного пункта техники")
		}
		for i, cue := range g.Cues {
			if strings.TrimSpace(cue) == "" {
				problems = append(problems, fmt.Sprintf("%s: пустой пункт техники %d", where, i+1))
			}
		}
		for i, mistake := range g.Mistakes {
			if strings.TrimSpace(mistake) == "" {
				problems = append(problems, fmt.Sprintf("%s: пустая ошибка %d", where, i+1))
			}
		}

		if g.Media == nil {
			continue
		}
		if g.Media.Kind != KindClip && g.Media.Kind != KindFrames {
			problems = append(problems, fmt.Sprintf(
				"%s: kind=%q, бывает только %q или %q", where, g.Media.Kind, KindClip, KindFrames))
		}
		// Both reach the screen as "{credit} · {license}", so both are held to the same rule.
		// Surrounding whitespace matters here: it is invisible in the file and visible in
		// the caption under the demonstration.
		for _, field := range []struct{ name, value string }{
			{"credit", g.Media.Credit},
			{"license", g.Media.License},
		} {
			switch {
			case strings.TrimSpace(field.value) == "":
				problems = append(problems, fmt.Sprintf("%s: пустой %s у медиа", where, field.name))
			case field.value != strings.TrimSpace(field.value):
				problems = append(problems, fmt.Sprintf(
					"%s: %s у медиа окружён пробелами — они видны в подписи", where, field.name))
			}
		}
		if !sourceRe.MatchString(g.Media.Source) {
			problems = append(problems, fmt.Sprintf(
				"%s: source=%q — нужна https-ссылка на страницу первоисточника",
				where, g.Media.Source))
		}
	}

	return problems
}

// Load reads the guides file.
//
// A missing file is not an error: guides are optional, and the app is perfectly usable
// without them. A malformed one stops startup, the same rule programs follow — a half-read
// reference is worse than an obvious refusal to come up.
//
// A guide for an exercise no longer in the program, and an exercise with no guide, are both
// normal: programs change every 6–8 weeks, guides much more rarely.
func Load(path, mediaDir string) (*Set, error) {
	set, err := Reload(path, mediaDir)
	if errors.Is(err, fs.ErrNotExist) {
		return Empty(), nil
	}
	return set, err
}

// Reload rereads the guides file for an admin reload, where a missing file is an error.
//
// This is the one way it differs from Load, and the difference matters. At startup an absent
// file means "this deployment ships no guides". At reload it almost always means a rename, a
// typo in GYM_GUIDES, or an edit caught mid-save — and answering 200 to that would swap a
// working reference for an empty one, whose new hash every client then fetches and writes
// over its own copy. The reference would disappear from every device, including the ones
// sitting offline at the gym, which is the single thing this file exists to survive.
func Reload(path, mediaDir string) (*Set, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("прочитать справочник %s: %w", path, err)
	}
	set, err := Parse(path, raw)
	if err != nil {
		return nil, err
	}
	if problems := missingMedia(set.File, mediaDir); len(problems) > 0 {
		return nil, validationError(path, problems)
	}
	return set, nil
}

// missingMedia checks that every demonstration a guide promises is actually on disk.
//
// This is deliberately part of loading rather than a runtime concern: a card that offers a
// clip and then shows a broken player reads as a broken application, and the guides file and
// the media directory are edited by hand, separately, which is exactly how they drift apart.
func missingMedia(f *File, dir string) []string {
	var problems []string

	ids := make([]string, 0, len(f.Exercises))
	for id := range f.Exercises {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for _, id := range ids {
		media := f.Exercises[id].Media
		if media == nil {
			continue
		}
		for _, name := range media.Files(id) {
			if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
				problems = append(problems, fmt.Sprintf(
					"упражнение %q: нет файла %s в %s", id, name, dir))
			}
		}
	}
	return problems
}

// mustCanonical builds the one Set that is constructed in code rather than parsed. It goes
// through the same canonicalisation as Parse so the empty set's hash is the hash Parse would
// produce for an empty file — otherwise the 304 path would silently never fire for a user
// whose guides file has no entries.
func mustCanonical(f *File) *Set {
	hash, canonical, err := confload.Canonical(f)
	if err != nil {
		panic(err)
	}
	return &Set{Hash: hash, Canonical: canonical, File: f}
}
