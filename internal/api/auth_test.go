package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kepthx/gym-tracker/internal/auth"
	"github.com/kepthx/gym-tracker/internal/db"
	"github.com/kepthx/gym-tracker/internal/store"
)

const testTTL = 180 * 24 * time.Hour

type harness struct {
	t      *testing.T
	api    *API
	store  *store.Store
	db     *db.DB
	server *httptest.Server
	dbPath string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	return newHarnessAt(t, filepath.Join(t.TempDir(), "test.db"))
}

func newHarnessAt(t *testing.T, dbPath string) *harness {
	t.Helper()
	ctx := context.Background()

	d, err := db.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("открыть базу: %v", err)
	}
	if err := d.Migrate(ctx); err != nil {
		t.Fatalf("миграции: %v", err)
	}

	st := store.New(d)
	a := New(Deps{Store: st, TokenTTL: testTTL, DBPath: dbPath, Version: "test"})
	a.loginDelay = time.Millisecond // tests should not wait a third of a second per attempt
	// The per-IP bucket is loosened deliberately: otherwise it fires before the failure
	// counter in the database and the second layer's tests would be exercising the first.
	// The bucket has a test of its own.
	a.loginLimiter = newIPLimiter(time.Microsecond, 10_000)

	mux := http.NewServeMux()
	a.Routes(mux)
	server := httptest.NewServer(a.Wrap(mux))

	h := &harness{t: t, api: a, store: st, db: d, server: server, dbPath: dbPath}
	t.Cleanup(func() { server.Close(); d.Close() })
	return h
}

func (h *harness) addUser(username, password string, isAdmin bool) int64 {
	h.t.Helper()
	hash, err := auth.HashPassword(password)
	if err != nil {
		h.t.Fatalf("хеш пароля: %v", err)
	}
	id, err := h.store.CreateUser(context.Background(), username, username, hash, isAdmin)
	if err != nil {
		h.t.Fatalf("создать пользователя: %v", err)
	}
	return id
}

func (h *harness) do(req *http.Request) *http.Response {
	h.t.Helper()
	// A client with no cookie jar: each test decides for itself what to attach to a request.
	resp, err := h.server.Client().Do(req)
	if err != nil {
		h.t.Fatalf("запрос: %v", err)
	}
	return resp
}

func (h *harness) post(path, body string, headers ...string) *http.Response {
	h.t.Helper()
	req, err := http.NewRequest(http.MethodPost, h.server.URL+path, strings.NewReader(body))
	if err != nil {
		h.t.Fatalf("собрать запрос: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	applyHeaders(h.t, req, headers)
	return h.do(req)
}

func (h *harness) get(path string, headers ...string) *http.Response {
	h.t.Helper()
	req, err := http.NewRequest(http.MethodGet, h.server.URL+path, nil)
	if err != nil {
		h.t.Fatalf("собрать запрос: %v", err)
	}
	applyHeaders(h.t, req, headers)
	return h.do(req)
}

func applyHeaders(t *testing.T, req *http.Request, headers []string) {
	t.Helper()
	if len(headers)%2 != 0 {
		t.Fatalf("заголовки задаются парами, получено %d значений", len(headers))
	}
	for i := 0; i < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
}

// login performs a log-in and returns the session cookie.
func (h *harness) login(username, password string) *http.Cookie {
	h.t.Helper()
	body := fmt.Sprintf(`{"username":%q,"password":%q}`, username, password)
	resp := h.post("/api/auth/login", body)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		h.t.Fatalf("вход вернул %d", resp.StatusCode)
	}
	for _, c := range resp.Cookies() {
		if c.Name == cookieName {
			return c
		}
	}
	h.t.Fatal("вход не выдал cookie сессии")
	return nil
}

func decode[T any](t *testing.T, resp *http.Response) T {
	t.Helper()
	defer resp.Body.Close()
	var out T
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("разобрать ответ: %v", err)
	}
	return out
}

func TestLoginSucceedsAndIssuesCookie(t *testing.T) {
	h := newHarness(t)
	h.addUser("igor", "очень-секретный-пароль", true)

	resp := h.post("/api/auth/login", `{"username":"igor","password":"очень-секретный-пароль"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("статус %d", resp.StatusCode)
	}
	body := decode[sessionResponse](t, resp)
	if body.User.Username != "igor" || !body.User.IsAdmin {
		t.Errorf("тело ответа: %+v", body.User)
	}
	if body.ExpiresAt <= time.Now().UnixMilli() {
		t.Error("срок действия токена в прошлом")
	}
	// The raw token comes back once, from login: the cookie is HttpOnly, and the copy the
	// client keeps in IndexedDB is what survives WebKit dropping the cookie.
	if body.Token == "" {
		t.Error("вход не вернул токен в теле ответа")
	}

	var cookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == cookieName {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("cookie сессии не выдана")
	}
	// The server sets the lifetime: in WebKit a cookie written from document.cookie lives
	// a week, while a server-set one lasts the full term. Otherwise the password would have
	// to be typed at the gym.
	if !cookie.HttpOnly {
		t.Error("cookie доступна скриптам")
	}
	if cookie.SameSite != http.SameSiteLaxMode {
		t.Error("cookie без SameSite=Lax")
	}
	if cookie.MaxAge < int(testTTL.Seconds()) {
		t.Errorf("MaxAge=%d, ожидалось не меньше %d", cookie.MaxAge, int(testTTL.Seconds()))
	}
}

func TestLoginWithoutUsernameUsesTheOnlyUser(t *testing.T) {
	h := newHarness(t)
	h.addUser("igor", "очень-секретный-пароль", true)

	// The login screen shows a single password field while there is one user.
	resp := h.post("/api/auth/login", `{"password":"очень-секретный-пароль"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("статус %d", resp.StatusCode)
	}
	if got := decode[sessionResponse](t, resp).User.Username; got != "igor" {
		t.Errorf("вошли как %q", got)
	}
}

func TestLoginRejectsWrongPassword(t *testing.T) {
	h := newHarness(t)
	h.addUser("igor", "очень-секретный-пароль", false)

	resp := h.post("/api/auth/login", `{"username":"igor","password":"не-тот"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("статус %d, ожидался 401", resp.StatusCode)
	}
	if len(resp.Cookies()) != 0 {
		t.Error("при неверном пароле выдана cookie")
	}
}

// The response to a nonexistent username must not differ from the response to a wrong
// password, or it could be used to enumerate who exists in the system.
func TestUnknownUserLooksLikeWrongPassword(t *testing.T) {
	h := newHarness(t)
	h.addUser("igor", "очень-секретный-пароль", false)

	wrongPass := h.post("/api/auth/login", `{"username":"igor","password":"не-тот"}`)
	defer wrongPass.Body.Close()
	noUser := h.post("/api/auth/login", `{"username":"нет-такого","password":"не-тот"}`)
	defer noUser.Body.Close()

	if wrongPass.StatusCode != noUser.StatusCode {
		t.Fatalf("статусы различаются: %d и %d", wrongPass.StatusCode, noUser.StatusCode)
	}
}

func TestProtectedRoutesRequireToken(t *testing.T) {
	h := newHarness(t)
	h.addUser("igor", "очень-секретный-пароль", false)

	for _, path := range []string{"/api/auth/me", "/api/sync?since=0", "/api/program"} {
		resp := h.get(path)
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s без токена вернул %d, ожидался 401", path, resp.StatusCode)
		}
	}
}

func TestTokenWorksAsCookieAndAsBearer(t *testing.T) {
	h := newHarness(t)
	h.addUser("igor", "очень-секретный-пароль", false)
	cookie := h.login("igor", "очень-секретный-пароль")

	byCookie := h.get("/api/auth/me", "Cookie", cookie.Name+"="+cookie.Value)
	byCookie.Body.Close()
	if byCookie.StatusCode != http.StatusOK {
		t.Errorf("по cookie: %d", byCookie.StatusCode)
	}

	// The fallback path for when the cookie is gone but the copy of the token in storage remains.
	byBearer := h.get("/api/auth/me", "Authorization", "Bearer "+cookie.Value)
	byBearer.Body.Close()
	if byBearer.StatusCode != http.StatusOK {
		t.Errorf("по заголовку Authorization: %d", byBearer.StatusCode)
	}
}

func TestInvalidAndExpiredTokensAreRejected(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	userID := h.addUser("igor", "очень-секретный-пароль", false)

	resp := h.get("/api/auth/me", "Authorization", "Bearer выдуманный-токен")
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("выдуманный токен: %d", resp.StatusCode)
	}

	raw, _, err := h.store.CreateToken(ctx, userID, -time.Hour, "тест", time.Now())
	if err != nil {
		t.Fatalf("выдать токен: %v", err)
	}
	expired := h.get("/api/auth/me", "Authorization", "Bearer "+raw)
	expired.Body.Close()
	if expired.StatusCode != http.StatusUnauthorized {
		t.Errorf("истёкший токен: %d", expired.StatusCode)
	}
}

func TestLogoutRevokesTheToken(t *testing.T) {
	h := newHarness(t)
	h.addUser("igor", "очень-секретный-пароль", false)
	cookie := h.login("igor", "очень-секретный-пароль")

	out := h.post("/api/auth/logout", "", "Cookie", cookie.Name+"="+cookie.Value)
	out.Body.Close()
	if out.StatusCode != http.StatusNoContent {
		t.Fatalf("выход вернул %d", out.StatusCode)
	}

	after := h.get("/api/auth/me", "Cookie", cookie.Name+"="+cookie.Value)
	after.Body.Close()
	if after.StatusCode != http.StatusUnauthorized {
		t.Errorf("после выхода токен ещё работает: %d", after.StatusCode)
	}
}

func TestLockoutAfterRepeatedFailures(t *testing.T) {
	h := newHarness(t)
	h.addUser("igor", "очень-секретный-пароль", false)

	for i := 0; i < store.MaxFailuresBeforeLock; i++ {
		resp := h.post("/api/auth/login", `{"username":"igor","password":"не-тот"}`)
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("попытка %d вернула %d", i+1, resp.StatusCode)
		}
	}

	locked := h.post("/api/auth/login", `{"username":"igor","password":"не-тот"}`)
	defer locked.Body.Close()
	if locked.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("после %d неудач статус %d, ожидался 429", store.MaxFailuresBeforeLock, locked.StatusCode)
	}
	retry, err := strconv.Atoi(locked.Header.Get("Retry-After"))
	if err != nil || retry <= 0 {
		t.Fatalf("Retry-After: %q", locked.Header.Get("Retry-After"))
	}

	// A correct password does not get through either: otherwise the lockout would not
	// impede brute force.
	withRight := h.post("/api/auth/login", `{"username":"igor","password":"очень-секретный-пароль"}`)
	withRight.Body.Close()
	if withRight.StatusCode != http.StatusTooManyRequests {
		t.Errorf("во время блокировки верный пароль дал %d", withRight.StatusCode)
	}
}

// The failure counter lives in the database rather than in memory: restarting the
// service must not hand brute force another five attempts.
func TestLockoutSurvivesRestart(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")

	first := newHarnessAt(t, dbPath)
	first.addUser("igor", "очень-секретный-пароль", false)
	for i := 0; i < store.MaxFailuresBeforeLock; i++ {
		resp := first.post("/api/auth/login", `{"username":"igor","password":"не-тот"}`)
		resp.Body.Close()
	}
	first.server.Close()
	first.db.Close()

	second := newHarnessAt(t, dbPath)
	locked := second.post("/api/auth/login", `{"username":"igor","password":"не-тот"}`)
	defer locked.Body.Close()
	if locked.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("после перезапуска статус %d, ожидался 429", locked.StatusCode)
	}
}

func TestSuccessfulLoginClearsFailureCount(t *testing.T) {
	h := newHarness(t)
	h.addUser("igor", "очень-секретный-пароль", false)

	for i := 0; i < store.MaxFailuresBeforeLock-1; i++ {
		resp := h.post("/api/auth/login", `{"username":"igor","password":"не-тот"}`)
		resp.Body.Close()
	}
	h.login("igor", "очень-секретный-пароль")

	// After a success the counter starts over; otherwise someone who fumbled their password
	// once a couple of weeks ago would find themselves locked out for no reason.
	for i := 0; i < store.MaxFailuresBeforeLock-1; i++ {
		resp := h.post("/api/auth/login", `{"username":"igor","password":"не-тот"}`)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("попытка %d после успеха вернула %d", i+1, resp.StatusCode)
		}
	}
}

// The first brute-force layer: an in-memory per-IP bucket. It is faster than the counter
// in the database and damps fast brute force without waiting for failures to pile up
// against a particular username.
func TestIPRateLimitStopsRapidAttempts(t *testing.T) {
	h := newHarness(t)
	h.addUser("igor", "очень-секретный-пароль", false)
	h.api.loginLimiter = newIPLimiter(10*time.Second, 3)

	var limited bool
	for i := 0; i < 5; i++ {
		// The usernames differ every time: the failure counter in the database has nothing
		// to do with it, the per-IP bucket is what has to fire.
		body := fmt.Sprintf(`{"username":"имя-%d","password":"не-тот"}`, i)
		resp := h.post("/api/auth/login", body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("ведро на IP не сработало за пять быстрых попыток")
	}
}

func TestDataIsIsolatedBetweenUsers(t *testing.T) {
	h := newHarness(t)
	h.addUser("igor", "очень-секретный-пароль", false)
	h.addUser("lena", "другой-очень-секретный", false)

	igor := h.login("igor", "очень-секретный-пароль")
	lena := h.login("lena", "другой-очень-секретный")

	if igor.Value == lena.Value {
		t.Fatal("двум пользователям выдан один токен")
	}

	igorMe := decode[sessionResponse](t, h.get("/api/auth/me", "Cookie", igor.Name+"="+igor.Value))
	lenaMe := decode[sessionResponse](t, h.get("/api/auth/me", "Cookie", lena.Name+"="+lena.Value))
	if igorMe.User.Username != "igor" || lenaMe.User.Username != "lena" {
		t.Fatalf("токены перепутаны: %q и %q", igorMe.User.Username, lenaMe.User.Username)
	}
}

func TestCSRFBlocksCrossSiteWrites(t *testing.T) {
	h := newHarness(t)
	h.addUser("igor", "очень-секретный-пароль", false)
	cookie := h.login("igor", "очень-секретный-пароль")

	// The token lives in an HttpOnly cookie, so a browser would attach it to a request from
	// another site too. That is exactly what gets cut off.
	cross := h.post("/api/sync", `{"device_id":"x","since":0,"ops":[]}`,
		"Cookie", cookie.Name+"="+cookie.Value,
		"Sec-Fetch-Site", "cross-site")
	cross.Body.Close()
	if cross.StatusCode != http.StatusForbidden {
		t.Errorf("запрос с чужого сайта вернул %d, ожидался 403", cross.StatusCode)
	}

	same := h.post("/api/sync", `{"device_id":"x","since":0,"ops":[]}`,
		"Cookie", cookie.Name+"="+cookie.Value,
		"Sec-Fetch-Site", "same-origin")
	same.Body.Close()
	if same.StatusCode != http.StatusOK {
		t.Errorf("свой запрос вернул %d", same.StatusCode)
	}

	badOrigin := h.post("/api/sync", `{"device_id":"x","since":0,"ops":[]}`,
		"Cookie", cookie.Name+"="+cookie.Value,
		"Origin", "https://зло.example")
	badOrigin.Body.Close()
	if badOrigin.StatusCode != http.StatusForbidden {
		t.Errorf("чужой Origin вернул %d, ожидался 403", badOrigin.StatusCode)
	}
}

func TestSecurityHeaders(t *testing.T) {
	h := newHarness(t)
	resp := h.get("/api/auth/me")
	defer resp.Body.Close()

	want := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"Referrer-Policy":        "same-origin",
	}
	for header, value := range want {
		if got := resp.Header.Get(header); got != value {
			t.Errorf("%s = %q, ожидалось %q", header, got, value)
		}
	}
	csp := resp.Header.Get("Content-Security-Policy")
	for _, directive := range []string{"default-src 'self'", "frame-ancestors 'none'", "base-uri 'none'"} {
		if !strings.Contains(csp, directive) {
			t.Errorf("в CSP нет %q: %s", directive, csp)
		}
	}
}

func TestSlidingExpiryExtendsOldToken(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	userID := h.addUser("igor", "очень-секретный-пароль", false)

	// A token issued long ago: a request with it should extend the term and reissue the cookie.
	old := time.Now().Add(-slideAfter - 24*time.Hour)
	raw, _, err := h.store.CreateToken(ctx, userID, testTTL, "тест", old)
	if err != nil {
		t.Fatalf("выдать токен: %v", err)
	}

	resp := h.get("/api/auth/me", "Authorization", "Bearer "+raw)
	body := decode[sessionResponse](t, resp)

	if body.ExpiresAt < time.Now().Add(testTTL-time.Hour).UnixMilli() {
		t.Errorf("срок не продлён: %d", body.ExpiresAt)
	}
	var reissued bool
	for _, c := range resp.Cookies() {
		if c.Name == cookieName && c.Value == raw {
			reissued = true
		}
	}
	if !reissued {
		t.Error("cookie не перевыпущена при продлении")
	}
}

func TestPruneAuthRemovesExpiredTokens(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	userID := h.addUser("igor", "очень-секретный-пароль", false)

	if _, _, err := h.store.CreateToken(ctx, userID, -time.Hour, "старый", time.Now()); err != nil {
		t.Fatalf("выдать истёкший токен: %v", err)
	}
	live, _, err := h.store.CreateToken(ctx, userID, testTTL, "живой", time.Now())
	if err != nil {
		t.Fatalf("выдать живой токен: %v", err)
	}

	if err := h.store.PruneAuth(ctx, time.Now()); err != nil {
		t.Fatalf("уборка: %v", err)
	}

	var count int
	if err := h.db.R.QueryRowContext(ctx, `SELECT count(*) FROM auth_tokens`).Scan(&count); err != nil {
		t.Fatalf("посчитать токены: %v", err)
	}
	if count != 1 {
		t.Errorf("токенов осталось %d, ожидался 1", count)
	}
	resp := h.get("/api/auth/me", "Authorization", "Bearer "+live)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("живой токен перестал работать после уборки: %d", resp.StatusCode)
	}
}

// Behind a trusted proxy the socket address is the proxy's own; the login limiter has to key
// on the address the proxy appended, or every visitor shares one bucket of attempts.
func TestTrustedProxyLimitsByForwardedAddress(t *testing.T) {
	h := newHarness(t)
	h.addUser("igor", "очень-секретный-пароль", false)
	h.api.trustProxy = true
	h.api.loginLimiter = newIPLimiter(10*time.Second, 2)

	// Two strangers exhaust their own buckets…
	for i := 0; i < 4; i++ {
		resp := h.post("/api/auth/login", `{"username":"кто-то","password":"не-тот"}`,
			"X-Forwarded-For", fmt.Sprintf("203.0.113.%d", i%2))
		resp.Body.Close()
	}
	// …and the real user, from a third address, still gets in.
	resp := h.post("/api/auth/login", `{"username":"igor","password":"очень-секретный-пароль"}`,
		"X-Forwarded-For", "198.51.100.7")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("вход через прокси вернул %d — лимит на IP общий для всех", resp.StatusCode)
	}
}

func TestForwardedHeadersAreIgnoredWithoutTrustedProxy(t *testing.T) {
	h := newHarness(t)
	h.addUser("igor", "очень-секретный-пароль", false)
	h.api.loginLimiter = newIPLimiter(10*time.Second, 2)

	// A client cannot dodge the limiter by inventing a new address per attempt.
	var limited bool
	for i := 0; i < 5; i++ {
		resp := h.post("/api/auth/login", `{"username":"кто-то","password":"не-тот"}`,
			"X-Forwarded-For", fmt.Sprintf("203.0.113.%d", i))
		resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("подделанный X-Forwarded-For обошёл лимит на IP")
	}
}

func TestTrustedProxyTLSMakesCookieSecure(t *testing.T) {
	h := newHarness(t)
	h.addUser("igor", "очень-секретный-пароль", false)
	h.api.trustProxy = true

	resp := h.post("/api/auth/login", `{"username":"igor","password":"очень-секретный-пароль"}`,
		"X-Forwarded-Proto", "https")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("статус %d", resp.StatusCode)
	}
	var cookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == cookieName {
			cookie = c
		}
	}
	if cookie == nil || !cookie.Secure {
		t.Error("cookie без Secure, хотя прокси сообщил о TLS")
	}
	if resp.Header.Get("Strict-Transport-Security") == "" {
		t.Error("нет HSTS, хотя прокси сообщил о TLS")
	}
}

// A mistyped endpoint is a 404, not the SPA shell with a 200.
func TestUnknownAPIRouteIs404(t *testing.T) {
	h := newHarness(t)
	resp := h.get("/api/no-such-thing")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("статус %d, ожидался 404", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type %q, ожидался JSON", ct)
	}
}

// Logging in correctly, again and again, is not brute force: several devices behind one
// address, or a test suite, must not be told to wait a minute after the fifth success.
func TestSuccessfulLoginsAreNotRateLimited(t *testing.T) {
	h := newHarness(t)
	h.addUser("igor", "очень-секретный-пароль", false)
	h.api.loginLimiter = newIPLimiter(10*time.Second, 2)

	for i := 0; i < 6; i++ {
		resp := h.post("/api/auth/login", `{"username":"igor","password":"очень-секретный-пароль"}`)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("успешный вход №%d вернул %d — удачные попытки съедают лимит", i+1, resp.StatusCode)
		}
	}

	// The bucket is still armed against guesses, though.
	var limited bool
	for i := 0; i < 4; i++ {
		resp := h.post("/api/auth/login", `{"username":"igor","password":"не-тот"}`)
		resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("после удачных входов лимит на неудачные попытки перестал работать")
	}
}
