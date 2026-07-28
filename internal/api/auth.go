package api

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/kepthx/gym-tracker/internal/auth"
	"github.com/kepthx/gym-tracker/internal/store"
)

const (
	cookieName = "gt"
	// slideAfter is how long a token has to live before a request extends it. Extending it
	// on every request would mean a database write for every little thing.
	slideAfter = 30 * 24 * time.Hour
	// defaultLoginDelay suppresses the response-time side channel and at the same time caps
	// brute-force throughput independently of the other layers.
	defaultLoginDelay = 300 * time.Millisecond
)

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type userBody struct {
	ID          int64  `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	IsAdmin     bool   `json:"is_admin"`
}

type sessionResponse struct {
	User      userBody `json:"user"`
	ExpiresAt int64    `json:"expires_at"`
}

func (a *API) postLogin(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	ip := clientIP(r)

	// The delay is the same for every outcome, including a nonexistent username: otherwise
	// response timing could be used to enumerate names.
	defer sleepJitter(a.loginDelay)

	if !a.loginLimiter.allow(ip, now) {
		writeRetryAfter(w, time.Minute, "слишком много попыток входа")
		return
	}

	var req loginRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "bad_request", "не разбирается")
		return
	}
	if req.Password == "" {
		writeError(w, http.StatusUnprocessableEntity, "bad_request", "не задан пароль")
		return
	}

	username, err := a.resolveUsername(r, req.Username)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "не удалось определить пользователя")
		return
	}

	lockout, err := a.store.LockoutFor(r.Context(), username, now)
	if err != nil {
		slog.Error("не удалось проверить блокировку", "ошибка", err)
		writeError(w, http.StatusInternalServerError, "internal", "не удалось проверить вход")
		return
	}
	if lockout > 0 {
		writeRetryAfter(w, lockout, "слишком много неудачных попыток")
		return
	}

	user, hash, err := a.store.UserByName(r.Context(), username)
	// A nonexistent username follows the same path as a wrong password: an identical
	// response and an identical delay give away nothing about who exists in the system.
	valid := false
	if err == nil && !user.Disabled {
		valid, _ = auth.VerifyPassword(req.Password, hash)
	} else if err != nil && !errors.Is(err, store.ErrNotFound) {
		slog.Error("не удалось прочитать пользователя", "ошибка", err)
		writeError(w, http.StatusInternalServerError, "internal", "не удалось проверить вход")
		return
	}

	if err := a.store.RecordLoginAttempt(r.Context(), ip, username, valid, now); err != nil {
		slog.Error("не удалось записать попытку входа", "ошибка", err)
	}
	if !valid {
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "неверный пароль")
		return
	}

	raw, expires, err := a.store.CreateToken(r.Context(), user.ID, a.tokenTTL, r.UserAgent(), now)
	if err != nil {
		slog.Error("не удалось выдать токен", "ошибка", err)
		writeError(w, http.StatusInternalServerError, "internal", "не удалось войти")
		return
	}

	setSessionCookie(w, r, raw, a.tokenTTL)
	writeJSON(w, http.StatusOK, sessionResponse{User: toUserBody(user), ExpiresAt: expires.UnixMilli()})
}

// resolveUsername lets the login screen get by with a single password field while there
// is one user. Once there is a second, the client starts sending a name and the schema
// does not change.
func (a *API) resolveUsername(r *http.Request, given string) (string, error) {
	if given != "" {
		return given, nil
	}
	only, err := a.store.OnlyUser(r.Context())
	if errors.Is(err, store.ErrNotFound) {
		// There are no users, or several — a name is required. The empty string goes into
		// the attempt log and matches nobody.
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return only.Username, nil
}

func (a *API) postLogout(w http.ResponseWriter, r *http.Request) {
	if raw := tokenFromRequest(r); raw != "" {
		if err := a.store.DeleteToken(r.Context(), raw); err != nil {
			slog.Error("не удалось отозвать токен", "ошибка", err)
		}
	}
	clearSessionCookie(w, r)
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) getMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, sessionResponse{
		User:      toUserBody(sessionUser(r)),
		ExpiresAt: sessionExpiry(r).UnixMilli(),
	})
}

func setSessionCookie(w http.ResponseWriter, r *http.Request, raw string, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:  cookieName,
		Value: raw,
		Path:  "/",
		// The lifetime is set by the server via Set-Cookie. This matters: ITP in WebKit caps
		// script-writable storage at seven days but leaves a server-set cookie alone, and that
		// is the only way a login survives into next month without a password at the gym.
		MaxAge:   int(ttl.Seconds()),
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
	})
}

func clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
	})
}

// tokenFromRequest accepts a token from either the cookie or the Authorization header.
//
// Storing it twice is insurance for the "a login lasts months" requirement: if one is
// lost, the session continues on the other and the server reissues the missing one.
func tokenFromRequest(r *http.Request) string {
	if c, err := r.Cookie(cookieName); err == nil && c.Value != "" {
		return c.Value
	}
	if header := r.Header.Get("Authorization"); strings.HasPrefix(header, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
	}
	return ""
}

func toUserBody(u *store.User) userBody {
	return userBody{ID: u.ID, Username: u.Username, DisplayName: u.DisplayName, IsAdmin: u.IsAdmin}
}

func writeRetryAfter(w http.ResponseWriter, wait time.Duration, message string) {
	seconds := int(wait.Seconds())
	if seconds < 1 {
		seconds = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	writeError(w, http.StatusTooManyRequests, "rate_limited", message)
}

func clientIP(r *http.Request) string {
	// The app listens to the outside world itself, with no reverse proxy, so X-Forwarded-For
	// headers cannot be trusted: anyone can set them and slip past the limiter.
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func sleepJitter(base time.Duration) {
	jitter, err := rand.Int(rand.Reader, big.NewInt(int64(base/2)))
	if err != nil {
		time.Sleep(base)
		return
	}
	time.Sleep(base + time.Duration(jitter.Int64()))
}
