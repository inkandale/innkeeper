package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/inkandale/innkeeper/internal/auth"
	"github.com/inkandale/innkeeper/internal/blobstore"
	"github.com/inkandale/innkeeper/internal/jobs"
	"github.com/inkandale/innkeeper/internal/library"
	"github.com/inkandale/innkeeper/internal/pluginhost"
	"github.com/inkandale/innkeeper/internal/plugins"
	"github.com/inkandale/innkeeper/internal/secrets"
	"github.com/inkandale/innkeeper/internal/targets"
	"github.com/inkandale/innkeeper/internal/testdb"
	"github.com/inkandale/innkeeper/internal/testutil"
	"github.com/inkandale/innkeeper/internal/tier"
	"github.com/inkandale/innkeeper/protocol"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

type testEnv struct {
	t     *testing.T
	s     *Server
	srv   *httptest.Server
	pool  *pgxpool.Pool
	token string
	// manifests is the registry's own map, so tests can add plugins to it.
	manifests map[string]*protocol.Manifest
	// loginID is a fake login provider whose owner subject is "owner".
	loginID int64
	// importID is a fake plugin instance that can import.
	importID int64
}

// newEnv serves the API over a fresh database, with the fake plugin and a
// "vault" plugin that has an api_key secret but is never run.
func newEnv(t *testing.T) *testEnv {
	t.Helper()
	failedLoginDelay = 0
	watchInterval = 20 * time.Millisecond
	ctx := context.Background()
	pool := testdb.New(t)
	store := &blobstore.Store{Name: "library", Root: t.TempDir()}
	box, err := secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	fake, err := protocol.LoadManifest(testutil.FakePluginWith(t, []string{"auth", "import", "push", "pull", "codec", "mcp"}))
	if err != nil {
		t.Fatal(err)
	}
	vault := &protocol.Manifest{Name: "vault", Version: "1.0.0", Protocol: protocol.Version,
		Command: []string{"./vault"}, Capabilities: []string{"push"}, Secrets: []string{"api_key"}, MultipleInstances: true}
	// target is the fake plugin as a target app only.
	target := *fake
	target.Name, target.Capabilities = "target", []string{"push", "pull"}
	target.Command = []string{fake.Command[0], "-caps=push,pull", "-name=target"}
	manifests := map[string]*protocol.Manifest{"fake": fake, "vault": vault, "target": &target}
	reg := plugins.New(pool, manifests, store, box)
	mgr := pluginhost.NewManager(NoCore, time.Minute, quiet)
	t.Cleanup(mgr.Close)
	lib := library.New(pool, store)
	e := &testEnv{t: t, pool: pool, manifests: manifests, s: &Server{Lib: lib, Queue: jobs.New(pool), Registry: reg, Auth: auth.New(pool),
		Targets: targets.New(pool, lib, reg, mgr, quiet), Plugins: mgr, Log: quiet}}
	// Keeps tests offline: the default source is on GitHub.
	if _, err := pool.Exec(ctx, `DELETE FROM plugin_source`); err != nil {
		t.Fatal(err)
	}
	e.srv = httptest.NewServer(e.s.Handler())
	t.Cleanup(e.srv.Close)
	if _, e.token, err = e.s.Auth.CreateToken(ctx, "test", tier.Destructive); err != nil {
		t.Fatal(err)
	}
	owner := "owner"
	if e.loginID, err = reg.CreateInstance(ctx, plugins.NewInstance{Plugin: "fake", Name: new("Login"), OwnerSubject: &owner}); err != nil {
		t.Fatal(err)
	}
	if e.importID, err = reg.CreateInstance(ctx, plugins.NewInstance{Plugin: "fake", Name: new("Importer")}); err != nil {
		t.Fatal(err)
	}
	return e
}

type result struct {
	Status int
	Header http.Header
	Body   []byte
}

func (r result) String() string { return fmt.Sprintf("%d %s", r.Status, r.Body) }

// cookie returns the cookie named name that the response set.
func (r result) cookie(t *testing.T, name string) *http.Cookie {
	t.Helper()
	for _, line := range r.Header.Values("Set-Cookie") {
		if c, err := http.ParseSetCookie(line); err == nil && c.Name == name {
			return c
		}
	}
	t.Fatalf("response set no %s cookie: %v", name, r.Header.Values("Set-Cookie"))
	return nil
}

// client returns redirects to the test instead of following them. The
// timeout ends a job stream that never finishes.
var client = &http.Client{
	Timeout:       30 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// apiPath adds /api to a path the Huma API serves. OAuth, MCP, and login
// callbacks stay at the root.
func apiPath(p string) string {
	for _, root := range []string{"/oauth/", "/.well-known/", "/mcp"} {
		if strings.HasPrefix(p, root) {
			return p
		}
	}
	if strings.HasPrefix(p, "/auth/") && strings.Contains(p, "/callback") {
		return p
	}
	return "/api" + p
}

// send makes a request without credentials. A []byte body is sent as
// application/octet-stream, anything else as JSON. headers are name, value
// pairs.
func (e *testEnv) send(method, path string, body any, headers ...string) result {
	e.t.Helper()
	var r io.Reader
	contentType := "application/json"
	switch b := body.(type) {
	case nil:
	case []byte:
		r, contentType = bytes.NewReader(b), "application/octet-stream"
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			e.t.Fatal(err)
		}
		r = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, e.srv.URL+apiPath(path), r)
	if err != nil {
		e.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", contentType)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := client.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		e.t.Fatal(err)
	}
	return result{resp.StatusCode, resp.Header, raw}
}

// call sends a request with the API token and fails the test unless the
// status is want. If out is not nil, it decodes the response body into out.
func (e *testEnv) call(method, path string, body any, want int, out any) result {
	e.t.Helper()
	r := e.send(method, path, body, "Authorization", "Bearer "+e.token)
	if r.Status != want {
		e.t.Fatalf("%s %s: got %s, want %d", method, path, r, want)
	}
	if out != nil {
		if err := json.Unmarshal(r.Body, out); err != nil {
			e.t.Fatalf("%s %s: decode %s: %v", method, path, r.Body, err)
		}
	}
	return r
}

// markBroken marks an instance broken at its current version.
func (e *testEnv) markBroken(id int64, cause string) {
	e.t.Helper()
	ctx := context.Background()
	inst, err := e.s.Registry.Instance(ctx, id)
	if err != nil {
		e.t.Fatal(err)
	}
	if err := e.s.Registry.MarkBroken(ctx, id, inst.Version, errors.New(cause)); err != nil {
		e.t.Fatal(err)
	}
}
