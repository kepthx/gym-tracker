package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/kepthx/gym-tracker/internal/store"
)

// Routes attaches the API routes to the given mux.
func (a *API) Routes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/auth/login", a.postLogin)
	mux.HandleFunc("POST /api/auth/logout", a.postLogout)
	mux.HandleFunc("GET /api/auth/me", a.requireUser(a.getMe))

	mux.HandleFunc("POST /api/sync", a.requireUser(a.postSync))
	mux.HandleFunc("GET /api/sync", a.requireUser(a.getSync))
	mux.HandleFunc("GET /api/program", a.requireUser(a.getProgram))
	mux.HandleFunc("GET /api/export", a.requireUser(a.getExport))

	// The database file and the diagnostics contain every user's data.
	mux.HandleFunc("GET /api/admin/backup", a.requireAdmin(a.getBackup))
	mux.HandleFunc("GET /api/admin/diag", a.requireAdmin(a.getDiag))
	mux.HandleFunc("POST /api/admin/program/reload", a.requireAdmin(a.postProgramReload))
}

// Wrap wraps a finished handler in the layers common to the whole service.
func Wrap(h http.Handler) http.Handler {
	return secureHeaders(checkCSRF(h))
}

type programResponse struct {
	Hash    string          `json:"hash"`
	Program json.RawMessage `json:"program"`
}

// getProgram returns the current user's program. Everyone has their own.
func (a *API) getProgram(w http.ResponseWriter, r *http.Request) {
	uid := userID(r)

	snapshot, err := a.store.CurrentProgram(r.Context(), uid)
	switch {
	case errors.Is(err, store.ErrNoProgram):
		writeError(w, http.StatusNotFound, "no_program", "программа не задана")
		return
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "пользователь не найден")
		return
	case err != nil:
		slog.Error("не удалось прочитать программу", "пользователь", uid, "ошибка", err)
		writeError(w, http.StatusInternalServerError, "internal", "не удалось прочитать программу")
		return
	}

	// The snapshot is immutable, so its hash is a perfect ETag: a repeat request returns
	// 304 and spends no traffic at the gym.
	w.Header().Set("ETag", `"`+snapshot.Hash+`"`)
	if match := r.Header.Get("If-None-Match"); match == `"`+snapshot.Hash+`"` {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	writeJSON(w, http.StatusOK, programResponse{
		Hash:    snapshot.Hash,
		Program: json.RawMessage(snapshot.Canonical),
	})
}
