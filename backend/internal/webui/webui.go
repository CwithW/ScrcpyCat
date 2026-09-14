// Package webui serves the frontend compiled into the control-plane binary.
package webui

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"
)

// The tracked .keep allows API development and Go tests before a Vite build.
// Production builds run make build-frontend first; no runtime asset path exists.
//
//go:embed all:dist
var assets embed.FS

func Handler() http.Handler {
	dist, err := fs.Sub(assets, "dist")
	if err != nil {
		panic(err)
	}
	return handler(dist)
}

func handler(dist fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		// Unknown API endpoints and missing assets must never return the SPA.
		root := strings.SplitN(name, "/", 2)[0]
		switch root {
		case "api", "agent", "devices", "connect_client", "register_agent", "upload", "downloads", "snapshots", "healthz":
			http.NotFound(w, r)
			return
		}
		for _, part := range strings.Split(name, "/") {
			if strings.HasPrefix(part, ".") {
				http.NotFound(w, r)
				return
			}
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if name == "" {
			name = "index.html"
		}
		data, err := fs.ReadFile(dist, name)
		if err != nil && root != "assets" && path.Ext(name) == "" {
			name = "index.html"
			data, err = fs.ReadFile(dist, name)
		}
		if err != nil {
			if name == "index.html" {
				http.Error(w, "frontend assets are not built; run make build-frontend", http.StatusServiceUnavailable)
			} else {
				http.NotFound(w, r)
			}
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		if strings.HasPrefix(name, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		w.Header().Set("ETag", fmt.Sprintf("\"%x\"", sha256.Sum256(data)))
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if path.Ext(name) == ".wasm" {
			w.Header().Set("Content-Type", "application/wasm")
		}
		http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
	})
}
