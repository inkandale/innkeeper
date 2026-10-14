# Source Updates (Core) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The core follows each card's and lorebook's site sources through pull-only bindings, checks them on a schedule, and offers each upstream change in the changes queue as a three-way merge.

**Architecture:** INN-51, spec `docs/superpowers/specs/2026-10-14-source-updates-design.md`. This plan is 51a, core Go only.
- 51b, the `url-import` plugin: [Contract for 51b and 51c](#contract-for-51b-and-51c).
- 51c, the web UI: same section.

Pieces:
- **Pull-only plugin:** declares `pull`, not `push`, and lists the source kinds it follows in a new manifest key, `source_kinds`.
- **Following sources:** a source-check job binds each live source of a followed kind to that plugin's instance, with `remote_id` `kind:ref`. The binding's accepted copy is the entity's first revision that a plugin imported. The job then checks every such binding.
- **Bindings:** `push = false` marks these bindings. They are never ahead, may share a `remote_id`, and compare the site's avatar by hash.
- **Changes queue:** they appear as `update`, `missing`, or `error` entries. Review, "ignore this change" (`Dismiss`), pause, and autosync reuse compare, resolve, and the existing binding flags.

**Tech Stack:** Go, pgx, PostgreSQL migrations, Huma, MCP Go SDK.

**Tests:** DB tests skip without the database URL, so a run without it proves nothing. Run every Go test with
`INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres` and `-timeout 15m`. Below, `$DB` means that assignment. No commit may be marked as a breaking change.

**Deviations from the spec**, already folded into the spec:
- Bindings are created by the source check, not by every source write. The check binds the sources it finds and unbinds the ones the owner removed, so the library never calls into targets. The queue hides a removed source's entry straight away.
- One baseline rule covers imports and existing cards: the first revision a plugin wrote, else the first revision. For an imported card, that is the imported content.
- `binding` also gets `check_error`, so an `error` entry can say what failed.
- "Ignore this change" is a two-way resolve that keeps the library side. A three-way one would still take the author's edits to fields the owner never touched.
- Autosync leaves an update for review when the avatar differs, since there's no ancestor avatar to tell whose change it is.
- Books embedded in a card aren't checked. Reconcile already leaves `character_book` out (`describeReconcile`), so a pull-only target must not list it in `fields`.

---

## File map

| File | Change |
|---|---|
| `protocol/manifest.go`, `protocol/manifest_test.go` | `source_kinds` key and its validation |
| `internal/db/migrations/0022_pull_only_bindings.sql` | New columns, partial unique indexes, `binding_status` never ahead for `NOT push` |
| `internal/wire/sync.go`, `internal/wire/changes.go` | `Binding` gets `push`, `paused`, `check_failures`, `check_error`. `ChangeUpdate` and `ChangeRow.LorebookID` |
| `internal/targets/targets.go` | `describe` accepts pull-only, `describePush`, binding query columns, `SetPaused`, `stored` columns |
| `internal/targets/push.go`, `preview.go`, `bind.go` | Call `describePush` |
| `internal/targets/sources.go` (new) | `followers`, `follow`, `CheckSources`, `CheckEntitySources`, `Dismiss`, `applyUpdate` |
| `internal/targets/check.go` | Followed bindings: avatar hash, failures, pause. `checkEach` shared with Scan, which skips followed bindings |
| `internal/targets/changes.go` | `update`, `missing`, `error` entries for followed bindings, including lorebook rows |
| `internal/targets/reconcile_compare.go`, `resolve.go` | `itemBinding` picks the entity's binding. `saveBinding` stores the avatar hash and `push` |
| `internal/targets/jobs.go`, `internal/targets/wire.go` | `SourceCheckKind`, `RunSourceCheck`, `ChangeUpdate` alias |
| `internal/targets/env_test.go`, `internal/targets/sources_test.go` (new) | Pull-only fake and the tests |
| `cmd/innkeeper/worker.go` | Handler and `INNKEEPER_SOURCE_CHECK_EVERY` schedule |
| `internal/api/targets.go`, `internal/api/reconcile.go`, tests | PATCH `paused`, dismiss, per-entity source check, `/source-checks`, `update` state |
| `internal/mcp/tools.go`, `internal/api/mcp_test.go` | `check_sources` tool, `update` in `list_changes` |
| `README.md`, `docs/protocol.md` | Env var, manifest key, pull-only targets |

---

### Task 1: The `source_kinds` manifest key

**Files:**
- Modify: `protocol/manifest.go:37-67` (struct), `:233` (end of `Validate`)
- Test: `protocol/manifest_test.go`

- [ ] **Step 1: Write the failing test**

Append to `protocol/manifest_test.go`:

```go
func TestManifestSourceKinds(t *testing.T) {
	base := Manifest{Name: "url-import", Version: "0.3.0", Protocol: Version, Command: []string{"./url-import"}}
	tests := []struct {
		name    string
		caps    []string
		kinds   []string
		wantErr string
	}{
		{"pull-only follower", []string{"import", "sites", "pull"}, []string{"chub", "character-tavern"}, ""},
		{"no pull", []string{"import"}, []string{"chub"}, "source_kinds needs the pull capability and not push"},
		{"an app", []string{"push", "pull"}, []string{"chub"}, "source_kinds needs the pull capability and not push"},
		{"bad kind", []string{"pull"}, []string{"Chub"}, `source kind "Chub"`},
		{"twice", []string{"pull"}, []string{"chub", "chub"}, `source kind "chub" listed twice`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := base
			m.Capabilities, m.SourceKinds = tt.caps, tt.kinds
			err := m.Validate()
			if tt.wantErr == "" && err != nil {
				t.Fatalf("Validate = %v", err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("Validate = %v, want %q", err, tt.wantErr)
			}
		})
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./protocol -run TestManifestSourceKinds`
Expected: FAIL to compile: `m.SourceKinds undefined`.

- [ ] **Step 3: Add the key**

In `protocol/manifest.go`, after the `Inputs` field:

```go
	// SourceKinds are the site source kinds a pull-only plugin follows: the
	// core binds each such source to its instance as remote_id kind:ref.
	SourceKinds []string `toml:"source_kinds"`
```

At the end of `Validate`, before `return errors.Join(errs...)`:

```go
	if len(m.SourceKinds) > 0 && (!m.Has("pull") || m.Has("push")) {
		errs = append(errs, errors.New("source_kinds needs the pull capability and not push"))
	}
	seenKinds := map[string]bool{}
	for _, k := range m.SourceKinds {
		if !sourceKindPattern.MatchString(k) {
			errs = append(errs, fmt.Errorf("source kind %q must match %s", k, sourceKindPattern))
		}
		if seenKinds[k] {
			errs = append(errs, fmt.Errorf("source kind %q listed twice", k))
		}
		seenKinds[k] = true
	}
```

`sourceKindPattern` already lives in `protocol/sites.go`, which validates the same kinds in `Source.Validate`.

- [ ] **Step 4: Run the tests**

Run: `go test ./protocol`
Expected: PASS.

- [ ] **Step 5: Document the key**

In `docs/protocol.md`, add a row to the manifest table after `inputs`:

```markdown
| `source_kinds` | no; only for `pull` without `push` | The site source kinds the plugin follows, such as `chub`, each listed once, in the same form as a source's `kind`. The core binds each source of these kinds to the plugin's instance and checks it for updates; see [Following site sources](#following-site-sources) |
```

- [ ] **Step 6: Commit**

```bash
git add protocol/manifest.go protocol/manifest_test.go docs/protocol.md
git commit -m "feat(protocol): let a pull-only plugin name the source kinds it follows"
```

---

### Task 2: Pull-only binding columns and status

**Files:**
- Create: `internal/db/migrations/0022_pull_only_bindings.sql`
- Test: `internal/targets/sources_test.go` (new)

- [ ] **Step 1: Write the failing tests**

Create `internal/targets/sources_test.go`:

```go
package targets

import (
	"testing"

	"github.com/davidgibbons/innkeeper/internal/db"
	"github.com/davidgibbons/innkeeper/internal/library"
)

func TestPullOnlyBindingsMayShareAnItem(t *testing.T) {
	e := setup(t, nil)
	insert := func(card int64, push bool) error {
		_, err := e.pool.Exec(e.ctx, `INSERT INTO binding (instance_id, card_id, remote_id, remote_state, accepted_data,
			accepted_hash, push) VALUES ($1, $2, 'shared', 'unknown', '{}', '', $3)`, e.inst, card, push)
		return err
	}
	if err := insert(e.card, false); err != nil {
		t.Fatal(err)
	}
	if err := insert(e.newCard("Tova"), false); err != nil {
		t.Fatalf("second pull-only binding on one item: %v", err)
	}
	if err := insert(e.newCard("Pell"), true); err != nil {
		t.Fatalf("first push binding on the item: %v", err)
	}
	if err := insert(e.newCard("Iko"), true); !db.IsCode(err, db.UniqueViolation) {
		t.Fatalf("second push binding on one item = %v, want a unique violation", err)
	}
}

func TestAPullOnlyBindingIsNeverAhead(t *testing.T) {
	e := setup(t, nil)
	b := e.push(e.card)
	if _, err := e.pool.Exec(e.ctx, `UPDATE binding SET push = false WHERE id = $1`, b.ID); err != nil {
		t.Fatal(err)
	}
	e.update(e.card, func(ed *library.Edit) { ed.Data = setField(ed.Data, "description", "A dwarf smith, retired.") })
	if got := e.status(e.card); got != "in_sync" {
		t.Fatalf("status = %s, want in_sync", got)
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `$DB go test ./internal/targets -run 'TestPullOnlyBindingsMayShareAnItem|TestAPullOnlyBindingIsNeverAhead' -timeout 15m`
Expected: FAIL with `column "push" of relation "binding" does not exist`.

- [ ] **Step 3: Write the migration**

Create `internal/db/migrations/0022_pull_only_bindings.sql`:

```sql
-- push is false for a binding that follows a site source on a pull-only
-- target: the core only reads it.
ALTER TABLE binding ADD COLUMN push boolean NOT NULL DEFAULT true,
    -- The owner stopped checking it; checks skip it.
    ADD COLUMN paused boolean NOT NULL DEFAULT false,
    -- sha256 of the site's avatar as last accepted, for a pull-only binding.
    ADD COLUMN avatar_hash text,
    -- Checks of a pull-only binding that failed in a row, and the last error.
    ADD COLUMN check_failures int NOT NULL DEFAULT 0,
    ADD COLUMN check_error text;

-- Cards that share a source each follow it, so only push bindings own their item.
DROP INDEX binding_card_remote;
DROP INDEX binding_lorebook_remote;
CREATE UNIQUE INDEX binding_card_remote ON binding (instance_id, remote_id)
    WHERE card_id IS NOT NULL AND remote_state <> 'missing' AND push;
CREATE UNIQUE INDEX binding_lorebook_remote ON binding (instance_id, remote_id)
    WHERE lorebook_id IS NOT NULL AND remote_state <> 'missing' AND push;

-- As in 0011, except that a pull-only binding is never ahead: nothing pushes it.
DROP VIEW binding_status;
CREATE VIEW binding_status AS
SELECT b.id AS binding_id, b.instance_id, b.card_id, b.lorebook_id, a.ahead,
    CASE
        WHEN b.remote_state = 'missing' THEN 'missing'
        WHEN b.remote_state = 'changed' AND a.ahead THEN 'diverged'
        WHEN b.remote_state = 'changed' THEN 'remote_changed'
        WHEN a.ahead THEN 'ahead'
        WHEN b.remote_state = 'unknown' THEN 'cant_check'
        ELSE 'in_sync'
    END AS status
FROM binding b
JOIN LATERAL (SELECT revision_id, lorebook_revision_ids, lorebook_remote_ids, app_behind FROM push WHERE binding_id = b.id ORDER BY id DESC LIMIT 1) p ON TRUE
CROSS JOIN LATERAL (
    -- A card is ahead when its data, avatar, override for this instance, or
    -- lorebooks changed since the last push.
    SELECT b.push AND (p.app_behind OR hr.content_hash <> pr.content_hash
        OR hr.avatar_blob IS DISTINCT FROM pr.avatar_blob
        OR hr.overrides -> b.instance_id::text IS DISTINCT FROM pr.overrides -> b.instance_id::text
        OR coalesce(p.lorebook_revision_ids <> array(
            SELECT l.head_revision_id FROM card_lorebook cl JOIN lorebook l ON l.id = cl.lorebook_id
            WHERE cl.card_id = b.card_id ORDER BY l.head_revision_id), FALSE)
        -- A linked lorebook unbound here adds a NULL, which no push sent.
        OR coalesce(p.lorebook_remote_ids <> array(
            SELECT lb.remote_id FROM card_lorebook cl
            LEFT JOIN binding lb ON lb.lorebook_id = cl.lorebook_id AND lb.instance_id = b.instance_id
            -- Byte order, as the core sorts them.
            WHERE cl.card_id = b.card_id ORDER BY lb.remote_id COLLATE "C"), FALSE)) AS ahead
    FROM card c JOIN card_revision hr ON hr.id = c.head_revision_id
    JOIN card_revision pr ON pr.id = p.revision_id
    WHERE c.id = b.card_id
    UNION ALL
    -- A lorebook is ahead when its data or override for this instance changed.
    SELECT b.push AND (p.app_behind OR hr.content_hash <> pr.content_hash
        OR hr.overrides -> b.instance_id::text IS DISTINCT FROM pr.overrides -> b.instance_id::text)
    FROM lorebook l JOIN lorebook_revision hr ON hr.id = l.head_revision_id
    JOIN lorebook_revision pr ON pr.id = p.revision_id
    WHERE l.id = b.lorebook_id
) a;
```

- [ ] **Step 4: Run the tests**

Run: `$DB go test ./internal/targets ./internal/db -timeout 15m`
Expected: PASS, including every existing targets test, since `push` defaults to true.

- [ ] **Step 5: Commit**

```bash
git add internal/db/migrations/0022_pull_only_bindings.sql internal/targets/sources_test.go
git commit -m "feat(targets): add pull-only bindings that are never ahead and may share an item"
```

---

### Task 3: Targets accept a pull-only plugin

**Files:**
- Modify: `internal/targets/targets.go:178-197` (`describe`), `internal/targets/push.go:46`, `internal/targets/preview.go:36`, `internal/targets/bind.go:130`
- Modify: `internal/targets/env_test.go` (the `env` struct, `setup`)
- Test: `internal/targets/sources_test.go`

- [ ] **Step 1: Give the test env a pull-only fake**

In `internal/targets/env_test.go`, add two fields to `env` after `card int64`:

```go
	// up is the pull-only fake's instance, once addUpstream made one; updir is where it keeps the site's pages.
	up    int64
	updir string
```

In `setup`, after the `sites` manifest lines, add:

```go
	// upstream is the fake as a pull-only plugin that follows fake sources; addUpstream gives it an instance.
	upstream := *m
	upstream.Name, upstream.Capabilities, upstream.SourceKinds = "upstream", []string{"pull"}, []string{"fake"}
	upstream.Command = []string{m.Command[0], "-caps=pull", "-name=upstream"}
```

and change the registry line to:

```go
	reg := plugins.New(pool, map[string]*protocol.Manifest{"fake": m, "sites": &sites, "upstream": &upstream}, store, box)
```

Append to `internal/targets/sources_test.go`, and add `encoding/json`, `errors`, `maps`, `os`, `path/filepath`, and `github.com/davidgibbons/innkeeper/internal/plugins` to its imports:

```go
// addUpstream creates an instance of the pull-only fake. It keeps each site
// page as <updir>/fake:<ref>.json, and config adds to its settings.
func (e *env) addUpstream(config map[string]any) {
	e.t.Helper()
	e.updir = e.t.TempDir()
	cfg := map[string]any{"dir": e.updir, "fields": []string{"name", "description", "first_mes"}, "lorebooks": "standalone"}
	maps.Copy(cfg, config)
	id, err := e.reg.CreateInstance(e.ctx, plugins.NewInstance{Plugin: "upstream", Name: "Sites", Config: cfg})
	if err != nil {
		e.t.Fatal(err)
	}
	e.up = id
}

// publish puts a card's page on the fake site as its author last saved it.
// avatar is an image file, or empty.
func (e *env) publish(ref string, data map[string]any, avatar string) {
	e.t.Helper()
	item := map[string]any{"spec": "chara_card_v3", "spec_version": "3.0", "data": data}
	if avatar != "" {
		item["avatar"] = avatar
	}
	e.writeSite(ref, item)
}

func (e *env) writeSite(ref string, item map[string]any) {
	e.t.Helper()
	raw, _ := json.Marshal(item)
	if err := os.WriteFile(filepath.Join(e.updir, "fake:"+ref+".json"), raw, 0o644); err != nil {
		e.t.Fatal(err)
	}
}

// imported creates a card as an import plugin would, with the fake source ref.
func (e *env) imported(ref string, data map[string]any) int64 {
	e.t.Helper()
	raw, _ := json.Marshal(data)
	c, _, err := e.lib.Cards.CreateFromName(e.ctx, data["name"].(string), "upload", library.Edit{Data: raw, Source: "plugin:upstream"})
	if err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.lib.Cards.AddSource(e.ctx, c.ID, library.Source{Kind: "fake", Ref: ref}); err != nil {
		e.t.Fatal(err)
	}
	return c.ID
}
```

- [ ] **Step 2: Write the failing test**

Append to `internal/targets/sources_test.go`:

```go
func TestPushRefusesAPullOnlyTarget(t *testing.T) {
	e := setup(t, nil)
	e.addUpstream(nil)
	if _, err := e.s.Push(e.ctx, library.Card, e.up, e.card, false); !errors.Is(err, ErrCannot) {
		t.Fatalf("push = %v, want ErrCannot", err)
	}
}

func TestAPullOnlyTargetCanBeRead(t *testing.T) {
	e := setup(t, nil)
	e.addUpstream(nil)
	if items, err := e.s.Items(e.ctx, library.Card, e.up); err != nil || len(items) != 0 {
		t.Fatalf("items = %+v, %v", items, err)
	}
}
```

- [ ] **Step 3: Run it to verify it fails**

Run: `$DB go test ./internal/targets -run 'TestPushRefusesAPullOnlyTarget|TestAPullOnlyTargetCanBeRead' -timeout 15m`
Expected: `TestAPullOnlyTargetCanBeRead` FAILS with `plugin can't push: upstream`. `TestPushRefusesAPullOnlyTarget` passes already. It guards Step 4.

- [ ] **Step 4: Split `describe`**

In `internal/targets/targets.go`, replace the head of `describe`, from its doc comment through the `ErrIncapable` branch:

```go
// describe resolves a target plugin's instance, one that pushes or pulls,
// and asks what its app stores.
func (s *Service) describe(ctx context.Context, instanceID int64) (pluginhost.Instance, protocol.TargetInfo, error) {
	var info protocol.TargetInfo
	inst, err := s.reg.Instance(ctx, instanceID)
	if err != nil {
		return inst, info, err
	}
	if !inst.Manifest.Has("push") && !inst.Manifest.Has("pull") {
		return inst, info, fmt.Errorf("%w: %s", ErrCannot, inst.Manifest.Name)
	}
```

The rest of the function, from `s.reg.Call(... MethodTargetDescribe ...)` on, is unchanged. Add below it:

```go
// describePush is describe for a target the library's copy is sent to, so
// not one that only follows site sources.
func (s *Service) describePush(ctx context.Context, instanceID int64) (pluginhost.Instance, protocol.TargetInfo, error) {
	inst, info, err := s.describe(ctx, instanceID)
	if err == nil && !inst.Manifest.Has("push") {
		err = fmt.Errorf("%w: %s only follows site sources", ErrCannot, inst.Manifest.Name)
	}
	return inst, info, err
}
```

Then point the three push paths at it:

```bash
sed -i '' 's/inst, info, err := s.describe(ctx, instanceID)/inst, info, err := s.describePush(ctx, instanceID)/' \
  internal/targets/push.go internal/targets/preview.go
git diff --stat
```

Expected: one line changed in each file. In `internal/targets/bind.go`, change only `CheckLorebookPush`'s call to `s.describePush(ctx, instanceID)`. `describePull`, a few lines above it, has the same line and must keep `describe`, as must `Check`.

- [ ] **Step 5: Run the tests**

Run: `$DB go test ./internal/targets -timeout 15m`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/targets
git commit -m "feat(targets): read from a pull-only target and refuse to push to it"
```

---

### Task 4: Follow sources

**Files:**
- Modify: `internal/wire/sync.go` (`Binding`), `internal/targets/targets.go` (`bindingQuery`, `scanBinding`), `internal/targets/check.go` (`Scan`)
- Create: `internal/targets/sources.go`
- Test: `internal/targets/sources_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/targets/sources_test.go`:

```go
// follow checks a card's sources now and returns its one followed binding.
func (e *env) follow(card int64) Binding {
	e.t.Helper()
	list, err := e.s.CheckEntitySources(e.ctx, library.Card, card)
	if err != nil {
		e.t.Fatal(err)
	}
	for _, b := range list {
		if !b.Push {
			return b
		}
	}
	e.t.Fatalf("card %d follows no source: %+v", card, list)
	return Binding{}
}

var vyrCard = map[string]any{"name": "Vyr", "description": "A red dragon.", "first_mes": "Who wakes me?"}

func TestFollowingASourceAcceptsTheImportedRevision(t *testing.T) {
	e := setup(t, nil)
	e.addUpstream(nil)
	card := e.imported("vyr", vyrCard)
	e.update(card, func(ed *library.Edit) { ed.Data = setField(ed.Data, "description", "My red dragon.") })
	e.publish("vyr", vyrCard, "")
	b := e.follow(card)
	if b.InstanceID != e.up || b.RemoteID != "fake:vyr" || b.RemoteState != remoteUnchanged || b.Ahead || b.Status != "in_sync" {
		t.Fatalf("binding = %+v", b)
	}
}

func TestFollowingFlagsAnUpdatePublishedSinceTheImport(t *testing.T) {
	e := setup(t, nil)
	e.addUpstream(nil)
	card := e.imported("vyr", vyrCard)
	e.publish("vyr", map[string]any{"name": "Vyr", "description": "A red dragon.", "first_mes": "Begone."}, "")
	b := e.follow(card)
	if b.RemoteState != remoteChanged || b.Status != "remote_changed" || len(b.Changes) != 1 || b.Changes[0].Field != "first_mes" {
		t.Fatalf("binding = %+v", b)
	}
}

func TestCardsSharingASourceEachFollowIt(t *testing.T) {
	e := setup(t, nil)
	e.addUpstream(nil)
	first, second := e.imported("vyr", vyrCard), e.imported("vyr", vyrCard)
	e.publish("vyr", vyrCard, "")
	a, b := e.follow(first), e.follow(second)
	if a.ID == b.ID || a.RemoteID != "fake:vyr" || b.RemoteID != "fake:vyr" {
		t.Fatalf("bindings = %+v, %+v", a, b)
	}
}

func TestRemovingASourceStopsFollowingIt(t *testing.T) {
	e := setup(t, nil)
	e.addUpstream(nil)
	card := e.imported("vyr", vyrCard)
	e.publish("vyr", vyrCard, "")
	e.follow(card)
	srcs, err := e.lib.Cards.Sources(e.ctx, card)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.lib.Cards.RemoveSource(e.ctx, card, srcs[0].ID); err != nil {
		t.Fatal(err)
	}
	list, err := e.s.CheckEntitySources(e.ctx, library.Card, card)
	if err != nil || len(list) != 0 {
		t.Fatalf("bindings = %+v, %v", list, err)
	}
}

func TestScanLeavesFollowedSourcesAlone(t *testing.T) {
	e := setup(t, nil)
	e.addUpstream(nil)
	card := e.imported("vyr", vyrCard)
	e.publish("vyr", vyrCard, "")
	b := e.follow(card)
	e.publish("vyr", map[string]any{"name": "Vyr", "description": "A red dragon.", "first_mes": "Begone."}, "")
	if _, err := e.s.Scan(e.ctx, func(ScanProgress) {}); err != nil {
		t.Fatal(err)
	}
	if got, err := e.s.Binding(e.ctx, b.ID); err != nil || got.RemoteState != remoteUnchanged {
		t.Fatalf("after a scan = %+v, %v", got, err)
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `$DB go test ./internal/targets -run 'Follow|SharingASource|RemovingASource|ScanLeaves' -timeout 15m`
Expected: FAIL to compile: `e.s.CheckEntitySources undefined` and `b.Push undefined`.

- [ ] **Step 3: Add the binding fields**

In `internal/wire/sync.go`, add to `Binding` after `Autosync`:

```go
	// Push is false for a binding that follows a site source: the core only
	// reads it, and it is never ahead.
	Push bool `json:"push"`
	// Paused means checks skip the binding, as when the owner ignores a source's changes.
	Paused bool `json:"paused"`
	// CheckFailures counts a followed source's checks that failed in a row;
	// CheckError is the last one's error.
	CheckFailures int    `json:"check_failures"`
	CheckError    string `json:"check_error,omitempty"`
```

In `internal/targets/targets.go`, change the first two lines of `bindingQuery` to:

```go
const bindingQuery = `SELECT b.id, b.instance_id, coalesce(b.card_id, 0), coalesce(b.lorebook_id, 0), b.remote_id,
		s.status, s.ahead, b.remote_state, b.autosync, b.push, b.paused, b.check_failures, coalesce(b.check_error, ''),
		b.checked_at, b.created_at, p.id, p.kind, p.revision_id, p.lorebook_revision_ids,
```

and the `row.Scan` in `scanBinding` to:

```go
	err := row.Scan(&b.ID, &b.InstanceID, &b.CardID, &b.LorebookID, &b.RemoteID, &b.Status, &b.Ahead, &b.RemoteState, &b.Autosync,
		&b.Push, &b.Paused, &b.CheckFailures, &b.CheckError,
		&b.CheckedAt, &b.CreatedAt, &p.ID, &p.Kind, &p.RevisionID, &p.LorebookRevisionIDs, &p.LorebookRemoteIDs, &p.PushedHash,
		&p.RemoteID, &p.PushedAt, &accepted, &seen)
```

- [ ] **Step 4: Share Scan's loop**

In `internal/targets/check.go`, replace `Scan` from its `rows, err := s.pool.Query` line through the end of the `for _, id := range ids` loop with:

```go
	// Includes 'unknown' bindings, so a target that gains pull gets checked.
	// Followed sources are CheckSources' job, on their own schedule.
	rows, err := s.pool.Query(ctx, `SELECT b.id FROM binding b JOIN plugin_instance i ON i.id = b.instance_id
		WHERE i.broken_error IS NULL AND b.push ORDER BY b.id`)
	if err != nil {
		return p, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return p, err
	}
	if err := s.checkEach(ctx, ids, &p, report, nil); err != nil {
		return p, err
	}
```

In the apps loop below it, replace the `case err != nil:` body with `p.fail(fmt.Sprintf("instance %d", id), err)` and keep its `continue`. Then add after `Scan`:

```go
// checkEach checks each binding and counts what it found in p. A binding that
// fails to check is counted, and the rest go on. then, if set, runs after
// each check that worked, and its error counts as the binding's.
func (s *Service) checkEach(ctx context.Context, ids []int64, p *ScanProgress, report func(ScanProgress),
	then func(Binding) error) error {
	for _, id := range ids {
		b, err := s.Check(ctx, id)
		if err == nil && then != nil {
			err = then(b)
		}
		switch {
		case ctx.Err() != nil:
			return ctx.Err()
		case errors.Is(err, ErrNotFound):
			continue
		case err != nil:
			p.fail(fmt.Sprintf("binding %d", id), err)
		case b.RemoteState == remoteUnknown:
			// The target still can't pull, so nothing was checked.
		default:
			p.Checked++
			switch b.RemoteState {
			case remoteChanged:
				p.Changed++
			case remoteMissing:
				p.Missing++
			}
		}
		report(*p)
	}
	return nil
}

// fail counts a failure and keeps its error, up to maxErrors.
func (p *ScanProgress) fail(what string, err error) {
	p.Failed++
	if len(p.Errors) < maxErrors {
		p.Errors = append(p.Errors, fmt.Sprintf("%s: %v", what, err))
	}
}
```

- [ ] **Step 5: Write `sources.go`**

Create `internal/targets/sources.go`:

```go
package targets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/davidgibbons/innkeeper/internal/library"
)

// followers maps each pull-only instance to the source kinds it follows. A
// kind two plugins claim goes to the oldest working instance.
func (s *Service) followers(ctx context.Context) (map[int64][]string, error) {
	insts, err := s.reg.InstancesWith(ctx, "pull")
	if err != nil {
		return nil, err
	}
	manifests := s.reg.Manifests()
	claimed := map[string]bool{}
	out := map[int64][]string{}
	for _, in := range insts {
		m := manifests[in.Plugin]
		if in.BrokenError != nil || m == nil || m.Has("push") {
			continue
		}
		for _, kind := range m.SourceKinds {
			if !claimed[kind] {
				claimed[kind] = true
				out[in.ID] = append(out[in.ID], kind)
			}
		}
	}
	return out, nil
}

// follow binds each live source of kinds that has no binding on the
// instance yet, and unbinds the instance's followed bindings whose source the
// owner removed. only limits it to one entity; 0 means all of them. The
// accepted copy is the entity's first revision a plugin wrote, else its
// first, so an update published since the import shows on the first check.
//
// ponytail: an entity with two sources of one followed kind follows the
// older; a binding per source needs a key wider than (instance, entity).
func (s *Service) follow(ctx context.Context, k kind, instanceID int64, kinds []string, only int64) error {
	_, info, err := s.describePull(ctx, k, instanceID)
	if k.isLorebook() && errors.Is(err, ErrCannot) {
		// The target keeps lorebooks inside cards, so it can't follow one.
		return nil
	}
	if err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM binding b WHERE b.instance_id = $1 AND NOT b.push AND b.`+k.FK+` IS NOT NULL
			AND ($2::bigint = 0 OR b.`+k.FK+` = $2) AND NOT EXISTS (SELECT 1 FROM `+k.SourceTable+` src
				WHERE src.`+k.FK+` = b.`+k.FK+` AND src.removed_at IS NULL AND src.kind || ':' || src.ref = b.remote_id)`,
			instanceID, only); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT DISTINCT ON (src.`+k.FK+`) src.`+k.FK+`, src.kind || ':' || src.ref, e.head_revision_id,
				(SELECT r.data FROM `+k.RevTable+` r WHERE r.`+k.FK+` = src.`+k.FK+`
					ORDER BY r.source LIKE 'plugin:%' DESC, r.id LIMIT 1)
			FROM `+k.SourceTable+` src JOIN `+k.Table+` e ON e.id = src.`+k.FK+`
			WHERE src.removed_at IS NULL AND src.kind = ANY($2) AND e.archived_at IS NULL AND ($3::bigint = 0 OR src.`+k.FK+` = $3)
				AND NOT EXISTS (SELECT 1 FROM binding b WHERE b.instance_id = $1 AND b.`+k.FK+` = src.`+k.FK+`)
			ORDER BY src.`+k.FK+`, src.id`, instanceID, kinds, only)
		if err != nil {
			return err
		}
		type want struct {
			id, head int64
			remoteID string
			data     json.RawMessage
		}
		wants, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (want, error) {
			var w want
			err := row.Scan(&w.id, &w.remoteID, &w.head, &w.data)
			return w, err
		})
		if err != nil {
			return err
		}
		for _, w := range wants {
			accepted, hash, err := Project(w.data, k.fields(info))
			if err != nil {
				return err
			}
			var bindingID int64
			err = tx.QueryRow(ctx, `INSERT INTO binding (instance_id, `+k.FK+`, remote_id, remote_state, accepted_data,
					accepted_hash, push)
				VALUES ($1, $2, $3, 'unknown', $4, $5, false) ON CONFLICT DO NOTHING RETURNING id`,
				instanceID, w.id, w.remoteID, accepted, hash).Scan(&bindingID)
			if errors.Is(err, pgx.ErrNoRows) {
				// Another check bound it first.
				continue
			}
			if err != nil {
				return err
			}
			// binding_status needs a push row; a pull-only binding is never ahead of it.
			if _, err := tx.Exec(ctx, `INSERT INTO push (binding_id, kind, revision_id, pushed_hash, remote_id)
				VALUES ($1, 'bind', $2, $3, $4)`, bindingID, w.head, hash, w.remoteID); err != nil {
				return err
			}
		}
		return nil
	})
}

// CheckSources binds the sources that pull-only plugins follow, then checks
// each followed binding that isn't paused. report gets the progress after each binding.
func (s *Service) CheckSources(ctx context.Context, report func(ScanProgress)) (ScanProgress, error) {
	return s.checkSources(ctx, nil, 0, report)
}

// CheckEntitySources is CheckSources for one card or lorebook, now. It
// returns the entity's bindings.
func (s *Service) CheckEntitySources(ctx context.Context, lk *library.Kind, id int64) ([]Binding, error) {
	if _, err := s.checkSources(ctx, lk, id, func(ScanProgress) {}); err != nil {
		return nil, err
	}
	return s.Bindings(ctx, lk, id)
}

// checkSources follows and checks every entity's sources, or with only, one entity's.
func (s *Service) checkSources(ctx context.Context, only *library.Kind, id int64, report func(ScanProgress)) (ScanProgress, error) {
	var p ScanProgress
	followers, err := s.followers(ctx)
	if err != nil {
		return p, err
	}
	for inst, kinds := range followers {
		for _, lk := range library.Kinds {
			if only != nil && lk != only {
				continue
			}
			if err := s.follow(ctx, targetKind(lk), inst, kinds, id); err != nil {
				if ctx.Err() != nil {
					return p, ctx.Err()
				}
				p.fail(fmt.Sprintf("instance %d", inst), err)
			}
		}
	}
	q := `SELECT b.id FROM binding b JOIN plugin_instance i ON i.id = b.instance_id
		LEFT JOIN card c ON c.id = b.card_id LEFT JOIN lorebook l ON l.id = b.lorebook_id
		WHERE NOT b.push AND NOT b.paused AND i.broken_error IS NULL AND coalesce(c.archived_at, l.archived_at) IS NULL`
	var args []any
	if only != nil {
		q += ` AND b.` + only.FK + ` = $1`
		args = append(args, id)
	}
	rows, err := s.pool.Query(ctx, q+` ORDER BY b.id`, args...)
	if err != nil {
		return p, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return p, err
	}
	return p, s.checkEach(ctx, ids, &p, report, nil)
}
```

- [ ] **Step 6: Run the tests**

Run: `$DB go test ./internal/targets -timeout 15m`
Expected: PASS. `Check` already reads a followed binding through `describe` and `fetch`. Task 5 adds the avatar and failure handling.

- [ ] **Step 7: Commit**

```bash
git add internal/wire/sync.go internal/targets
git commit -m "feat(targets): follow each site source through a pull-only binding"
```

---

### Task 5: Checking a followed source

**Files:**
- Modify: `internal/targets/check.go` (`Check`, `record`), `internal/targets/targets.go` (`stored`, `SetPaused`)
- Test: `internal/targets/sources_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/targets/sources_test.go`, and add `strings` to its imports:

```go
func TestANewAvatarOnTheSiteIsAnUpdate(t *testing.T) {
	e := setup(t, nil)
	e.addUpstream(nil)
	card := e.imported("vyr", vyrCard)
	stripes := patternPNG(t, func(x, _ int) uint8 { return uint8(x / 8 % 2 * 255) })
	rings := patternPNG(t, func(x, y int) uint8 { return uint8((x-32)*(x-32)+(y-32)*(y-32)) / 128 * 255 })
	e.publish("vyr", vyrCard, stripes)
	if b := e.follow(card); b.RemoteState != remoteUnchanged {
		t.Fatalf("first check = %+v, want the avatar accepted silently", b)
	}
	e.publish("vyr", vyrCard, rings)
	if b := e.follow(card); b.RemoteState != remoteChanged {
		t.Fatalf("after a new avatar = %+v", b)
	}
}

func TestFailedChecksOfASourceAreCounted(t *testing.T) {
	e := setup(t, nil)
	e.addUpstream(map[string]any{"fail_get": "retryable"})
	card := e.imported("vyr", vyrCard)
	var b Binding
	for range 3 {
		b = e.follow(card)
	}
	if b.CheckFailures != 3 || !strings.Contains(b.CheckError, "app unavailable") {
		t.Fatalf("binding = %+v", b)
	}
}

func TestASourceGoneFromTheSiteIsMissing(t *testing.T) {
	e := setup(t, nil)
	e.addUpstream(nil)
	card := e.imported("vyr", vyrCard)
	if b := e.follow(card); b.RemoteState != remoteMissing || b.CheckFailures != 0 {
		t.Fatalf("binding = %+v", b)
	}
}

func TestAPausedSourceIsNotChecked(t *testing.T) {
	e := setup(t, nil)
	e.addUpstream(nil)
	card := e.imported("vyr", vyrCard)
	e.publish("vyr", vyrCard, "")
	b := e.follow(card)
	if _, err := e.s.SetPaused(e.ctx, b.ID, true); err != nil {
		t.Fatal(err)
	}
	e.publish("vyr", map[string]any{"name": "Vyr", "description": "A red dragon.", "first_mes": "Begone."}, "")
	if got := e.follow(card); got.RemoteState != remoteUnchanged || !got.Paused {
		t.Fatalf("paused = %+v", got)
	}
	if _, err := e.s.SetPaused(e.ctx, b.ID, false); err != nil {
		t.Fatal(err)
	}
	if got := e.follow(card); got.RemoteState != remoteChanged {
		t.Fatalf("checked again = %+v", got)
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `$DB go test ./internal/targets -run 'NewAvatarOnTheSite|FailedChecksOfASource|GoneFromTheSite|PausedSource' -timeout 15m`
Expected: FAIL to compile: `e.s.SetPaused undefined`.

- [ ] **Step 3: Read the new columns**

In `internal/targets/targets.go`, add to the `stored` struct:

```go
	// push is false for a followed source; paused skips checks; avatarHash is
	// the site's accepted avatar, nil before the first check.
	push, paused bool
	avatarHash   *string
```

and change `stored`'s query and scan to:

```go
	err := s.pool.QueryRow(ctx, `SELECT instance_id, card_id, lorebook_id, remote_id, remote_state, accepted_hash,
		accepted_data, seen_data, version, push, paused, avatar_hash FROM binding b WHERE b.id = $1`, id).
		Scan(&b.instanceID, &card, &lorebook, &b.remoteID, &b.remoteState, &b.acceptedHash, &b.accepted, &b.seen, &b.version,
			&b.push, &b.paused, &b.avatarHash)
```

Add after `SetAutosync`:

```go
// SetPaused stops or restarts checks of a binding.
func (s *Service) SetPaused(ctx context.Context, id int64, on bool) (Binding, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE binding SET paused = $2 WHERE id = $1`, id, on)
	if err != nil {
		return Binding{}, err
	}
	if tag.RowsAffected() == 0 {
		return Binding{}, ErrNotFound
	}
	return s.Binding(ctx, id)
}
```

- [ ] **Step 4: Check a followed binding**

In `internal/targets/check.go`, add `encoding/json` to the imports and replace `Check` and `record` with:

```go
// Check reads a bound card or lorebook back from its app and records whether the app's
// copy changed since the last push or resolve. If a push or resolve lands while
// it reads, Check records nothing and returns the binding as they left it.
// A paused binding isn't read.
func (s *Service) Check(ctx context.Context, id int64) (Binding, error) {
	b, err := s.stored(ctx, id)
	if err != nil {
		return Binding{}, err
	}
	if b.paused {
		return s.Binding(ctx, id)
	}
	inst, info, err := s.describe(ctx, b.instanceID)
	if err != nil {
		return Binding{}, err
	}
	state, seen, avatar := remoteUnknown, any(nil), ""
	if info.Pull {
		var data json.RawMessage
		var hash string
		if b.push {
			data, hash, err = s.fetch(ctx, b.k, inst, info, b.remoteID)
		} else {
			data, hash, avatar, err = s.fetchFollowed(ctx, b.k, inst, info, b.remoteID)
		}
		switch {
		case remoteNotFound(err):
			state = remoteMissing
		case err != nil:
			if !b.push && ctx.Err() == nil {
				err = errors.Join(err, s.recordFailure(ctx, id, err))
			}
			return Binding{}, err
		// A followed source's first check accepts its avatar silently.
		case hash == b.acceptedHash && (b.push || b.avatarHash == nil || *b.avatarHash == avatar):
			state = remoteUnchanged
		default:
			state, seen = remoteChanged, data
		}
	}
	var accept *string
	if avatar != "" {
		accept = &avatar
	}
	if err := s.record(ctx, id, b, state, seen, accept); err != nil {
		return Binding{}, err
	}
	return s.Binding(ctx, id)
}

// fetchFollowed is fetch for a followed source. It also stores the site's
// avatar as a blob and returns its sha256, since a new avatar is an update too.
//
// ponytail: stores the avatar on every check; unreferenced blobs are
// collected after a day. Ask the plugin for a hash first if that grows.
func (s *Service) fetchFollowed(ctx context.Context, k kind, inst pluginhost.Instance, info protocol.TargetInfo,
	remoteID string) (json.RawMessage, string, string, error) {
	if k.isLorebook() {
		data, hash, err := s.fetch(ctx, k, inst, info, remoteID)
		return data, hash, "", err
	}
	got, err := s.getCard(ctx, inst, protocol.TargetGetParams{RemoteID: remoteID, Avatar: true})
	if err != nil {
		return nil, "", "", err
	}
	raw, err := cardData(got.Card)
	if err != nil {
		return nil, "", "", err
	}
	data, hash, err := Project(raw, k.fields(info))
	if err != nil || got.Avatar == "" {
		return data, hash, "", err
	}
	blob, err := s.lib.IngestBlob(ctx, got.Avatar)
	return data, hash, blob.SHA256, err
}

// record stores what a check found, unless a push or resolve landed since b
// was read. It guards on version alone: the accepted hash can't tell, because
// a push of identical content leaves it unchanged. The guard must be a column
// of the row, since a subquery would keep the statement's old snapshot when
// the UPDATE waits on a push's row lock. avatar is accepted only where none was.
func (s *Service) record(ctx context.Context, id int64, b stored, state string, seen any, avatar *string) error {
	_, err := s.pool.Exec(ctx, `UPDATE binding SET remote_state = $2, seen_data = $3,
		checked_at = CASE WHEN $2 = 'unknown' THEN NULL ELSE now() END,
		avatar_hash = coalesce(avatar_hash, $5), check_failures = 0, check_error = NULL
		WHERE id = $1 AND version = $4`, id, state, seen, b.version, avatar)
	// Another binding took the ID while this one was missing, so the app's
	// item there is a different one, and this binding stays missing.
	if db.IsCode(err, db.UniqueViolation) && b.remoteState == remoteMissing {
		return nil
	}
	return err
}

// recordFailure counts a failed check of a followed source.
func (s *Service) recordFailure(ctx context.Context, id int64, cause error) error {
	_, err := s.pool.Exec(ctx, `UPDATE binding SET check_failures = check_failures + 1, check_error = $2 WHERE id = $1`,
		id, cause.Error())
	return err
}
```

Add `github.com/davidgibbons/innkeeper/internal/pluginhost` to `check.go`'s imports.

- [ ] **Step 5: Run the tests**

Run: `$DB go test ./internal/targets -timeout 15m`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/targets
git commit -m "feat(targets): check followed sources' avatars, count failed checks, and skip paused bindings"
```

---

### Task 6: Source updates in the changes queue

**Files:**
- Modify: `internal/wire/changes.go`, `internal/targets/wire.go`, `internal/targets/changes.go`
- Test: `internal/targets/sources_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/targets/sources_test.go`:

```go
// followedStates maps each card or lorebook in the queue to its upstream entry's state.
func (e *env) followedStates() (cards, lorebooks map[int64]AppChange) {
	e.t.Helper()
	ch, err := e.s.Changes(e.ctx, ChangesFilter{})
	if err != nil {
		e.t.Fatal(err)
	}
	cards, lorebooks = map[int64]AppChange{}, map[int64]AppChange{}
	for _, r := range ch.Rows {
		for _, a := range r.Apps {
			if a.InstanceID != e.up {
				continue
			}
			if r.LorebookID != 0 {
				lorebooks[r.LorebookID] = a
			} else {
				cards[r.CardID] = a
			}
		}
	}
	return cards, lorebooks
}

func TestChangesListSourceUpdates(t *testing.T) {
	e := setup(t, nil)
	e.addUpstream(nil)
	changed, gone, same := e.imported("vyr", vyrCard), e.imported("gone", vyrCard), e.imported("same", vyrCard)
	e.publish("vyr", map[string]any{"name": "Vyr", "description": "A red dragon.", "first_mes": "Begone."}, "")
	e.publish("same", vyrCard, "")
	for _, c := range []int64{changed, gone, same} {
		e.follow(c)
	}
	e.update(same, func(ed *library.Edit) { ed.Data = setField(ed.Data, "description", "My red dragon.") })
	cards, _ := e.followedStates()
	if cards[changed].State != ChangeUpdate || cards[gone].State != ChangeMissing || len(cards) != 2 {
		t.Fatalf("entries = %+v", cards)
	}
	srcs, _ := e.lib.Cards.Sources(e.ctx, changed)
	if err := e.lib.Cards.RemoveSource(e.ctx, changed, srcs[0].ID); err != nil {
		t.Fatal(err)
	}
	if cards, _ := e.followedStates(); len(cards) != 1 {
		t.Fatalf("after removing a source = %+v", cards)
	}
}

func TestChangesShowASourceThatKeepsFailing(t *testing.T) {
	e := setup(t, nil)
	e.addUpstream(map[string]any{"fail_get": "retryable"})
	card := e.imported("vyr", vyrCard)
	for range 2 {
		e.follow(card)
	}
	if cards, _ := e.followedStates(); len(cards) != 0 {
		t.Fatalf("after 2 failures = %+v", cards)
	}
	e.follow(card)
	if cards, _ := e.followedStates(); cards[card].State != ChangeError || !strings.Contains(cards[card].Error, "app unavailable") {
		t.Fatalf("after 3 failures = %+v", cards)
	}
}

func TestChangesListALorebookSourceUpdate(t *testing.T) {
	e := setup(t, nil)
	e.addUpstream(nil)
	lb := e.newLorebook("Forge lore")
	if _, err := e.lib.Lorebooks.AddSource(e.ctx, lb, library.Source{Kind: "fake", Ref: "lore"}); err != nil {
		t.Fatal(err)
	}
	e.writeSite("lore", map[string]any{"lorebook": map[string]any{"name": "Forge lore", "description": "Forge lore, revised.",
		"entries": []any{map[string]any{"keys": []any{"forge"}, "content": "The forge burns hot."}}}})
	if _, err := e.s.CheckEntitySources(e.ctx, library.Lorebook, lb); err != nil {
		t.Fatal(err)
	}
	if _, books := e.followedStates(); books[lb].State != ChangeUpdate {
		t.Fatalf("lorebook entries = %+v", books)
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `$DB go test ./internal/targets -run 'TestChanges(ListSource|ShowASource|ListALorebook)' -timeout 15m`
Expected: FAIL to compile: `ChangeUpdate undefined` and `r.LorebookID undefined`.

- [ ] **Step 3: Add the state and the lorebook row**

In `internal/wire/changes.go`, add to the state constants:

```go
	// ChangeUpdate is a followed site source whose author changed it.
	ChangeUpdate = "update"
```

change the `State` tag to `enum:"differs,both_changed,ahead,missing,error,new,update"`, and add to `ChangeRow` after `CardID`:

```go
	// LorebookID is set instead of CardID for a lorebook that follows a site source.
	LorebookID int64 `json:"lorebook_id,omitempty"`
```

In `internal/targets/wire.go`, add `ChangeUpdate = wire.ChangeUpdate` to the constants.

- [ ] **Step 4: Build the entries**

In `internal/targets/changes.go`, add `maps` to the imports and add a constant above `Changes`:

```go
// failuresShown is how many checks of a followed source must fail in a row
// before the queue shows it, so one bad hour of a site stays quiet.
const failuresShown = 3
```

Replace the `add` closure with:

```go
	// count tallies c and says whether the filter keeps it.
	count := func(c AppChange) bool {
		if out.Counts[c.InstanceID] == nil {
			out.Counts[c.InstanceID] = map[string]int{}
		}
		out.Counts[c.InstanceID][c.State]++
		return (f.InstanceID == 0 || c.InstanceID == f.InstanceID) && (f.State == "" || c.State == f.State)
	}
	add := func(card int64, c AppChange) {
		kept := count(c)
		if card != 0 {
			seen[[2]int64{card, c.InstanceID}] = true
		}
		if !kept {
			return
		}
		if card == 0 {
			orphans = append(orphans, ChangeRow{Name: fmt.Sprintf("%s (%s only)", c.Name, names[c.InstanceID]), Apps: []AppChange{c}})
			return
		}
		r := cards[card]
		if r == nil {
			r = &ChangeRow{CardID: card}
			cards[card] = r
		}
		r.Apps = append(r.Apps, c)
	}
	books := map[int64]*ChangeRow{}
	addBook := func(lorebook int64, c AppChange) {
		if !count(c) {
			return
		}
		r := books[lorebook]
		if r == nil {
			r = &ChangeRow{LorebookID: lorebook}
			books[lorebook] = r
		}
		r.Apps = append(r.Apps, c)
	}
```

In the `binding_status` query, add `AND b.push` after `c.archived_at IS NULL`. After that query's `ForEachRow` block, add:

```go
	// Followed sources, from their last check. One the owner removed is gone
	// from the queue before the next check unbinds it.
	fl, err := s.pool.Query(ctx, `SELECT coalesce(b.card_id, 0), coalesce(b.lorebook_id, 0), b.instance_id, b.id, b.remote_id,
			b.remote_state, coalesce(b.check_error, '')
		FROM binding b LEFT JOIN card c ON c.id = b.card_id LEFT JOIN lorebook l ON l.id = b.lorebook_id
		WHERE NOT b.push AND NOT b.paused AND coalesce(c.archived_at, l.archived_at) IS NULL
			AND (b.remote_state IN ('changed', 'missing') OR b.check_failures >= $1)
			AND EXISTS (SELECT 1 FROM card_source src WHERE src.card_id = b.card_id AND src.removed_at IS NULL
					AND src.kind || ':' || src.ref = b.remote_id
				UNION ALL SELECT 1 FROM lorebook_source src WHERE src.lorebook_id = b.lorebook_id AND src.removed_at IS NULL
					AND src.kind || ':' || src.ref = b.remote_id)
		ORDER BY b.id`, failuresShown)
	if err != nil {
		return out, err
	}
	var book int64
	var state, checkErr string
	if _, err := pgx.ForEachRow(fl, []any{&card, &book, &inst, &binding, &remote, &state, &checkErr}, func() error {
		c := AppChange{InstanceID: inst, RemoteID: remote, BindingID: binding}
		switch state {
		case remoteChanged:
			c.State = ChangeUpdate
		case remoteMissing:
			c.State = ChangeMissing
		default:
			c.State, c.Error = ChangeError, checkErr
		}
		if book != 0 {
			addBook(book, c)
		} else {
			add(card, c)
		}
		return nil
	}); err != nil {
		return out, err
	}
```

After the card naming `ForEachRow` block, before `byName`, add:

```go
	lr, err := s.pool.Query(ctx, `SELECT l.id, coalesce(r.data->>'name', '') FROM lorebook l
		JOIN lorebook_revision r ON r.id = l.head_revision_id WHERE l.id = ANY($1)`, slices.Collect(maps.Keys(books)))
	if err != nil {
		return out, err
	}
	if _, err := pgx.ForEachRow(lr, []any{&id, &name}, func() error {
		r := books[id]
		r.Name = name
		named = append(named, *r)
		return nil
	}); err != nil {
		return out, err
	}
```

and make `byName` break ties on lorebooks too:

```go
	byName := func(a, b ChangeRow) int {
		return cmp.Or(cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)), cmp.Compare(a.CardID, b.CardID),
			cmp.Compare(a.LorebookID, b.LorebookID))
	}
```

Update `Changes`' doc comment's first sentence to: "Changes lists every card some app needs the owner for, the cards and lorebooks whose followed source changed, and the app items no card matches."

- [ ] **Step 5: Run the tests**

Run: `$DB go test ./internal/targets -timeout 15m`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/wire/changes.go internal/targets
git commit -m "feat(targets): list source updates, gone sources, and failing checks in the changes queue"
```

---

### Task 7: Review and dismiss a source update

**Files:**
- Modify: `internal/targets/reconcile_compare.go` (`itemBinding`, `compareItem`), `internal/targets/resolve.go` (`item`, `importItem`, `ancestor`, `bind`, `saveBinding`)
- Modify: `internal/targets/sources.go` (`Dismiss`)
- Test: `internal/targets/sources_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/targets/sources_test.go`, and add `github.com/davidgibbons/innkeeper/internal/merge` to its imports:

```go
func TestReviewingASharedSourceUsesTheCardsOwnBinding(t *testing.T) {
	e := setup(t, nil)
	e.addUpstream(nil)
	first := e.imported("vyr", vyrCard)
	second := e.imported("vyr", map[string]any{"name": "Vyr", "description": "A red dragon, older.", "first_mes": "Who wakes me?"})
	e.publish("vyr", vyrCard, "")
	e.follow(first)
	b := e.follow(second)
	c, err := e.s.CompareItem(e.ctx, library.Card, e.up, b.RemoteID, second)
	if err != nil || c.Base != "accepted" || c.AcceptedHash != b.LastPush.PushedHash {
		t.Fatalf("comparison = %+v, %v; want the second card's accepted copy", c, err)
	}
}

func TestReviewingASourceUpdateKeepsTheOwnersEdits(t *testing.T) {
	e := setup(t, nil)
	e.addUpstream(nil)
	card := e.imported("vyr", vyrCard)
	e.update(card, func(ed *library.Edit) { ed.Data = setField(ed.Data, "description", "My red dragon.") })
	e.publish("vyr", map[string]any{"name": "Vyr", "description": "A red dragon.", "first_mes": "Begone."}, "")
	b := e.follow(card)
	c, err := e.s.CompareItem(e.ctx, library.Card, e.up, b.RemoteID, card)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.Resolve(e.ctx, library.Card, Resolution{InstanceID: e.up, RemoteID: b.RemoteID, AppHash: c.AppHash,
		BaseRevisionID: c.HeadRevisionID, AcceptedHash: c.AcceptedHash, Action: "bind", CardID: card,
		Default: merge.ChooseLibrary}); err != nil {
		t.Fatal(err)
	}
	var data map[string]any
	_ = json.Unmarshal(e.head(card).Data, &data)
	if data["description"] != "My red dragon." || data["first_mes"] != "Begone." {
		t.Fatalf("head = %v", data)
	}
	if got, _ := e.s.Binding(e.ctx, b.ID); got.RemoteState != remoteUnchanged {
		t.Fatalf("binding = %+v", got)
	}
}

func TestDismissingASourceUpdateKeepsTheLibrary(t *testing.T) {
	e := setup(t, nil)
	e.addUpstream(nil)
	card := e.imported("vyr", vyrCard)
	e.publish("vyr", map[string]any{"name": "Vyr", "description": "A red dragon.", "first_mes": "Begone."}, "")
	b := e.follow(card)
	before := e.revisions(card)
	got, err := e.s.Dismiss(e.ctx, b.ID)
	if err != nil || got.RemoteState != remoteUnchanged || e.revisions(card) != before {
		t.Fatalf("dismissed = %+v, %v; revisions %d, want %d", got, err, e.revisions(card), before)
	}
	e.publish("vyr", map[string]any{"name": "Vyr", "description": "A red dragon.", "first_mes": "Begone, again."}, "")
	if got := e.follow(card); got.RemoteState != remoteChanged {
		t.Fatalf("after another change = %+v", got)
	}
}

func TestDismissingANewAvatarAcceptsIt(t *testing.T) {
	e := setup(t, nil)
	e.addUpstream(nil)
	card := e.imported("vyr", vyrCard)
	stripes := patternPNG(t, func(x, _ int) uint8 { return uint8(x / 8 % 2 * 255) })
	rings := patternPNG(t, func(x, y int) uint8 { return uint8((x-32)*(x-32)+(y-32)*(y-32)) / 128 * 255 })
	e.publish("vyr", vyrCard, stripes)
	e.follow(card)
	e.publish("vyr", vyrCard, rings)
	b := e.follow(card)
	if _, err := e.s.Dismiss(e.ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	if got := e.follow(card); got.RemoteState != remoteUnchanged {
		t.Fatalf("after dismissing the avatar = %+v", got)
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `$DB go test ./internal/targets -run 'TestReviewing|TestDismissing' -timeout 15m`
Expected: FAIL to compile: `e.s.Dismiss undefined`. With a stub `Dismiss`, `TestReviewingASharedSourceUsesTheCardsOwnBinding` fails on the accepted hash. `TestReviewingASourceUpdateKeepsTheOwnersEdits` passes already and guards the flow.

- [ ] **Step 3: Find the entity's own binding**

In `internal/targets/reconcile_compare.go`, replace `itemBinding` with:

```go
// itemBinding finds the app item's binding: the live one, else a missing one,
// since an app can reuse a missing binding's ID. Cards that share a followed
// source each have one, so id, when set, picks the entity's.
func itemBinding(ctx context.Context, q queryRower, k kind, instanceID int64, remoteID string, id int64) (boundItem, error) {
	var b boundItem
	err := q.QueryRow(ctx, `SELECT id, `+k.FK+`, accepted_hash, remote_state, accepted_data FROM binding
		WHERE instance_id = $1 AND remote_id = $2 AND `+k.FK+` IS NOT NULL
		ORDER BY `+k.FK+` = $3 DESC, remote_state = 'missing', id LIMIT 1`, instanceID, remoteID, id).
		Scan(&b.id, &b.entityID, &b.acceptedHash, &b.remoteState, &b.accepted)
	if errors.Is(err, pgx.ErrNoRows) {
		return boundItem{}, nil
	}
	return b, err
}
```

Update the four callers:
- `reconcile_compare.go`, `compareItem`: `itemBinding(ctx, s.pool, k, instanceID, remoteID, c.CardID)`
- `resolve.go`, `item`: `itemBinding(ctx, rs.tx, k, r.InstanceID, r.RemoteID, 0)`
- `resolve.go`, `importItem`: `itemBinding(ctx, rs.tx, k, instanceID, remoteID, 0)`
- `resolve.go`, `ancestor`: `itemBinding(ctx, rs.tx, k, r.InstanceID, r.RemoteID, id)`

- [ ] **Step 4: Store the avatar and `push` on resolve**

In `internal/targets/resolve.go`, change the `bind` call in `item` to:

```go
	bound, err := rs.bind(ctx, k, info, rinfo, it, r, id, inst.Manifest.Has("push"))
```

Change `bind`'s signature to:

```go
func (rs *resolver) bind(ctx context.Context, k kind, info, rinfo protocol.TargetInfo, it appItem, r Resolution,
	id int64, push bool) (ResolveResult, error) {
```

and its `saveBinding` call to:

```go
	bindingID, err := rs.saveBinding(ctx, k, r, id, cb.id, accepted, acceptedHash, it.avatar, push)
```

Replace `saveBinding` with:

```go
// saveBinding points the entity's binding, or a new one, at the app item as
// accepted, with the app's avatar, and returns its ID. push is false for a
// new binding on a target that only follows site sources. It fails with
// ErrBound if another binding has the item.
func (rs *resolver) saveBinding(ctx context.Context, k kind, r Resolution, id, bindingID int64,
	accepted json.RawMessage, acceptedHash, avatar string, push bool) (int64, error) {
	var err error
	if bindingID != 0 {
		_, err = rs.tx.Exec(ctx, `UPDATE binding SET remote_id = $2, remote_state = 'unchanged', accepted_data = $3,
				accepted_hash = $4, seen_data = NULL, checked_at = now(), version = version + 1,
				avatar_hash = nullif($5, ''), check_failures = 0, check_error = NULL
			WHERE id = $1`, bindingID, r.RemoteID, accepted, acceptedHash, avatar)
	} else {
		err = rs.tx.QueryRow(ctx, `INSERT INTO binding (instance_id, `+k.FK+`, remote_id, remote_state, accepted_data,
				accepted_hash, checked_at, avatar_hash, push)
			VALUES ($1, $2, $3, 'unchanged', $4, $5, now(), nullif($6, ''), $7) RETURNING id`,
			r.InstanceID, id, r.RemoteID, accepted, acceptedHash, avatar, push).Scan(&bindingID)
	}
	if db.IsCode(err, db.UniqueViolation) {
		return 0, ErrBound
	}
	return bindingID, err
}
```

A push binding's `avatar_hash` is never read: `Check` compares it only when `push` is false.

- [ ] **Step 5: Add `Dismiss`**

Append to `internal/targets/sources.go`, and add `github.com/davidgibbons/innkeeper/internal/merge` to its imports:

```go
// Dismiss accepts the app's copy as it is now, leaving the library as it is.
// For a followed source, this is "ignore this change": the next change on
// the site shows again.
func (s *Service) Dismiss(ctx context.Context, id int64) (Binding, error) {
	b, err := s.Binding(ctx, id)
	if err != nil {
		return Binding{}, err
	}
	lk, entity := bindingEntity(b)
	k := targetKind(lk)
	// Two-way, so the author's edits to fields the owner never touched are refused too.
	c, err := s.compareItem(ctx, k, b.InstanceID, b.RemoteID, entity, true)
	if err != nil {
		return Binding{}, err
	}
	if _, err := s.resolve(ctx, k, Resolution{InstanceID: b.InstanceID, RemoteID: b.RemoteID, AppHash: c.AppHash,
		BaseRevisionID: c.HeadRevisionID, AcceptedHash: c.AcceptedHash, Action: "bind", CardID: entity, Base: "none",
		Default: merge.ChooseLibrary}, resolveOpts{bindOnly: true, skipLeftOut: true}); err != nil {
		return Binding{}, err
	}
	return s.Binding(ctx, id)
}

// bindingEntity is the kind and ID of what b binds.
func bindingEntity(b Binding) (*library.Kind, int64) {
	if b.LorebookID != 0 {
		return library.Lorebook, b.LorebookID
	}
	return library.Card, b.CardID
}
```

- [ ] **Step 6: Run the tests**

Run: `$DB go test ./internal/targets -timeout 15m`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/targets
git commit -m "feat(targets): review a followed source against the card's own copy, and dismiss its change"
```

---

### Task 8: Autosync applies a source update without conflicts

**Files:**
- Modify: `internal/targets/sources.go` (`checkSources`, `applyUpdate`), `internal/targets/check.go` (`ScanProgress`)
- Test: `internal/targets/sources_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/targets/sources_test.go`:

```go
func TestAutosyncTakesASourceUpdateWithoutConflicts(t *testing.T) {
	e := setup(t, nil)
	e.addUpstream(nil)
	card := e.imported("vyr", vyrCard)
	e.update(card, func(ed *library.Edit) { ed.Data = setField(ed.Data, "description", "My red dragon.") })
	e.publish("vyr", map[string]any{"name": "Vyr", "description": "A red dragon.", "first_mes": "Begone."}, "")
	b := e.follow(card)
	if _, err := e.s.SetAutosync(e.ctx, b.ID, true); err != nil {
		t.Fatal(err)
	}
	p, err := e.s.CheckSources(e.ctx, func(ScanProgress) {})
	if err != nil || p.Applied != 1 {
		t.Fatalf("progress = %+v, %v", p, err)
	}
	var data map[string]any
	_ = json.Unmarshal(e.head(card).Data, &data)
	if data["description"] != "My red dragon." || data["first_mes"] != "Begone." {
		t.Fatalf("head = %v", data)
	}
}

func TestAutosyncLeavesAConflictingSourceUpdate(t *testing.T) {
	e := setup(t, nil)
	e.addUpstream(nil)
	card := e.imported("vyr", vyrCard)
	// Both sides rewrite the same word, so the hunk conflicts.
	e.update(card, func(ed *library.Edit) { ed.Data = setField(ed.Data, "description", "A blue dragon.") })
	e.publish("vyr", map[string]any{"name": "Vyr", "description": "A gold dragon.", "first_mes": "Who wakes me?"}, "")
	b := e.follow(card)
	if _, err := e.s.SetAutosync(e.ctx, b.ID, true); err != nil {
		t.Fatal(err)
	}
	before := e.revisions(card)
	p, err := e.s.CheckSources(e.ctx, func(ScanProgress) {})
	if err != nil || p.Applied != 0 || e.revisions(card) != before {
		t.Fatalf("progress = %+v, %v; revisions %d, want %d", p, err, e.revisions(card), before)
	}
	if got, _ := e.s.Binding(e.ctx, b.ID); got.RemoteState != remoteChanged {
		t.Fatalf("binding = %+v", got)
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `$DB go test ./internal/targets -run TestAutosync.*Source -timeout 15m`
Expected: FAIL to compile: `p.Applied undefined`.

- [ ] **Step 3: Count applied updates**

In `internal/targets/check.go`, add to `ScanProgress` after `Missing`:

```go
	// Applied counts followed sources whose update autosync took.
	Applied int `json:"applied,omitempty"`
```

- [ ] **Step 4: Apply after each check**

In `internal/targets/sources.go`, replace the last line of `checkSources` with:

```go
	return p, s.checkEach(ctx, ids, &p, report, func(b Binding) error {
		if b.RemoteState != remoteChanged || !b.Autosync {
			return nil
		}
		applied, err := s.applyUpdate(ctx, b)
		if applied {
			p.Applied++
		}
		return err
	})
```

Append:

```go
// applyUpdate takes a followed source's change into the library, as a review
// keeping the library side would, when no hunk conflicts and the avatar is
// the library's. It says whether it applied.
func (s *Service) applyUpdate(ctx context.Context, b Binding) (bool, error) {
	lk, entity := bindingEntity(b)
	k := targetKind(lk)
	c, err := s.compareItem(ctx, k, b.InstanceID, b.RemoteID, entity, false)
	if err != nil {
		return false, err
	}
	// Without an ancestor avatar, a different one may be the owner's choice.
	if c.Avatar != nil || hasConflict(c) {
		return false, nil
	}
	_, err = s.resolve(ctx, k, Resolution{InstanceID: b.InstanceID, RemoteID: b.RemoteID, AppHash: c.AppHash,
		BaseRevisionID: c.HeadRevisionID, AcceptedHash: c.AcceptedHash, Action: "bind", CardID: entity,
		Default: merge.ChooseLibrary}, resolveOpts{skipLeftOut: true})
	return err == nil, err
}

// hasConflict says whether any hunk of c needs the owner.
func hasConflict(c Comparison) bool {
	for _, f := range c.Fields {
		for _, h := range f.Hunks {
			if h.Status == merge.Conflict {
				return true
			}
		}
	}
	return false
}
```

A followed binding always compares with its accepted copy as the ancestor, so its hunks are `same`, `auto_library`, `auto_app`, or `conflict`. Resolve takes the `auto_*` hunks by status.

- [ ] **Step 5: Run the tests**

Run: `$DB go test ./internal/targets -timeout 15m`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/targets
git commit -m "feat(targets): with autosync on, take a source update that merges without conflicts"
```

---

### Task 9: The source-check job and its schedule

**Files:**
- Modify: `internal/targets/jobs.go`, `cmd/innkeeper/worker.go`, `README.md`

- [ ] **Step 1: Add the job kind and handler**

In `internal/targets/jobs.go`, change the kinds block to:

```go
// Job kinds. A push job's instance is the target; a scan and a source check have none.
const (
	PushKind = "target.push"
	ScanKind = "target.scan"
	// SourceCheckKind follows and checks every site source; see CheckSources.
	SourceCheckKind = "target.source-check"
)
```

and append after `RunScan`:

```go
// RunSourceCheck is the jobs.Handler for SourceCheckKind.
func (s *Service) RunSourceCheck(ctx context.Context, r *jobs.Run) error {
	_, err := s.CheckSources(ctx, func(p ScanProgress) {
		raw, _ := json.Marshal(p)
		r.SetProgress(raw)
	})
	return err
}
```

- [ ] **Step 2: Schedule it**

In `cmd/innkeeper/worker.go`, replace the `syncEvery` block with:

```go
	syncEvery, err := everyEnv("INNKEEPER_SYNC_EVERY", time.Hour)
	if err != nil {
		return err
	}
	sourceCheckEvery, err := everyEnv("INNKEEPER_SOURCE_CHECK_EVERY", 24*time.Hour)
	if err != nil {
		return err
	}
```

Add `targets.SourceCheckKind: tg.RunSourceCheck,` to `Handlers` after `targets.SyncKind`. After the `if syncEvery > 0` block, add:

```go
	if sourceCheckEvery > 0 {
		w.Schedules = append(w.Schedules, jobs.Schedule{Kind: targets.SourceCheckKind, Every: sourceCheckEvery})
	}
```

Append to the file:

```go
// everyEnv reads a schedule from env var name: a duration such as 1h, or 0
// for none. def is the schedule when it's unset.
func everyEnv(name string, def time.Duration) (time.Duration, error) {
	v := os.Getenv(name)
	if v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("%s: %q is not a duration like 1h, or 0 for no schedule", name, v)
	}
	return d, nil
}
```

- [ ] **Step 3: Document the variable**

In `README.md`, add a row after `INNKEEPER_SYNC_EVERY`:

```markdown
| `INNKEEPER_SOURCE_CHECK_EVERY` | `worker` | How often to check the sites cards and lorebooks came from for updates, such as `12h`. `0` turns the schedule off. Default `24h` |
```

- [ ] **Step 4: Build and test**

Run: `go build ./... && $DB go test ./internal/targets ./cmd/... -timeout 15m`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/targets/jobs.go cmd/innkeeper/worker.go README.md
git commit -m "feat(worker): check site sources for updates every INNKEEPER_SOURCE_CHECK_EVERY"
```

---

### Task 10: API routes

**Files:**
- Modify: `internal/api/targets.go`, `internal/api/reconcile.go:49`
- Test: `internal/api/targets_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/api/targets_test.go`, and add `os` and `path/filepath` to its imports if missing:

```go
// upstream installs the fake as a pull-only plugin that follows fake sources,
// with one instance. It returns the instance and the folder of site pages.
func (e *testEnv) upstream() (int64, string) {
	e.t.Helper()
	up := *e.manifests["target"]
	up.Name, up.Capabilities, up.SourceKinds = "upstream", []string{"pull"}, []string{"fake"}
	up.Command = []string{up.Command[0], "-caps=pull", "-name=upstream"}
	e.manifests["upstream"] = &up
	dir := e.t.TempDir()
	id, err := e.s.Registry.CreateInstance(context.Background(), plugins.NewInstance{Plugin: "upstream", Name: "Sites",
		Config: map[string]any{"dir": dir, "fields": []string{"name", "first_mes"}, "lorebooks": "standalone"}})
	if err != nil {
		e.t.Fatal(err)
	}
	return id, dir
}

func TestSourceCheckRoutes(t *testing.T) {
	e := newEnv(t)
	_, dir := e.upstream()
	var c entityOut
	e.call("POST", "/cards", map[string]any{"data": map[string]any{"name": "Vyr", "first_mes": "Who wakes me?"}}, 201, &c)
	if _, err := e.s.Lib.Cards.AddSource(context.Background(), c.ID, library.Source{Kind: "fake", Ref: "vyr"}); err != nil {
		t.Fatal(err)
	}
	page := `{"spec": "chara_card_v3", "spec_version": "3.0", "data": {"name": "Vyr", "first_mes": "Begone."}}`
	if err := os.WriteFile(filepath.Join(dir, "fake:vyr.json"), []byte(page), 0o644); err != nil {
		t.Fatal(err)
	}
	var list []targets.Binding
	e.call("POST", fmt.Sprintf("/cards/%d/source-check", c.ID), nil, 200, &list)
	if len(list) != 1 || list[0].Push || list[0].RemoteID != "fake:vyr" || list[0].RemoteState != "changed" {
		t.Fatalf("bindings = %+v", list)
	}
	path := fmt.Sprintf("/bindings/%d", list[0].ID)
	var b targets.Binding
	e.call("POST", path+"/dismiss", nil, 200, &b)
	if b.RemoteState != "unchanged" {
		t.Fatalf("dismissed = %+v", b)
	}
	e.call("PATCH", path, map[string]any{"paused": true}, 200, &b)
	if !b.Paused || b.Autosync {
		t.Fatalf("paused = %+v", b)
	}
	var a, again jobOut
	e.call("POST", "/source-checks", nil, 202, &a)
	e.call("POST", "/source-checks", nil, 202, &again)
	if a.ID == 0 || a.ID != again.ID || a.Kind != targets.SourceCheckKind {
		t.Fatalf("jobs = %+v, %+v", a, again)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `$DB go test ./internal/api -run TestSourceCheckRoutes -timeout 15m`
Expected: FAIL with `404` on `POST /cards/{id}/source-check`.

- [ ] **Step 3: Add the routes**

In `internal/api/targets.go`, replace `autosyncIn` with:

```go
type bindingEditIn struct {
	ID   int64 `path:"id"`
	Body struct {
		Autosync *bool `json:"autosync,omitempty" doc:"Leave out to keep it as it is"`
		Paused   *bool `json:"paused,omitempty" doc:"Checks skip a paused binding. Leave out to keep it as it is"`
	}
}
```

Replace the `update-binding` operation's description and handler:

```go
		Description: "With autosync on, each sync pushes the library's copy when the card is ahead and the app's copy hasn't changed; " +
			"for a binding that follows a site source, each source check takes an update that merges without conflicts. " +
			"A paused binding isn't checked. Destructive, as a push is: autosync replaces the app's copy."},
		func(ctx context.Context, in *bindingEditIn) (*bindingOut, error) {
			if in.Body.Autosync != nil {
				if _, err := s.Targets.SetAutosync(ctx, in.ID, *in.Body.Autosync); err != nil {
					return nil, s.fail(err)
				}
			}
			if in.Body.Paused != nil {
				if _, err := s.Targets.SetPaused(ctx, in.ID, *in.Body.Paused); err != nil {
					return nil, s.fail(err)
				}
			}
			return s.binding(s.Targets.Binding(ctx, in.ID))
		})
```

After the `check-binding` operation, add:

```go
	huma.Register(api, huma.Operation{OperationID: "dismiss-binding-change", Method: http.MethodPost, Path: "/bindings/{id}/dismiss",
		Summary: "Accept the app's copy without changing the library", Tags: tags,
		Description: "For a binding that follows a site source, ignores this change; the next one on the site shows again."},
		func(ctx context.Context, in *idIn) (*bindingOut, error) {
			return s.binding(s.Targets.Dismiss(ctx, in.ID))
		})

	huma.Register(api, huma.Operation{OperationID: "start-source-check", Method: http.MethodPost,
		Path: "/source-checks", Summary: "Check every site source for updates", Tags: tags, DefaultStatus: http.StatusAccepted,
		Description: "Follows each card's and lorebook's site sources and reads them back. With autosync on for a binding, " +
			"takes an update that merges without conflicts. Returns the check already queued or running, if there is one."},
		func(ctx context.Context, _ *struct{}) (*jobResp, error) {
			j, err := s.Queue.EnqueueOnce(ctx, jobs.Spec{Kind: targets.SourceCheckKind})
			if err != nil {
				return nil, s.fail(err)
			}
			return &jobResp{Body: toJob(j)}, nil
		})
```

In the per-kind loop, after `list-<kind>-bindings`, add:

```go
		huma.Register(api, huma.Operation{OperationID: "check-" + k.Name + "-sources", Method: http.MethodPost,
			Path: "/" + k.Plural + "/{id}/source-check", Summary: "Check a " + k.Name + "'s site sources for updates now", Tags: tags,
			Description: "Follows the " + k.Name + "'s sources and reads them back, as the scheduled check does. Returns its bindings."},
			func(ctx context.Context, in *idIn) (*bindingsOut, error) {
				if _, err := ents.Get(ctx, in.ID); err != nil {
					return nil, s.fail(err)
				}
				list, err := s.Targets.CheckEntitySources(ctx, k, in.ID)
				if err != nil {
					return nil, s.fail(err)
				}
				return &bindingsOut{Body: orEmpty(list)}, nil
			})
```

Change the `list-<kind>-bindings` summary to `"List a " + k.Name + "'s bindings: the apps it is pushed to and the site sources it follows"`.

In `internal/api/reconcile.go:49`, change the `State` tag's enum to `enum:"differs,both_changed,ahead,missing,error,new,update"`.

- [ ] **Step 4: Run the API tests and regenerate the schema**

Run: `$DB go test ./internal/api -timeout 15m`
Expected: PASS, including `TestBindingAutosyncRoute` and the tier test. All three new POST routes are Write, the method's default.

Run: `cd web && npm run gen && npm run typecheck && npm test`
Expected: the schema changes, typecheck passes, tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/api web/openapi.json web/src/api/schema.d.ts
git commit -m "feat(api): check, pause, and dismiss followed site sources"
```

---

### Task 11: MCP

**Files:**
- Modify: `internal/mcp/tools.go`
- Test: `internal/api/mcp_test.go`

- [ ] **Step 1: Write the failing test**

In `internal/api/mcp_test.go`, change `builtIn(all) != 35` to `builtIn(all) != 36`. Append:

```go
func TestCheckSourcesTool(t *testing.T) {
	e := newEnv(t)
	s := e.mcpClient(tier.Write)
	if got := tool(t, s, "check_sources", map[string]any{}, false); !strings.Contains(got, `"kind":"target.source-check"`) {
		t.Fatalf("check_sources = %s", got)
	}
	var c entityOut
	e.call("POST", "/cards", map[string]any{"data": map[string]any{"name": "Vyr"}}, 201, &c)
	if got := tool(t, s, "check_sources", map[string]any{"id": c.ID}, false); !strings.HasPrefix(got, "[") {
		t.Fatalf("check_sources for one card = %s", got)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `$DB go test ./internal/api -run 'TestCheckSourcesTool|TestMCPToolsByTier' -timeout 15m`
Expected: FAIL: unknown tool `check_sources`, and the count is 35.

- [ ] **Step 3: Add the tool**

In `internal/mcp/tools.go`, add after `changesIn`:

```go
type sourceCheckIn struct {
	Kind string `json:"kind,omitempty" jsonschema:"card or lorebook; default card"`
	ID   int64  `json:"id,omitempty" jsonschema:"One card or lorebook to check now; omit to start a job that checks every source"`
}
```

In `changesIn`, change the `State` description to `"Only this state: differs, both_changed, ahead, missing, error, new, or update. Ahead means the library has what the app doesn't: not pushed. Update means a followed site source changed"`.

After `refresh`, add:

```go
	def("check_sources", "Check the sites cards and lorebooks came from, such as chub.ai, for updates. With id, checks that one "+
		"now and returns its bindings; without, starts a job. An update shows in list_changes with state update; review it "+
		"with compare_item and resolve_item, passing the card's id.", tier.Write,
		func(ctx context.Context, c caller, in sourceCheckIn) (*sdk.CallToolResult, error) {
			if in.ID == 0 {
				return c.call(ctx, "POST", "/source-checks", nil)
			}
			k, err := kindNamed(cmp.Or(in.Kind, library.Card.Name))
			if err != nil {
				return nil, err
			}
			return c.call(ctx, "POST", fmt.Sprintf("/%s/%d/source-check", k.Plural, in.ID), nil)
		}),
```

Change `list_bindings`' description to `"List the target apps a card or lorebook is pushed to, and the site sources it follows, with each one's status."`.

- [ ] **Step 4: Run the tests**

Run: `$DB go test ./internal/api ./internal/mcp -timeout 15m`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/mcp/tools.go internal/api/mcp_test.go
git commit -m "feat(mcp): check site sources for updates"
```

---

### Task 12: Document pull-only targets

**Files:**
- Modify: `docs/protocol.md` (after the `push` and `pull` section)

- [ ] **Step 1: Add the section**

At the end of the `### push and pull` section, before `### codec`, add:

```markdown
#### Following site sources

A plugin with `pull`, no `push`, and `source_kinds` follows site sources. The
core binds each live source of those kinds to the plugin's oldest working
instance. The `remote_id` is `kind:ref`, such as `chub:alice/vyr`. The core
then checks each source on the owner's schedule and offers the author's
changes in the changes queue.

- `target.describe`: `pull` true, `update` false. Leave `character_book` out
  of `fields`; the core doesn't compare embedded books. Use `standalone`
  lorebooks to follow lorebook sources.
- `target.get` and `target.get_lorebook`: return the site's current copy. A
  source check sets `avatar`, so return the image when asked.
- Errors: return `-32004` when the page is gone, private, or hides its
  definition. The core shows that as "no longer on the site". Make a timeout,
  a 5xx, or rate limiting retryable. The core shows a source only after 3
  failed checks in a row.
- `target.list` and `target.list_lorebooks`: return no items. Instance checks
  and `ink plugin check` call them.
- Space out requests to one site yourself; the core checks sources one at a time.
```

- [ ] **Step 2: Commit**

```bash
git add docs/protocol.md
git commit -m "docs(protocol): describe plugins that follow site sources"
```

---

### Task 13: Verify

- [ ] **Step 1: Run what CI runs**

```bash
go vet ./...
$DB go test -race ./... -timeout 15m
cd web && npm run typecheck && npm test
```

Expected: all pass.

- [ ] **Step 2: Read the diff**

Run: `git diff origin/main --stat` and read the whole diff. Look for:
- dead code, and stale names such as `autosyncIn`
- comments that narrate the change
- a `describe` caller that pushes but wasn't moved to `describePush`

---

## Contract for 51b and 51c

### 51b: `url-import`

| Item | Value |
|---|---|
| Manifest | `capabilities = ["import", "sites", "pull"]`; `source_kinds = ["chub", "risu", "character-tavern"]`, the kinds its `sites.detect` emits. Never `push` |
| `target.describe` | `fields`: the CCv3 `data` keys the plugin returns, without `character_book`. `pull: true`, `update: false`, `lorebooks: "standalone"`, `lorebook_fields`: the `character_book` keys it returns, such as `name`, `description`, `scan_depth`, `token_budget`, `recursive_scanning`, `extensions`, `entries`. Answer without contacting a site |
| `target.get` | Params `{remote_id, avatar}`. `remote_id` is `kind:ref` with the ref exactly as stored on the source, such as `chub:alice/vyr`. Reply `{card}`, a CCv3 card. With `avatar: true`, also reply `avatar`, the image as a file in `blob_tmp`. Leave out `lorebook_remote_ids` |
| `target.get_lorebook` | Params `{remote_id}` for a lorebook source, such as `chub:alice/forge-lore`. Reply `{lorebook}`, a CCv3 `character_book` |
| Gone | `-32004` (`protocol.CodeRemoteNotFound`), not retryable, for a 404, a deleted or private page, a hidden definition, and a `remote_id` whose kind the plugin doesn't follow |
| Transient | A retryable protocol error for timeouts, 5xx, and 429 |
| `target.list`, `target.list_lorebooks` | `{"items": []}` |
| `target.put`, `target.put_lorebook` | Not implemented; method not found |
| Stable output | Two fetches of an unchanged page must return identical `data` for the listed fields, or every check flags an update. Drop volatile values, such as view counts in `extensions.chub`, before replying |
| Pacing | Space requests per host; the core checks one source at a time |

### 51c: web

Run `npm run gen` after 51a merges.

**Bindings**

Every binding has these new fields:
- `push`: false for a followed source.
- `paused`
- `check_failures`
- `check_error`, which may be absent

`GET /{cards|lorebooks}/{id}/bindings` now includes followed sources. Split the targets panel on `push`: push bindings are apps, and `push: false` bindings are sources.

The url-import instance now has the `pull` capability. Places that pick "apps" by `pull` must also require `push`:
- `web/src/welcome/BringInCards.tsx`
- `web/src/welcome/SetUpPlugins.tsx`
- target facets in search

**Changes queue (`GET /sync/changes`)**

- `AppChange.state` has a new value, `update`.
- A followed source's entry has `instance_id` (the url-import instance), `remote_id`, and `binding_id`. Its state is one of:
  - `update`
  - `missing`: show "No longer on chub" with the site's name.
  - `error`: show `error`.
- A row may carry `lorebook_id` instead of `card_id`.
- The `state` query parameter accepts `update`.

**Reviewing an update**

1. Compare with `GET /instances/{instance_id}/cards/{remote_id}/compare?id={card_id}`, or `.../lorebooks/...`. Path-escape `remote_id`: it contains `:` and `/`.
2. Resolve with `POST /instances/{instance_id}/cards/{remote_id}/resolve`. The body is the Resolution, with `action: "bind"` and the comparison's hashes.

Always send `card_id`, because several cards may follow one source.

**Owner actions**

| Action | Route |
|---|---|
| Ignore this change | `POST /bindings/{id}/dismiss` → Binding |
| Ignore all | `PATCH /bindings/{id}` `{"paused": true}` |
| Check again | `PATCH /bindings/{id}` `{"paused": false}` |
| Autosync | `PATCH /bindings/{id}` `{"autosync": true}`. Both body fields are optional now |
| Remove source | The existing `DELETE /{cards|lorebooks}/{id}/sources/{source_id}`. The queue entry disappears straight away |

**Checks**

- "Check for updates" on a card or lorebook: `POST /{cards|lorebooks}/{id}/source-check` → `Binding[]`, the entity's bindings after the check.
- Library-wide: `POST /source-checks` → 202 Job, kind `target.source-check`.
- The job's progress is `ScanProgress`: `checked`, `changed`, `missing`, `failed`, `errors`, and a new `applied`.

**Card page banner:** a binding with `push: false` and `remote_state: "changed"`.
