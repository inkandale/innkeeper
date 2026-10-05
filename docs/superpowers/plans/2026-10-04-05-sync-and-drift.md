# Sync and Drift Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Push library cards to target apps, record each push, detect both kinds of drift, and let the owner adopt changes made in an app.

**Architecture:** A new `internal/targets` package owns bindings, pushes, checks, and adopts. It calls `push` and `pull` plugins through the plugin host with three new protocol methods: `target.describe`, `target.put`, and `target.get`. A SQL view, `binding_status`, works out each binding's status, so the API, the `target_state` search filter, and later the UI all read the same answer. Pushes and the hourly drift scan are job kinds, and a bulk push reuses the bulk parent job.

**Tech Stack:** Go 1.27, `github.com/jackc/pgx/v5` v5.11.0, PostgreSQL 18, Huma v2.39.1. No new dependencies.

**Spec:** `docs/superpowers/specs/2026-10-03-innkeeper-core-design.md`, sections "Sync", "Targets", "Methods", and "Error handling".

**Kaneo:** INN-5.

---

## Scope

In this plan:

- Protocol: `target.describe`, `target.put`, `target.get`, error `-32004` for a card the app no longer has, and a contract kit check of `target.describe`.
- `binding` and `push` tables, and the `binding_status` view.
- Pushing one card or many. Per-target overrides applied as JSON merge patches. Linked lorebooks embedded as `character_book` for targets that declare `lorebooks: "embedded"`.
- Both kinds of drift. **Library ahead** comes from comparing revisions in SQL. **Remote changed** comes from reading the card back and hashing the fields the target stores.
- Adopting app changes into the card, into the target's override, or ignoring them.
- Push-only targets (`pull: false`) and create-only targets (`update: false`).
- A push preview that lists the fields a push would drop.
- An hourly drift scan job, the `target_state` search filter, the `push` CLI command, and refusing to delete an instance that has bindings.

Left out:

| Left out | Why | Where |
|---|---|---|
| Standalone lorebook sync | Decided for this plan: lorebooks travel embedded in cards. The schema and protocol leave room for it; see "Room for standalone lorebooks" | A later plan |
| `target.list`, binding a card the app already has | Needs a design for importing from an app | A later plan |
| Deleting cards in an app | `describe` reports `delete`, but nothing uses it | A later plan |
| Avatar drift | Apps re-encode images, so bytes don't compare | A later plan |
| Adopting `character_book` changes | Lorebooks are separate entities; the diff shows the change but adopt skips it | With standalone lorebooks |
| MCP tools and web UI | | Plan 7 |

## Plan series

This is plan 5 of the core. See plan 1 for the full list.

Constraints this plan leaves for later plans:

- **Plan 6 (Lumiverse target):**
  - Implement `target.describe`, `target.put`, and `target.get` as `docs/protocol.md` describes. `internal/fakeplugin/target.go` is a working reference.
  - Declare `lorebooks: "embedded"` until standalone lorebook sync exists.
  - The `avatar` param is an absolute path inside the library's blob store. Read the file; don't move or change it.
  - `target.describe` must not contact the app, because the core calls it before every push and check.
- **Plan 7 (MCP and UI):**
  - MCP `push` is `POST /cards/{id}/push`, `check_drift` is `POST /bindings/{id}/check`, and `adopt` is `POST /bindings/{id}/adopt`.
  - The UI calls `GET /cards/{id}/push-preview` before a push. It warns when `dropped_fields` isn't empty, and asks before a push when `new_copy` is true.
  - Binding statuses: `in_sync`, `ahead`, `remote_changed`, `diverged`, `missing`, `cant_check`. Show `cant_check` as "can't check app changes", never as in sync.
  - Discarding app changes is a push. The adopt route has no discard mode.
- **Standalone lorebooks:**
  - `binding.lorebook_id` exists and `binding` checks that exactly one of `card_id` and `lorebook_id` is set. Nothing sets `lorebook_id` yet.
  - `push.revision_id` has no foreign key, so it can name a `lorebook_revision`.
  - `binding_status` joins `card`, so it leaves out lorebook bindings. Add a branch for them.
  - `target.put_lorebook` and `target.get_lorebook` are reserved names. Card pushes will need to pass the lorebooks' remote IDs.
  - `internal/targets/data.go` helpers (`project`, `diff`, `applyChanges`, `overrideChanges`) work on any data object, so lorebooks can reuse them.
- **Binding a card the app already has (`target.list`):** done in `2026-10-05-09-bind-existing.md`. A bind writes a `push` row with `kind = 'bind'`, so the inner join stays.

## Design notes

Reviews during implementation changed the code after these tasks were written. Where a task's code differs from the branch, the branch is right; the notes below describe the branch.

- **Status.** A binding stores `remote_state`, which is what the last check found in the app: `unknown`, `unchanged`, `changed`, or `missing`. The spec's `binding.state` is this column. Whether the library is ahead is never stored. The `binding_status` view works it out from the last push, so it can't go stale after an edit.
- **Ahead.** The card is ahead when, compared with the pushed revision, its `content_hash` changed, its avatar changed, its override for this instance changed, or (for targets that embed lorebooks) a linked lorebook's head revision changed. An edit to a field the target doesn't store still counts as ahead. That's harmless except on create-only targets, where the push preview's `new_copy` makes the owner confirm.
- **Remote changed.** After each push, a target that can pull is read back. The read-back copy, limited to the target's `fields`, becomes `accepted_data`, and its hash is `push.pushed_hash`. Reading back means an app that reformats what it gets doesn't show as changed. A check reads the app's copy again and compares hashes. When they differ, `seen_data` holds the app's copy and the binding's `changes` list the fields that differ. Values compare the way `cardhash.ContentHash` normalizes them, so whitespace and empty values don't count.
- **Adopt** applies the changed fields to the card's data or to this instance's override, in a revision with source `adopt`, then makes `seen_data` the new `accepted_data`. Adopting into the card also removes those fields from this instance's override, which would otherwise undo the adopt at the next push. Adopting into an override builds a real merge patch against the card's data, so keys the app removed inside an object stay removed. After adopting into the card, the binding is `ahead`. That's accurate, because the card has a revision the app hasn't been pushed. `ignore` only moves the baseline. A change to `character_book` alone returns `ErrNoChanges` unless `into` is `ignore`.
- **Concurrency.** A push takes a transaction-scoped advisory lock on (instance, card) and holds it across `target.put`, so concurrent first pushes can't make two copies. It renders the card and resolves the plugin instance before taking the lock, and inside the lock calls the plugin host directly. That way it holds only one connection, and concurrent pushes can't use up the pool while holding the lock. Checks and adopts hold no lock. Every push and adopt bumps `binding.version`, and checks and adopts write with an `UPDATE` guarded on it. A guard on another table, such as the latest push ID, doesn't work: PostgreSQL re-checks a waiting `UPDATE` against the new row, but not its subqueries.
- **Job re-runs.** The job queue re-runs a push on shutdown, on a lost lease, and on a retryable error, and creating a card isn't idempotent. So once a push sends `target.put`, it finishes on a context that ignores cancellation. A create that fails with the plugin gone or a timeout returns `ErrUncertain`, which the job wraps in `jobs.ErrFinal` so it fails rather than retrying, even when the worker is stopping. A push job records the binding's latest push ID when queued. If it would create a card and another push landed first, it skips; that check runs inside the push's lock. An update always goes ahead, since re-sending the head is safe.
- **Pool size.** A running job can hold a connection while its lease heartbeat or an import notification needs another, so the worker's pool has at least `2 × concurrency + 2` connections.
- **Registry.Call** takes a resolved `pluginhost.Instance` instead of an ID, because looking up the instance needs a connection.
- **The `target_state` filter** takes an instance ID, not the instance name in the spec's example. IDs are what overrides are keyed by.
- **Errors.** A plugin's JSON-RPC error and a bad reply map to `502`. A plugin that crashed or failed to start maps to `502` without details, which go to the log. A broken instance is `409`.

## File structure

| File | Responsibility |
|---|---|
| `protocol/target.go` | `target.*` method names, params, results, `TargetInfo.Validate` |
| `internal/fakeplugin/target.go` | Fake target app that keeps cards as files, set up by instance config |
| `internal/contract/contract.go` | `target.describe` check for `push` plugins |
| `internal/db/migrations/0004_sync.sql` | `binding`, `push`, `binding_status` |
| `internal/plugins/plugins.go` | `ErrInUse`, `HasCapability`, `Call`, `Retryable` |
| `internal/library/export.go` | `CardBook`, shared by export and push |
| `internal/library/search.go` | `target_state` filter |
| `internal/targets/data.go` | Merge patch, projection, diff, applying changes |
| `internal/targets/targets.go` | Service, binding reads, `describe`, `render`, `fetch` |
| `internal/targets/push.go` | `Push`, `Preview` |
| `internal/targets/check.go` | `Check`, `Scan` |
| `internal/targets/adopt.go` | `Adopt` |
| `internal/targets/jobs.go` | Push and scan job kinds and handlers |
| `internal/api/targets.go` | Sync routes |
| `cmd/innkeeper/commands.go` | `push` command |

## Running tests

Database tests need PostgreSQL with pgvector, as in the README:

```bash
docker run -d --name innkeeper-pg -e POSTGRES_PASSWORD=postgres -p 55432:5432 pgvector/pgvector:pg18
export INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres
```

Diffs in this plan apply with `git apply` from the repo root, in task order.

---

### Task 1: Target protocol messages

**Files:**
- Create: `protocol/target.go`
- Test: `protocol/target_test.go`

- [ ] **Step 1: Write the failing test**

```go
package protocol

import (
	"strings"
	"testing"
)

func TestTargetInfoValidate(t *testing.T) {
	tests := []struct {
		name    string
		info    TargetInfo
		wantErr string
	}{
		{"valid", TargetInfo{Fields: []string{"name"}, Lorebooks: LorebooksEmbedded}, ""},
		{"no fields", TargetInfo{Lorebooks: LorebooksNone}, "fields is empty"},
		{"bad lorebooks", TargetInfo{Fields: []string{"name"}, Lorebooks: "sometimes"}, `lorebooks "sometimes"`},
		{"missing lorebooks", TargetInfo{Fields: []string{"name"}}, `lorebooks ""`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.info.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want it to mention %q", err, tt.wantErr)
			}
		})
	}
}
```

- [ ] **Step 2: Run it to make sure it fails**

Run: `go test ./protocol/ -run TestTargetInfoValidate`
Expected: FAIL to build, with `undefined: TargetInfo`.

- [ ] **Step 3: Add the messages**

```go
package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
)

// The push and pull capabilities. A push target answers target.describe and
// target.put. One that can pull also answers target.get. The names
// target.put_lorebook and target.get_lorebook are reserved for targets that
// keep lorebooks on their own.
const (
	MethodTargetDescribe = "target.describe"
	MethodTargetPut      = "target.put"
	MethodTargetGet      = "target.get"
)

// CodeRemoteNotFound is the error for a remote_id the app no longer has.
const CodeRemoteNotFound = -32004

// How a target stores lorebooks.
const (
	LorebooksEmbedded   = "embedded"
	LorebooksStandalone = "standalone"
	LorebooksNone       = "none"
)

// TargetInfo is the reply to target.describe: what the app stores.
type TargetInfo struct {
	// Fields are the CCv3 data keys the app keeps.
	Fields []string `json:"fields"`
	Pull   bool     `json:"pull"`
	// Update is false for a create-only app, where every put makes a new copy.
	Update bool `json:"update"`
	// Delete is reserved: the core doesn't delete remote cards yet.
	Delete    bool   `json:"delete"`
	Lorebooks string `json:"lorebooks"`
}

// Validate reports every problem with the description at once.
func (t TargetInfo) Validate() error {
	var errs []error
	if len(t.Fields) == 0 {
		errs = append(errs, errors.New("fields is empty"))
	}
	if !slices.Contains([]string{LorebooksEmbedded, LorebooksStandalone, LorebooksNone}, t.Lorebooks) {
		errs = append(errs, fmt.Errorf("lorebooks %q must be embedded, standalone, or none", t.Lorebooks))
	}
	return errors.Join(errs...)
}

// TargetPutParams sends one card. RemoteID is empty for a new card. Avatar
// is the path of the avatar file in a blob store, or empty.
type TargetPutParams struct {
	Card     json.RawMessage `json:"card"`
	RemoteID string          `json:"remote_id,omitempty"`
	Avatar   string          `json:"avatar,omitempty"`
}

// TargetPutResult names the app's copy of the card.
type TargetPutResult struct {
	RemoteID string `json:"remote_id"`
}

// TargetGetParams asks for the app's copy of one card.
type TargetGetParams struct {
	RemoteID string `json:"remote_id"`
}

// TargetGetResult is the app's copy, as a CCv3 card.
type TargetGetResult struct {
	Card json.RawMessage `json:"card"`
}
```

- [ ] **Step 4: Run the test**

Run: `go test ./protocol/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add protocol/target.go protocol/target_test.go
git commit -m "Add the target.describe, target.put, and target.get messages"
```

### Task 2: Fake target and the contract check

The fake plugin becomes a target app that keeps each card in `<dir>/<remote_id>.json`, so tests can read and edit what the app holds. Its instance config sets the `describe` flags. The contract kit checks `target.describe` for every `push` plugin.

**Files:**
- Create: `internal/fakeplugin/target.go`
- Modify: `internal/fakeplugin/main.go`, `internal/contract/contract.go`
- Test: `internal/contract/contract_test.go`

- [ ] **Step 1: Update the contract tests**

```diff
diff --git a/internal/contract/contract_test.go b/internal/contract/contract_test.go
index 29691d5..47d84cc 100644
--- a/internal/contract/contract_test.go
+++ b/internal/contract/contract_test.go
@@ -28,8 +28,8 @@ func TestWellBehavedPluginPasses(t *testing.T) {
 	if err != nil {
 		t.Fatal(err)
 	}
-	if len(results) != 4 {
-		t.Fatalf("ran %d checks, want 4", len(results))
+	if len(results) != 5 {
+		t.Fatalf("ran %d checks, want 5", len(results))
 	}
 	if f := failed(results); len(f) > 0 {
 		t.Fatalf("checks failed: %v", f)
@@ -46,6 +46,7 @@ func TestMisbehavingPluginsFail(t *testing.T) {
 		{"crash-unknown", "unknown method returns -32601", "plugin exited"},
 		{"crash-unknown", "exits after shutdown", "already exited"},
 		{"no-shutdown", "answers shutdown", "no response within"},
+		{"bad-describe", "target.describe returns a valid description", "fields is empty"},
 	}
 	for _, tt := range tests {
 		t.Run(tt.mode+"/"+tt.check, func(t *testing.T) {
```

- [ ] **Step 2: Run them to make sure they fail**

Run: `go test ./internal/contract/`
Expected: FAIL. `TestWellBehavedPluginPasses` ran 4 checks, want 5, and the `bad-describe` case finds no such check.

- [ ] **Step 3: Add the fake target**

```go
package main

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/davidgibbons/innkeeper/protocol"
)

// target is the fake push and pull target, set up by the instance config. It
// keeps each card as <dir>/<remote_id>.json, so tests can read and change
// what the app holds. Unset flags default to a full-sync app that embeds
// lorebooks.
type target struct {
	Dir       string   `json:"dir"`
	Fields    []string `json:"fields"`
	Pull      *bool    `json:"pull"`
	Update    *bool    `json:"update"`
	Lorebooks string   `json:"lorebooks"`
	// FailPut makes target.put fail: retryable or fatal.
	FailPut string `json:"fail_put"`
}

func newTarget(config map[string]any) target {
	raw, _ := json.Marshal(config)
	var t target
	_ = json.Unmarshal(raw, &t)
	return t
}

func (t target) describe() protocol.TargetInfo {
	info := protocol.TargetInfo{Fields: t.Fields, Pull: true, Update: true,
		Lorebooks: cmp.Or(t.Lorebooks, protocol.LorebooksEmbedded)}
	if len(info.Fields) == 0 {
		info.Fields = []string{"name", "description", "first_mes", "character_book"}
	}
	if t.Pull != nil {
		info.Pull = *t.Pull
	}
	if t.Update != nil {
		info.Update = *t.Update
	}
	return info
}

func (t target) path(id string) string { return filepath.Join(t.Dir, id+".json") }

func (t target) put(params json.RawMessage) (any, error) {
	var p protocol.TargetPutParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, protocol.NewError(protocol.CodeInvalidParams, err.Error(), false)
	}
	switch t.FailPut {
	case "retryable":
		return nil, protocol.NewError(-32000, "app unavailable", true)
	case "fatal":
		return nil, protocol.NewError(-32000, "app rejected the card", false)
	}
	if t.Dir == "" {
		return nil, protocol.NewError(-32000, "config.dir is not set", false)
	}
	info := t.describe()
	id := p.RemoteID
	if id != "" && info.Update {
		if _, err := os.Stat(t.path(id)); err != nil {
			return nil, protocol.NewError(protocol.CodeRemoteNotFound, "no card "+id, false)
		}
	} else {
		// Concurrent puts each claim their own ID.
		for n := 1; ; n++ {
			id = fmt.Sprintf("r%d", n)
			f, err := os.OpenFile(t.path(id), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
			if err == nil {
				f.Close()
				break
			}
			if !errors.Is(err, os.ErrExist) {
				return nil, err
			}
		}
	}
	var card struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(p.Card, &card); err != nil {
		return nil, protocol.NewError(protocol.CodeInvalidParams, err.Error(), false)
	}
	// Like a real app, keep only the fields it stores.
	kept := map[string]json.RawMessage{}
	for _, f := range info.Fields {
		if v, ok := card.Data[f]; ok {
			kept[f] = v
		}
	}
	stored := map[string]any{"spec": "chara_card_v3", "spec_version": "3.0", "data": kept}
	if p.Avatar != "" {
		stored["avatar"] = p.Avatar
	}
	raw, err := json.Marshal(stored)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(t.path(id), raw, 0o644); err != nil {
		return nil, err
	}
	return protocol.TargetPutResult{RemoteID: id}, nil
}

func (t target) get(params json.RawMessage) (any, error) {
	var p protocol.TargetGetParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, protocol.NewError(protocol.CodeInvalidParams, err.Error(), false)
	}
	raw, err := os.ReadFile(t.path(p.RemoteID))
	if errors.Is(err, os.ErrNotExist) {
		return nil, protocol.NewError(protocol.CodeRemoteNotFound, "no card "+p.RemoteID, false)
	}
	if err != nil {
		return nil, err
	}
	return protocol.TargetGetResult{Card: raw}, nil
}
```

```diff
diff --git a/internal/fakeplugin/main.go b/internal/fakeplugin/main.go
index f641c1c..d88698d 100644
--- a/internal/fakeplugin/main.go
+++ b/internal/fakeplugin/main.go
@@ -16,18 +16,19 @@ import (
 )
 
 func main() {
-	mode := flag.String("mode", "", "bad-protocol | wrong-caps | wrong-version | slow-init | bad-errors | silent | crash-unknown | no-shutdown")
+	mode := flag.String("mode", "", "bad-protocol | wrong-caps | wrong-version | slow-init | bad-errors | silent | crash-unknown | no-shutdown | bad-describe")
 	caps := flag.String("caps", "push,pull", "comma-separated capabilities to report")
 	flag.Parse()
 
 	var conn *protocol.Conn
 	var blobTmp string
+	var config map[string]any
 	handler := func(ctx context.Context, method string, params json.RawMessage) (any, error) {
 		switch method {
 		case protocol.MethodInitialize:
 			var init protocol.InitializeParams
 			_ = json.Unmarshal(params, &init)
-			blobTmp = init.BlobTmp
+			blobTmp, config = init.BlobTmp, init.Config
 			res := protocol.InitializeResult{
 				Protocol:     protocol.Version,
 				Name:         "fake",
@@ -76,6 +77,15 @@ func main() {
 			os.Exit(3)
 		case protocol.MethodImportRun:
 			return importRun(conn, blobTmp, params)
+		case protocol.MethodTargetDescribe:
+			if *mode == "bad-describe" {
+				return protocol.TargetInfo{Lorebooks: "sometimes"}, nil
+			}
+			return newTarget(config).describe(), nil
+		case protocol.MethodTargetPut:
+			return newTarget(config).put(params)
+		case protocol.MethodTargetGet:
+			return newTarget(config).get(params)
 		case protocol.MethodAuthBegin:
 			return protocol.AuthBeginResult{
 				Form:  json.RawMessage(`{"type": "object", "properties": {"password": {"type": "string"}}, "required": ["password"]}`),
```

- [ ] **Step 4: Add the contract check**

```diff
diff --git a/internal/contract/contract.go b/internal/contract/contract.go
index 4c3ef42..161120e 100644
--- a/internal/contract/contract.go
+++ b/internal/contract/contract.go
@@ -9,6 +9,7 @@ import (
 	"errors"
 	"fmt"
 	"log/slog"
+	"slices"
 	"time"
 
 	"github.com/davidgibbons/innkeeper/internal/pluginhost"
@@ -56,6 +57,12 @@ func Check(ctx context.Context, dir string, log *slog.Logger) ([]Result, error)
 		return append(results, Result{"answers shutdown", dead}, Result{"exits after shutdown", dead}), nil
 	}
 
+	if slices.Contains(m.Capabilities, "push") {
+		callCtx, cancel = context.WithTimeout(ctx, callTimeout)
+		results = append(results, Result{"target.describe returns a valid description", checkDescribe(callCtx, p, m)})
+		cancel()
+	}
+
 	callCtx, cancel = context.WithTimeout(ctx, callTimeout)
 	err = p.Call(callCtx, protocol.MethodShutdown, nil, nil)
 	cancel()
@@ -66,6 +73,20 @@ func Check(ctx context.Context, dir string, log *slog.Logger) ([]Result, error)
 	return append(results, Result{"exits after shutdown", p.Stop()}), nil
 }
 
+func checkDescribe(ctx context.Context, p *pluginhost.Process, m *protocol.Manifest) error {
+	var info protocol.TargetInfo
+	if err := p.Call(ctx, protocol.MethodTargetDescribe, nil, &info); err != nil {
+		return err
+	}
+	if err := info.Validate(); err != nil {
+		return err
+	}
+	if info.Pull && !slices.Contains(m.Capabilities, "pull") {
+		return errors.New("pull is true, but the manifest doesn't declare the pull capability")
+	}
+	return nil
+}
+
 // noCore answers plugin callbacks during a check, where no library exists.
 func noCore(_ context.Context, method string, _ json.RawMessage) (any, error) {
 	return nil, protocol.MethodNotFound(method)
```

- [ ] **Step 5: Run the tests**

Run: `go vet ./internal/fakeplugin && go test ./internal/contract/`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/fakeplugin internal/contract
git commit -m "Add a fake target app and check target.describe in the contract kit"
```

### Task 3: Sync schema and plugin registry helpers

**Files:**
- Create: `internal/db/migrations/0004_sync.sql`
- Modify: `internal/plugins/plugins.go`
- Test: `internal/db/db_test.go`, `internal/plugins/plugins_test.go`, `internal/plugins/call_test.go`

- [ ] **Step 1: Write the failing tests**

```diff
diff --git a/internal/db/db_test.go b/internal/db/db_test.go
index 33e20ed..729e692 100644
--- a/internal/db/db_test.go
+++ b/internal/db/db_test.go
@@ -19,8 +19,8 @@ func TestMigrateTwice(t *testing.T) {
 	if err := pool.QueryRow(ctx, `SELECT count(*) FROM schema_migration`).Scan(&n); err != nil {
 		t.Fatal(err)
 	}
-	if n != 3 {
-		t.Fatalf("schema_migration has %d rows, want 3", n)
+	if n != 4 {
+		t.Fatalf("schema_migration has %d rows, want 4", n)
 	}
 }
 
```

```diff
diff --git a/internal/plugins/plugins_test.go b/internal/plugins/plugins_test.go
index b65698d..5757c92 100644
--- a/internal/plugins/plugins_test.go
+++ b/internal/plugins/plugins_test.go
@@ -235,6 +235,24 @@ func TestDeleteInstance(t *testing.T) {
 	}
 }
 
+func TestDeleteInstanceWithBindings(t *testing.T) {
+	ctx := context.Background()
+	reg, _ := newRegistry(t)
+	id, err := reg.CreateInstance(ctx, NewInstance{Plugin: "fake", Name: "Fake"})
+	if err != nil {
+		t.Fatal(err)
+	}
+	_, err = reg.pool.Exec(ctx, `WITH c AS (INSERT INTO card (slug, provenance) VALUES ('a', 'upload') RETURNING id)
+		INSERT INTO binding (instance_id, card_id, remote_id, remote_state, accepted_data, accepted_hash)
+		SELECT $1, id, 'r1', 'unchanged', '{}', '' FROM c`, id)
+	if err != nil {
+		t.Fatal(err)
+	}
+	if err := reg.DeleteInstance(ctx, id); !errors.Is(err, ErrInUse) {
+		t.Fatalf("delete: %v, want ErrInUse", err)
+	}
+}
+
 func TestUpdateInstanceEndsSessions(t *testing.T) {
 	ctx := context.Background()
 	reg, _ := newRegistry(t)
```

```go
package plugins

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/davidgibbons/innkeeper/internal/blobstore"
	"github.com/davidgibbons/innkeeper/internal/pluginhost"
	"github.com/davidgibbons/innkeeper/internal/secrets"
	"github.com/davidgibbons/innkeeper/internal/testdb"
	"github.com/davidgibbons/innkeeper/internal/testutil"
	"github.com/davidgibbons/innkeeper/protocol"
)

func TestCallMarksBrokenOnStartFailure(t *testing.T) {
	ctx := context.Background()
	m, err := protocol.LoadManifest(testutil.FakePlugin(t, "-mode=wrong-version"))
	if err != nil {
		t.Fatal(err)
	}
	box, err := secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	store := &blobstore.Store{Name: "library", Root: t.TempDir()}
	reg := New(testdb.New(t), map[string]*protocol.Manifest{"fake": m}, store, box)
	mgr := pluginhost.NewManager(nil, time.Minute, quiet)
	t.Cleanup(mgr.Close)
	id, err := reg.CreateInstance(ctx, NewInstance{Plugin: "fake", Name: "Fake"})
	if err != nil {
		t.Fatal(err)
	}
	inst, err := reg.Instance(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	var startErr *pluginhost.StartError
	if err := reg.Call(ctx, mgr, inst, protocol.MethodTargetDescribe, nil, nil); !errors.As(err, &startErr) {
		t.Fatalf("call: %v, want a start error", err)
	}
	if _, err := reg.Instance(ctx, id); !errors.Is(err, ErrBroken) {
		t.Fatalf("instance after the failed start: %v, want ErrBroken", err)
	}
}

func TestRetryable(t *testing.T) {
	tests := []struct {
		err  error
		want bool
	}{
		{protocol.ErrClosed, true},
		{fmt.Errorf("put: %w", protocol.NewError(-32000, "rate limited", true)), true},
		{protocol.NewError(-32000, "rejected", false), false},
		{errors.New("database down"), false},
	}
	for _, tt := range tests {
		if got := Retryable(tt.err); got != tt.want {
			t.Errorf("Retryable(%v) = %v, want %v", tt.err, got, tt.want)
		}
	}
}

func TestHasCapability(t *testing.T) {
	reg := New(nil, map[string]*protocol.Manifest{"fake": {Capabilities: []string{"push"}}}, nil, nil)
	if !reg.HasCapability("fake", "push") || reg.HasCapability("fake", "pull") || reg.HasCapability("missing", "push") {
		t.Fatal("HasCapability is wrong")
	}
}
```

- [ ] **Step 2: Run them to make sure they fail**

Run: `go test ./internal/db/ ./internal/plugins/`
Expected: FAIL. `internal/plugins` doesn't build (`undefined: ErrInUse`), and `TestMigrateTwice` finds 3 migrations.

- [ ] **Step 3: Add the migration**

```sql
-- A binding links a library card to its copy in a target app. lorebook_id is
-- for targets that keep lorebooks on their own; nothing sets it yet.
CREATE TABLE binding (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    -- No cascade: an instance can't be deleted while anything is bound to it.
    instance_id bigint NOT NULL REFERENCES plugin_instance,
    card_id bigint REFERENCES card,
    lorebook_id bigint REFERENCES lorebook,
    remote_id text NOT NULL CHECK (remote_id <> ''),
    -- unknown: the target can't pull. unchanged or changed: the last check.
    -- missing: the app no longer has remote_id.
    remote_state text NOT NULL CHECK (remote_state IN ('unknown', 'unchanged', 'changed', 'missing')),
    -- The app's copy, limited to the fields it stores, as of the last push
    -- or adopt. Checks compare against accepted_hash.
    accepted_data jsonb NOT NULL,
    accepted_hash text NOT NULL,
    -- The app's copy at the last check, while it differs from accepted_data.
    seen_data jsonb,
    checked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (num_nonnulls(card_id, lorebook_id) = 1),
    UNIQUE (instance_id, card_id),
    UNIQUE (instance_id, lorebook_id)
);
CREATE INDEX ON binding (card_id);
CREATE INDEX ON binding (lorebook_id);

CREATE TABLE push (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    binding_id bigint NOT NULL REFERENCES binding ON DELETE CASCADE,
    -- A card_revision or lorebook_revision, by the binding's kind.
    revision_id bigint NOT NULL,
    -- Head revisions of the lorebooks embedded in the card, sorted. Null when
    -- the target doesn't embed lorebooks.
    lorebook_revision_ids bigint[],
    pushed_hash text NOT NULL,
    remote_id text NOT NULL,
    pushed_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON push (binding_id, id);

-- How each card binding stands. The card is ahead when its data, avatar,
-- override for this instance, or embedded lorebooks changed since the last
-- push. Lorebook bindings will need their own branch here.
CREATE VIEW binding_status AS
SELECT b.id AS binding_id, b.instance_id, b.card_id, a.ahead,
    CASE
        WHEN b.remote_state = 'missing' THEN 'missing'
        WHEN b.remote_state = 'changed' AND a.ahead THEN 'diverged'
        WHEN b.remote_state = 'changed' THEN 'remote_changed'
        WHEN a.ahead THEN 'ahead'
        WHEN b.remote_state = 'unknown' THEN 'cant_check'
        ELSE 'in_sync'
    END AS status
FROM binding b
JOIN LATERAL (SELECT * FROM push WHERE binding_id = b.id ORDER BY id DESC LIMIT 1) p ON TRUE
JOIN card c ON c.id = b.card_id
JOIN card_revision hr ON hr.id = c.head_revision_id
JOIN card_revision pr ON pr.id = p.revision_id
CROSS JOIN LATERAL (SELECT
    hr.content_hash <> pr.content_hash
    OR hr.avatar_blob IS DISTINCT FROM pr.avatar_blob
    OR hr.overrides -> b.instance_id::text IS DISTINCT FROM pr.overrides -> b.instance_id::text
    OR coalesce(p.lorebook_revision_ids <> array(
        SELECT l.head_revision_id FROM card_lorebook cl JOIN lorebook l ON l.id = cl.lorebook_id
        WHERE cl.card_id = b.card_id ORDER BY l.head_revision_id), FALSE) AS ahead) a;
```

- [ ] **Step 4: Add the registry helpers**

`Call` takes a resolved instance, so a caller holding a connection doesn't need another one to look the instance up.

```diff
diff --git a/internal/plugins/plugins.go b/internal/plugins/plugins.go
index 0086bb8..dc97e5a 100644
--- a/internal/plugins/plugins.go
+++ b/internal/plugins/plugins.go
@@ -32,6 +32,8 @@ var (
 	ErrBroken    = errors.New("plugin instance is broken")
 	ErrInvalid   = errors.New("invalid plugin instance")
 	ErrNameTaken = errors.New("plugin instance name already in use")
+	// ErrInUse means cards are still bound to the instance.
+	ErrInUse = errors.New("plugin instance has bound cards; unbind them first")
 )
 
 // LoadDir loads the manifest in each folder of dir. A folder with an invalid
@@ -75,6 +77,12 @@ func New(pool *pgxpool.Pool, manifests map[string]*protocol.Manifest, blobs *blo
 	return &Registry{pool: pool, manifests: manifests, blobs: blobs, box: box}
 }
 
+// HasCapability reports whether plugin is installed and declares capability.
+func (r *Registry) HasCapability(plugin, capability string) bool {
+	m, ok := r.manifests[plugin]
+	return ok && slices.Contains(m.Capabilities, capability)
+}
+
 // Manifests returns the installed plugins by name.
 func (r *Registry) Manifests() map[string]*protocol.Manifest { return r.manifests }
 
@@ -220,9 +228,13 @@ func (r *Registry) UpdateInstance(ctx context.Context, id int64, e InstanceEdit)
 }
 
 // DeleteInstance removes an instance. Its jobs stay, without an instance,
-// and logins through it end.
+// and logins through it end. It returns ErrInUse while cards are bound to it.
 func (r *Registry) DeleteInstance(ctx context.Context, id int64) error {
 	tag, err := r.pool.Exec(ctx, `DELETE FROM plugin_instance WHERE id = $1`, id)
+	var pgErr *pgconn.PgError
+	if errors.As(err, &pgErr) && pgErr.Code == "23503" {
+		return ErrInUse
+	}
 	if err != nil {
 		return err
 	}
@@ -324,3 +336,25 @@ func (r *Registry) MarkBroken(ctx context.Context, id int64, version string, cau
 		id, cause.Error(), time.UnixMicro(micros))
 	return err
 }
+
+// Call sends method to inst's plugin through mgr. A plugin that fails to
+// start marks the instance broken.
+func (r *Registry) Call(ctx context.Context, mgr *pluginhost.Manager, inst pluginhost.Instance, method string, params, result any) error {
+	err := mgr.Call(ctx, inst, method, params, result)
+	var startErr *pluginhost.StartError
+	if errors.As(err, &startErr) && ctx.Err() == nil {
+		id, perr := strconv.ParseInt(inst.ID, 10, 64)
+		if perr == nil {
+			perr = r.MarkBroken(context.WithoutCancel(ctx), id, inst.Version, err)
+		}
+		return errors.Join(err, perr)
+	}
+	return err
+}
+
+// Retryable reports whether a failed plugin call may work later: the plugin
+// exited, or it replied with a retryable error.
+func Retryable(err error) bool {
+	var rpcErr *protocol.Error
+	return errors.Is(err, protocol.ErrClosed) || errors.As(err, &rpcErr) && rpcErr.Retryable()
+}
```

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/db/ ./internal/plugins/`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/db internal/plugins
git commit -m "Add binding and push tables, and refuse to delete a bound instance"
```

### Task 4: Share the embedded lorebook

Push builds `character_book` the same way export does, and needs the lorebook revisions it used. Move that code into `CardBook`. The existing `TestExportCardEmbedsLorebooks` covers it.

**Files:**
- Modify: `internal/library/export.go` (replace the whole file), `internal/library/blobs.go`

- [ ] **Step 1: Replace `export.go`**

```go
package library

import (
	"context"
	"encoding/json"
	"slices"
)

// ExportCard returns a card's head revision as a CCv3 card, with its linked
// lorebooks embedded as data.character_book.
func (l *Library) ExportCard(ctx context.Context, id int64) (json.RawMessage, error) {
	c, err := l.Cards.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	rev, err := l.Cards.Revision(ctx, c.HeadRevisionID)
	if err != nil {
		return nil, err
	}
	var data map[string]json.RawMessage
	if err := json.Unmarshal(rev.Data, &data); err != nil {
		return nil, err
	}
	book, _, err := l.CardBook(ctx, id)
	if err != nil {
		return nil, err
	}
	if book != nil {
		data["character_book"] = book
	}
	return json.Marshal(map[string]any{"spec": "chara_card_v3", "spec_version": "3.0", "data": data})
}

// CardBook returns the character_book a card embeds: the first linked
// lorebook's fields, with every linked lorebook's entries. The book is nil
// when no lorebook is linked. It also returns the head revision ID of each
// linked lorebook, sorted.
//
// ponytail: merged entries keep their own id fields, which can repeat across
// lorebooks; renumber them if an app minds.
func (l *Library) CardBook(ctx context.Context, cardID int64) (json.RawMessage, []int64, error) {
	links, err := l.CardLorebooks(ctx, cardID)
	if err != nil {
		return nil, nil, err
	}
	var book map[string]any
	entries := []any{}
	revs := []int64{}
	for _, lid := range links {
		lb, err := l.Lorebooks.Get(ctx, lid)
		if err != nil {
			return nil, nil, err
		}
		lrev, err := l.Lorebooks.Revision(ctx, lb.HeadRevisionID)
		if err != nil {
			return nil, nil, err
		}
		revs = append(revs, lrev.ID)
		var b map[string]any
		if err := json.Unmarshal(lrev.Data, &b); err != nil {
			return nil, nil, err
		}
		if book == nil {
			book = b
		}
		if e, ok := b["entries"].([]any); ok {
			entries = append(entries, e...)
		}
	}
	slices.Sort(revs)
	if book == nil {
		return nil, revs, nil
	}
	book["entries"] = entries
	raw, err := json.Marshal(book)
	return raw, revs, err
}
```

- [ ] **Step 2: Add `BlobPath`**

```diff
diff --git a/internal/library/blobs.go b/internal/library/blobs.go
index 663baae..19485d0 100644
--- a/internal/library/blobs.go
+++ b/internal/library/blobs.go
@@ -113,3 +113,6 @@ func (l *Library) OpenBlob(ctx context.Context, sha string) (Blob, *os.File, err
 	}
 	return b, f, err
 }
+
+// BlobPath returns where a blob's file lives.
+func (l *Library) BlobPath(sha string) (string, error) { return l.blobs.Path(sha) }
```

- [ ] **Step 3: Run the library tests**

Run: `go test ./internal/library/`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/library/export.go internal/library/blobs.go
git commit -m "Split the embedded lorebook out of card export"
```

### Task 5: Data helpers

Pure functions over CCv3 data objects: merge patches, projecting to a target's fields, diffing, and applying changes.

**Files:**
- Create: `internal/targets/data.go`
- Test: `internal/targets/data_test.go`

- [ ] **Step 1: Write the failing tests**

```go
package targets

import (
	"encoding/json"
	"slices"
	"testing"
)

func TestMergePatch(t *testing.T) {
	// Cases from RFC 7386, appendix A.
	tests := []struct{ doc, patch, want string }{
		{`{"a":"b"}`, `{"a":"c"}`, `{"a":"c"}`},
		{`{"a":"b"}`, `{"b":"c"}`, `{"a":"b","b":"c"}`},
		{`{"a":"b"}`, `{"a":null}`, `{}`},
		{`{"a":{"b":"c"}}`, `{"a":{"b":"d","c":null}}`, `{"a":{"b":"d"}}`},
		{`{"a":[{"b":"c"}]}`, `{"a":[1]}`, `{"a":[1]}`},
		{`{"e":null}`, `{"a":1}`, `{"a":1,"e":null}`},
		{`{"n":12345678901234567890}`, `{}`, `{"n":12345678901234567890}`},
	}
	for _, tt := range tests {
		got, err := MergePatch(json.RawMessage(tt.doc), json.RawMessage(tt.patch))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != tt.want {
			t.Errorf("MergePatch(%s, %s) = %s, want %s", tt.doc, tt.patch, got, tt.want)
		}
	}
}

func TestProject(t *testing.T) {
	data := json.RawMessage(`{"name": "Brakka", "scenario": "A forge.", "first_mes": "Hail."}`)
	got, hash, err := project(data, []string{"name", "first_mes", "description"})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"first_mes":"Hail.","name":"Brakka"}` {
		t.Fatalf("projected %s", got)
	}
	_, same, _ := project(json.RawMessage(`{"name": " Brakka\r\n", "first_mes": "Hail.", "description": ""}`), []string{"name", "first_mes", "description"})
	if same != hash {
		t.Fatal("whitespace and empty fields changed the hash")
	}
}

func TestDiff(t *testing.T) {
	got, err := diff(json.RawMessage(`{"name": "Brakka", "description": "A dwarf.", "tags": [], "first_mes": "Hail."}`),
		json.RawMessage(`{"name": "Brakka ", "first_mes": "Well met.", "scenario": "A forge."}`))
	if err != nil {
		t.Fatal(err)
	}
	var fields []string
	for _, c := range got {
		fields = append(fields, c.Field)
	}
	if !slices.Equal(fields, []string{"description", "first_mes", "scenario"}) {
		t.Fatalf("changed fields = %v", fields)
	}
	if got[0].After != nil || string(got[1].After) != `"Well met."` || got[2].Before != nil {
		t.Fatalf("changes = %+v", got)
	}
}

func TestDroppedFields(t *testing.T) {
	got, err := droppedFields(json.RawMessage(`{"name": "Brakka", "scenario": "A forge.", "tags": [], "system_prompt": " "}`),
		[]string{"name"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []string{"scenario"}) {
		t.Fatalf("dropped = %v", got)
	}
}

func TestAdoptChanges(t *testing.T) {
	changes := []Change{
		{Field: "first_mes", Before: json.RawMessage(`"Hail."`), After: json.RawMessage(`"Well met."`)},
		{Field: "description", Before: json.RawMessage(`"A dwarf."`)},
		{Field: "character_book", After: json.RawMessage(`{"entries": []}`)},
	}
	data, changed, err := applyChanges(json.RawMessage(`{"name": "Brakka", "description": "A dwarf.", "first_mes": "Hail."}`), changes)
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	if string(data) != `{"first_mes":"Well met.","name":"Brakka"}` {
		t.Fatalf("data = %s", data)
	}
	over, changed, err := overrideChanges(json.RawMessage(`{"7": {"name": "Brakka (Fake)"}, "9": {"name": "Other"}}`), "7", changes)
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	if string(over) != `{"7":{"description":null,"first_mes":"Well met.","name":"Brakka (Fake)"},"9":{"name":"Other"}}` {
		t.Fatalf("overrides = %s", over)
	}
	if _, changed, _ := applyChanges(json.RawMessage(`{}`), changes[2:]); changed {
		t.Fatal("a lorebook-only change was applied")
	}
}
```

- [ ] **Step 2: Run them to make sure they fail**

Run: `go test ./internal/targets/`
Expected: FAIL to build, with `undefined: MergePatch`.

- [ ] **Step 3: Write the helpers**

```go
package targets

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/davidgibbons/innkeeper/internal/cardhash"
)

// Change is one top-level field the app changed.
type Change struct {
	Field string `json:"field"`
	// Before is absent if the field was empty at the last push or adopt.
	Before json.RawMessage `json:"before,omitempty"`
	// After is absent if the app cleared the field.
	After json.RawMessage `json:"after,omitempty"`
}

// MergePatch applies an RFC 7386 JSON merge patch to doc. Per-target
// overrides are merge patches applied to a card's data.
func MergePatch(doc, patch json.RawMessage) (json.RawMessage, error) {
	d, err := decode(doc)
	if err != nil {
		return nil, err
	}
	p, err := decode(patch)
	if err != nil {
		return nil, err
	}
	return json.Marshal(mergePatch(d, p))
}

func mergePatch(doc, patch any) any {
	p, ok := patch.(map[string]any)
	if !ok {
		return patch
	}
	d, ok := doc.(map[string]any)
	if !ok {
		d = map[string]any{}
	}
	for k, v := range p {
		if v == nil {
			delete(d, k)
		} else {
			d[k] = mergePatch(d[k], v)
		}
	}
	return d
}

// decode keeps numbers as written, so large integers survive.
func decode(raw json.RawMessage) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	err := dec.Decode(&v)
	return v, err
}

func object(raw json.RawMessage) (map[string]json.RawMessage, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	if m == nil {
		return nil, errors.New("not a JSON object")
	}
	return m, nil
}

// cardData returns a CCv3 card's data object.
func cardData(card json.RawMessage) (json.RawMessage, error) {
	var c struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(card, &c); err != nil {
		return nil, err
	}
	if _, err := object(c.Data); err != nil {
		return nil, fmt.Errorf("card data: %w", err)
	}
	return c.Data, nil
}

// project keeps only the fields a target stores, and hashes the result, so
// fields the app drops never count as changes.
func project(data json.RawMessage, fields []string) (json.RawMessage, string, error) {
	all, err := object(data)
	if err != nil {
		return nil, "", err
	}
	kept := map[string]json.RawMessage{}
	for _, f := range fields {
		if v, ok := all[f]; ok {
			kept[f] = v
		}
	}
	raw, err := json.Marshal(kept)
	if err != nil {
		return nil, "", err
	}
	hash, err := cardhash.ContentHash(raw)
	return raw, hash, err
}

// valueHash hashes one field's value as the content hash normalizes it, so
// whitespace and empty values compare the way the drift hash does.
func valueHash(v json.RawMessage) (string, error) {
	if v == nil {
		v = json.RawMessage("null")
	}
	return cardhash.ContentHash(fmt.Appendf(nil, `{"v": %s}`, v))
}

// diff lists the top-level fields that differ between two data objects, by
// name.
func diff(before, after json.RawMessage) ([]Change, error) {
	b, err := object(before)
	if err != nil {
		return nil, err
	}
	a, err := object(after)
	if err != nil {
		return nil, err
	}
	keys := slices.Sorted(maps.Keys(b))
	for k := range a {
		if _, ok := b[k]; !ok {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	var out []Change
	for _, k := range keys {
		hb, err := valueHash(b[k])
		if err != nil {
			return nil, err
		}
		ha, err := valueHash(a[k])
		if err != nil {
			return nil, err
		}
		if hb != ha {
			out = append(out, Change{Field: k, Before: b[k], After: a[k]})
		}
	}
	return out, nil
}

// droppedFields lists the fields with content that a target doesn't store.
func droppedFields(data json.RawMessage, fields []string) ([]string, error) {
	all, err := object(data)
	if err != nil {
		return nil, err
	}
	empty, err := valueHash(nil)
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, k := range slices.Sorted(maps.Keys(all)) {
		if slices.Contains(fields, k) {
			continue
		}
		if h, err := valueHash(all[k]); err != nil {
			return nil, err
		} else if h != empty {
			out = append(out, k)
		}
	}
	return out, nil
}

// adoptable leaves out character_book: lorebook changes can't be adopted yet,
// because the card's lorebooks are separate entities.
func adoptable(changes []Change) []Change {
	return slices.DeleteFunc(slices.Clone(changes), func(c Change) bool { return c.Field == "character_book" })
}

// applyChanges sets each changed field of data to the app's value, or
// removes it where the app cleared it. It reports whether it changed
// anything.
func applyChanges(data json.RawMessage, changes []Change) (json.RawMessage, bool, error) {
	changes = adoptable(changes)
	if len(changes) == 0 {
		return data, false, nil
	}
	all, err := object(data)
	if err != nil {
		return nil, false, err
	}
	for _, c := range changes {
		if c.After == nil {
			delete(all, c.Field)
		} else {
			all[c.Field] = c.After
		}
	}
	out, err := json.Marshal(all)
	return out, true, err
}

// overrideChanges adds the changes to the override for key, so the target
// gets the app's values while the card's own data stays as it is.
func overrideChanges(overrides json.RawMessage, key string, changes []Change) (json.RawMessage, bool, error) {
	changes = adoptable(changes)
	if len(changes) == 0 {
		return overrides, false, nil
	}
	all, err := object(overrides)
	if err != nil {
		return nil, false, err
	}
	patch := map[string]json.RawMessage{}
	if p, ok := all[key]; ok {
		if patch, err = object(p); err != nil {
			return nil, false, fmt.Errorf("override %s: %w", key, err)
		}
	}
	for _, c := range changes {
		if c.After == nil {
			patch[c.Field] = json.RawMessage("null")
		} else {
			patch[c.Field] = c.After
		}
	}
	if all[key], err = json.Marshal(patch); err != nil {
		return nil, false, err
	}
	out, err := json.Marshal(all)
	return out, true, err
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/targets/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/targets
git commit -m "Add merge patch, projection, and diff helpers for sync"
```

### Task 6: Push, preview, and check

**Files:**
- Create: `internal/targets/targets.go`, `internal/targets/push.go`, `internal/targets/check.go`
- Test: `internal/targets/env_test.go`, `internal/targets/push_test.go`, `internal/targets/check_test.go`

- [ ] **Step 1: Write the test helpers**

```go
package targets

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/davidgibbons/innkeeper/internal/blobstore"
	"github.com/davidgibbons/innkeeper/internal/library"
	"github.com/davidgibbons/innkeeper/internal/pluginhost"
	"github.com/davidgibbons/innkeeper/internal/plugins"
	"github.com/davidgibbons/innkeeper/internal/secrets"
	"github.com/davidgibbons/innkeeper/internal/testdb"
	"github.com/davidgibbons/innkeeper/internal/testutil"
	"github.com/davidgibbons/innkeeper/protocol"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func noCore(_ context.Context, method string, _ json.RawMessage) (any, error) {
	return nil, protocol.MethodNotFound(method)
}

type env struct {
	t    *testing.T
	ctx  context.Context
	pool *pgxpool.Pool
	lib  *library.Library
	reg  *plugins.Registry
	s    *Service
	inst int64
	// dir is where the fake app keeps its cards.
	dir  string
	card int64
}

// setup returns a Service with one fake target instance, set up by config
// plus a dir, and one card.
func setup(t *testing.T, config map[string]any) *env {
	t.Helper()
	ctx := context.Background()
	pool := testdb.New(t)
	store := &blobstore.Store{Name: "library", Root: t.TempDir()}
	lib := library.New(pool, store)
	m, err := protocol.LoadManifest(testutil.FakePluginWith(t, []string{"push", "pull"}))
	if err != nil {
		t.Fatal(err)
	}
	box, err := secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	reg := plugins.New(pool, map[string]*protocol.Manifest{"fake": m}, store, box)
	mgr := pluginhost.NewManager(noCore, time.Minute, quiet)
	t.Cleanup(mgr.Close)
	e := &env{t: t, ctx: ctx, pool: pool, lib: lib, reg: reg, s: New(pool, lib, reg, mgr, quiet), dir: t.TempDir()}
	cfg := map[string]any{"dir": e.dir}
	maps.Copy(cfg, config)
	if e.inst, err = reg.CreateInstance(ctx, plugins.NewInstance{Plugin: "fake", Name: "Fake app", Config: cfg}); err != nil {
		t.Fatal(err)
	}
	e.card = e.newCard("Brakka")
	return e
}

func (e *env) newCard(name string) int64 {
	e.t.Helper()
	data := fmt.Sprintf(`{"name": %q, "description": "A dwarf smith.", "first_mes": "Hail.", "scenario": "A forge."}`, name)
	c, _, err := e.lib.Cards.CreateFromName(e.ctx, name, "upload", library.Edit{Data: json.RawMessage(data), Source: "ui"})
	if err != nil {
		e.t.Fatal(err)
	}
	return c.ID
}

func (e *env) head(card int64) library.Revision {
	e.t.Helper()
	c, err := e.lib.Cards.Get(e.ctx, card)
	if err != nil {
		e.t.Fatal(err)
	}
	rev, err := e.lib.Cards.Revision(e.ctx, c.HeadRevisionID)
	if err != nil {
		e.t.Fatal(err)
	}
	return rev
}

// update adds a revision to card, made by change editing the head's content.
func (e *env) update(card int64, change func(*library.Edit)) {
	e.t.Helper()
	rev := e.head(card)
	ed := library.Edit{Data: rev.Data, Overrides: rev.Overrides, Source: "ui"}
	if rev.AvatarBlob != nil {
		ed.AvatarBlob = *rev.AvatarBlob
	}
	change(&ed)
	if _, err := e.lib.Cards.Update(e.ctx, card, rev.ID, ed); err != nil {
		e.t.Fatal(err)
	}
}

// setField returns data with key set to value, or removed if value is nil.
func setField(data json.RawMessage, key string, value any) json.RawMessage {
	var m map[string]any
	_ = json.Unmarshal(data, &m)
	if value == nil {
		delete(m, key)
	} else {
		m[key] = value
	}
	out, _ := json.Marshal(m)
	return out
}

// key is the instance's override key.
func (e *env) key() string { return strconv.FormatInt(e.inst, 10) }

func (e *env) push(card int64) Binding {
	e.t.Helper()
	b, err := e.s.Push(e.ctx, e.inst, card, false)
	if err != nil {
		e.t.Fatal(err)
	}
	return b
}

func (e *env) check(b Binding) Binding {
	e.t.Helper()
	b, err := e.s.Check(e.ctx, b.ID)
	if err != nil {
		e.t.Fatal(err)
	}
	return b
}

func (e *env) status(card int64) string {
	e.t.Helper()
	list, err := e.s.Bindings(e.ctx, card)
	if err != nil {
		e.t.Fatal(err)
	}
	if len(list) != 1 {
		e.t.Fatalf("card %d has %d bindings", card, len(list))
	}
	return list[0].Status
}

// remote reads the whole file the fake app keeps for a card.
func (e *env) remote(id string) map[string]any {
	e.t.Helper()
	raw, err := os.ReadFile(filepath.Join(e.dir, id+".json"))
	if err != nil {
		e.t.Fatal(err)
	}
	var card map[string]any
	if err := json.Unmarshal(raw, &card); err != nil {
		e.t.Fatal(err)
	}
	return card
}

// remoteData is the data object of the app's copy.
func (e *env) remoteData(id string) map[string]any {
	e.t.Helper()
	data, _ := e.remote(id)["data"].(map[string]any)
	return data
}

// editRemote changes the app's copy as the owner would in the app. A nil
// value removes the field.
func (e *env) editRemote(id, key string, value any) {
	e.t.Helper()
	card := e.remote(id)
	data, _ := card["data"].(map[string]any)
	if value == nil {
		delete(data, key)
	} else {
		data[key] = value
	}
	raw, _ := json.Marshal(card)
	if err := os.WriteFile(filepath.Join(e.dir, id+".json"), raw, 0o644); err != nil {
		e.t.Fatal(err)
	}
}
```

- [ ] **Step 2: Write the failing tests**

```go
package targets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/davidgibbons/innkeeper/internal/blobstore"
	"github.com/davidgibbons/innkeeper/internal/library"
	"github.com/davidgibbons/innkeeper/internal/plugins"
)

func TestPushCreatesThenUpdates(t *testing.T) {
	e := setup(t, nil)
	b := e.push(e.card)
	if b.RemoteID != "r1" || b.Status != "in_sync" || b.RemoteState != "unchanged" || b.LastPush.RemoteID != "r1" {
		t.Fatalf("first push: %+v", b)
	}
	if got := e.remoteData("r1"); got["name"] != "Brakka" || got["scenario"] != nil {
		t.Fatalf("app has %v; want the name and no scenario, which it doesn't store", got)
	}
	e.update(e.card, func(ed *library.Edit) { ed.Data = setField(ed.Data, "description", "A grumpy dwarf smith.") })
	if s := e.status(e.card); s != "ahead" {
		t.Fatalf("after an edit: %s", s)
	}
	b = e.push(e.card)
	if b.RemoteID != "r1" || b.Status != "in_sync" {
		t.Fatalf("second push: %+v", b)
	}
	if got := e.remoteData("r1")["description"]; got != "A grumpy dwarf smith." {
		t.Fatalf("app description = %v", got)
	}
	pushes, err := e.s.Pushes(e.ctx, b.ID)
	if err != nil || len(pushes) != 2 || pushes[0].RevisionID != e.head(e.card).ID {
		t.Fatalf("pushes = %+v, %v", pushes, err)
	}
}

func TestPushAppliesOverrideAndEmbedsLorebooks(t *testing.T) {
	e := setup(t, nil)
	lb, _, err := e.lib.Lorebooks.Create(e.ctx, "forge-lore", "upload", library.Edit{Source: "ui",
		Data: json.RawMessage(`{"name": "Forge lore", "entries": [{"keys": ["anvil"], "content": "Old iron."}]}`)})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.lib.LinkLorebook(e.ctx, e.card, lb.ID); err != nil {
		t.Fatal(err)
	}
	overrides := func(other string) json.RawMessage {
		return fmt.Appendf(nil, `{%q: {"name": "Brakka (Fake)"}, "999": {"name": %q}}`, e.key(), other)
	}
	e.update(e.card, func(ed *library.Edit) { ed.Overrides = overrides("Other") })
	b := e.push(e.card)
	got := e.remoteData(b.RemoteID)
	book, _ := got["character_book"].(map[string]any)
	if entries, _ := book["entries"].([]any); got["name"] != "Brakka (Fake)" || len(entries) != 1 {
		t.Fatalf("app has %v", got)
	}
	if !slices.Equal(b.LastPush.LorebookRevisionIDs, []int64{lb.HeadRevisionID}) {
		t.Fatalf("lorebook revisions = %v", b.LastPush.LorebookRevisionIDs)
	}

	// Another target's override doesn't make this one ahead.
	e.update(e.card, func(ed *library.Edit) { ed.Overrides = overrides("Changed") })
	if s := e.status(e.card); s != "in_sync" {
		t.Fatalf("after another target's override changed: %s", s)
	}
	// An embedded lorebook's edit does.
	if _, err := e.lib.Lorebooks.Update(e.ctx, lb.ID, lb.HeadRevisionID, library.Edit{Source: "ui",
		Data: json.RawMessage(`{"name": "Forge lore", "entries": []}`)}); err != nil {
		t.Fatal(err)
	}
	if s := e.status(e.card); s != "ahead" {
		t.Fatalf("after a lorebook edit: %s", s)
	}
}

func TestPushOnlyTarget(t *testing.T) {
	e := setup(t, map[string]any{"pull": false, "lorebooks": "none"})
	lb, _, err := e.lib.Lorebooks.Create(e.ctx, "forge-lore", "upload", library.Edit{Source: "ui",
		Data: json.RawMessage(`{"name": "Forge lore", "entries": []}`)})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.lib.LinkLorebook(e.ctx, e.card, lb.ID); err != nil {
		t.Fatal(err)
	}
	b := e.push(e.card)
	if b.Status != "cant_check" || b.RemoteState != "unknown" || b.CheckedAt != nil || b.LastPush.LorebookRevisionIDs != nil {
		t.Fatalf("push: %+v", b)
	}
	if _, ok := e.remoteData(b.RemoteID)["character_book"]; ok {
		t.Fatal("a target without lorebooks got one")
	}
	if b = e.check(b); b.Status != "cant_check" {
		t.Fatalf("check: %+v", b)
	}
	e.update(e.card, func(ed *library.Edit) { ed.Data = setField(ed.Data, "first_mes", "Well met.") })
	if s := e.status(e.card); s != "ahead" {
		t.Fatalf("after an edit: %s", s)
	}
}

func TestCreateOnlyTarget(t *testing.T) {
	e := setup(t, map[string]any{"update": false})
	e.push(e.card)
	if _, err := e.s.Push(e.ctx, e.inst, e.card, false); !errors.Is(err, ErrNewCopy) {
		t.Fatalf("second push: %v, want ErrNewCopy", err)
	}
	p, err := e.s.Preview(e.ctx, e.inst, e.card)
	if err != nil || !p.NewCopy {
		t.Fatalf("preview = %+v, %v", p, err)
	}
	b, err := e.s.Push(e.ctx, e.inst, e.card, true)
	if err != nil || b.RemoteID != "r2" {
		t.Fatalf("push with new copy: %+v, %v", b, err)
	}
	if pushes, _ := e.s.Pushes(e.ctx, b.ID); len(pushes) != 2 || pushes[1].RemoteID != "r1" {
		t.Fatalf("pushes = %+v", pushes)
	}
}

func TestPushRecreatesMissingCopy(t *testing.T) {
	e := setup(t, nil)
	b := e.push(e.card)
	gone := func() {
		if err := os.Remove(filepath.Join(e.dir, b.RemoteID+".json")); err != nil {
			t.Fatal(err)
		}
	}
	// Unchecked: the update fails, so the push makes a new copy.
	gone()
	if b = e.push(e.card); b.Status != "in_sync" {
		t.Fatalf("push after the app lost it: %+v", b)
	}
	e.remote(b.RemoteID)
	// Checked: the binding shows missing, and a push makes a new copy.
	gone()
	if b = e.check(b); b.Status != "missing" {
		t.Fatalf("check: %+v", b)
	}
	if b = e.push(e.card); b.Status != "in_sync" {
		t.Fatalf("push after missing: %+v", b)
	}
	e.remote(b.RemoteID)
}

func TestPushFailures(t *testing.T) {
	for mode, retryable := range map[string]bool{"retryable": true, "fatal": false} {
		t.Run(mode, func(t *testing.T) {
			e := setup(t, map[string]any{"fail_put": mode})
			_, err := e.s.Push(e.ctx, e.inst, e.card, false)
			if err == nil || plugins.Retryable(err) != retryable {
				t.Fatalf("push: %v, want retryable %v", err, retryable)
			}
			if list, _ := e.s.Bindings(e.ctx, e.card); len(list) != 0 {
				t.Fatalf("a failed push left bindings: %+v", list)
			}
		})
	}
}

func TestPreview(t *testing.T) {
	e := setup(t, nil)
	p, err := e.s.Preview(e.ctx, e.inst, e.card)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(p.DroppedFields, []string{"scenario"}) || p.NewCopy || !strings.Contains(string(p.Card), `"Brakka"`) {
		t.Fatalf("preview = %+v", p)
	}
	if _, err := e.s.Preview(e.ctx, e.inst, 999); !errors.Is(err, library.ErrNotFound) {
		t.Fatalf("missing card: %v", err)
	}
}

// A push holds one connection while it calls the plugin, so pushes must not
// need a second one: with a one-connection pool they would deadlock.
func TestConcurrentPushesNeedOneConnection(t *testing.T) {
	e := setup(t, nil)
	cfg := e.pool.Config()
	cfg.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(e.ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	s := New(pool, library.New(pool, &blobstore.Store{Name: "library", Root: t.TempDir()}), e.reg, e.s.plugins, quiet)
	cards := []int64{e.card, e.newCard("Carrow"), e.newCard("Dunmore")}
	ctx, cancel := context.WithTimeout(e.ctx, 20*time.Second)
	defer cancel()
	errs := make(chan error, len(cards))
	for _, c := range cards {
		go func() {
			_, err := s.Push(ctx, e.inst, c, false)
			errs <- err
		}()
	}
	for range cards {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
}
```

```go
package targets

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/davidgibbons/innkeeper/internal/library"
)

func TestCheck(t *testing.T) {
	e := setup(t, nil)
	b := e.push(e.card)
	// A change in whitespace only isn't a change.
	e.editRemote("r1", "description", "  A dwarf smith.\r\n")
	if b = e.check(b); b.Status != "in_sync" || b.RemoteState != "unchanged" || b.CheckedAt == nil {
		t.Fatalf("after a whitespace edit: %+v", b)
	}
	e.editRemote("r1", "first_mes", "Well met.")
	b = e.check(b)
	if b.Status != "remote_changed" || len(b.Changes) != 1 || b.Changes[0].Field != "first_mes" ||
		string(b.Changes[0].Before) != `"Hail."` || string(b.Changes[0].After) != `"Well met."` {
		t.Fatalf("after an app edit: %+v", b)
	}
	e.update(e.card, func(ed *library.Edit) { ed.Data = setField(ed.Data, "description", "A grumpy dwarf smith.") })
	if s := e.status(e.card); s != "diverged" {
		t.Fatalf("after both changed: %s", s)
	}
}

func TestScan(t *testing.T) {
	e := setup(t, nil)
	other := e.newCard("Carrow")
	e.push(e.card)
	e.push(other)
	e.editRemote("r1", "first_mes", "Well met.")
	if err := os.Remove(filepath.Join(e.dir, "r2.json")); err != nil {
		t.Fatal(err)
	}
	var reports int
	p, err := e.s.Scan(e.ctx, func(ScanProgress) { reports++ })
	if err != nil {
		t.Fatal(err)
	}
	if p.Checked != 2 || p.Changed != 1 || p.Missing != 1 || p.Failed != 0 || reports != 2 {
		t.Fatalf("scan = %+v after %d reports", p, reports)
	}
	if e.status(e.card) != "remote_changed" || e.status(other) != "missing" {
		t.Fatal("scan didn't record what it found")
	}
}
```

- [ ] **Step 3: Run them to make sure they fail**

Run: `go test ./internal/targets/`
Expected: FAIL to build, with `undefined: Service`.

- [ ] **Step 4: Write the service**

```go
// Package targets syncs library cards with target apps through push and
// pull plugins: bindings, pushes, drift checks, and adopting app changes.
package targets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/davidgibbons/innkeeper/internal/library"
	"github.com/davidgibbons/innkeeper/internal/pluginhost"
	"github.com/davidgibbons/innkeeper/internal/plugins"
	"github.com/davidgibbons/innkeeper/protocol"
)

var (
	ErrNotFound = errors.New("binding not found")
	// ErrCannot means the instance's plugin lacks the capability.
	ErrCannot = errors.New("plugin can't push")
	// ErrNewCopy means the target can't update its copy, so pushing again
	// makes another one. The caller must ask for that.
	ErrNewCopy = errors.New("this target can't update cards; push with new_copy to make another copy")
	// ErrNoChanges means there are no app changes to adopt.
	ErrNoChanges = errors.New("the app's copy has no changes to adopt")
	// ErrBadReply means the plugin replied with something the core can't use.
	ErrBadReply = errors.New("plugin sent a bad reply")
)

// Remote states: what the last check found in the app.
const (
	remoteUnknown   = "unknown"
	remoteUnchanged = "unchanged"
	remoteChanged   = "changed"
	remoteMissing   = "missing"
)

// Service pushes cards to targets and checks them for drift.
type Service struct {
	pool    *pgxpool.Pool
	lib     *library.Library
	reg     *plugins.Registry
	plugins *pluginhost.Manager
	log     *slog.Logger
}

// New returns a Service that runs target plugins with mgr.
func New(pool *pgxpool.Pool, lib *library.Library, reg *plugins.Registry, mgr *pluginhost.Manager, log *slog.Logger) *Service {
	return &Service{pool: pool, lib: lib, reg: reg, plugins: mgr, log: log}
}

// Push is one push of a card to a target.
type Push struct {
	ID         int64 `json:"id"`
	RevisionID int64 `json:"revision_id"`
	// LorebookRevisionIDs are the embedded lorebooks' revisions; absent when
	// the target doesn't embed lorebooks.
	LorebookRevisionIDs []int64   `json:"lorebook_revision_ids,omitempty"`
	PushedHash          string    `json:"pushed_hash"`
	RemoteID            string    `json:"remote_id"`
	PushedAt            time.Time `json:"pushed_at"`
}

// Binding is a card's link to its copy in one target app.
type Binding struct {
	ID         int64  `json:"id"`
	InstanceID int64  `json:"instance_id"`
	CardID     int64  `json:"card_id"`
	RemoteID   string `json:"remote_id"`
	Status     string `json:"status" enum:"in_sync,ahead,remote_changed,diverged,missing,cant_check"`
	// Ahead means the library changed since the last push.
	Ahead       bool       `json:"ahead"`
	RemoteState string     `json:"remote_state" enum:"unknown,unchanged,changed,missing"`
	CheckedAt   *time.Time `json:"checked_at,omitempty"`
	LastPush    Push       `json:"last_push"`
	// Changes are the fields the app changed since the last push or adopt.
	Changes   []Change  `json:"changes,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

const bindingQuery = `SELECT b.id, b.instance_id, b.card_id, b.remote_id, s.status, s.ahead, b.remote_state,
		b.checked_at, b.created_at, p.id, p.revision_id, p.lorebook_revision_ids, p.pushed_hash, p.remote_id,
		p.pushed_at, b.accepted_data, b.seen_data
	FROM binding b JOIN binding_status s ON s.binding_id = b.id
	JOIN LATERAL (SELECT * FROM push WHERE binding_id = b.id ORDER BY id DESC LIMIT 1) p ON TRUE`

func scanBinding(row pgx.Row) (Binding, error) {
	var b Binding
	var accepted, seen []byte
	p := &b.LastPush
	err := row.Scan(&b.ID, &b.InstanceID, &b.CardID, &b.RemoteID, &b.Status, &b.Ahead, &b.RemoteState,
		&b.CheckedAt, &b.CreatedAt, &p.ID, &p.RevisionID, &p.LorebookRevisionIDs, &p.PushedHash, &p.RemoteID,
		&p.PushedAt, &accepted, &seen)
	if errors.Is(err, pgx.ErrNoRows) {
		return b, ErrNotFound
	}
	if err != nil || seen == nil {
		return b, err
	}
	b.Changes, err = diff(accepted, seen)
	return b, err
}

// Binding returns one binding.
func (s *Service) Binding(ctx context.Context, id int64) (Binding, error) {
	return scanBinding(s.pool.QueryRow(ctx, bindingQuery+` WHERE b.id = $1`, id))
}

// Bindings returns a card's bindings, oldest first.
func (s *Service) Bindings(ctx context.Context, cardID int64) ([]Binding, error) {
	rows, err := s.pool.Query(ctx, bindingQuery+` WHERE b.card_id = $1 ORDER BY b.id`, cardID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Binding, error) { return scanBinding(row) })
}

// Pushes returns a binding's pushes, newest first.
func (s *Service) Pushes(ctx context.Context, bindingID int64) ([]Push, error) {
	if _, err := s.Binding(ctx, bindingID); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT id, revision_id, lorebook_revision_ids, pushed_hash, remote_id, pushed_at
		FROM push WHERE binding_id = $1 ORDER BY id DESC`, bindingID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Push, error) {
		var p Push
		err := row.Scan(&p.ID, &p.RevisionID, &p.LorebookRevisionIDs, &p.PushedHash, &p.RemoteID, &p.PushedAt)
		return p, err
	})
}

// Unbind forgets a binding and its pushes. The app keeps its copy.
func (s *Service) Unbind(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM binding WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// lock serializes pushes of one card to one target, so concurrent first
// pushes can't make two copies. It holds until tx ends.
func lock(ctx context.Context, tx pgx.Tx, instanceID, cardID int64) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
		fmt.Sprintf("binding %d card %d", instanceID, cardID))
	return err
}

// stored is a binding row as checks and adopts read it.
type stored struct {
	instanceID, cardID                  int64
	remoteID, remoteState, acceptedHash string
	accepted, seen                      []byte
}

func (s *Service) stored(ctx context.Context, id int64) (stored, error) {
	var b stored
	err := s.pool.QueryRow(ctx, `SELECT instance_id, card_id, remote_id, remote_state, accepted_hash, accepted_data,
		seen_data FROM binding WHERE id = $1`, id).
		Scan(&b.instanceID, &b.cardID, &b.remoteID, &b.remoteState, &b.acceptedHash, &b.accepted, &b.seen)
	if errors.Is(err, pgx.ErrNoRows) {
		return b, ErrNotFound
	}
	return b, err
}

// describe resolves a push plugin's instance and asks what its app stores.
func (s *Service) describe(ctx context.Context, instanceID int64) (pluginhost.Instance, protocol.TargetInfo, error) {
	var info protocol.TargetInfo
	inst, err := s.reg.Instance(ctx, instanceID)
	if err != nil {
		return inst, info, err
	}
	name := inst.Manifest.Name
	if !s.reg.HasCapability(name, "push") {
		return inst, info, fmt.Errorf("%w: %s", ErrCannot, name)
	}
	if err := s.reg.Call(ctx, s.plugins, inst, protocol.MethodTargetDescribe, nil, &info); err != nil {
		return inst, info, err
	}
	if err := info.Validate(); err != nil {
		return inst, info, fmt.Errorf("%w: target.describe: %w", ErrBadReply, err)
	}
	info.Pull = info.Pull && s.reg.HasCapability(name, "pull")
	return inst, info, nil
}

// rendered is a card as one target gets it.
type rendered struct {
	card     json.RawMessage
	data     json.RawMessage
	avatar   string
	revision int64
	// lorebooks is nil unless the target embeds lorebooks.
	lorebooks []int64
}

// render builds the card a target gets: the head revision's data with the
// instance's override applied, and linked lorebooks embedded if the target
// takes them that way.
//
// ponytail: a target with standalone lorebooks gets cards without them until
// target.put_lorebook exists.
func (s *Service) render(ctx context.Context, instanceID, cardID int64, info protocol.TargetInfo) (rendered, error) {
	c, err := s.lib.Cards.Get(ctx, cardID)
	if err != nil {
		return rendered{}, err
	}
	rev, err := s.lib.Cards.Revision(ctx, c.HeadRevisionID)
	if err != nil {
		return rendered{}, err
	}
	r := rendered{data: rev.Data, revision: rev.ID}
	overrides, err := object(rev.Overrides)
	if err != nil {
		return rendered{}, err
	}
	if patch, ok := overrides[strconv.FormatInt(instanceID, 10)]; ok {
		if r.data, err = MergePatch(r.data, patch); err != nil {
			return rendered{}, err
		}
	}
	if info.Lorebooks == protocol.LorebooksEmbedded {
		var book json.RawMessage
		if book, r.lorebooks, err = s.lib.CardBook(ctx, cardID); err != nil {
			return rendered{}, err
		}
		if book != nil {
			data, err := object(r.data)
			if err != nil {
				return rendered{}, err
			}
			data["character_book"] = book
			if r.data, err = json.Marshal(data); err != nil {
				return rendered{}, err
			}
		}
	}
	if rev.AvatarBlob != nil {
		if r.avatar, err = s.lib.BlobPath(*rev.AvatarBlob); err != nil {
			return rendered{}, err
		}
	}
	r.card, err = json.Marshal(map[string]any{"spec": "chara_card_v3", "spec_version": "3.0", "data": r.data})
	return r, err
}

// fetch reads the app's copy, limited to the fields it stores.
func (s *Service) fetch(ctx context.Context, inst pluginhost.Instance, info protocol.TargetInfo, remoteID string) (json.RawMessage, string, error) {
	var got protocol.TargetGetResult
	err := s.reg.Call(ctx, s.plugins, inst, protocol.MethodTargetGet, protocol.TargetGetParams{RemoteID: remoteID}, &got)
	if err != nil {
		return nil, "", err
	}
	data, err := cardData(got.Card)
	if err != nil {
		return nil, "", fmt.Errorf("%w: target.get: %w", ErrBadReply, err)
	}
	return project(data, info.Fields)
}

func remoteNotFound(err error) bool {
	var rpcErr *protocol.Error
	return errors.As(err, &rpcErr) && rpcErr.Code == protocol.CodeRemoteNotFound
}
```

- [ ] **Step 5: Write push and preview**

```go
package targets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/davidgibbons/innkeeper/internal/pluginhost"
	"github.com/davidgibbons/innkeeper/protocol"
)

// Push sends a card's head revision to a target and records the push. A
// card pushed there before is updated in place, which overwrites changes
// made in the app. A target that can't update gets another copy, but only
// when newCopy is set. If the app lost its copy, the push makes a new one.
func (s *Service) Push(ctx context.Context, instanceID, cardID int64, newCopy bool) (Binding, error) {
	inst, info, err := s.describe(ctx, instanceID)
	if err != nil {
		return Binding{}, err
	}
	r, err := s.render(ctx, instanceID, cardID, info)
	if err != nil {
		return Binding{}, err
	}
	// Everything from here to the commit uses only tx's connection, so
	// concurrent pushes can't use up the pool while holding the lock.
	//
	// ponytail: holds the transaction across the plugin calls; fine at one
	// owner's scale.
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Binding{}, err
	}
	defer tx.Rollback(ctx)
	if err := lock(ctx, tx, instanceID, cardID); err != nil {
		return Binding{}, err
	}
	var remoteID, state string
	err = tx.QueryRow(ctx, `SELECT remote_id, remote_state FROM binding WHERE instance_id = $1 AND card_id = $2`,
		instanceID, cardID).Scan(&remoteID, &state)
	bound := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Binding{}, err
	}
	put := protocol.TargetPutParams{Card: r.card, Avatar: r.avatar}
	switch {
	case !bound || state == remoteMissing:
	case info.Update:
		put.RemoteID = remoteID
	case !newCopy:
		return Binding{}, ErrNewCopy
	}
	var res protocol.TargetPutResult
	err = s.reg.Call(ctx, s.plugins, inst, protocol.MethodTargetPut, put, &res)
	if put.RemoteID != "" && remoteNotFound(err) {
		put.RemoteID = ""
		err = s.reg.Call(ctx, s.plugins, inst, protocol.MethodTargetPut, put, &res)
	}
	if err != nil {
		return Binding{}, err
	}
	if res.RemoteID == "" {
		return Binding{}, fmt.Errorf("%w: target.put returned no remote_id", ErrBadReply)
	}
	accepted, hash, remoteState, err := s.readBack(ctx, inst, info, res.RemoteID, r.data)
	if err != nil {
		return Binding{}, err
	}
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO binding (instance_id, card_id, remote_id, remote_state, accepted_data,
			accepted_hash, checked_at)
		VALUES ($1, $2, $3, $4, $5, $6, CASE WHEN $4 = 'unknown' THEN NULL ELSE now() END)
		ON CONFLICT (instance_id, card_id) DO UPDATE SET remote_id = EXCLUDED.remote_id,
			remote_state = EXCLUDED.remote_state, accepted_data = EXCLUDED.accepted_data,
			accepted_hash = EXCLUDED.accepted_hash, seen_data = NULL, checked_at = EXCLUDED.checked_at
		RETURNING id`, instanceID, cardID, res.RemoteID, remoteState, accepted, hash).Scan(&id)
	if err != nil {
		return Binding{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO push (binding_id, revision_id, lorebook_revision_ids, pushed_hash, remote_id)
		VALUES ($1, $2, $3, $4, $5)`, id, r.revision, r.lorebooks, hash, res.RemoteID)
	if err != nil {
		return Binding{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Binding{}, err
	}
	return s.Binding(ctx, id)
}

// readBack returns what the app holds after a push, its hash, and the
// remote state. A target that can pull is read back, so an app that
// reformats what it gets doesn't show as changed.
func (s *Service) readBack(ctx context.Context, inst pluginhost.Instance, info protocol.TargetInfo, remoteID string, sent json.RawMessage) (json.RawMessage, string, string, error) {
	if info.Pull {
		data, hash, err := s.fetch(ctx, inst, info, remoteID)
		if err == nil {
			return data, hash, remoteUnchanged, nil
		}
		// The push worked, so record it against what was sent instead.
		s.log.Warn("read back pushed card", "instance", inst.ID, "remote_id", remoteID, "err", err)
	}
	data, hash, err := project(sent, info.Fields)
	state := remoteUnknown
	if info.Pull {
		state = remoteUnchanged
	}
	return data, hash, state, err
}

// Preview is what a push would send, for the owner to confirm first.
type Preview struct {
	// Card is the CCv3 card the target would get.
	Card json.RawMessage `json:"card"`
	// DroppedFields are fields with content that the target doesn't store.
	DroppedFields []string `json:"dropped_fields"`
	// NewCopy means the push makes another copy, because the target can't
	// update the one it has.
	NewCopy bool `json:"new_copy"`
}

// Preview renders the card for a target without pushing it.
func (s *Service) Preview(ctx context.Context, instanceID, cardID int64) (Preview, error) {
	_, info, err := s.describe(ctx, instanceID)
	if err != nil {
		return Preview{}, err
	}
	r, err := s.render(ctx, instanceID, cardID, info)
	if err != nil {
		return Preview{}, err
	}
	dropped, err := droppedFields(r.data, info.Fields)
	if err != nil {
		return Preview{}, err
	}
	var state string
	err = s.pool.QueryRow(ctx, `SELECT remote_state FROM binding WHERE instance_id = $1 AND card_id = $2`,
		instanceID, cardID).Scan(&state)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Preview{}, err
	}
	return Preview{Card: r.card, DroppedFields: dropped, NewCopy: err == nil && state != remoteMissing && !info.Update}, nil
}
```

- [ ] **Step 6: Write check and scan**

```go
package targets

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Check reads a bound card back from its app and records whether the app's
// copy changed since the last push or adopt. If a push or adopt lands while
// it reads, Check records nothing and returns the binding as they left it.
func (s *Service) Check(ctx context.Context, id int64) (Binding, error) {
	b, err := s.stored(ctx, id)
	if err != nil {
		return Binding{}, err
	}
	inst, info, err := s.describe(ctx, b.instanceID)
	if err != nil {
		return Binding{}, err
	}
	state, seen := remoteUnknown, any(nil)
	if info.Pull {
		data, hash, err := s.fetch(ctx, inst, info, b.remoteID)
		switch {
		case remoteNotFound(err):
			state = remoteMissing
		case err != nil:
			return Binding{}, err
		case hash == b.acceptedHash:
			state = remoteUnchanged
		default:
			state, seen = remoteChanged, data
		}
	}
	_, err = s.pool.Exec(ctx, `UPDATE binding SET remote_state = $3, seen_data = $4,
		checked_at = CASE WHEN $3 = 'unknown' THEN NULL ELSE now() END
		WHERE id = $1 AND accepted_hash = $2`, id, b.acceptedHash, state, seen)
	if err != nil {
		return Binding{}, err
	}
	return s.Binding(ctx, id)
}

// ScanProgress counts what a scan found.
type ScanProgress struct {
	Checked int      `json:"checked"`
	Changed int      `json:"changed"`
	Missing int      `json:"missing"`
	Failed  int      `json:"failed"`
	Errors  []string `json:"errors,omitempty"`
}

// maxErrors is how many check errors a scan's progress keeps.
const maxErrors = 20

// Scan checks every binding on a target that can pull, except on broken
// instances. A binding that fails to check is counted, and the scan goes on.
// report gets the progress after each binding.
func (s *Service) Scan(ctx context.Context, report func(ScanProgress)) (ScanProgress, error) {
	var p ScanProgress
	rows, err := s.pool.Query(ctx, `SELECT b.id FROM binding b JOIN plugin_instance i ON i.id = b.instance_id
		WHERE b.remote_state <> 'unknown' AND i.broken_error IS NULL ORDER BY b.id`)
	if err != nil {
		return p, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return p, err
	}
	for _, id := range ids {
		b, err := s.Check(ctx, id)
		switch {
		case ctx.Err() != nil:
			return p, ctx.Err()
		case errors.Is(err, ErrNotFound):
			continue
		case err != nil:
			p.Failed++
			if len(p.Errors) < maxErrors {
				p.Errors = append(p.Errors, fmt.Sprintf("binding %d: %v", id, err))
			}
		default:
			p.Checked++
			switch b.RemoteState {
			case remoteChanged:
				p.Changed++
			case remoteMissing:
				p.Missing++
			}
		}
		report(p)
	}
	return p, nil
}
```

- [ ] **Step 7: Run the tests**

Run: `go test -race ./internal/targets/`
Expected: PASS.

To see that `TestConcurrentPushesNeedOneConnection` guards the lock, move the `s.render` call in `Push` to after `lock` and run it again. It fails with `context deadline exceeded`. Move it back.

- [ ] **Step 8: Commit**

```bash
git add internal/targets
git commit -m "Push cards to targets and check them for changes made in the app"
```

### Task 7: Adopt

**Files:**
- Create: `internal/targets/adopt.go`
- Test: `internal/targets/adopt_test.go`

- [ ] **Step 1: Write the failing tests**

```go
package targets

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/davidgibbons/innkeeper/internal/library"
)

// changed pushes the card, then changes first_mes and clears description in
// the app, and checks.
func changed(t *testing.T) (*env, Binding) {
	e := setup(t, nil)
	b := e.push(e.card)
	e.editRemote("r1", "first_mes", "Well met.")
	e.editRemote("r1", "description", nil)
	return e, e.check(b)
}

func TestAdoptIntoBase(t *testing.T) {
	e, b := changed(t)
	b, err := e.s.Adopt(e.ctx, b.ID, IntoBase, e.head(e.card).ID)
	if err != nil {
		t.Fatal(err)
	}
	if b.Status != "ahead" || b.RemoteState != "unchanged" || b.Changes != nil {
		t.Fatalf("binding = %+v", b)
	}
	rev := e.head(e.card)
	var data map[string]any
	_ = json.Unmarshal(rev.Data, &data)
	if data["first_mes"] != "Well met." || data["description"] != nil || data["scenario"] != "A forge." || rev.Source != "adopt" {
		t.Fatalf("head = %s from %s", rev.Data, rev.Source)
	}
	if _, err := e.s.Adopt(e.ctx, b.ID, IntoBase, rev.ID); !errors.Is(err, ErrNoChanges) {
		t.Fatalf("second adopt: %v", err)
	}
}

func TestAdoptIntoOverride(t *testing.T) {
	e, b := changed(t)
	before := e.head(e.card)
	if _, err := e.s.Adopt(e.ctx, b.ID, IntoOverride, before.ID); err != nil {
		t.Fatal(err)
	}
	rev := e.head(e.card)
	var overrides map[string]map[string]any
	_ = json.Unmarshal(rev.Overrides, &overrides)
	patch, hasDescription := overrides[e.key()]["description"]
	if string(rev.Data) != string(before.Data) || overrides[e.key()]["first_mes"] != "Well met." || !hasDescription || patch != nil {
		t.Fatalf("head data %s, overrides %s", rev.Data, rev.Overrides)
	}
	// The next push sends the override, so the app keeps its version.
	b = e.push(e.card)
	if got := e.remoteData(b.RemoteID); b.Status != "in_sync" || got["first_mes"] != "Well met." || got["description"] != nil {
		t.Fatalf("after push: %+v, app has %v", b, got)
	}
}

func TestAdoptIgnore(t *testing.T) {
	e, b := changed(t)
	b, err := e.s.Adopt(e.ctx, b.ID, IntoIgnore, 0)
	if err != nil || b.Status != "in_sync" {
		t.Fatalf("ignore: %+v, %v", b, err)
	}
	if hist, _ := e.lib.Cards.History(e.ctx, e.card); len(hist) != 1 {
		t.Fatalf("ignore added %d revisions", len(hist)-1)
	}
}

func TestAdoptStaleBase(t *testing.T) {
	e, b := changed(t)
	stale := e.head(e.card).ID
	e.update(e.card, func(ed *library.Edit) { ed.Data = setField(ed.Data, "scenario", "A cold forge.") })
	if _, err := e.s.Adopt(e.ctx, b.ID, IntoBase, stale); !errors.Is(err, library.ErrConflict) {
		t.Fatalf("adopt on a stale base: %v", err)
	}
	if s := e.status(e.card); s != "diverged" {
		t.Fatalf("a failed adopt changed the status to %s", s)
	}
	if _, err := e.s.Adopt(e.ctx, b.ID, "elsewhere", stale); !errors.Is(err, library.ErrInvalid) {
		t.Fatalf("bad into: %v", err)
	}
}
```

- [ ] **Step 2: Run them to make sure they fail**

Run: `go test ./internal/targets/ -run TestAdopt`
Expected: FAIL to build, with `undefined: IntoBase`.

- [ ] **Step 3: Write adopt**

```go
package targets

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/davidgibbons/innkeeper/internal/cardhash"
	"github.com/davidgibbons/innkeeper/internal/library"
)

// Where Adopt puts the app's changes.
const (
	// IntoBase writes them into the card's own data.
	IntoBase = "base"
	// IntoOverride writes them into this target's override.
	IntoOverride = "override"
	// IntoIgnore leaves the library as it is and stops reporting them.
	IntoIgnore = "ignore"
)

// ErrMoved means a push or check changed the binding while Adopt ran.
var ErrMoved = errors.New("the binding changed while adopting; look at it again")

// Adopt takes the app's changes into the library and accepts the app's copy
// as the new baseline for checks. Unless into is IntoIgnore, it adds a
// revision on top of base, so a stale base returns library.ErrConflict.
// Changes to character_book are not adopted; edit the lorebooks instead. To
// discard the app's changes, push: a push overwrites them.
func (s *Service) Adopt(ctx context.Context, id int64, into string, base int64) (Binding, error) {
	if into != IntoBase && into != IntoOverride && into != IntoIgnore {
		return Binding{}, fmt.Errorf("%w: into must be base, override, or ignore", library.ErrInvalid)
	}
	b, err := s.stored(ctx, id)
	if err != nil {
		return Binding{}, err
	}
	if b.remoteState != remoteChanged {
		return Binding{}, ErrNoChanges
	}
	changes, err := diff(b.accepted, b.seen)
	if err != nil {
		return Binding{}, err
	}
	// ponytail: the revision commits before the binding does. If a push or
	// check lands in between, the revision stays and Adopt returns ErrMoved.
	if into != IntoIgnore {
		if err := s.adopt(ctx, b.instanceID, b.cardID, base, into, changes); err != nil {
			return Binding{}, err
		}
	}
	hash, err := cardhash.ContentHash(b.seen)
	if err != nil {
		return Binding{}, err
	}
	tag, err := s.pool.Exec(ctx, `UPDATE binding SET accepted_data = seen_data, accepted_hash = $3, seen_data = NULL,
			remote_state = 'unchanged'
		WHERE id = $1 AND remote_state = 'changed' AND accepted_hash = $2 AND seen_data = $4`,
		id, b.acceptedHash, hash, b.seen)
	if err != nil {
		return Binding{}, err
	}
	if tag.RowsAffected() == 0 {
		return Binding{}, ErrMoved
	}
	return s.Binding(ctx, id)
}

// adopt writes changes into the card's data or the instance's override, in a
// revision on top of base.
func (s *Service) adopt(ctx context.Context, instanceID, cardID, base int64, into string, changes []Change) error {
	rev, err := s.lib.Cards.Revision(ctx, base)
	if errors.Is(err, library.ErrNotFound) || err == nil && rev.EntityID != cardID {
		return library.ErrConflict
	}
	if err != nil {
		return err
	}
	inst, err := s.reg.GetInstance(ctx, instanceID)
	if err != nil {
		return err
	}
	e := library.Edit{Data: rev.Data, Overrides: rev.Overrides, Source: "adopt", Message: "Adopt changes from " + inst.Name}
	if rev.AvatarBlob != nil {
		e.AvatarBlob = *rev.AvatarBlob
	}
	var changed bool
	if into == IntoBase {
		e.Data, changed, err = applyChanges(rev.Data, changes)
	} else {
		e.Overrides, changed, err = overrideChanges(rev.Overrides, strconv.FormatInt(instanceID, 10), changes)
	}
	if err != nil || !changed {
		return err
	}
	_, err = s.lib.Cards.Update(ctx, cardID, base, e)
	return err
}
```

- [ ] **Step 4: Run the tests**

Run: `go test -race ./internal/targets/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/targets
git commit -m "Adopt app changes into a card or its target override"
```

### Task 8: The `target_state` search filter

**Files:**
- Modify: `internal/library/search.go`
- Test: `internal/library/search_test.go`, `internal/targets/filter_test.go`

- [ ] **Step 1: Write the failing tests**

```diff
diff --git a/internal/library/search_test.go b/internal/library/search_test.go
index 0773cd6..1628128 100644
--- a/internal/library/search_test.go
+++ b/internal/library/search_test.go
@@ -151,15 +151,16 @@ func TestSearchRejectsBadQueries(t *testing.T) {
 	lib := newLibrary(t)
 	ctx := context.Background()
 	for name, q := range map[string]Query{
-		"two conditions": {Filter: &Filter{Tag: ptr("a"), Archived: ptr(true)}},
-		"no condition":   {Filter: &Filter{Not: &Filter{}}},
-		"semantic":       {Semantic: true},
-		"catalog source": {Sources: []string{"catalog:chub-archive"}},
-		"bad include":    {Include: []string{"enrichments"}},
-		"negative size":  {Page: Page{Size: -1}},
-		"huge size":      {Page: Page{Size: 501}},
-		"huge filter":    {Filter: &Filter{Or: manyTags(201)}},
-		"bad cursor":     {Page: Page{Cursor: ptr("abc")}},
+		"two conditions":   {Filter: &Filter{Tag: ptr("a"), Archived: ptr(true)}},
+		"no condition":     {Filter: &Filter{Not: &Filter{}}},
+		"semantic":         {Semantic: true},
+		"catalog source":   {Sources: []string{"catalog:chub-archive"}},
+		"bad include":      {Include: []string{"enrichments"}},
+		"negative size":    {Page: Page{Size: -1}},
+		"huge size":        {Page: Page{Size: 501}},
+		"huge filter":      {Filter: &Filter{Or: manyTags(201)}},
+		"bad cursor":       {Page: Page{Cursor: ptr("abc")}},
+		"bad target state": {Filter: &Filter{TargetState: &TargetState{Instance: 1, Is: "pushed"}}},
 	} {
 		if _, err := lib.Search(ctx, q); !errors.Is(err, ErrInvalid) {
 			t.Errorf("%s: got %v, want ErrInvalid", name, err)
```

```go
package targets

import (
	"slices"
	"testing"

	"github.com/davidgibbons/innkeeper/internal/library"
)

func TestFilterTargetState(t *testing.T) {
	e := setup(t, nil)
	edited, unpushed := e.newCard("Carrow"), e.newCard("Dunmore")
	e.push(e.card)
	e.push(edited)
	e.update(edited, func(ed *library.Edit) { ed.Data = setField(ed.Data, "first_mes", "Well met.") })

	search := func(f library.Filter) []int64 {
		t.Helper()
		ids, err := e.lib.SearchIDs(e.ctx, library.Query{Filter: &f})
		if err != nil {
			t.Fatal(err)
		}
		return ids
	}
	state := func(is string) library.Filter {
		return library.Filter{TargetState: &library.TargetState{Instance: e.inst, Is: is}}
	}
	if got := search(state("ahead")); !slices.Equal(got, []int64{edited}) {
		t.Fatalf("ahead = %v", got)
	}
	if got := search(state("in_sync")); !slices.Equal(got, []int64{e.card}) {
		t.Fatalf("in_sync = %v", got)
	}
	notInSync := state("in_sync")
	if got := search(library.Filter{Not: &notInSync}); !slices.Equal(got, []int64{edited, unpushed}) {
		t.Fatalf("not in_sync = %v", got)
	}
}
```

- [ ] **Step 2: Run them to make sure they fail**

Run: `go test ./internal/library/ ./internal/targets/`
Expected: FAIL to build, with `unknown field TargetState`.

- [ ] **Step 3: Add the filter**

```diff
diff --git a/internal/library/search.go b/internal/library/search.go
index 271953b..f1e0b42 100644
--- a/internal/library/search.go
+++ b/internal/library/search.go
@@ -33,14 +33,24 @@ type Page struct {
 // Filter is one condition on a card. Set exactly one field. Archived cards
 // are left out unless a filter mentions archived.
 type Filter struct {
-	And        []Filter `json:"and,omitempty"`
-	Or         []Filter `json:"or,omitempty"`
-	Not        *Filter  `json:"not,omitempty"`
-	Tag        *string  `json:"tag,omitempty" doc:"Has this tag, in any case."`
-	Collection *int64   `json:"collection,omitempty" doc:"Is in this collection."`
-	Archived   *bool    `json:"archived,omitempty"`
+	And         []Filter     `json:"and,omitempty"`
+	Or          []Filter     `json:"or,omitempty"`
+	Not         *Filter      `json:"not,omitempty"`
+	Tag         *string      `json:"tag,omitempty" doc:"Has this tag, in any case."`
+	Collection  *int64       `json:"collection,omitempty" doc:"Is in this collection."`
+	Archived    *bool        `json:"archived,omitempty"`
+	TargetState *TargetState `json:"target_state,omitempty" doc:"Is pushed to this target and stands this way. A card never pushed there matches no state."`
 }
 
+// TargetState is a card's status on one target, as GET /cards/{id}/bindings
+// shows it.
+type TargetState struct {
+	Instance int64  `json:"instance" doc:"Plugin instance ID"`
+	Is       string `json:"is" enum:"in_sync,ahead,remote_changed,diverged,missing,cant_check"`
+}
+
+var targetStatuses = []string{"in_sync", "ahead", "remote_changed", "diverged", "missing", "cant_check"}
+
 // Hit is one matching card.
 type Hit struct {
 	ID         int64          `json:"id"`
@@ -75,7 +85,7 @@ func (w *where) filter(f Filter) (string, error) {
 		return "", fmt.Errorf("%w: filter has more than 200 conditions", ErrInvalid)
 	}
 	set := 0
-	for _, ok := range []bool{f.And != nil, f.Or != nil, f.Not != nil, f.Tag != nil, f.Collection != nil, f.Archived != nil} {
+	for _, ok := range []bool{f.And != nil, f.Or != nil, f.Not != nil, f.Tag != nil, f.Collection != nil, f.Archived != nil, f.TargetState != nil} {
 		if ok {
 			set++
 		}
@@ -97,6 +107,12 @@ func (w *where) filter(f Filter) (string, error) {
 	case f.Collection != nil:
 		return `EXISTS (SELECT 1 FROM collection_card cc
 			WHERE cc.card_id = c.id AND cc.collection_id = ` + w.arg(*f.Collection) + `)`, nil
+	case f.TargetState != nil:
+		if !slices.Contains(targetStatuses, f.TargetState.Is) {
+			return "", fmt.Errorf("%w: target_state.is must be one of %v", ErrInvalid, targetStatuses)
+		}
+		return `EXISTS (SELECT 1 FROM binding_status bs WHERE bs.card_id = c.id
+			AND bs.instance_id = ` + w.arg(f.TargetState.Instance) + ` AND bs.status = ` + w.arg(f.TargetState.Is) + `)`, nil
 	}
 	return `((c.archived_at IS NOT NULL) = ` + w.arg(*f.Archived) + `)`, nil
 }
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/library/ ./internal/targets/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/library/search.go internal/library/search_test.go internal/targets/filter_test.go
git commit -m "Filter searches by a card's status on a target"
```

### Task 9: Push and scan jobs

**Files:**
- Create: `internal/targets/jobs.go`
- Modify: `cmd/innkeeper/worker.go`
- Test: `internal/targets/jobs_test.go`

- [ ] **Step 1: Write the failing test**

```go
package targets

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/davidgibbons/innkeeper/internal/jobs"
)

func TestJobs(t *testing.T) {
	e := setup(t, nil)
	other := e.newCard("Carrow")
	q := jobs.New(e.pool)
	w := &jobs.Worker{Queue: q, Log: quiet, Poll: 50 * time.Millisecond,
		Handlers: map[string]jobs.Handler{PushKind: e.s.RunPush, ScanKind: e.s.RunScan}}
	ctx, cancel := context.WithCancel(e.ctx)
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	defer func() { cancel(); <-done }()
	wait := func(id int64) jobs.Job {
		t.Helper()
		for deadline := time.Now().Add(20 * time.Second); ; time.Sleep(50 * time.Millisecond) {
			j, err := q.Get(e.ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if j.Status == jobs.StatusSucceeded || j.Status == jobs.StatusFailed {
				return j
			}
			if time.Now().After(deadline) {
				t.Fatalf("job %d still %s", id, j.Status)
			}
		}
	}

	parent, err := EnqueueBulkPush(e.ctx, q, e.inst, []int64{e.card, other}, false)
	if err != nil {
		t.Fatal(err)
	}
	if j := wait(parent.ID); j.Status != jobs.StatusSucceeded {
		t.Fatalf("bulk push %s: %v", j.Status, j.Error)
	}
	if e.status(e.card) != "in_sync" || e.status(other) != "in_sync" {
		t.Fatal("bulk push didn't push both cards")
	}

	e.editRemote("r1", "first_mes", "Well met.")
	scan, err := q.Enqueue(e.ctx, jobs.Spec{Kind: ScanKind})
	if err != nil {
		t.Fatal(err)
	}
	j := wait(scan.ID)
	var p ScanProgress
	_ = json.Unmarshal(j.Progress, &p)
	if j.Status != jobs.StatusSucceeded || p.Checked != 2 || p.Changed != 1 {
		t.Fatalf("scan %s, progress %s", j.Status, j.Progress)
	}
}
```

- [ ] **Step 2: Run it to make sure it fails**

Run: `go test ./internal/targets/ -run TestJobs`
Expected: FAIL to build, with `undefined: PushKind`.

- [ ] **Step 3: Add the job kinds**

```go
package targets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/davidgibbons/innkeeper/internal/bulk"
	"github.com/davidgibbons/innkeeper/internal/jobs"
	"github.com/davidgibbons/innkeeper/internal/plugins"
)

// Job kinds. A push job's instance is the target; a scan has none.
const (
	PushKind = "target.push"
	ScanKind = "target.scan"
)

type pushPayload struct {
	CardID  int64 `json:"card_id"`
	NewCopy bool  `json:"new_copy,omitempty"`
}

// EnqueuePush queues a push of one card.
func EnqueuePush(ctx context.Context, q *jobs.Queue, instanceID, cardID int64, newCopy bool) (jobs.Job, error) {
	return q.Enqueue(ctx, jobs.Spec{Kind: PushKind, InstanceID: &instanceID, Payload: pushPayload{cardID, newCopy}})
}

// EnqueueBulkPush queues a push of each card, as one bulk job.
func EnqueueBulkPush(ctx context.Context, q *jobs.Queue, instanceID int64, cardIDs []int64, newCopy bool) (jobs.Job, error) {
	children := make([]jobs.Spec, len(cardIDs))
	for i, id := range cardIDs {
		children[i] = jobs.Spec{Kind: PushKind, InstanceID: &instanceID, Payload: pushPayload{id, newCopy}}
	}
	return q.EnqueueBulk(ctx, jobs.Spec{Kind: bulk.Kind, InstanceID: &instanceID,
		Payload: map[string]any{"action": PushKind, "new_copy": newCopy}}, children)
}

// RunPush is the jobs.Handler for PushKind.
func (s *Service) RunPush(ctx context.Context, r *jobs.Run) error {
	if r.InstanceID == nil {
		return errors.New("push job has no plugin instance")
	}
	var p pushPayload
	if err := json.Unmarshal(r.Payload, &p); err != nil {
		return fmt.Errorf("push payload: %w", err)
	}
	b, err := s.Push(ctx, *r.InstanceID, p.CardID, p.NewCopy)
	if plugins.Retryable(err) {
		return fmt.Errorf("%w: %w", jobs.ErrRetry, err)
	}
	if err != nil {
		return err
	}
	raw, err := json.Marshal(map[string]any{"binding_id": b.ID, "remote_id": b.RemoteID})
	r.SetProgress(raw)
	return err
}

// RunScan is the jobs.Handler for ScanKind.
func (s *Service) RunScan(ctx context.Context, r *jobs.Run) error {
	_, err := s.Scan(ctx, func(p ScanProgress) {
		raw, _ := json.Marshal(p)
		r.SetProgress(raw)
	})
	return err
}
```

- [ ] **Step 4: Run the worker's handlers and hourly scan**

```diff
diff --git a/cmd/innkeeper/worker.go b/cmd/innkeeper/worker.go
index 17f22ef..37e6ae8 100644
--- a/cmd/innkeeper/worker.go
+++ b/cmd/innkeeper/worker.go
@@ -13,6 +13,7 @@ import (
 	"github.com/davidgibbons/innkeeper/internal/importer"
 	"github.com/davidgibbons/innkeeper/internal/jobs"
 	"github.com/davidgibbons/innkeeper/internal/pluginhost"
+	"github.com/davidgibbons/innkeeper/internal/targets"
 )
 
 const (
@@ -46,6 +47,7 @@ func worker() error {
 	im.Plugins = mgr
 	go stopIdlePlugins(ctx, mgr)
 
+	tg := targets.New(svc.pool, svc.lib, svc.registry, mgr, log)
 	q := jobs.New(svc.pool)
 	w := &jobs.Worker{
 		Queue:       q,
@@ -54,6 +56,8 @@ func worker() error {
 		Handlers: map[string]jobs.Handler{
 			importer.Kind:    im.Run,
 			bulk.ArchiveKind: bulk.Archive(svc.lib),
+			targets.PushKind: tg.RunPush,
+			targets.ScanKind: tg.RunScan,
 			blobGCKind: func(ctx context.Context, r *jobs.Run) error {
 				blobs, leftovers, err := svc.lib.CollectGarbage(ctx, 24*time.Hour)
 				r.SetProgress(fmt.Appendf(nil, `{"blobs": %d, "leftovers": %d}`, blobs, leftovers))
@@ -68,6 +72,7 @@ func worker() error {
 		Schedules: []jobs.Schedule{
 			{Kind: blobGCKind, Every: 24 * time.Hour},
 			{Kind: jobPruneKind, Every: 24 * time.Hour},
+			{Kind: targets.ScanKind, Every: time.Hour},
 		},
 	}
 	log.Info("worker started", "plugins", len(svc.manifests), "concurrency", concurrency)
```

- [ ] **Step 5: Run the tests**

Run: `go build ./... && go test -race ./internal/targets/`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/targets/jobs.go internal/targets/jobs_test.go cmd/innkeeper/worker.go
git commit -m "Run pushes and an hourly drift scan as jobs"
```

### Task 10: Sync API

**Files:**
- Create: `internal/api/targets.go`
- Modify: `internal/api/server.go`, `internal/api/jobs.go`, `cmd/innkeeper/api.go`
- Test: `internal/api/api_test.go`, `internal/api/targets_test.go`

- [ ] **Step 1: Write the failing tests**

The test environment's fake plugin gains `push` and `pull`. The new target instance is made per test, so the instance list in `TestInstanceRoutes` keeps its length.

```diff
diff --git a/internal/api/api_test.go b/internal/api/api_test.go
index 55a04bf..55e2fb5 100644
--- a/internal/api/api_test.go
+++ b/internal/api/api_test.go
@@ -22,6 +22,7 @@ import (
 	"github.com/davidgibbons/innkeeper/internal/pluginhost"
 	"github.com/davidgibbons/innkeeper/internal/plugins"
 	"github.com/davidgibbons/innkeeper/internal/secrets"
+	"github.com/davidgibbons/innkeeper/internal/targets"
 	"github.com/davidgibbons/innkeeper/internal/testdb"
 	"github.com/davidgibbons/innkeeper/internal/testutil"
 	"github.com/davidgibbons/innkeeper/protocol"
@@ -54,7 +55,7 @@ func newEnv(t *testing.T) *testEnv {
 	if err != nil {
 		t.Fatal(err)
 	}
-	fake, err := protocol.LoadManifest(testutil.FakePluginWith(t, []string{"auth", "import"}))
+	fake, err := protocol.LoadManifest(testutil.FakePluginWith(t, []string{"auth", "import", "push", "pull"}))
 	if err != nil {
 		t.Fatal(err)
 	}
@@ -63,8 +64,9 @@ func newEnv(t *testing.T) *testEnv {
 	reg := plugins.New(pool, map[string]*protocol.Manifest{"fake": fake, "vault": vault}, store, box)
 	mgr := pluginhost.NewManager(NoCore, time.Minute, quiet)
 	t.Cleanup(mgr.Close)
-	e := &testEnv{t: t, pool: pool, s: &Server{Lib: library.New(pool, store), Queue: jobs.New(pool),
-		Registry: reg, Auth: auth.New(pool), Plugins: mgr, Log: quiet}}
+	lib := library.New(pool, store)
+	e := &testEnv{t: t, pool: pool, s: &Server{Lib: lib, Queue: jobs.New(pool), Registry: reg, Auth: auth.New(pool),
+		Targets: targets.New(pool, lib, reg, mgr, quiet), Plugins: mgr, Log: quiet}}
 	e.srv = httptest.NewServer(e.s.Handler())
 	t.Cleanup(e.srv.Close)
 	if _, e.token, err = e.s.Auth.CreateToken(ctx, "test"); err != nil {
```

```go
package api

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/davidgibbons/innkeeper/internal/bulk"
	"github.com/davidgibbons/innkeeper/internal/plugins"
	"github.com/davidgibbons/innkeeper/internal/targets"
)

// target configures a fake target app that keeps its cards in a temp dir,
// and returns the instance and the dir.
func (e *testEnv) target() (int64, string) {
	e.t.Helper()
	dir := e.t.TempDir()
	id, err := e.s.Registry.CreateInstance(context.Background(), plugins.NewInstance{Plugin: "fake", Name: "Target",
		Config: map[string]any{"dir": dir}})
	if err != nil {
		e.t.Fatal(err)
	}
	return id, dir
}

func TestPushRoutes(t *testing.T) {
	e := newEnv(t)
	inst, dir := e.target()
	var c entityOut
	e.call("POST", "/cards", map[string]any{"data": map[string]any{"name": "Brakka", "first_mes": "Hail.", "scenario": "A forge."}}, 201, &c)
	path := fmt.Sprintf("/cards/%d", c.ID)

	var p targets.Preview
	e.call("GET", fmt.Sprintf("%s/push-preview?instance_id=%d", path, inst), nil, 200, &p)
	if len(p.DroppedFields) != 1 || p.DroppedFields[0] != "scenario" || p.NewCopy {
		t.Fatalf("preview = %+v", p)
	}
	e.call("GET", path+"/push-preview", nil, 422, nil)

	var j jobOut
	e.call("POST", path+"/push", map[string]any{"instance_id": inst}, 202, &j)
	if j.Kind != targets.PushKind || *j.InstanceID != inst {
		t.Fatalf("push job = %+v", j)
	}
	e.call("POST", path+"/push", map[string]any{"instance_id": e.importID + 1000}, 404, nil)
	e.call("POST", "/cards/999/push", map[string]any{"instance_id": inst}, 404, nil)
	e.markBroken(e.importID, "boom")
	e.call("POST", path+"/push", map[string]any{"instance_id": e.importID}, 409, nil)

	// No worker runs here, so push directly.
	if _, err := e.s.Targets.Push(context.Background(), inst, c.ID, false); err != nil {
		t.Fatal(err)
	}
	var list []targets.Binding
	e.call("GET", path+"/bindings", nil, 200, &list)
	if len(list) != 1 || list[0].Status != "in_sync" || list[0].RemoteID != "r1" {
		t.Fatalf("bindings = %+v", list)
	}
	bpath := fmt.Sprintf("/bindings/%d", list[0].ID)

	// The owner changes the card in the app.
	if err := os.WriteFile(filepath.Join(dir, "r1.json"), []byte(`{"spec": "chara_card_v3", "data": {"name": "Brakka", "first_mes": "Well met."}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var b targets.Binding
	e.call("POST", bpath+"/check", nil, 200, &b)
	if b.Status != "remote_changed" || len(b.Changes) != 1 {
		t.Fatalf("check = %+v", b)
	}
	e.call("POST", bpath+"/adopt", map[string]any{"into": "base", "base_revision_id": c.Revision.ID + 100}, 409, nil)
	e.call("POST", bpath+"/adopt", map[string]any{"into": "base", "base_revision_id": c.Revision.ID}, 200, &b)
	if b.Status != "ahead" {
		t.Fatalf("adopt = %+v", b)
	}
	e.call("POST", bpath+"/adopt", map[string]any{"into": "ignore"}, 409, nil)
	e.call("GET", path, nil, 200, &c)
	if c.Revision.Source != "adopt" {
		t.Fatalf("card after adopt = %+v", c.Revision)
	}

	var pushes []targets.Push
	e.call("GET", bpath+"/pushes", nil, 200, &pushes)
	if len(pushes) != 1 {
		t.Fatalf("pushes = %+v", pushes)
	}
	e.call("DELETE", fmt.Sprintf("/instances/%d", inst), nil, 409, nil)
	e.call("DELETE", bpath, nil, 204, nil)
	e.call("GET", bpath, nil, 404, nil)
	e.call("DELETE", fmt.Sprintf("/instances/%d", inst), nil, 204, nil)
}

func TestBulkPush(t *testing.T) {
	e := newEnv(t)
	inst, _ := e.target()
	for _, name := range []string{"Brakka", "Carrow"} {
		e.call("POST", "/cards", map[string]any{"data": map[string]any{"name": name}}, 201, nil)
	}
	e.call("POST", "/pushes", map[string]any{"instance_id": inst, "query": map[string]any{}}, 400, nil)
	var j jobOut
	e.call("POST", "/pushes", map[string]any{"instance_id": inst, "query": map[string]any{"text": "carrow"}}, 202, &j)
	if j.Kind != bulk.Kind || j.Children["queued"] != 1 {
		t.Fatalf("bulk push = %+v", j)
	}
}
```

- [ ] **Step 2: Run them to make sure they fail**

Run: `go test ./internal/api/`
Expected: FAIL to build, with `unknown field Targets in struct literal`.

- [ ] **Step 3: Map the new errors and add the service**

```diff
diff --git a/internal/api/server.go b/internal/api/server.go
index 083e82b..1c027de 100644
--- a/internal/api/server.go
+++ b/internal/api/server.go
@@ -17,6 +17,7 @@ import (
 	"github.com/davidgibbons/innkeeper/internal/library"
 	"github.com/davidgibbons/innkeeper/internal/pluginhost"
 	"github.com/davidgibbons/innkeeper/internal/plugins"
+	"github.com/davidgibbons/innkeeper/internal/targets"
 	"github.com/davidgibbons/innkeeper/protocol"
 )
 
@@ -26,6 +27,8 @@ type Server struct {
 	Queue    *jobs.Queue
 	Registry *plugins.Registry
 	Auth     *auth.Auth
+	// Targets must use Plugins.
+	Targets *targets.Service
 	// Plugins runs plugins for interactive calls, such as logins. Its core
 	// handler should be NoCore.
 	Plugins *pluginhost.Manager
@@ -85,6 +88,7 @@ func (s *Server) Handler() http.Handler {
 	s.searchRoutes(api)
 	s.instanceRoutes(api)
 	s.jobRoutes(api)
+	s.targetRoutes(api)
 	// Browsers arrive here from a login provider; it isn't part of the API.
 	mux.HandleFunc("GET /auth/{id}/callback", s.callback)
 	return mux
@@ -94,19 +98,31 @@ func (s *Server) Handler() http.Handler {
 // hidden from the client. A nil error stays nil.
 func (s *Server) fail(err error) error {
 	var se huma.StatusError
+	var rpcErr *protocol.Error
+	var startErr *pluginhost.StartError
 	switch {
 	case err == nil:
 		return nil
 	case errors.As(err, &se):
 		return se
 	case errors.Is(err, library.ErrNotFound), errors.Is(err, jobs.ErrNotFound),
-		errors.Is(err, plugins.ErrNotFound), errors.Is(err, auth.ErrNotFound):
+		errors.Is(err, plugins.ErrNotFound), errors.Is(err, auth.ErrNotFound), errors.Is(err, targets.ErrNotFound):
 		return huma.Error404NotFound("not found")
 	case errors.Is(err, library.ErrConflict), errors.Is(err, library.ErrSlugTaken), errors.Is(err, library.ErrNameTaken),
-		errors.Is(err, plugins.ErrNameTaken), errors.Is(err, jobs.ErrFinished):
+		errors.Is(err, plugins.ErrNameTaken), errors.Is(err, jobs.ErrFinished), errors.Is(err, plugins.ErrInUse),
+		errors.Is(err, targets.ErrNewCopy), errors.Is(err, targets.ErrNoChanges), errors.Is(err, targets.ErrMoved):
 		return huma.Error409Conflict(err.Error())
-	case errors.Is(err, library.ErrInvalid), errors.Is(err, plugins.ErrInvalid), errors.Is(err, plugins.ErrUnknownPlugin):
+	case errors.Is(err, plugins.ErrBroken):
+		// The stored error can hold paths and plugin output, so it stays out of the response.
+		return huma.Error409Conflict("plugin instance is broken; fix its settings first")
+	case errors.Is(err, library.ErrInvalid), errors.Is(err, plugins.ErrInvalid), errors.Is(err, plugins.ErrUnknownPlugin),
+		errors.Is(err, targets.ErrCannot):
 		return huma.Error400BadRequest(err.Error())
+	case errors.As(err, &rpcErr), errors.Is(err, targets.ErrBadReply):
+		return huma.Error502BadGateway("plugin: " + err.Error())
+	case errors.As(err, &startErr), errors.Is(err, protocol.ErrClosed):
+		s.Log.Error("plugin failed", "err", err)
+		return huma.Error502BadGateway("plugin failed; see the server log")
 	}
 	s.Log.Error("request failed", "err", err)
 	return huma.Error500InternalServerError("internal error")
```

- [ ] **Step 4: Share the instance check with imports**

```diff
diff --git a/internal/api/jobs.go b/internal/api/jobs.go
index 335b71c..cd308a5 100644
--- a/internal/api/jobs.go
+++ b/internal/api/jobs.go
@@ -6,7 +6,6 @@ import (
 	"encoding/json"
 	"fmt"
 	"net/http"
-	"slices"
 	"time"
 
 	"github.com/danielgtaylor/huma/v2"
@@ -15,6 +14,7 @@ import (
 	"github.com/davidgibbons/innkeeper/internal/importer"
 	"github.com/davidgibbons/innkeeper/internal/jobs"
 	"github.com/davidgibbons/innkeeper/internal/library"
+	"github.com/davidgibbons/innkeeper/internal/plugins"
 )
 
 // watchInterval is how often a job stream checks the job. Tests shorten it.
@@ -73,6 +73,22 @@ type bulkIn struct {
 	}
 }
 
+// usable checks that an instance exists, isn't broken, and its plugin has
+// capability.
+func (s *Server) usable(ctx context.Context, id int64, capability string) error {
+	inst, err := s.Registry.GetInstance(ctx, id)
+	if err != nil {
+		return s.fail(err)
+	}
+	if inst.BrokenError != nil {
+		return s.fail(plugins.ErrBroken)
+	}
+	if !s.Registry.HasCapability(inst.Plugin, capability) {
+		return huma.Error400BadRequest(fmt.Sprintf("plugin %s can't %s", inst.Plugin, capability))
+	}
+	return nil
+}
+
 // job returns a job, with child counts for a bulk job.
 func (s *Server) job(ctx context.Context, id int64) (jobOut, error) {
 	j, err := s.Queue.Get(ctx, id)
@@ -204,16 +220,8 @@ func (s *Server) jobRoutes(api huma.API) {
 		Summary: "Start an import", Description: "Queues an import job for an instance of an import plugin.",
 		Tags: tags, DefaultStatus: http.StatusAccepted},
 		func(ctx context.Context, in *importIn) (*jobResp, error) {
-			inst, err := s.Registry.GetInstance(ctx, in.Body.InstanceID)
-			if err != nil {
-				return nil, s.fail(err)
-			}
-			// The stored error can hold paths and plugin output, so it stays out of the response.
-			if inst.BrokenError != nil {
-				return nil, huma.Error409Conflict("plugin instance is broken; fix its settings first")
-			}
-			if m := s.Registry.Manifests()[inst.Plugin]; m == nil || !slices.Contains(m.Capabilities, "import") {
-				return nil, huma.Error400BadRequest(fmt.Sprintf("plugin %s can't import", inst.Plugin))
+			if err := s.usable(ctx, in.Body.InstanceID, "import"); err != nil {
+				return nil, err
 			}
 			params := in.Body.Params
 			if params == nil {
```

- [ ] **Step 5: Add the routes**

```go
package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/davidgibbons/innkeeper/internal/library"
	"github.com/davidgibbons/innkeeper/internal/targets"
)

type pushIn struct {
	ID   int64 `path:"id"`
	Body struct {
		InstanceID int64 `json:"instance_id"`
		NewCopy    bool  `json:"new_copy,omitempty" doc:"Let a target that can't update cards make another copy"`
	}
}

type bulkPushIn struct {
	Body struct {
		InstanceID int64         `json:"instance_id"`
		Query      library.Query `json:"query" doc:"Selects the cards, as in POST /search. Paging is ignored."`
		NewCopy    bool          `json:"new_copy,omitempty" doc:"Let a target that can't update cards make another copy"`
	}
}

type previewIn struct {
	ID         int64 `path:"id"`
	InstanceID int64 `query:"instance_id" required:"true"`
}

type previewOut struct{ Body targets.Preview }

type bindingOut struct{ Body targets.Binding }

type bindingsOut struct{ Body []targets.Binding }

type pushesOut struct{ Body []targets.Push }

type adoptIn struct {
	ID   int64 `path:"id"`
	Body struct {
		Into           string `json:"into" enum:"base,override,ignore" doc:"base: into the card's own data. override: into this target's override. ignore: keep the library as it is and stop reporting these changes."`
		BaseRevisionID int64  `json:"base_revision_id,omitempty" doc:"The card's head revision. Required unless into is ignore; a stale one returns 409."`
	}
}

func (s *Server) binding(b targets.Binding, err error) (*bindingOut, error) {
	if err != nil {
		return nil, s.fail(err)
	}
	return &bindingOut{Body: b}, nil
}

func (s *Server) targetRoutes(api huma.API) {
	tags := []string{"Sync"}
	huma.Register(api, huma.Operation{OperationID: "push-card", Method: http.MethodPost, Path: "/cards/{id}/push",
		Summary: "Push a card to a target", Tags: tags, DefaultStatus: http.StatusAccepted,
		Description: "Queues a push job. Pushing overwrites changes made in the app; adopt them first to keep them."},
		func(ctx context.Context, in *pushIn) (*jobResp, error) {
			if _, err := s.Lib.Cards.Get(ctx, in.ID); err != nil {
				return nil, s.fail(err)
			}
			if err := s.usable(ctx, in.Body.InstanceID, "push"); err != nil {
				return nil, err
			}
			j, err := targets.EnqueuePush(ctx, s.Queue, in.Body.InstanceID, in.ID, in.Body.NewCopy)
			if err != nil {
				return nil, s.fail(err)
			}
			return &jobResp{Body: toJob(j)}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "push-cards", Method: http.MethodPost, Path: "/pushes",
		Summary: "Push many cards to a target", Description: "Queues one child job per card the query matches.",
		Tags: tags, DefaultStatus: http.StatusAccepted},
		func(ctx context.Context, in *bulkPushIn) (*jobResp, error) {
			q := in.Body.Query
			if q.Filter == nil && q.Text == "" {
				return nil, huma.Error400BadRequest("bulk actions need a filter or text; to select every card, filter on archived")
			}
			if err := s.usable(ctx, in.Body.InstanceID, "push"); err != nil {
				return nil, err
			}
			ids, err := s.Lib.SearchIDs(ctx, q)
			if err != nil {
				return nil, s.fail(err)
			}
			p, err := targets.EnqueueBulkPush(ctx, s.Queue, in.Body.InstanceID, ids, in.Body.NewCopy)
			if err != nil {
				return nil, s.fail(err)
			}
			j, err := s.job(ctx, p.ID)
			if err != nil {
				return nil, err
			}
			return &jobResp{Body: j}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "preview-push", Method: http.MethodGet, Path: "/cards/{id}/push-preview",
		Summary: "Show what a push would send", Tags: tags,
		Description: "Asks the target plugin what it stores. Lists fields the push would drop, and whether it would make another copy."},
		func(ctx context.Context, in *previewIn) (*previewOut, error) {
			p, err := s.Targets.Preview(ctx, in.InstanceID, in.ID)
			if err != nil {
				return nil, s.fail(err)
			}
			return &previewOut{Body: p}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "list-card-bindings", Method: http.MethodGet, Path: "/cards/{id}/bindings",
		Summary: "List the targets a card is pushed to", Tags: tags},
		func(ctx context.Context, in *idIn) (*bindingsOut, error) {
			if _, err := s.Lib.Cards.Get(ctx, in.ID); err != nil {
				return nil, s.fail(err)
			}
			list, err := s.Targets.Bindings(ctx, in.ID)
			if err != nil {
				return nil, s.fail(err)
			}
			return &bindingsOut{Body: orEmpty(list)}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "get-binding", Method: http.MethodGet, Path: "/bindings/{id}",
		Summary: "Get a binding", Tags: tags},
		func(ctx context.Context, in *idIn) (*bindingOut, error) {
			return s.binding(s.Targets.Binding(ctx, in.ID))
		})

	huma.Register(api, huma.Operation{OperationID: "list-pushes", Method: http.MethodGet, Path: "/bindings/{id}/pushes",
		Summary: "List a binding's pushes", Description: "Newest first.", Tags: tags},
		func(ctx context.Context, in *idIn) (*pushesOut, error) {
			list, err := s.Targets.Pushes(ctx, in.ID)
			if err != nil {
				return nil, s.fail(err)
			}
			return &pushesOut{Body: orEmpty(list)}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "unbind", Method: http.MethodDelete, Path: "/bindings/{id}",
		Summary: "Unbind a card from a target", Description: "Forgets the binding and its pushes. The app keeps its copy.",
		Tags: tags, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *idIn) (*struct{}, error) {
			return nil, s.fail(s.Targets.Unbind(ctx, in.ID))
		})

	huma.Register(api, huma.Operation{OperationID: "check-binding", Method: http.MethodPost, Path: "/bindings/{id}/check",
		Summary: "Check the app's copy for changes", Description: "Reads the card back from the app now.", Tags: tags},
		func(ctx context.Context, in *idIn) (*bindingOut, error) {
			return s.binding(s.Targets.Check(ctx, in.ID))
		})

	huma.Register(api, huma.Operation{OperationID: "adopt", Method: http.MethodPost, Path: "/bindings/{id}/adopt",
		Summary: "Adopt the app's changes", Tags: tags,
		Description: "Changes to character_book aren't adopted. To discard the app's changes, push instead."},
		func(ctx context.Context, in *adoptIn) (*bindingOut, error) {
			return s.binding(s.Targets.Adopt(ctx, in.ID, in.Body.Into, in.Body.BaseRevisionID))
		})
}
```

- [ ] **Step 6: Give the API service its targets**

```diff
diff --git a/cmd/innkeeper/api.go b/cmd/innkeeper/api.go
index 3465c19..60f17e6 100644
--- a/cmd/innkeeper/api.go
+++ b/cmd/innkeeper/api.go
@@ -11,6 +11,7 @@ import (
 	"github.com/davidgibbons/innkeeper/internal/auth"
 	"github.com/davidgibbons/innkeeper/internal/jobs"
 	"github.com/davidgibbons/innkeeper/internal/pluginhost"
+	"github.com/davidgibbons/innkeeper/internal/targets"
 )
 
 func apiServer() error {
@@ -32,7 +33,7 @@ func apiServer() error {
 
 	stopping := make(chan struct{})
 	s := &api.Server{Lib: svc.lib, Queue: jobs.New(svc.pool), Registry: svc.registry, Auth: auth.New(svc.pool),
-		Plugins: mgr, Log: log, PublicURL: os.Getenv("INNKEEPER_PUBLIC_URL"), Stopping: stopping}
+		Targets: targets.New(svc.pool, svc.lib, svc.registry, mgr, log), Plugins: mgr, Log: log, PublicURL: os.Getenv("INNKEEPER_PUBLIC_URL"), Stopping: stopping}
 	srv := &http.Server{
 		Addr:              listen,
 		Handler:           s.Handler(),
```

- [ ] **Step 7: Run the tests**

Run: `go vet ./... && go test ./internal/api/`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/api cmd/innkeeper/api.go
git commit -m "Add sync routes: push, preview, bindings, check, and adopt"
```

### Task 11: `push` command

**Files:**
- Modify: `cmd/innkeeper/commands.go`, `cmd/innkeeper/main.go`
- Test: `cmd/innkeeper/commands_test.go`

- [ ] **Step 1: Write the failing test**

```go
package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPushCmd(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got = append(got, r.URL.Path+" "+string(body))
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"id": 7}`))
	}))
	defer srv.Close()
	t.Setenv("INNKEEPER_URL", srv.URL)
	t.Setenv("INNKEEPER_TOKEN", "tok")

	if err := pushCmd([]string{"3", "12"}); err != nil {
		t.Fatal(err)
	}
	if err := pushCmd([]string{"3", `{"filter": {"tag": "fantasy"}}`, "--new-copy"}); err != nil {
		t.Fatal(err)
	}
	want := []string{
		`/cards/12/push {"instance_id":3,"new_copy":false}`,
		`/pushes {"instance_id":3,"new_copy":true,"query":{"filter":{"tag":"fantasy"}}}`,
	}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("requests = %q, want %q", got, want)
	}
	for _, args := range [][]string{{"3"}, {"3", "12", "--force"}, {"x", "12"}, {"3", "{bad"}} {
		if err := pushCmd(args); err == nil {
			t.Errorf("push %q succeeded", args)
		}
	}
}
```

- [ ] **Step 2: Run it to make sure it fails**

Run: `go test ./cmd/innkeeper/ -run TestPushCmd`
Expected: FAIL to build, with `undefined: pushCmd`.

- [ ] **Step 3: Add the command**

```diff
diff --git a/cmd/innkeeper/commands.go b/cmd/innkeeper/commands.go
index 5e33d5b..0fc0187 100644
--- a/cmd/innkeeper/commands.go
+++ b/cmd/innkeeper/commands.go
@@ -97,6 +97,30 @@ func bulkCmd(args []string) error {
 	return startJob("/bulk", map[string]any{"query": query, "action": args[0]})
 }
 
+// pushCmd runs `innkeeper push INSTANCE_ID CARD_ID|QUERY_JSON [--new-copy]`.
+func pushCmd(args []string) error {
+	newCopy := len(args) == 3 && args[2] == "--new-copy"
+	if len(args) != 2 && !newCopy {
+		return errors.New(usage)
+	}
+	inst, err := parseID(args[0])
+	if err != nil {
+		return err
+	}
+	if strings.HasPrefix(strings.TrimSpace(args[1]), "{") {
+		query, err := jsonArg(args, 1, "")
+		if err != nil {
+			return err
+		}
+		return startJob("/pushes", map[string]any{"instance_id": json.Number(inst), "query": query, "new_copy": newCopy})
+	}
+	card, err := parseID(args[1])
+	if err != nil {
+		return err
+	}
+	return startJob("/cards/"+card+"/push", map[string]any{"instance_id": json.Number(inst), "new_copy": newCopy})
+}
+
 // export runs `innkeeper export CARD_ID [FILE]`.
 func export(args []string) error {
 	if len(args) < 1 || len(args) > 2 {
```

```diff
diff --git a/cmd/innkeeper/main.go b/cmd/innkeeper/main.go
index 4704938..604d5b3 100644
--- a/cmd/innkeeper/main.go
+++ b/cmd/innkeeper/main.go
@@ -24,6 +24,7 @@ API commands (read INNKEEPER_URL and INNKEEPER_TOKEN):
   innkeeper import <instance-id> [params-json]
   innkeeper export <card-id> [file]
   innkeeper bulk archive|unarchive <query-json>
+  innkeeper push <instance-id> <card-id>|<query-json> [--new-copy]
   innkeeper jobs [show|cancel|watch <job-id>]
 
 See README.md for the environment variables each command reads.`
@@ -61,6 +62,8 @@ func run(args []string) error {
 		return export(rest)
 	case cmd == "bulk":
 		return bulkCmd(rest)
+	case cmd == "push":
+		return pushCmd(rest)
 	case cmd == "jobs":
 		return jobsCmd(rest)
 	}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./cmd/innkeeper/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/innkeeper
git commit -m "Add the push command"
```

### Task 12: Docs

**Files:**
- Modify: `docs/protocol.md`, `README.md`

- [ ] **Step 1: Document the target methods**

````diff
diff --git a/docs/protocol.md b/docs/protocol.md
index a6d92ae..03e03a9 100644
--- a/docs/protocol.md
+++ b/docs/protocol.md
@@ -169,6 +169,52 @@ same way and waits a second, so don't add your own delay.
 {"jsonrpc": "2.0", "id": 3, "result": {"subject": "owner"}}
 ```
 
+### `push` and `pull`
+
+A target plugin sends cards to an app (`push`) and reads them back (`pull`).
+Cards cross as CCv3 in both directions; translate to and from the app's own
+format. Put app data that has no CCv3 field under `data.extensions.<plugin>`.
+
+**`target.describe`** takes no params and says what the app stores. The core
+calls it before every push and check, so the answer may depend on the
+instance's config. Don't contact the app to answer it.
+
+| Field | Meaning |
+|---|---|
+| `fields` | The CCv3 `data` keys the app keeps, such as `name`, `description`, `first_mes`. List `character_book` if the app keeps embedded lorebooks |
+| `pull` | `true` if `target.get` works. Needs the `pull` capability |
+| `update` | `true` if a `target.put` with a `remote_id` changes that card. `false` for an app where every put makes a new copy |
+| `delete` | Reserved. The core doesn't delete cards in apps yet |
+| `lorebooks` | `embedded`: linked lorebooks arrive as `data.character_book`. `standalone`: the app keeps lorebooks on their own; `target.put_lorebook` and `target.get_lorebook` are reserved for this, and until they exist the core sends cards without lorebooks. `none`: the app has no lorebooks |
+
+The core compares only `fields` when it looks for changes made in the app, and
+warns the owner before a push that would drop content in other fields.
+
+**`target.put`** creates or updates one card.
+
+| Param | Meaning |
+|---|---|
+| `card` | The CCv3 card, with this instance's override applied |
+| `remote_id` | Your ID for the app's copy. Absent for a new card, or when `update` is `false` |
+| `avatar` | Path to the avatar image in a blob store, or absent. Read it; don't move or change it |
+
+Reply with `remote_id`. If the app no longer has `remote_id`, reply with error
+`-32004`; the core then sends the card again as a new one.
+
+**`target.get`** reads one card back.
+
+| Param | Meaning |
+|---|---|
+| `remote_id` | The ID from `target.put` |
+
+Reply with `card`, the app's copy as a CCv3 card. If the app no longer has
+it, reply with error `-32004`.
+
+```json
+{"jsonrpc": "2.0", "id": 4, "method": "target.put", "params": {"card": {"spec": "chara_card_v3", "spec_version": "3.0", "data": {"name": "Brakka"}}}}
+{"jsonrpc": "2.0", "id": 4, "result": {"remote_id": "42"}}
+```
+
 ## Checking a plugin
 
 ```bash
@@ -179,6 +225,8 @@ It loads the manifest, then checks that:
 
 - the `initialize` reply matches the manifest
 - an unknown method returns `-32601` within 5 seconds
+- a `push` plugin's `target.describe` reply is valid, and claims `pull` only
+  if the manifest declares it
 - `shutdown` gets a reply within 5 seconds
 - the plugin exits after `shutdown`, rather than having exited earlier
 
````

- [ ] **Step 2: Document sync in the README**

```diff
diff --git a/README.md b/README.md
index 32780d0..19d6353 100644
--- a/README.md
+++ b/README.md
@@ -27,8 +27,9 @@ database at startup and stop cleanly on `SIGTERM`.
   `/openapi.json`, and browsable docs at `/docs`. Job progress streams from
   `GET /jobs/{id}/events`, which sends a `: ping` comment every 15 seconds
   while idle.
-- `innkeeper worker` runs jobs: imports, bulk actions, and daily cleanup of
-  unused blobs and of jobs that finished over 30 days ago.
+- `innkeeper worker` runs jobs: imports, bulk actions, pushes, an hourly
+  check of target apps for changes, and daily cleanup of unused blobs and of
+  jobs that finished over 30 days ago.
 
 | Variable | Used by | Meaning |
 |---|---|---|
@@ -82,6 +83,8 @@ innkeeper call <method> <path> [json]           any API call, such as: call GET
 innkeeper import <instance-id> [params-json]    start an import
 innkeeper export <card-id> [file]               a card as CCv3 JSON
 innkeeper bulk archive|unarchive <query-json>   archive or unarchive the cards a search matches
+innkeeper push <instance-id> <card-id>|<query-json> [--new-copy]
+                                                push one card, or the cards a search matches, to a target
 innkeeper jobs [show|cancel|watch <job-id>]     list or follow jobs
 innkeeper token create <name> | list | revoke <id>
 innkeeper backup <dir>
@@ -92,6 +95,32 @@ on `archived`. It touches only cards not already in the target state.
 
 `jobs watch` exits non-zero unless the job succeeds.
 
+`push --new-copy` is for apps that can't update a card: pushing there again
+makes another copy, so the push fails without the flag.
+
+## Sync
+
+A card pushed to a target app gets a binding. `GET /cards/{id}/bindings`
+shows its status on each target:
+
+| Status | Meaning |
+|---|---|
+| `in_sync` | Nothing changed on either side since the last push |
+| `ahead` | The card, its avatar, its override for this target, or its embedded lorebooks changed since the last push |
+| `remote_changed` | Someone changed the card in the app |
+| `diverged` | Both of the above |
+| `missing` | The app no longer has the card. The next push makes a new copy |
+| `cant_check` | The app can't be read back, so changes made there are unknown |
+
+When the app's copy changed, the binding lists the changed fields. Take them
+with `POST /bindings/{id}/adopt`, `into` one of:
+
+- `base`: into the card itself
+- `override`: into this target's override, so only this app keeps them
+- `ignore`: leave the library as it is and stop reporting them
+
+To throw the app's changes away, push: a push overwrites them.
+
 ## Backup and restore
 
 `innkeeper backup <dir>` writes a `pg_dump` file named
```

- [ ] **Step 3: Run everything**

Run: `gofmt -l . && go vet ./... && go test -race ./...`
Expected: `gofmt` prints nothing, and every package passes.

- [ ] **Step 4: Commit**

```bash
git add docs/protocol.md README.md
git commit -m "Document the target methods and sync"
```

- [ ] **Step 5: Update Kaneo**

Move INN-5 to Done, and set its description to point at this plan and the merge commit.
