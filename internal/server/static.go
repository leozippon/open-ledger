package server

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
)

// staticHandler serves the embedded web app from memory. The assets are tiny
// and never change at runtime, so they are hashed once into strong ETags.
type staticHandler struct {
	files map[string]staticFile
}

type staticFile struct {
	data        []byte
	etag        string
	contentType string
}

func newStatic(fsys fs.FS) (http.Handler, error) {
	h := &staticHandler{files: map[string]staticFile{}}
	err := fs.WalkDir(fsys, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		h.files["/"+name] = staticFile{
			data:        data,
			etag:        `"` + hex.EncodeToString(sum[:12]) + `"`,
			contentType: contentType(name),
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("load web assets: %w", err)
	}
	index, ok := h.files["/index.html"]
	if !ok {
		return nil, errors.New("load web assets: index.html is missing")
	}
	h.files["/"] = index
	icon, ok := h.files["/icon-180.png"]
	if !ok {
		return nil, errors.New("load web assets: icon-180.png is missing")
	}
	// /ledger-mark.png is the HTML-advertised icon (a URL iOS has never cached).
	// Old apple-touch-icon* and /touch-icon.png stay as aliases so probes do not 404.
	for _, name := range []string{
		"/ledger-mark.png",
		"/touch-icon.png",
		"/apple-touch-icon.png",
		"/apple-touch-icon-precomposed.png",
		"/apple-touch-icon-120x120.png",
		"/apple-touch-icon-120x120-precomposed.png",
		"/apple-touch-icon-152x152.png",
		"/apple-touch-icon-152x152-precomposed.png",
		"/apple-touch-icon-167x167.png",
		"/apple-touch-icon-167x167-precomposed.png",
		"/apple-touch-icon-180x180.png",
		"/apple-touch-icon-180x180-precomposed.png",
	} {
		h.files[name] = icon
	}
	return h, nil
}

func isHomeScreenIconPath(p string) bool {
	return p == "/ledger-mark.png" || p == "/touch-icon.png" || strings.HasPrefix(p, "/apple-touch-icon") ||
		p == "/icon-180.png" || p == "/icon-192.png" || p == "/icon-512.png"
}

func (h *staticHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	if isHomeScreenIconPath(p) {
		log.Printf("ledger: icon %s ua=%q", p, r.UserAgent())
	}
	file, ok := h.files[p]
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", file.contentType)
	w.Header().Set("ETag", file.etag)
	if isHomeScreenIconPath(p) {
		w.Header().Set("Cache-Control", "public, max-age=0, must-revalidate")
		w.Header().Set("Content-Disposition", `inline; filename="apple-touch-icon.png"`)
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	if strings.Contains(r.Header.Get("If-None-Match"), file.etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(file.data)))
	if r.Method == http.MethodHead {
		return
	}
	w.Write(file.data)
}

func contentType(name string) string {
	switch path.Ext(name) {
	case ".html":
		return "text/html; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".js":
		return "text/javascript; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	case ".webmanifest":
		return "application/manifest+json"
	}
	if ct := mime.TypeByExtension(path.Ext(name)); ct != "" {
		return ct
	}
	return "application/octet-stream"
}
