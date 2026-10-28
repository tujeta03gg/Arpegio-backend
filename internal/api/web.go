package api

import (
	"net/http"
	"path"
	"strings"
)

// withCORS lets a browser frontend served from another origin talk to the daemon (development only).
// allow is "*" or a single origin such as "http://localhost:5000".
func withCORS(allow string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Access-Control-Allow-Origin", allow)
		if allow != "*" {
			h.Add("Vary", "Origin")
		}
		h.Set("Access-Control-Expose-Headers", "Content-Length, Content-Range, Accept-Ranges")
		if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
			h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			h.Set("Access-Control-Allow-Headers", "Content-Type, Range")
			h.Set("Access-Control-Max-Age", "600")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// webApp serves a built single-page frontend: real files as they are, and index.html for
// any other path without an extension (client-side routes). Missing files with an
// extension are a plain 404 so a broken asset link is not hidden behind the app shell.
func webApp(dir string) http.Handler {
	files := http.Dir(dir)
	fs := http.FileServer(files)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := path.Clean("/" + r.URL.Path)
		if f, err := files.Open(name); err == nil {
			f.Close()
			fs.ServeHTTP(w, r)
			return
		}
		if path.Ext(name) != "" || strings.HasPrefix(name, prefix) {
			http.NotFound(w, r)
			return
		}
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/"
		fs.ServeHTTP(w, r2)
	})
}
