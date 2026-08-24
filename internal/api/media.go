package api

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// mediaNameRe is the whole of the path defence for the media handler.
//
// Names are derived from an exercise id, never taken from the guides file, so the shape is
// known exactly: "<id>.mp4", or "<id>-0.jpg" and "<id>-1.jpg". The alphabet excludes the
// separator and the dot outside the extension, so no name that matches can leave the
// directory — there is nothing left for a traversal check to catch.
var mediaNameRe = regexp.MustCompile(`^[a-z0-9_]{1,40}(-[01])?\.(mp4|jpg)$`)

var mediaTypes = map[string]string{
	".mp4": "video/mp4",
	".jpg": "image/jpeg",
}

// serveMedia serves an exercise demonstration.
//
// Unauthenticated, unlike everything under /api/, and deliberately so. These files are
// public-domain and CC BY footage of strangers in a gym — there is nothing here belonging to
// a user, and they are served exactly like the application's own assets. It also keeps the
// offline path honest: the service worker may cache this directory, and caching a 401 that
// arrived because a token expired would put a broken player on the screen at the gym with no
// way to notice.
func (a *API) serveMedia(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !mediaNameRe.MatchString(name) {
		http.NotFound(w, r)
		return
	}

	file, err := os.Open(filepath.Join(a.mediaDir, name))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}

	// Immutable by convention: replacing a demonstration means a new file, the same rule the
	// hashed build assets follow. A phone at the gym must not spend its connection
	// revalidating a clip it already has.
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("Content-Type", mediaTypes[strings.ToLower(filepath.Ext(name))])
	http.ServeContent(w, r, name, info.ModTime(), file)
}
