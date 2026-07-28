// Package program loads, validates and hashes training programs.
//
// A program lives in programs/<username>.json — one file per user. Changing a program means
// editing a file, not editing code. A program snapshot is stored in the database and
// addressed by the hash of its canonical form, so reformatting the file does not create a
// new snapshot, and renaming an exercise does not touch history already recorded.
package program

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// idRe constrains day and exercise identifiers. The narrow alphabet is deliberate: ids
// end up in history keys and in URLs, and any ambiguity there is expensive.
var idRe = regexp.MustCompile(`^[a-z0-9_]{1,40}$`)

// SupportedVersion is the version of the program file format.
const SupportedVersion = 1

type Program struct {
	Version int    `json:"version"`
	Name    string `json:"name"`
	Days    []Day  `json:"days"`
}

type Day struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Muscles   string     `json:"muscles"`
	Exercises []Exercise `json:"exercises"`
}

type Exercise struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Scheme      string `json:"scheme"`
	Sets        int    `json:"sets"`
	DefaultReps string `json:"default_reps"`
	Weighted    bool   `json:"weighted"`

	// Groups and RestSec are filled in from the start but unused by the first version of
	// the app: Groups will drive weekly volume per muscle group, and RestSec will feed the
	// rest timer. Adding them after the fact would mean editing every program, and the
	// snapshots in the database are already immutable.
	Groups  []string `json:"groups,omitempty"`
	RestSec int      `json:"rest_sec,omitempty"`
}

// Snapshot is a program together with its canonical form and hash.
type Snapshot struct {
	Hash      string
	Canonical []byte
	Program   *Program
}

// Day returns a day by its identifier.
func (p *Program) Day(id string) (*Day, bool) {
	for i := range p.Days {
		if p.Days[i].ID == id {
			return &p.Days[i], true
		}
	}
	return nil, false
}

// Exercise looks an exercise up by identifier across every day of the program.
func (p *Program) Exercise(id string) (*Exercise, bool) {
	for i := range p.Days {
		for j := range p.Days[i].Exercises {
			if p.Days[i].Exercises[j].ID == id {
				return &p.Days[i].Exercises[j], true
			}
		}
	}
	return nil, false
}

// TotalSets is how many sets a day holds per the program. The denominator of the
// "done/total" counter.
func (d *Day) TotalSets() int {
	total := 0
	for _, e := range d.Exercises {
		total += e.Sets
	}
	return total
}

// ValidationError collects every violation at once: fixing a program one error per
// restart is a poor way to spend an evening.
type ValidationError struct {
	Source   string
	Problems []string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("программа %s не прошла проверку:\n  - %s",
		e.Source, strings.Join(e.Problems, "\n  - "))
}

// Parse parses and validates a program, returning a snapshot with canonical JSON and a hash.
func Parse(source string, raw []byte) (*Snapshot, error) {
	var p Program
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&p); err != nil {
		return nil, &ValidationError{Source: source, Problems: []string{
			fmt.Sprintf("не разбирается как JSON программы: %v", err),
		}}
	}

	if problems := validate(&p); len(problems) > 0 {
		return nil, &ValidationError{Source: source, Problems: problems}
	}

	// Canonicalisation by re-marshalling: field order comes from the struct declaration,
	// and the file's indentation and line breaks are discarded. So reformatting the file
	// does not produce a new snapshot and does not breed rows in programs.
	canonical, err := json.Marshal(&p)
	if err != nil {
		return nil, fmt.Errorf("канонизировать программу %s: %w", source, err)
	}
	sum := sha256.Sum256(canonical)

	return &Snapshot{
		Hash:      hex.EncodeToString(sum[:]),
		Canonical: canonical,
		Program:   &p,
	}, nil
}

func validate(p *Program) []string {
	var problems []string

	if p.Version != SupportedVersion {
		problems = append(problems, fmt.Sprintf(
			"version=%d, поддерживается только %d", p.Version, SupportedVersion))
	}
	if strings.TrimSpace(p.Name) == "" {
		problems = append(problems, "пустое name программы")
	}
	if len(p.Days) == 0 {
		problems = append(problems, "нет ни одного дня")
	}

	dayIDs := make(map[string]int, len(p.Days))
	exerciseIDs := make(map[string]string)

	for i, day := range p.Days {
		where := fmt.Sprintf("день %d (%q)", i+1, day.ID)

		switch {
		case day.ID == "":
			problems = append(problems, where+": пустой id")
		case !idRe.MatchString(day.ID):
			problems = append(problems, fmt.Sprintf(
				"%s: id не подходит под %s", where, idRe))
		}
		if first, dup := dayIDs[day.ID]; dup && day.ID != "" {
			problems = append(problems, fmt.Sprintf(
				"%s: id уже занят днём %d", where, first+1))
		} else if day.ID != "" {
			dayIDs[day.ID] = i
		}

		if strings.TrimSpace(day.Name) == "" {
			problems = append(problems, where+": пустое name")
		}
		if len(day.Exercises) == 0 {
			problems = append(problems, where+": нет упражнений")
		}

		for j, ex := range day.Exercises {
			exWhere := fmt.Sprintf("%s, упражнение %d (%q)", where, j+1, ex.ID)

			switch {
			case ex.ID == "":
				problems = append(problems, exWhere+": пустой id")
			case !idRe.MatchString(ex.ID):
				problems = append(problems, fmt.Sprintf(
					"%s: id не подходит под %s", exWhere, idRe))
			}
			// An exercise id is unique across the whole program, not within a day: all
			// history and every chart hang off it, and one id has to mean exactly one
			// exercise.
			if firstDay, dup := exerciseIDs[ex.ID]; dup && ex.ID != "" {
				problems = append(problems, fmt.Sprintf(
					"%s: id уже занят в дне %q", exWhere, firstDay))
			} else if ex.ID != "" {
				exerciseIDs[ex.ID] = day.ID
			}

			if strings.TrimSpace(ex.Name) == "" {
				problems = append(problems, exWhere+": пустое name")
			}
			if strings.TrimSpace(ex.Scheme) == "" {
				problems = append(problems, exWhere+": пустая scheme")
			}
			if ex.Sets < 1 {
				problems = append(problems, fmt.Sprintf("%s: sets=%d, нужно >= 1", exWhere, ex.Sets))
			}
			if strings.TrimSpace(ex.DefaultReps) == "" {
				// The default has to be right in most cases, or editing the reps turns from
				// an exception into a mandatory step.
				problems = append(problems, exWhere+": пустое default_reps")
			}
			if ex.RestSec < 0 {
				problems = append(problems, fmt.Sprintf("%s: rest_sec=%d, нужно >= 0", exWhere, ex.RestSec))
			}
		}
	}

	return problems
}

// LoadDir reads the programs directory and returns snapshots keyed by username.
// A missing directory is not an error: the service starts, and users without a program see
// "no program set" instead of the day cards.
func LoadDir(dir string) (map[string]*Snapshot, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]*Snapshot{}, nil
		}
		return nil, fmt.Errorf("прочитать каталог программ %s: %w", dir, err)
	}

	result := make(map[string]*Snapshot)
	var failures []string

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".json") {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)

	for _, name := range names {
		full := filepath.Join(dir, name)
		raw, err := os.ReadFile(full)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", name, err))
			continue
		}
		snapshot, err := Parse(full, raw)
		if err != nil {
			failures = append(failures, err.Error())
			continue
		}
		username := strings.TrimSuffix(name, filepath.Ext(name))
		result[username] = snapshot
	}

	// A broken file stops startup: silently handing someone a program other than the one
	// they train by is worse than not coming up at all.
	if len(failures) > 0 {
		return nil, fmt.Errorf("программы в %s не загружены:\n%s", dir, strings.Join(failures, "\n"))
	}
	return result, nil
}
