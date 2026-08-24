package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/kepthx/gym-tracker/internal/guide"
)

const guidesFile = `{
  "version": 1,
  "exercises": {
    "squat_bb": {
      "summary": "Штанга на спине, таз ниже колен.",
      "cues": ["Гриф на задних дельтах"],
      "media": {"kind": "clip", "credit": "FitnessScape", "license": "CC BY 3.0",
                "source": "https://commons.wikimedia.org/wiki/File:Squat.webm"}
    }
  }
}`

func testGuides(t *testing.T, raw string) *guide.Set {
	t.Helper()
	set, err := guide.Parse("test.json", []byte(raw))
	if err != nil {
		t.Fatalf("разобрать справочник: %v", err)
	}
	return set
}

func TestGetGuides(t *testing.T) {
	h := newHarness(t)
	h.addUser("igor", "пароль-подлиннее", false)
	cookie := h.login("igor", "пароль-подлиннее")
	h.api.guides.Store(testGuides(t, guidesFile))

	resp := h.get("/api/guides", "Cookie", cookie.String())
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Fatalf("статус %d, ожидался 200", resp.StatusCode)
	}
	etag := resp.Header.Get("ETag")

	body := decode[struct {
		Hash   string `json:"hash"`
		Guides struct {
			Version   int `json:"version"`
			Exercises map[string]struct {
				Summary string   `json:"summary"`
				Cues    []string `json:"cues"`
				Media   *struct {
					Kind   string `json:"kind"`
					Credit string `json:"credit"`
				} `json:"media"`
			} `json:"exercises"`
		} `json:"guides"`
	}](t, resp)

	if body.Guides.Version != 1 {
		t.Fatalf("version=%d, ожидалась 1", body.Guides.Version)
	}
	squat, ok := body.Guides.Exercises["squat_bb"]
	if !ok {
		t.Fatal("в ответе нет squat_bb")
	}
	if squat.Media == nil || squat.Media.Kind != "clip" || squat.Media.Credit != "FitnessScape" {
		t.Fatalf("медиа потерялось: %+v", squat.Media)
	}
	if etag != `"`+body.Hash+`"` {
		t.Fatalf("ETag %q не совпадает с хешем %q", etag, body.Hash)
	}
}

// The set is immutable until a reload replaces it, so a repeat request has to cost nothing:
// at the gym the connection is the scarce resource.
func TestGetGuidesNotModified(t *testing.T) {
	h := newHarness(t)
	h.addUser("igor", "пароль-подлиннее", false)
	cookie := h.login("igor", "пароль-подлиннее")
	h.api.guides.Store(testGuides(t, guidesFile))

	first := h.get("/api/guides", "Cookie", cookie.String())
	first.Body.Close()
	etag := first.Header.Get("ETag")

	second := h.get("/api/guides", "Cookie", cookie.String(), "If-None-Match", etag)
	defer second.Body.Close()
	if second.StatusCode != http.StatusNotModified {
		t.Fatalf("статус %d, ожидался 304", second.StatusCode)
	}
}

// No guides file at all is a normal state: the app works, the cards simply do not expand.
func TestGetGuidesEmpty(t *testing.T) {
	h := newHarness(t)
	h.addUser("igor", "пароль-подлиннее", false)
	cookie := h.login("igor", "пароль-подлиннее")

	resp := h.get("/api/guides", "Cookie", cookie.String())
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Fatalf("статус %d, ожидался 200", resp.StatusCode)
	}
	body := decode[struct {
		Hash   string `json:"hash"`
		Guides struct {
			Exercises map[string]any `json:"exercises"`
		} `json:"guides"`
	}](t, resp)

	if len(body.Guides.Exercises) != 0 {
		t.Fatalf("упражнений %d, ожидалось 0", len(body.Guides.Exercises))
	}
	if body.Hash == "" {
		t.Fatal("у пустого набора нет хеша — сравнивать ETag не с чем")
	}
}

func TestGetGuidesRequiresAuth(t *testing.T) {
	h := newHarness(t)
	resp := h.get("/api/guides")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("статус %d, ожидался 401", resp.StatusCode)
	}
}

func TestGuidesReload(t *testing.T) {
	h := newHarness(t)
	h.addUser("admin", "пароль-подлиннее", true)
	cookie := h.login("admin", "пароль-подлиннее")

	h.api.reloadGuides = func() (*guide.Set, error) { return testGuides(t, guidesFile), nil }

	resp := h.post("/api/admin/guides/reload", "", "Cookie", cookie.String(),
		"Sec-Fetch-Site", "same-origin")
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Fatalf("статус %d, ожидался 200", resp.StatusCode)
	}
	resp.Body.Close()

	if _, ok := h.api.guides.Load().File.Exercises["squat_bb"]; !ok {
		t.Fatal("после перезагрузки справочник не подменился")
	}
}

// A broken file must leave the previous set in place: reference text that half-loaded is
// worse than the version that was already there.
func TestGuidesReloadKeepsPreviousOnError(t *testing.T) {
	h := newHarness(t)
	h.addUser("admin", "пароль-подлиннее", true)
	cookie := h.login("admin", "пароль-подлиннее")

	good := testGuides(t, guidesFile)
	h.api.guides.Store(good)
	h.api.reloadGuides = func() (*guide.Set, error) {
		_, err := guide.Parse("test.json", []byte(`{"version":1,"exercises":{"squat_bb":{}}}`))
		return nil, err
	}

	resp := h.post("/api/admin/guides/reload", "", "Cookie", cookie.String(),
		"Sec-Fetch-Site", "same-origin")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("статус %d, ожидался 422", resp.StatusCode)
	}
	if h.api.guides.Load() != good {
		t.Fatal("битая перезагрузка подменила рабочий справочник")
	}
}

func TestGuidesReloadRequiresAdmin(t *testing.T) {
	h := newHarness(t)
	h.addUser("igor", "пароль-подлиннее", false)
	cookie := h.login("igor", "пароль-подлиннее")

	resp := h.post("/api/admin/guides/reload", "", "Cookie", cookie.String(),
		"Sec-Fetch-Site", "same-origin")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("статус %d, ожидался 403", resp.StatusCode)
	}
}

// Nothing third-party, and no hole left behind by the version that embedded a YouTube frame.
// This is CONTEXT.md §9 expressed as an assertion: the demonstrations are served from this
// origin now, so there is no longer any reason for the policy to name another host.
func TestCSPIsStrictlyFirstParty(t *testing.T) {
	h := newHarness(t)
	resp := h.get("/api/auth/me")
	defer resp.Body.Close()

	csp := resp.Header.Get("Content-Security-Policy")
	for _, mustStay := range []string{
		"default-src 'self'", "script-src 'self'", "connect-src 'self'",
		"img-src 'self' data:", "frame-ancestors 'none'",
	} {
		if !strings.Contains(csp, mustStay) {
			t.Fatalf("из CSP пропало %q:\n%s", mustStay, csp)
		}
	}
	for _, mustNotAppear := range []string{"frame-src", "youtube", "ytimg", "google"} {
		if strings.Contains(csp, mustNotAppear) {
			t.Fatalf("в CSP осталось стороннее %q:\n%s", mustNotAppear, csp)
		}
	}
}
