package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/davidgibbons/innkeeper/protocol"
)

// addTheme installs a theme plugin named name whose stylesheet is css.
func (e *testEnv) addTheme(name, css string) {
	e.t.Helper()
	dir := e.t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "theme.css"), []byte(css), 0o644); err != nil {
		e.t.Fatal(err)
	}
	e.s.Registry.Manifests()[name] = &protocol.Manifest{Name: name, Version: "1.0.0", Protocol: protocol.Version,
		Capabilities: []string{"theme"}, Stylesheet: "theme.css", Dir: dir}
}

func TestThemeRoutes(t *testing.T) {
	e := newEnv(t)
	e.addTheme("dusk", ":root { --accent: #c33; }")

	// No theme selected: an empty stylesheet, with no credentials needed.
	r := e.send("GET", "/theme.css", nil)
	if r.Status != 200 || len(r.Body) != 0 || !strings.HasPrefix(r.Header.Get("Content-Type"), "text/css") {
		t.Fatalf("default theme.css = %s %q", r, r.Header.Get("Content-Type"))
	}
	if r := e.send("GET", "/themes/dusk.css", nil); r.Status != 200 || !strings.Contains(string(r.Body), "--accent") {
		t.Fatalf("themes/dusk.css = %s", r)
	}
	for _, path := range []string{"/themes/missing.css", "/themes/fake.css", "/themes/dusk.js"} {
		if r := e.send("GET", path, nil); r.Status != 404 {
			t.Errorf("GET %s = %s, want 404", path, r)
		}
	}

	var settings struct{ Theme string }
	e.call("GET", "/settings", nil, 200, &settings)
	if settings.Theme != "" {
		t.Fatalf("settings = %+v", settings)
	}
	e.call("PATCH", "/settings", map[string]any{"theme": "dusk"}, 200, &settings)
	if settings.Theme != "dusk" {
		t.Fatalf("settings after patch = %+v", settings)
	}
	if r := e.send("GET", "/theme.css", nil); !strings.Contains(string(r.Body), "--accent") {
		t.Fatalf("selected theme.css = %s", r)
	}
	e.call("PATCH", "/settings", map[string]any{"theme": "fake"}, 400, nil)
	if r := e.send("PATCH", "/settings", map[string]any{"theme": ""}); r.Status != 401 {
		t.Fatalf("unauthenticated PATCH = %s", r)
	}
}

func TestAuthMe(t *testing.T) {
	e := newEnv(t)
	var me struct{ Tier string }
	e.call("GET", "/auth/me", nil, 200, &me)
	if me.Tier != "destructive" {
		t.Fatalf("me = %+v", me)
	}
	if r := e.send("GET", "/auth/me", nil); r.Status != 401 {
		t.Fatalf("anonymous /auth/me = %s", r)
	}
}
