package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	return rec
}

func TestHandler(t *testing.T) {
	h := Handler(fstest.MapFS{
		"index.html":       {Data: []byte("<!doctype html><title>Innkeeper</title>")},
		"assets/app-1a.js": {Data: []byte("console.log(1)")},
	})
	tests := []struct {
		path, body, cache string
	}{
		{"/", "<title>Innkeeper", "no-cache"},
		{"/cards/12", "<title>Innkeeper", "no-cache"},
		{"/assets/app-1a.js", "console.log", "public, max-age=31536000, immutable"},
		// A missing asset is a 404, not the app: a stale bundle should fail loudly.
		{"/assets/gone.js", "404", ""},
	}
	for _, tt := range tests {
		rec := get(t, h, tt.path)
		if !strings.Contains(rec.Body.String(), tt.body) {
			t.Errorf("%s: body = %q, want %q", tt.path, rec.Body, tt.body)
		}
		if got := rec.Header().Get("Cache-Control"); got != tt.cache {
			t.Errorf("%s: Cache-Control = %q, want %q", tt.path, got, tt.cache)
		}
	}
	csp := get(t, h, "/").Header().Get("Content-Security-Policy")
	for _, want := range []string{"script-src 'self'", "style-src 'self'", "frame-ancestors 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP %q lacks %q", csp, want)
		}
	}
}

func TestHandlerWithoutBuild(t *testing.T) {
	rec := get(t, Handler(fstest.MapFS{}), "/")
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "npm run build") {
		t.Fatalf("got %d %q", rec.Code, rec.Body)
	}
}
