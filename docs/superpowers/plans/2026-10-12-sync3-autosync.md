# Autosync Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A binding with autosync on gets the library's copy pushed by every sync when the card is ahead, unless the app's copy changed.

**Architecture:** Step 3 of `docs/superpowers/specs/2026-10-11-single-sync-design.md` (INN-45).
- **Storage:** a `binding.autosync` column, set through `PATCH /bindings/{id}`.
- **Pass 2:** after pass 1 has run for every app, `Service.Sync` pushes each app's autosync bindings whose status is `ahead`.
- **Guard:** each push passes `pushExpect{appHash: accepted_hash}`, so `push` refuses with `ErrAppChanged` when the app's copy isn't the one the library last agreed with. That item is counted and left for review.
- **Locking:** the sync's ctx holds the sync lock, so these pushes skip `waitForSync`.

**Tech Stack:** Go, pgx v5, PostgreSQL, Huma; the web API client is regenerated with `npm run gen`.

**Tests:** DB tests skip without the database URL, so a run without it proves nothing. Always run with
`INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres`.
`go test ./internal/targets` takes about three minutes; use `-timeout 15m`.

---

## Files

- Create `internal/db/migrations/0019_binding_autosync.sql`, and update the count in `internal/db/db_test.go` (`TestMigrateTwice`).
- Modify `internal/wire/sync.go`: `Binding.Autosync`.
- Modify `internal/targets/targets.go`: select the column, add `SetAutosync`. Test: `internal/targets/sync_test.go`.
- Modify `internal/targets/sync.go`: pass 2, `autosync`. Test: `internal/targets/sync_test.go`.
- Modify `internal/api/targets.go`: `PATCH /bindings/{id}`. Test: `internal/api/targets_test.go`.
- Regenerate `web/openapi.json` and `web/src/api/schema.d.ts`.

---

### Task 1: store autosync and set it

**Files:**
- Create: `internal/db/migrations/0019_binding_autosync.sql`
- Modify: `internal/db/db_test.go`, `internal/wire/sync.go`, `internal/targets/targets.go`
- Test: `internal/targets/sync_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/targets/sync_test.go`:

```go
func TestSetAutosync(t *testing.T) {
	e := setup(t, nil)
	b := e.push(e.card)
	if b.Autosync {
		t.Fatal("a new binding has autosync on")
	}
	got, err := e.s.SetAutosync(e.ctx, b.ID, true)
	if err != nil || !got.Autosync {
		t.Fatalf("on = %+v, %v", got, err)
	}
	if got, err = e.s.SetAutosync(e.ctx, b.ID, false); err != nil || got.Autosync {
		t.Fatalf("off = %+v, %v", got, err)
	}
	if _, err := e.s.SetAutosync(e.ctx, 999999, true); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown binding: %v", err)
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres go test ./internal/targets -run TestSetAutosync`
Expected: a build failure, `b.Autosync undefined`.

- [ ] **Step 3: Add the column**

Create `internal/db/migrations/0019_binding_autosync.sql`:

```sql
-- With autosync on, each sync pushes the library's copy when the card is
-- ahead and the app's copy hasn't changed.
ALTER TABLE binding ADD COLUMN autosync boolean NOT NULL DEFAULT false;
```

In `internal/db/db_test.go`, `TestMigrateTwice` expects 18 migrations. Change both 18s to 19.

- [ ] **Step 4: Expose and set it**

In `internal/wire/sync.go`, add to `Binding` after `RemoteState`:

```go
	// Autosync means each sync pushes the library's copy when the card is
	// ahead and the app's copy hasn't changed.
	Autosync bool `json:"autosync"`
```

In `internal/targets/targets.go`:
- In `bindingQuery`, add `b.autosync` after `b.remote_state`.
- In `scanBinding`, add `&b.Autosync` after `&b.RemoteState`.
- After `Unbind`, add:

```go
// SetAutosync turns autosync on or off for a binding.
func (s *Service) SetAutosync(ctx context.Context, id int64, on bool) (Binding, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE binding SET autosync = $2 WHERE id = $1`, id, on)
	if err != nil {
		return Binding{}, err
	}
	if tag.RowsAffected() == 0 {
		return Binding{}, ErrNotFound
	}
	return s.Binding(ctx, id)
}
```

Check that no other query scans `bindingQuery` with its own column list. Grep `internal/targets` for `scanBinding(` and `bindingQuery`.

- [ ] **Step 5: Run the packages**

Run: `INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres go test -timeout 15m ./internal/targets ./internal/db ./internal/backup`
Expected: `ok` for all three. `internal/backup` copies `binding` by its column list, so it covers the new column.

- [ ] **Step 6: Commit**

```bash
git add internal/db/migrations/0019_binding_autosync.sql internal/db/db_test.go internal/wire/sync.go internal/targets/targets.go internal/targets/sync_test.go
git commit -m "feat(targets): store autosync on a binding"
```

---

### Task 2: pass 2 pushes autosync bindings

**Files:**
- Modify: `internal/targets/sync.go`
- Test: `internal/targets/sync_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/targets/sync_test.go`. It uses `addApp` from this file and `setup`, `push`, `editRemote`, `update`, `setField` and `head` from `env_test.go`. Add `"github.com/inkandale/innkeeper/internal/library"` to its imports if missing.

```go
// appData reads the data of the card the fake app in dir keeps as remoteID.
func appData(t *testing.T, dir, remoteID string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, remoteID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var item struct{ Data map[string]any }
	if err := json.Unmarshal(raw, &item); err != nil {
		t.Fatal(err)
	}
	return item.Data
}

// twoApps pushes e.card to e's app and to a second one, and returns the
// second app's dir and binding.
func twoApps(e *env) (string, Binding) {
	e.t.Helper()
	e.push(e.card)
	inst2, dir2 := e.addApp()
	b2, err := e.s.Push(e.ctx, inst2, e.card, false)
	if err != nil {
		e.t.Fatal(err)
	}
	return dir2, b2
}

// An edit pulled from one app reaches an app with autosync on in the same sync.
func TestSyncAutosyncPushes(t *testing.T) {
	for _, on := range []bool{true, false} {
		e := setup(t, nil)
		dir2, b2 := twoApps(e)
		if _, err := e.s.SetAutosync(e.ctx, b2.ID, on); err != nil {
			t.Fatal(err)
		}
		e.editRemote(e.bindingOfCard(e.inst).RemoteID, "description", "Edited in the first app.")
		res, err := e.s.Sync(e.ctx, SyncParams{}, nil)
		if err != nil {
			t.Fatal(err)
		}
		got := appData(t, dir2, b2.RemoteID)["description"]
		pushed := res.Apps[1].Totals["autosync_pushed"]
		if on && (got != "Edited in the first app." || pushed != 1) {
			t.Errorf("autosync on: second app has %v, pushed %d", got, pushed)
		}
		if !on && (got == "Edited in the first app." || pushed != 0) {
			t.Errorf("autosync off: second app has %v, pushed %d", got, pushed)
		}
	}
}

// Autosync doesn't overwrite an app copy that changed; the item waits for review.
func TestSyncAutosyncLeavesChangedApp(t *testing.T) {
	e := setup(t, nil)
	dir2, b2 := twoApps(e)
	if _, err := e.s.SetAutosync(e.ctx, b2.ID, true); err != nil {
		t.Fatal(err)
	}
	e.update(e.card, func(ed *library.Edit) { ed.Data = setField(ed.Data, "first_mes", "Edited in the library.") })
	raw, err := os.ReadFile(filepath.Join(dir2, b2.RemoteID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var item map[string]any
	if err := json.Unmarshal(raw, &item); err != nil {
		t.Fatal(err)
	}
	item["data"].(map[string]any)["description"] = "Edited in the second app."
	raw, _ = json.Marshal(item)
	if err := os.WriteFile(filepath.Join(dir2, b2.RemoteID+".json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := e.s.Sync(e.ctx, SyncParams{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := appData(t, dir2, b2.RemoteID)["description"]; got != "Edited in the second app." {
		t.Errorf("second app was overwritten: %v", got)
	}
	if res.Apps[1].Totals["autosync_changed"] != 1 || res.Apps[1].Totals["autosync_pushed"] != 0 {
		t.Errorf("totals = %v", res.Apps[1].Totals)
	}
}
```

The tests need `e.bindingOfCard(inst)`, the card's binding on one instance. Add it to `sync_test.go`:

```go
// bindingOfCard is e.card's binding on inst.
func (e *env) bindingOfCard(inst int64) Binding {
	e.t.Helper()
	list, err := e.s.Bindings(e.ctx, e.card)
	if err != nil {
		e.t.Fatal(err)
	}
	for _, b := range list {
		if b.InstanceID == inst {
			return b
		}
	}
	e.t.Fatalf("card %d has no binding on %d", e.card, inst)
	return Binding{}
}
```

The fake app's `first_mes` and `description` are stored fields, so each edit is visible. If `setup`'s card (`Brakka`) has no `description`, the edits still differ from the pushed copy, which has none.

- [ ] **Step 2: Run them to see them fail**

Run: `INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres go test ./internal/targets -run 'TestSyncAutosync'`
Expected:
- `TestSyncAutosyncPushes` fails with `autosync on: second app has <nil>, pushed 0`, or with the old description.
- `TestSyncAutosyncLeavesChangedApp` fails on its totals.

- [ ] **Step 3: Implement pass 2**

In `internal/targets/sync.go`, add `"crypto/rand"` and `"errors"` to the imports. In `Sync`, after the per-app loop and before `return res, nil`, add:

```go
	// Pass 2 runs after every app's pass 1, so an edit pulled from one app reaches the others.
	for i := range res.Apps {
		if res.Apps[i].Error == "" {
			s.autosync(ctx, &res.Apps[i])
		}
	}
	if progress != nil {
		progress(res)
	}
```

Append:

```go
// autosync pushes the app's bindings that have autosync on and are ahead.
// Each push expects the app to hold the copy the library last agreed with,
// so one that changed in the app is left for the owner and counted as
// autosync_changed.
func (s *Service) autosync(ctx context.Context, app *SyncApp) {
	// Without an accepted copy, a push can't tell whether the app changed.
	rows, err := s.pool.Query(ctx, `SELECT b.id, coalesce(b.card_id, 0), coalesce(b.lorebook_id, 0), b.accepted_hash, p.id
		FROM binding b JOIN binding_status st ON st.binding_id = b.id
		JOIN LATERAL (SELECT id FROM push WHERE binding_id = b.id ORDER BY id DESC LIMIT 1) p ON TRUE
		WHERE b.instance_id = $1 AND b.autosync AND st.status = 'ahead' AND coalesce(b.accepted_hash, '') <> ''
		ORDER BY b.id`, app.InstanceID)
	if err != nil {
		app.Error = "autosync: " + err.Error()
		return
	}
	type due struct {
		binding, card, lorebook int64
		accepted                string
		lastPush                int64
	}
	var list []due
	var d due
	if _, err := pgx.ForEachRow(rows, []any{&d.binding, &d.card, &d.lorebook, &d.accepted, &d.lastPush}, func() error {
		list = append(list, d)
		return nil
	}); err != nil {
		app.Error = "autosync: " + err.Error()
		return
	}
	if app.Totals == nil {
		app.Totals = map[string]int{}
	}
	for _, d := range list {
		k, id := cardKind, d.card
		if d.lorebook != 0 {
			k, id = lorebookKind, d.lorebook
		}
		_, err := s.push(ctx, k, app.InstanceID, id, false, rand.Text(), &pushExpect{lastPush: &d.lastPush, appHash: d.accepted})
		switch {
		case err == nil:
			app.Totals["autosync_pushed"]++
		case errors.Is(err, ErrAppChanged):
			app.Totals["autosync_changed"]++
		default:
			app.Totals["autosync_failed"]++
			s.log.Warn("sync: autosync push failed", "binding", d.binding, "err", err)
		}
	}
}
```

If `accepted_hash` is `NOT NULL` in the schema, `coalesce` is harmless. Check `internal/db/migrations` for its definition and keep or drop the `coalesce` to match. The `d.lastPush` pointer is taken inside the loop over a copy, so each push gets its own value.

- [ ] **Step 4: Run the whole package**

Run: `INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres go test -timeout 15m ./internal/targets`
Expected: `ok`.

If `TestSyncAutosyncLeavesChangedApp` shows the second app overwritten, `push` didn't compare the app copy. Read `push` around `expect.appHash` (push.go, about line 105). It skips the check when `pushedSince` is computed wrongly or when `info.Pull` is false, so make sure the pass-2 query's `p.id` is the latest push. Don't weaken the test.

- [ ] **Step 5: Commit**

```bash
git add internal/targets/sync.go internal/targets/sync_test.go
git commit -m "feat(targets): a sync pushes autosync bindings that are ahead"
```

---

### Task 3: `PATCH /bindings/{id}` and the web client

**Files:**
- Modify: `internal/api/targets.go`, `internal/api/targets_test.go`
- Regenerate: `web/openapi.json`, `web/src/api/schema.d.ts`

- [ ] **Step 1: Write the failing API test**

Append to `internal/api/targets_test.go`:

```go
func TestBindingAutosyncRoute(t *testing.T) {
	e := newEnv(t)
	inst, _ := e.target()
	var c entityOut
	e.call("POST", "/cards", map[string]any{"data": map[string]any{"name": "Brakka", "first_mes": "Hail."}}, 201, &c)
	b, err := e.s.Targets.Push(context.Background(), inst, c.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/bindings/%d", b.ID)
	var got targets.Binding
	e.call("PATCH", path, map[string]any{"autosync": true}, 200, &got)
	if !got.Autosync {
		t.Fatalf("patched = %+v", got)
	}
	e.call("GET", path, nil, 200, &got)
	if !got.Autosync {
		t.Errorf("read back = %+v", got)
	}
	e.call("PATCH", "/bindings/999999", map[string]any{"autosync": true}, 404, nil)
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres go test ./internal/api -run TestBindingAutosyncRoute`
Expected: FAIL, `PATCH` returns 405.

- [ ] **Step 3: Add the route**

In `internal/api/targets.go`, add near the other binding input types:

```go
type autosyncIn struct {
	ID   int64 `path:"id"`
	Body struct {
		Autosync bool `json:"autosync"`
	}
}
```

and register it after `get-binding`:

```go
	huma.Register(api, huma.Operation{Metadata: needs(tier.Write), OperationID: "update-binding", Method: http.MethodPatch,
		Path: "/bindings/{id}", Summary: "Change a binding's settings", Tags: tags,
		Description: "With autosync on, each sync pushes the library's copy when the card is ahead and the app's copy hasn't changed."},
		func(ctx context.Context, in *autosyncIn) (*bindingOut, error) {
			return s.binding(s.Targets.SetAutosync(ctx, in.ID, in.Body.Autosync))
		})
```

- [ ] **Step 4: Run the API package and regenerate the client**

Run: `INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres go test -timeout 15m ./internal/api`
Expected: `ok`.

Run `cd web && npm run gen && npm test`. Expected: the generated files change, and all web tests pass. CI checks that the generated client matches the API.

- [ ] **Step 5: Commit**

```bash
git add internal/api/targets.go internal/api/targets_test.go web/openapi.json web/src/api/schema.d.ts
git commit -m "feat(api): turn autosync on or off for a binding"
```

---

### Task 4: full check

- [ ] **Step 1:** Run `go vet ./... && INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres go test -count=1 -timeout 20m ./...`. Expected: every package `ok` or `no test files`.
