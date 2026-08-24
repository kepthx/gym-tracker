// Package confload holds the parts of "a hand-edited JSON file, validated and hashed" that
// training programs and exercise guides have in common.
//
// Both are files a person edits with vi on the server, both have to fail loudly rather than
// half-load, and both are addressed by the hash of their canonical form. Keeping the shared
// rules here is not tidiness: programs and guides are joined by exercise_id, so an id
// alphabet that drifts between the two packages fails at boot on the server rather than in
// the editor, and a fix to how problems are reported would otherwise reach only one of the
// two admin endpoints.
package confload

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// IDRe constrains day, exercise and guide identifiers. The narrow alphabet is deliberate:
// ids end up in history keys and in URLs, and any ambiguity there is expensive. A guide key
// IS an exercise id, which is why one rule governs both files.
var IDRe = regexp.MustCompile(`^[a-z0-9_]{1,40}$`)

// ValidationError collects every violation at once: fixing a file one error per restart is
// a poor way to spend an evening.
type ValidationError struct {
	// Headline is the whole first line — "программа igor.json не прошла проверку". The
	// caller supplies it because the Russian verb agrees with the noun, and the noun
	// differs between a программа and a справочник.
	Headline string
	Source   string
	Problems []string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("%s:\n  - %s", e.Headline, strings.Join(e.Problems, "\n  - "))
}

// Decode parses exactly one JSON value into dst and refuses anything else in the file.
//
// DisallowUnknownFields catches a misspelled key. The trailing-content check catches what a
// streaming decoder otherwise waves through: a merge conflict, a double paste, an edit that
// left a second object behind. Without it json.Decoder stops at the first value and discards
// the rest of the file silently — a half-read file that looks perfectly loaded, which is the
// one outcome both of these formats are meant to make impossible.
func Decode(raw []byte, dst any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return errors.New("после объекта в файле есть ещё данные")
	}
	return nil
}

// Canonical re-marshals a parsed file and hashes it.
//
// Field order comes from the struct declaration and map keys are sorted by encoding/json, so
// the file's indentation and line breaks are discarded: reformatting does not change the
// hash. That is what keeps a program snapshot from breeding rows in the database and a
// guides ETag from missing on every launch.
func Canonical(v any) (hash string, canonical []byte, err error) {
	canonical, err = json.Marshal(v)
	if err != nil {
		return "", nil, err
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), canonical, nil
}
