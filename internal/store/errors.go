package store

import "errors"

var (
	// ErrNotFound — the requested row does not exist.
	ErrNotFound = errors.New("не найдено")
	// ErrNoProgram — the user exists but has no program yet.
	ErrNoProgram = errors.New("программа не задана")
	// ErrForbidden — the object belongs to a different user.
	ErrForbidden = errors.New("чужой объект")
)
