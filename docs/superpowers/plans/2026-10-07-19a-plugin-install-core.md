# Runtime plugin install: core Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A running Innkeeper installs, updates, rolls back, and uninstalls plugins from owner-added sources, without an image rebuild or restart.

**Architecture:** A source is the URL of an `index.toml` that lists plugin versions and a gzipped tarball per platform with its sha256. The registry downloads and verifies a tarball, unpacks it into `INNKEEPER_PLUGINS_DIR/<name>/<source>@<version>/`, and swaps a `current` symlink. Postgres records installs and bumps a sequence; the worker compares that sequence before each job and reloads when it moved. Each instance's process version includes its plugin's install time, so the plugin host starts the new version for new calls and stops the old one once idle. `password-login` and `card-codec` ship in a read-only built-in folder that an install overrides.

**Tech Stack:** Go 1.27 (`archive/tar`, `compress/gzip`, `os.Root`), pgx, Huma, BurntSushi/toml, Helm, Docker.

Ticket: INN-19. Web UI is plan 19b; publishing from innkeeper-plugins is plan 19c.

---

## File structure

| File | Responsibility |
|---|---|
| `internal/db/migrations/0014_plugin_install.sql` | `plugin_source`, `plugin_install`, `plugin_generation` |
| `internal/plugins/plugins.go` | Registry: reloadable manifests, `Open`, `Sync`, `LoadInstalled` |
| `internal/plugins/catalog.go` | Fetch a source's index and artifacts; `Newer` |
| `internal/plugins/install.go` | Sources, `Install`, `Uninstall`, `RemoveData`, `untar` |
| `internal/plugins/database.go` | `dbNames` shared by provisioning and `RemoveData` |
| `internal/testutil/pluginsource.go` | Test helper: a `file://` source with real tarballs |
| `internal/api/pluginstore.go` | Sources, catalog, install, uninstall, remove-data routes |
| `cmd/innkeeper/services.go`, `worker.go`, `api.go` | Wire `plugins.Open`; `Sync` before each job |
| `Dockerfile`, `compose.yaml`, `.github/workflows/release.yml` | Built-in plugins only; `/data` layout; no SSH |
| `chart/` | Plugins PVC at `/data/plugins` |
| `README.md`, `docs/protocol.md`, core design spec | Env vars, forward-read rule, install design |

Decision recorded during planning: the core accepts only plugins whose protocol equals `protocol.Version`. A plugin with another protocol can't install at all, so the "refuse a protocol bump while jobs run" rule from the ticket never applies and has no code.

---

### Task 1: Schema for sources and installs

**Files:**
- Create: `internal/db/migrations/0014_plugin_install.sql`

- [ ] **Step 1: Write the migration**

```sql
-- Where the owner installs plugins from. "builtin" names the image's plugins.
CREATE TABLE plugin_source (
    id text PRIMARY KEY CHECK (id ~ '^[a-z0-9][a-z0-9-]{0,31}$' AND id <> 'builtin'),
    url text NOT NULL UNIQUE CHECK (url ~ '^(https|file)://'),
    created_at timestamptz NOT NULL DEFAULT now()
);

INSERT INTO plugin_source (id, url)
VALUES ('official', 'https://github.com/inkandale/innkeeper-plugins/releases/download/index/index.toml');

-- One row per plugin the owner has installed. Uninstalling nulls the version
-- but keeps the row, so changed_at still gives the plugin's instances a new
-- process version.
CREATE TABLE plugin_install (
    name text PRIMARY KEY,
    source text REFERENCES plugin_source,
    version text,
    sha256 text,
    previous_source text REFERENCES plugin_source ON DELETE SET NULL,
    previous_version text,
    previous_sha256 text,
    changed_at timestamptz NOT NULL DEFAULT now(),
    CHECK ((source IS NULL) = (version IS NULL) AND (version IS NULL) = (sha256 IS NULL))
);

-- Every install and uninstall takes a value, so other processes know to reload.
CREATE SEQUENCE plugin_generation;
```

- [ ] **Step 2: Run the migration tests**

Run: `go test ./internal/db/... ./internal/plugins/...`
Expected: PASS. `testdb.New` migrates every test database, so a syntax error fails here.

- [ ] **Step 3: Commit**

```bash
git add internal/db/migrations/0014_plugin_install.sql
git commit -m "feat: schema for plugin sources and installs"
```

---

### Task 2: Load installed plugins from versioned folders

**Files:**
- Modify: `internal/plugins/plugins.go` (after `LoadDir`)
- Test: `internal/plugins/plugins_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/plugins/plugins_test.go`:

```go
func manifestFor(name, version string) string {
	return fmt.Sprintf("name = %q\nversion = %q\nprotocol = 1\ncommand = [\"./%s\"]\ncapabilities = [\"import\"]\n",
		name, version, name)
}

func TestLoadInstalled(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, filepath.Join(dir, "good", "local@1.0.0"), manifestFor("good", "1.0.0"))
	writeManifest(t, filepath.Join(dir, "good", "local@2.0.0"), manifestFor("good", "2.0.0"))
	if err := os.Symlink("local@2.0.0", filepath.Join(dir, "good", "current")); err != nil {
		t.Fatal(err)
	}
	writeManifest(t, filepath.Join(dir, "nocurrent", "local@1.0.0"), manifestFor("nocurrent", "1.0.0"))
	writeManifest(t, filepath.Join(dir, "misnamed", "local@1.0.0"), manifestFor("other", "1.0.0"))
	if err := os.Symlink("local@1.0.0", filepath.Join(dir, "misnamed", "current")); err != nil {
		t.Fatal(err)
	}

	got, err := LoadInstalled(dir, quiet)
	if err != nil {
		t.Fatal(err)
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got["good"] == nil || got["good"].Version != "2.0.0" {
		t.Fatalf("loaded %v, want only good 2.0.0", got)
	}
	if want := filepath.Join(real, "good", "local@2.0.0"); got["good"].Dir != want {
		t.Fatalf("Dir = %s, want %s", got["good"].Dir, want)
	}

	none, err := LoadInstalled(filepath.Join(dir, "missing"), quiet)
	if err != nil || len(none) != 0 {
		t.Fatalf("missing dir: %v, %v", none, err)
	}
}
```

- [ ] **Step 2: Run it to make sure it fails**

Run: `go test ./internal/plugins -run TestLoadInstalled`
Expected: FAIL with `undefined: LoadInstalled`.

- [ ] **Step 3: Implement**

Add to `internal/plugins/plugins.go` after `LoadDir` (add `"io/fs"` to the imports):

```go
// LoadInstalled loads the plugins the owner installed into dir. Each plugin's
// folder holds one folder per version and a current symlink to one of them.
// A missing dir has no plugins.
func LoadInstalled(dir string, log *slog.Logger) (map[string]*protocol.Manifest, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]*protocol.Manifest{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := map[string]*protocol.Manifest{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		// Resolved, so a running process keeps its own version's folder after current moves.
		cur, err := filepath.EvalSymlinks(filepath.Join(dir, e.Name(), "current"))
		if err != nil {
			log.Error("skip plugin: no current version", "dir", e.Name(), "err", err)
			continue
		}
		m, err := protocol.LoadManifest(cur)
		if err == nil && m.Name != e.Name() {
			err = fmt.Errorf("manifest names %q", m.Name)
		}
		if err != nil {
			log.Error("skip plugin", "dir", e.Name(), "err", err)
			continue
		}
		out[m.Name] = m
	}
	return out, nil
}
```

- [ ] **Step 4: Run it to make sure it passes**

Run: `go test ./internal/plugins -run TestLoadInstalled`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/plugins/plugins.go internal/plugins/plugins_test.go
git commit -m "feat: load installed plugins from versioned folders"
```

---

### Task 3: Reloadable registry

The registry's manifest map becomes swappable under a lock. `Open` loads the built-in and installed folders; `Sync` reloads when `plugin_generation` moved. Instance versions include the plugin's `changed_at`.

**Files:**
- Modify: `internal/plugins/plugins.go`
- Modify: `internal/plugins/database.go`
- Test: `internal/plugins/plugins_test.go`

- [ ] **Step 1: Write the failing tests**

Add to `internal/plugins/plugins_test.go` (add `"github.com/jackc/pgx/v5/pgxpool"` and `"time"` to the imports):

```go
// openRegistry opens a registry over pool with a built-in "login" plugin and
// an empty installed folder, and returns it with its dirs.
func openRegistry(t *testing.T, pool *pgxpool.Pool) (*Registry, Dirs) {
	t.Helper()
	dirs := Dirs{Builtin: t.TempDir(), Installed: t.TempDir()}
	writeManifest(t, filepath.Join(dirs.Builtin, "login"), manifestFor("login", "1.0.0"))
	return openWith(t, pool, dirs), dirs
}

func openWith(t *testing.T, pool *pgxpool.Pool, dirs Dirs) *Registry {
	t.Helper()
	box, err := secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	reg, err := Open(context.Background(), pool, dirs, &blobstore.Store{Name: "library", Root: t.TempDir()}, box, "", quiet)
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

func TestSyncReloadsAfterGeneration(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	reg, dirs := openRegistry(t, pool)
	if m := reg.Manifests()["login"]; m == nil || m.Version != "1.0.0" || !reg.IsBuiltin("login") {
		t.Fatalf("login = %+v, want built-in 1.0.0", m)
	}

	base := filepath.Join(dirs.Installed, "login")
	writeManifest(t, filepath.Join(base, "local@2.0.0"), manifestFor("login", "2.0.0"))
	if err := os.Symlink("local@2.0.0", filepath.Join(base, "current")); err != nil {
		t.Fatal(err)
	}
	if err := reg.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if v := reg.Manifests()["login"].Version; v != "1.0.0" {
		t.Fatalf("reloaded without a new generation: %s", v)
	}
	if _, err := pool.Exec(ctx, `SELECT nextval('plugin_generation')`); err != nil {
		t.Fatal(err)
	}
	if err := reg.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if v := reg.Manifests()["login"].Version; v != "2.0.0" {
		t.Fatalf("after a new generation, login = %s, want the installed 2.0.0", v)
	}
}

func TestInstanceVersionFollowsPluginChange(t *testing.T) {
	ctx := context.Background()
	reg, _ := newRegistry(t)
	id, err := reg.CreateInstance(ctx, NewInstance{Plugin: "fake", Name: "Fake"})
	if err != nil {
		t.Fatal(err)
	}
	before, err := reg.Instance(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reg.pool.Exec(ctx, `INSERT INTO plugin_install (name, changed_at) VALUES ('fake', now() + interval '1 hour')`); err != nil {
		t.Fatal(err)
	}
	after, err := reg.Instance(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := strconv.ParseInt(before.Version, 10, 64)
	a, _ := strconv.ParseInt(after.Version, 10, 64)
	if a <= b {
		t.Fatalf("version %d after a plugin change, want more than %d", a, b)
	}
	// A failure at the old version must not mark the instance broken.
	if err := reg.MarkBroken(ctx, id, before.Version, errors.New("old")); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Instance(ctx, id); err != nil {
		t.Fatalf("stale MarkBroken broke the instance: %v", err)
	}
	if err := reg.MarkBroken(ctx, id, after.Version, errors.New("new")); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Instance(ctx, id); !errors.Is(err, ErrBroken) {
		t.Fatalf("got %v, want ErrBroken", err)
	}
}
```

Add `"strconv"` to the test imports if it's missing.

- [ ] **Step 2: Run them to make sure they fail**

Run: `go test ./internal/plugins -run 'TestSyncReloadsAfterGeneration|TestInstanceVersionFollowsPluginChange'`
Expected: FAIL with `undefined: Dirs`, `undefined: Open`.

- [ ] **Step 3: Make the registry reloadable**

In `internal/plugins/plugins.go`, add `"net/http"` and `"sync"` to the imports, then replace the `Registry` struct and `New` with:

```go
// Registry resolves plugin instances to what the plugin host needs to run
// them.
type Registry struct {
	pool   *pgxpool.Pool
	blobs  *blobstore.Store
	box    *secrets.Box
	dirs   Dirs
	dbURL  string
	log    *slog.Logger
	client *http.Client

	// mu guards the fields below, which a reload replaces.
	mu        sync.RWMutex
	manifests map[string]*protocol.Manifest
	builtin   map[string]*protocol.Manifest
	dsns      map[string]string // by plugin, set by ProvisionDatabases
	gen       int64             // plugin_generation at the last load; -1 before it

	// installMu serializes installs, uninstalls, and data removal.
	installMu sync.Mutex
}

// Dirs are where a Registry finds plugins.
type Dirs struct {
	// Builtin holds the image's plugins, one folder each, or "" for none.
	Builtin string
	// Installed holds the plugins the owner installs.
	Installed string
}

// New returns a Registry for a fixed set of plugins, for tests and tools that
// don't install. blobs is where plugins write new blobs; box seals instance
// secrets.
func New(pool *pgxpool.Pool, manifests map[string]*protocol.Manifest, blobs *blobstore.Store, box *secrets.Box) *Registry {
	return &Registry{pool: pool, manifests: manifests, blobs: blobs, box: box, gen: -1}
}

// Open loads the plugins in dirs and provisions their databases. dbURL is the
// core's database URL, which provisioning needs.
func Open(ctx context.Context, pool *pgxpool.Pool, dirs Dirs, blobs *blobstore.Store, box *secrets.Box, dbURL string, log *slog.Logger) (*Registry, error) {
	if err := os.MkdirAll(dirs.Installed, 0o755); err != nil {
		return nil, err
	}
	r := New(pool, nil, blobs, box)
	r.dirs, r.dbURL, r.log = dirs, dbURL, log
	r.client = &http.Client{
		Timeout: 5 * time.Minute,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if req.URL.Scheme != "https" {
				return errors.New("redirect leaves https")
			}
			if len(via) >= 10 {
				return errors.New("too many redirects")
			}
			return nil
		},
	}
	return r, r.Sync(ctx)
}

// Sync reloads the plugins from disk when an install or uninstall happened
// since the last load, in this process or another.
func (r *Registry) Sync(ctx context.Context) error {
	var gen int64
	err := r.pool.QueryRow(ctx, `SELECT CASE WHEN is_called THEN last_value ELSE 0 END FROM plugin_generation`).Scan(&gen)
	if err != nil {
		return err
	}
	r.mu.RLock()
	same := gen == r.gen
	r.mu.RUnlock()
	if same {
		return nil
	}
	builtin := map[string]*protocol.Manifest{}
	if r.dirs.Builtin != "" {
		if builtin, err = LoadDir(r.dirs.Builtin, r.log); err != nil {
			return err
		}
	}
	installed, err := LoadInstalled(r.dirs.Installed, r.log)
	if err != nil {
		return err
	}
	all := maps.Clone(builtin)
	maps.Copy(all, installed)
	r.mu.Lock()
	r.manifests, r.builtin, r.gen = all, builtin, gen
	r.mu.Unlock()
	return r.ProvisionDatabases(ctx, r.dbURL)
}

func (r *Registry) manifest(name string) (*protocol.Manifest, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	m, ok := r.manifests[name]
	return m, ok
}

// IsBuiltin reports whether the image ships plugin name, so uninstalling it
// falls back to that copy.
func (r *Registry) IsBuiltin(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.builtin[name]
	return ok
}
```

Replace `HasCapability` and `Manifests`:

```go
// HasCapability reports whether plugin is installed and declares capability.
func (r *Registry) HasCapability(plugin, capability string) bool {
	m, ok := r.manifest(plugin)
	return ok && slices.Contains(m.Capabilities, capability)
}

// Manifests returns the installed plugins by name.
func (r *Registry) Manifests() map[string]*protocol.Manifest {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return maps.Clone(r.manifests)
}
```

Replace every remaining `r.manifests[x]` lookup with `r.manifest(x)`: in `CreateInstance`, `UpdateInstance`, `Instance`, `CodecInstance`, and `SetTheme`. Each already reads `m, ok := ...`; only the right-hand side changes. In `CodecInstance` the line becomes:

```go
		if m, ok := r.manifest(i.Plugin); ok && slices.Contains(m.Capabilities, "codec") && slices.Contains(m.Formats, format) {
```

In `Instance`, replace the query and the `DatabaseURL` field:

```go
	err := r.pool.QueryRow(ctx, `SELECT pi.plugin, pi.config, pi.secrets, pi.broken_error,
			greatest(pi.updated_at, coalesce(p.changed_at, pi.updated_at))
		FROM plugin_instance pi LEFT JOIN plugin_install p ON p.name = pi.plugin WHERE pi.id = $1`, id).
		Scan(&plugin, &config, &sealed, &broken, &updated)
```

```go
			DatabaseURL: r.dsn(plugin),
```

and add next to `manifest`:

```go
func (r *Registry) dsn(plugin string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.dsns[plugin]
}
```

Replace the `MarkBroken` statement so it compares against the same version expression:

```go
	_, err = r.pool.Exec(ctx, `UPDATE plugin_instance pi SET broken_error = $2, broken_at = now()
		WHERE id = $1 AND greatest(updated_at,
			coalesce((SELECT changed_at FROM plugin_install WHERE name = pi.plugin), updated_at)) = $3`,
		id, cause.Error(), time.UnixMicro(micros))
```

Update the `Instance.Version` doc comment in `internal/pluginhost/manager.go`:

```go
	// Version is a decimal number that grows whenever the instance's
	// settings or its plugin's install change.
```

- [ ] **Step 4: Lock the database provisioning**

In `internal/plugins/database.go`, change the loop over manifests to use a snapshot, and set `dsns` under the lock. Replace

```go
	for name, m := range r.manifests {
```

with

```go
	for name, m := range r.Manifests() {
```

and replace `r.dsns = dsns` with

```go
	r.mu.Lock()
	r.dsns = dsns
	r.mu.Unlock()
```

Extract the schema and role names, which Task 7 reuses. Add at the bottom of `database.go`:

```go
// dbNames returns plugin's schema and login role in database. Roles are
// cluster-wide, so two databases on one server must not share one.
func dbNames(database, plugin string) (schema, role string) {
	schema = "plugin_" + strings.ReplaceAll(plugin, "-", "_")
	return schema, database + "_" + schema
}
```

and in `ProvisionDatabases` replace

```go
		schema := "plugin_" + strings.ReplaceAll(name, "-", "_")
		// Roles are cluster-wide, so two databases on one server must not share one.
		role := database + "_" + schema
```

with

```go
		schema, role := dbNames(database, name)
```

- [ ] **Step 5: Run the package tests**

Run: `go test -race ./internal/plugins/... ./internal/pluginhost/...`
Expected: PASS, including the two new tests and every existing one.

- [ ] **Step 6: Commit**

```bash
git add internal/plugins internal/pluginhost/manager.go
git commit -m "feat: reload plugins when another process installs one"
```

---

### Task 4: Read a source's index

**Files:**
- Create: `internal/plugins/catalog.go`
- Test: `internal/plugins/catalog_test.go`

- [ ] **Step 1: Write the failing tests**

```go
package plugins

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

const testIndex = `[[plugin]]
name = "good"
version = "1.0.0"
protocol = 1
capabilities = ["import"]
description = "A good plugin."
unknown_key = "ignored"

[[plugin.artifact]]
os = "linux"
arch = "amd64"
url = "good-1.0.0-linux-amd64.tar.gz"
sha256 = "abc"
`

func TestIndex(t *testing.T) {
	ctx := context.Background()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(testIndex))
	}))
	defer srv.Close()
	reg := &Registry{client: srv.Client()}

	got, err := reg.Index(ctx, srv.URL+"/index.toml")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "good" || len(got[0].Artifacts) != 1 || got[0].Artifacts[0].Arch != "amd64" {
		t.Fatalf("got %+v", got)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.toml"), []byte(testIndex), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := reg.Index(ctx, "file://"+filepath.Join(dir, "index.toml")); err != nil || len(got) != 1 {
		t.Fatalf("file source: %+v, %v", got, err)
	}

	if _, err := reg.Index(ctx, "http://example.com/index.toml"); !errors.Is(err, ErrSource) {
		t.Fatalf("http source: got %v, want ErrSource", err)
	}
}

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"1.0.1", "1.0.0", true},
		{"1.10.0", "1.9.0", true},
		{"1.0.0", "1.0.0", false},
		{"1.0", "1.0.1", false},
		{"v2.0.0", "1.9.9", true},
		{"1.0.0-rc1", "1.0.0", false},
	} {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
```

- [ ] **Step 2: Run them to make sure they fail**

Run: `go test ./internal/plugins -run 'TestIndex|TestNewer'`
Expected: FAIL with `undefined: ErrSource`.

- [ ] **Step 3: Implement**

Create `internal/plugins/catalog.go`:

```go
package plugins

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// ErrSource means a plugin source couldn't be read, or what it served is wrong.
var ErrSource = errors.New("plugin source")

// Limits on what Innkeeper downloads from a source.
const (
	maxIndex = 1 << 20
	// ponytail: artifacts are read into memory; stream to disk if plugins outgrow this.
	maxArtifact = 256 << 20
)

// Entry is one plugin version in a source's index.toml. Unknown keys are
// ignored, so a newer index still reads.
type Entry struct {
	Name         string     `toml:"name" json:"name"`
	Version      string     `toml:"version" json:"version"`
	Protocol     int        `toml:"protocol" json:"protocol"`
	Capabilities []string   `toml:"capabilities" json:"capabilities"`
	Description  string     `toml:"description" json:"description"`
	Artifacts    []Artifact `toml:"artifact" json:"-"`
}

// Artifact is an Entry's gzipped tarball for one platform. URL may be
// relative to the index.
type Artifact struct {
	OS     string `toml:"os"`
	Arch   string `toml:"arch"`
	URL    string `toml:"url"`
	SHA256 string `toml:"sha256"`
}

// Index reads the index.toml at rawURL, an https:// or file:// URL.
func (r *Registry) Index(ctx context.Context, rawURL string) ([]Entry, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSource, err)
	}
	b, err := r.fetch(ctx, u, maxIndex)
	if err != nil {
		return nil, err
	}
	var idx struct {
		Plugin []Entry `toml:"plugin"`
	}
	if err := toml.Unmarshal(b, &idx); err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrSource, u.Redacted(), err)
	}
	return idx.Plugin, nil
}

// fetch reads u, up to max bytes.
func (r *Registry) fetch(ctx context.Context, u *url.URL, max int64) ([]byte, error) {
	var body io.ReadCloser
	switch u.Scheme {
	case "https":
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrSource, err)
		}
		resp, err := r.client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrSource, err)
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, fmt.Errorf("%w: GET %s: %s", ErrSource, u.Redacted(), resp.Status)
		}
		body = resp.Body
	case "file":
		f, err := os.Open(u.Path)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrSource, err)
		}
		body = f
	default:
		return nil, fmt.Errorf("%w: %s is not an https:// or file:// URL", ErrSource, u.Redacted())
	}
	defer body.Close()
	b, err := io.ReadAll(io.LimitReader(body, max+1))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSource, err)
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("%w: %s is larger than %d bytes", ErrSource, u.Redacted(), max)
	}
	return b, nil
}

// artifactURL resolves a's URL against its index's, which it must share a
// scheme with: an https index can't point at local files.
func artifactURL(index *url.URL, a Artifact) (*url.URL, error) {
	u, err := index.Parse(a.URL)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSource, err)
	}
	if u.Scheme != index.Scheme {
		return nil, fmt.Errorf("%w: artifact %s must use %s:// like its index", ErrSource, u.Redacted(), index.Scheme)
	}
	return u, nil
}

// Newer reports whether version a is higher than b, comparing dot-separated
// numbers.
// ponytail: ignores anything after "-" or "+"; use x/mod/semver if a source needs pre-releases ordered.
func Newer(a, b string) bool {
	pa, pb := versionParts(a), versionParts(b)
	for i := range max(len(pa), len(pb)) {
		var x, y int
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		if x != y {
			return x > y
		}
	}
	return false
}

func versionParts(v string) []int {
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	var out []int
	for _, s := range strings.Split(v, ".") {
		n, _ := strconv.Atoi(s)
		out = append(out, n)
	}
	return out
}
```

- [ ] **Step 4: Run them to make sure they pass**

Run: `go test ./internal/plugins -run 'TestIndex|TestNewer'`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/plugins/catalog.go internal/plugins/catalog_test.go
git commit -m "feat: read plugin source indexes"
```

---

### Task 5: Test helper for a local plugin source

Both `internal/plugins` and `internal/api` tests need a source with real tarballs.

**Files:**
- Create: `internal/testutil/pluginsource.go`

- [ ] **Step 1: Write the helper**

```go
package testutil

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/inkandale/innkeeper/protocol"
)

// PluginRelease is one plugin version for PluginSource.
type PluginRelease struct {
	Name, Version string
	// Protocol is the manifest's and index's protocol; 0 means protocol.Version.
	Protocol int
	// SHA256 overrides the index's hash; "" lists the tarball's real one.
	SHA256 string
}

// PluginSource writes a tarball per release and an index.toml listing them
// for this platform into a new folder, and returns the index's file:// URL.
func PluginSource(t *testing.T, releases ...PluginRelease) string {
	t.Helper()
	dir := t.TempDir()
	var index strings.Builder
	for _, r := range releases {
		if r.Protocol == 0 {
			r.Protocol = protocol.Version
		}
		tgz := PluginTarball(t, r.Name, r.Version, r.Protocol)
		file := fmt.Sprintf("%s-%s.tar.gz", r.Name, r.Version)
		if err := os.WriteFile(filepath.Join(dir, file), tgz, 0o644); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(tgz)
		hash := hex.EncodeToString(sum[:])
		if r.SHA256 != "" {
			hash = r.SHA256
		}
		fmt.Fprintf(&index, "[[plugin]]\nname = %q\nversion = %q\nprotocol = %d\ncapabilities = [\"import\"]\ndescription = \"Test plugin.\"\n\n"+
			"[[plugin.artifact]]\nos = %q\narch = %q\nurl = %q\nsha256 = %q\n\n",
			r.Name, r.Version, r.Protocol, runtime.GOOS, runtime.GOARCH, file, hash)
	}
	path := filepath.Join(dir, "index.toml")
	if err := os.WriteFile(path, []byte(index.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return "file://" + path
}

// PluginTarball returns a gzipped tarball holding a plugin.toml and a stub
// binary, as innkeeper-plugins publishes them.
func PluginTarball(t *testing.T, name, version string, proto int) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	manifest := fmt.Sprintf("name = %q\nversion = %q\nprotocol = %d\ncommand = [\"./%s\"]\ncapabilities = [\"import\"]\n",
		name, version, proto, name)
	for _, f := range []struct {
		name string
		mode int64
		body string
	}{{"plugin.toml", 0o644, manifest}, {name, 0o755, "#!/bin/sh\n"}} {
		if err := tw.WriteHeader(&tar.Header{Name: f.name, Mode: f.mode, Size: int64(len(f.body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(f.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
```

- [ ] **Step 2: Check it builds**

Run: `go vet ./internal/testutil/`
Expected: no output.

- [ ] **Step 3: Commit**

```bash
git add internal/testutil/pluginsource.go
git commit -m "test: helper for a local plugin source"
```

---

### Task 6: Sources and install

**Files:**
- Create: `internal/plugins/install.go`
- Test: `internal/plugins/install_test.go`

- [ ] **Step 1: Write the failing tests**

```go
package plugins

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/inkandale/innkeeper/internal/testdb"
	"github.com/inkandale/innkeeper/internal/testutil"
)

func folders(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func TestSources(t *testing.T) {
	ctx := context.Background()
	reg, _ := openRegistry(t, testdb.New(t))
	if err := reg.AddSource(ctx, Source{ID: "builtin", URL: "file:///x"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("builtin id: %v", err)
	}
	if err := reg.AddSource(ctx, Source{ID: "web", URL: "http://example.com/index.toml"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("http URL: %v", err)
	}
	if err := reg.AddSource(ctx, Source{ID: "local", URL: "file:///srv/index.toml"}); err != nil {
		t.Fatal(err)
	}
	if err := reg.AddSource(ctx, Source{ID: "local", URL: "file:///other.toml"}); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("duplicate id: %v", err)
	}
	got, err := reg.Sources(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "official" || got[1].ID != "local" {
		t.Fatalf("sources = %+v", got)
	}
	if err := reg.DeleteSource(ctx, "local"); err != nil {
		t.Fatal(err)
	}
	if err := reg.DeleteSource(ctx, "local"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete twice: %v", err)
	}
}

func TestInstallUpdateRollback(t *testing.T) {
	ctx := context.Background()
	reg, dirs := openRegistry(t, testdb.New(t))
	src := testutil.PluginSource(t,
		testutil.PluginRelease{Name: "good", Version: "1.0.0"},
		testutil.PluginRelease{Name: "good", Version: "2.0.0"})
	if err := reg.AddSource(ctx, Source{ID: "local", URL: src}); err != nil {
		t.Fatal(err)
	}

	if err := reg.Install(ctx, "local", "good", "1.0.0"); err != nil {
		t.Fatal(err)
	}
	if m := reg.Manifests()["good"]; m == nil || m.Version != "1.0.0" {
		t.Fatalf("after install: %+v", m)
	}
	if err := reg.Install(ctx, "local", "good", "2.0.0"); err != nil {
		t.Fatal(err)
	}
	ins, err := reg.Installs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if in := ins["good"]; in.Version != "2.0.0" || in.PreviousVersion == nil || *in.PreviousVersion != "1.0.0" {
		t.Fatalf("after update: %+v", in)
	}

	// Rollback reuses the folder on disk, so it works with the source gone.
	if err := os.RemoveAll(filepath.Dir(src[len("file://"):])); err != nil {
		t.Fatal(err)
	}
	if err := reg.Install(ctx, "local", "good", "1.0.0"); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if v := reg.Manifests()["good"].Version; v != "1.0.0" {
		t.Fatalf("after rollback: %s", v)
	}
	got := folders(t, filepath.Join(dirs.Installed, "good"))
	if !slices.Equal(got, []string{"current", "local@1.0.0", "local@2.0.0"}) {
		t.Fatalf("folders = %v", got)
	}
}

func TestInstallRefusesBadArtifacts(t *testing.T) {
	ctx := context.Background()
	reg, dirs := openRegistry(t, testdb.New(t))
	src := testutil.PluginSource(t,
		testutil.PluginRelease{Name: "tampered", Version: "1.0.0", SHA256: "00"},
		testutil.PluginRelease{Name: "future", Version: "1.0.0", Protocol: 99})
	if err := reg.AddSource(ctx, Source{ID: "local", URL: src}); err != nil {
		t.Fatal(err)
	}
	if err := reg.Install(ctx, "local", "tampered", "1.0.0"); !errors.Is(err, ErrSource) {
		t.Fatalf("bad hash: %v", err)
	}
	if err := reg.Install(ctx, "local", "future", "1.0.0"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("other protocol: %v", err)
	}
	if err := reg.Install(ctx, "local", "missing", "1.0.0"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("not in index: %v", err)
	}
	if err := reg.Install(ctx, "local", "../escape", "1.0.0"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad name: %v", err)
	}
	for _, name := range []string{"tampered", "future"} {
		if reg.Manifests()[name] != nil {
			t.Fatalf("%s installed", name)
		}
		if leftovers, _ := os.ReadDir(filepath.Join(dirs.Installed, name)); len(leftovers) != 0 {
			t.Fatalf("%s left %v", name, leftovers)
		}
	}
}

func TestSyncSeesOtherProcessInstall(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	api, dirs := openRegistry(t, pool)
	worker := openWith(t, pool, dirs)
	src := testutil.PluginSource(t, testutil.PluginRelease{Name: "good", Version: "1.0.0"})
	if err := api.AddSource(ctx, Source{ID: "local", URL: src}); err != nil {
		t.Fatal(err)
	}
	if err := api.Install(ctx, "local", "good", "1.0.0"); err != nil {
		t.Fatal(err)
	}
	if err := worker.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if worker.Manifests()["good"] == nil {
		t.Fatal("worker didn't load the plugin the API installed")
	}
}

func TestUntarRefusesEscape(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "../evil", Mode: 0o644, Size: 1, Typeflag: tar.TypeReg})
	tw.Write([]byte("x"))
	tw.Close()
	gz.Close()
	dir := t.TempDir()
	if err := untar(&buf, filepath.Join(dir, "in")); err == nil {
		t.Fatal("untar wrote outside its folder")
	}
	if _, err := os.Stat(filepath.Join(dir, "evil")); err == nil {
		t.Fatal("evil exists")
	}
}
```

Note `openRegistry` (Task 3) gives the registry no `client`, because `Open` sets one. The file sources here don't use it.

`untar` creates its target folder, so the test passes a folder that doesn't exist yet.

- [ ] **Step 2: Run them to make sure they fail**

Run: `go test ./internal/plugins -run 'TestSources|TestInstall|TestSyncSees|TestUntar'`
Expected: FAIL with `undefined: Source`.

- [ ] **Step 3: Implement**

Create `internal/plugins/install.go`:

```go
package plugins

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/inkandale/innkeeper/protocol"
)

// ErrSourceInUse means plugins are still installed from a source.
var ErrSourceInUse = errors.New("plugins are installed from this source; uninstall them first")

var pluginName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// Source is an index.toml that plugins install from.
type Source struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}

// Installed is a plugin the owner installed, and the version a rollback
// returns to.
type Installed struct {
	Name            string  `json:"name"`
	Source          string  `json:"source"`
	Version         string  `json:"version"`
	SHA256          string  `json:"sha256"`
	PreviousSource  *string `json:"previous_source,omitempty"`
	PreviousVersion *string `json:"previous_version,omitempty"`
}

// Sources returns the sources, oldest first.
func (r *Registry) Sources(ctx context.Context) ([]Source, error) {
	rows, err := r.pool.Query(ctx, `SELECT id, url FROM plugin_source ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Source])
}

// AddSource adds an https:// or file:// index to install plugins from.
func (r *Registry) AddSource(ctx context.Context, s Source) error {
	u, err := url.Parse(s.URL)
	if err != nil || !(u.Scheme == "https" && u.Host != "" || u.Scheme == "file" && filepath.IsAbs(u.Path)) {
		return fmt.Errorf("%w: source URL must be https:// or an absolute file:// path", ErrInvalid)
	}
	_, err = r.pool.Exec(ctx, `INSERT INTO plugin_source (id, url) VALUES ($1, $2)`, s.ID, s.URL)
	return writeErr(err)
}

// DeleteSource removes a source that no installed plugin came from.
func (r *Registry) DeleteSource(ctx context.Context, id string) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM plugin_source WHERE id = $1`, id)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23503" {
		return ErrSourceInUse
	}
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Installs returns the owner-installed plugins by name.
func (r *Registry) Installs(ctx context.Context) (map[string]Installed, error) {
	rows, err := r.pool.Query(ctx, `SELECT name, source, version, sha256, previous_source, previous_version
		FROM plugin_install WHERE version IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, pgx.RowToStructByPos[Installed])
	if err != nil {
		return nil, err
	}
	out := map[string]Installed{}
	for _, in := range list {
		out[in.Name] = in
	}
	return out, nil
}

// Install installs version of plugin name from source and makes it current.
// The current and previous versions stay on disk, and installing one of them
// again reuses its folder, so a rollback needs no download.
func (r *Registry) Install(ctx context.Context, source, name, version string) error {
	if !pluginName.MatchString(name) || version == "" || !filepath.IsLocal(version) || strings.ContainsAny(version, `/\`) {
		return fmt.Errorf("%w: plugin %q version %q", ErrInvalid, name, version)
	}
	r.installMu.Lock()
	defer r.installMu.Unlock()
	var srcURL string
	err := r.pool.QueryRow(ctx, `SELECT url FROM plugin_source WHERE id = $1`, source).Scan(&srcURL)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: no source %q", ErrInvalid, source)
	}
	if err != nil {
		return err
	}

	base := filepath.Join(r.dirs.Installed, name)
	folder := source + "@" + version
	sha, err := r.onDisk(ctx, name, source, version, filepath.Join(base, folder))
	if err != nil {
		return err
	}
	if sha == "" {
		if sha, err = r.download(ctx, srcURL, name, version, base, folder); err != nil {
			return err
		}
	}
	// ponytail: disk switches before the database records it; a failed
	// record leaves the new version loaded but unrecorded until the next install.
	if err := switchCurrent(base, folder); err != nil {
		return err
	}
	prev, err := r.record(ctx, source, name, version, sha)
	if err != nil {
		return err
	}
	keep := []string{"current", folder}
	if prev != "" {
		keep = append(keep, prev)
	}
	if err := prune(base, keep); err != nil {
		return err
	}
	return r.Sync(ctx)
}

// onDisk returns the sha256 of name's current or previous install when it is
// source's version and its folder still exists, or "" otherwise.
func (r *Registry) onDisk(ctx context.Context, name, source, version, dir string) (string, error) {
	var sha *string
	err := r.pool.QueryRow(ctx, `SELECT CASE
			WHEN source = $2 AND version = $3 THEN sha256
			WHEN previous_source = $2 AND previous_version = $3 THEN previous_sha256 END
		FROM plugin_install WHERE name = $1`, name, source, version).Scan(&sha)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && sha == nil {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(filepath.Join(dir, "plugin.toml")); err != nil {
		return "", nil
	}
	return *sha, nil
}

// download fetches name's version from the index at srcURL, checks it, and
// unpacks it into base/folder. It returns the tarball's sha256.
func (r *Registry) download(ctx context.Context, srcURL, name, version, base, folder string) (string, error) {
	index, err := url.Parse(srcURL)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrSource, err)
	}
	entries, err := r.Index(ctx, srcURL)
	if err != nil {
		return "", err
	}
	i := slices.IndexFunc(entries, func(e Entry) bool { return e.Name == name && e.Version == version })
	if i < 0 {
		return "", fmt.Errorf("%w: %s %s is not in %s", ErrInvalid, name, version, index.Redacted())
	}
	e := entries[i]
	if e.Protocol != protocol.Version {
		return "", fmt.Errorf("%w: %s %s speaks protocol %d; this Innkeeper speaks %d",
			ErrInvalid, name, version, e.Protocol, protocol.Version)
	}
	j := slices.IndexFunc(e.Artifacts, func(a Artifact) bool { return a.OS == runtime.GOOS && a.Arch == runtime.GOARCH })
	if j < 0 {
		return "", fmt.Errorf("%w: %s %s has no build for %s/%s", ErrInvalid, name, version, runtime.GOOS, runtime.GOARCH)
	}
	a := e.Artifacts[j]
	u, err := artifactURL(index, a)
	if err != nil {
		return "", err
	}
	body, err := r.fetch(ctx, u, maxArtifact)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(body)
	sha := hex.EncodeToString(sum[:])
	if !strings.EqualFold(sha, a.SHA256) {
		return "", fmt.Errorf("%w: %s has sha256 %s; the index lists %s", ErrSource, u.Redacted(), sha, a.SHA256)
	}

	if err := os.MkdirAll(base, 0o755); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(base, ".install-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	if err := untar(bytes.NewReader(body), tmp); err != nil {
		return "", fmt.Errorf("%w: %s: %v", ErrSource, u.Redacted(), err)
	}
	m, err := protocol.LoadManifest(tmp)
	if err != nil {
		return "", fmt.Errorf("%w: %s: %v", ErrSource, u.Redacted(), err)
	}
	if m.Name != name || m.Version != version || m.Protocol != protocol.Version {
		return "", fmt.Errorf("%w: %s holds %s %s protocol %d, not what the index lists", ErrSource, u.Redacted(), m.Name, m.Version, m.Protocol)
	}
	dst := filepath.Join(base, folder)
	if err := os.RemoveAll(dst); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, dst); err != nil {
		return "", err
	}
	return sha, nil
}

// record saves the install and returns the previous version's folder name,
// or "" if there is none.
func (r *Registry) record(ctx context.Context, source, name, version, sha string) (string, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	// The old current version becomes the previous one, unless it is this same install.
	const same = `(plugin_install.source IS NOT DISTINCT FROM EXCLUDED.source AND plugin_install.version IS NOT DISTINCT FROM EXCLUDED.version)`
	var prevSource, prevVersion *string
	err = tx.QueryRow(ctx, `INSERT INTO plugin_install (name, source, version, sha256) VALUES ($1, $2, $3, $4)
		ON CONFLICT (name) DO UPDATE SET
			previous_source = CASE WHEN `+same+` THEN plugin_install.previous_source ELSE plugin_install.source END,
			previous_version = CASE WHEN `+same+` THEN plugin_install.previous_version ELSE plugin_install.version END,
			previous_sha256 = CASE WHEN `+same+` THEN plugin_install.previous_sha256 ELSE plugin_install.sha256 END,
			source = EXCLUDED.source, version = EXCLUDED.version, sha256 = EXCLUDED.sha256, changed_at = now()
		RETURNING previous_source, previous_version`, name, source, version, sha).Scan(&prevSource, &prevVersion)
	if err != nil {
		return "", err
	}
	// A new version may fix what broke the plugin's instances.
	if _, err := tx.Exec(ctx, `UPDATE plugin_instance SET broken_error = NULL, broken_at = NULL WHERE plugin = $1`, name); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `SELECT nextval('plugin_generation')`); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	if prevSource == nil || prevVersion == nil {
		return "", nil
	}
	return *prevSource + "@" + *prevVersion, nil
}

// switchCurrent points base/current at folder, replacing the link in one rename.
func switchCurrent(base, folder string) error {
	tmp := filepath.Join(base, ".current-new")
	if err := os.Remove(tmp); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Symlink(folder, tmp); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(base, "current"))
}

// prune removes everything in base but keep.
func prune(base string, keep []string) error {
	entries, err := os.ReadDir(base)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !slices.Contains(keep, e.Name()) {
			if err := os.RemoveAll(filepath.Join(base, e.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

// untar unpacks a gzipped tarball of regular files and folders into dir,
// creating it. Links and paths that leave dir are refused.
func untar(r io.Reader, dir string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		name := path.Clean(h.Name)
		if !filepath.IsLocal(name) {
			return fmt.Errorf("tarball path %q leaves the folder", h.Name)
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := root.MkdirAll(name, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := root.MkdirAll(path.Dir(name), 0o755); err != nil {
				return err
			}
			f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644|os.FileMode(h.Mode)&0o111)
			if err != nil {
				return err
			}
			n, err := io.Copy(f, io.LimitReader(tr, maxArtifact+1))
			if cerr := f.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				return err
			}
			if n > maxArtifact {
				return fmt.Errorf("tarball file %q is larger than %d bytes", h.Name, maxArtifact)
			}
		default:
			return fmt.Errorf("tarball entry %q is not a file or folder", h.Name)
		}
	}
}
```

In `writeErr` (in `plugins.go`), a `plugin_source` id or URL check failure already maps to `ErrInvalid` (code 23514), and a duplicate id or URL to `ErrNameTaken` (23505).

- [ ] **Step 4: Run them to make sure they pass**

Run: `go test -race ./internal/plugins/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/plugins/install.go internal/plugins/install_test.go
git commit -m "feat: install plugins from sources with hash checks and rollback"
```

---

### Task 7: Uninstall and remove data

**Files:**
- Modify: `internal/plugins/install.go`
- Test: `internal/plugins/install_test.go`, `internal/plugins/database_test.go`

- [ ] **Step 1: Write the failing tests**

Add to `internal/plugins/install_test.go`:

```go
func TestUninstallKeepsInstances(t *testing.T) {
	ctx := context.Background()
	reg, dirs := openRegistry(t, testdb.New(t))
	src := testutil.PluginSource(t,
		testutil.PluginRelease{Name: "good", Version: "1.0.0"},
		testutil.PluginRelease{Name: "login", Version: "2.0.0"})
	if err := reg.AddSource(ctx, Source{ID: "local", URL: src}); err != nil {
		t.Fatal(err)
	}
	if err := reg.Install(ctx, "local", "good", "1.0.0"); err != nil {
		t.Fatal(err)
	}
	id, err := reg.CreateInstance(ctx, NewInstance{Plugin: "good", Name: "Good"})
	if err != nil {
		t.Fatal(err)
	}

	if err := reg.Uninstall(ctx, "good"); err != nil {
		t.Fatal(err)
	}
	if reg.Manifests()["good"] != nil {
		t.Fatal("still loaded")
	}
	if _, err := os.Stat(filepath.Join(dirs.Installed, "good")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("folder still there: %v", err)
	}
	if _, err := reg.Instance(ctx, id); !errors.Is(err, ErrUnknownPlugin) {
		t.Fatalf("instance of uninstalled plugin: %v", err)
	}
	if err := reg.Install(ctx, "local", "good", "1.0.0"); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Instance(ctx, id); err != nil {
		t.Fatalf("reinstall didn't bring the instance back: %v", err)
	}

	// A built-in plugin can't be uninstalled, but an install over it can.
	if err := reg.Uninstall(ctx, "login"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("uninstall built-in: %v", err)
	}
	if err := reg.Install(ctx, "local", "login", "2.0.0"); err != nil {
		t.Fatal(err)
	}
	if err := reg.Uninstall(ctx, "login"); err != nil {
		t.Fatal(err)
	}
	if v := reg.Manifests()["login"].Version; v != "1.0.0" {
		t.Fatalf("after uninstalling the override, login = %s, want the built-in 1.0.0", v)
	}
	if err := reg.Uninstall(ctx, "never"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("uninstall unknown: %v", err)
	}
}
```

Add to `internal/plugins/database_test.go`:

```go
func TestRemoveData(t *testing.T) {
	ctx := context.Background()
	reg, pool, dbURL := newDBRegistry(t)
	if err := reg.ProvisionDatabases(ctx, dbURL); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.CreateInstance(ctx, NewInstance{Plugin: "cat-log", Name: "Cats"}); err != nil {
		t.Fatal(err)
	}
	if err := reg.RemoveData(ctx, "cat-log"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("remove data while installed: %v", err)
	}
	delete(reg.manifests, "cat-log")
	if err := reg.RemoveData(ctx, "cat-log"); err != nil {
		t.Fatal(err)
	}
	var instances int
	var schema, role bool
	err := pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM plugin_instance WHERE plugin = 'cat-log'),
		EXISTS (SELECT FROM pg_namespace WHERE nspname = 'plugin_cat_log'),
		EXISTS (SELECT FROM pg_roles WHERE rolname = current_database() || '_plugin_cat_log')`).Scan(&instances, &schema, &role)
	if err != nil {
		t.Fatal(err)
	}
	if instances != 0 || schema || role {
		t.Fatalf("left instances %d, schema %v, role %v", instances, schema, role)
	}
}
```

Add `"errors"` to `database_test.go`'s imports if it's missing.

- [ ] **Step 2: Run them to make sure they fail**

Run: `go test ./internal/plugins -run 'TestUninstallKeepsInstances|TestRemoveData'`
Expected: FAIL with `reg.Uninstall undefined`.

- [ ] **Step 3: Implement**

Add to `internal/plugins/install.go`:

```go
// Uninstall removes an installed plugin's code. Its instances and data stay,
// so reinstalling brings them back. Uninstalling a plugin the image also
// ships falls back to the image's copy; that copy can't be uninstalled.
func (r *Registry) Uninstall(ctx context.Context, name string) error {
	if !pluginName.MatchString(name) {
		return ErrNotFound
	}
	r.installMu.Lock()
	defer r.installMu.Unlock()
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE plugin_install SET source = NULL, version = NULL, sha256 = NULL,
			previous_source = NULL, previous_version = NULL, previous_sha256 = NULL, changed_at = now()
		WHERE name = $1 AND version IS NOT NULL`, name)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		if r.IsBuiltin(name) {
			return fmt.Errorf("%w: %s is built in", ErrInvalid, name)
		}
		return ErrNotFound
	}
	if _, err := tx.Exec(ctx, `SELECT nextval('plugin_generation')`); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	if err := os.RemoveAll(filepath.Join(r.dirs.Installed, name)); err != nil {
		return err
	}
	return r.Sync(ctx)
}

// RemoveData deletes an uninstalled plugin's instances, database schema, and
// role. It can't be undone. It returns ErrInUse while cards are bound to one
// of the instances.
func (r *Registry) RemoveData(ctx context.Context, name string) error {
	if !pluginName.MatchString(name) {
		return ErrNotFound
	}
	r.installMu.Lock()
	defer r.installMu.Unlock()
	if _, ok := r.manifest(name); ok {
		return fmt.Errorf("%w: uninstall %s first", ErrInvalid, name)
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `DELETE FROM plugin_instance WHERE plugin = $1`, name)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.ConstraintName == "binding_instance_id_fkey" {
		return ErrInUse
	}
	if err != nil {
		return err
	}
	var database string
	if err := tx.QueryRow(ctx, `SELECT current_database()`).Scan(&database); err != nil {
		return err
	}
	schema, role := dbNames(database, name)
	var roleExists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT FROM pg_roles WHERE rolname = $1)`, role).Scan(&roleExists); err != nil {
		return err
	}
	stmts := []string{`DROP SCHEMA IF EXISTS ` + pgx.Identifier{schema}.Sanitize() + ` CASCADE`}
	if roleExists {
		rid := pgx.Identifier{role}.Sanitize()
		stmts = append(stmts, `DROP OWNED BY `+rid, `DROP ROLE `+rid)
	}
	for _, q := range stmts {
		if _, err := tx.Exec(ctx, q); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM plugin_install WHERE name = $1`, name); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
```

- [ ] **Step 4: Run them to make sure they pass**

Run: `go test -race ./internal/plugins/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/plugins
git commit -m "feat: uninstall plugins and remove their data separately"
```

---

### Task 8: API routes

**Files:**
- Create: `internal/api/pluginstore.go`
- Modify: `internal/api/server.go` (`routes`, `fail`)
- Modify: `internal/api/instances.go` (`list-plugins`, `pluginOut`)
- Test: `internal/api/pluginstore_test.go`

- [ ] **Step 1: Write the failing test**

```go
package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/inkandale/innkeeper/internal/blobstore"
	"github.com/inkandale/innkeeper/internal/plugins"
	"github.com/inkandale/innkeeper/internal/secrets"
	"github.com/inkandale/innkeeper/internal/testutil"
)

func TestPluginStore(t *testing.T) {
	e := newEnv(t)
	box, err := secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	reg, err := plugins.Open(context.Background(), e.pool, plugins.Dirs{Installed: t.TempDir()},
		&blobstore.Store{Name: "library", Root: t.TempDir()}, box, "", quiet)
	if err != nil {
		t.Fatal(err)
	}
	e.s.Registry = reg
	src := testutil.PluginSource(t,
		testutil.PluginRelease{Name: "good", Version: "1.0.0"},
		testutil.PluginRelease{Name: "good", Version: "2.0.0"})

	// Tests stay offline, so drop the default source.
	e.call(http.MethodDelete, "/plugin-sources/official", nil, http.StatusNoContent, nil)
	e.call(http.MethodPost, "/plugin-sources", map[string]string{"id": "local", "url": src}, http.StatusCreated, nil)
	e.call(http.MethodPost, "/plugin-sources", map[string]string{"id": "web", "url": "http://example.com/i.toml"}, http.StatusBadRequest, nil)

	var catalog []catalogSourceOut
	e.call(http.MethodGet, "/plugin-catalog", nil, http.StatusOK, &catalog)
	if len(catalog) != 1 || len(catalog[0].Plugins) != 1 || catalog[0].Plugins[0].Version != "2.0.0" ||
		len(catalog[0].Plugins[0].Versions) != 2 || catalog[0].Plugins[0].Versions[1] != "1.0.0" {
		t.Fatalf("catalog = %+v", catalog)
	}

	e.call(http.MethodPost, "/plugins/good/install", map[string]string{"source": "local", "version": "1.0.0"}, http.StatusNoContent, nil)
	e.call(http.MethodPost, "/plugins/good/install", map[string]string{"source": "local", "version": "2.0.0"}, http.StatusNoContent, nil)
	var list []pluginOut
	e.call(http.MethodGet, "/plugins", nil, http.StatusOK, &list)
	if len(list) != 1 || list[0].Source != "local" || list[0].Version != "2.0.0" ||
		list[0].PreviousVersion == nil || *list[0].PreviousVersion != "1.0.0" {
		t.Fatalf("plugins = %+v", list)
	}

	e.call(http.MethodDelete, "/plugin-sources/local", nil, http.StatusConflict, nil)
	e.call(http.MethodDelete, "/plugins/good/data", nil, http.StatusBadRequest, nil)
	e.call(http.MethodDelete, "/plugins/good", nil, http.StatusNoContent, nil)
	e.call(http.MethodDelete, "/plugins/good/data", nil, http.StatusNoContent, nil)
	e.call(http.MethodDelete, "/plugins/good", nil, http.StatusNotFound, nil)
}
```

- [ ] **Step 2: Run it to make sure it fails**

Run: `go test ./internal/api -run TestPluginStore`
Expected: FAIL with `undefined: catalogSourceOut`.

- [ ] **Step 3: Add the routes**

Create `internal/api/pluginstore.go`:

```go
package api

import (
	"context"
	"net/http"
	"slices"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/inkandale/innkeeper/internal/plugins"
	"github.com/inkandale/innkeeper/internal/tier"
)

type sourcesOut struct{ Body []plugins.Source }

type sourceIn struct{ Body plugins.Source }

type sourceIDIn struct {
	ID string `path:"id"`
}

type pluginNameIn struct {
	Name string `path:"name"`
}

type installIn struct {
	Name string `path:"name"`
	Body struct {
		Source  string `json:"source" minLength:"1"`
		Version string `json:"version" minLength:"1"`
	}
}

// catalogPluginOut is a plugin a source offers: its newest version's details,
// and every version it lists.
type catalogPluginOut struct {
	plugins.Entry
	// Versions is newest first.
	Versions []string `json:"versions"`
}

type catalogSourceOut struct {
	Source  plugins.Source     `json:"source"`
	Plugins []catalogPluginOut `json:"plugins"`
	// Error says why the source couldn't be read; Plugins is empty then.
	Error string `json:"error,omitempty"`
}

type catalogOut struct{ Body []catalogSourceOut }

func (s *Server) pluginStoreRoutes(api huma.API) {
	tags := []string{"Plugins"}
	// Plugins run as code inside Innkeeper, so changing them is owner-level.
	destructive := needs(tier.Destructive)

	huma.Register(api, huma.Operation{OperationID: "list-plugin-sources", Method: http.MethodGet, Path: "/plugin-sources",
		Summary: "List plugin sources", Tags: tags},
		func(ctx context.Context, _ *struct{}) (*sourcesOut, error) {
			list, err := s.Registry.Sources(ctx)
			if err != nil {
				return nil, s.fail(err)
			}
			return &sourcesOut{Body: orEmpty(list)}, nil
		})

	huma.Register(api, huma.Operation{Metadata: destructive, OperationID: "add-plugin-source", Method: http.MethodPost,
		Path: "/plugin-sources", Summary: "Add a plugin source", Tags: tags, DefaultStatus: http.StatusCreated,
		Description: "A source is the https:// or file:// URL of an index.toml. Its plugins run as code inside " +
			"Innkeeper with their own database role, so add only sources you trust."},
		func(ctx context.Context, in *sourceIn) (*struct{}, error) {
			return nil, s.fail(s.Registry.AddSource(ctx, in.Body))
		})

	huma.Register(api, huma.Operation{Metadata: destructive, OperationID: "delete-plugin-source", Method: http.MethodDelete,
		Path: "/plugin-sources/{id}", Summary: "Delete a plugin source", Tags: tags, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *sourceIDIn) (*struct{}, error) {
			return nil, s.fail(s.Registry.DeleteSource(ctx, in.ID))
		})

	huma.Register(api, huma.Operation{OperationID: "plugin-catalog", Method: http.MethodGet, Path: "/plugin-catalog",
		Summary: "Check sources for plugins", Tags: tags,
		Description: "Reads every source's index. It changes nothing."},
		func(ctx context.Context, _ *struct{}) (*catalogOut, error) {
			sources, err := s.Registry.Sources(ctx)
			if err != nil {
				return nil, s.fail(err)
			}
			out := &catalogOut{Body: []catalogSourceOut{}}
			// ponytail: sources are read one at a time; fetch in parallel if owners add many.
			for _, src := range sources {
				entries, err := s.Registry.Index(ctx, src.URL)
				c := catalogSourceOut{Source: src, Plugins: catalog(entries)}
				if err != nil {
					c.Error = err.Error()
				}
				out.Body = append(out.Body, c)
			}
			return out, nil
		})

	huma.Register(api, huma.Operation{Metadata: destructive, OperationID: "install-plugin", Method: http.MethodPost,
		Path: "/plugins/{name}/install", Summary: "Install or update a plugin", Tags: tags, DefaultStatus: http.StatusNoContent,
		Description: "Installs a version from a source and makes it current. Running jobs finish on the old version. " +
			"Installing the previous version rolls back without a download."},
		func(ctx context.Context, in *installIn) (*struct{}, error) {
			return nil, s.fail(s.Registry.Install(ctx, in.Body.Source, in.Name, in.Body.Version))
		})

	huma.Register(api, huma.Operation{Metadata: destructive, OperationID: "uninstall-plugin", Method: http.MethodDelete,
		Path: "/plugins/{name}", Summary: "Uninstall a plugin", Tags: tags, DefaultStatus: http.StatusNoContent,
		Description: "Removes the plugin's code. Its instances and data stay, and come back if it's installed again."},
		func(ctx context.Context, in *pluginNameIn) (*struct{}, error) {
			return nil, s.fail(s.Registry.Uninstall(ctx, in.Name))
		})

	huma.Register(api, huma.Operation{Metadata: destructive, OperationID: "remove-plugin-data", Method: http.MethodDelete,
		Path: "/plugins/{name}/data", Summary: "Remove an uninstalled plugin's data", Tags: tags, DefaultStatus: http.StatusNoContent,
		Description: "Deletes the plugin's instances, database schema, and role. This can't be undone."},
		func(ctx context.Context, in *pluginNameIn) (*struct{}, error) {
			return nil, s.fail(s.Registry.RemoveData(ctx, in.Name))
		})
}

// catalog groups a source's entries by plugin, newest version first.
func catalog(entries []plugins.Entry) []catalogPluginOut {
	byName := map[string]*catalogPluginOut{}
	for _, e := range entries {
		p, ok := byName[e.Name]
		if !ok {
			p = &catalogPluginOut{Entry: e}
			byName[e.Name] = p
		} else if plugins.Newer(e.Version, p.Version) {
			p.Entry = e
		}
		p.Versions = append(p.Versions, e.Version)
	}
	out := []catalogPluginOut{}
	for _, p := range byName {
		slices.SortFunc(p.Versions, func(a, b string) int {
			if plugins.Newer(a, b) {
				return -1
			}
			if plugins.Newer(b, a) {
				return 1
			}
			return 0
		})
		out = append(out, *p)
	}
	slices.SortFunc(out, func(a, b catalogPluginOut) int { return strings.Compare(a.Name, b.Name) })
	return out
}
```

In `internal/api/server.go`, add `s.pluginStoreRoutes(api)` after `s.instanceRoutes(api)` in `routes`. In `fail`, add `errors.Is(err, plugins.ErrSourceInUse)` to the 409 case list, and a new case before the `startErr` case:

```go
	case errors.Is(err, plugins.ErrSource):
		return huma.Error502BadGateway(err.Error())
```

- [ ] **Step 4: Show where each plugin came from**

In `internal/api/instances.go`, add three fields to `pluginOut`:

```go
	// Source is the id of the source the plugin was installed from, or "builtin".
	Source          string  `json:"source"`
	PreviousSource  *string `json:"previous_source,omitempty"`
	PreviousVersion *string `json:"previous_version,omitempty"`
	// Builtin means the image ships the plugin, so it can't be uninstalled
	// past that copy.
	Builtin bool `json:"builtin"`
```

and in the `list-plugins` handler, read the installs before the loop and fill them in:

```go
			installs, err := s.Registry.Installs(ctx)
			if err != nil {
				return nil, s.fail(err)
			}
			out := &pluginsOut{Body: []pluginOut{}}
			for _, m := range s.Registry.Manifests() {
				p := pluginOut{Name: m.Name, Version: m.Version, Capabilities: m.Capabilities,
					Secrets: orEmpty(m.Secrets), Formats: m.Formats, Config: m.Config,
					Source: "builtin", Builtin: s.Registry.IsBuiltin(m.Name)}
				if in, ok := installs[m.Name]; ok {
					p.Source, p.PreviousSource, p.PreviousVersion = in.Source, in.PreviousSource, in.PreviousVersion
				}
```

A plugin from `plugins.New` (tests and tools) has no install row, so it shows as `builtin` with `Builtin: false`.

- [ ] **Step 5: Run the API tests and regenerate the spec**

Run: `go test -race ./internal/api/...`
Expected: PASS

Run: `cd web && npm run gen`
Expected: `web/openapi.json` and `web/src/api/schema.d.ts` gain the new operations.

- [ ] **Step 6: Commit**

```bash
git add internal/api web/openapi.json web/src/api/schema.d.ts
git commit -m "feat: API to manage plugin sources and installs"
```

---

### Task 9: Wire it into the API and worker

**Files:**
- Modify: `cmd/innkeeper/services.go`
- Modify: `cmd/innkeeper/worker.go`
- Modify: `cmd/innkeeper/api.go:47`

- [ ] **Step 1: Open the registry in `openServices`**

In `cmd/innkeeper/services.go`, remove the `manifests` field from `services` and the `protocol` import. Replace the block from `manifests, err := plugins.LoadDir(...)` through the `ProvisionDatabases` call with:

```go
	pool, err := openDB(ctx, maxConnsAtLeast)
	if err != nil {
		return nil, err
	}
	store := &blobstore.Store{Name: "library", Root: blobDir}
	dirs := plugins.Dirs{Builtin: os.Getenv("INNKEEPER_BUILTIN_PLUGINS_DIR"), Installed: cfg["INNKEEPER_PLUGINS_DIR"]}
	registry, err := plugins.Open(ctx, pool, dirs, store, box, os.Getenv("INNKEEPER_DATABASE_URL"), log)
	if err != nil {
		pool.Close()
		return nil, err
	}
```

and drop `manifests: manifests,` from the returned struct. Update the `openServices` doc comment's "loads the installed plugins" to "loads the built-in and installed plugins".

- [ ] **Step 2: Fix the startup log lines**

In `cmd/innkeeper/api.go:47` and `cmd/innkeeper/worker.go:101`, replace `len(svc.manifests)` with `len(svc.registry.Manifests())`.

- [ ] **Step 3: Sync before each job**

In `cmd/innkeeper/worker.go`, after the `w := &jobs.Worker{...}` literal:

```go
	// Plugins installed or removed through the API take effect at the next job.
	for kind, h := range w.Handlers {
		w.Handlers[kind] = func(ctx context.Context, r *jobs.Run) error {
			if err := svc.registry.Sync(ctx); err != nil {
				return err
			}
			return h(ctx, r)
		}
	}
```

- [ ] **Step 4: Build and run every test**

Run: `go vet ./... && go test -race ./...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add cmd/innkeeper
git commit -m "feat: load installed plugins at startup and reload before each job"
```

---

### Task 10: Image, compose, chart, release

The image ships only `password-login` and `card-codec`. Because innkeeper-plugins is public, the build clones it over HTTPS and needs no SSH key.

**Files:**
- Modify: `Dockerfile`
- Modify: `compose.yaml`
- Modify: `.github/workflows/release.yml`
- Modify: `chart/values.yaml`, `chart/templates/pvc.yaml`, `chart/templates/deployment.yaml`

- [ ] **Step 1: Dockerfile**

Replace the `plugins` stage with:

```dockerfile
# The built-in plugins, built against this core's protocol package. Owners
# install the rest at runtime.
FROM golang:1.27-alpine AS plugins
# A branch or commit. CI passes main's commit, so a new one skips the cached clone.
ARG PLUGINS_REF=main
RUN apk add --no-cache git make
RUN git clone https://github.com/inkandale/innkeeper-plugins.git /plugins-src \
    && git -C /plugins-src checkout --detach "$PLUGINS_REF"
COPY . /core
WORKDIR /plugins-src
# ponytail: no go mod tidy, so this needs innkeeper-plugins' go.sum to cover
# everything protocol/ imports; add tidy if a build reports a missing entry.
RUN go mod edit -replace github.com/inkandale/innkeeper=/core \
    && make install DEST=/plugins PLUGINS="password-login card-codec"
```

In the final stage, replace the `mkdir`/`chown` line, the plugins `COPY`, and the `ENV` block:

```dockerfile
    && mkdir -p /data/blobs /data/plugins \
    && chown -R innkeeper /data
COPY --from=build /innkeeper /usr/local/bin/innkeeper
COPY --from=plugins /plugins /usr/lib/innkeeper/plugins
USER innkeeper
ENV INNKEEPER_BLOB_DIR=/data/blobs \
    INNKEEPER_PLUGINS_DIR=/data/plugins \
    INNKEEPER_BUILTIN_PLUGINS_DIR=/usr/lib/innkeeper/plugins \
    INNKEEPER_LISTEN=:8080
```

- [ ] **Step 2: compose.yaml**

Delete the `ssh: [default]` line and its comment. Replace the `- blobs:/data/blobs` volume with `- data:/data`, and the `blobs:` entry under the top-level `volumes:` with `data:`.

- [ ] **Step 3: release.yml**

Replace the `plugins` step with:

```yaml
      # Building main's commit rather than "main" keeps the layer cache from
      # reusing an older clone.
      - id: plugins
        run: |
          ref=$(git ls-remote https://github.com/inkandale/innkeeper-plugins.git refs/heads/main | cut -f1)
          test -n "$ref"
          echo "ref=$ref" >> "$GITHUB_OUTPUT"
```

and delete the `ssh: default=/home/runner/.ssh/plugins` line from `docker/build-push-action`. After the merge, delete the `PLUGINS_DEPLOY_KEY` repository secret.

- [ ] **Step 4: Chart**

In `chart/values.yaml`, under `persistence:`, add:

```yaml
  # Installed plugins, shared by the API and worker containers.
  plugins:
    size: 1Gi
```

In `chart/templates/pvc.yaml`, add after the blobs claim:

```yaml
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: {{ .Release.Name }}-plugins
  labels: {{- include "innkeeper.labels" . | nindent 4 }}
spec:
  accessModes: {{- toYaml .Values.persistence.accessModes | nindent 4 }}
  {{- with .Values.persistence.storageClass }}
  storageClassName: {{ . }}
  {{- end }}
  resources:
    requests:
      storage: {{ .Values.persistence.plugins.size }}
```

In `chart/templates/deployment.yaml`, add to `volumeMounts` after the blobs mount:

```yaml
            - name: plugins
              mountPath: /data/plugins
```

and to `volumes` after the blobs volume:

```yaml
        - name: plugins
          persistentVolumeClaim:
            claimName: {{ .Release.Name }}-plugins
```

- [ ] **Step 5: Check the chart and the stack**

Run: `helm lint chart && helm template t chart | grep -A3 'name: plugins'`
Expected: lint passes; the template shows the plugins mount and claim.

Run: `docker compose build && docker compose up -d && sleep 5 && docker compose logs api | grep "api listening"`
Expected: `plugins=2`. Publish the API on a port other than 8080 (`INNKEEPER_PORT=8090`), which mcpproxy holds locally.

Run: `docker compose down`

- [ ] **Step 6: Commit**

```bash
git add Dockerfile compose.yaml .github/workflows/release.yml chart
git commit -m "feat: ship only built-in plugins and keep installs on a volume"
```

---

### Task 11: Docs

Use the `documentation-writing` skill for these edits.

**Files:**
- Modify: `README.md` (env var table at line 61)
- Modify: `docs/protocol.md`
- Modify: `docs/superpowers/specs/2026-10-03-innkeeper-core-design.md` (line 197 and after "Lifecycle")

- [ ] **Step 1: README env vars**

Replace the `INNKEEPER_PLUGINS_DIR` row and add one for the built-in folder:

```markdown
| `INNKEEPER_PLUGINS_DIR` | `api`, `worker`, `restore` | Writable folder for plugins installed from the Plugins page; `/data/plugins` in the image |
| `INNKEEPER_BUILTIN_PLUGINS_DIR` | `api`, `worker`, `restore` | Read-only folder of plugins the image ships, one subfolder each; an install overrides them |
```

- [ ] **Step 2: Forward-read rule in the protocol reference**

Add to `docs/protocol.md`, in the section on plugin databases:

```markdown
The owner can roll a plugin back to the version before. A plugin that
changes its tables must keep reading what its next version wrote, so a
rollback never strands its data.
```

- [ ] **Step 3: Core design spec**

Replace "The core image bakes in the v1 plugins from a pinned commit of this repo." with:

```markdown
CI publishes each plugin version as a gzipped tarball per platform on a
GitHub Release, and an `index.toml` that lists them with their sha256. The
core image ships only `password-login` and `card-codec`; the owner installs
the rest from the Plugins page.
```

Add a subsection after "Lifecycle":

```markdown
### Install and update

- **Sources.** A source is the https:// or file:// URL of an `index.toml`.
  The official index is there by default; the owner can add others. A
  plugin's identity on the Plugins page is `source/name`, and only one plugin
  per name is installed at a time.
- **Install.** The core downloads the tarball for its platform, checks its
  sha256 against the index, checks the manifest and protocol, and unpacks it
  into `<plugins dir>/<name>/<source>@<version>/`. A `current` symlink
  switches in one rename.
- **Running jobs.** A job already running finishes on the old version. New
  calls start the new one, and the old process stops once idle. The worker
  reloads before each job when an install happened.
- **Rollback.** The current and previous versions stay on disk. Installing
  the previous one switches back without a download.
- **Built-in plugins.** An install overrides the image's copy, and
  uninstalling falls back to it.
- **Uninstall.** Removes the code; instances and the plugin's database stay.
  A separate action removes the data.
```

- [ ] **Step 4: Commit**

```bash
git add README.md docs
git commit -m "docs: runtime plugin install"
```
