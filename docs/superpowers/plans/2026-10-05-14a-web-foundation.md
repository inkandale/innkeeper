# Web UI Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Serve an embedded React UI at `/` with the API moved to `/api`, a login page, the app shell, and theme plugins.

**Architecture:** The Huma API gets its own mux, mounted at `/api/` with `http.StripPrefix`. The root mux keeps the OAuth, MCP, and login callback routes, and sends everything else to a `web` package that serves the Vite build from `embed.FS`. Theme plugins are manifests with a stylesheet and no command. The selected theme is a row in a new `setting` table, served at `/api/theme.css`.

**Tech Stack:** Go 1.27, Huma v2.39, Vite, React, TypeScript, React Router, TanStack Query, `openapi-typescript`, `openapi-fetch`, Vitest.

**Spec:** `docs/superpowers/specs/2026-10-05-web-ui-design.md`, "Serving and routes", "Themes", and "Shell". Build order phase 1.

---

## Design notes

- **Routing.** Go's `ServeMux` picks the most specific pattern, so `/api/`, `/mcp`, `/oauth/…`, `/.well-known/…`, and `GET /auth/{id}/callback` win over the UI's `/`.
- **MCP** gets the inner API mux, so its tools keep calling `/cards/…` with no prefix.
- **Huma docs.** Setting `Servers: [{URL: "/api"}]` makes Huma's `/docs` page load `/api/openapi.yaml`, through its `getAPIPrefix`.
- **The spec without a database.** `api.Spec()` registers every route on a throwaway mux with a zero `Server`. Route registration doesn't touch the server's fields; only handlers do.
- **A missing build.** `web/dist/.gitkeep` is committed, so `//go:embed all:dist` compiles before `npm run build` has run. Without `index.html`, the UI handler answers `503` with a hint. Go tests and `go build` never need Node.
- **Themes are not instances.** A theme plugin has no process and no settings, so `CreateInstance` refuses it. Selecting one stores its name.
- **Theme order.** Vite puts the app's `<link>` at the end of `<head>`, so `main.tsx` appends the theme's `<link>` at runtime, after it.
- **CSP.** `style-src 'self'` blocks `style="…"` attributes in HTML, but not React's `style` prop, which sets styles through the CSSOM.
- **401s.** One `openapi-fetch` middleware sends the browser to `/login?next=…` on any `401`. `next` must be a same-site path.
- **Who am I.** `GET /api/auth/me` returns the caller's tier, so the shell can check the session on load.

## Files

| File | Change |
|---|---|
| `internal/api/server.go` | Split mux, `/api` mount, `config()`, `routes()`, `Spec()`, `UI` field |
| `internal/api/api_test.go` | `send` adds `/api` to API paths |
| `internal/api/jobs_test.go` | Raw event-stream URLs gain `/api` |
| `internal/api/routing_test.go` | New: prefix, docs, UI fallback |
| `cmd/innkeeper/client.go`, `commands_test.go` | CLI adds `/api` |
| `cmd/innkeeper/openapi.go`, `main.go` | New `openapi` command |
| `protocol/manifest.go`, `manifest_test.go` | `theme` capability, `stylesheet` key |
| `internal/contract/contract.go` | Theme plugins skip the process checks |
| `internal/db/migrations/0010_settings.sql` | New `setting` table |
| `internal/plugins/plugins.go`, `plugins_test.go` | Refuse theme instances; `Theme`, `SetTheme` |
| `internal/api/settings.go`, `settings_test.go` | New: settings, theme CSS, `auth/me` |
| `web/embed.go`, `web/handler.go`, `web/handler_test.go` | New: embedded UI handler |
| `cmd/innkeeper/api.go` | Pass the UI handler |
| `web/` Vite project | New: shell, login, API client, tokens |
| `Dockerfile`, `.github/workflows/ci.yml`, `.gitignore` | Node build stage, web CI job |
| `README.md`, `docs/protocol.md` | `/api` URLs, the UI, theme plugins |

---

### Task 1: Mount the API under `/api`

**Files:**
- Modify: `internal/api/server.go:110-137`
- Modify: `internal/api/api_test.go` (the `send` function)
- Modify: `internal/api/jobs_test.go:140,180`
- Create: `internal/api/routing_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/api/routing_test.go`:

```go
package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestAPIIsUnderPrefix(t *testing.T) {
	e := newEnv(t)
	var spec struct {
		Servers []struct{ URL string } `json:"servers"`
		Paths   map[string]any       `json:"paths"`
	}
	r := e.send("GET", "/openapi.json", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("GET /api/openapi.json: %s", r)
	}
	if err := json.Unmarshal(r.Body, &spec); err != nil {
		t.Fatal(err)
	}
	if len(spec.Servers) != 1 || spec.Servers[0].URL != "/api" {
		t.Fatalf("servers = %+v", spec.Servers)
	}
	if _, ok := spec.Paths["/cards"]; !ok {
		t.Fatal("spec paths should not include the /api prefix")
	}
	// The docs page must load the spec from under the prefix.
	if docs := e.send("GET", "/docs", nil); !strings.Contains(string(docs.Body), "/api/openapi") {
		t.Fatalf("docs page doesn't reference /api/openapi: %s", docs.Body)
	}
	// The unprefixed path is no longer the API.
	resp, err := client.Get(e.srv.URL + "/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET /openapi.json = %d, want 404 with no UI", resp.StatusCode)
	}
}
```

- [ ] **Step 2: Make `send` add the prefix**

In `internal/api/api_test.go`, add this function above `send`:

```go
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
```

and change the request line in `send` from:

```go
	req, err := http.NewRequest(method, e.srv.URL+path, r)
```

to:

```go
	req, err := http.NewRequest(method, e.srv.URL+apiPath(path), r)
```

Add `"strings"` to the imports if it isn't there.

In `internal/api/jobs_test.go`, change both event-stream URLs (lines 140 and 180) from `"%s/jobs/%d/events"` to `"%s/api/jobs/%d/events"`.

- [ ] **Step 3: Run the tests to see them fail**

Run: `go test ./internal/api/ -count=1 2>&1 | tail -20`
Expected: FAIL. Nearly every test gets `404`, because the API still lives at the root.

- [ ] **Step 4: Split the mux**

In `internal/api/server.go`, replace `Handler()` (lines 110-137) with:

```go
// config is the API's Huma config. Its server URL makes the docs page load
// the spec from under /api.
func config() huma.Config {
	cfg := huma.DefaultConfig("Innkeeper", "0.1.0")
	cfg.Servers = []*huma.Server{{URL: "/api"}}
	cfg.Components.SecuritySchemes = map[string]*huma.SecurityScheme{
		"token":   {Type: "http", Scheme: "bearer"},
		"session": {Type: "apiKey", In: "cookie", Name: sessionCookie},
	}
	cfg.Security = []map[string][]string{{"token": {}}, {"session": {}}}
	return cfg
}

// routes registers every API operation.
func (s *Server) routes(api huma.API) {
	s.authRoutes(api)
	s.entityRoutes(api, s.Lib.Cards, "cards", "card", "Cards")
	s.cardRoutes(api)
	s.entityRoutes(api, s.Lib.Lorebooks, "lorebooks", "lorebook", "Lorebooks")
	s.organizeRoutes(api)
	s.searchRoutes(api)
	s.instanceRoutes(api)
	s.jobRoutes(api)
	s.targetRoutes(api)
	s.toolRoutes(api)
}

// Handler returns the HTTP handler for every route: the API under /api,
// OAuth and MCP at the root, and UI for everything else.
func (s *Server) Handler() http.Handler {
	s.loginSlot = make(chan struct{}, 1)
	apiMux := http.NewServeMux()
	api := humago.New(apiMux, config())
	api.UseMiddleware(s.authenticate(api))
	s.routes(api)

	mux := http.NewServeMux()
	mux.Handle("/api/", http.StripPrefix("/api", apiMux))
	// Browsers arrive here from a login provider; it isn't part of the API.
	mux.HandleFunc("GET /auth/{id}/callback", s.callback)
	s.oauthRoutes(mux)
	mux.Handle("/mcp", mcp.New(apiMux, s.Auth, s.Log, s.resourceMetadataURL()))
	if s.UI != nil {
		mux.Handle("/", s.UI)
	}
	return mux
}
```

`s.Lib.Cards` is read during registration, so `routes` needs a non-nil `Lib`. Task 3 handles that for `Spec()`.

Add the `UI` field to `Server`, after `Stopping`:

```go
	// UI serves the web UI for every path the API, OAuth, and MCP don't.
	// Nil serves none.
	UI http.Handler
```

- [ ] **Step 5: Run the tests to see them pass**

Run: `go test ./internal/api/ ./internal/mcp/ -count=1 2>&1 | tail -20`
Expected: PASS. If a test fails with `404`, it builds a URL without `send`; give it the `/api` prefix as in Step 2.

- [ ] **Step 6: Commit**

```bash
git add internal/api/server.go internal/api/api_test.go internal/api/jobs_test.go internal/api/routing_test.go
git commit -m "feat(api): serve the API under /api"
```

---

### Task 2: Point the CLI at `/api`

**Files:**
- Modify: `cmd/innkeeper/client.go:20-27`
- Modify: `cmd/innkeeper/commands_test.go` (both `httptest.NewServer` calls)

- [ ] **Step 1: Make the test servers require the prefix**

In `cmd/innkeeper/commands_test.go`, wrap each of the two test handlers in `http.StripPrefix("/api", …)`. A request without the prefix then gets `404`, and the test fails. For example, `TestPushCmd` becomes:

```go
	srv := httptest.NewServer(http.StripPrefix("/api", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got = append(got, r.URL.Path+" "+string(body))
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"id": 7}`))
	})))
```

Make the same change to the second server, in `TestSourcesCmd`.

- [ ] **Step 2: Run the tests to see them fail**

Run: `go test ./cmd/innkeeper/ -run 'TestPushCmd|TestSourcesCmd' -count=1`
Expected: FAIL with `404`.

- [ ] **Step 3: Add the prefix in `newClient`**

In `cmd/innkeeper/client.go`, replace `newClient` with:

```go
func newClient() (*client, error) {
	root := strings.TrimSuffix(os.Getenv("INNKEEPER_URL"), "/")
	c := &client{base: root + "/api", token: os.Getenv("INNKEEPER_TOKEN")}
	if root == "" || c.token == "" {
		return nil, errors.New("set INNKEEPER_URL and INNKEEPER_TOKEN; make a token with: innkeeper token create <name> [read|write|destructive]")
	}
	return c, nil
}
```

Update the comment on `client` to read:

```go
// client calls the HTTP API under INNKEEPER_URL/api with INNKEEPER_TOKEN.
```

- [ ] **Step 4: Run the tests to see them pass**

Run: `go test ./cmd/innkeeper/ -count=1`
Expected: PASS. `client_test.go` builds `client{base: srv.URL}` directly, so it doesn't change.

- [ ] **Step 5: Commit**

```bash
git add cmd/innkeeper/client.go cmd/innkeeper/commands_test.go
git commit -m "feat(cli): call the API under /api"
```

---

### Task 3: `innkeeper openapi`

**Files:**
- Modify: `internal/api/server.go` (add `Spec`)
- Modify: `internal/api/routing_test.go`
- Create: `cmd/innkeeper/openapi.go`
- Modify: `cmd/innkeeper/main.go` (usage and dispatch)

- [ ] **Step 1: Write the failing test**

Append to `internal/api/routing_test.go`:

```go
func TestSpecNeedsNoServices(t *testing.T) {
	b, err := Spec()
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Servers []struct{ URL string } `json:"servers"`
		Paths   map[string]any       `json:"paths"`
	}
	if err := json.Unmarshal(b, &spec); err != nil {
		t.Fatal(err)
	}
	if spec.Servers[0].URL != "/api" || spec.Paths["/search"] == nil {
		t.Fatalf("spec = %s", b[:min(len(b), 300)])
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./internal/api/ -run TestSpecNeedsNoServices -count=1`
Expected: FAIL to compile: `undefined: Spec`.

- [ ] **Step 3: Write `Spec`**

Append to `internal/api/server.go`:

```go
// Spec returns the OpenAPI spec as indented JSON, without a database or
// plugins.
func Spec() ([]byte, error) {
	api := humago.New(http.NewServeMux(), config())
	s := &Server{Lib: &library.Library{}}
	s.routes(api)
	return json.MarshalIndent(api.OpenAPI(), "", "  ")
}
```

- [ ] **Step 4: Run it to see it pass**

Run: `go test ./internal/api/ -run TestSpecNeedsNoServices -count=1`
Expected: PASS. If it panics on a nil field during registration, give that field a zero value in `Spec` the way `Lib` gets one.

- [ ] **Step 5: Add the command**

Create `cmd/innkeeper/openapi.go`:

```go
package main

import (
	"os"

	"github.com/inkandale/innkeeper/internal/api"
)

// openapi prints the API's OpenAPI spec, for generating clients.
func openapi() error {
	b, err := api.Spec()
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(append(b, '\n'))
	return err
}
```

In `cmd/innkeeper/main.go`, add `  innkeeper openapi` to `usage` after `  innkeeper api`, and add this case after the `api` case in `run`:

```go
	case cmd == "openapi" && len(rest) == 0:
		return openapi()
```

- [ ] **Step 6: Check it runs**

Run: `go run ./cmd/innkeeper openapi | head -5`
Expected: the start of a JSON document, with `"openapi": "3.1.0"`.

- [ ] **Step 7: Commit**

```bash
git add internal/api/server.go internal/api/routing_test.go cmd/innkeeper/openapi.go cmd/innkeeper/main.go
git commit -m "feat(cli): print the OpenAPI spec"
```

---

### Task 4: The embedded UI handler

**Files:**
- Create: `web/embed.go`, `web/handler.go`, `web/handler_test.go`, `web/dist/.gitkeep`
- Modify: `.gitignore`
- Modify: `internal/api/routing_test.go`
- Modify: `cmd/innkeeper/api.go`

- [ ] **Step 1: Write the failing test**

Create `web/handler_test.go`:

```go
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
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./web/ -count=1`
Expected: FAIL to compile: `undefined: Handler`.

- [ ] **Step 3: Write the handler**

Create `web/handler.go`:

```go
// Package web serves the built web UI.
package web

import (
	"errors"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

const csp = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data: blob:; " +
	"connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'"

// Handler serves files from fsys, and index.html for any other path so the
// UI's router can handle it. Files under assets/ have hashed names and are
// cached for a year.
func Handler(fsys fs.FS) http.Handler {
	files := http.FileServerFS(fsys)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", csp)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if strings.HasPrefix(name, "assets/") {
			if _, err := fs.Stat(fsys, name); err != nil {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			files.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		if st, err := fs.Stat(fsys, name); err == nil && !st.IsDir() && name != "index.html" {
			files.ServeHTTP(w, r)
			return
		}
		index, err := fs.ReadFile(fsys, "index.html")
		if errors.Is(err, fs.ErrNotExist) {
			http.Error(w, "The web UI isn't built. Run npm run build in web/.", http.StatusServiceUnavailable)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(index)
	})
}
```

`index.html` is written directly rather than through the file server, because `http.FileServer` redirects `/index.html` to `/`.

Create `web/embed.go`:

```go
package web

import (
	"embed"
	"io/fs"
)

// dist holds the Vite build. It's empty, apart from .gitkeep, until
// npm run build has run.
//
//go:embed all:dist
var dist embed.FS

// Dist is the built UI.
func Dist() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}
```

Create the placeholder and ignore the rest of the build:

```bash
mkdir -p web/dist && touch web/dist/.gitkeep
printf '/web/dist/*\n!/web/dist/.gitkeep\n/web/node_modules/\n' >> .gitignore
```

- [ ] **Step 4: Run it to see it pass**

Run: `go test ./web/ -count=1`
Expected: PASS.

- [ ] **Step 5: Test the UI behind the API's mux**

Append to `internal/api/routing_test.go`:

```go
func TestUIServesNonAPIPaths(t *testing.T) {
	e := newEnv(t)
	e.s.UI = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ui")) })
	srv := httptest.NewServer(e.s.Handler())
	defer srv.Close()
	for path, want := range map[string]string{"/cards/12": "ui", "/": "ui", "/login": "ui"} {
		resp, err := client.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if string(body) != want {
			t.Errorf("GET %s = %q, want %q", path, body, want)
		}
	}
	// The API and the login callback still win.
	resp, err := client.Get(srv.URL + "/api/cards/12")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("GET /api/cards/12 = %d, want 401", resp.StatusCode)
	}
}
```

Add `"io"` and `"net/http/httptest"` to the test file's imports.

Run: `go test ./internal/api/ -run TestUIServesNonAPIPaths -count=1`
Expected: PASS.

- [ ] **Step 6: Wire it into `innkeeper api`**

In `cmd/innkeeper/api.go`, import `"github.com/inkandale/innkeeper/web"` and add `UI: web.Handler(web.Dist()),` to the `api.Server` literal, after `Stopping: stopping`.

Run: `go build ./... && go vet ./cmd/... ./web/`
Expected: no output.

- [ ] **Step 7: Commit**

```bash
git add web/embed.go web/handler.go web/handler_test.go web/dist/.gitkeep .gitignore internal/api/routing_test.go cmd/innkeeper/api.go
git commit -m "feat(web): serve the embedded UI at the root"
```

---

### Task 5: Theme plugins in the manifest

**Files:**
- Modify: `protocol/manifest.go`
- Modify: `protocol/manifest_test.go`
- Modify: `internal/contract/contract.go:40-45`

- [ ] **Step 1: Write the failing tests**

Append to `protocol/manifest_test.go`:

```go
const themeManifest = `
name = "dusk"
version = "1.0.0"
protocol = 1
capabilities = ["theme"]
stylesheet = "theme.css"
`

func TestThemeManifest(t *testing.T) {
	dir := writeManifest(t, themeManifest)
	if err := os.WriteFile(filepath.Join(dir, "theme.css"), []byte(":root{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := LoadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !m.IsTheme() || m.StylesheetPath() != filepath.Join(m.Dir, "theme.css") {
		t.Fatalf("manifest = %+v", m)
	}
}

func TestThemeManifestErrors(t *testing.T) {
	tests := []struct{ name, body, want string }{
		{"missing file", themeManifest, "stylesheet"},
		{"with command", themeManifest + `command = ["./x"]` + "\n", "a theme plugin has no command"},
		{"no stylesheet", strings.Replace(themeManifest, `stylesheet = "theme.css"`, "", 1), "a theme plugin needs a stylesheet"},
		{"outside folder", strings.Replace(themeManifest, `"theme.css"`, `"../theme.css"`, 1), "outside the plugin folder"},
		{"not css", strings.Replace(themeManifest, `"theme.css"`, `"theme.js"`, 1), "must end in .css"},
		{"mixed", strings.Replace(themeManifest, `["theme"]`, `["theme", "push"]`, 1), "theme can't be combined"},
		{"stylesheet without theme", validManifest[:strings.Index(validManifest, "[config]")] + `stylesheet = "a.css"` + "\n", "stylesheet needs the theme capability"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadManifest(writeManifest(t, tt.body))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./protocol/ -run Theme -count=1`
Expected: FAIL to compile: `m.IsTheme undefined`.

- [ ] **Step 3: Add the capability and key**

In `protocol/manifest.go`:

Add `"theme"` to the end of `Capabilities`:

```go
var Capabilities = []string{"import", "search", "push", "pull", "action", "enrich", "codec", "auth", "mcp", "theme"}
```

Add the field to `Manifest`, after `Database`:

```go
	// Stylesheet is a theme plugin's CSS file, inside the plugin folder.
	Stylesheet string `toml:"stylesheet"`
```

Add `"os"` to the imports. In `LoadManifest`, after `Validate` succeeds and before `return &m, nil`:

```go
	if m.IsTheme() {
		if _, err := os.Stat(m.StylesheetPath()); err != nil {
			return nil, fmt.Errorf("manifest %s: stylesheet: %w", abs, err)
		}
	}
```

In `Validate`, replace the `command` check:

```go
	if len(m.Command) == 0 || m.Command[0] == "" {
		errs = append(errs, errors.New("command is required"))
	} else if m.Dir != "" && !filepath.IsAbs(m.Command[0]) && strings.Contains(m.Command[0], "/") {
		if rel, err := filepath.Rel(m.Dir, m.CommandPath()); err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
			errs = append(errs, fmt.Errorf("command %q is outside the plugin folder", m.Command[0]))
		}
	}
```

with:

```go
	if m.IsTheme() {
		errs = append(errs, m.validateTheme()...)
	} else if len(m.Command) == 0 || m.Command[0] == "" {
		errs = append(errs, errors.New("command is required"))
	} else if m.Dir != "" && !filepath.IsAbs(m.Command[0]) && strings.Contains(m.Command[0], "/") {
		if !inside(m.Dir, m.CommandPath()) {
			errs = append(errs, fmt.Errorf("command %q is outside the plugin folder", m.Command[0]))
		}
	}
	if !m.IsTheme() && m.Stylesheet != "" {
		errs = append(errs, errors.New("stylesheet needs the theme capability"))
	}
```

Append to the file:

```go
// IsTheme reports whether the plugin is a theme: a stylesheet and no process.
func (m *Manifest) IsTheme() bool { return slices.Contains(m.Capabilities, "theme") }

// StylesheetPath is the theme's CSS file.
func (m *Manifest) StylesheetPath() string { return filepath.Join(m.Dir, m.Stylesheet) }

func (m *Manifest) validateTheme() []error {
	var errs []error
	if len(m.Capabilities) > 1 {
		errs = append(errs, errors.New("theme can't be combined with other capabilities"))
	}
	if len(m.Command) > 0 {
		errs = append(errs, errors.New("a theme plugin has no command"))
	}
	switch {
	case m.Stylesheet == "":
		errs = append(errs, errors.New("a theme plugin needs a stylesheet"))
	case !strings.HasSuffix(m.Stylesheet, ".css"):
		errs = append(errs, fmt.Errorf("stylesheet %q must end in .css", m.Stylesheet))
	case filepath.IsAbs(m.Stylesheet) || (m.Dir != "" && !inside(m.Dir, m.StylesheetPath())):
		errs = append(errs, fmt.Errorf("stylesheet %q is outside the plugin folder", m.Stylesheet))
	}
	return errs
}

// inside reports whether path is in dir.
func inside(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, "../")
}
```

`filepath.Join` cleans `..`, so `StylesheetPath` of `"../theme.css"` resolves outside the folder and `inside` catches it.

- [ ] **Step 4: Run them to see them pass**

Run: `go test ./protocol/ -count=1`
Expected: PASS, including the existing manifest tests.

- [ ] **Step 5: Skip the process checks for themes**

In `internal/contract/contract.go`, in `Check`, right after the manifest loads:

```go
	m, err := protocol.LoadManifest(dir)
	if err != nil {
		return nil, err
	}
	// A theme has no process; loading the manifest checked its stylesheet.
	if m.IsTheme() {
		return []Result{{"theme stylesheet is readable", nil}}, nil
	}
```

Run: `go test ./internal/contract/ -count=1`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add protocol/manifest.go protocol/manifest_test.go internal/contract/contract.go
git commit -m "feat(protocol): add theme plugins"
```

---

### Task 6: Store the selected theme

**Files:**
- Create: `internal/db/migrations/0010_settings.sql`
- Modify: `internal/plugins/plugins.go`
- Modify: `internal/plugins/plugins_test.go`

- [ ] **Step 1: Write the failing test**

In `internal/plugins/plugins_test.go`, change `newRegistry` to also install a theme:

```go
func newRegistry(t *testing.T) (*Registry, *blobstore.Store) {
	t.Helper()
	store := &blobstore.Store{Name: "library", Root: t.TempDir()}
	box, err := secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	m := &protocol.Manifest{Name: "fake", Version: "0.0.1", Secrets: []string{"api_key", "region"}}
	dusk := &protocol.Manifest{Name: "dusk", Version: "1.0.0", Capabilities: []string{"theme"}, Stylesheet: "theme.css"}
	return New(testdb.New(t), map[string]*protocol.Manifest{"fake": m, "dusk": dusk}, store, box), store
}
```

Append:

```go
func TestTheme(t *testing.T) {
	ctx := context.Background()
	reg, _ := newRegistry(t)
	if name, err := reg.Theme(ctx); err != nil || name != "" {
		t.Fatalf("default theme = %q, %v", name, err)
	}
	if err := reg.SetTheme(ctx, "dusk"); err != nil {
		t.Fatal(err)
	}
	if name, _ := reg.Theme(ctx); name != "dusk" {
		t.Fatalf("theme = %q, want dusk", name)
	}
	for _, bad := range []string{"fake", "missing"} {
		if err := reg.SetTheme(ctx, bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("SetTheme(%q) = %v, want ErrInvalid", bad, err)
		}
	}
	if err := reg.SetTheme(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if name, _ := reg.Theme(ctx); name != "" {
		t.Fatalf("theme after reset = %q", name)
	}
	if _, err := reg.CreateInstance(ctx, NewInstance{Plugin: "dusk", Name: "Dusk"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("theme instance: %v", err)
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./internal/plugins/ -run TestTheme -count=1`
Expected: FAIL to compile: `reg.Theme undefined`.

- [ ] **Step 3: Add the table**

Create `internal/db/migrations/0010_settings.sql`:

```sql
-- Core settings the owner changes at runtime, one row per key.
CREATE TABLE setting (
    key text PRIMARY KEY,
    value text NOT NULL
);
```

- [ ] **Step 4: Add `Theme`, `SetTheme`, and the instance check**

In `internal/plugins/plugins.go`, in `CreateInstance`, after the unknown-plugin check:

```go
	if m.IsTheme() {
		return 0, fmt.Errorf("%w: %s is a theme; select it instead of configuring it", ErrInvalid, n.Plugin)
	}
```

Append:

```go
// Theme is the selected theme plugin's name, or "" for the default theme.
func (r *Registry) Theme(ctx context.Context) (string, error) {
	var name string
	err := r.pool.QueryRow(ctx, `SELECT value FROM setting WHERE key = 'theme'`).Scan(&name)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return name, err
}

// SetTheme selects an installed theme plugin, or the default theme for "".
func (r *Registry) SetTheme(ctx context.Context, name string) error {
	if name == "" {
		_, err := r.pool.Exec(ctx, `DELETE FROM setting WHERE key = 'theme'`)
		return err
	}
	if m, ok := r.manifests[name]; !ok || !m.IsTheme() {
		return fmt.Errorf("%w: %q is not an installed theme", ErrInvalid, name)
	}
	_, err := r.pool.Exec(ctx, `INSERT INTO setting (key, value) VALUES ('theme', $1)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, name)
	return err
}
```

`pgx` and `errors` are already imported in this file.

- [ ] **Step 5: Run the tests to see them pass**

Run: `go test ./internal/plugins/ ./internal/db/ -count=1`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/db/migrations/0010_settings.sql internal/plugins/plugins.go internal/plugins/plugins_test.go
git commit -m "feat(plugins): select a theme"
```

---

### Task 7: Settings, theme CSS, and `auth/me` routes

**Files:**
- Create: `internal/api/settings.go`
- Create: `internal/api/settings_test.go`
- Modify: `internal/api/server.go` (register them)

- [ ] **Step 1: Write the failing tests**

Create `internal/api/settings_test.go`:

```go
package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inkandale/innkeeper/protocol"
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
```

`e.call` sends the test's destructive token.

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/api/ -run 'TestThemeRoutes|TestAuthMe' -count=1`
Expected: FAIL with `404` on `/api/theme.css`.

- [ ] **Step 3: Write the routes**

Create `internal/api/settings.go`:

```go
package api

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/inkandale/innkeeper/internal/tier"
)

type settings struct {
	Theme string `json:"theme" doc:"The selected theme plugin, or empty for the default theme."`
}

type settingsOut struct{ Body settings }

type patchSettingsIn struct {
	Body struct {
		Theme *string `json:"theme,omitempty" doc:"An installed theme plugin's name, or empty for the default theme."`
	}
}

type meOut struct {
	Body struct {
		Tier string `json:"tier" doc:"What the caller's credentials may do."`
	}
}

func (s *Server) settings(ctx context.Context) (*settingsOut, error) {
	name, err := s.Registry.Theme(ctx)
	if err != nil {
		return nil, s.fail(err)
	}
	return &settingsOut{Body: settings{Theme: name}}, nil
}

func (s *Server) settingsRoutes(api huma.API) {
	tags := []string{"Settings"}
	huma.Register(api, huma.Operation{OperationID: "get-settings", Method: http.MethodGet, Path: "/settings",
		Summary: "Get settings", Tags: tags},
		func(ctx context.Context, _ *struct{}) (*settingsOut, error) { return s.settings(ctx) })

	huma.Register(api, huma.Operation{OperationID: "update-settings", Method: http.MethodPatch, Path: "/settings",
		Summary: "Change settings", Tags: tags},
		func(ctx context.Context, in *patchSettingsIn) (*settingsOut, error) {
			if in.Body.Theme != nil {
				if err := s.Registry.SetTheme(ctx, *in.Body.Theme); err != nil {
					return nil, s.fail(err)
				}
			}
			return s.settings(ctx)
		})

	huma.Register(api, huma.Operation{Metadata: needs(tier.Read), OperationID: "get-me", Method: http.MethodGet,
		Path: "/auth/me", Summary: "Check credentials", Description: "Returns 401 without valid credentials.", Tags: []string{"Auth"}},
		func(ctx context.Context, _ *struct{}) (*meOut, error) {
			out := &meOut{}
			out.Body.Tier = callerTier(ctx).String()
			return out, nil
		})
}

// themeRoutes serve theme stylesheets. They're public: the login page uses
// them too, and a stylesheet holds nothing secret.
func (s *Server) themeRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /theme.css", func(w http.ResponseWriter, r *http.Request) {
		name, err := s.Registry.Theme(r.Context())
		if err != nil {
			s.Log.Error("read theme setting", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		// A theme that's been uninstalled falls back to the default.
		s.serveTheme(w, r, name, false)
	})
	mux.HandleFunc("GET /themes/{file}", func(w http.ResponseWriter, r *http.Request) {
		name, ok := strings.CutSuffix(r.PathValue("file"), ".css")
		if !ok {
			http.NotFound(w, r)
			return
		}
		s.serveTheme(w, r, name, true)
	})
}

// serveTheme writes the named theme's stylesheet. An unknown name is a 404
// when strict, and an empty stylesheet otherwise.
func (s *Server) serveTheme(w http.ResponseWriter, r *http.Request, name string, strict bool) {
	m := s.Registry.Manifests()[name]
	if m == nil || !m.IsTheme() {
		if strict {
			http.NotFound(w, r)
			return
		}
		name = ""
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if name == "" {
		return
	}
	f, err := os.Open(m.StylesheetPath())
	if errors.Is(err, fs.ErrNotExist) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.Log.Error("open theme", "theme", name, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.ServeContent(w, r, "", st.ModTime(), f)
}
```

In `internal/api/server.go`, add `s.settingsRoutes(api)` at the end of `routes`, and in `Handler` add this line right after `s.routes(api)`:

```go
	s.themeRoutes(apiMux)
```

`Spec()` calls only `routes`, so the theme routes, which aren't Huma operations, stay out of the spec.

- [ ] **Step 4: Run the tests to see them pass**

Run: `go test ./internal/api/ -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api/settings.go internal/api/settings_test.go internal/api/server.go
git commit -m "feat(api): serve themes and settings, and check credentials"
```

---

### Task 8: Scaffold the Vite project and generated client

**Files:**
- Create: `web/package.json`, `web/package-lock.json`, `web/tsconfig.json`, `web/vite.config.ts`, `web/index.html`
- Create: `web/openapi.json`, `web/src/api/schema.d.ts` (generated)
- Create: `web/src/api/client.ts`, `web/src/api/client.test.ts`
- Create: `web/src/vite-env.d.ts`

- [ ] **Step 1: Create the project and install dependencies**

```bash
cd web
npm init -y
npm pkg set name=innkeeper-web private=true type=module
npm pkg delete main description keywords author license version
npm pkg set scripts.dev="vite" \
  scripts.build="tsc --noEmit && vite build" \
  scripts.test="vitest run" \
  scripts.typecheck="tsc --noEmit" \
  scripts.gen="go run ../cmd/innkeeper openapi > openapi.json && openapi-typescript openapi.json -o src/api/schema.d.ts"
npm install react react-dom react-router @tanstack/react-query openapi-fetch
npm install -D vite @vitejs/plugin-react typescript @types/react @types/react-dom @types/node \
  openapi-typescript vitest jsdom @testing-library/react @testing-library/user-event @testing-library/jest-dom
cd ..
```

Expected: `web/package.json` and `web/package-lock.json` exist, and `web/node_modules/` is ignored by git.

- [ ] **Step 2: Add the config files**

Create `web/tsconfig.json`:

```json
{
  "compilerOptions": {
    "target": "ES2022",
    "lib": ["ES2022", "DOM", "DOM.Iterable"],
    "module": "ESNext",
    "moduleResolution": "Bundler",
    "jsx": "react-jsx",
    "strict": true,
    "noUncheckedIndexedAccess": true,
    "noEmit": true,
    "skipLibCheck": true,
    "types": ["vite/client", "vitest/globals", "@testing-library/jest-dom"]
  },
  "include": ["src", "vite.config.ts"]
}
```

Create `web/vite.config.ts`:

```ts
/// <reference types="vitest/config" />
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// A local `innkeeper api` serves everything that isn't the UI.
const api = process.env.INNKEEPER_URL ?? "http://localhost:8080";

export default defineConfig({
  plugins: [react()],
  server: {
    proxy: Object.fromEntries(["/api", "/auth", "/oauth"].map((p) => [p, api])),
  },
  test: {
    environment: "jsdom",
    globals: true,
    setupFiles: ["./src/test-setup.ts"],
  },
});
```

Create `web/src/test-setup.ts`:

```ts
import "@testing-library/jest-dom/vitest";
```

Create `web/src/vite-env.d.ts`:

```ts
/// <reference types="vite/client" />
```

Create `web/index.html`:

```html
<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <title>Innkeeper</title>
  </head>
  <body>
    <div id="root"></div>
    <script type="module" src="/src/main.tsx"></script>
  </body>
</html>
```

- [ ] **Step 3: Generate the schema**

Run: `cd web && npm run gen && cd ..`
Expected: `web/openapi.json` and `web/src/api/schema.d.ts` exist, and `grep -c '"/cards"' web/src/api/schema.d.ts` prints at least `1`.

- [ ] **Step 4: Write the failing client test**

Create `web/src/api/client.test.ts`:

```ts
import { loginPath, safeNext } from "./client";

test("loginPath keeps where the user was", () => {
  expect(loginPath("/cards", "?tag=elf")).toBe("/login?next=%2Fcards%3Ftag%3Delf");
});

test("loginPath doesn't loop on the login page", () => {
  expect(loginPath("/login", "?next=%2Fcards")).toBe("/login?next=%2Fcards");
});

test.each([
  ["/cards?page=2", "/cards?page=2"],
  [null, "/"],
  ["https://evil.example", "/"],
  ["//evil.example", "/"],
  ["/\\evil.example", "/"],
])("safeNext(%s) = %s", (next, want) => {
  expect(safeNext(next)).toBe(want);
});
```

Run: `cd web && npx vitest run src/api/client.test.ts; cd ..`
Expected: FAIL, because `./client` doesn't exist.

- [ ] **Step 5: Write the client**

Create `web/src/api/client.ts`:

```ts
import createClient, { type Middleware } from "openapi-fetch";
import type { paths } from "./schema";

/** loginPath is the login page URL that returns to path and search. */
export function loginPath(path: string, search: string): string {
  if (path === "/login") return path + search;
  return "/login?next=" + encodeURIComponent(path + search);
}

/** safeNext returns next if it's a path on this site, or "/". */
export function safeNext(next: string | null): string {
  if (!next || !next.startsWith("/") || next.startsWith("//") || next.startsWith("/\\")) return "/";
  return next;
}

// Any 401 means the session is gone, so go log in and come back. A failed
// login is also a 401, so login calls are left to the login page.
const toLogin: Middleware = {
  onResponse({ request, response }) {
    const path = new URL(request.url).pathname;
    const loginCall = path.startsWith("/api/auth/") && path !== "/api/auth/me";
    if (response.status === 401 && !loginCall && window.location.pathname !== "/login") {
      window.location.assign(loginPath(window.location.pathname, window.location.search));
    }
  },
};

// fetch is looked up per call, not bound once, so tests can stub it. The
// base URL is absolute because Request rejects relative URLs outside a browser.
export const api = createClient<paths>({
  baseUrl: new URL("/api", window.location.origin).href,
  fetch: (req) => globalThis.fetch(req),
});
api.use(toLogin);

/** ApiError carries the API's problem detail. */
export class ApiError extends Error {
  constructor(
    readonly status: number,
    detail: string,
  ) {
    super(detail);
  }
}

/** must returns a response's data, or throws its error as an ApiError. */
export function must<T>(res: { data?: T; error?: unknown; response: Response }): T {
  if (res.error !== undefined || res.data === undefined) {
    const detail = (res.error as { detail?: string } | undefined)?.detail ?? res.response.statusText;
    throw new ApiError(res.response.status, detail);
  }
  return res.data;
}
```

Run: `cd web && npx vitest run src/api/client.test.ts; cd ..`
Expected: PASS.

`must` treats an empty body (`data` undefined) as an error. Endpoints that return `204` are called without `must`.

- [ ] **Step 6: Commit**

```bash
git add web/package.json web/package-lock.json web/tsconfig.json web/vite.config.ts web/index.html \
  web/openapi.json web/src/api web/src/vite-env.d.ts web/src/test-setup.ts
git commit -m "feat(web): scaffold the UI and its generated API client"
```

---

### Task 9: Default tokens, shell, and routes

**Files:**
- Create: `web/src/theme/tokens.css`, `web/src/shell/Shell.tsx`, `web/src/shell/Shell.css`, `web/src/shell/Page.tsx`
- Create: `web/src/main.tsx`, `web/src/App.tsx`
- Create: `web/src/shell/Shell.test.tsx`

- [ ] **Step 1: Write the default tokens**

Create `web/src/theme/tokens.css`. Every color, size, and layout choice a theme might change is a custom property here:

```css
:root {
  --bg: #151821;
  --surface: #1a1d29;
  --surface-2: #1e2230;
  --border: #2a2f40;
  --text: #e8e8ef;
  --text-muted: #8a90a4;
  --accent: #7c6cf0;
  --accent-text: #ffffff;
  --ok: #2e9e5b;
  --ahead: #3a7bd5;
  --drift: #d9822b;
  --unknown: #666a78;
  --danger: #d9534f;

  --font: system-ui, -apple-system, "Segoe UI", sans-serif;
  --font-size: 14px;
  --radius: 8px;
  --space: 8px;
  --density: 1;

  --rail-width: 200px;
  --rail-width-collapsed: 56px;
  --tile-aspect: 3 / 4;

  color-scheme: dark;
}

*,
*::before,
*::after {
  box-sizing: border-box;
}

body {
  margin: 0;
  background: var(--bg);
  color: var(--text);
  font: var(--font-size) / 1.45 var(--font);
}

a {
  color: inherit;
}

button,
input,
select,
textarea {
  font: inherit;
  color: inherit;
}

.button {
  background: var(--surface-2);
  border: 1px solid var(--border);
  border-radius: calc(var(--radius) * 0.75);
  padding: calc(var(--space) * 0.5 * var(--density)) calc(var(--space) * 1.25);
  cursor: pointer;
}

.button.primary {
  background: var(--accent);
  border-color: var(--accent);
  color: var(--accent-text);
}

.input {
  width: 100%;
  background: var(--bg);
  border: 1px solid var(--border);
  border-radius: calc(var(--radius) * 0.75);
  padding: calc(var(--space) * 0.75);
}

.error {
  color: var(--danger);
}
```

- [ ] **Step 2: Write the failing shell test**

Create `web/src/shell/Shell.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react";
import { createMemoryRouter, RouterProvider } from "react-router";
import { Shell } from "./Shell";

test("the rail links every section and marks the current one", () => {
  const router = createMemoryRouter(
    [{ path: "/", element: <Shell />, children: [{ path: "cards", element: <p>cards page</p> }] }],
    { initialEntries: ["/cards"] },
  );
  render(<RouterProvider router={router} />);
  for (const name of ["Cards", "Lorebooks", "Collections", "Sync", "Settings"]) {
    expect(screen.getByRole("link", { name })).toBeInTheDocument();
  }
  expect(screen.getByRole("link", { name: "Cards" })).toHaveAttribute("aria-current", "page");
  expect(screen.getByText("cards page")).toBeInTheDocument();
});
```

Run: `cd web && npx vitest run src/shell; cd ..`
Expected: FAIL, because `./Shell` doesn't exist.

- [ ] **Step 3: Write the shell**

Create `web/src/shell/Shell.css`. Layout uses named grid areas, so a theme can move them:

```css
.shell {
  display: grid;
  grid-template-areas: "rail main";
  grid-template-columns: var(--rail-width) 1fr;
  height: 100dvh;
}

.shell.collapsed {
  grid-template-columns: var(--rail-width-collapsed) 1fr;
}

.rail {
  grid-area: rail;
  display: flex;
  flex-direction: column;
  gap: calc(var(--space) * 0.5);
  padding: var(--space);
  background: var(--surface);
  border-right: 1px solid var(--border);
}

.rail-brand {
  font-weight: 700;
  padding: var(--space);
}

.rail a {
  display: flex;
  gap: var(--space);
  align-items: center;
  padding: var(--space);
  border-radius: var(--radius);
  color: var(--text-muted);
  text-decoration: none;
}

.rail a[aria-current="page"] {
  background: var(--surface-2);
  color: var(--text);
}

.rail-bottom {
  margin-top: auto;
}

.shell.collapsed .rail-label,
.shell.collapsed .rail-brand {
  display: none;
}

.main {
  grid-area: main;
  overflow: auto;
  min-width: 0;
}

@media (max-width: 640px) {
  .shell,
  .shell.collapsed {
    grid-template-areas: "main" "rail";
    grid-template-columns: 1fr;
    grid-template-rows: 1fr auto;
  }

  .rail {
    flex-direction: row;
    justify-content: space-around;
    border-right: none;
    border-top: 1px solid var(--border);
  }

  .rail-brand,
  .rail-label,
  .rail-bottom {
    display: none;
  }
}
```

Create `web/src/shell/Shell.tsx`:

```tsx
import { useState } from "react";
import { NavLink, Outlet } from "react-router";
import { api } from "../api/client";
import "./Shell.css";

const sections = [
  { to: "/cards", label: "Cards", icon: "▣" },
  { to: "/lorebooks", label: "Lorebooks", icon: "📖" },
  { to: "/collections", label: "Collections", icon: "🗂" },
  { to: "/sync", label: "Sync", icon: "⇅" },
  { to: "/settings", label: "Settings", icon: "⚙" },
];

async function logout() {
  await api.POST("/auth/logout");
  window.location.assign("/login");
}

export function Shell() {
  const [collapsed, setCollapsed] = useState(false);
  return (
    <div className={collapsed ? "shell collapsed" : "shell"}>
      <nav className="rail" aria-label="Sections">
        <div className="rail-brand">Innkeeper</div>
        {sections.map((s) => (
          <NavLink key={s.to} to={s.to} aria-label={s.label}>
            <span aria-hidden="true">{s.icon}</span>
            <span className="rail-label">{s.label}</span>
          </NavLink>
        ))}
        <div className="rail-bottom">
          <button className="button" onClick={() => setCollapsed(!collapsed)} aria-label="Collapse menu">
            {collapsed ? "»" : "«"}
          </button>{" "}
          {!collapsed && (
            <button className="button" onClick={logout}>
              Log out
            </button>
          )}
        </div>
      </nav>
      <main className="main">
        <Outlet />
      </main>
    </div>
  );
}
```

Create `web/src/shell/Page.tsx`, the heading each section shows until its phase builds it:

```tsx
export function Page({ title }: { title: string }) {
  return (
    <section style={{ padding: "calc(var(--space) * 2)" }}>
      <h1>{title}</h1>
    </section>
  );
}
```

Run: `cd web && npx vitest run src/shell; cd ..`
Expected: PASS.

- [ ] **Step 4: Write the app and entry point**

Create `web/src/App.tsx`:

```tsx
import { useQuery } from "@tanstack/react-query";
import { createBrowserRouter, Navigate, Outlet } from "react-router";
import { api, must } from "./api/client";
import { Login } from "./login/Login";
import { Page } from "./shell/Page";
import { Shell } from "./shell/Shell";

// RequireAuth checks the session. A 401 sends the browser to /login via the
// client's middleware.
function RequireAuth() {
  const me = useQuery({ queryKey: ["me"], queryFn: async () => must(await api.GET("/auth/me")) });
  if (me.isPending) return null;
  if (me.isError) return <p className="error">{me.error.message}</p>;
  return <Outlet />;
}

export const router = createBrowserRouter([
  { path: "/login", element: <Login /> },
  {
    element: <RequireAuth />,
    children: [
      {
        path: "/",
        element: <Shell />,
        children: [
          { index: true, element: <Navigate to="/cards" replace /> },
          { path: "cards", element: <Page title="Cards" /> },
          { path: "lorebooks", element: <Page title="Lorebooks" /> },
          { path: "collections", element: <Page title="Collections" /> },
          { path: "sync", element: <Page title="Sync" /> },
          { path: "settings", element: <Page title="Settings" /> },
        ],
      },
    ],
  },
]);
```

Create `web/src/main.tsx`:

```tsx
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { RouterProvider } from "react-router";
import { router } from "./App";
import "./theme/tokens.css";

// The theme loads after the app's CSS, so its rules win.
const theme = document.createElement("link");
theme.rel = "stylesheet";
theme.href = "/api/theme.css";
document.head.append(theme);

const queryClient = new QueryClient({
  defaultOptions: { queries: { retry: (n, err) => n < 3 && !("status" in err && err.status === 401) } },
});

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  </StrictMode>,
);
```

`App.tsx` imports `./login/Login`, which Task 10 creates, so the type check passes only after Task 10. Commit now and check the build at the end of Task 10.

- [ ] **Step 5: Commit**

```bash
git add web/src/theme web/src/shell web/src/App.tsx web/src/main.tsx
git commit -m "feat(web): add the shell, routes, and default theme tokens"
```

---

### Task 10: Login page

**Files:**
- Create: `web/src/login/Login.tsx`, `web/src/login/Login.css`, `web/src/login/fields.ts`, `web/src/login/fields.test.ts`, `web/src/login/Login.test.tsx`

- [ ] **Step 1: Write the failing field test**

A provider's form is a JSON Schema object. The login page needs only its fields, not the full form renderer that phase 6 builds.

Create `web/src/login/fields.test.ts`:

```ts
import { formFields } from "./fields";

test("formFields lists each property, in order, with its input type", () => {
  const schema = {
    type: "object",
    properties: {
      username: { type: "string", title: "User name" },
      password: { type: "string", format: "password" },
      code: { type: "string", writeOnly: true },
    },
    required: ["password"],
  };
  expect(formFields(schema)).toEqual([
    { name: "username", label: "User name", type: "text", required: false },
    { name: "password", label: "password", type: "password", required: true },
    { name: "code", label: "code", type: "password", required: false },
  ]);
});

test("formFields treats a field named password as secret", () => {
  expect(formFields({ type: "object", properties: { password: { type: "string" } } })[0]?.type).toBe("password");
});

test("formFields returns nothing for a schema without properties", () => {
  expect(formFields({})).toEqual([]);
});
```

Run: `cd web && npx vitest run src/login/fields.test.ts; cd ..`
Expected: FAIL, because `./fields` doesn't exist.

- [ ] **Step 2: Write `formFields`**

Create `web/src/login/fields.ts`:

```ts
export type Field = { name: string; label: string; type: "text" | "password"; required: boolean };

type Schema = {
  properties?: Record<string, { title?: string; format?: string; writeOnly?: boolean }>;
  required?: string[];
};

/** formFields lists a login form schema's fields as text inputs. */
export function formFields(schema: unknown): Field[] {
  const s = schema as Schema;
  return Object.entries(s.properties ?? {}).map(([name, p]) => ({
    name,
    label: p.title ?? name,
    type: p.format === "password" || p.writeOnly || name === "password" ? "password" : "text",
    required: s.required?.includes(name) ?? false,
  }));
}
```

Run: `cd web && npx vitest run src/login/fields.test.ts; cd ..`
Expected: PASS.

- [ ] **Step 3: Write the failing login test**

Create `web/src/login/Login.test.tsx`:

```tsx
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter, RouterProvider } from "react-router";
import { Login } from "./Login";

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

function renderLogin() {
  const router = createMemoryRouter(
    [
      { path: "/login", element: <Login /> },
      { path: "/cards", element: <p>cards page</p> },
    ],
    { initialEntries: ["/login?next=%2Fcards"] },
  );
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
}

afterEach(() => vi.unstubAllGlobals());

test("a form login completes and goes to next", async () => {
  const calls: string[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      calls.push(`${req.method} ${new URL(req.url).pathname}`);
      if (req.url.endsWith("/auth/providers")) return json([{ id: 3, name: "Password", plugin: "fake" }]);
      if (req.url.endsWith("/auth/3/begin"))
        return json({ form: { type: "object", properties: { password: { type: "string" } }, required: ["password"] } });
      if (req.url.endsWith("/auth/3/complete")) {
        expect(await req.json()).toEqual({ params: { password: "hunter2" } });
        // Like the API: an empty 200 says Content-Length: 0.
        return new Response(null, { status: 200, headers: { "Content-Length": "0" } });
      }
      return json({ detail: "unexpected" }, 500);
    }),
  );
  renderLogin();
  await userEvent.type(await screen.findByLabelText("password"), "hunter2");
  await userEvent.click(screen.getByRole("button", { name: "Log in" }));
  expect(await screen.findByText("cards page")).toBeInTheDocument();
  expect(calls).toEqual(["GET /api/auth/providers", "POST /api/auth/3/begin", "POST /api/auth/3/complete"]);
});

test("a failed login shows the error", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      if (req.url.endsWith("/auth/providers")) return json([{ id: 3, name: "Password", plugin: "fake" }]);
      if (req.url.endsWith("/begin")) return json({ form: { type: "object", properties: { password: { type: "string" } } } });
      return json({ status: 401, detail: "login failed" }, 401);
    }),
  );
  renderLogin();
  await userEvent.type(await screen.findByLabelText("password"), "wrong");
  await userEvent.click(screen.getByRole("button", { name: "Log in" }));
  expect(await screen.findByText("login failed")).toBeInTheDocument();
});

test("no providers explains what to do", async () => {
  vi.stubGlobal("fetch", vi.fn(async () => json([])));
  renderLogin();
  expect(await screen.findByText(/No login provider is set up/)).toBeInTheDocument();
});
```

`openapi-fetch` calls `fetch` with a `Request`, so the stub reads `req.url` and `req.method`.

Run: `cd web && npx vitest run src/login/Login.test.tsx; cd ..`
Expected: FAIL, because `./Login` doesn't exist.

- [ ] **Step 4: Write the login page**

Create `web/src/login/Login.css`:

```css
.login {
  max-width: 360px;
  margin: 15vh auto 0;
  padding: calc(var(--space) * 3);
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  display: flex;
  flex-direction: column;
  gap: calc(var(--space) * 1.5);
}

.login h1 {
  margin: 0;
  font-size: 1.4em;
}

.login label {
  display: flex;
  flex-direction: column;
  gap: calc(var(--space) * 0.5);
  color: var(--text-muted);
}
```

Create `web/src/login/Login.tsx`:

```tsx
import { useQuery } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { useNavigate, useSearchParams } from "react-router";
import { api, must, safeNext } from "../api/client";
import { formFields, type Field } from "./fields";
import "./Login.css";

type Provider = { id: number; name: string; plugin: string };

export function Login() {
  const providers = useQuery({
    queryKey: ["login-providers"],
    queryFn: async () => must(await api.GET("/auth/providers")),
  });
  const [chosen, setChosen] = useState<Provider | null>(null);
  const list = providers.data ?? [];
  const provider = chosen ?? (list.length === 1 ? list[0]! : null);

  let body;
  if (providers.isPending) body = null;
  else if (providers.isError) body = <p className="error">{providers.error.message}</p>;
  else if (list.length === 0)
    body = <p>No login provider is set up. The README's "First login" section explains how to add one.</p>;
  else if (!provider)
    body = list.map((p) => (
      <button key={p.id} className="button" onClick={() => setChosen(p)}>
        {p.name}
      </button>
    ));
  else body = <ProviderLogin key={provider.id} provider={provider} />;

  return (
    <main className="login">
      <h1>Innkeeper</h1>
      {body}
    </main>
  );
}

function ProviderLogin({ provider }: { provider: Provider }) {
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const [error, setError] = useState("");
  const begin = useQuery({
    queryKey: ["login-begin", provider.id],
    queryFn: async () => must(await api.POST("/auth/{id}/begin", { params: { path: { id: provider.id } } })),
    staleTime: Infinity,
  });

  if (begin.isPending) return null;
  if (begin.isError) return <p className="error">{begin.error.message}</p>;
  if (begin.data.redirect) {
    window.location.assign(begin.data.redirect);
    return null;
  }
  const fields: Field[] = formFields(begin.data.form);

  async function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const values = Object.fromEntries(
      fields.map((f) => [f.name, String(new FormData(e.currentTarget).get(f.name) ?? "")]),
    );
    const res = await api.POST("/auth/{id}/complete", {
      params: { path: { id: provider.id } },
      body: { params: values },
    });
    if (!res.response.ok) {
      setError((res.error as { detail?: string } | undefined)?.detail ?? res.response.statusText);
      return;
    }
    navigate(safeNext(params.get("next")), { replace: true });
  }

  return (
    <form onSubmit={submit} className="login" style={{ margin: 0, padding: 0, border: "none" }}>
      {fields.map((f) => (
        <label key={f.name}>
          {f.label}
          <input
            className="input"
            name={f.name}
            type={f.type}
            required={f.required}
            autoComplete={f.type === "password" ? "current-password" : "username"}
          />
        </label>
      ))}
      {error && <p className="error">{error}</p>}
      <button className="button primary" type="submit">
        Log in
      </button>
    </form>
  );
}
```

`/auth/{id}/complete` returns `200` with no body, so it's checked through `res.response.ok` rather than `must`.

Run: `cd web && npx vitest run; cd ..`
Expected: PASS for all test files.

- [ ] **Step 5: Check the build**

Run: `cd web && npm run build; cd ..`
Expected: `tsc` reports no errors, and Vite writes `web/dist/index.html` and `web/dist/assets/`.

If `tsc` reports that `begin.data.form` or `begin.data.redirect` don't exist, check the generated names in `web/src/api/schema.d.ts` under `begin-login` and use them.

- [ ] **Step 6: Commit**

```bash
git add web/src/login
git commit -m "feat(web): add the login page"
```

---

### Task 11: Build the UI into the image and check it in CI

**Files:**
- Modify: `Dockerfile`
- Modify: `.github/workflows/ci.yml`

- [ ] **Step 1: Add a Node stage to the Dockerfile**

In `Dockerfile`, add this stage before the `build` stage:

```dockerfile
FROM node:24-alpine AS web
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npx vite build
```

`npx vite build` skips the `tsc` check that `npm run build` runs, because CI already type-checks.

In the `build` stage, after `COPY . .`, copy the built UI over the placeholder:

```dockerfile
COPY --from=web /web/dist ./web/dist
```

- [ ] **Step 2: Build the image**

Run: `docker build --target build -t innkeeper-build-check .`
Expected: success. The `plugins` stage needs an SSH key and is skipped by `--target build`.

- [ ] **Step 3: Add a web job to CI**

In `.github/workflows/ci.yml`, add this job under `jobs:`:

```yaml
  web:
    runs-on: ubuntu-latest
    defaults:
      run:
        working-directory: web
    steps:
      - uses: actions/checkout@v7
        with:
          persist-credentials: false
      - uses: actions/setup-go@v7
        with:
          go-version-file: go.mod
      - uses: actions/setup-node@v6
        with:
          node-version: 24
          cache: npm
          cache-dependency-path: web/package-lock.json
      - run: npm ci
      # The generated client must match the API.
      - run: npm run gen && git diff --exit-code -- openapi.json src/api/schema.d.ts
      - run: npm run typecheck
      - run: npm test
      - run: npx vite build
```

Check the newest major version of `actions/setup-node` and use it if it's newer than `v6`.

- [ ] **Step 4: Commit**

```bash
git add Dockerfile .github/workflows/ci.yml
git commit -m "ci: build and check the web UI"
```

---

### Task 12: Docs

Invoke the `documentation-writing` skill before this task.

**Files:**
- Modify: `README.md`
- Modify: `docs/protocol.md`

- [ ] **Step 1: Update the README's API URLs**

In `README.md`:

- Line 47: the spec is at `/api/openapi.json` and the docs at `/api/docs`.
- Line 81: `curl … "$INNKEEPER_URL/api/tags"`.
- Line 247: `"$INNKEEPER_URL/api/blobs"`.
- Every other `$INNKEEPER_URL/<path>` that names an API route gains `/api`. `/mcp` keeps no prefix.
- `INNKEEPER_URL` stays the site root; say so where the table on line 63 describes it.

Run: `grep -n 'INNKEEPER_URL/' README.md`
Expected: every API path shows `/api/`, and `/mcp` doesn't.

- [ ] **Step 2: Describe the UI in the README**

Add a short "Web UI" section after the API section:

```markdown
## Web UI

`innkeeper api` serves the web UI at its root, for example
`http://localhost:8080/`. Log in with any login provider (see
[First login](#first-login)).

To work on the UI, run `innkeeper api` and then, in `web/`:

    npm install
    npm run dev

Vite serves the UI with hot reload and proxies `/api`, `/auth`, and `/oauth`
to `INNKEEPER_URL`, which defaults to `http://localhost:8080`. After changing
the API, run `npm run gen` to regenerate the client.
```

- [ ] **Step 3: Document theme plugins**

In `docs/protocol.md`:

- Add `theme` to the `capabilities` row of the manifest table.
- Add a row: `` | `stylesheet` | only for `theme` | The theme's CSS file, inside the plugin folder | ``.
- Change the `command` row's Required column to `yes, except for themes`.
- Add a section after the manifest table:

```markdown
### Themes

A theme plugin is a stylesheet with no process. Its manifest lists only the
`theme` capability, sets `stylesheet`, and has no `command`. The UI loads the
selected theme after its own CSS, so the theme can override any custom
property the UI defines on `:root` and any page's `grid-template-areas`.
Themes can't run script.
```

- [ ] **Step 4: Commit**

```bash
git add README.md docs/protocol.md
git commit -m "docs: describe the /api prefix, the web UI, and theme plugins"
```

---

### Task 13: Check it end to end

- [ ] **Step 1: Run every Go test**

Run: `go vet ./... && go test -race ./... 2>&1 | tail -30`
Expected: every package `ok`.

- [ ] **Step 2: Run the web checks**

Run: `cd web && npm run gen && git diff --exit-code -- openapi.json src/api/schema.d.ts && npm run typecheck && npm test && npm run build; cd ..`
Expected: no diff, no type errors, all tests pass, and the build succeeds.

- [ ] **Step 3: Try it in a browser**

Start PostgreSQL with `docker compose up -d` (see `compose.yaml`), then `go run ./cmd/innkeeper migrate` and `go run ./cmd/innkeeper api` with the README's environment variables set. Open `http://localhost:8080/`.

Expected:
- `/` redirects to `/login?next=%2F`, which then lands on `/cards` after logging in.
- The rail shows all five sections, highlights the current one, and collapses.
- `http://localhost:8080/api/docs` shows the API docs and loads the spec.
- Log out returns to `/login`.
- The browser console shows no CSP violations.
