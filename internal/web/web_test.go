package web

import (
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func mapFS(files map[string]string) fstest.MapFS {
	out := fstest.MapFS{}
	for name, body := range files {
		out[name] = &fstest.MapFile{Data: []byte(body)}
	}
	return out
}

func request(t *testing.T, h http.Handler, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func body(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	data, err := io.ReadAll(rec.Result().Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(data)
}

func builtFS() fs.FS {
	return mapFS(map[string]string{
		"dist/index.html":       "<html>student-built</html>",
		"dist/teacher.html":     "<html>teacher-built</html>",
		"dist/assets/app.js":    "console.log('app')",
		"dist/assets/app.css":   "body{}",
		"fallback/index.html":   "fallback-student",
		"fallback/teacher.html": "fallback-teacher",
	})
}

func TestStudentHandlerServesBuiltPage(t *testing.T) {
	h := NewHandler(builtFS(), "dist/index.html", "fallback/index.html")
	for _, target := range []string{"/", "/index.html"} {
		rec := request(t, h, http.MethodGet, target)
		if rec.Code != http.StatusOK {
			t.Errorf("%s status = %d", target, rec.Code)
		}
		if got := body(t, rec); !strings.Contains(got, "student-built") {
			t.Errorf("%s body = %q", target, got)
		}
	}
}

func TestTeacherHandlerServesBuiltPage(t *testing.T) {
	h := NewHandler(builtFS(), "dist/teacher.html", "fallback/teacher.html")
	for _, target := range []string{"/", "/teacher.html"} {
		rec := request(t, h, http.MethodGet, target)
		if rec.Code != http.StatusOK {
			t.Errorf("%s status = %d", target, rec.Code)
		}
		if got := body(t, rec); !strings.Contains(got, "teacher-built") {
			t.Errorf("%s body = %q", target, got)
		}
	}
}

func TestStudentHandlerDoesNotServeTeacherPage(t *testing.T) {
	h := NewHandler(builtFS(), "dist/index.html", "fallback/index.html")
	rec := request(t, h, http.MethodGet, "/teacher.html")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestTeacherHandlerDoesNotServeStudentPage(t *testing.T) {
	h := NewHandler(builtFS(), "dist/teacher.html", "fallback/teacher.html")
	rec := request(t, h, http.MethodGet, "/index.html")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestAssetsServedWithCacheHeaders(t *testing.T) {
	h := NewHandler(builtFS(), "dist/index.html", "fallback/index.html")
	rec := request(t, h, http.MethodGet, "/assets/app.js")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := body(t, rec); got != "console.log('app')" {
		t.Fatalf("body = %q", got)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "javascript") {
		t.Errorf("content-type = %q", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("cache-control = %q", cc)
	}
}

func TestFallbackWhenBuiltPageMissing(t *testing.T) {
	fsys := mapFS(map[string]string{
		"fallback/index.html":   "fallback-student",
		"fallback/teacher.html": "fallback-teacher",
	})
	h := NewHandler(fsys, "dist/index.html", "fallback/index.html")
	rec := request(t, h, http.MethodGet, "/")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := body(t, rec); !strings.Contains(got, "fallback-student") {
		t.Fatalf("body = %q", got)
	}
}

func TestRejectsTraversal(t *testing.T) {
	h := NewHandler(builtFS(), "dist/index.html", "fallback/index.html")
	for _, target := range []string{
		"/assets/../index.html",
		"/assets/%2e%2e/index.html",
		"/assets/..%2findex.html",
		"/assets/..%5cindex.html",
		"/assets/%2e%2e%5cindex.html",
	} {
		rec := request(t, h, http.MethodGet, target)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s status = %d, want 404", target, rec.Code)
		}
	}
}

func TestRejectsUnknownPath(t *testing.T) {
	h := NewHandler(builtFS(), "dist/index.html", "fallback/index.html")
	rec := request(t, h, http.MethodGet, "/secret.txt")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestRejectsNonGet(t *testing.T) {
	h := NewHandler(builtFS(), "dist/index.html", "fallback/index.html")
	rec := request(t, h, http.MethodPost, "/")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
	if allow := rec.Header().Get("Allow"); !strings.Contains(allow, "GET") {
		t.Errorf("Allow = %q", allow)
	}
}

func TestSecurityHeaders(t *testing.T) {
	h := NewHandler(builtFS(), "dist/index.html", "fallback/index.html")
	rec := request(t, h, http.MethodGet, "/")
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("x-content-type-options = %q", got)
	}
	csp := rec.Header().Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'self'", "connect-src 'self' ws: wss:", "frame-ancestors 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("csp %q missing %q", csp, want)
		}
	}
}
