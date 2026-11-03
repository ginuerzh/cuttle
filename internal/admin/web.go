package admin

import (
	"embed"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var webFS embed.FS

// handleWeb serves the embedded SPA, falling back to index.html for client
// routes. /api/* is never handled here.
func (s *Server) handleWeb(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		http.NotFound(w, r)
		return
	}

	root, err := fs.Sub(webFS, "dist")
	if err != nil {
		http.Error(w, "web UI not built", http.StatusServiceUnavailable)
		return
	}

	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if name == "" || name == "." {
		name = "index.html"
	}
	data, err := fs.ReadFile(root, name)
	if err != nil {
		data, err = fs.ReadFile(root, "index.html")
		if err != nil {
			http.Error(w, "web UI not built", http.StatusServiceUnavailable)
			return
		}
		name = "index.html"
	}

	ct := mime.TypeByExtension(path.Ext(name))
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.Write(data)
}
