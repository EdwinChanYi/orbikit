// Package static serves the embedded admin UI.
package static

import (
	"bytes"
	"embed"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// Vite writes this directory from web/admin (build.outDir). Embed cannot see files outside the package.
//
//go:embed all:dist
var dist embed.FS

// Handler serves the admin UI.
// "/" and paths that are not files return dist/index.html.
// Paths under /assets/ are served as files.
func Handler() http.Handler {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		})
	}
	return &handler{fsys: sub}
}

type handler struct {
	fsys fs.FS
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name == "." {
		name = ""
	}
	switch {
	case name == "":
		if !h.serve(w, r, "index.html") {
			http.NotFound(w, r)
		}
	case isAsset(name):
		if !h.serve(w, r, name) {
			http.NotFound(w, r)
		}
	default:
		if !h.serve(w, r, name) && !h.serve(w, r, "index.html") {
			http.NotFound(w, r)
		}
	}
}

func isAsset(name string) bool {
	return name == "assets" || strings.HasPrefix(name, "assets/")
}

// serve writes name when it is a regular file and reports whether it wrote a response.
func (h *handler) serve(w http.ResponseWriter, r *http.Request, name string) bool {
	f, err := h.fsys.Open(name)
	if err != nil {
		return false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		return false
	}
	rs, err := readSeeker(f)
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return true
	}
	http.ServeContent(w, r, info.Name(), info.ModTime(), rs)
	return true
}

func readSeeker(f fs.File) (io.ReadSeeker, error) {
	if rs, ok := f.(io.ReadSeeker); ok {
		return rs, nil
	}
	b, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	return bytes.NewReader(b), nil
}
