// Package web serves the built frontend, embedded directly into the binary.
package web

import (
	"embed"
	"errors"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var embedded embed.FS

// Handler serves the app's static files.
//
// Vite's build output carries a content hash in each filename, so those are cached forever,
// while index.html never is: otherwise, after an update, the app would keep loading files
// that no longer exist.
func Handler() (http.Handler, error) {
	dist, err := fs.Sub(embedded, "dist")
	if err != nil {
		return nil, err
	}

	if _, err := fs.Stat(dist, "index.html"); err != nil {
		// The frontend is not built. The service will still come up — the API works — but
		// that has to be said out loud rather than serving a blank screen.
		return notBuiltHandler(), nil
	}

	files := http.FS(dist)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := path.Clean(strings.TrimPrefix(r.URL.Path, "/"))
		if name == "." || name == "/" {
			name = "index.html"
		}

		file, err := dist.Open(name)
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				http.Error(w, "не удалось прочитать файл", http.StatusInternalServerError)
				return
			}
			// The app is a single page: an unknown path is one of its internal screens, not
			// a missing file.
			serveIndex(w, r, files)
			return
		}
		file.Close()

		if strings.HasPrefix(name, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		http.FileServer(files).ServeHTTP(w, r)
	}), nil
}

func serveIndex(w http.ResponseWriter, r *http.Request, files http.FileSystem) {
	index, err := files.Open("index.html")
	if err != nil {
		http.Error(w, "приложение не собрано", http.StatusInternalServerError)
		return
	}
	defer index.Close()

	info, err := index.Stat()
	if err != nil {
		http.Error(w, "приложение не собрано", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Cache-Control", "no-cache")
	http.ServeContent(w, r, "index.html", info.ModTime(), index)
}

func notBuiltHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "фронтенд не собран: выполните `make build` или `npm --prefix web run build`",
			http.StatusServiceUnavailable)
	})
}
