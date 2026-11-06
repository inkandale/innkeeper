# Plugin protocol version Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give the plugin protocol a `major.minor` version, so core offers and runs only the plugin versions it can run, and shows the rest as needing a newer Innkeeper.

**Architecture:** `protocol` gains `Minor`, a `protocol_version` manifest key, `CheckVersion`, and `NewerProtocolError`. `plugins.Entry` gains the same key and one `CheckProtocol` method that install and the catalog both use. `LoadInstalled` reports installed plugins that need a newer minor instead of dropping them silently, and `GET /plugins` lists them with `needs_protocol`. The Plugins page shows each version it can't run as "needs a newer Innkeeper", with no button for it. In the plugins repo, `indexcheck` and `publish` carry the key, and forge-ai 0.14.1 declares `1.1`.

**Tech Stack:** Go (Huma, pgx, BurntSushi/toml), React and TypeScript (TanStack Query, Vitest, Testing Library).

Spec: [`docs/superpowers/specs/2026-11-06-plugin-protocol-version-design.md`](../specs/2026-11-06-plugin-protocol-version-design.md).

---

## Decisions

Where the code showed the spec to be silent or ambiguous, the plan takes the simplest reading:

| Topic | Decision |
|---|---|
| Unknown keys in a newer manifest | `LoadManifest` checks `protocol_version` before it reports unknown keys. A plugin for a newer minor may use keys this core doesn't know, such as `[ui] title` on a pre-1.1 core. Without this, a rolled-back core would report "unknown keys" and never "needs a newer Innkeeper". |
| Empty `protocol_version` | `CheckVersion` treats `""` as `<protocol>.0` and accepts it. Parsing is an unexported helper, since nothing outside `protocol` needs the numbers. |
| Tarball and entry match | The two `protocol_version` strings must be equal, as `name` and `version` are. `""` and `"1.0"` count as different; `publish` copies one string into both. |
| What "can run" means | `Entry.CheckProtocol` checks both `protocol` and `protocol_version`. An entry for another major also counts as needing a newer Innkeeper. |
| A plugin with no runnable version | `catalog` leaves it out. There's nothing to offer, and the setup wizard and the Available list would otherwise offer an install that fails. |
| Update notes | The Plugins page shows notes from the offered version down to the installed one. A version it can't run doesn't contribute notes. |
| Listing an installed plugin that needs a newer minor | `GET /plugins` lists it with `needs_protocol`, status `broken`, and no capabilities, secrets, or config. The other pages pick plugins by capability, so they skip it. The Plugins page shows it under its own heading, **Needs a newer Innkeeper**, with Roll back and Uninstall. A built-in copy of the same name wins, as it does today. |
| `indexcheck`'s minor check | No extra code. `indexcheck` loads each manifest with the pinned core's `protocol.LoadManifest`, which refuses a minor above the pinned `Minor`, and the entry must equal the manifest. A test proves it. |
| `publish` | Its `entry` struct gains `protocol_version`. Without it, `publish` drops the key from `entry.toml` and the published index, and core would refuse forge-ai 0.14.1's tarball as not matching its entry. The spec doesn't mention this. |
| forge-ai release notes | The plugins repo's `index.toml` has no `notes` key. `publish -index` builds the notes from commit subjects since `forge-ai-v0.14.0`. These already include `77246ce fix(forge-ai): refresh the Editing line when the page's context changes`. The release commit is a `chore`, so the notes leave it out. |
| Plugins README | Step 2 of "Files to add or change" names `protocol_version`, and says how to set it. |

## Files

Core, `/Users/dgibbons/git/innkeeper-ai-assistant`:

| File | Change |
|---|---|
| `protocol/messages.go` | `Minor` next to `Version` |
| `protocol/version.go` (new) | `CheckVersion`, `NewerProtocolError`, `parseVersion` |
| `protocol/version_test.go` (new) | `CheckVersion` and the `LoadManifest` early check |
| `protocol/manifest.go` | `Manifest.ProtocolVersion`; the early check in `LoadManifest`; `Validate` calls `CheckVersion` |
| `protocol/manifest_test.go` | Two rows in `TestManifestErrors` |
| `internal/plugins/catalog.go` | `Entry.ProtocolVersion`, `Entry.CheckProtocol` |
| `internal/plugins/install.go` | `download` uses `CheckProtocol` and compares the tarball's `protocol_version` |
| `internal/plugins/install_test.go` | Two install tests |
| `internal/testutil/pluginsource.go` | `PluginRelease.ProtocolVersion`, `ManifestProtocolVersion` |
| `internal/plugins/plugins.go` | `LoadInstalled` returns plugins needing a newer minor; `Registry.newer`, `Registry.NeedsNewer` |
| `internal/plugins/plugins_test.go` | `TestLoadInstalled` covers a plugin needing a newer minor |
| `internal/api/pluginstore.go` | `catalog` offers the newest runnable version; `newer` |
| `internal/api/instances.go` | `pluginOut.NeedsProtocol`; `list-plugins` lists plugins needing a newer minor |
| `internal/api/pluginstore_test.go` | `withInstalls` helper and two tests |
| `web/openapi.json`, `web/src/api/schema.d.ts` | Regenerated with `npm run gen`, never by hand |
| `web/src/settings/Plugins.tsx` | "needs a newer Innkeeper" lines, the new group, no button for a version core can't run |
| `web/src/settings/Plugins.test.tsx` | Two tests |
| `docs/protocol.md` | Version `1.1`, a Versions section with its table, the convention, the manifest row, the index bullets |
| `AGENTS.md` | Plugins section: bump `Minor` with each protocol addition |

Plugins, `/Users/dgibbons/git/innkeeper-plugins-ai-assistant`:

| File | Change |
|---|---|
| `go.mod`, `go.sum` | Pin the core commit that adds `protocol.Minor` |
| `internal/indexcheck/main.go`, `main_test.go` | Compare `protocol_version`; test the pinned minor |
| `internal/publish/main.go`, `main_test.go` | Keep `protocol_version` |
| `README.md` | Name `protocol_version` in "Files to add or change" |
| `forge-ai/plugin.toml`, `index.toml` | forge-ai 0.14.1 with `protocol_version = "1.1"` |

## Test commands

Core, from `/Users/dgibbons/git/innkeeper-ai-assistant`. Database tests skip silently without the URL, so always set it:

```bash
export INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres
go vet ./... && go test -race ./...
cd web && npm run typecheck && npx vitest run src/settings/Plugins.test.tsx
```

Plugins, from `/Users/dgibbons/git/innkeeper-plugins-ai-assistant`:

```bash
go vet ./... && go test -race ./...
INNKEEPER_PLUGIN_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres make check
make test-ui
```

`make check` fails 2 card-embed checks on origin/main for a reason unrelated to this work. Expect those 2 failures and no others.

Don't check out, switch, or rebase branches in either worktree. Commit on the branch each worktree is on.

---

## Task 1: `protocol` gets `Minor` and `protocol_version`

**Files:**
- Modify: `protocol/messages.go:9-10`
- Create: `protocol/version.go`
- Create: `protocol/version_test.go`
- Modify: `protocol/manifest.go` (`Manifest` struct ~line 69, `LoadManifest` ~line 170, `Validate` ~line 268)
- Modify: `protocol/manifest_test.go` (`TestManifestErrors` table ~line 118)

- [ ] **Step 1: Write the failing tests**

Create `protocol/version_test.go`:

```go
package protocol

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestCheckVersion(t *testing.T) {
	current := fmt.Sprintf("%d.%d", Version, Minor)
	for _, tt := range []struct {
		version, wantErr string
	}{
		{"", ""},
		{fmt.Sprintf("%d.0", Version), ""},
		{current, ""},
		{"1", "must be <major>.<minor>"},
		{"1.", "must be <major>.<minor>"},
		{"v1.1", "must be <major>.<minor>"},
		{"1.1.0", "must be <major>.<minor>"},
		{"+1.1", "must be <major>.<minor>"},
		{" 1.1", "must be <major>.<minor>"},
		{"99999999999999999999.1", "must be <major>.<minor>"},
		{"2.0", "doesn't match protocol 1"},
	} {
		t.Run(tt.version, func(t *testing.T) {
			err := CheckVersion(Version, tt.version)
			if tt.wantErr == "" && err != nil {
				t.Fatalf("CheckVersion(%q) = %v", tt.version, err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("CheckVersion(%q) = %v, want %q", tt.version, err, tt.wantErr)
			}
		})
	}
}

func TestCheckVersionReportsANewerMinor(t *testing.T) {
	next := fmt.Sprintf("%d.%d", Version, Minor+1)
	err := CheckVersion(Version, next)
	var newer *NewerProtocolError
	if !errors.As(err, &newer) || newer.Needs != next {
		t.Fatalf("CheckVersion(%q) = %v, want a NewerProtocolError", next, err)
	}
	if want := fmt.Sprintf("needs protocol %s; this Innkeeper speaks %d.%d", next, Version, Minor); err.Error() != want {
		t.Fatalf("message = %q, want %q", err, want)
	}
	if err := CheckVersion(Version, "1"); errors.As(err, &newer) {
		t.Fatalf("a malformed version is a NewerProtocolError: %v", err)
	}
}

func TestLoadManifestReadsProtocolVersion(t *testing.T) {
	current := fmt.Sprintf("%d.%d", Version, Minor)
	body := strings.Replace(validManifest, "protocol = 1", "protocol = 1\nprotocol_version = \""+current+"\"", 1)
	m, err := LoadManifest(writeManifest(t, body))
	if err != nil {
		t.Fatal(err)
	}
	if m.ProtocolVersion != current {
		t.Fatalf("ProtocolVersion = %q, want %q", m.ProtocolVersion, current)
	}
}

// A newer minor may add keys this core doesn't know; the error must still say what the plugin needs.
func TestLoadManifestReportsANewerProtocolBeforeUnknownKeys(t *testing.T) {
	body := strings.Replace(validManifest, "protocol = 1", "protocol = 1\nprotocol_version = \"1.99\"\nfrom_the_future = true", 1)
	_, err := LoadManifest(writeManifest(t, body))
	var newer *NewerProtocolError
	if !errors.As(err, &newer) || newer.Needs != "1.99" {
		t.Fatalf("LoadManifest = %v, want a NewerProtocolError needing 1.99", err)
	}
}
```

In `protocol/manifest_test.go`, add two rows to the `TestManifestErrors` table, right after the `"wrong protocol"` row:

```go
		{"malformed protocol_version", `protocol = 1`, "protocol = 1\nprotocol_version = \"1\"", "must be <major>.<minor>"},
		{"protocol_version of another major", `protocol = 1`, "protocol = 1\nprotocol_version = \"2.0\"", "doesn't match protocol 1"},
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./protocol/`
Expected: build failure, `undefined: CheckVersion`, `undefined: NewerProtocolError`, `undefined: Minor`.

- [ ] **Step 3: Add `Minor`**

In `protocol/messages.go`, replace:

```go
// Version is the protocol version this core speaks.
const Version = 1
```

with:

```go
// Version is the protocol major version this core speaks.
const Version = 1

// Minor is the protocol minor version this core speaks. Bump it with each
// addition to the protocol, and add a row to docs/protocol.md's version table.
const Minor = 1
```

- [ ] **Step 4: Write `protocol/version.go`**

```go
package protocol

import (
	"fmt"
	"regexp"
	"strconv"
)

var versionPattern = regexp.MustCompile(`^(\d+)\.(\d+)$`)

// NewerProtocolError means a plugin needs a protocol minor above Minor: a
// newer Innkeeper runs it.
type NewerProtocolError struct {
	// Needs is the plugin's protocol_version, such as "1.2".
	Needs string
}

func (e *NewerProtocolError) Error() string {
	return fmt.Sprintf("needs protocol %s; this Innkeeper speaks %d.%d", e.Needs, Version, Minor)
}

// CheckVersion reports whether this core speaks a plugin's protocol_version,
// given the protocol major the plugin declares beside it. Empty means
// <major>.0. A minor above Minor is a *NewerProtocolError.
func CheckVersion(major int, version string) error {
	if version == "" {
		return nil
	}
	gotMajor, gotMinor, err := parseVersion(version)
	if err != nil {
		return err
	}
	if gotMajor != major {
		return fmt.Errorf("protocol_version %s doesn't match protocol %d", version, major)
	}
	if gotMinor > Minor {
		return &NewerProtocolError{Needs: version}
	}
	return nil
}

// parseVersion splits a protocol_version, "<major>.<minor>" such as "1.1".
func parseVersion(s string) (major, minor int, err error) {
	m := versionPattern.FindStringSubmatch(s)
	if m != nil {
		major, err = strconv.Atoi(m[1])
		if err == nil {
			minor, err = strconv.Atoi(m[2])
		}
	}
	if m == nil || err != nil {
		return 0, 0, fmt.Errorf("protocol_version %q must be <major>.<minor>, such as \"1.1\"", s)
	}
	return major, minor, nil
}
```

- [ ] **Step 5: Add the manifest field and checks**

In `protocol/manifest.go`, in the `Manifest` struct, replace:

```go
	Protocol     int                   `toml:"protocol"`
	Command      []string              `toml:"command"`
```

with:

```go
	Protocol     int                   `toml:"protocol"`
	Command      []string              `toml:"command"`

	// ProtocolVersion is the protocol major.minor the plugin needs, such as
	// "1.1"; empty means <protocol>.0.
	ProtocolVersion string `toml:"protocol_version"`
```

In `LoadManifest`, replace:

```go
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	var unknown []string
```

with:

```go
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	// A newer minor may use keys this core doesn't know, so report what the plugin needs instead.
	var newer *NewerProtocolError
	if err := CheckVersion(m.Protocol, m.ProtocolVersion); errors.As(err, &newer) {
		return nil, fmt.Errorf("manifest %s: %w", abs, err)
	}
	var unknown []string
```

In `Validate`, replace:

```go
	if m.Protocol != Version {
		errs = append(errs, fmt.Errorf("protocol %d is not supported; this core speaks %d", m.Protocol, Version))
	}
```

with:

```go
	if m.Protocol != Version {
		errs = append(errs, fmt.Errorf("protocol %d is not supported; this core speaks %d", m.Protocol, Version))
	}
	if err := CheckVersion(m.Protocol, m.ProtocolVersion); err != nil {
		errs = append(errs, err)
	}
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go vet ./protocol/ && go test -race ./protocol/`
Expected: `ok  github.com/inkandale/innkeeper/protocol`

- [ ] **Step 7: Commit**

```bash
git add protocol/messages.go protocol/version.go protocol/version_test.go protocol/manifest.go protocol/manifest_test.go
git commit -m "feat(protocol): version the protocol as major.minor with protocol_version"
```

---

## Task 2: Install refuses a version core can't run

**Files:**
- Modify: `internal/plugins/catalog.go` (`Entry` ~line 29; imports)
- Modify: `internal/plugins/install.go:186-190, 225-227`
- Modify: `internal/testutil/pluginsource.go`
- Test: `internal/plugins/install_test.go`

- [ ] **Step 1: Let test releases declare `protocol_version`**

In `internal/testutil/pluginsource.go`, add two fields to `PluginRelease`, after `Protocol`:

```go
	// ProtocolVersion is the manifest's and index entry's protocol_version; "" leaves it out.
	ProtocolVersion string
	// ManifestProtocolVersion, if set, replaces ProtocolVersion in the manifest only.
	ManifestProtocolVersion string
```

In `PluginSource`, replace:

```go
		fmt.Fprintf(&index, "[[plugin]]\nname = %q\nversion = %q\nprotocol = %d\ncapabilities = [\"import\"]\n%snotes = %q\ndescription = \"Test plugin.\"\n\n"+
			"[[plugin.artifact]]\nos = %q\narch = %q\nurl = %q\nsha256 = %q\n\n",
			r.Name, r.Version, r.Protocol, usesLine(r.Uses), r.Notes, runtime.GOOS, runtime.GOARCH, file, hash)
```

with:

```go
		fmt.Fprintf(&index, "[[plugin]]\nname = %q\nversion = %q\nprotocol = %d\n%scapabilities = [\"import\"]\n%snotes = %q\ndescription = \"Test plugin.\"\n\n"+
			"[[plugin.artifact]]\nos = %q\narch = %q\nurl = %q\nsha256 = %q\n\n",
			r.Name, r.Version, r.Protocol, protocolVersionLine(r.ProtocolVersion), usesLine(r.Uses), r.Notes, runtime.GOOS, runtime.GOARCH, file, hash)
```

In `PluginTarball`, replace:

```go
	name := r.Name
	manifest := fmt.Sprintf("name = %q\nversion = %q\nprotocol = %d\ncommand = [\"./%s\"]\ncapabilities = [\"import\"]\n%s",
		name, r.Version, r.Protocol, name, usesLine(r.Uses))
```

with:

```go
	name := r.Name
	protocolVersion := r.ProtocolVersion
	if r.ManifestProtocolVersion != "" {
		protocolVersion = r.ManifestProtocolVersion
	}
	manifest := fmt.Sprintf("name = %q\nversion = %q\nprotocol = %d\ncommand = [\"./%s\"]\ncapabilities = [\"import\"]\n%s%s",
		name, r.Version, r.Protocol, name, usesLine(r.Uses), protocolVersionLine(protocolVersion))
```

Add below `usesLine`:

```go
// protocolVersionLine is a TOML protocol_version line, or nothing without one.
func protocolVersionLine(v string) string {
	if v == "" {
		return ""
	}
	return fmt.Sprintf("protocol_version = %q\n", v)
}
```

- [ ] **Step 2: Write the failing tests**

Append to `internal/plugins/install_test.go`, and add `"fmt"`, `"strings"`, and `"github.com/inkandale/innkeeper/protocol"` to its imports:

```go
func TestInstallRefusesANewerProtocolBeforeDownloading(t *testing.T) {
	ctx := context.Background()
	reg, _ := openRegistry(t, testdb.New(t))
	// The wrong hash fails any download as ErrSource, so ErrInvalid shows the check came first.
	src := testutil.PluginSource(t, testutil.PluginRelease{Name: "next", Version: "1.0.0", ProtocolVersion: "1.99", SHA256: "00"})
	if err := reg.AddSource(ctx, PluginSource{ID: "local", URL: src}); err != nil {
		t.Fatal(err)
	}
	err := reg.Install(ctx, "local", "next", "1.0.0")
	var newer *protocol.NewerProtocolError
	if !errors.Is(err, ErrInvalid) || !errors.As(err, &newer) || newer.Needs != "1.99" {
		t.Fatalf("install = %v; want ErrInvalid needing protocol 1.99", err)
	}
}

func TestInstallRefusesATarballWhoseProtocolVersionDiffers(t *testing.T) {
	ctx := context.Background()
	reg, _ := openRegistry(t, testdb.New(t))
	current := fmt.Sprintf("%d.%d", protocol.Version, protocol.Minor)
	src := testutil.PluginSource(t,
		testutil.PluginRelease{Name: "same", Version: "1.0.0", ProtocolVersion: current},
		testutil.PluginRelease{Name: "differs", Version: "1.0.0", ManifestProtocolVersion: current})
	if err := reg.AddSource(ctx, PluginSource{ID: "local", URL: src}); err != nil {
		t.Fatal(err)
	}
	if err := reg.Install(ctx, "local", "same", "1.0.0"); err != nil {
		t.Fatalf("matching protocol_version: %v", err)
	}
	if err := reg.Install(ctx, "local", "differs", "1.0.0"); !errors.Is(err, ErrSource) || !strings.Contains(err.Error(), "protocol_version") {
		t.Fatalf("install = %v; want ErrSource naming protocol_version", err)
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres go test ./internal/plugins/ -run 'TestInstallRefuses' -v`
Expected: both new tests FAIL. The first fails because install reports a sha256 mismatch (ErrSource) after downloading. The second fails with `install = <nil>`.

- [ ] **Step 4: Add `Entry.ProtocolVersion` and `CheckProtocol`**

In `internal/plugins/catalog.go`, add `"github.com/inkandale/innkeeper/protocol"` to the imports. In `Entry`, replace:

```go
	Protocol     int      `toml:"protocol" json:"protocol"`
```

with:

```go
	Protocol     int      `toml:"protocol" json:"protocol"`
	// ProtocolVersion is the protocol major.minor the version needs; empty means <protocol>.0.
	ProtocolVersion string `toml:"protocol_version" json:"protocol_version,omitempty"`
```

Add below the `Artifact` type:

```go
// CheckProtocol reports why this Innkeeper can't run the entry's version, or
// nil if it can. A *protocol.NewerProtocolError means a newer Innkeeper can.
func (e Entry) CheckProtocol() error {
	if e.Protocol != protocol.Version {
		return fmt.Errorf("speaks protocol %d; this Innkeeper speaks %d", e.Protocol, protocol.Version)
	}
	return protocol.CheckVersion(e.Protocol, e.ProtocolVersion)
}
```

- [ ] **Step 5: Use it in `download`**

In `internal/plugins/install.go`, replace:

```go
	e := entries[i]
	if e.Protocol != protocol.Version {
		return "", fmt.Errorf("%w: %s %s speaks protocol %d; this Innkeeper speaks %d",
			ErrInvalid, name, version, e.Protocol, protocol.Version)
	}
```

with:

```go
	e := entries[i]
	if err := e.CheckProtocol(); err != nil {
		return "", fmt.Errorf("%w: %s %s %w", ErrInvalid, name, version, err)
	}
```

and replace:

```go
	if m.Name != name || m.Version != version || m.Protocol != protocol.Version {
		return "", fmt.Errorf("%w: %s holds %s %s protocol %d, not what the index lists", ErrSource, u.Redacted(), m.Name, m.Version, m.Protocol)
	}
```

with:

```go
	if m.Name != name || m.Version != version || m.Protocol != protocol.Version || m.ProtocolVersion != e.ProtocolVersion {
		return "", fmt.Errorf("%w: %s holds %s %s protocol %d protocol_version %q, not what the index lists",
			ErrSource, u.Redacted(), m.Name, m.Version, m.Protocol, m.ProtocolVersion)
	}
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres go test -race ./internal/plugins/ ./internal/testutil/`
Expected: `ok` for both. `TestInstallRefusesBadArtifacts` still passes: its protocol 99 entry gives `invalid plugin instance: future 1.0.0 speaks protocol 99; this Innkeeper speaks 1`, the same message as before.

- [ ] **Step 7: Commit**

```bash
git add internal/plugins/catalog.go internal/plugins/install.go internal/plugins/install_test.go internal/testutil/pluginsource.go
git commit -m "feat(plugins): refuse to install a version that needs a newer protocol"
```

---

## Task 3: `LoadInstalled` reports plugins that need a newer minor

**Files:**
- Modify: `internal/plugins/plugins.go` (`LoadInstalled` ~line 81-112, `Registry` struct ~line 116, `Sync` ~line 200-208, below `Manifests` ~line 258)
- Test: `internal/plugins/plugins_test.go` (`TestLoadInstalled` ~line 354)

- [ ] **Step 1: Write the failing test**

In `internal/plugins/plugins_test.go`, `TestLoadInstalled`: after the `misnamed` symlink block, add:

```go
	writeManifest(t, filepath.Join(dir, "future", "local@1.0.0"), manifestFor("future", "1.0.0")+"protocol_version = \"1.99\"\n")
	if err := os.Symlink("local@1.0.0", filepath.Join(dir, "future", "current")); err != nil {
		t.Fatal(err)
	}
```

Replace:

```go
	got, err := LoadInstalled(dir, quiet)
	if err != nil {
		t.Fatal(err)
	}
```

with:

```go
	got, newer, err := LoadInstalled(dir, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if len(newer) != 1 || newer["future"] != "1.99" {
		t.Fatalf("newer = %v, want only future needing 1.99", newer)
	}
```

The existing `len(got) != 1` check already proves `future` isn't loaded. Replace:

```go
	none, err := LoadInstalled(filepath.Join(dir, "missing"), quiet)
	if err != nil || len(none) != 0 {
```

with:

```go
	none, _, err := LoadInstalled(filepath.Join(dir, "missing"), quiet)
	if err != nil || len(none) != 0 {
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/plugins/ -run TestLoadInstalled`
Expected: build failure, `assignment mismatch: 3 variables but LoadInstalled returns 2 values`.

- [ ] **Step 3: Change `LoadInstalled`**

In `internal/plugins/plugins.go`, replace the whole `LoadInstalled` function with:

```go
// LoadInstalled loads the plugins the owner installed into dir. Each plugin's
// folder holds one folder per version and a current symlink to one of them.
// A missing dir has no plugins. A plugin whose current version needs a newer
// protocol isn't loaded; newer maps its name to the protocol_version it needs.
func LoadInstalled(dir string, log *slog.Logger) (manifests map[string]*protocol.Manifest, newer map[string]string, err error) {
	manifests, newer = map[string]*protocol.Manifest{}, map[string]string{}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return manifests, newer, nil
	}
	if err != nil {
		return nil, nil, err
	}
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
		var needs *protocol.NewerProtocolError
		if errors.As(err, &needs) {
			log.Warn("skip plugin: needs a newer Innkeeper", "dir", e.Name(), "err", err)
			newer[e.Name()] = needs.Needs
			continue
		}
		if err != nil {
			log.Error("skip plugin", "dir", e.Name(), "err", err)
			continue
		}
		manifests[m.Name] = m
	}
	return manifests, newer, nil
}
```

- [ ] **Step 4: Keep the result on the `Registry`**

In the `Registry` struct, replace:

```go
	builtin   map[string]*protocol.Manifest
```

with:

```go
	builtin   map[string]*protocol.Manifest
	newer     map[string]string // by installed plugin: the protocol_version it needs
```

In `Sync`, replace:

```go
	installed, err := LoadInstalled(r.dirs.Installed, r.log)
	if err != nil {
		return err
	}
	all := maps.Clone(builtin)
	maps.Copy(all, installed)
	r.mu.Lock()
	r.manifests, r.builtin, r.gen = all, builtin, gen
	r.mu.Unlock()
```

with:

```go
	installed, newer, err := LoadInstalled(r.dirs.Installed, r.log)
	if err != nil {
		return err
	}
	all := maps.Clone(builtin)
	maps.Copy(all, installed)
	r.mu.Lock()
	r.manifests, r.builtin, r.newer, r.gen = all, builtin, newer, gen
	r.mu.Unlock()
```

Add below `Manifests`:

```go
// NeedsNewer returns the installed plugins this Innkeeper can't run, with
// the protocol_version each needs. They aren't in Manifests.
func (r *Registry) NeedsNewer() map[string]string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return maps.Clone(r.newer)
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres go vet ./... && go test -race ./internal/plugins/`
Expected: `ok  github.com/inkandale/innkeeper/internal/plugins`. `go vet ./...` passes, which shows `Sync` was the only other caller.

- [ ] **Step 6: Commit**

```bash
git add internal/plugins/plugins.go internal/plugins/plugins_test.go
git commit -m "feat(plugins): keep track of installed plugins that need a newer protocol"
```

---

## Task 4: The catalog offers the newest version core can run

**Files:**
- Modify: `internal/api/pluginstore.go` (`catalogPluginOut` ~line 37, `catalog` ~line 141)
- Test: `internal/api/pluginstore_test.go`
- Regenerate: `web/openapi.json`, `web/src/api/schema.d.ts`

- [ ] **Step 1: Write the failing test**

Append to `internal/api/pluginstore_test.go`:

```go
// withInstalls gives e a registry that installs into a new folder, and returns that folder.
func withInstalls(t *testing.T, e *testEnv) string {
	t.Helper()
	box, err := secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	reg, err := plugins.Open(context.Background(), e.pool, plugins.Dirs{Installed: dir},
		&blobstore.Store{Name: "library", Root: t.TempDir()}, box, "", quiet)
	if err != nil {
		t.Fatal(err)
	}
	e.s.Registry = reg
	return dir
}

func TestPluginCatalogOffersTheNewestVersionThisInnkeeperRuns(t *testing.T) {
	e := newEnv(t)
	withInstalls(t, e)
	src := testutil.PluginSource(t,
		testutil.PluginRelease{Name: "good", Version: "1.0.0"},
		testutil.PluginRelease{Name: "good", Version: "2.0.0", ProtocolVersion: "1.99"},
		testutil.PluginRelease{Name: "later", Version: "1.0.0", ProtocolVersion: "1.99"},
		testutil.PluginRelease{Name: "plain", Version: "1.0.0"})
	e.call(http.MethodPost, "/plugin-sources", map[string]string{"id": "local", "url": src}, http.StatusCreated, nil)

	var catalog []catalogSourceOut
	e.call(http.MethodGet, "/plugin-catalog", nil, http.StatusOK, &catalog)
	got := catalog[0].Plugins
	if len(got) != 2 || got[0].Name != "good" || got[1].Name != "plain" {
		t.Fatalf("plugins = %+v; want good and plain, and not later, which has no version this Innkeeper runs", got)
	}
	if good := got[0]; good.Version != "1.0.0" || !slices.Equal(good.Versions, []string{"2.0.0", "1.0.0"}) ||
		good.Newer == nil || *good.Newer != (newerVersionOut{Version: "2.0.0", ProtocolVersion: "1.99"}) {
		t.Fatalf("good = %+v; want 1.0.0 offered and 2.0.0 newer", good)
	}
	if got[1].Newer != nil {
		t.Fatalf("plain newer = %+v; want none", got[1].Newer)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres go test ./internal/api/ -run TestPluginCatalogOffersTheNewestVersionThisInnkeeperRuns`
Expected: build failure, `good.Newer undefined` and `undefined: newerVersionOut`.

- [ ] **Step 3: Add `newer` and change `catalog`**

In `internal/api/pluginstore.go`, replace the `catalogPluginOut` type with:

```go
// catalogPluginOut is a plugin a source offers: the details of the newest
// version this Innkeeper runs, and every version it lists.
type catalogPluginOut struct {
	plugins.Entry
	// Versions is newest first.
	Versions []string `json:"versions"`
	// Notes maps a version to what changed in it, for versions that have notes.
	Notes map[string]string `json:"notes,omitempty"`
	Needs []string          `json:"needs,omitempty" doc:"Capabilities the plugin needs another plugin to offer, such as ai"`
	Newer *newerVersionOut  `json:"newer,omitempty" doc:"The newest version, when this Innkeeper can't run it"`
}

// newerVersionOut is a plugin version that needs a newer Innkeeper.
type newerVersionOut struct {
	Version         string `json:"version"`
	ProtocolVersion string `json:"protocol_version" doc:"The protocol_version it declares, such as 1.2"`
}
```

Replace the whole `catalog` function with:

```go
// catalog groups a source's entries by plugin, newest version first. Each
// plugin offers its newest version this Innkeeper runs; a plugin with none
// is left out.
func catalog(entries []plugins.Entry) []catalogPluginOut {
	byName := map[string]*catalogPluginOut{}
	for _, e := range entries {
		p, ok := byName[e.Name]
		if !ok {
			p = &catalogPluginOut{}
			byName[e.Name] = p
		}
		switch {
		case e.CheckProtocol() != nil:
			if p.Newer == nil || plugins.Newer(e.Version, p.Newer.Version) {
				p.Newer = &newerVersionOut{Version: e.Version, ProtocolVersion: e.ProtocolVersion}
			}
		case p.Name == "" || plugins.Newer(e.Version, p.Version):
			p.Entry = e
		}
		p.Versions = append(p.Versions, e.Version)
		if e.Notes != "" {
			if p.Notes == nil {
				p.Notes = map[string]string{}
			}
			p.Notes[e.Version] = e.Notes
		}
	}
	out := []catalogPluginOut{}
	for _, p := range byName {
		if p.Name == "" {
			continue
		}
		if p.Newer != nil && !plugins.Newer(p.Newer.Version, p.Version) {
			p.Newer = nil
		}
		p.Needs = protocol.Needs(p.Uses)
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

`p.Name == ""` means no runnable entry has been seen yet, since `p.Entry` is set only from a runnable one.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres go test -race ./internal/api/ -run 'TestPlugin'`
Expected: `ok  github.com/inkandale/innkeeper/internal/api`, including `TestPluginStore`.

- [ ] **Step 5: Regenerate the API schema**

Run: `cd web && npm run gen && npm run typecheck`
Expected: `git diff --stat` shows only `web/openapi.json` and `web/src/api/schema.d.ts` changed under `web/`. `CatalogPluginOut` has `newer?: components["schemas"]["NewerVersionOut"]` and `protocol_version?: string`. Typecheck passes.

- [ ] **Step 6: Commit**

```bash
git add internal/api/pluginstore.go internal/api/pluginstore_test.go web/openapi.json web/src/api/schema.d.ts
git commit -m "feat(api): offer the newest plugin version this Innkeeper runs, and name the newer one"
```

---

## Task 5: `GET /plugins` lists installs that need a newer minor

**Files:**
- Modify: `internal/api/instances.go` (`pluginOut` ~line 30, `list-plugins` handler ~line 118-176)
- Test: `internal/api/pluginstore_test.go`
- Regenerate: `web/openapi.json`, `web/src/api/schema.d.ts`

- [ ] **Step 1: Write the failing test**

Append to `internal/api/pluginstore_test.go`, and add `"os"` and `"path/filepath"` to its imports:

```go
func TestPluginsListsAnInstallThatNeedsANewerProtocol(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	installed := withInstalls(t, e)
	src := testutil.PluginSource(t, testutil.PluginRelease{Name: "good", Version: "1.0.0"})
	e.call(http.MethodPost, "/plugin-sources", map[string]string{"id": "local", "url": src}, http.StatusCreated, nil)
	e.call(http.MethodPost, "/plugins/good/install", map[string]string{"source": "local", "version": "1.0.0"}, http.StatusNoContent, nil)

	// As if Innkeeper were rolled back below what the installed version needs.
	f, err := os.OpenFile(filepath.Join(installed, "good", "current", "plugin.toml"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("protocol_version = \"1.99\"\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `SELECT nextval('plugin_generation')`); err != nil {
		t.Fatal(err)
	}
	if err := e.s.Registry.Sync(ctx); err != nil {
		t.Fatal(err)
	}

	if e.s.Registry.Manifests()["good"] != nil {
		t.Fatal("good still runs")
	}
	var list []pluginOut
	e.call(http.MethodGet, "/plugins", nil, http.StatusOK, &list)
	i := slices.IndexFunc(list, func(p pluginOut) bool { return p.Name == "good" })
	if i < 0 {
		t.Fatalf("plugins = %+v; want good listed", list)
	}
	if p := list[i]; p.NeedsProtocol != "1.99" || p.Version != "1.0.0" || p.Source != "local" || p.Status != "broken" {
		t.Fatalf("good = %+v; want 1.0.0 from local, broken, needing protocol 1.99", p)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres go test ./internal/api/ -run TestPluginsListsAnInstallThatNeedsANewerProtocol`
Expected: build failure, `p.NeedsProtocol undefined`.

- [ ] **Step 3: Add `needs_protocol` and list the plugins**

In `internal/api/instances.go`, in `pluginOut`, after the `Permissions` field add:

```go
	NeedsProtocol     string                   `json:"needs_protocol,omitempty" doc:"Set when this Innkeeper can't run the installed version: the protocol_version it needs. Nothing runs the plugin, and its status is broken."`
```

In the `list-plugins` handler, replace:

```go
			out := &pluginsOut{Body: []pluginOut{}}
			for _, m := range s.Registry.Manifests() {
```

with:

```go
			out := &pluginsOut{Body: []pluginOut{}}
			manifests := s.Registry.Manifests()
			for _, m := range manifests {
```

and replace:

```go
				out.Body = append(out.Body, p)
			}
			slices.SortFunc(out.Body, func(a, b pluginOut) int { return strings.Compare(a.Name, b.Name) })
```

with:

```go
				out.Body = append(out.Body, p)
			}
			for name, needs := range s.Registry.NeedsNewer() {
				// A built-in copy runs in its place.
				if manifests[name] != nil {
					continue
				}
				in := installs[name]
				out.Body = append(out.Body, pluginOut{Name: name, Title: name, Version: in.Version, Status: "broken",
					Capabilities: []string{}, Secrets: []string{}, Source: in.Source,
					PreviousSource: in.PreviousSource, PreviousVersion: in.PreviousVersion, NeedsProtocol: needs})
			}
			slices.SortFunc(out.Body, func(a, b pluginOut) int { return strings.Compare(a.Name, b.Name) })
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres go test -race ./internal/api/`
Expected: `ok  github.com/inkandale/innkeeper/internal/api`

- [ ] **Step 5: Regenerate the API schema**

Run: `cd web && npm run gen && npm run typecheck`
Expected: `PluginOut` in `web/src/api/schema.d.ts` has `needs_protocol?: string`. Typecheck passes.

- [ ] **Step 6: Commit**

```bash
git add internal/api/instances.go internal/api/pluginstore_test.go web/openapi.json web/src/api/schema.d.ts
git commit -m "feat(api): list an installed plugin that needs a newer protocol"
```

---

## Task 6: The Plugins page says which versions need a newer Innkeeper

**Files:**
- Modify: `web/src/settings/Plugins.tsx`
- Test: `web/src/settings/Plugins.test.tsx`

- [ ] **Step 1: Write the failing tests**

Append to `web/src/settings/Plugins.test.tsx`:

```tsx
test("says which versions need a newer Innkeeper and offers no button for them", async () => {
  const user = userEvent.setup();
  const fetch = globalThis.fetch;
  vi.stubGlobal("fetch", async (req: Request) => {
    const path = new URL(req.url).pathname;
    if (path === "/api/plugins")
      return json(titled([
        { name: "forge-ai", version: "0.13.0", capabilities: ["action"], secrets: [], source: "official", builtin: false,
          category: "AI", multiple_instances: false, status: "ready" },
      ]));
    if (path === "/api/plugin-catalog")
      return json([{
        source: { id: "official", url: "https://example.com/index.toml" },
        plugins: [
          { name: "forge-ai", version: "0.13.1", versions: ["0.14.1", "0.13.1", "0.13.0"],
            notes: { "0.14.1": "Needs the new core", "0.13.1": "Fix search" },
            newer: { version: "0.14.1", protocol_version: "1.1" }, protocol: 1, capabilities: [], description: "Forge" },
          { name: "newbie", version: "1.0.0", versions: ["2.0.0", "1.0.0"],
            newer: { version: "2.0.0", protocol_version: "1.1" }, protocol: 1, capabilities: [], description: "New" },
        ],
      }]);
    return fetch(req);
  });
  renderPlugins();
  const forge = (await screen.findByRole("heading", { name: "forge-ai" })).closest("article")!;
  expect(await within(forge).findByText("0.14.1 needs a newer Innkeeper")).toBeInTheDocument();
  expect(within(forge).getByRole("button", { name: "Update to 0.13.1" })).toBeInTheDocument();
  expect(within(forge).queryByRole("button", { name: "Update to 0.14.1" })).not.toBeInTheDocument();
  await user.click(within(forge).getByText("What's new"));
  expect(within(forge).getByText("Fix search")).toBeVisible();
  expect(within(forge).queryByText("Needs the new core")).not.toBeInTheDocument();

  const available = screen.getByRole("region", { name: "Available" });
  expect(within(available).getByText("2.0.0 needs a newer Innkeeper")).toBeInTheDocument();
  expect(within(available).getByRole("button", { name: "Install newbie" })).toHaveTextContent("Install 1.0.0");
});

test("lists an installed plugin that needs a newer Innkeeper apart, with rollback and uninstall", async () => {
  const user = userEvent.setup();
  const fetch = globalThis.fetch;
  vi.stubGlobal("fetch", async (req: Request) =>
    new URL(req.url).pathname === "/api/plugins" && req.method === "GET"
      ? json(titled([
          { name: "stalled", version: "2.0.0", capabilities: [], secrets: [], source: "official", builtin: false,
            previous_source: "official", previous_version: "1.0.0", multiple_instances: false, status: "broken",
            needs_protocol: "1.9" },
        ]))
      : fetch(req),
  );
  renderPlugins();
  const group = (await screen.findByRole("heading", { name: "Needs a newer Innkeeper" })).closest("section")!;
  const stalled = within(group).getByRole("heading", { name: "stalled" }).closest("article")!;
  expect(within(stalled).getByText(/2\.0\.0 needs a newer Innkeeper, one that speaks protocol 1\.9/)).toBeInTheDocument();
  expect(within(stalled).queryByRole("link", { name: "Set up" })).not.toBeInTheDocument();
  const menu = menuOf(stalled, "stalled");
  expect(within(menu).getByRole("button", { name: "Roll back to 1.0.0" })).toBeInTheDocument();
  await user.click(within(menu).getByRole("button", { name: "Uninstall" }));
  await confirmWith("Uninstall");
  expect(requests).toContain("DELETE /api/plugins/stalled");
});
```

The second test's `requests` comes from the default mock, which the wrapper falls through to for every request other than `GET /api/plugins`.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd web && npx vitest run src/settings/Plugins.test.tsx`
Expected: the two new tests FAIL. The first can't find "0.14.1 needs a newer Innkeeper". The second can't find the "Needs a newer Innkeeper" heading. The other tests pass.

- [ ] **Step 3: Split out the plugins this Innkeeper can't run**

In `web/src/settings/Plugins.tsx`, replace:

```tsx
  const needSetup = installed.filter((p) => p.status === "needs_setup").length;
```

with:

```tsx
  const needSetup = installed.filter((p) => p.status === "needs_setup").length;
  // Installed plugins this Innkeeper can't run get their own group, without a card's setup actions.
  const stalled = installed.filter((p) => p.needs_protocol);
```

and replace:

```tsx
      {byCategory(installed).map(([category, list]) => (
```

with:

```tsx
      {byCategory(installed.filter((p) => !p.needs_protocol)).map(([category, list]) => (
```

- [ ] **Step 4: Share the Roll back and Uninstall buttons**

Replace:

```tsx
  const card = (p: Plugin) => {
```

with:

```tsx
  const rollBack = (p: Plugin) => {
    const source = p.previous_source;
    const version = p.previous_version;
    if (!source || !version) return null;
    return (
      <button disabled={busy} onClick={() => install.mutate({ name: p.name, source, version })}>
        Roll back to {version}
      </button>
    );
  };

  const uninstallButton = (p: Plugin) => (
    <button
      disabled={busy}
      onClick={() =>
        ask({
          title: `Uninstall ${p.name}?`,
          body: (
            <p>
              {p.builtin
                ? "Innkeeper goes back to its built-in copy."
                : "Its instances and data stay, and come back if you install it again."}
            </p>
          ),
          confirmLabel: "Uninstall",
          onConfirm: () => uninstall.mutate(p.name),
        })
      }
    >
      Uninstall
    </button>
  );

  const card = (p: Plugin) => {
```

In `card`, replace:

```tsx
              {prevSource && prevVersion && (
                <button disabled={busy} onClick={() => install.mutate({ name: p.name, source: prevSource, version: prevVersion })}>
                  Roll back to {prevVersion}
                </button>
              )}
              {only && instanceActions(only)}
              {canUninstall && (
                <button
                  disabled={busy}
                  onClick={() =>
                    ask({
                      title: `Uninstall ${p.name}?`,
                      body: (
                        <p>
                          {p.builtin
                            ? "Innkeeper goes back to its built-in copy."
                            : "Its instances and data stay, and come back if you install it again."}
                        </p>
                      ),
                      confirmLabel: "Uninstall",
                      onConfirm: () => uninstall.mutate(p.name),
                    })
                  }
                >
                  Uninstall
                </button>
              )}
```

with:

```tsx
              {rollBack(p)}
              {only && instanceActions(only)}
              {canUninstall && uninstallButton(p)}
```

- [ ] **Step 5: Show the line on a card and an offer**

In `card`, replace:

```tsx
    const update = offers.updates.get(p.name);
```

with:

```tsx
    const update = offers.updates.get(p.name);
    const blocked = offers.blocked.get(p.name);
```

and replace:

```tsx
            {p.description && <p className="muted">{p.description}</p>}
```

with:

```tsx
            {p.description && <p className="muted">{p.description}</p>}
            {blocked && <p className="muted">{blocked} needs a newer Innkeeper</p>}
```

In the Available list, replace:

```tsx
                        <p className="muted">{o.description}</p>
```

with:

```tsx
                        <p className="muted">{o.description}</p>
                        {o.newer && <p className="muted">{o.newer} needs a newer Innkeeper</p>}
```

- [ ] **Step 6: Add the group**

Replace:

```tsx
      {orphanPlugins.length > 0 && (
```

with:

```tsx
      {stalled.length > 0 && (
        <section className="plugin-group">
          <h2>Needs a newer Innkeeper</h2>
          {stalled.map((p) => (
            <article key={p.name} className="plugin">
              <header>
                <div className="about">
                  <h3>{p.title}</h3>{" "}
                  <span className="muted">{`${from(p.source)}${p.version}`}</span>
                  <p className="muted">
                    {p.version} needs a newer Innkeeper, one that speaks protocol {p.needs_protocol}. Its instances and
                    data stay.
                  </p>
                </div>
                <Menu label={`More for ${p.name}`}>
                  {rollBack(p)}
                  {uninstallButton(p)}
                </Menu>
              </header>
            </article>
          ))}
        </section>
      )}
      {orphanPlugins.length > 0 && (
```

- [ ] **Step 7: Teach `catalogOffers` about `newer`**

Replace:

```tsx
type Offer = { source: string; name: string; title: string; category?: string; version: string; description: string; recommended?: boolean };
```

with:

```tsx
type Offer = {
  source: string;
  name: string;
  title: string;
  category?: string;
  version: string;
  description: string;
  recommended?: boolean;
  // The newest version, when this Innkeeper can't run it.
  newer?: string;
};
```

Replace the whole `catalogOffers` function with:

```tsx
/** catalogOffers splits what the sources list into updates for installed
 * plugins, from the source each came from, and plugins not installed. blocked
 * holds an installed plugin's newest version when this Innkeeper can't run it. */
function catalogOffers(catalog: CatalogSource[], installed: Plugin[]) {
  const updates = new Map<string, Update>();
  const blocked = new Map<string, string>();
  const available: Offer[] = [];
  const byName = new Map(installed.map((p) => [p.name, p]));
  for (const s of catalog) {
    for (const p of s.plugins ?? []) {
      const have = byName.get(p.name);
      if (!have) {
        const { name, version, description, recommended, category } = p;
        available.push({
          source: s.source.id, name, title: p.title || name, category, version, description, recommended, newer: p.newer?.version,
        });
      } else if (have.source === s.source.id && !have.needs_protocol) {
        if (p.newer) blocked.set(p.name, p.newer.version);
        if (p.version === have.version) continue;
        // p.version is the newest this Innkeeper runs, so a running install is older.
        // versions is newest first; without the installed one listed, only the offered one's notes apply.
        const versions = p.versions ?? [];
        const at = versions.indexOf(have.version);
        const newer = at < 0 ? [p.version] : versions.slice(versions.indexOf(p.version), at);
        const notes = newer.flatMap((v): Update["notes"] => (p.notes?.[v] ? [[v, p.notes[v]]] : []));
        updates.set(p.name, { version: p.version, notes });
      }
    }
  }
  return { updates, blocked, available };
}
```

- [ ] **Step 8: Run the tests to verify they pass**

Run: `cd web && npm run typecheck && npx vitest run src/settings/Plugins.test.tsx src/welcome`
Expected: typecheck passes, and every test passes, including the existing "lists what the sources offer on load" and "rolls back, uninstalls" tests.

- [ ] **Step 9: Commit**

```bash
git add web/src/settings/Plugins.tsx web/src/settings/Plugins.test.tsx
git commit -m "feat(web): show plugin versions that need a newer Innkeeper"
```

---

## Task 7: Document the protocol version

**Files:**
- Modify: `docs/protocol.md` (lines 1-4, Conventions ~line 23, Manifest table ~line 97, Publishing a source ~line 1457-1510)
- Modify: `AGENTS.md` (Plugins section ~line 17-28)

Invoke the `documentation-writing` skill first. Edit these files with the Edit tool, not a Bash heredoc.

- [ ] **Step 1: State the version and add the Versions section**

In `docs/protocol.md`, replace:

```markdown
Protocol version 1. The design behind it is in
[the core design spec](superpowers/specs/2026-10-03-innkeeper-core-design.md).
```

with:

```markdown
Protocol version 1.1. The design behind it is in
[the core design spec](superpowers/specs/2026-10-03-innkeeper-core-design.md).

## Versions

The protocol has a `<major>.<minor>` version of its own, apart from
Innkeeper's. A plugin declares the major as `protocol` and the minor it
needs as `protocol_version`; see [Manifest](#manifest). Innkeeper runs a
plugin with its own major and a minor no higher than its own. It doesn't
offer a version that needs a higher minor, and the Plugins page says that
version needs a newer Innkeeper.

| Version | Adds |
|---|---|
| 1.0 | Everything not listed below |
| 1.1 | The `panel` grant and `[ui] title` (see [`ui`](#ui)), the `cards.edit` permission, `host.request`'s `snapshot`, and the `context` notice (see [The bridge](#the-bridge)) |

Set `protocol_version` to the newest minor whose features the plugin uses.
```

- [ ] **Step 2: Add the convention**

Replace:

```markdown
New parts of the protocol follow these rules. Older names that break them
stay as they are, so existing plugins keep working; each is listed as an
exception.
```

with:

```markdown
New parts of the protocol follow these rules. Older names that break them
stay as they are, so existing plugins keep working; each is listed as an
exception.

### Versioning

Each addition to the protocol bumps `protocol.Minor` and adds a row to the
[version table](#versions). A change that breaks existing plugins needs a
new major.
```

- [ ] **Step 3: Add the manifest row**

Replace:

```markdown
| `protocol` | yes | Protocol version; must be `1` |
```

with:

```markdown
| `protocol` | yes | Protocol major version; must be `1` |
| `protocol_version` | no | The protocol version the plugin needs, such as `"1.1"`, with the major of `protocol`. Without it, `1.0`. See [Versions](#versions) |
```

- [ ] **Step 4: Document it in the index**

In "Publishing a source", replace:

```markdown
version = "0.2.0"
protocol = 1
capabilities = ["push", "pull"]
```

with:

```markdown
version = "0.2.0"
protocol = 1
protocol_version = "1.1"
capabilities = ["push", "pull"]
```

Replace:

```markdown
- `title` is the name the setup wizard shows, as in the manifest.
```

with:

```markdown
- `title` is the name the setup wizard shows, as in the manifest.
- `protocol_version` is the manifest's; leave both out for `1.0`. Innkeeper
  offers the newest version whose protocol it speaks, and names any newer
  version as needing a newer Innkeeper.
```

Replace:

```markdown
  It may contain only regular files and folders, no links. Its manifest's
  `name`, `version`, and `protocol` must match the index entry.
```

with:

```markdown
  It may contain only regular files and folders, no links. Its manifest's
  `name`, `version`, `protocol`, and `protocol_version` must match the index
  entry.
```

- [ ] **Step 5: Add the rule to `AGENTS.md`**

In `AGENTS.md`, replace:

```markdown
- Core knows plugins only through the manifest and the protocol. Never
  branch on a plugin's name.
```

with:

```markdown
- Core knows plugins only through the manifest and the protocol. Never
  branch on a plugin's name.
- Every addition to the protocol bumps `protocol.Minor` and adds a row to
  the version table in `docs/protocol.md`.
```

- [ ] **Step 6: Check the links**

Run: `grep -n '^## Versions\|^### `ui`\|^#### The bridge\|^## Manifest' docs/protocol.md`
Expected: four headings, so the anchors `#versions`, `#ui`, `#the-bridge`, and `#manifest` resolve.

- [ ] **Step 7: Run what CI runs**

```bash
export INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres
go vet ./... && go test -race ./...
cd web && npm run typecheck && npm test
```

Expected: all pass.

- [ ] **Step 8: Commit**

```bash
git add docs/protocol.md AGENTS.md
git commit -m "docs(protocol): document protocol 1.1 and protocol_version"
```

---

## Task 8: Push core to develop (controller only)

The implementer doesn't do this task. The controller pushes the core commits from Tasks 1 to 7 to `origin/develop`, and passes the pushed commit's full SHA to Task 9. The plugins repo can't pin a core commit that isn't on the remote.

- [ ] **Step 1 (controller): Push the core commits to `origin/develop` and record the SHA of the commit that holds `protocol.Minor`, or a later one.**

---

## Task 9: Pin the core and carry `protocol_version` in the plugins repo

Work in `/Users/dgibbons/git/innkeeper-plugins-ai-assistant`. Needs the core SHA from Task 8; below it is `<CORE_SHA>`.

**Files:**
- Modify: `go.mod`, `go.sum`
- Modify: `internal/indexcheck/main.go`, `internal/indexcheck/main_test.go`
- Modify: `internal/publish/main.go`, `internal/publish/main_test.go`
- Modify: `README.md` (~line 45-47)

- [ ] **Step 1: Pin the core**

Run:

```bash
go get github.com/inkandale/innkeeper@<CORE_SHA> && go mod tidy
grep -n 'inkandale/innkeeper ' go.mod
```

Expected: `go.mod` pins `github.com/inkandale/innkeeper v0.0.0-<date>-<first 12 of CORE_SHA>`.

- [ ] **Step 2: Check the pin builds and passes**

Run: `go vet ./... && go test -race ./...`
Expected: all `ok`.

- [ ] **Step 3: Commit the pin**

```bash
git add go.mod go.sum
git commit -m "chore: pin innkeeper to develop <first 7 of CORE_SHA>"
```

- [ ] **Step 4: Write the failing tests**

In `internal/indexcheck/main_test.go`, add `"github.com/inkandale/innkeeper/protocol"` to the imports, and append:

```go
func TestCheckRejectsAMismatchedProtocolVersion(t *testing.T) {
	root := t.TempDir()
	manifest := "name = \"a\"\nversion = \"1.0.0\"\nprotocol = 1\ncommand = [\"./a\"]\ncapabilities = [\"push\"]\ndescription = \"d\"\n"
	index := "[[plugin]]\nname = \"a\"\npath = \"a\"\nversion = \"1.0.0\"\nprotocol = 1\nprotocol_version = \"1.1\"\ncapabilities = [\"push\"]\ndescription = \"d\"\n"
	if err := os.MkdirAll(filepath.Join(root, "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string]string{"a/plugin.toml": manifest, "index.toml": index} {
		if err := os.WriteFile(filepath.Join(root, path), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	errs := check(root)
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), `index protocol_version "1.1"; plugin.toml has ""`) {
		t.Fatalf("want one protocol_version mismatch, got %v", errs)
	}
}

func TestCheckRejectsAProtocolAboveThePinnedCore(t *testing.T) {
	root := t.TempDir()
	next := fmt.Sprintf("%d.%d", protocol.Version, protocol.Minor+1)
	manifest := "name = \"a\"\nversion = \"1.0.0\"\nprotocol = 1\nprotocol_version = \"" + next + "\"\ncommand = [\"./a\"]\ncapabilities = [\"push\"]\ndescription = \"d\"\n"
	index := "[[plugin]]\nname = \"a\"\npath = \"a\"\nversion = \"1.0.0\"\nprotocol = 1\nprotocol_version = \"" + next + "\"\ncapabilities = [\"push\"]\ndescription = \"d\"\n"
	if err := os.MkdirAll(filepath.Join(root, "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string]string{"a/plugin.toml": manifest, "index.toml": index} {
		if err := os.WriteFile(filepath.Join(root, path), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	errs := check(root)
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "needs protocol "+next) {
		t.Fatalf("want one error saying a needs protocol %s, got %v", next, errs)
	}
}
```

In `internal/publish/main_test.go`, append:

```go
func TestFinishIndexKeepsProtocolVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.toml")
	published := "[[plugin]]\nname = \"password-login\"\nversion = \"0.1.0\"\nprotocol = 1\nprotocol_version = \"1.1\"\n"
	if err := os.WriteFile(path, []byte(published), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := finishIndex("../..", path); err != nil {
		t.Fatal(err)
	}
	var index struct {
		Plugin []entry `toml:"plugin"`
	}
	if _, err := toml.DecodeFile(path, &index); err != nil {
		t.Fatal(err)
	}
	if got := index.Plugin[0].ProtocolVersion; got != "1.1" {
		t.Fatalf("protocol_version = %q, want 1.1", got)
	}
}
```

- [ ] **Step 5: Run the tests to verify they fail**

Run: `go test ./internal/indexcheck/ ./internal/publish/`
Expected: publish fails to build, `index.Plugin[0].ProtocolVersion undefined`. Both new indexcheck tests FAIL: `entry` doesn't know the key yet, so `check` reports `index.toml: unknown keys: [plugin.protocol_version]` instead of the mismatch, and the above-the-pin test gets two errors instead of one.

- [ ] **Step 6: Compare `protocol_version` in `indexcheck`**

In `internal/indexcheck/main.go`, in `entry`, replace:

```go
	Protocol     int      `toml:"protocol"`
```

with:

```go
	Protocol     int      `toml:"protocol"`
	// ProtocolVersion must match plugin.toml's; LoadManifest refuses one above the pinned core's protocol.Minor.
	ProtocolVersion string `toml:"protocol_version"`
```

In `check`, replace:

```go
		if !slices.Equal(e.Uses, m.Uses) {
```

with:

```go
		if e.ProtocolVersion != m.ProtocolVersion {
			errs = append(errs, fmt.Errorf("%s: index protocol_version %q; plugin.toml has %q", e.Path, e.ProtocolVersion, m.ProtocolVersion))
		}
		if !slices.Equal(e.Uses, m.Uses) {
```

- [ ] **Step 7: Keep `protocol_version` in `publish`**

In `internal/publish/main.go`, in `entry`, replace:

```go
	Protocol     int        `toml:"protocol"`
```

with:

```go
	Protocol     int        `toml:"protocol"`
	// ProtocolVersion is copied as-is, since core requires the tarball's manifest to match it.
	ProtocolVersion string `toml:"protocol_version,omitempty"`
```

- [ ] **Step 8: Name it in the README**

In `README.md`, replace:

```markdown
2. An `index.toml` entry with `path = "<folder>"` and the same name,
   version, protocol, capabilities, description, and category as
   `plugin.toml`. `go run ./internal/indexcheck` checks they match.
```

with:

```markdown
2. An `index.toml` entry with `path = "<folder>"` and the same name,
   version, protocol, protocol_version, capabilities, description, and
   category as `plugin.toml`. `go run ./internal/indexcheck` checks they
   match. Set `protocol_version` to the newest protocol minor whose
   features the plugin uses, from the version table in the core's
   `docs/protocol.md`; leave it out for `1.0`.
```

Edit `README.md` with the Edit tool, after invoking the `documentation-writing` skill.

- [ ] **Step 9: Run the tests to verify they pass**

Run: `gofmt -l internal/ && go vet ./... && go test -race ./...`
Expected: `gofmt` prints nothing; all `ok`.

- [ ] **Step 10: Commit**

```bash
git add internal/indexcheck/main.go internal/indexcheck/main_test.go internal/publish/main.go internal/publish/main_test.go README.md
git commit -m "feat(indexcheck): check protocol_version, and keep it in published entries"
```

---

## Task 10: Release forge-ai 0.14.1 on protocol 1.1

Work in `/Users/dgibbons/git/innkeeper-plugins-ai-assistant`. Other plugins don't change.

**Files:**
- Modify: `forge-ai/plugin.toml:3-4`
- Modify: `index.toml` (the forge-ai entry, ~line 95-96)

- [ ] **Step 1: Bump the manifest**

In `forge-ai/plugin.toml`, replace:

```toml
version = "0.14.0"
protocol = 1
```

with:

```toml
version = "0.14.1"
protocol = 1
protocol_version = "1.1"
```

- [ ] **Step 2: Bump the index entry**

In `index.toml`, in the `name = "forge-ai"` entry, replace:

```toml
path = "forge-ai"
version = "0.14.0"
protocol = 1
```

with:

```toml
path = "forge-ai"
version = "0.14.1"
protocol = 1
protocol_version = "1.1"
```

The index has no `notes` key. `publish -index` builds 0.14.1's notes from the commits since `forge-ai-v0.14.0`. Check that they hold the Editing-line fix:

Run: `git log --format=%s forge-ai-v0.14.0..HEAD -- forge-ai`
Expected: `fix(forge-ai): refresh the Editing line when the page's context changes`.

- [ ] **Step 3: Run the checks**

```bash
go vet ./... && go test -race ./...
INNKEEPER_PLUGIN_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres make check
make test-ui
```

Expected: `go test` passes, including `TestRepoIndex`. `make check` passes forge-ai and fails only the 2 card-embed checks that fail on origin/main. `make test-ui` passes.

- [ ] **Step 4: Commit**

```bash
git add forge-ai/plugin.toml index.toml
git commit -m "chore(forge-ai): release 0.14.1 on protocol 1.1"
```

The `chore` type keeps this commit out of the release notes.

## Rollout

Not part of the plan's tasks; for whoever ships it. Production runs a core from before this change. That core ignores `protocol_version` and still offers forge-ai 0.14.x. Don't update forge-ai on production until it restarts onto the core from Task 8.
