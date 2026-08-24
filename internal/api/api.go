// Package api is the HTTP layer: routes, request parsing, response codes.
package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/kepthx/gym-tracker/internal/guide"
	"github.com/kepthx/gym-tracker/internal/store"
)

// Deps is what the API receives from outside and does not construct itself.
type Deps struct {
	Store    *store.Store
	TokenTTL time.Duration
	// DebugAuth enables logging in via the X-Debug-User header, for debugging over curl.
	DebugAuth bool
	Backups   Backups
	DBPath    string
	Version   string
	// ReloadPrograms rereads the programs directory and returns who got attached to what.
	ReloadPrograms func(ctx context.Context) (map[string]string, error)
	// Guides is the exercise technique reference served at /api/guides. Never nil in
	// production; New substitutes an empty set when it is.
	Guides *guide.Set
	// ReloadGuides rereads the guides file from disk and returns the fresh set.
	ReloadGuides func() (*guide.Set, error)
}

// Backups is what the API needs to know about backups.
type Backups interface {
	Latest() (string, error)
	LastRun() time.Time
}

type API struct {
	store    *store.Store
	tokenTTL time.Duration
	// debugAuth enables logging in via the X-Debug-User header, for debugging over curl.
	// It comes from an environment variable and is off in a production build.
	debugAuth    bool
	loginLimiter *ipLimiter
	// loginDelay is a field so that tests do not wait a third of a second per attempt.
	loginDelay time.Duration

	backups        Backups
	dbPath         string
	version        string
	startedAt      time.Time
	reloadPrograms func(ctx context.Context) (map[string]string, error)

	// guides is swapped wholesale by the admin reload while requests are being served, so
	// it is read through an atomic pointer rather than a lock: readers never block.
	guides       atomic.Pointer[guide.Set]
	reloadGuides func() (*guide.Set, error)
}

func New(deps Deps) *API {
	if deps.DebugAuth {
		slog.Warn("включена заглушка аутентификации — только для разработки")
	}
	a := &API{
		store:    deps.Store,
		tokenTTL: deps.TokenTTL,

		backups:        deps.Backups,
		dbPath:         deps.DBPath,
		version:        deps.Version,
		startedAt:      time.Now(),
		reloadPrograms: deps.ReloadPrograms,
		reloadGuides:   deps.ReloadGuides,
		// Five attempts in a row, then one every ten seconds. Someone who forgot their
		// password will not notice the difference; brute force becomes pointless.
		loginLimiter: newIPLimiter(10*time.Second, 5),
		debugAuth:    deps.DebugAuth,
		loginDelay:   defaultLoginDelay,
	}
	if deps.Guides != nil {
		a.guides.Store(deps.Guides)
	} else {
		a.guides.Store(guide.Empty())
	}
	return a
}

type errorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	var body errorBody
	body.Error.Code = code
	body.Error.Message = message
	writeJSON(w, status, body)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		// The response has already started going out, too late to change the status — all
		// that is left is to log it.
		slog.Error("не удалось записать ответ", "ошибка", err)
	}
}
