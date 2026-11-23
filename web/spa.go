// Package web is the React app (see package.json) and the Go handler that serves its build.
package web

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// Handler serves the built SPA from dist:
//   - /assets/* are content-hashed by Vite, so they are cached for a year as immutable; a
//     missing one is a 404, never index.html.
//   - Any other existing file is served with revalidation.
//   - Everything else gets index.html, uncached, so client-side routes work and a deploy is
//     picked up on the next navigation.
func Handler(dist fs.FS) http.Handler {
	if dist == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "The web app is not embedded in this build. Run `make dev` and open http://localhost:5173.", http.StatusNotFound)
		})
	}
	files := http.FileServerFS(dist)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if strings.HasPrefix(name, "assets/") {
			if !exists(dist, name) {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			files.ServeHTTP(w, r)
			return
		}
		if name != "" && name != "index.html" && exists(dist, name) {
			w.Header().Set("Cache-Control", "no-cache")
			files.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFileFS(w, r, dist, "index.html")
	})
}

func exists(fsys fs.FS, name string) bool {
	st, err := fs.Stat(fsys, name)
	return err == nil && !st.IsDir()
}
