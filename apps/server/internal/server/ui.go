package server

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed webdist/* webdist/assets/*
var webAssets embed.FS

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	assets, err := fs.Sub(webAssets, "webdist")
	if err != nil {
		http.Error(w, "frontend unavailable", 500)
		return
	}
	requested := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if requested == "." || requested == "" {
		requested = "index.html"
	}
	if _, err = fs.Stat(assets, requested); err != nil {
		requested = "index.html"
	}
	if strings.HasPrefix(requested, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	http.ServeFileFS(w, r, assets, requested)
}
