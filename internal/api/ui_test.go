package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/inkandale/innkeeper/internal/plugins"
	"github.com/inkandale/innkeeper/protocol"
)

// addPanel gives e a ui plugin named panel whose bundle is in its folder.
func addPanel(t *testing.T, e *testEnv) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "ui"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"ui/index.html": "<p>panel", "ui/app.js": "1", "ui/app.css": "p{}", "secret.txt": "no"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	e.manifests["panel"] = &protocol.Manifest{Name: "panel", Version: "1", Protocol: protocol.Version, Dir: dir,
		Capabilities: []string{protocol.CapabilityUI}, UI: &protocol.UI{Title: "Panel", Entry: "ui/index.html", Slot: protocol.SlotCorner}}
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
	for _, want := range []string{"sandbox allow-scripts", "connect-src 'none'", "script-src http://" + res.Request.Host + "/api/plugin-ui/panel/", "frame-ancestors 'self'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP %q lacks %q", csp, want)
		}
	}
	if res.Header.Get("X-Content-Type-Options") != "nosniff" || res.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("headers %v", res.Header)
	}
}

func TestPluginUITypesBundleFiles(t *testing.T) {
	e := newEnv(t)
	addPanel(t, e)
	for file, want := range map[string]string{"app.js": "text/javascript", "app.css": "text/css"} {
		res, err := http.Get(e.srv.URL + "/api/plugin-ui/panel/" + file)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if got := res.Header.Get("Content-Type"); !strings.HasPrefix(got, want) {
			t.Errorf("%s: Content-Type %q, want %q", file, got, want)
		}
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

// addUIInstance gives e an instance of the fake plugin as a ui plugin. A copy
// keeps the ui capability out of the shared fake, which needs a [ui] table to
// load and which other tests list.
func addUIInstance(t *testing.T, e *testEnv) (id int64, dir string) {
	t.Helper()
	ui := *e.manifests["fake"]
	ui.Name, ui.Capabilities = "panelled", []string{protocol.CapabilityUI}
	ui.Command = []string{ui.Command[0], "-caps=ui", "-name=panelled"}
	ui.UI = &protocol.UI{Title: "Panel", Entry: "ui/index.html", Slot: protocol.SlotCorner, Permissions: []string{protocol.PermissionLibraryRead}}
	if err := os.MkdirAll(filepath.Join(ui.Dir, "ui"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ui.Dir, ui.UI.Entry), []byte("<p>panel"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.manifests[ui.Name] = &ui
	id, err := e.s.Registry.CreateInstance(t.Context(), plugins.NewInstance{Plugin: ui.Name, Name: new("Panelled")})
	if err != nil {
		t.Fatal(err)
	}
	return id, ui.Dir
}

func (e *testEnv) uiRequest(t *testing.T, ctx context.Context, instance int64, method string, params any) *http.Response {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"method": method, "params": params})
	req, err := http.NewRequestWithContext(ctx, "POST", fmt.Sprintf("%s/api/instances/%d/ui/call", e.srv.URL, instance), bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+e.token)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 200 {
		defer res.Body.Close()
		out, _ := io.ReadAll(res.Body)
		t.Fatalf("status %d: %s", res.StatusCode, out)
	}
	return res
}

// uiStream posts one ui call and returns the SSE body.
func (e *testEnv) uiStream(t *testing.T, instance int64, method string, params any) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	res := e.uiRequest(t, ctx, instance, method, params)
	defer res.Body.Close()
	out, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read the stream: %v; got %.200q", err, out)
	}
	return string(out)
}

// startSlowCall returns once the plugin is inside its slow call.
func (e *testEnv) startSlowCall(t *testing.T, ctx context.Context, instance int64) *http.Response {
	t.Helper()
	res := e.uiRequest(t, ctx, instance, "slow", nil)
	lines := bufio.NewReader(res.Body)
	for {
		line, err := lines.ReadString('\n')
		if err != nil {
			t.Fatalf("no first event: %v", err)
		}
		if line == "event: event\n" {
			return res
		}
	}
}

func waitForCancelled(t *testing.T, pluginDir string) {
	t.Helper()
	waitForFile(t, pluginDir, "cancelled")
}

// waitForFile fails when the plugin never leaves name in its folder. A flood
// takes over 10s under -race on CI; past uiWriteTimeout's 30s default, a
// timed-out write would free a stalled stream and hide the stall.
func waitForFile(t *testing.T, pluginDir, name string) {
	t.Helper()
	deadline := time.Now().Add(25 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(pluginDir, name)); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the plugin left no %s file", name)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// setUIWriteTimeout must run before newEnv, so the server stops before the
// value is restored.
func setUIWriteTimeout(t *testing.T, d time.Duration) {
	t.Helper()
	old := uiWriteTimeout
	uiWriteTimeout = d
	t.Cleanup(func() { uiWriteTimeout = old })
}

func TestUICallStreamsEventsThenResult(t *testing.T) {
	e := newEnv(t)
	id, _ := addUIInstance(t, e)
	got := e.uiStream(t, id, "echo", map[string]string{"text": "hi"})
	event := strings.Index(got, "event: event\ndata: {\"text\":\"hi\"}\n\n")
	result := strings.Index(got, "event: result\ndata: {\"status\":\"ok\"}\n\n")
	if event < 0 || result < event {
		t.Fatalf("stream: %q", got)
	}
}

func TestUICallSendsNullForAnEmptyResult(t *testing.T) {
	e := newEnv(t)
	id, _ := addUIInstance(t, e)
	if got := e.uiStream(t, id, "empty", nil); got != "event: result\ndata: null\n\n" {
		t.Fatalf("stream: %q", got)
	}
}

func TestUICallReportsPluginError(t *testing.T) {
	e := newEnv(t)
	id, _ := addUIInstance(t, e)
	if got := e.uiStream(t, id, "fail", nil); !strings.Contains(got, "event: error") || !strings.Contains(got, "the panel broke") {
		t.Fatalf("stream: %q", got)
	}
}

func TestUICallHidesCoreErrors(t *testing.T) {
	e := newEnv(t)
	id, _ := addUIInstance(t, e)
	want := "event: error\ndata: {\"message\":\"the plugin failed\"}\n\n"
	for _, method := range []string{"die", "leak"} {
		if got := e.uiStream(t, id, method, nil); got != want {
			t.Fatalf("%s: stream: %q", method, got)
		}
	}
}

func TestUICallDropsOversizedEvent(t *testing.T) {
	e := newEnv(t)
	id, _ := addUIInstance(t, e)
	got := e.uiStream(t, id, "huge", nil)
	if strings.Contains(got, "event: event") || !strings.Contains(got, "event: result") {
		t.Fatalf("stream: %.200q", got)
	}
}

func TestUICallLibraryNeedsGrant(t *testing.T) {
	e := newEnv(t)
	id, _ := addUIInstance(t, e)
	call := map[string]any{"name": "search_cards", "arguments": map[string]any{}}
	if got := e.uiStream(t, id, "library", call); !strings.Contains(got, "Library access is off") {
		t.Fatalf("without the grant: %q", got)
	}
	e.call("PATCH", fmt.Sprintf("/instances/%d", id), map[string]any{"grants": []string{"library.read"}}, 200, nil)
	if got := e.uiStream(t, id, "library", call); !strings.Contains(got, "event: result") {
		t.Fatalf("with the grant: %q", got)
	}
}

func TestUICallRefusesInstanceWithoutUI(t *testing.T) {
	e := newEnv(t)
	id, err := e.s.Registry.CreateInstance(t.Context(), plugins.NewInstance{Plugin: "target", Name: new("App")})
	if err != nil {
		t.Fatal(err)
	}
	e.call("POST", fmt.Sprintf("/instances/%d/ui/call", id), map[string]any{"method": "echo"}, 400, nil)
}

func TestUICallClientDisconnectCancelsPlugin(t *testing.T) {
	e := newEnv(t)
	id, dir := addUIInstance(t, e)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	res := e.startSlowCall(t, ctx, id)
	defer res.Body.Close()
	cancel()
	waitForCancelled(t, dir)
}

func TestUICallServerStopEndsStreamAndCancelsPlugin(t *testing.T) {
	e := newEnv(t)
	id, dir := addUIInstance(t, e)
	stopping := make(chan struct{})
	e.s.Stopping = stopping
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	res := e.startSlowCall(t, ctx, id)
	defer res.Body.Close()
	close(stopping)
	if _, err := io.Copy(io.Discard, res.Body); err != nil {
		t.Fatalf("the stream didn't end: %v", err)
	}
	waitForCancelled(t, dir)
}

func TestUICallRefusesLibraryCallAfterTheRunEnded(t *testing.T) {
	e := newEnv(t)
	id, _ := addUIInstance(t, e)
	e.call("PATCH", fmt.Sprintf("/instances/%d", id), map[string]any{"grants": []string{"library.read"}}, 200, nil)
	e.uiStream(t, id, "remember", nil)
	if got := e.uiStream(t, id, "replay", nil); !strings.Contains(got, "no active run with this token") {
		t.Fatalf("stream: %q", got)
	}
}

// startStalledFlood floods a stream that is never read and returns once the
// plugin has sent every event, which needs core to keep reading them.
func (e *testEnv) startStalledFlood(t *testing.T, ctx context.Context, instance int64, pluginDir string) *http.Response {
	t.Helper()
	res := e.uiRequest(t, ctx, instance, "flood", nil)
	t.Cleanup(func() { res.Body.Close() })
	waitForFile(t, pluginDir, "flooded")
	return res
}

// The write timeout stays at its default here: firing during the test would
// free a blocked read loop and hide the stall.
func TestUICallStalledReaderLeavesPluginUsable(t *testing.T) {
	e := newEnv(t)
	id, dir := addUIInstance(t, e)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	e.startStalledFlood(t, ctx, id, dir)

	echoCtx, echoCancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer echoCancel()
	echo := e.uiRequest(t, echoCtx, id, "echo", "hi")
	defer echo.Body.Close()
	got, err := io.ReadAll(echo.Body)
	if err != nil || !strings.Contains(string(got), "event: result") {
		t.Fatalf("stream: %q, err %v", got, err)
	}
}

func TestUICallWriteTimeoutAbortsStalledStream(t *testing.T) {
	setUIWriteTimeout(t, 200*time.Millisecond)
	e := newEnv(t)
	id, dir := addUIInstance(t, e)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	res := e.startStalledFlood(t, ctx, id, dir)
	time.Sleep(2 * uiWriteTimeout)
	// Only an aborted write leaves the chunked body without its terminator.
	if _, err := io.Copy(io.Discard, res.Body); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("read the stream: %v, want a truncated body", err)
	}
}

func TestPluginListShowsUIPanelAndPermissions(t *testing.T) {
	e := newEnv(t)
	addUIInstance(t, e)
	var list []pluginOut
	e.call("GET", "/plugins", nil, 200, &list)
	byName := map[string]pluginOut{}
	for _, p := range list {
		byName[p.Name] = p
	}
	panel := byName["panelled"]
	if panel.UIURL != "/api/plugin-ui/panelled/index.html" || panel.UITitle != "Panel" ||
		!slices.Equal(panel.Permissions, []string{protocol.PermissionPanel, protocol.PermissionLibraryRead}) {
		t.Fatalf("ui plugin = %+v", panel)
	}
	if plain := byName["vault"]; plain.UIURL != "" || plain.UITitle != "" || len(plain.Permissions) != 0 {
		t.Fatalf("plain plugin = %+v", plain)
	}
}

func TestUIInstanceTakesThePanelGrant(t *testing.T) {
	e := newEnv(t)
	id, _ := addUIInstance(t, e)
	path := fmt.Sprintf("/instances/%d", id)
	var inst plugins.InstanceInfo
	e.call("PATCH", path, map[string]any{"grants": []string{protocol.PermissionPanel}}, 200, &inst)
	if !slices.Equal(inst.Grants, []string{protocol.PermissionPanel}) {
		t.Fatalf("grants = %v", inst.Grants)
	}
	// The panelled plugin doesn't declare cards.edit.
	e.call("PATCH", path, map[string]any{"grants": []string{protocol.PermissionCardsEdit}}, 400, nil)
}
