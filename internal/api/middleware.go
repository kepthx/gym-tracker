package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/kepthx/gym-tracker/internal/store"
)

type ctxKey int

const (
	ctxSession ctxKey = iota
	ctxToken
)

func sessionUser(r *http.Request) *store.User {
	s, _ := r.Context().Value(ctxSession).(*store.Session)
	if s == nil {
		return &store.User{}
	}
	return s.User
}

func sessionExpiry(r *http.Request) time.Time {
	s, _ := r.Context().Value(ctxSession).(*store.Session)
	if s == nil {
		return time.Time{}
	}
	return s.ExpiresAt
}

func userID(r *http.Request) int64 { return sessionUser(r).ID }

// requireUser validates the token and puts the session into the context.
func (a *API) requireUser(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session, raw, ok := a.authenticate(w, r)
		if !ok {
			return
		}
		ctx := context.WithValue(r.Context(), ctxSession, session)
		ctx = context.WithValue(ctx, ctxToken, raw)
		next(w, r.WithContext(ctx))
	}
}

// requireAdmin guards everything that reaches beyond one person's data.
// The database file and the diagnostics contain every user's data.
func (a *API) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return a.requireUser(func(w http.ResponseWriter, r *http.Request) {
		if !sessionUser(r).IsAdmin {
			writeError(w, http.StatusForbidden, "forbidden", "недостаточно прав")
			return
		}
		next(w, r)
	})
}

func (a *API) authenticate(w http.ResponseWriter, r *http.Request) (*store.Session, string, bool) {
	if a.debugAuth {
		if session, raw, ok := a.debugSession(r); ok {
			return session, raw, true
		}
	}

	raw := tokenFromRequest(r)
	if raw == "" {
		writeError(w, http.StatusUnauthorized, "unauthorized", "требуется вход")
		return nil, "", false
	}

	now := time.Now()
	session, err := a.store.LookupToken(r.Context(), raw, now)
	if errors.Is(err, store.ErrTokenInvalid) {
		clearSessionCookie(w, r)
		writeError(w, http.StatusUnauthorized, "unauthorized", "требуется вход")
		return nil, "", false
	}
	if err != nil {
		slog.Error("не удалось проверить токен", "ошибка", err)
		writeError(w, http.StatusInternalServerError, "internal", "не удалось проверить вход")
		return nil, "", false
	}

	// Sliding renewal: for an active user the window never expires, so the password is
	// never asked for at the gym. The cookie is reissued along the way — that covers the
	// case where the token only arrived in the Authorization header.
	if now.Sub(session.CreatedAt) > slideAfter {
		if expires, err := a.store.SlideToken(r.Context(), raw, a.tokenTTL, now); err == nil {
			session.ExpiresAt = expires
			session.CreatedAt = now
			setSessionCookie(w, r, raw, a.tokenTTL)
		} else {
			slog.Error("не удалось продлить токен", "ошибка", err)
		}
	}

	return session, raw, true
}

// debugSession is an authentication stub for development and debugging over curl.
// It is enabled only by an environment variable and does nothing in a production build.
func (a *API) debugSession(r *http.Request) (*store.Session, string, bool) {
	id, err := strconv.ParseInt(r.Header.Get("X-Debug-User"), 10, 64)
	if err != nil || id <= 0 {
		return nil, "", false
	}
	return &store.Session{
		User:      &store.User{ID: id, Username: "debug", IsAdmin: true},
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(a.tokenTTL),
	}, "", true
}

// secureHeaders sets the headers a reverse proxy would otherwise have provided.
func secureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		// same-origin sends no referrer off-site at all — including to the video player,
		// which is why the iframe must not override this with a laxer per-element policy.
		h.Set("Referrer-Policy", "same-origin")
		// First-party by default, with exactly one exception, spelled out at frame-src
		// below: no third-party script, style, image, font or connection anywhere.
		h.Set("Content-Security-Policy",
			"default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; "+
				"img-src 'self' data:; connect-src 'self'; base-uri 'none'; "+
				"form-action 'self'; frame-ancestors 'none'; "+
				// The single deliberate exception to the first-party rule: the technique
				// video in an exercise guide. Nothing else is opened up — no script-src,
				// no img-src, no connect-src — because the player is a bare iframe with no
				// YouTube JS API and no thumbnail pulled from ytimg. And the iframe is only
				// created after an explicit tap on play, so a screen that is merely open
				// sends Google nothing at all.
				"frame-src https://www.youtube-nocookie.com")
		if r.TLS != nil {
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

// checkCSRF guards state-changing requests. The token lives in an HttpOnly cookie, so the
// browser attaches it to a request from another site too — that is what has to be cut off.
//
// A client with neither Fetch Metadata headers nor an Origin is not a browser (curl, a
// script), and request forgery is only possible from a browser, so such a request passes.
func checkCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}

		if site := r.Header.Get("Sec-Fetch-Site"); site != "" {
			if site != "same-origin" && site != "none" {
				writeError(w, http.StatusForbidden, "csrf", "запрос с чужого сайта")
				return
			}
			next.ServeHTTP(w, r)
			return
		}

		if origin := r.Header.Get("Origin"); origin != "" && !sameOrigin(origin, r.Host) {
			writeError(w, http.StatusForbidden, "csrf", "запрос с чужого источника")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func sameOrigin(origin, host string) bool {
	trimmed := strings.TrimPrefix(strings.TrimPrefix(origin, "https://"), "http://")
	return strings.EqualFold(trimmed, host)
}
