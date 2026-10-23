package web

import (
	"embed"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
)

//go:embed all:dist all:fallback
var embedded embed.FS

type Handler struct {
	fsys         fs.FS
	pagePath     string
	pageName     string
	fallbackPath string
	assetRoot    string
}

func StudentHandler() http.Handler {
	return NewHandler(embedded, "dist/index.html", "fallback/index.html")
}

func TeacherHandler() http.Handler {
	return NewHandler(embedded, "dist/teacher.html", "fallback/teacher.html")
}

func NewHandler(fsys fs.FS, pagePath, fallbackPath string) http.Handler {
	return &Handler{
		fsys:         fsys,
		pagePath:     pagePath,
		pageName:     path.Base(pagePath),
		fallbackPath: fallbackPath,
		assetRoot:    path.Dir(pagePath) + "/assets/",
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	setSecurityHeaders(w)
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if isUnsafePath(r.URL.Path) || isUnsafePath(r.URL.EscapedPath()) {
		http.NotFound(w, r)
		return
	}

	switch {
	case r.URL.Path == "/" || r.URL.Path == "/"+h.pageName:
		h.servePage(w, r)
	case strings.HasPrefix(r.URL.Path, "/assets/"):
		h.serveAsset(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (h *Handler) servePage(w http.ResponseWriter, r *http.Request) {
	name := h.pagePath
	if !fileExists(h.fsys, name) {
		name = h.fallbackPath
	}
	h.serveFile(w, r, name, "text/html; charset=utf-8", "no-store")
}

func (h *Handler) serveAsset(w http.ResponseWriter, r *http.Request) {
	relative := strings.TrimPrefix(r.URL.Path, "/")
	name := path.Join(path.Dir(h.pagePath), relative)
	if !strings.HasPrefix(name, h.assetRoot) {
		http.NotFound(w, r)
		return
	}
	if !fs.ValidPath(name) || !fileExists(h.fsys, name) {
		http.NotFound(w, r)
		return
	}
	contentType := contentTypeFor(name)
	h.serveFile(w, r, name, contentType, "public, max-age=31536000, immutable")
}

func (h *Handler) serveFile(w http.ResponseWriter, r *http.Request, name, contentType, cacheControl string) {
	data, err := fs.ReadFile(h.fsys, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", cacheControl)
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(data)
}

func fileExists(fsys fs.FS, name string) bool {
	info, err := fs.Stat(fsys, name)
	return err == nil && !info.IsDir()
}

func isUnsafePath(value string) bool {
	if strings.Contains(value, "\\") || strings.Contains(value, "..") {
		return true
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "." || segment == ".." {
			return true
		}
	}
	return false
}

func contentTypeFor(name string) string {
	if ct := mime.TypeByExtension(path.Ext(name)); ct != "" {
		return ct
	}
	switch path.Ext(name) {
	case ".js", ".mjs":
		return "text/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".html":
		return "text/html; charset=utf-8"
	case ".json":
		return "application/json; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	default:
		return "application/octet-stream"
	}
}

func setSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self' ws: wss:; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
}
