package server

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"net/http"
	"strings"
	"sync"
)

//go:embed all:dist
var distFS embed.FS

var (
	distHashOnce sync.Once
	distHashHex  string
)

// DistHash returns the hex sha256 of the embedded dist/index.html. It is cached
// after first call. Returns "" if the file is missing (development / broken build).
// Ops can compare this value to the one produced by `task web-build` to catch
// the "forgot to rebuild frontend before go build" footgun.
func DistHash() string {
	distHashOnce.Do(func() {
		data, err := distFS.ReadFile("dist/index.html")
		if err != nil {
			return
		}
		sum := sha256.Sum256(data)
		distHashHex = hex.EncodeToString(sum[:])
	})
	return distHashHex
}

func staticHandler() http.Handler {
	subFS, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic("embedded dist not found: " + err.Error())
	}

	fileServer := http.FileServer(http.FS(subFS))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		// API and auth paths are handled by other handlers
		if strings.HasPrefix(path, "/api/") || strings.HasPrefix(path, "/auth/") {
			http.NotFound(w, r)
			return
		}

		// Try to serve the file directly
		if f, err := fs.Stat(subFS, strings.TrimPrefix(path, "/")); err == nil && !f.IsDir() {
			fileServer.ServeHTTP(w, r)
			return
		}

		// SPA fallback: serve index.html for all non-file routes
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	})
}
