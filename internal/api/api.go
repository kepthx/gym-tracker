// Package api is the HTTP layer: routes, request parsing, response codes.
package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

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
}

func New(deps Deps) *API {
	if deps.DebugAuth {
		slog.Warn("включена заглушка аутентификации — только для разработки")
	}
	return &API{
		store:    deps.Store,
		tokenTTL: deps.TokenTTL,

		backups:        deps.Backups,
		dbPath:         deps.DBPath,
		version:        deps.Version,
		startedAt:      time.Now(),
		reloadPrograms: deps.ReloadPrograms,
		// Five attempts in a row, then one every ten seconds. Someone who forgot their
		// password will not notice the difference; brute force becomes pointless.
		loginLimiter: newIPLimiter(10*time.Second, 5),
		debugAuth:    deps.DebugAuth,
		loginDelay:   defaultLoginDelay,
	}
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
