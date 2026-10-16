# Settings form implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give the instance settings page tabs, defaults with per-field reset and a "default updated" mark, a checkbox group, an instance picker, a choices dropdown filled by the plugin, and plugin-declared buttons such as Test connection.

**Architecture:** Section 2 of the [AI services spec](../specs/2026-10-15-ai-services-design.md). A new optional `settings` capability adds three stateless calls: `settings.defaults`, `settings.options` and `settings.run`. Core runs them on a dedicated process per plugin, the same way `auth.setup` runs before any instance exists, and passes the unsaved draft config and secrets in each call's params. The web form reads the new schema keywords and calls three new endpoints. Per-override default hashes live in a new `plugin_instance.default_hashes` column.

**Tech Stack:** Go (Huma, pgx, PostgreSQL), React + TypeScript (TanStack Query, openapi-fetch, Vitest, Testing Library).

---

## Assumptions on plan 1

Plan 1 (the `ai` capability) provides this, and Task 6 calls it:

```go
// InstancesOffering returns the working instances whose plugin reported
// service (such as "ai.chat") in its initialize result, oldest first.
func (r *Registry) InstancesOffering(ctx context.Context, mgr *pluginhost.Manager, service string) ([]InstanceInfo, error)
```

It lives in `internal/plugins`. If plan 1 names or shapes it differently, change the one call in Task 6 Step 3.

## Changes from the spec

- **Initialize still runs.** `pluginhost.Start` always sends `initialize`, so a settings process gets an empty config and no secrets, exactly like `setupInstance` in `internal/api/setup.go:141`. The draft goes in each `settings.*` call. A plugin's `initialize` must therefore succeed with an empty config; forge-ai already does, because it stores the config error and reports it later.
- **Defaults is a GET.** Defaults is `GET /plugins/{name}/settings/defaults?instance=`, because it reads and needs no draft. Options and run stay POST.
- **The picker stores an integer.** The instance picker is `type = "integer"` with `format = "instance"`, because instance ids are integers.
- **`{name}`, not `{id}`.** Plugins are keyed by name, so the paths use `{name}`.

## Contracts for later plans

**Manifest:**

```toml
capabilities = ["ai", "settings"]

[settings.actions]
test = { label = "Test connection" }
```

`settings.actions` needs the `settings` capability. Keys match `^[a-z][a-z0-9_]*$`, and each needs a `label`.

**Config schema keywords** the form reads:

| Keyword | On | Renders |
|---|---|---|
| `default` | any field | The value shown when there's no override. Saving drops values equal to it. |
| `contentMediaType` | string | A textarea. This exists already. |
| `x-options = true` | string | A text input with a datalist, filled by `settings.options` when you press "Load choices". |
| `format = "instance"`, `x-offers = "<service>"` | integer | A dropdown of instances offering that service. |
| `type = "array"`, `items = { type = "string", enum = [...] }` | array | A checkbox group. |
| a top-level `type = "object"` property | object | Its own tab, titled with its `title`. |

**Protocol** (`protocol/settings.go`):

```go
const (
	MethodSettingsDefaults = "settings.defaults"
	MethodSettingsOptions  = "settings.options"
	MethodSettingsRun      = "settings.run"
)

// SettingsDraft is the form's unsaved config and the secrets that go with it.
type SettingsDraft struct {
	Config  map[string]any    `json:"config"`
	Secrets map[string]string `json:"secrets"`
}

// settings.defaults takes no params.
type SettingsDefaultsResult struct {
	Defaults map[string]any `json:"defaults"` // by JSON pointer, such as "/prompts/tighten"
}

type SettingsOptionsParams struct {
	SettingsDraft
	Field string `json:"field"` // JSON pointer
}
type SettingsOptionsResult struct {
	Options []SettingsOption `json:"options"`
}
type SettingsOption struct {
	Value string `json:"value"`
	Label string `json:"label,omitempty"`
}

type SettingsRunParams struct {
	SettingsDraft
	Action string `json:"action"`
}
type SettingsRunResult struct {
	OK      bool            `json:"ok"`
	Message string          `json:"message,omitempty"`
	Checks  []SettingsCheck `json:"checks,omitempty"`
}
type SettingsCheck struct {
	Name      string `json:"name"`
	OK        bool   `json:"ok"`
	Message   string `json:"message,omitempty"`
	LatencyMS int64  `json:"latency_ms,omitempty"`
}
```

- A plugin answers only the methods it needs. Core treats `MethodNotFound` from `settings.defaults` as no defaults.
- A failed options lookup is an RPC error. Its message reaches the owner, because these endpoints need the destructive tier.
- A failed test is `OK: false` with checks, not an error.

## File map

| File | Change |
|---|---|
| `protocol/manifest.go` | `settings` capability; `Settings` table with `Actions`; validation |
| `protocol/settings.go` | New: method names and param and result types |
| `protocol/manifest_test.go` | Tests for `[settings.actions]` |
| `internal/db/migrations/0026_default_hashes.sql` | New column `default_hashes` |
| `internal/plugins/defaults.go` | New: JSON pointer lookup, schema defaults, default hashing and merging |
| `internal/plugins/defaults_test.go` | New: unit tests for the above |
| `internal/plugins/plugins.go` | `DefaultBases` on `NewInstance` and `InstanceEdit`; writes `default_hashes` |
| `internal/plugins/plugins_test.go` | Hash storage test against PostgreSQL |
| `internal/plugins/settings.go` | New: `SettingsDraft` resolution, `Defaults`, `Options`, `RunSetting` |
| `internal/plugins/settings_test.go` | New: settings calls against the fake plugin |
| `internal/fakeplugin/settings.go` | New: fake `settings.*` handlers |
| `internal/fakeplugin/main.go` | Dispatch to them |
| `internal/api/instances.go` | `settings_actions` on plugins; `default_bases` on create and patch; `offers` filter |
| `internal/api/plugin_settings.go` | New: the three endpoints |
| `internal/api/plugin_settings_test.go` | New: endpoint tests |
| `web/openapi.json`, `web/src/api/schema.d.ts` | Regenerated with `npm run gen`. Never hand-edit. |
| `web/src/settings/SchemaForm.tsx` | New keywords, `path`, `FormContext`, reset, mark, checkbox group, datalist, picker |
| `web/src/settings/SchemaForm.test.tsx` | Tests for each |
| `web/src/settings/Instance.tsx` | Tabs, defaults query, bases on save, settings buttons, options loader |
| `web/src/settings/Instance.test.tsx` | Tests for tabs, reset round trip, Test, Load choices |
| `web/src/settings/Settings.css` | Tab and reset button styles |

---

### Task 1: Protocol: the `settings` capability and manifest table

**Files:**
- Create: `protocol/settings.go`
- Modify: `protocol/manifest.go`
- Test: `protocol/manifest_test.go`

- [ ] **Step 1: Write the failing test**

Append to `protocol/manifest_test.go`:

```go
func TestManifestSettingsActions(t *testing.T) {
	base := `
name = "ai"
version = "0.1.0"
protocol = 1
command = ["./ai"]
`
	m, err := LoadManifest(writeManifest(t, base+`capabilities = ["settings"]

[settings.actions]
test = { label = "Test connection" }
`))
	if err != nil {
		t.Fatal(err)
	}
	if m.Settings.Actions["test"].Label != "Test connection" {
		t.Fatalf("actions = %+v", m.Settings.Actions)
	}
	for name, body := range map[string]string{
		"no capability": `capabilities = ["action"]
[settings.actions]
test = { label = "Test" }
`,
		"no label": `capabilities = ["settings"]
[settings.actions]
test = {}
`,
		"bad key": `capabilities = ["settings"]
[settings.actions]
Test-It = { label = "Test" }
`,
	} {
		if _, err := LoadManifest(writeManifest(t, base+body)); err == nil {
			t.Errorf("%s: loaded", name)
		}
	}
}
```

- [ ] **Step 2: Run the test and check it fails**

Run: `go test ./protocol -run TestManifestSettingsActions`
Expected: FAIL to compile, because `m.Settings` is undefined.

- [ ] **Step 3: Write the code**

In `protocol/manifest.go`, add `"settings"` to `Capabilities`:

```go
var Capabilities = []string{"import", "search", "push", "pull", "action", "records", "enrich", "codec", "auth", "mcp", "theme", "catalog", "sites", "settings"}
```

Add the field to `Manifest`, after `Records`:

```go
	// Settings holds the buttons a settings plugin adds to its instance form.
	Settings Settings `toml:"settings"`
```

Add the types after `Enrichment`:

```go
// Settings is the manifest's [settings] table.
type Settings struct {
	// Actions are buttons on the instance form, by id; each runs settings.run.
	Actions map[string]SettingsAction `toml:"actions"`
}

// SettingsAction is one button on the instance form.
type SettingsAction struct {
	Label string `toml:"label"`
}
```

In `Validate`, before `return errors.Join(errs...)`:

```go
	if len(m.Settings.Actions) > 0 && !m.Has("settings") {
		errs = append(errs, errors.New("settings.actions needs the settings capability"))
	}
	for _, id := range slices.Sorted(maps.Keys(m.Settings.Actions)) {
		if !keyPattern.MatchString(id) {
			errs = append(errs, fmt.Errorf("settings action %q must match %s", id, keyPattern))
		}
		if strings.TrimSpace(m.Settings.Actions[id].Label) == "" {
			errs = append(errs, fmt.Errorf("settings action %q: label is required", id))
		}
	}
```

Create `protocol/settings.go`:

```go
package protocol

// The settings capability serves the instance form. Its calls are stateless:
// the core sends them to a process started with an empty config, and each
// carries the form's unsaved draft.
const (
	MethodSettingsDefaults = "settings.defaults"
	MethodSettingsOptions  = "settings.options"
	MethodSettingsRun      = "settings.run"
)

// SettingsDraft is the form's unsaved config and the secrets that go with it:
// the saved ones with the form's changes on top.
type SettingsDraft struct {
	Config  map[string]any    `json:"config"`
	Secrets map[string]string `json:"secrets"`
}

// SettingsDefaultsResult holds defaults the manifest can't, by JSON pointer
// into the config, such as "/prompts/tighten".
type SettingsDefaultsResult struct {
	Defaults map[string]any `json:"defaults"`
}

// SettingsOptionsParams asks for the choices of the config field at the JSON
// pointer Field.
type SettingsOptionsParams struct {
	SettingsDraft
	Field string `json:"field"`
}

// SettingsOptionsResult lists a field's choices.
type SettingsOptionsResult struct {
	Options []SettingsOption `json:"options"`
}

// SettingsOption is one choice; Label defaults to Value.
type SettingsOption struct {
	Value string `json:"value"`
	Label string `json:"label,omitempty"`
}

// SettingsRunParams runs one of the manifest's settings.actions.
type SettingsRunParams struct {
	SettingsDraft
	Action string `json:"action"`
}

// SettingsRunResult is what the button reports. A failed check is OK false,
// not an error.
type SettingsRunResult struct {
	OK      bool            `json:"ok"`
	Message string          `json:"message,omitempty"`
	Checks  []SettingsCheck `json:"checks,omitempty"`
}

// SettingsCheck is one row of a run's report.
type SettingsCheck struct {
	Name      string `json:"name"`
	OK        bool   `json:"ok"`
	Message   string `json:"message,omitempty"`
	LatencyMS int64  `json:"latency_ms,omitempty"`
}
```

- [ ] **Step 4: Run the tests and check they pass**

Run: `go test ./protocol`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add protocol/manifest.go protocol/manifest_test.go protocol/settings.go
git commit -m "feat(protocol): add the settings capability and settings.actions"
```

---

### Task 2: Default helpers (JSON pointer, schema defaults, hashes)

**Files:**
- Create: `internal/plugins/defaults.go`
- Test: `internal/plugins/defaults_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/plugins/defaults_test.go`:

```go
package plugins

import (
	"maps"
	"testing"
)

func TestSchemaDefaultsFindsNestedDefaults(t *testing.T) {
	schema := map[string]any{"type": "object", "properties": map[string]any{
		"url":     map[string]any{"type": "string", "default": "http://localhost"},
		"a/b":     map[string]any{"type": "integer", "default": int64(3)},
		"prompts": map[string]any{"type": "object", "properties": map[string]any{"greet": map[string]any{"type": "string", "default": "Hi"}}},
		"model":   map[string]any{"type": "string"},
	}}
	got := schemaDefaults(schema)
	want := map[string]any{"/url": "http://localhost", "/a~1b": int64(3), "/prompts/greet": "Hi"}
	if !maps.Equal(got, want) {
		t.Fatalf("defaults = %v, want %v", got, want)
	}
}

func TestLookupFollowsPointer(t *testing.T) {
	config := map[string]any{"prompts": map[string]any{"greet": "Yo"}, "a/b": 1.0}
	if v, ok := lookup(config, "/prompts/greet"); !ok || v != "Yo" {
		t.Errorf("/prompts/greet = %v, %v", v, ok)
	}
	if v, ok := lookup(config, "/a~1b"); !ok || v != 1.0 {
		t.Errorf("/a~1b = %v, %v", v, ok)
	}
	if _, ok := lookup(config, "/prompts/missing"); ok {
		t.Error("found a missing key")
	}
	if _, ok := lookup(config, "/prompts/greet/deeper"); ok {
		t.Error("walked into a string")
	}
}

func TestMergeDefaultHashesKeepsOldBasesForUntouchedOverrides(t *testing.T) {
	current := map[string]string{"/p/a": hashDefault("old A"), "/p/gone": hashDefault("x")}
	config := map[string]any{"p": map[string]any{"a": "mine", "b": "mine too"}}
	got := mergeDefaultHashes(current, config, map[string]any{"/p/b": "default B", "/p/none": "y"})
	want := map[string]string{"/p/a": hashDefault("old A"), "/p/b": hashDefault("default B")}
	if !maps.Equal(got, want) {
		t.Fatalf("hashes = %v, want %v", got, want)
	}
}

func TestHashDefaultIgnoresKeyOrder(t *testing.T) {
	if hashDefault(map[string]any{"a": 1, "b": 2}) != hashDefault(map[string]any{"b": 2, "a": 1}) {
		t.Fatal("key order changed the hash")
	}
}
```

- [ ] **Step 2: Run the test and check it fails**

Run: `go test ./internal/plugins -run 'SchemaDefaults|Lookup|MergeDefaultHashes|HashDefault'`
Expected: FAIL to compile, because `schemaDefaults` is undefined.

- [ ] **Step 3: Write the code**

Create `internal/plugins/defaults.go`:

```go
package plugins

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
)

var pointerEscape = strings.NewReplacer("~", "~0", "/", "~1")
var pointerUnescape = strings.NewReplacer("~1", "/", "~0", "~")

// schemaDefaults returns each "default" in a config schema by JSON pointer.
func schemaDefaults(schema map[string]any) map[string]any {
	out := map[string]any{}
	var walk func(s map[string]any, at string)
	walk = func(s map[string]any, at string) {
		if d, ok := s["default"]; ok && at != "" {
			out[at] = d
		}
		props, _ := s["properties"].(map[string]any)
		for k, p := range props {
			if ps, ok := p.(map[string]any); ok {
				walk(ps, at+"/"+pointerEscape.Replace(k))
			}
		}
	}
	walk(schema, "")
	return out
}

// lookup returns the value at a JSON pointer into config.
func lookup(config map[string]any, pointer string) (any, bool) {
	var cur any = config
	for _, tok := range strings.Split(strings.TrimPrefix(pointer, "/"), "/") {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = obj[pointerUnescape.Replace(tok)]; !ok {
			return nil, false
		}
	}
	return cur, true
}

// hashDefault fingerprints a default value. json.Marshal sorts map keys, so
// equal values hash the same.
func hashDefault(v any) string {
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// mergeDefaultHashes is the hash of the default each override in config
// replaced: bases for overrides the owner just set, current for the rest.
// A pointer config no longer overrides is dropped.
func mergeDefaultHashes(current map[string]string, config map[string]any, bases map[string]any) map[string]string {
	out := map[string]string{}
	for p, h := range current {
		if _, ok := lookup(config, p); ok {
			out[p] = h
		}
	}
	for p, d := range bases {
		if _, ok := lookup(config, p); ok {
			out[p] = hashDefault(d)
		}
	}
	return out
}
```

- [ ] **Step 4: Run the tests and check they pass**

Run: `go test ./internal/plugins -run 'SchemaDefaults|Lookup|MergeDefaultHashes|HashDefault'`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/plugins/defaults.go internal/plugins/defaults_test.go
git commit -m "feat(plugins): find config defaults and hash the ones an override replaces"
```

---

### Task 3: Store default hashes with the instance

**Files:**
- Create: `internal/db/migrations/0026_default_hashes.sql`
- Modify: `internal/plugins/plugins.go` (`NewInstance`, `InstanceEdit`, `CreateInstance`, `UpdateInstance`; new `DefaultHashes`)
- Test: `internal/plugins/plugins_test.go`

These tests need PostgreSQL: set `INNKEEPER_TEST_DATABASE_URL`, or they skip.

- [ ] **Step 1: Write the failing test**

Append to `internal/plugins/plugins_test.go`:

```go
func TestInstanceKeepsHashOfReplacedDefault(t *testing.T) {
	ctx := context.Background()
	reg, _ := newRegistry(t)
	id, err := reg.CreateInstance(ctx, NewInstance{Plugin: "fake", Name: "Fake",
		Config: map[string]any{"p": map[string]any{"a": "mine"}}, DefaultBases: map[string]any{"/p/a": "default A"}})
	if err != nil {
		t.Fatal(err)
	}
	hashes, err := reg.DefaultHashes(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if hashes["/p/a"] != hashDefault("default A") {
		t.Fatalf("after create: %v", hashes)
	}

	// A save that leaves /p/a alone keeps its base; a new override gets its own.
	cfg := map[string]any{"p": map[string]any{"a": "mine", "b": "also mine"}}
	if err := reg.UpdateInstance(ctx, id, InstanceEdit{Config: cfg, DefaultBases: map[string]any{"/p/b": "default B"}}); err != nil {
		t.Fatal(err)
	}
	if hashes, _ = reg.DefaultHashes(ctx, id); hashes["/p/a"] != hashDefault("default A") || hashes["/p/b"] != hashDefault("default B") {
		t.Fatalf("after adding b: %v", hashes)
	}

	// Resetting /p/a drops its hash.
	if err := reg.UpdateInstance(ctx, id, InstanceEdit{Config: map[string]any{"p": map[string]any{"b": "also mine"}}}); err != nil {
		t.Fatal(err)
	}
	if hashes, _ = reg.DefaultHashes(ctx, id); len(hashes) != 1 || hashes["/p/b"] == "" {
		t.Fatalf("after reset: %v", hashes)
	}
}
```

- [ ] **Step 2: Run the test and check it fails**

Run: `go test ./internal/plugins -run TestInstanceKeepsHashOfReplacedDefault`
Expected: FAIL to compile, because `DefaultBases` is unknown.

- [ ] **Step 3: Write the migration**

Create `internal/db/migrations/0026_default_hashes.sql`:

```sql
-- For each config override, a hash of the default it replaced, so the form can mark one whose default has since changed.
ALTER TABLE plugin_instance ADD COLUMN default_hashes jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(default_hashes) = 'object');
```

- [ ] **Step 4: Write the code**

In `internal/plugins/plugins.go`, add a field to both `NewInstance` and `InstanceEdit`:

```go
	// DefaultBases maps a JSON pointer the owner just overrode to the default it replaced.
	DefaultBases map[string]any
```

In `CreateInstance`, after `if n.Config == nil { ... }`, compute the hashes and add them to the insert:

```go
	hashes := mergeDefaultHashes(nil, n.Config, n.DefaultBases)
	var id int64
	err = r.pool.QueryRow(ctx, `INSERT INTO plugin_instance (plugin, name, config, secrets, owner_subject, library_access, default_hashes)
		VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`, n.Plugin, n.Name, n.Config, sealed, n.OwnerSubject, n.LibraryAccess, hashes).Scan(&id)
```

In `UpdateInstance`, read the current hashes with the locked row:

```go
	var plugin string
	var sealed []byte
	var hashes map[string]string
	err = tx.QueryRow(ctx, `SELECT plugin, secrets, default_hashes FROM plugin_instance WHERE id = $1 FOR UPDATE`, id).Scan(&plugin, &sealed, &hashes)
```

Replace the `var config any` block with one that also sets hashes:

```go
	// An untyped nil sends SQL NULL, which keeps the current config and hashes.
	var config, newHashes any
	if e.Config != nil {
		config, newHashes = e.Config, mergeDefaultHashes(hashes, e.Config, e.DefaultBases)
	}
```

Then change the `UPDATE` to set the column, adding `$8`:

```go
	_, err = tx.Exec(ctx, `UPDATE plugin_instance SET name = coalesce($2, name), config = coalesce($3, config),
			secrets = $4, owner_subject = CASE WHEN $5 THEN nullif($6::text, '') ELSE owner_subject END,
			library_access = coalesce($7, library_access), default_hashes = coalesce($8, default_hashes),
			broken_error = NULL, broken_at = NULL, updated_at = now()
		WHERE id = $1`, id, e.Name, config, sealed, e.OwnerSubject != nil, owner, e.LibraryAccess, newHashes)
```

Add after `GetInstance`:

```go
// DefaultHashes returns, for each of an instance's config overrides, the
// hash of the default it replaced, by JSON pointer.
func (r *Registry) DefaultHashes(ctx context.Context, id int64) (map[string]string, error) {
	var h map[string]string
	err := r.pool.QueryRow(ctx, `SELECT default_hashes FROM plugin_instance WHERE id = $1`, id).Scan(&h)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return h, err
}
```

- [ ] **Step 5: Run the tests and check they pass**

Run: `go test ./internal/plugins ./internal/db`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/db/migrations/0026_default_hashes.sql internal/plugins/plugins.go internal/plugins/plugins_test.go
git commit -m "feat(plugins): keep a hash of the default each config override replaced"
```

---

### Task 4: Fake plugin settings handlers

**Files:**
- Create: `internal/fakeplugin/settings.go`
- Modify: `internal/fakeplugin/main.go`

The fake plugin's answers show which draft it got. Options echoes `config.url`, and the Test check passes only when the `api_key` secret is `hunter2`.

- [ ] **Step 1: Write the handlers**

Create `internal/fakeplugin/settings.go`:

```go
package main

import (
	"encoding/json"

	"github.com/inkandale/innkeeper/protocol"
)

func settingsCall(method string, params json.RawMessage) (any, error) {
	switch method {
	case protocol.MethodSettingsDefaults:
		return protocol.SettingsDefaultsResult{Defaults: map[string]any{"/prompts/greet": "Hello, {{user}}."}}, nil
	case protocol.MethodSettingsOptions:
		var p protocol.SettingsOptionsParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, protocol.NewError(protocol.CodeInvalidParams, err.Error(), false)
		}
		if p.Field != "/model" {
			return nil, protocol.NewError(protocol.CodeInvalidParams, "no choices for "+p.Field, false)
		}
		url, _ := p.Config["url"].(string)
		if url == "" {
			return nil, protocol.NewError(-32000, "set the url first", false)
		}
		return protocol.SettingsOptionsResult{Options: []protocol.SettingsOption{{Value: url + "/m1", Label: "Model one"}, {Value: url + "/m2"}}}, nil
	case protocol.MethodSettingsRun:
		var p protocol.SettingsRunParams
		if err := json.Unmarshal(params, &p); err != nil || p.Action != "test" {
			return nil, protocol.NewError(protocol.CodeInvalidParams, "unknown action", false)
		}
		ok := p.Secrets["api_key"] == "hunter2"
		msg := "Works."
		if !ok {
			msg = "The key was refused."
		}
		return protocol.SettingsRunResult{OK: ok, Message: msg, Checks: []protocol.SettingsCheck{{Name: "chat", OK: ok, LatencyMS: 1}}}, nil
	}
	return nil, protocol.MethodNotFound(method)
}
```

In `internal/fakeplugin/main.go`, add a case before `case protocol.MethodAuthBegin:`:

```go
		case protocol.MethodSettingsDefaults, protocol.MethodSettingsOptions, protocol.MethodSettingsRun:
			return settingsCall(method, params)
```

- [ ] **Step 2: Build it**

Run: `go build ./internal/fakeplugin`
Expected: no output.

- [ ] **Step 3: Commit**

```bash
git add internal/fakeplugin/settings.go internal/fakeplugin/main.go
git commit -m "test(fakeplugin): answer settings.defaults, options, and run"
```

---

### Task 5: Registry settings calls

**Files:**
- Create: `internal/plugins/settings.go`
- Test: `internal/plugins/settings_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/plugins/settings_test.go`:

```go
package plugins

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/inkandale/innkeeper/internal/blobstore"
	"github.com/inkandale/innkeeper/internal/pluginhost"
	"github.com/inkandale/innkeeper/internal/secrets"
	"github.com/inkandale/innkeeper/internal/testdb"
	"github.com/inkandale/innkeeper/internal/testutil"
	"github.com/inkandale/innkeeper/protocol"
)

// settingsRegistry has "conf", the fake plugin as a settings plugin with an
// api_key secret, a url default, and a Test button.
func settingsRegistry(t *testing.T) (*Registry, *pluginhost.Manager) {
	t.Helper()
	m, err := protocol.LoadManifest(testutil.FakePluginWith(t, []string{"settings"}))
	if err != nil {
		t.Fatal(err)
	}
	m.Name, m.Secrets = "conf", []string{"api_key"}
	m.Command = append(m.Command, "-name=conf")
	m.Config = map[string]any{"type": "object", "properties": map[string]any{
		"url":     map[string]any{"type": "string", "default": "http://localhost"},
		"prompts": map[string]any{"type": "object", "properties": map[string]any{"greet": map[string]any{"type": "string"}}},
	}}
	m.Settings.Actions = map[string]protocol.SettingsAction{"test": {Label: "Test connection"}}
	other := &protocol.Manifest{Name: "other", Version: "1.0.0", Secrets: []string{"api_key"}}
	box, err := secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	store := &blobstore.Store{Name: "library", Root: t.TempDir()}
	reg := New(testdb.New(t), map[string]*protocol.Manifest{"conf": m, "other": other}, store, box)
	mgr := pluginhost.NewManager(nil, time.Minute, quiet)
	t.Cleanup(mgr.Close)
	return reg, mgr
}

func TestDefaultsJoinsSchemaAndPluginAndMarksUpdated(t *testing.T) {
	ctx := context.Background()
	reg, mgr := settingsRegistry(t)
	id, err := reg.CreateInstance(ctx, NewInstance{Plugin: "conf", Name: "C",
		Config:       map[string]any{"url": "http://mine", "prompts": map[string]any{"greet": "Yo"}},
		DefaultBases: map[string]any{"/url": "http://localhost", "/prompts/greet": "an older default"}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := reg.Defaults(ctx, mgr, "conf", &id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Defaults["/url"] != "http://localhost" || got.Defaults["/prompts/greet"] != "Hello, {{user}}." {
		t.Fatalf("defaults = %v", got.Defaults)
	}
	if !slices.Equal(got.Updated, []string{"/prompts/greet"}) {
		t.Fatalf("updated = %v, want only /prompts/greet", got.Updated)
	}
}

func TestSettingsCallsLayerDraftSecretsOnSaved(t *testing.T) {
	ctx := context.Background()
	reg, mgr := settingsRegistry(t)
	id, err := reg.CreateInstance(ctx, NewInstance{Plugin: "conf", Name: "C", Secrets: map[string]string{"api_key": "hunter2"}})
	if err != nil {
		t.Fatal(err)
	}
	res, err := reg.RunSetting(ctx, mgr, "conf", "test", Draft{Instance: &id})
	if err != nil || !res.OK {
		t.Fatalf("saved key: %+v, %v", res, err)
	}
	wrong := "nope"
	if res, _ := reg.RunSetting(ctx, mgr, "conf", "test", Draft{Instance: &id, Secrets: map[string]*string{"api_key": &wrong}}); res.OK {
		t.Fatal("a typed key didn't replace the saved one")
	}
	if res, _ := reg.RunSetting(ctx, mgr, "conf", "test", Draft{Instance: &id, Secrets: map[string]*string{"api_key": nil}}); res.OK {
		t.Fatal("a removed key was still sent")
	}
	opts, err := reg.Options(ctx, mgr, "conf", "/model", Draft{Config: map[string]any{"url": "http://draft"}})
	if err != nil || len(opts.Options) != 2 || opts.Options[0].Value != "http://draft/m1" {
		t.Fatalf("options = %+v, %v", opts, err)
	}
}

func TestSettingsCallsRefuseAnotherPluginsInstance(t *testing.T) {
	ctx := context.Background()
	reg, mgr := settingsRegistry(t)
	id, err := reg.CreateInstance(ctx, NewInstance{Plugin: "other", Name: "O", Secrets: map[string]string{"api_key": "hunter2"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reg.RunSetting(ctx, mgr, "conf", "test", Draft{Instance: &id}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
	if _, err := reg.RunSetting(ctx, mgr, "conf", "deploy", Draft{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("undeclared action: %v, want ErrInvalid", err)
	}
	if _, err := reg.RunSetting(ctx, mgr, "conf", "test", Draft{Secrets: map[string]*string{"password": new(string)}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown secret: %v, want ErrInvalid", err)
	}
	if _, err := reg.RunSetting(ctx, mgr, "other", "test", Draft{}); !errors.Is(err, ErrIncapable) {
		t.Fatalf("plugin without settings: %v, want ErrIncapable", err)
	}
}
```

- [ ] **Step 2: Run the test and check it fails**

Run: `go test ./internal/plugins -run 'Defaults|SettingsCalls'`
Expected: FAIL to compile, because `reg.Defaults` is undefined.

- [ ] **Step 3: Write the code**

Create `internal/plugins/settings.go`:

```go
package plugins

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/inkandale/innkeeper/internal/pluginhost"
	"github.com/inkandale/innkeeper/protocol"
)

// Draft is the instance form's unsaved state. Secrets go on top of the saved
// instance's, if Instance is set; a nil value removes one.
type Draft struct {
	Instance *int64
	Config   map[string]any
	Secrets  map[string]*string
}

// Defaults is a plugin's config defaults by JSON pointer, and which of an
// instance's overrides replaced a default that has since changed.
type Defaults struct {
	Defaults map[string]any `json:"defaults"`
	Updated  []string       `json:"updated" doc:"JSON pointers of overrides whose default changed after they were saved."`
}

// settingsInstance is the plugin's process for settings calls: started with
// an empty config, and replaced when the plugin's install changes.
func (r *Registry) settingsInstance(ctx context.Context, plugin string) (pluginhost.Instance, error) {
	m, ok := r.manifest(plugin)
	if !ok {
		return pluginhost.Instance{}, fmt.Errorf("%w: %s", ErrUnknownPlugin, plugin)
	}
	if !m.Has("settings") {
		return pluginhost.Instance{}, fmt.Errorf("%w: plugin %s has no settings calls", ErrIncapable, plugin)
	}
	var changed time.Time
	err := r.pool.QueryRow(ctx, `SELECT changed_at FROM plugin_install WHERE name = $1`, plugin).Scan(&changed)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return pluginhost.Instance{}, err
	}
	return pluginhost.Instance{ID: "settings-" + plugin, Version: strconv.FormatInt(changed.UnixMicro(), 10), Manifest: m}, nil
}

// draft resolves d into what a settings call sends.
func (r *Registry) draft(ctx context.Context, m *protocol.Manifest, d Draft) (protocol.SettingsDraft, error) {
	out := protocol.SettingsDraft{Config: d.Config, Secrets: map[string]string{}}
	if out.Config == nil {
		out.Config = map[string]any{}
	}
	if d.Instance != nil {
		var plugin string
		var sealed []byte
		err := r.pool.QueryRow(ctx, `SELECT plugin, secrets FROM plugin_instance WHERE id = $1`, *d.Instance).Scan(&plugin, &sealed)
		if errors.Is(err, pgx.ErrNoRows) {
			return out, ErrNotFound
		}
		if err != nil {
			return out, err
		}
		// Another plugin's saved secrets must never reach this one.
		if plugin != m.Name {
			return out, fmt.Errorf("%w: instance %d belongs to %s, not %s", ErrInvalid, *d.Instance, plugin, m.Name)
		}
		if out.Secrets, err = r.box.Open(sealed); err != nil {
			return out, err
		}
	}
	for name, v := range d.Secrets {
		if err := checkSecret(m, name); err != nil {
			return out, err
		}
		if v == nil {
			delete(out.Secrets, name)
		} else {
			out.Secrets[name] = *v
		}
	}
	return out, nil
}

// Defaults joins the config schema's defaults with the plugin's. With an
// instance, it also lists the overrides whose default has changed.
func (r *Registry) Defaults(ctx context.Context, mgr *pluginhost.Manager, plugin string, instance *int64) (Defaults, error) {
	m, ok := r.manifest(plugin)
	if !ok {
		return Defaults{}, fmt.Errorf("%w: %s", ErrUnknownPlugin, plugin)
	}
	out := Defaults{Defaults: schemaDefaults(m.Config), Updated: []string{}}
	if m.Has("settings") {
		inst, err := r.settingsInstance(ctx, plugin)
		if err != nil {
			return out, err
		}
		var res protocol.SettingsDefaultsResult
		err = mgr.Call(ctx, inst, protocol.MethodSettingsDefaults, nil, &res)
		var rpcErr *protocol.Error
		if err != nil && !(errors.As(err, &rpcErr) && rpcErr.Code == protocol.CodeMethodNotFound) {
			return out, err
		}
		maps.Copy(out.Defaults, res.Defaults)
	}
	if instance == nil {
		return out, nil
	}
	hashes, err := r.DefaultHashes(ctx, *instance)
	if err != nil {
		return out, err
	}
	for p, h := range hashes {
		if d, ok := out.Defaults[p]; ok && hashDefault(d) != h {
			out.Updated = append(out.Updated, p)
		}
	}
	slices.Sort(out.Updated)
	return out, nil
}

// Options asks the plugin for the choices of the config field at pointer.
func (r *Registry) Options(ctx context.Context, mgr *pluginhost.Manager, plugin, pointer string, d Draft) (protocol.SettingsOptionsResult, error) {
	var res protocol.SettingsOptionsResult
	inst, err := r.settingsInstance(ctx, plugin)
	if err != nil {
		return res, err
	}
	draft, err := r.draft(ctx, inst.Manifest, d)
	if err != nil {
		return res, err
	}
	err = mgr.Call(ctx, inst, protocol.MethodSettingsOptions, protocol.SettingsOptionsParams{SettingsDraft: draft, Field: pointer}, &res)
	return res, err
}

// RunSetting runs one of the plugin's settings.actions against the draft.
func (r *Registry) RunSetting(ctx context.Context, mgr *pluginhost.Manager, plugin, action string, d Draft) (protocol.SettingsRunResult, error) {
	var res protocol.SettingsRunResult
	inst, err := r.settingsInstance(ctx, plugin)
	if err != nil {
		return res, err
	}
	if _, ok := inst.Manifest.Settings.Actions[action]; !ok {
		return res, fmt.Errorf("%w: plugin %s has no settings action %q", ErrInvalid, plugin, action)
	}
	draft, err := r.draft(ctx, inst.Manifest, d)
	if err != nil {
		return res, err
	}
	err = mgr.Call(ctx, inst, protocol.MethodSettingsRun, protocol.SettingsRunParams{SettingsDraft: draft, Action: action}, &res)
	return res, err
}
```

`settingsInstance` leaves `Params` empty, and `pluginhost.Start` fills an empty config, secrets and blob stores. `changed` stays the zero time for a builtin plugin with no install row, so its version is a fixed number until the image changes.

- [ ] **Step 4: Run the tests and check they pass**

Run: `go test ./internal/plugins`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/plugins/settings.go internal/plugins/settings_test.go
git commit -m "feat(plugins): run settings calls against the form's draft"
```

---

### Task 6: API endpoints, settings_actions, default_bases, offers filter

**Files:**
- Create: `internal/api/plugin_settings.go`
- Modify: `internal/api/instances.go`
- Modify: `internal/api/server.go:170` (register routes)
- Test: `internal/api/plugin_settings_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/api/plugin_settings_test.go`:

```go
package api

import (
	"fmt"
	"strings"
	"testing"

	"github.com/inkandale/innkeeper/internal/plugins"
	"github.com/inkandale/innkeeper/internal/tier"
	"github.com/inkandale/innkeeper/protocol"
)

// addConf adds "conf", the fake plugin as a settings plugin with a Test button.
func (e *testEnv) addConf() {
	conf := *e.manifests["fake"]
	conf.Name, conf.Capabilities, conf.Secrets = "conf", []string{"settings"}, []string{"api_key"}
	conf.Command = []string{conf.Command[0], "-caps=settings", "-name=conf"}
	conf.Config = map[string]any{"type": "object", "properties": map[string]any{
		"url":     map[string]any{"type": "string", "default": "http://localhost"},
		"prompts": map[string]any{"type": "object", "properties": map[string]any{"greet": map[string]any{"type": "string"}}},
	}}
	conf.Settings.Actions = map[string]protocol.SettingsAction{"test": {Label: "Test connection"}}
	e.manifests["conf"] = &conf
}

func TestPluginSettingsRoutes(t *testing.T) {
	e := newEnv(t)
	e.addConf()

	var listed []pluginOut
	e.call("GET", "/plugins", nil, 200, &listed)
	var conf pluginOut
	for _, p := range listed {
		if p.Name == "conf" {
			conf = p
		}
	}
	if conf.SettingsActions["test"] != "Test connection" {
		t.Fatalf("settings_actions = %v", conf.SettingsActions)
	}

	var inst plugins.InstanceInfo
	e.call("POST", "/instances", map[string]any{"plugin": "conf", "name": "C",
		"config":        map[string]any{"prompts": map[string]any{"greet": "Yo"}},
		"default_bases": map[string]any{"/prompts/greet": "an older default"},
		"secrets":       map[string]string{"api_key": "hunter2"}}, 201, &inst)

	var defs plugins.Defaults
	e.call("GET", fmt.Sprintf("/plugins/conf/settings/defaults?instance=%d", inst.ID), nil, 200, &defs)
	if defs.Defaults["/prompts/greet"] != "Hello, {{user}}." || len(defs.Updated) != 1 {
		t.Fatalf("defaults = %+v", defs)
	}

	var run protocol.SettingsRunResult
	r := e.call("POST", "/plugins/conf/settings/run", map[string]any{"action": "test", "instance": inst.ID}, 200, &run)
	if !run.OK || strings.Contains(string(r.Body), "hunter2") {
		t.Fatalf("run = %s", r)
	}
	var opts protocol.SettingsOptionsResult
	e.call("POST", "/plugins/conf/settings/options", map[string]any{"field": "/model", "config": map[string]any{"url": "http://draft"}}, 200, &opts)
	if len(opts.Options) != 2 {
		t.Fatalf("options = %+v", opts)
	}
	// The plugin's own message reaches the owner.
	r = e.call("POST", "/plugins/conf/settings/options", map[string]any{"field": "/model"}, 502, nil)
	if !strings.Contains(string(r.Body), "set the url first") {
		t.Fatalf("options error = %s", r)
	}
	e.call("POST", "/plugins/conf/settings/run", map[string]any{"action": "deploy"}, 400, nil)
	e.call("POST", "/plugins/vault/settings/run", map[string]any{"action": "test"}, 400, nil)
}

// A draft can point the plugin anywhere and send the saved key there, so
// options and run need the tier that can change the instance.
func TestPluginSettingsCallsNeedDestructive(t *testing.T) {
	e := newEnv(t)
	e.addConf()
	read := "Bearer " + e.tokenWith(tier.Read)
	for _, path := range []string{"/plugins/conf/settings/run", "/plugins/conf/settings/options"} {
		if r := e.send("POST", path, map[string]any{"action": "test", "field": "/model"}, "Authorization", read); r.Status != 403 {
			t.Errorf("%s with read tier: %s", path, r)
		}
	}
	if r := e.send("GET", "/plugins/conf/settings/defaults", nil, "Authorization", read); r.Status != 200 {
		t.Errorf("defaults with read tier: %s", r)
	}
}
```

Before writing this test, check the 403 status `e.send` returns for a too-low tier against an existing tier test in `internal/api`, such as `tiers_test.go`. If it's 401, change the expected status.

- [ ] **Step 2: Run the test and check it fails**

Run: `go test ./internal/api -run PluginSettings`
Expected: FAIL to compile, because `SettingsActions` is undefined.

- [ ] **Step 3: Write the code**

In `internal/api/instances.go`:

1. Add a field to `pluginOut`:

```go
	SettingsActions map[string]string        `json:"settings_actions,omitempty" doc:"Buttons the instance form shows: label by action id, run with POST /plugins/{name}/settings/run"`
```

   In list-plugins, after the `for k, rt := range m.Records` loop:

```go
				for id, a := range m.Settings.Actions {
					if p.SettingsActions == nil {
						p.SettingsActions = map[string]string{}
					}
					p.SettingsActions[id] = a.Label
				}
```

2. Add a field to the `Body` of both `createInstanceIn` and `updateInstanceIn`:

```go
		DefaultBases map[string]any `json:"default_bases,omitempty" doc:"For each JSON pointer the owner just overrode, the default it replaced, so the form can mark it once the default changes."`
```

   Pass it on, as `DefaultBases: in.Body.DefaultBases`, in both the `plugins.NewInstance{...}` and `plugins.InstanceEdit{...}` literals.

3. Filter list-instances by service. Give it an input type:

```go
type listInstancesIn struct {
	Offers string `query:"offers" doc:"Only instances that offer this service, such as ai.chat"`
}
```

   Change its handler:

```go
		func(ctx context.Context, in *listInstancesIn) (*instancesOut, error) {
			list, err := s.Registry.ListInstances(ctx)
			if in.Offers != "" {
				list, err = s.Registry.InstancesOffering(ctx, s.Plugins, in.Offers)
			}
			if err != nil {
				return nil, s.fail(err)
			}
			return &instancesOut{Body: orEmpty(list)}, nil
		})
```

Create `internal/api/plugin_settings.go`:

```go
package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/inkandale/innkeeper/internal/plugins"
	"github.com/inkandale/innkeeper/internal/tier"
	"github.com/inkandale/innkeeper/protocol"
)

type pluginNameIn struct {
	Name string `path:"name"`
}

type draftBody struct {
	Instance *int64             `json:"instance,omitempty" doc:"An instance of this plugin whose saved secrets the draft starts from"`
	Config   map[string]any     `json:"config,omitempty" doc:"The form's unsaved config"`
	Secrets  map[string]*string `json:"secrets,omitempty" doc:"Typed secrets; null removes a saved one. Write-only."`
}

func (d draftBody) draft() plugins.Draft {
	return plugins.Draft{Instance: d.Instance, Config: d.Config, Secrets: d.Secrets}
}

type defaultsIn struct {
	pluginNameIn
	Instance int64 `query:"instance" doc:"Also list this instance's overrides whose default changed"`
}

type defaultsOut struct{ Body plugins.Defaults }

type optionsIn struct {
	pluginNameIn
	Body struct {
		draftBody
		Field string `json:"field" doc:"JSON pointer to the config field"`
	}
}

type optionsOut struct{ Body protocol.SettingsOptionsResult }

type runSettingIn struct {
	pluginNameIn
	Body struct {
		draftBody
		Action string `json:"action" doc:"One of the plugin's settings_actions"`
	}
}

type runSettingOut struct{ Body protocol.SettingsRunResult }

func (s *Server) pluginSettingsRoutes(api huma.API) {
	tags := []string{"Plugins"}
	huma.Register(api, huma.Operation{Metadata: needs(tier.Read), OperationID: "get-plugin-defaults", Method: http.MethodGet,
		Path: "/plugins/{name}/settings/defaults", Summary: "Get a plugin's config defaults", Tags: tags},
		func(ctx context.Context, in *defaultsIn) (*defaultsOut, error) {
			var inst *int64
			if in.Instance != 0 {
				inst = &in.Instance
			}
			d, err := s.Registry.Defaults(ctx, s.Plugins, in.Name, inst)
			if err != nil {
				return nil, s.settingsFail(err)
			}
			return &defaultsOut{Body: d}, nil
		})

	// A draft can name any address and send the saved secrets there, which
	// is what changing the instance can do; so these need that tier.
	huma.Register(api, huma.Operation{Metadata: needs(tier.Destructive), OperationID: "get-plugin-options", Method: http.MethodPost,
		Path: "/plugins/{name}/settings/options", Summary: "List the choices for a config field", Tags: tags},
		func(ctx context.Context, in *optionsIn) (*optionsOut, error) {
			if err := s.loginGuard(ctx, in.Name, false); err != nil {
				return nil, err
			}
			res, err := s.Registry.Options(ctx, s.Plugins, in.Name, in.Body.Field, in.Body.draft())
			if err != nil {
				return nil, s.settingsFail(err)
			}
			return &optionsOut{Body: res}, nil
		})

	huma.Register(api, huma.Operation{Metadata: needs(tier.Destructive), OperationID: "run-plugin-setting", Method: http.MethodPost,
		Path: "/plugins/{name}/settings/run", Summary: "Run a button on the instance form", Description: "Such as Test connection, against the unsaved draft.", Tags: tags},
		func(ctx context.Context, in *runSettingIn) (*runSettingOut, error) {
			if err := s.loginGuard(ctx, in.Name, false); err != nil {
				return nil, err
			}
			res, err := s.Registry.RunSetting(ctx, s.Plugins, in.Name, in.Body.Action, in.Body.draft())
			if err != nil {
				return nil, s.settingsFail(err)
			}
			return &runSettingOut{Body: res}, nil
		})
}

// settingsFail is fail, except a plugin's own error message reaches the
// owner: only the destructive tier can make these calls.
func (s *Server) settingsFail(err error) error {
	var rpcErr *protocol.Error
	switch {
	case errors.Is(err, plugins.ErrIncapable):
		return huma.Error400BadRequest(err.Error())
	case errors.As(err, &rpcErr) && rpcErr.Code != protocol.CodeMethodNotFound:
		return huma.Error502BadGateway(rpcErr.Message)
	}
	return s.fail(err)
}
```

`settingsFail` also serves defaults, which needs only the read tier. The text a plugin puts in a `settings.defaults` error is its own and holds no draft. A plugin that leaks paths there is the same plugin that shows the defaults.

In `internal/api/server.go`, after `s.instanceRoutes(api)`:

```go
	s.pluginSettingsRoutes(api)
```

- [ ] **Step 4: Run the tests and check they pass**

Run: `go test ./internal/api -run 'PluginSettings|InstanceRoutes|InstanceCheck'`
Expected: PASS

- [ ] **Step 5: Regenerate the API schema**

Run: `cd web && npm run gen`
Expected: `web/openapi.json` and `web/src/api/schema.d.ts` change, and include `/plugins/{name}/settings/defaults`, `settings_actions` and `default_bases`.

- [ ] **Step 6: Commit**

```bash
git add internal/api/plugin_settings.go internal/api/plugin_settings_test.go internal/api/instances.go internal/api/server.go web/openapi.json web/src/api/schema.d.ts
git commit -m "feat(api): serve plugin settings defaults, options, and buttons"
```

---

### Task 7: SchemaForm: defaults, reset, the updated mark, and new controls

**Files:**
- Modify: `web/src/settings/SchemaForm.tsx`
- Modify: `web/src/settings/Settings.css`
- Test: `web/src/settings/SchemaForm.test.tsx`

`FormContext` carries what a field needs from its form: the defaults, the updated pointers, a loader for choices, and a list of instances by service. Its default value is empty, so the other users of `SchemaForm` (`Records.tsx`, `SetUpPlugins.tsx`, `forge/Inputs.tsx`) behave as before.

- [ ] **Step 1: Write the failing tests**

Append to `web/src/settings/SchemaForm.test.tsx`:

```tsx
import { FormContext, type FormHelpers } from "./SchemaForm";

function WithHelpers({ schema, initial, helpers }: { schema: Schema; initial?: unknown; helpers: Partial<FormHelpers> }) {
  return (
    <FormContext.Provider value={{ defaults: {}, updated: new Set(), ...helpers }}>
      <Harness schema={schema} initial={initial} />
    </FormContext.Provider>
  );
}

const prompts: Schema = {
  type: "object",
  properties: { prompts: { type: "object", properties: { greet: { type: "string", title: "Greet", contentMediaType: "text/plain" } } } },
};

test("a field shows its default, and typing it back drops the override", async () => {
  const user = userEvent.setup();
  render(<WithHelpers schema={prompts} initial={{}} helpers={{ defaults: { "/prompts/greet": "Hello" } }} />);
  const box = screen.getByLabelText("Greet");
  expect(box).toHaveValue("Hello");
  expect(screen.queryByRole("button", { name: "Reset Greet" })).toBeNull();
  await user.type(box, "!");
  expect(value()).toEqual({ prompts: { greet: "Hello!" } });
  await user.type(box, "{Backspace}");
  expect(value()).toBeNull();
});

test("reset clears an override and the updated mark shows on it", async () => {
  const user = userEvent.setup();
  render(
    <WithHelpers
      schema={prompts}
      initial={{ prompts: { greet: "Yo" } }}
      helpers={{ defaults: { "/prompts/greet": "Hello" }, updated: new Set(["/prompts/greet"]) }}
    />,
  );
  expect(screen.getByText("Default updated")).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "Reset Greet" }));
  expect(value()).toBeNull();
  expect(screen.getByLabelText("Greet")).toHaveValue("Hello");
  expect(screen.queryByText("Default updated")).toBeNull();
});

test("an array of string enum is a checkbox group", async () => {
  const user = userEvent.setup();
  const kinds: Schema = { type: "object", properties: { kinds: { type: "array", title: "Kinds", items: { type: "string", enum: ["chat", "embed"] } } } };
  render(<Harness schema={kinds} initial={{}} />);
  await user.click(screen.getByLabelText("embed"));
  await user.click(screen.getByLabelText("chat"));
  expect(value()).toEqual({ kinds: ["chat", "embed"] });
  await user.click(screen.getByLabelText("chat"));
  await user.click(screen.getByLabelText("embed"));
  expect(value()).toBeNull();
});

test("x-options loads choices into a datalist and still takes free text", async () => {
  const user = userEvent.setup();
  const loadOptions = vi.fn(async () => [{ value: "m1", label: "Model one" }]);
  const model: Schema = { type: "object", properties: { model: { type: "string", title: "Model", "x-options": true } } };
  const { container } = render(<WithHelpers schema={model} initial={{}} helpers={{ loadOptions }} />);
  await user.click(screen.getByRole("button", { name: "Load choices for Model" }));
  expect(loadOptions).toHaveBeenCalledWith("/model");
  expect(container.querySelector("datalist option")).toHaveAttribute("value", "m1");
  await user.type(screen.getByLabelText("Model"), "custom");
  expect(value()).toEqual({ model: "custom" });
});

test("format instance picks from the instances offering the service", async () => {
  const user = userEvent.setup();
  const instances = vi.fn(() => [{ id: 7, name: "Local LLM" }]);
  const conn: Schema = { type: "object", properties: { connection: { type: "integer", title: "Connection", format: "instance", "x-offers": "ai.chat" } } };
  render(<WithHelpers schema={conn} initial={{}} helpers={{ instances }} />);
  expect(instances).toHaveBeenCalledWith("ai.chat");
  await user.selectOptions(screen.getByLabelText("Connection"), "Local LLM");
  expect(value()).toEqual({ connection: 7 });
});
```

- [ ] **Step 2: Run the tests and check they fail**

Run: `cd web && npx vitest run src/settings/SchemaForm.test.tsx`
Expected: FAIL, because `FormContext` is not exported.

- [ ] **Step 3: Write the code**

Replace the top of `web/src/settings/SchemaForm.tsx`, from the imports through `fits`, with:

```tsx
import { createContext, useContext, useState, type ReactNode } from "react";

/** Schema is the part of JSON Schema the form renders. */
export type Schema = {
  type?: string;
  title?: string;
  description?: string;
  enum?: unknown[];
  default?: unknown;
  /** Any media type marks a string as long text, edited in a multi-line box. */
  contentMediaType?: string;
  /** "instance" with x-offers makes an integer a picker of instances offering that service. */
  format?: string;
  "x-offers"?: string;
  /** The plugin lists this string's choices with settings.options. */
  "x-options"?: boolean;
  items?: Schema;
  properties?: Record<string, Schema>;
  required?: string[];
};

/** FormHelpers is what fields need from the form around them; every part is optional. */
export type FormHelpers = {
  /** Defaults by JSON pointer: the schema's and the plugin's. */
  defaults: Record<string, unknown>;
  /** Pointers whose override replaced a default that has since changed. */
  updated: Set<string>;
  loadOptions?: (pointer: string) => Promise<{ value: string; label?: string }[]>;
  instances?: (service: string) => { id: number; name: string }[];
};

export const FormContext = createContext<FormHelpers>({ defaults: {}, updated: new Set() });

type Props = {
  schema: Schema;
  /** label is the property name, used when the schema has no title. */
  label: string;
  value: unknown;
  onChange: (v: unknown) => void;
  required?: boolean;
  /** path is the field's JSON pointer, "" for the root. */
  path?: string;
};

const fits: Record<string, (v: unknown) => boolean> = {
  string: (v) => typeof v === "string",
  number: (v) => typeof v === "number",
  integer: (v) => Number.isInteger(v),
  boolean: (v) => typeof v === "boolean",
  object: (v) => typeof v === "object" && v !== null && !Array.isArray(v),
  array: Array.isArray,
};

/** pointerKey escapes a property name for a JSON pointer. */
const pointerKey = (k: string) => k.replaceAll("~", "~0").replaceAll("/", "~1");

/** same compares JSON values; config values have no cycles or functions. */
const same = (a: unknown, b: unknown) => JSON.stringify(a) === JSON.stringify(b);
```

Change the signature, and pass `path` to children in the object branch:

```tsx
export function SchemaForm({ schema, label, value, onChange, required, path = "" }: Props) {
  const helpers = useContext(FormContext);
  // Above the object branch's early return, so every render calls the same hooks.
  const [options, setOptions] = useState<{ value: string; label?: string }[]>([]);
  const [optionsError, setOptionsError] = useState<string>();
```

In the object branch's child `<SchemaForm ... />`, add `path={`${path}/${pointerKey(k)}`}`.

Replace everything from `let control: ReactNode;` to the end of the function with:

```tsx
  const def = helpers.defaults[path];
  const hasDefault = def !== undefined;
  const overridden = hasDefault && value !== undefined;
  const shown = value ?? def;
  // Typing the default back is no override.
  const set = (v: unknown) => onChange(hasDefault && v !== undefined && same(v, def) ? undefined : v);
  const listId = `options-${path}`;

  let control: ReactNode;
  if (known && t === "integer" && schema.format === "instance" && schema["x-offers"] && helpers.instances) {
    control = (
      <select className="input" required={required} value={(shown as number) ?? ""} onChange={(e) => set(e.target.value ? Number(e.target.value) : undefined)}>
        <option value="">—</option>
        {helpers.instances(schema["x-offers"]).map((i) => (
          <option key={i.id} value={i.id}>
            {i.name}
          </option>
        ))}
      </select>
    );
  } else if (known && t === "array" && schema.items?.type === "string" && schema.items.enum?.every((e) => typeof e === "string")) {
    const picked = (shown as string[] | undefined) ?? [];
    return (
      <fieldset className="form-field checks">
        <legend>{title}</legend>
        {(schema.items.enum as string[]).map((e) => (
          <label key={e} className="check">
            <input
              type="checkbox"
              checked={picked.includes(e)}
              onChange={(ev) => {
                const next = (schema.items!.enum as string[]).filter((x) => (x === e ? ev.target.checked : picked.includes(x)));
                set(next.length > 0 ? next : undefined);
              }}
            />
            <span>{e}</span>
          </label>
        ))}
        {schema.description && <small className="muted">{schema.description}</small>}
      </fieldset>
    );
  } else if (known && t === "string" && schema.enum?.every((e) => typeof e === "string")) {
    control = (
      <select className="input" required={required} value={(shown as string) ?? ""} onChange={(e) => set(e.target.value || undefined)}>
        <option value="">—</option>
        {(schema.enum as string[]).map((e) => (
          <option key={e}>{e}</option>
        ))}
      </select>
    );
  } else if (known && t === "string" && schema.contentMediaType) {
    control = <textarea className="input" rows={8} required={required} value={(shown as string) ?? ""} onChange={(e) => set(e.target.value || undefined)} />;
  } else if (known && t === "string") {
    control = (
      <input
        className="input"
        required={required}
        list={schema["x-options"] ? listId : undefined}
        value={(shown as string) ?? ""}
        onChange={(e) => set(e.target.value || undefined)}
      />
    );
  } else if (known && (t === "number" || t === "integer")) {
    control = (
      <input
        className="input"
        type="number"
        step={t === "integer" ? 1 : "any"}
        required={required}
        value={(shown as number) ?? ""}
        onChange={(e) => set(e.target.value === "" ? undefined : Number(e.target.value))}
      />
    );
  } else if (known && t === "boolean") {
    control = <input type="checkbox" checked={shown === true} onChange={(e) => set(e.target.checked)} />;
  } else {
    control = <RawJson value={value} onChange={onChange} required={required} />;
  }
  return (
    <div className="form-field">
      <label className={t === "boolean" && known ? "check" : undefined}>
        <span>{title}</span>
        {control}
      </label>
      {schema["x-options"] && helpers.loadOptions && (
        <>
          <datalist id={listId}>
            {options.map((o) => (
              <option key={o.value} value={o.value} label={o.label} />
            ))}
          </datalist>
          <button
            type="button"
            className="button"
            aria-label={`Load choices for ${title}`}
            onClick={() =>
              helpers.loadOptions!(path).then(
                (o) => (setOptions(o), setOptionsError(undefined)),
                (e: Error) => setOptionsError(e.message),
              )
            }
          >
            Load choices
          </button>
          {optionsError && <small className="error">{optionsError}</small>}
        </>
      )}
      {overridden && (
        <button type="button" className="button reset" aria-label={`Reset ${title}`} title="Back to the default" onClick={() => onChange(undefined)}>
          Reset
        </button>
      )}
      {overridden && helpers.updated.has(path) && <small className="muted">Default updated</small>}
      {schema.description && <small className="muted">{schema.description}</small>}
    </div>
  );
}
```

In `web/src/settings/Settings.css`, add:

```css
.checks {
  border: none;
  padding: 0;
}

.button.reset {
  margin-top: calc(var(--space) / 2);
}
```

- [ ] **Step 4: Run the tests and check they pass**

Run: `cd web && npx vitest run src/settings/SchemaForm.test.tsx && npm run typecheck`
Expected: PASS, including the four existing tests.

- [ ] **Step 5: Commit**

```bash
git add web/src/settings/SchemaForm.tsx web/src/settings/SchemaForm.test.tsx web/src/settings/Settings.css
git commit -m "feat(web): show config defaults with reset, checkbox groups, choices, and an instance picker"
```

---

### Task 8: InstanceForm: tabs, defaults, bases on save, settings buttons

**Files:**
- Modify: `web/src/settings/Instance.tsx`
- Modify: `web/src/settings/Settings.css`
- Test: `web/src/settings/Instance.test.tsx`

- [ ] **Step 1: Write the failing tests**

In `web/src/settings/Instance.test.tsx`, add a plugin and an instance next to the others:

```tsx
const conf = {
  name: "conf", version: "0.1.0", capabilities: ["settings"], secrets: ["api_key"],
  settings_actions: { test: "Test connection" },
  config: {
    type: "object",
    properties: {
      url: { type: "string", title: "URL" },
      prompts: { type: "object", title: "Prompts", properties: { greet: { type: "string", title: "Greet", contentMediaType: "text/plain" } } },
    },
  },
};
const confInst = { ...broken, id: 6, plugin: "conf", name: "c", config: { url: "http://x" }, secrets: ["api_key"], broken_error: undefined };
```

In the `fetch` stub, return `[login, st, ai, conf]` for `/api/plugins` and answer the new paths. Put these lines before the generic `const body = await req.json();`:

```tsx
      if (req.method === "GET" && u.pathname === "/api/instances/6") return json(confInst);
      if (u.pathname === "/api/plugins/conf/settings/defaults") return json({ defaults: { "/prompts/greet": "Hello" }, updated: [] });
      if (u.pathname.endsWith("/settings/defaults")) return json({ defaults: {}, updated: [] });
      if (u.pathname === "/api/plugins/conf/settings/run") {
        const body = await req.json();
        sent.push({ method: req.method, path: u.pathname, body });
        return json({ ok: false, message: "The key was refused.", checks: [{ name: "chat", ok: false, latency_ms: 12 }] });
      }
```

Append the tests:

```tsx
test("an object in the config gets its own tab, and the default shows there", async () => {
  const user = userEvent.setup();
  renderAt("/settings/instances/6");
  expect(await screen.findByRole("tab", { name: "General" })).toHaveAttribute("aria-selected", "true");
  expect(screen.getByLabelText("URL")).toBeVisible();
  await user.click(screen.getByRole("tab", { name: "Prompts" }));
  expect(screen.getByLabelText("Greet")).toHaveValue("Hello");
  expect(screen.getByLabelText("URL")).not.toBeVisible();
});

test("saving an override sends the default it replaced", async () => {
  const user = userEvent.setup();
  renderAt("/settings/instances/6");
  await user.click(await screen.findByRole("tab", { name: "Prompts" }));
  await user.type(screen.getByLabelText("Greet"), "!");
  await user.click(screen.getByRole("button", { name: "Save" }));
  const patch = sent.find((s) => s.method === "PATCH");
  expect(patch?.body).toMatchObject({
    config: { url: "http://x", prompts: { greet: "Hello!" } },
    default_bases: { "/prompts/greet": "Hello" },
  });
});

test("a settings button runs against the draft and shows each check", async () => {
  const user = userEvent.setup();
  renderAt("/settings/instances/6");
  await user.type(await screen.findByLabelText("api_key"), "nope");
  await user.click(screen.getByRole("button", { name: "Test connection" }));
  expect(await screen.findByText("The key was refused.")).toBeInTheDocument();
  expect(screen.getByText(/chat/)).toHaveTextContent("12 ms");
  const run = sent.find((s) => s.path === "/api/plugins/conf/settings/run");
  expect(run?.body).toEqual({ action: "test", instance: 6, config: { url: "http://x" }, secrets: { api_key: "nope" } });
});
```

- [ ] **Step 2: Run the tests and check they fail**

Run: `cd web && npx vitest run src/settings/Instance.test.tsx`
Expected: FAIL, with no tab named "General".

- [ ] **Step 3: Write the code**

In `web/src/settings/Instance.tsx`, change the import:

```tsx
import { FormContext, SchemaForm, type Schema } from "./SchemaForm";
```

Add below `canon`:

```tsx
/** at returns the value at a JSON pointer into v. */
function at(v: unknown, pointer: string): unknown {
  return pointer
    .split("/")
    .slice(1)
    .map((k) => k.replaceAll("~1", "/").replaceAll("~0", "~"))
    .reduce<unknown>((o, k) => (o && typeof o === "object" ? (o as Record<string, unknown>)[k] : undefined), v);
}

/** tabsOf splits a config schema: each top-level object is a tab, and the rest go on General. */
function tabsOf(schema: Schema | undefined): { general: Schema; tabs: [string, Schema][] } {
  const props = Object.entries(schema?.properties ?? {});
  const tabs = props.filter(([, s]) => s.type === "object" && s.properties);
  const general = { ...schema, properties: Object.fromEntries(props.filter(([, s]) => !(s.type === "object" && s.properties))) };
  return { general, tabs };
}
```

Inside `InstanceForm`, after the `libraryAccess` state, add:

```tsx
  const schema = plugin.config as Schema | undefined;
  const { general, tabs } = tabsOf(schema);
  const [tab, setTab] = useState("general");
  const defaults = useQuery({
    queryKey: ["plugin-defaults", plugin.name, instance?.id],
    queryFn: async () =>
      must(
        await api.GET("/plugins/{name}/settings/defaults", {
          params: { path: { name: plugin.name }, query: instance ? { instance: instance.id } : {} },
        }),
      ),
  });
  const defs = defaults.data?.defaults ?? {};
  const draft = () => ({ instance: instance?.id, config: (config ?? {}) as Record<string, unknown>, secrets });
  const offering = useQueries({
    queries: services(schema).map((offers) => ({
      queryKey: ["instances", { offers }],
      queryFn: async () => ({ offers, list: must(await api.GET("/instances", { params: { query: { offers } } })) }),
    })),
  });
  const helpers = {
    defaults: defs,
    updated: new Set(defaults.data?.updated ?? []),
    loadOptions: async (field: string) => {
      const res = await api.POST("/plugins/{name}/settings/options", { params: { path: { name: plugin.name } }, body: { ...draft(), field } });
      return must(res).options ?? [];
    },
    instances: (service: string) => offering.find((q) => q.data?.offers === service)?.data?.list ?? [],
  };
  const run = useMutation({
    mutationFn: async (action: string) =>
      must(await api.POST("/plugins/{name}/settings/run", { params: { path: { name: plugin.name } }, body: { ...draft(), action } })),
    meta: { toast: false },
  });
```

Add `useQueries` to the `@tanstack/react-query` import. Add this helper at module level, next to `tabsOf`:

```tsx
/** services lists the x-offers of the schema's instance pickers, so each list is fetched once. */
function services(schema: Schema | undefined): string[] {
  const out = new Set<string>();
  const walk = (s: Schema) => {
    if (s.format === "instance" && s["x-offers"]) out.add(s["x-offers"]);
    Object.values(s.properties ?? {}).forEach(walk);
  };
  if (schema) walk(schema);
  return [...out];
}
```

In the save mutation, compute the bases before the POST or PATCH:

```tsx
      // For each override this edit set, the default it replaced, so the form can say when that default changes.
      const startedFrom = JSON.parse(initial) as unknown;
      const bases = Object.fromEntries(
        Object.entries(defs).filter(([p]) => at(cfg, p) !== undefined && canon(at(cfg, p)) !== canon(at(startedFrom, p))),
      );
      const default_bases = Object.keys(bases).length > 0 ? bases : undefined;
```

Add `default_bases` to the POST body. In the PATCH body, send it only with the config: `default_bases: canon(cfg) !== initial ? default_bases : undefined`.

Replace the single `<SchemaForm schema={plugin.config ...} />` line, the secrets map, the auth owner field and the library-access field with a tabbed block. The fields stay as they are; they're wrapped:

```tsx
      <FormContext.Provider value={helpers}>
        {tabs.length > 0 && (
          <div role="tablist" aria-label="Settings" className="tabs instance-tabs">
            {[["general", "General"] as const, ...tabs.map(([k, s]) => [k, s.title ?? k] as const)].map(([k, label]) => (
              <button key={k} type="button" role="tab" id={`tab-${k}`} aria-selected={tab === k} aria-controls={`panel-${k}`} className={tab === k ? "active" : undefined} onClick={() => setTab(k)}>
                {label}
              </button>
            ))}
          </div>
        )}
        <div role="tabpanel" id="panel-general" aria-labelledby="tab-general" hidden={tab !== "general"}>
          {schema && <SchemaForm schema={general} label="" value={config} onChange={setConfig} />}
          {/* the existing secrets map, owner subject field, and library access field, unchanged */}
          {Object.keys(plugin.settings_actions ?? {}).length > 0 && (
            <div className="actions">
              {Object.entries(plugin.settings_actions ?? {}).map(([id, label]) => (
                <button key={id} type="button" className="button" disabled={run.isPending} onClick={() => run.mutate(id)}>
                  {label}
                </button>
              ))}
            </div>
          )}
          {run.data && (
            <div className={run.data.ok ? undefined : "error"}>
              {run.data.message && <p>{run.data.message}</p>}
              <ul>
                {(run.data.checks ?? []).map((c) => (
                  <li key={c.name}>
                    {c.ok ? "✓" : "✗"} {c.name}
                    {c.latency_ms ? ` — ${c.latency_ms} ms` : ""}
                    {c.message ? `: ${c.message}` : ""}
                  </li>
                ))}
              </ul>
            </div>
          )}
          {run.error && <p className="error">{run.error.message}</p>}
        </div>
        {tabs.map(([k, s]) => (
          <div key={k} role="tabpanel" id={`panel-${k}`} aria-labelledby={`tab-${k}`} hidden={tab !== k}>
            <SchemaForm
              schema={{ ...s, title: "" }}
              label=""
              path={`/${k.replaceAll("~", "~0").replaceAll("/", "~1")}`}
              value={(config as Record<string, unknown> | undefined)?.[k]}
              onChange={(v) =>
                setConfig((c: unknown) => {
                  const { [k]: _, ...rest } = (c ?? {}) as Record<string, unknown>;
                  return v === undefined ? rest : { ...rest, [k]: v };
                })
              }
            />
          </div>
        ))}
      </FormContext.Provider>
```

Two more changes:
- General's `SchemaForm` changes only the general keys. Pass it an `onChange` that keeps the tab keys: `onChange={(v) => setConfig((c: unknown) => ({ ...Object.fromEntries(tabs.map(([k]) => [k, (c as Record<string, unknown> | undefined)?.[k]]).filter(([, x]) => x !== undefined)), ...((v ?? {}) as object) }))}`.
- A required field on a hidden tab blocks the save without showing why. Add `onInvalidCapture` to the `<form>`, so the browser's invalid event opens that field's tab:

```tsx
      onInvalidCapture={(e) => {
        const panel = (e.target as HTMLElement).closest('[role="tabpanel"]');
        if (panel) setTab(panel.id.replace("panel-", ""));
      }}
```

In `web/src/settings/Settings.css`:

```css
.instance-tabs button {
  background: none;
  border: none;
  padding: var(--space) 0;
  color: var(--text-muted);
  cursor: pointer;
}

.instance-tabs button.active {
  color: var(--text);
  border-bottom: 2px solid var(--accent);
}
```

The Forge has the only other tablist (`web/src/forge/Forge.tsx:124`). This is the second copy, so it stays inline. Extract a `Tabs` component on the third.

- [ ] **Step 4: Run the tests and check they pass**

Run: `cd web && npx vitest run src/settings && npm run typecheck`
Expected: PASS. The existing Instance tests still pass, because a plugin with no object properties shows no tablist.

- [ ] **Step 5: Commit**

```bash
git add web/src/settings/Instance.tsx web/src/settings/Instance.test.tsx web/src/settings/Settings.css
git commit -m "feat(web): tab the instance form and run its settings buttons"
```

---

### Task 9: Run what CI runs

- [ ] **Step 1: Run the checks**

```bash
go vet ./...
go test -race ./...
cd web && npm run typecheck && npm test
```

Expected: everything passes. The database tests run only with `INNKEEPER_TEST_DATABASE_URL` set; without it they skip, and that doesn't count as passing.

- [ ] **Step 2: Read the diff**

Run: `git diff origin/main --stat` and read the whole diff. Look for:
- names that differ from the contracts above;
- a `FormContext` field that no test uses;
- leftover narration comments.

- [ ] **Step 3: Commit any fixes**

```bash
git add -A
git commit -m "chore: tidy the settings form"
```
