// Package store provides data access: programs, workouts, sets, synchronisation.
package store

import (
	"github.com/kepthx/gym-tracker/internal/db"
)

type Store struct {
	db *db.DB
	// onSessionFinished is called after a workout is finished. A backup is taken right
	// away if the previous one is already stale: a workout that was just recorded should
	// land on disk rather than wait for the night.
	onSessionFinished func()
}

func New(d *db.DB) *Store {
	return &Store{db: d}
}

func (s *Store) SetOnSessionFinished(fn func()) {
	s.onSessionFinished = fn
}
