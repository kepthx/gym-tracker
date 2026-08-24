package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

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
	mux.HandleFunc("GET /api/guides", a.requireUser(a.getGuides))

	// Outside /api/ on purpose. The service worker is forbidden from caching /api/ — a stale
	// answer there is indistinguishable from fresh data — while these files are immutable and
	// have to be cached, or the demonstration would need a connection every time. The pattern
	// is more specific than the SPA's "GET /", so it wins the route.
	mux.HandleFunc("GET /media/{name}", a.serveMedia)
	mux.HandleFunc("GET /api/export", a.requireUser(a.getExport))

	// The database file and the diagnostics contain every user's data.
	mux.HandleFunc("GET /api/admin/backup", a.requireAdmin(a.getBackup))
	mux.HandleFunc("GET /api/admin/diag", a.requireAdmin(a.getDiag))
	mux.HandleFunc("POST /api/admin/program/reload", a.requireAdmin(a.postProgramReload))
	mux.HandleFunc("POST /api/admin/guides/reload", a.requireAdmin(a.postGuidesReload))
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

	// The snapshot is immutable, so its hash is a perfect ETag.
	writeHashed(w, r, snapshot.Hash, programResponse{
		Hash:    snapshot.Hash,
		Program: json.RawMessage(snapshot.Canonical),
	})
}

type guidesResponse struct {
	Hash   string          `json:"hash"`
	Guides json.RawMessage `json:"guides"`
}

// getGuides returns the exercise technique reference — the same one for everybody.
//
// Guides are keyed by exercise_id rather than by program hash, and an exercise id is
// forever, so this answer stays correct for a workout recorded against a program that has
// since been replaced.
func (a *API) getGuides(w http.ResponseWriter, r *http.Request) {
	set := a.guides.Load()

	// The set is immutable until an admin reload replaces it wholesale, so its hash is a
	// perfect ETag.
	writeHashed(w, r, set.Hash, guidesResponse{
		Hash:   set.Hash,
		Guides: json.RawMessage(set.Canonical),
	})
}

// writeHashed answers a conditional GET for an immutable, content-addressed body: a repeat
// request returns 304 and spends no traffic at the gym.
//
// The quoting convention lives here rather than in each handler because the header and the
// comparison have to agree, and a disagreement fails silently — as a 304 that never happens,
// i.e. as the full body shipping on every launch rather than as an error.
func writeHashed(w http.ResponseWriter, r *http.Request, hash string, body any) {
	etag := `"` + hash + `"`
	w.Header().Set("ETag", etag)
	if etagMatches(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	writeJSON(w, http.StatusOK, body)
}

// etagMatches is the If-None-Match comparison from RFC 9110 §13.1.2: a comma-separated list,
// "*", and the weak comparison function.
//
// Exact string equality is not enough in this deployment. Anything in front of the process —
// nginx with gzip on, for one — may hand the client back W/"…" for the tag it was given, and
// treating that as a miss means the whole reference is re-sent on every check.
func etagMatches(header, etag string) bool {
	if header == "" {
		return false
	}
	for _, candidate := range strings.Split(header, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" || strings.TrimPrefix(candidate, "W/") == etag {
			return true
		}
	}
	return false
}
