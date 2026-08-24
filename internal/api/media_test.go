package api

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func mediaHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	h.api.mediaDir = t.TempDir()
	for _, name := range []string{"squat_bb.mp4", "plank-0.jpg", "plank-1.jpg"} {
		if err := os.WriteFile(filepath.Join(h.api.mediaDir, name), []byte("содержимое"), 0o644); err != nil {
			t.Fatalf("записать %s: %v", name, err)
		}
	}
	return h
}

func TestServeMedia(t *testing.T) {
	h := mediaHarness(t)

	cases := []struct{ path, wantType string }{
		{"/media/squat_bb.mp4", "video/mp4"},
		{"/media/plank-0.jpg", "image/jpeg"},
		{"/media/plank-1.jpg", "image/jpeg"},
	}
	for _, c := range cases {
		resp := h.get(c.path)
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			t.Fatalf("%s: статус %d, ожидался 200", c.path, resp.StatusCode)
		}
		if got := resp.Header.Get("Content-Type"); got != c.wantType {
			resp.Body.Close()
			t.Fatalf("%s: Content-Type %q, ожидался %q", c.path, got, c.wantType)
		}
		// Immutable: a phone at the gym must not spend its connection revalidating a clip
		// it already has.
		if got := resp.Header.Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
			resp.Body.Close()
			t.Fatalf("%s: Cache-Control %q", c.path, got)
		}
		resp.Body.Close()
	}
}

// Demonstrations are footage of strangers in a gym under a free licence — nothing here
// belongs to a user. They are served like the application's own assets, and the service
// worker is allowed to cache them, which a 401 from an expired token would poison.
func TestServeMediaNeedsNoLogin(t *testing.T) {
	h := mediaHarness(t)
	resp := h.get("/media/squat_bb.mp4")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("статус %d без входа, ожидался 200", resp.StatusCode)
	}
}

// The name is matched against a pattern rather than cleaned, so nothing that could leave the
// directory is a name at all. These are the shapes that would matter if it were not.
func TestServeMediaRefusesAnythingButAName(t *testing.T) {
	h := mediaHarness(t)

	secret := filepath.Join(filepath.Dir(h.api.mediaDir), "секрет.mp4")
	if err := os.WriteFile(secret, []byte("не отдавать"), 0o644); err != nil {
		t.Fatalf("записать соседний файл: %v", err)
	}

	for _, path := range []string{
		"/media/..%2fсекрет.mp4",
		"/media/%2e%2e%2fсекрет.mp4",
		"/media/squat_bb.mp4%2f..%2f..%2fetc%2fpasswd",
		"/media/squat_bb.exe",
		"/media/squat_bb",
		"/media/SQUAT_BB.mp4",
		"/media/squat_bb-2.jpg",
		"/media/.env",
		"/media/",
	} {
		resp := h.get(path)
		body := resp.StatusCode
		resp.Body.Close()
		if body == http.StatusOK {
			t.Errorf("%s отдан со статусом 200", path)
		}
	}
}

func TestServeMediaMissingFile(t *testing.T) {
	h := mediaHarness(t)
	resp := h.get("/media/bench_bb.mp4")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("статус %d, ожидался 404", resp.StatusCode)
	}
}

// Range requests are what a video element uses to seek. http.ServeContent gives them for
// free, and losing that would only show up as a player that cannot scrub.
func TestServeMediaSupportsRange(t *testing.T) {
	h := mediaHarness(t)
	resp := h.get("/media/squat_bb.mp4", "Range", "bytes=0-3")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent {
		t.Fatalf("статус %d, ожидался 206", resp.StatusCode)
	}
}
