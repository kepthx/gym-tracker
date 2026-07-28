package store

import (
	"fmt"
	"regexp"
	"strings"
)

// MaxOpsPerBatch caps the batch size. A workout produces around thirty operations, so
// the limit is deliberately generous and only guards against a junk request.
const MaxOpsPerBatch = 500

type OpType string

const (
	OpSessionStart  OpType = "session.start"
	OpSetUpsert     OpType = "set.upsert"
	OpSessionFinish OpType = "session.finish"
	OpSessionDelete OpType = "session.delete"
)

var (
	uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	dateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	idRe   = regexp.MustCompile(`^[a-z0-9_]{1,40}$`)
)

// Op is a single user action placed on the client's outbox queue.
//
// The flat struct, instead of a per-type union, is deliberate: it parses unambiguously
// from JSON without an intermediate step, and it reads well in logs and while debugging.
// Pointers appear where an absent field has to be distinguished from a zero value.
type Op struct {
	OpID string `json:"op_id"`
	TS   int64  `json:"ts"`
	Seq  int64  `json:"seq"`
	Type OpType `json:"type"`

	SessionID string `json:"session_id"`

	// session.start
	Date        string `json:"date,omitempty"`
	DayID       string `json:"day_id,omitempty"`
	StartedAt   int64  `json:"started_at,omitempty"`
	ProgramHash string `json:"program_hash,omitempty"`

	// set.upsert — always carries the whole row, never a patch: at the moment of the tap
	// the client holds the set's full state, so this is free and removes a class of
	// merge bugs.
	ExerciseID string   `json:"exercise_id,omitempty"`
	Idx        *int64   `json:"idx,omitempty"`
	Done       *bool    `json:"done,omitempty"`
	Weight     *float64 `json:"weight,omitempty"`
	Reps       *string  `json:"reps,omitempty"`

	// session.finish
	FinishedAt int64 `json:"finished_at,omitempty"`
}

type OpStatus string

const (
	StatusApplied   OpStatus = "applied"
	StatusDuplicate OpStatus = "duplicate"
	StatusRejected  OpStatus = "rejected"
)

// OpResult is the fate of a single operation. It is returned per operation rather than
// per batch: one broken operation must not reject nine sound ones and jam the queue.
type OpResult struct {
	OpID   string   `json:"op_id"`
	Status OpStatus `json:"status"`
	// Reason is filled in only for rejected.
	Reason string `json:"reason,omitempty"`
	// Warning — the operation was applied, but with a caveat the user has to be told about.
	Warning string `json:"warning,omitempty"`
	// ClosedSessionID — a workout closed automatically while resolving a conflict.
	ClosedSessionID string `json:"closed_session_id,omitempty"`
}

const WarnAutoClosed = "auto_closed_session"

// validate checks the shape of an operation. Returns an empty string when all is well.
// The check is purely syntactic: there are no database calls here, so that rejecting
// junk costs not a single query.
func (op *Op) validate() string {
	if strings.TrimSpace(op.OpID) == "" {
		return "пустой op_id"
	}
	if !uuidRe.MatchString(op.OpID) {
		return "op_id не UUID"
	}
	if op.TS <= 0 {
		return "ts не задан"
	}
	if !uuidRe.MatchString(op.SessionID) {
		return "session_id не UUID"
	}

	switch op.Type {
	case OpSessionStart:
		if !dateRe.MatchString(op.Date) {
			return "date не в формате ГГГГ-ММ-ДД"
		}
		if !idRe.MatchString(op.DayID) {
			return "day_id не подходит под формат идентификатора"
		}
		if op.StartedAt <= 0 {
			return "started_at не задан"
		}
		if len(op.ProgramHash) != 64 {
			return "program_hash не похож на sha256"
		}
	case OpSetUpsert:
		if !idRe.MatchString(op.ExerciseID) {
			return "exercise_id не подходит под формат идентификатора"
		}
		if op.Idx == nil {
			return "не задан idx"
		}
		if *op.Idx < 0 {
			return "idx отрицательный"
		}
		if op.Done == nil {
			return "не задан done"
		}
		if op.Weight != nil && (*op.Weight < 0 || *op.Weight > 10000) {
			return fmt.Sprintf("вес %v вне разумных пределов", *op.Weight)
		}
		if op.Reps != nil && len([]rune(*op.Reps)) > 20 {
			return "слишком длинные повторы"
		}
	case OpSessionFinish:
		if op.FinishedAt <= 0 {
			return "finished_at не задан"
		}
	case OpSessionDelete:
		// session_id is already checked above, there are no other fields.
	default:
		return fmt.Sprintf("неизвестный тип операции %q", op.Type)
	}
	return ""
}
