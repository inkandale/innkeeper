package api

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inkandale/innkeeper/internal/library"
	"github.com/inkandale/innkeeper/internal/plugins"
	"github.com/inkandale/innkeeper/protocol"
)

// addTheme installs a theme plugin named name whose stylesheet is css.
func (e *testEnv) addTheme(name, css string) {
	e.t.Helper()
	dir := e.t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "theme.css"), []byte(css), 0o644); err != nil {
		e.t.Fatal(err)
	}
	e.manifests[name] = &protocol.Manifest{Name: name, Version: "1.0.0", Protocol: protocol.Version,
		Capabilities: []string{"theme"}, Stylesheet: "theme.css", Dir: dir}
}

func TestSettingsShowTheVersion(t *testing.T) {
	e := newEnv(t)
	e.s.Version = "1.2.3"
	var settings struct{ Version string }
	e.call("GET", "/settings", nil, 200, &settings)
	if settings.Version != "1.2.3" {
		t.Fatalf("version = %q, want 1.2.3", settings.Version)
	}
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

func TestThemeSymlinkEscape(t *testing.T) {
	e := newEnv(t)
	e.addTheme("dusk", "")
	outside := filepath.Join(t.TempDir(), "secret.css")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	css := filepath.Join(e.s.Registry.Manifests()["dusk"].Dir, "theme.css")
	if err := os.Remove(css); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, css); err != nil {
		t.Fatal(err)
	}
	if r := e.send("GET", "/themes/dusk.css", nil); r.Status != 404 {
		t.Fatalf("symlinked stylesheet = %s, want 404", r)
	}
}

func TestUninstalledTheme(t *testing.T) {
	e := newEnv(t)
	e.addTheme("dusk", ":root{}")
	e.call("PATCH", "/settings", map[string]any{"theme": "dusk"}, 200, nil)
	delete(e.manifests, "dusk")
	var settings struct{ Theme string }
	e.call("GET", "/settings", nil, 200, &settings)
	if settings.Theme != "" {
		t.Fatalf("settings = %+v", settings)
	}
	if r := e.send("GET", "/theme.css", nil); r.Status != 200 || len(r.Body) != 0 {
		t.Fatalf("theme.css = %s", r)
	}
}

func TestOnboardingDone(t *testing.T) {
	e := newEnv(t)
	var got struct {
		OnboardingDone bool `json:"onboarding_done"`
	}
	e.call("GET", "/settings", nil, 200, &got)
	if got.OnboardingDone {
		t.Fatal("onboarding done on a new server")
	}
	e.call("PATCH", "/settings", map[string]any{"onboarding_done": true}, 200, &got)
	if !got.OnboardingDone {
		t.Fatal("PATCH didn't set onboarding_done")
	}
	e.call("GET", "/settings", nil, 200, &got)
	if !got.OnboardingDone {
		t.Fatal("onboarding_done didn't persist")
	}
	e.call("PATCH", "/settings", map[string]any{"onboarding_done": false}, 200, &got)
	if got.OnboardingDone {
		t.Fatal("PATCH didn't clear onboarding_done")
	}
}

func TestPluginIcon(t *testing.T) {
	e := newEnv(t)
	m := *e.manifests["fake"]
	m.Dir = t.TempDir()
	m.Icon = "icon.svg"
	if err := os.WriteFile(filepath.Join(m.Dir, "icon.svg"), []byte("<svg/>"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.manifests["fake"] = &m

	var installed []struct{ Name, Icon string }
	e.call("GET", "/plugins", nil, 200, &installed)
	for _, p := range installed {
		if p.Name == "fake" && p.Icon != "/api/plugins/fake/icon" {
			t.Fatalf("fake icon = %q", p.Icon)
		}
	}

	// Public, like theme stylesheets: a logo holds nothing secret.
	r := e.send("GET", "/plugins/fake/icon", nil)
	if r.Status != 200 || string(r.Body) != "<svg/>" || r.Header.Get("Content-Type") != "image/svg+xml" {
		t.Fatalf("icon = %s %q", r, r.Header.Get("Content-Type"))
	}
	// Opened directly, an SVG's scripts must not run.
	if csp := r.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'none'") {
		t.Fatalf("icon CSP = %q", csp)
	}
	if r := e.send("GET", "/plugins/missing/icon", nil); r.Status != 404 {
		t.Fatalf("missing plugin icon = %s", r)
	}
}

func TestPluginIconSymlinkEscape(t *testing.T) {
	e := newEnv(t)
	m := *e.manifests["fake"]
	m.Dir = t.TempDir()
	m.Icon = "icon.svg"
	e.manifests["fake"] = &m
	outside := filepath.Join(t.TempDir(), "secret.svg")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(m.Dir, "icon.svg")); err != nil {
		t.Fatal(err)
	}
	if r := e.send("GET", "/plugins/fake/icon", nil); r.Status != 404 {
		t.Fatalf("symlinked icon = %s, want 404", r)
	}
}

// addBrain installs an ai plugin named brain with one instance and returns its ID.
func (e *testEnv) addBrain(config map[string]any) int64 {
	e.t.Helper()
	brain := *e.manifests["fake"]
	brain.Name, brain.Capabilities = "brain", []string{protocol.CapabilityAI}
	brain.Command = []string{brain.Command[0], "-caps=ai", "-name=brain"}
	e.manifests["brain"] = &brain
	id, err := e.s.Registry.CreateInstance(context.Background(), plugins.NewInstance{Plugin: "brain", Name: new("Brain"), Config: config})
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

var vectorCfg = map[string]any{"vectors": true, "services": []string{protocol.MethodVectorQuery}}

func TestSettingVectorInstance(t *testing.T) {
	e := newEnrichEnv(t, vectorCfg)
	ctx := context.Background()
	other, err := e.s.Registry.CreateInstance(ctx, plugins.NewInstance{Plugin: "judge", Name: new("Other")})
	if err != nil {
		t.Fatal(err)
	}
	card := e.card("Brakka", "abc")
	c, err := e.s.Lib.Cards.Get(ctx, card)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.s.Lib.SaveVectors(ctx, other, []library.NewVector{{CardID: card, RevisionID: c.HeadRevisionID, Space: "fake", Vector: []float32{1, 2}}}); err != nil {
		t.Fatal(err)
	}

	var s settings
	e.call("PATCH", "/settings", map[string]any{"vector_instance": e.instID}, 200, &s)
	if s.VectorInstance == nil || *s.VectorInstance != e.instID || !s.VectorsAvailable {
		t.Fatalf("settings = %+v", s)
	}
	var n int
	if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM card_embedding WHERE instance_id <> $1`, e.instID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("other instances' vectors = %d, %v; want 0", n, err)
	}
	// Cleared with one vector instance left, automatic picks it.
	e.call("DELETE", fmt.Sprintf("/instances/%d", other), nil, 204, nil)
	e.call("PATCH", "/settings", map[string]any{"vector_instance": nil}, 200, &s)
	e.call("GET", "/settings", nil, 200, &s)
	if s.VectorInstance == nil || *s.VectorInstance != e.instID {
		t.Fatalf("cleared: %+v", s) // the only vector instance is still picked
	}
}

func TestSettingVectorInstanceWithTwoInstancesNeedsAChoice(t *testing.T) {
	e := newEnrichEnv(t, vectorCfg)
	ctx := context.Background()
	other, err := e.s.Registry.CreateInstance(ctx, plugins.NewInstance{Plugin: "judge", Name: new("Other"), Config: vectorCfg})
	if err != nil {
		t.Fatal(err)
	}
	var s settings
	e.call("GET", "/settings", nil, 200, &s)
	if s.VectorInstance != nil {
		t.Fatalf("two vector instances, none chosen: %+v", s)
	}
	e.call("POST", "/cards/search", map[string]any{"text": "abc", "semantic": true}, 409, nil)
	e.call("PATCH", "/settings", map[string]any{"vector_instance": other}, 200, &s)
	if s.VectorInstance == nil || *s.VectorInstance != other {
		t.Fatalf("chose %d: %+v", other, s)
	}
}

func TestSettingVectorInstanceRejectsAnInstanceWithoutVectorQuery(t *testing.T) {
	e := newEnrichEnv(t)
	e.call("PATCH", "/settings", map[string]any{"vector_instance": e.instID}, 400, nil)
}

func TestSettingVectorInstanceRejectsAMissingAndADisabledInstance(t *testing.T) {
	e := newEnrichEnv(t, vectorCfg)
	e.call("PATCH", "/settings", map[string]any{"vector_instance": e.instID + 1000}, 404, nil)
	e.call("PATCH", fmt.Sprintf("/instances/%d", e.instID), map[string]any{"disabled": true}, 200, nil)
	e.call("PATCH", "/settings", map[string]any{"vector_instance": e.instID}, 409, nil)
}

func TestSettingsForgetADeletedVectorInstance(t *testing.T) {
	e := newEnrichEnv(t, vectorCfg)
	e.call("PATCH", "/settings", map[string]any{"vector_instance": e.instID}, 200, nil)
	e.call("DELETE", fmt.Sprintf("/instances/%d", e.instID), nil, 204, nil)
	var s settings
	e.call("GET", "/settings", nil, 200, &s)
	if s.VectorInstance != nil {
		t.Fatalf("settings = %+v", s)
	}
}

func TestSettingsIgnoreADisabledVectorInstance(t *testing.T) {
	e := newEnrichEnv(t, vectorCfg)
	e.call("PATCH", "/settings", map[string]any{"vector_instance": e.instID}, 200, nil)
	e.call("PATCH", fmt.Sprintf("/instances/%d", e.instID), map[string]any{"disabled": true}, 200, nil)
	var s settings
	e.call("GET", "/settings", nil, 200, &s)
	if s.VectorInstance != nil {
		t.Fatalf("settings = %+v", s)
	}
}

func TestTheOnlyVectorInstanceNeedsNoSetting(t *testing.T) {
	e := newEnrichEnv(t, vectorCfg)
	var s settings
	e.call("GET", "/settings", nil, 200, &s)
	if s.VectorInstance == nil || *s.VectorInstance != e.instID {
		t.Fatalf("settings = %+v", s)
	}
}
