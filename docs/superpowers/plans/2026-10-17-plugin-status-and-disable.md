# Plugin status words and disabling instances implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** One set of status words for plugins and instances, a single-line card for any plugin with zero or one instance, Needs setup for an instance missing required config, and Disable/Enable per instance.

**Architecture:** `plugin_instance` gains `disabled`. `InstanceInfo` gains `disabled` and a computed `status`, and a `Usable()` method that every list-based lookup uses instead of checking `BrokenError` itself. `Registry.Instance` refuses a disabled instance with `ErrDisabled`, so every ID-based lookup refuses it too. `UpdateInstance` sets `disabled` and refuses to disable the last usable login. The web page renders from `status`.

**Tech Stack:** Go, pgx, huma; React 19, TanStack Query, Vitest.

**Spec:** `docs/superpowers/specs/2026-10-17-plugin-status-and-disable-design.md`. Ticket: INN-68.

**Work in:** `/Users/dgibbons/git/innkeeper-plugin-status` (branch `plugin-status-disable`). Task 6 is in `/Users/dgibbons/git/innkeeper-plugins`. Never edit `/Users/dgibbons/git/innkeeper`.

**Database tests** need `INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres`; without it they skip.

---

### Task 1: The disabled column and instance status

**Files:**
- Create: `internal/db/migrations/00NN_instance_disabled.sql`. NN is the next free number: `ls internal/db/migrations | tail -1`, because another branch (INN-65) also adds migrations.
- Modify: `internal/plugins/plugins.go`: `InstanceInfo` (around line 239), `infoCols` and `scanInfo` (around line 470).
- Test: `internal/plugins/plugins_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestInstanceStatus(t *testing.T) {
	ctx := context.Background()
	box, err := secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	pool := testdb.New(t)
	reg := New(pool, map[string]*protocol.Manifest{
		"conf": {Name: "conf", Version: "1", Capabilities: []string{"import"}, MultipleInstances: true,
			Config: map[string]any{"type": "object", "required": []any{"url"}}},
	}, &blobstore.Store{Name: "library", Root: t.TempDir()}, box)
	create := func(name string, config map[string]any) int64 {
		id, err := reg.CreateInstance(ctx, NewInstance{Plugin: "conf", Name: name, Config: config})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	ready := create("ready", map[string]any{"url": "https://x"})
	missing := create("missing", nil)
	broken := create("broken", map[string]any{"url": "https://x"})
	if _, err := pool.Exec(ctx, `UPDATE plugin_instance SET broken_error = 'boom', broken_at = now() WHERE id = $1`, broken); err != nil {
		t.Fatal(err)
	}
	off := create("off", map[string]any{"url": "https://x"})
	if _, err := pool.Exec(ctx, `UPDATE plugin_instance SET disabled = true, broken_error = 'boom' WHERE id = $1`, off); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[int64]string{ready: "ready", missing: "needs_setup", broken: "broken", off: "disabled"} {
		got, err := reg.GetInstance(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != want {
			t.Errorf("instance %s: status %q, want %q", got.Name, got.Status, want)
		}
	}
}
```

- [ ] **Step 2: Run it and check that it fails**

Run: `go test ./internal/plugins/ -run TestInstanceStatus`
Expected: FAIL to compile with `got.Status undefined`.

- [ ] **Step 3: Implement**

The migration file:

```sql
-- A disabled instance keeps its settings, but nothing uses it.
ALTER TABLE plugin_instance ADD COLUMN disabled boolean NOT NULL DEFAULT false;
```

In `InstanceInfo`, after `BrokenAt`:

```go
	Disabled bool `json:"disabled"`
	// Status is disabled, broken, needs_setup, or ready, in that order.
	Status string `json:"status" enum:"ready,needs_setup,broken,disabled"`
```

Add after the struct:

```go
// Usable reports whether anything may use the instance.
func (i InstanceInfo) Usable() bool { return i.BrokenError == nil && !i.Disabled }
```

Add `disabled` to the end of `infoCols`, scan it into `&i.Disabled` (after `&i.UpdatedAt`, in the same position), and at the end of `scanInfo`, before `return i, nil`, set:

```go
	i.Status = r.instanceStatus(i)
```

with:

```go
func (r *Registry) instanceStatus(i InstanceInfo) string {
	switch {
	case i.Disabled:
		return "disabled"
	case i.BrokenError != nil:
		return "broken"
	}
	m, ok := r.manifest(i.Plugin)
	if !ok {
		return "ready"
	}
	required, _ := m.Config["required"].([]any)
	for _, k := range required {
		if key, _ := k.(string); key != "" && i.Config[key] == nil {
			return "needs_setup"
		}
	}
	return "ready"
}
```

`EnsureSingletons` reads `m.Config["required"]` the same way. If a third copy of that line appears, extract `requiredConfig(m) []string`; with two, leave it.

- [ ] **Step 4: Run it and check that it passes**

Run: `go test ./internal/plugins/ -run TestInstanceStatus -v`
Expected: PASS, not SKIP.

- [ ] **Step 5: Commit**

```bash
git add internal/db/migrations internal/plugins/plugins.go internal/plugins/plugins_test.go
git commit -m "feat(plugins): give each instance a status and a disabled flag"
```

---

### Task 2: Plugin status from instance status

**Files:**
- Modify: `internal/plugins/plugins.go` (`Status`, around line 254)
- Test: `internal/plugins/plugins_test.go` (`TestPluginStatus`)
- Modify: `internal/api/instances.go` (`pluginOut.Status` enum and doc)

- [ ] **Step 1: Rewrite the test table**

Replace the `tests` table and fixtures in `TestPluginStatus` with:

```go
	ok := InstanceInfo{ID: 1, Status: "ready"}
	bad := InstanceInfo{ID: 2, Status: "broken"}
	unset := InstanceInfo{ID: 3, Status: "needs_setup"}
	off := InstanceInfo{ID: 4, Status: "disabled"}
	solo := &protocol.Manifest{Name: "solo", Capabilities: []string{"import"}}
	many := &protocol.Manifest{Name: "many", Capabilities: []string{"push"}, MultipleInstances: true}
	theme := &protocol.Manifest{Name: "dusk", Capabilities: []string{"theme"}}
	tests := []struct {
		name      string
		m         *protocol.Manifest
		instances []InstanceInfo
		want      string
	}{
		{"theme", theme, nil, "ready"},
		{"singleton with no instance", solo, nil, "needs_setup"},
		{"multi with no instance", many, nil, "needs_setup"},
		{"one ready", solo, []InstanceInfo{ok}, "ready"},
		{"broken wins", many, []InstanceInfo{ok, unset, bad}, "broken"},
		{"needs setup beats ready", many, []InstanceInfo{ok, unset}, "needs_setup"},
		{"all disabled", many, []InstanceInfo{off, off}, "disabled"},
		{"one disabled, one ready", many, []InstanceInfo{off, ok}, "ready"},
	}
```

Keep the loop that calls `Status(tt.m, tt.instances)`.

- [ ] **Step 2: Run it and check that it fails**

Run: `go test ./internal/plugins/ -run TestPluginStatus`
Expected: FAIL on "multi with no instance" (got `instances`) and the cases that follow it.

- [ ] **Step 3: Implement**

```go
// Status sums up a plugin's instances for the Plugins page, in the same words
// as an instance's status: a broken or unset instance shows first, and a
// plugin is disabled only when every instance is.
func Status(m *protocol.Manifest, instances []InstanceInfo) string {
	has := func(s string) bool {
		return slices.ContainsFunc(instances, func(i InstanceInfo) bool { return i.Status == s })
	}
	switch {
	case m.IsTheme():
		return "ready"
	case len(instances) == 0:
		return "needs_setup"
	case has("broken"):
		return "broken"
	case has("needs_setup"):
		return "needs_setup"
	case !has("ready"):
		return "disabled"
	}
	return "ready"
}
```

In `internal/api/instances.go`, change `pluginOut.Status`'s tags to:

```go
	Status            string `json:"status" enum:"ready,needs_setup,broken,disabled" doc:"Sums up the plugin's instances: broken or needs_setup if any instance is, disabled if every one is, needs_setup with none. A theme is ready."`
```

- [ ] **Step 4: Run them and check that they pass**

Run: `go test ./internal/plugins/ ./internal/api/ -run 'TestPluginStatus|TestPluginStore'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/plugins/plugins.go internal/plugins/plugins_test.go internal/api/instances.go
git commit -m "feat(plugins): sum up a plugin's status from its instances"
```

---

### Task 3: Nothing uses a disabled instance

**Files:**
- Modify: `internal/plugins/plugins.go`: errors block, `Instance`, `load`, `CodecInstance` (SQL around line 575), `InstancesOffering` (around line 689)
- Modify: `internal/plugins/sites.go:38-43`
- Modify: `internal/api/listcache.go:92`, `internal/api/oauth.go:376`, `internal/api/auth.go:129`, `internal/api/server.go:262`
- Modify: `internal/services/services.go:69`
- Modify: `internal/targets/sources.go:30` and `:200` (SQL), `internal/targets/sync.go:166`, `internal/targets/check.go:164` (SQL)
- Test: `internal/plugins/plugins_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestDisabledInstanceIsSkipped(t *testing.T) {
	ctx := context.Background()
	box, err := secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	pool := testdb.New(t)
	reg := New(pool, map[string]*protocol.Manifest{
		"codec": {Name: "codec", Version: "1", Capabilities: []string{"codec"}, Formats: []string{"png"}},
	}, &blobstore.Store{Name: "library", Root: t.TempDir()}, box)
	id, err := reg.CreateInstance(ctx, NewInstance{Plugin: "codec", Name: "codec"})
	if err != nil {
		t.Fatal(err)
	}
	disabled := true
	if err := reg.UpdateInstance(ctx, id, InstanceEdit{Disabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Instance(ctx, id); !errors.Is(err, ErrDisabled) {
		t.Fatalf("Instance: err = %v, want ErrDisabled", err)
	}
	if _, err := reg.CodecInstance(ctx, "png"); !errors.Is(err, ErrNoCodec) {
		t.Fatalf("CodecInstance: err = %v, want ErrNoCodec", err)
	}
	if _, err := reg.CreateInstance(ctx, NewInstance{Plugin: "codec", Name: "codec 2"}); !errors.Is(err, ErrSingleton) {
		t.Fatalf("second instance while disabled: err = %v, want ErrSingleton", err)
	}

	enabled := false
	if err := reg.UpdateInstance(ctx, id, InstanceEdit{Disabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.CodecInstance(ctx, "png"); err != nil {
		t.Fatalf("after Enable: CodecInstance err = %v", err)
	}
}
```

This also covers the spec's `TestDisabledSingletonBlocksSecondInstance`.

- [ ] **Step 2: Run it and check that it fails**

Run: `go test ./internal/plugins/ -run TestDisabledInstanceIsSkipped`
Expected: FAIL to compile with `unknown field Disabled in struct literal of type InstanceEdit` and `undefined: ErrDisabled`.

- [ ] **Step 3: Implement in the registry**

Errors block:

```go
	// ErrDisabled means the owner disabled the instance; nothing uses it until they enable it.
	ErrDisabled = errors.New("plugin instance is disabled")
```

`InstanceEdit` gains `Disabled *bool`. In `UpdateInstance`'s `UPDATE`, add `disabled = coalesce($9, disabled)` and pass `e.Disabled` as `$9`. Add `|| e.Disabled != nil` to the condition that deletes the instance's sessions. (Task 4 adds the last-login check.)

`load` selects `pi.disabled` too and returns it. Change its signature to return a small struct instead of a third bare value, because a fourth would be one too many:

```go
// state is what Instance refuses an instance for.
type state struct {
	broken   *string
	disabled bool
}
```

`load(ctx, id) (pluginhost.Instance, state, error)`. `Instance` becomes:

```go
func (r *Registry) Instance(ctx context.Context, id int64) (pluginhost.Instance, error) {
	inst, st, err := r.load(ctx, id)
	switch {
	case err != nil:
		return inst, err
	case st.disabled:
		return pluginhost.Instance{}, fmt.Errorf("%w: %d", ErrDisabled, id)
	case st.broken != nil:
		return pluginhost.Instance{}, fmt.Errorf("%w: %s", ErrBroken, *st.broken)
	}
	return inst, nil
}
```

Update every other caller of `load` (`grep -n "r.load(" internal/plugins/*.go`) to read `st.broken` where it read `broken`.

In `CodecInstance`'s query: `WHERE broken_error IS NULL AND NOT disabled`.

- [ ] **Step 4: Update every lookup**

Each of these chooses instances to use. Make each skip disabled ones the way it skips broken ones:

| Site | Change |
|---|---|
| `internal/plugins/plugins.go` `InstancesOffering` (around 689) | `if !info.Usable() { continue }`; skip `errors.Is(err, ErrBroken) \|\| errors.Is(err, ErrDisabled)` |
| `internal/plugins/sites.go:38` and `:43` | `if !i.Usable() \|\| seen[i.Plugin]`; skip `ErrDisabled` too |
| `internal/api/listcache.go:92` `eachWorking` | `if i.Usable()`; doc comment: "each usable instance" |
| `internal/api/oauth.go:376` `loginProviders` | `i.OwnerSubject != nil && i.Usable()` |
| `internal/api/auth.go:129` `authInstance` | `errors.Is(err, plugins.ErrBroken) \|\| errors.Is(err, plugins.ErrDisabled)` gives the same 503 |
| `internal/services/services.go:69` | add `errors.Is(err, plugins.ErrDisabled)` to the forbidden list |
| `internal/targets/sources.go:30` | `!in.Usable() \|\| m == nil \|\| m.Has("push")` |
| `internal/targets/sources.go:200` (SQL) | `AND i.broken_error IS NULL AND NOT i.disabled` |
| `internal/targets/sync.go:166` | `if in.Usable()` |
| `internal/targets/check.go:164` (SQL) | `WHERE i.broken_error IS NULL AND NOT i.disabled AND b.push` |
| `internal/api/server.go:262` | add a case before the `ErrBroken` case: `case errors.Is(err, plugins.ErrDisabled): return huma.Error409Conflict("plugin instance is disabled; enable it first")` |

Then make sure nothing was missed:

```bash
git grep -n "BrokenError == nil\|BrokenError != nil\|broken_error IS NULL" -- 'internal/**/*.go' | grep -v _test
```

Expected: only `instanceStatus`, `Usable`, the `cmd/ink` display code, and SQL lines that already have `NOT disabled`. Every other hit is a missed site; fix it the same way.

- [ ] **Step 5: Run the suite**

Run: `go test -race ./...`
Expected: PASS, including `TestDisabledInstanceIsSkipped`.

- [ ] **Step 6: Commit**

```bash
git add -u internal
git commit -m "feat(plugins): let the owner disable an instance so nothing uses it"
```

---

### Task 4: The API, MCP, and the last-login guard

**Files:**
- Modify: `internal/plugins/plugins.go` (`UpdateInstance`, errors block)
- Modify: `internal/api/instances.go` (`updateInstanceIn`, the `update-instance` handler)
- Modify: `internal/api/server.go` (409 list)
- Modify: `internal/mcp/tools.go` (`update_instance` and `instanceUpdateIn`)
- Test: `internal/api/instances_test.go`
- Generated: `web/openapi.json`, `web/src/api/schema.d.ts`

- [ ] **Step 1: Write the failing test**

Read `internal/api/instances_test.go` and `internal/api/setup_test.go` first, and follow how they build an env with a login plugin and call `e.call`. Then add `TestDisableRefusesLastLogin`:

1. Create one usable login instance (auth plugin, `owner_subject` set).
2. `PATCH /instances/{id}` with `{"disabled": true}` → 409.
3. Create a second usable login instance.
4. The same PATCH on the first → 200, and its `status` is `disabled`.
5. Re-enable with `{"disabled": false}` → 200, `status` is `ready`.

- [ ] **Step 2: Run it and check that it fails**

Run: `go test ./internal/api/ -run TestDisableRefusesLastLogin -v`
Expected: FAIL. `disabled` is ignored or rejected as an unknown field.

- [ ] **Step 3: Implement**

Errors block:

```go
	// ErrLastLogin means disabling the instance would leave no way to log in.
	ErrLastLogin = errors.New("this is the last working login provider; add another before disabling it")
```

In `UpdateInstance`, after the `SELECT ... FOR UPDATE`, when `e.Disabled != nil && *e.Disabled` and the plugin has `auth`:

```go
		var others int
		err := tx.QueryRow(ctx, `SELECT count(*) FROM plugin_instance
			WHERE id <> $1 AND plugin = ANY($2) AND owner_subject IS NOT NULL AND broken_error IS NULL AND NOT disabled`,
			id, r.pluginsWith("auth")).Scan(&others)
		if err != nil {
			return err
		}
		if others == 0 {
			return ErrLastLogin
		}
```

`pluginsWith(capability string) []string` returns the names of loaded plugins that declare `capability`, read under `r.mu`. If a helper like it already exists in the package, use that instead.

In `internal/api/instances.go`, `updateInstanceIn.Body` gains:

```go
		Disabled *bool `json:"disabled,omitempty" doc:"true keeps the instance but stops everything using it; false turns it back on. Disabling a login provider ends its sessions."`
```

Pass `Disabled: in.Body.Disabled` into `plugins.InstanceEdit`. The handler already runs `loginGuard`, so OAuth and MCP clients can't disable a login provider.

In `internal/api/server.go`, add `errors.Is(err, plugins.ErrLastLogin),` to the 409 case.

In `internal/mcp/tools.go`, `instanceUpdateIn` gains `Disabled *bool` (with a jsonschema description matching the API doc), and `update_instance` passes it in its body struct.

- [ ] **Step 4: Run, then regenerate the schema**

Run: `go test ./internal/api/ ./internal/mcp/ && cd web && npm run gen && git diff --stat`
Expected: PASS. Only `web/openapi.json` and `web/src/api/schema.d.ts` change: `InstanceInfo` gains `disabled` and `status`, and the update body gains `disabled`.

- [ ] **Step 5: Commit**

```bash
git add internal web/openapi.json web/src/api/schema.d.ts
git commit -m "feat(api): disable and enable instances, but never the last login"
```

---

### Task 5: The Plugins page

**Files:**
- Modify: `web/src/settings/Plugins.tsx` (`instanceRows` and `card`)
- Modify: `web/src/settings/Settings.css`
- Test: `web/src/settings/Plugins.test.tsx`; fix fixtures in `web/src/settings/Instance.test.tsx` if the typecheck asks for `status` or `disabled`

- [ ] **Step 1: Write the failing tests**

In `Plugins.test.tsx`:
- Give every `inst(...)` fixture `status` and `disabled`: broken ones `"broken"`, the rest `"ready"`, all with `disabled: false`. Give `sillytavern` a second instance in the fixtures if it doesn't already have two.
- Change plugin `status` values that were `"instances"` to what `plugins.Status` now returns.

Add tests:
- A plugin with `multiple_instances: true` and **one** instance renders as one line. The article has no `.instances` list. It shows `Ready`, a `Settings` link to `/settings/instances/<id>`, an `Add instance` link, and in `More for <plugin>` the buttons `Disable <instance>` and `Delete instance <instance>`.
- A plugin with two instances lists both rows, each with its own `More for <instance>` menu.
- `screen.queryByText("Working")` is null.
- Clicking `Disable <name>` sends `PATCH /api/instances/<id>`, and the request body is `{"disabled":true}`. Record bodies in the fetch stub next to `requests`. An instance with `status: "disabled"` shows `Disabled` and an `Enable <name>` button.
- `Delete instance <name>` calls `confirm`, then `DELETE /api/instances/<id>`.

Update the existing tests that click `Delete home` or `Delete work`: the label is now `Delete instance home`.

- [ ] **Step 2: Run them and check that they fail**

Run: `cd web && npx vitest run src/settings/Plugins.test.tsx`
Expected: the new tests FAIL.

- [ ] **Step 3: Implement**

In `Plugins.tsx`, add a mutation next to `remove`:

```tsx
  const setDisabled = useMutation({
    mutationFn: async (v: { id: number; disabled: boolean }) =>
      mustOk(await api.PATCH("/instances/{id}", { params: { path: { id: v.id } }, body: { disabled: v.disabled } })),
    onSuccess: changed,
  });
```

Add these at module level, below `byCategory`:

```tsx
const statusWords = {
  ready: ["Ready", "succeeded"],
  needs_setup: ["Needs setup", "setup"],
  broken: ["Broken", "failed"],
  disabled: ["Disabled", "disabled"],
} as const;

/** StatusText shows a plugin's or instance's status in the page's four words. */
function StatusText({ status, error }: { status: keyof typeof statusWords; error?: string | null }) {
  const [word, tone] = statusWords[status];
  return (
    <span className={`state ${tone}`}>
      {word}
      {status === "broken" && error && (
        <>
          : <span>{error}</span>
        </>
      )}
    </span>
  );
}
```

Replace `instanceRows` and the instance-specific parts of `card` with:

```tsx
  const instanceActions = (i: InstanceInfo) => (
    <>
      <button
        aria-label={`${i.disabled ? "Enable" : "Disable"} ${i.name}`}
        disabled={setDisabled.isPending}
        onClick={() => setDisabled.mutate({ id: i.id, disabled: !i.disabled })}
      >
        {i.disabled ? "Enable" : "Disable"}
      </button>
      <button
        aria-label={`Delete instance ${i.name}`}
        onClick={() => confirm(`Delete ${i.name}? Its jobs stay, and logins through it end.`) && remove.mutate(i.id)}
      >
        Delete instance
      </button>
    </>
  );

  const instanceRows = (list: InstanceInfo[]) => (
    <ul className="instances">
      {list.map((i) => (
        <li key={i.id}>
          <Link to={`/settings/instances/${i.id}`}>{i.name}</Link>
          <StatusText status={i.status} error={i.broken_error} />
          <Menu label={`More for ${i.name}`}>{instanceActions(i)}</Menu>
        </li>
      ))}
    </ul>
  );
```

In `card`:
- `const listed = list.length > 1;`
- `const only = list.length === 1 ? list[0] : undefined;`
- Drop `broken`.
- Replace the three status spans with `{!listed && <StatusText status={p.status} error={only?.broken_error} />}`.
- The action buttons, after the Update button:

```tsx
          {p.capabilities?.includes("theme") ? (
            <Link to="/settings/appearance">Select in Appearance</Link>
          ) : (
            <>
              {only && (
                <Link className="button" to={`/settings/instances/${only.id}`}>
                  Settings
                </Link>
              )}
              {!only && !listed && !p.multiple_instances && (
                <Link className="button primary" to={addURL(p)}>
                  Set up
                </Link>
              )}
              {p.multiple_instances && (
                <Link className={list.length === 0 ? "button primary" : "button"} to={addURL(p)}>
                  Add instance
                </Link>
              )}
            </>
          )}
```

- The plugin menu: render it when `canUninstall || only || (prevSource && prevVersion)`, with Roll back, then `{only && instanceActions(only)}`, then Uninstall.
- After the header: `{listed && instanceRows(list)}`. Drop the "No instances yet." line; Needs setup and Add instance say it.

In `Settings.css`, next to `.state.setup`:

```css
.state.disabled { color: var(--text-muted); }
```

- [ ] **Step 4: Run the tests and the typecheck**

Run: `cd web && npm run typecheck && npm test`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src
git commit -m "feat(web): one status vocabulary, one-line cards, and Disable on the Plugins page"
```

---

### Task 6: folder requires its folder

**Files (in `/Users/dgibbons/git/innkeeper-plugins`, on a new branch `folder-required` from `origin/main`):**
- Modify: `folder/plugin.toml`, `index.toml`, `folder/main.go` (the version constant)

- [ ] **Step 1: Branch**

```bash
cd /Users/dgibbons/git/innkeeper-plugins && git fetch -q origin && git switch -c folder-required origin/main
```

- [ ] **Step 2: Mark the folder required and bump the version**

In `folder/plugin.toml`, under `[config]` after `type = "object"`, add `required = ["folder"]`. Bump the patch version in `folder/plugin.toml`, in folder's `index.toml` entry, and in the `const version` line of `folder/main.go`. All three must match, because the core refuses a plugin whose binary reports a different version than its manifest.

- [ ] **Step 3: Check**

Run, with `INNKEEPER_TEST_DATABASE_URL` and `INNKEEPER_PLUGIN_DATABASE_URL` both set to the test database URL:

```bash
go run ./internal/indexcheck && go test ./folder/... && make check
```

Expected: all pass, and the `make check` output includes `PASS  catalog.ingest completes`. If the contract check needs config for folder, check whether `check-config.json` already sets `folder`. It should; if not, add it.

- [ ] **Step 4: Commit**

```bash
git commit -am "fix(folder): require the folder to index"
```

Don't push. The controller pushes after the core change ships.

---

### Task 7: Verify everything

- [ ] **Step 1: Run what CI runs**

```bash
cd /Users/dgibbons/git/innkeeper-plugin-status
go vet ./...
go test -race ./...
cd web && npm run typecheck && npm test
```

Expected: all pass, with the database tests run rather than skipped.

- [ ] **Step 2: Read the diff**

Run: `git diff origin/develop --stat && git diff origin/develop`

Check for:
- leftover `Working` text;
- leftover `"instances"` status values;
- `BrokenError` checks that don't use `Usable()`;
- comments that narrate the change.
