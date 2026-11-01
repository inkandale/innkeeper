package api

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inkandale/innkeeper/protocol"
)

// addPanel gives e a ui plugin named panel whose bundle is in its folder.
func addPanel(t *testing.T, e *testEnv) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "ui"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"ui/index.html": "<p>panel", "ui/app.js": "1", "secret.txt": "no"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	e.manifests["panel"] = &protocol.Manifest{Name: "panel", Version: "1", Protocol: protocol.Version, Dir: dir,
		Capabilities: []string{protocol.CapabilityUI}, UI: &protocol.UI{Entry: "ui/index.html", Slot: protocol.SlotCorner}}
	return dir
}

func TestPluginUIServesBundleWithSandboxHeaders(t *testing.T) {
	e := newEnv(t)
	addPanel(t, e)
	res, err := http.Get(e.srv.URL + "/api/plugin-ui/panel/index.html")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("status %d", res.StatusCode)
	}
	csp := res.Header.Get("Content-Security-Policy")
	for _, want := range []string{"sandbox allow-scripts", "connect-src 'none'", "script-src http://" + res.Request.Host, "frame-ancestors 'self'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP %q lacks %q", csp, want)
		}
	}
	if res.Header.Get("X-Content-Type-Options") != "nosniff" || res.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("headers %v", res.Header)
	}
}

func TestPluginUIRefusesPathsOutsideTheBundle(t *testing.T) {
	e := newEnv(t)
	addPanel(t, e)
	for _, path := range []string{"/api/plugin-ui/panel/../secret.txt", "/api/plugin-ui/panel/%2e%2e/secret.txt", "/api/plugin-ui/nobody/index.html", "/api/plugin-ui/fake/index.html"} {
		res, err := http.Get(e.srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode == 200 || string(body) == "no" {
			t.Errorf("%s served", path)
		}
	}
}

func TestPluginUIRejectsAHostThatCouldAddCSPDirectives(t *testing.T) {
	e := newEnv(t)
	addPanel(t, e)
	for _, host := range []string{"a; script-src *", "a,b", "a'b", `a"b`} {
		req, err := http.NewRequest("GET", e.srv.URL+"/api/plugin-ui/panel/index.html", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Host = host
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 400 || res.Header.Get("Content-Security-Policy") != "" {
			t.Errorf("host %q: status %d, csp %q", host, res.StatusCode, res.Header.Get("Content-Security-Policy"))
		}
	}
}
