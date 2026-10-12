# Changes Queue Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** One queue with a row per card that needs the owner, built from each app's latest sync and the binding statuses, plus the web, CLI and MCP surfaces that use it.

**Architecture:** INN-47, spec `docs/superpowers/specs/2026-10-12-changes-queue-design.md`.
- **`Service.Changes`** reads each app's latest finished reconcile snapshot and `binding_status` and groups the changes by card. App items with no candidate card become orphan rows, listed first.
- **Stale entries are dropped.** An unbound item that's been bound since the snapshot, or a both-changed item whose binding has since accepted that app copy, no longer appears.
- **Reconcile items gain `app_changed`**, so an edit on both sides isn't confused with a library-only change. Library-only changes come from the binding status `ahead`.
- **Surfaces:** `GET /sync/changes`, `ink sync changes`, the MCP tool `list_changes`, and a web queue at `/sync/changes` that the Sync page, Review and the card panel point at.

**Tech Stack:** Go, Huma, cobra, MCP Go SDK, React + TanStack Query, Vitest.

**Tests:** DB tests skip without the database URL, so a run without it proves nothing. Always run with
`INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres`, and use `-timeout 15m`. After an API change, run `cd web && npm run gen` (CI checks it). No commit may be marked as a breaking change.

---

### Task 1: `Service.Changes`

**Files:**
- Create: `internal/wire/changes.go`, `internal/targets/changes.go`, `internal/targets/changes_test.go`
- Modify: `internal/wire/reconcile.go` (`ReconcileItem.AppChanged`), `internal/targets/reconcile.go` (set it), `internal/targets/wire.go` (aliases)

- [ ] **Step 1: Mark items the app changed**

In `internal/wire/reconcile.go`, add to `ReconcileItem` after `AppOnly`:

```go
	// AppChanged means the app changed this bound item since the last push
	// or resolve, whether or not the library did too.
	AppChanged bool `json:"app_changed,omitempty"`
```

In `internal/targets/reconcile.go`, in `suggest`'s candidate loop, track `appChanged` next to `only`: it's true when any hunk's status is `merge.AutoApp` or `merge.Conflict`. Keep a slice like `appOnly` (`appChangedOf = append(appChangedOf, changed)`). In the `VerdictBound` case, set `item.AppChanged = appChangedOf[0]` whenever there's a candidate and the binding isn't missing.

- [ ] **Step 2: The wire types**

Create `internal/wire/changes.go`:

```go
package wire

// App states in the changes queue.
const (
	ChangeDiffers     = "differs"
	ChangeBothChanged = "both_changed"
	ChangeAhead       = "ahead"
	ChangeMissing     = "missing"
	ChangeError       = "error"
	ChangeNew         = "new"
)

// AppChange is one app's change to a card, or an app item no card matches.
type AppChange struct {
	InstanceID int64  `json:"instance_id"`
	State      string `json:"state" enum:"differs,both_changed,ahead,missing,error,new"`
	RemoteID   string `json:"remote_id,omitempty"`
	// Name is the app item's name.
	Name      string `json:"name,omitempty"`
	BindingID int64  `json:"binding_id,omitempty"`
	// JobID is the reconcile job whose snapshot the state came from; 0 for ahead and missing.
	JobID     int64   `json:"job_id,omitempty"`
	AppHash   string  `json:"app_hash,omitempty"`
	Suggested *Option `json:"suggested,omitempty"`
	Error     string  `json:"error,omitempty"`
}

// ChangeRow is a library card, or an app item no card matches, with its changes.
type ChangeRow struct {
	// CardID is 0 for an app item no card matches.
	CardID int64       `json:"card_id,omitempty"`
	Name   string      `json:"name"`
	Apps   []AppChange `json:"apps"`
}

// Changes is the changes queue.
type Changes struct {
	Rows []ChangeRow `json:"rows"`
	// Counts are by instance ID, then state, before any filter.
	Counts map[int64]map[string]int `json:"counts"`
}

// ChangesFilter narrows the rows; zero values match everything.
type ChangesFilter struct {
	InstanceID int64
	State      string
}
```

In `internal/targets/wire.go`, alias `AppChange`, `ChangeRow`, `Changes` and `ChangesFilter`, plus the six state constants.

- [ ] **Step 3: Write the failing test**

Create `internal/targets/changes_test.go`. It uses the helpers in `env_test.go` and `sync_test.go` (`setup`, `libCard`, `appCard`, `push`, `update`, `editRemote`, `setField`, `check`, `head`, `cardsNamed`):

```go
package targets

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/davidgibbons/innkeeper/internal/library"
)

func TestChanges(t *testing.T) {
	e := setup(t, nil)
	// ahead: pushed, then edited in the library only.
	e.push(e.card)
	e.update(e.card, func(ed *library.Edit) { ed.Data = setField(ed.Data, "first_mes", "Edited in the library.") })
	// both_changed: pushed, then edited on both sides.
	both := e.libCard(map[string]any{"name": "Tova", "description": "A smith."})
	bb := e.push(both)
	e.editRemote(bb.RemoteID, "description", "Edited in the app.")
	e.update(both, func(ed *library.Edit) { ed.Data = setField(ed.Data, "first_mes", "Edited in the library.") })
	// missing: pushed, then deleted in the app and checked.
	gone := e.libCard(map[string]any{"name": "Dain", "description": "A miner."})
	gb := e.push(gone)
	if err := os.Remove(filepath.Join(e.dir, gb.RemoteID+".json")); err != nil {
		t.Fatal(err)
	}
	e.check(gb)
	// differs: an app item likely the same as an unbound card.
	lia := e.libCard(map[string]any{"name": "Lia", "description": liaText})
	e.appCard("r1", map[string]any{"name": "Lia", "description": liaText + " She also juggles."}, nil)
	// new: an app item nothing matches.
	e.appCard("r2", map[string]any{"name": "Zed", "description": "Nobody in particular."}, nil)

	if _, err := e.s.Sync(e.ctx, SyncParams{Apply: ApplyNone}, nil); err != nil {
		t.Fatal(err)
	}
	ch, err := e.s.Changes(e.ctx, ChangesFilter{})
	if err != nil {
		t.Fatal(err)
	}
	states := map[string]string{}
	for _, r := range ch.Rows {
		if len(r.Apps) != 1 {
			t.Fatalf("row %q has %d apps: %+v", r.Name, len(r.Apps), r.Apps)
		}
		states[r.Name] = r.Apps[0].State
	}
	want := map[string]string{"Zed (Fake app only)": ChangeNew, "Brakka": ChangeAhead, "Tova": ChangeBothChanged,
		"Dain": ChangeMissing, "Lia": ChangeDiffers}
	if len(states) != len(want) {
		t.Errorf("rows = %v, want %v", states, want)
	}
	for name, st := range want {
		if states[name] != st {
			t.Errorf("%s = %q, want %q (rows %v)", name, states[name], st, states)
		}
	}
	if ch.Rows[0].CardID != 0 || ch.Counts[e.inst][ChangeAhead] != 1 {
		t.Errorf("first row %+v, counts %v", ch.Rows[0], ch.Counts)
	}

	// A filter narrows the rows but not the counts.
	ahead, err := e.s.Changes(e.ctx, ChangesFilter{State: ChangeAhead})
	if err != nil || len(ahead.Rows) != 1 || ahead.Rows[0].CardID != e.card || ahead.Counts[e.inst][ChangeNew] != 1 {
		t.Errorf("ahead only = %+v, %v", ahead, err)
	}

	// Binding the likely match takes it off the queue, though the snapshot is older.
	if _, err := e.s.Bind(e.ctx, e.inst, lia, "r1"); err != nil {
		t.Fatal(err)
	}
	ch, err = e.s.Changes(e.ctx, ChangesFilter{State: ChangeDiffers})
	if err != nil || len(ch.Rows) != 0 {
		t.Errorf("after bind = %+v, %v", ch.Rows, err)
	}
}
```

Notes:
- `setup`'s card is `Brakka`. Check the fake instance's name in `env_test.go` (the plan assumes "Fake app") and adjust the orphan's expected name.
- If `e.check` isn't a helper, call `e.s.Check(e.ctx, gb.ID)`.
- After Bind, Lia's row may become `ahead` or `both_changed`; the assertion only checks that no `differs` row remains.

- [ ] **Step 4: Run it to see it fail**

Run: `INNKEEPER_TEST_DATABASE_URL=... go test ./internal/targets -run TestChanges`
Expected: a build failure, `e.s.Changes undefined`.

- [ ] **Step 5: Implement**

Create `internal/targets/changes.go`:

```go
package targets

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Changes lists every card some app needs the owner for, and the app items no
// card matches, from each app's latest finished reconcile and the binding
// statuses. An entry the owner settled after that reconcile is left out.
//
// ponytail: built from every app's latest snapshot on each call; store the
// rows if a library outgrows that.
func (s *Service) Changes(ctx context.Context, f ChangesFilter) (Changes, error) {
	out := Changes{Rows: []ChangeRow{}, Counts: map[int64]map[string]int{}}
	names := map[int64]string{}
	insts, err := s.reg.ListInstances(ctx)
	if err != nil {
		return out, err
	}
	for _, in := range insts {
		names[in.ID] = in.Name
	}
	cards := map[int64]*ChangeRow{}
	var orphans []ChangeRow
	seen := map[[2]int64]bool{} // card and instance with a snapshot change
	add := func(card int64, c AppChange) {
		if out.Counts[c.InstanceID] == nil {
			out.Counts[c.InstanceID] = map[string]int{}
		}
		out.Counts[c.InstanceID][c.State]++
		if card != 0 {
			seen[[2]int64{card, c.InstanceID}] = true
		}
		if (f.InstanceID != 0 && c.InstanceID != f.InstanceID) || (f.State != "" && c.State != f.State) {
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

	// Remote IDs bound now, and the copy each binding accepted, to drop entries settled since the snapshot.
	type bound struct{ accepted string }
	live := map[int64]map[string]bound{}
	rows, err := s.pool.Query(ctx, `SELECT instance_id, remote_id, accepted_hash FROM binding
		WHERE card_id IS NOT NULL AND remote_state <> 'missing'`)
	if err != nil {
		return out, err
	}
	var inst int64
	var remote, accepted string
	if _, err := pgx.ForEachRow(rows, []any{&inst, &remote, &accepted}, func() error {
		if live[inst] == nil {
			live[inst] = map[string]bound{}
		}
		live[inst][remote] = bound{accepted}
		return nil
	}); err != nil {
		return out, err
	}

	snaps, err := s.pool.Query(ctx, `SELECT DISTINCT ON (instance_id) id, instance_id, progress FROM job
		WHERE kind = $1 AND status = 'succeeded' AND parent_id IS NULL AND instance_id IS NOT NULL
		ORDER BY instance_id, id DESC`, ReconcileKind)
	if err != nil {
		return out, err
	}
	type snapRow struct {
		job, inst int64
		raw       []byte
	}
	var latest []snapRow
	var sr snapRow
	if _, err := pgx.ForEachRow(snaps, []any{&sr.job, &sr.inst, &sr.raw}, func() error {
		latest = append(latest, sr)
		return nil
	}); err != nil {
		return out, err
	}
	for _, l := range latest {
		var snap Snapshot
		if err := json.Unmarshal(l.raw, &snap); err != nil {
			return out, fmt.Errorf("job %d snapshot: %w", l.job, err)
		}
		for _, it := range snap.Items {
			c := AppChange{InstanceID: l.inst, RemoteID: it.RemoteID, Name: it.Name, BindingID: it.BindingID,
				JobID: l.job, AppHash: it.AppHash, Suggested: it.Suggested, Error: it.Error}
			b, isBound := live[l.inst][it.RemoteID]
			card := int64(0)
			switch {
			case it.AutoBound || it.AutoImported || it.AutoPulled:
				continue
			case it.Verdict == VerdictBound:
				// A library-only change shows as ahead from the binding status instead.
				if !it.AppChanged || it.AppOnly || len(it.Candidates) == 0 || (isBound && b.accepted == it.AppHash) {
					continue
				}
				c.State, card = ChangeBothChanged, it.Candidates[0].CardID
			case isBound:
				// Bound since the snapshot.
				continue
			case it.Verdict == VerdictError:
				c.State = ChangeError
				if len(it.Candidates) > 0 {
					card = it.Candidates[0].CardID
				}
			default:
				i := unbound(it.Candidates)
				if it.Verdict == VerdictNew || i < 0 {
					c.State = ChangeNew
				} else {
					c.State, card = ChangeDiffers, it.Candidates[i].CardID
				}
			}
			add(card, c)
		}
	}

	st, err := s.pool.Query(ctx, `SELECT b.card_id, b.instance_id, b.id, b.remote_id, s.status FROM binding b
		JOIN binding_status s ON s.binding_id = b.id JOIN card c ON c.id = b.card_id
		WHERE c.archived_at IS NULL AND s.status IN ('ahead', 'missing') ORDER BY b.id`)
	if err != nil {
		return out, err
	}
	var card, binding int64
	var status string
	if _, err := pgx.ForEachRow(st, []any{&card, &inst, &binding, &remote, &status}, func() error {
		if seen[[2]int64{card, inst}] {
			return nil
		}
		add(card, AppChange{InstanceID: inst, State: status, RemoteID: remote, BindingID: binding})
		return nil
	}); err != nil {
		return out, err
	}

	ids := make([]int64, 0, len(cards))
	for id := range cards {
		ids = append(ids, id)
	}
	nr, err := s.pool.Query(ctx, `SELECT c.id, coalesce(r.data->>'name', '') FROM card c
		JOIN card_revision r ON r.id = c.head_revision_id WHERE c.id = ANY($1) AND c.archived_at IS NULL`, ids)
	if err != nil {
		return out, err
	}
	var id int64
	var name string
	var named []ChangeRow
	if _, err := pgx.ForEachRow(nr, []any{&id, &name}, func() error {
		r := cards[id]
		r.Name = name
		slices.SortFunc(r.Apps, func(a, b AppChange) int { return cmp.Compare(a.InstanceID, b.InstanceID) })
		named = append(named, *r)
		return nil
	}); err != nil {
		return out, err
	}
	byName := func(a, b ChangeRow) int {
		return cmp.Or(cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)), cmp.Compare(a.CardID, b.CardID))
	}
	slices.SortFunc(orphans, byName)
	slices.SortFunc(named, byName)
	out.Rows = append(append(out.Rows, orphans...), named...)
	return out, nil
}
```

A card left out by the name query is archived, so its row is dropped. Its counts stay, which is acceptable.

- [ ] **Step 6: Run the package**

Run: `INNKEEPER_TEST_DATABASE_URL=... go test -timeout 15m ./internal/targets`
Expected: `ok`. If a fixture doesn't produce the state the test expects, fix the fixture and say why. Don't loosen the classification.

- [ ] **Step 7: Commit**

```bash
git add internal/wire internal/targets
git commit -m "feat(targets): a changes queue with a row per card"
```

---

### Task 2: API, CLI, MCP

**Files:**
- Modify: `internal/api/reconcile.go`, `internal/api/reconcile_test.go`, `cmd/ink/sync.go` (or a new `cmd/ink/changes.go`), `cmd/ink/reconcile_test.go`, `internal/mcp/tools.go`, `internal/api/mcp_test.go`, `README.md`

- [ ] **Step 1: The route, test first**

API test, appended to `internal/api/reconcile_test.go`: start the worker (`e.work()`), add a target (`e.target()`) and an app card, then POST `/syncs` with `{"apply":"none"}` and watch it finish. `GET /sync/changes` returns 200, with one row whose app state is `new`. `GET /sync/changes?state=ahead` returns 0 rows but the same counts.

The route goes in `reconcileRoutes`:

```go
type changesIn struct {
	InstanceID int64  `query:"instance_id" doc:"Only this app's changes; counts still cover every app"`
	State      string `query:"state" enum:"differs,both_changed,ahead,missing,error,new" doc:"Only this state"`
}

type changesOut struct{ Body targets.Changes }
```

```go
	huma.Register(api, huma.Operation{OperationID: "list-changes", Method: http.MethodGet, Path: "/sync/changes",
		Summary: "List the cards that need the owner", Tags: tags,
		Description: "One row per card with a change in some app, and app items no card matches first. Built from each app's latest sync and the binding statuses."},
		func(ctx context.Context, in *changesIn) (*changesOut, error) {
			ch, err := s.Targets.Changes(ctx, targets.ChangesFilter{InstanceID: in.InstanceID, State: in.State})
			if err != nil {
				return nil, s.fail(err)
			}
			return &changesOut{Body: ch}, nil
		})
```

GET defaults to read tier, so `tiers_test` needs no change. Huma may reject a JSON map with int64 keys in the schema. If it does, change `Counts` to `map[string]map[string]int`, keyed by the decimal instance ID, in `wire` and in `Changes`, and note it.

- [ ] **Step 2: CLI**

Add `ink sync changes [--app <instance-id>] [--state <state>]`:
- It GETs `/sync/changes` with the query, and honors `--json` through `show`.
- Table output: one line per row, `<name>  <app name or #id>: <state>[, ...]`. Read the instance names from `GET /instances`, or print `#<id>`, whichever the CLI already does elsewhere.

Write a `cmd/ink` test with `stubAPI`. It checks the request URI with both flags and that the output contains a row name and its state.

- [ ] **Step 3: MCP**

Add `list_changes` at read tier, with `instance_id` and `state` inputs, calling `GET /sync/changes` with the query. Change the built-in count in `internal/api/mcp_test.go` from 34 to 35, and call the tool once in `TestMCPOtherTools`.

- [ ] **Step 4: README**

Invoke `documentation-writing`, then add two or three sentences where syncing is explained. Say the changes queue lists every card an app needs you for, name its states, and give `ink sync changes`. Edit with the Edit tool.

- [ ] **Step 5: Run, regenerate, commit**

Run `go vet ./... && INNKEEPER_TEST_DATABASE_URL=... go test -timeout 15m ./internal/api ./cmd/ink`, then `cd web && npm run gen && npm test`.

```bash
git add internal/api internal/mcp cmd/ink README.md web/openapi.json web/src/api/schema.d.ts
git commit -m "feat(api): list the changes queue, from the API, ink, and MCP"
```

---

### Task 3: web — the queue page and the Sync page

**Files:**
- Create: `web/src/sync/Changes.tsx`, `web/src/sync/Changes.test.tsx`
- Modify: `web/src/App.tsx` (route `sync/changes`), `web/src/sync/SyncPage.tsx`, `web/src/sync/SyncPage.test.tsx`, `web/src/sync/sync.ts` (helpers), `web/src/sync/Sync.css` (if needed)

Read `Queue.tsx`, `SyncPage.tsx` and their tests first, and match their patterns. That covers `api` and `must`, TanStack Query, toasts, `reviewPath`, `resolution` and `bulkResolutions` in `sync.ts`, the bulk apply mutation in `Queue.tsx`, and the push preview flow (`PushPreview`, and how `bulk push asks from the push preview`).

- [ ] **Step 1: Tests first** (`Changes.test.tsx`), stubbing `GET /api/sync/changes`:
  - The table shows one row per card. Each app's state appears as a cell under that app's column, and the orphan row comes first.
  - The app and state filters set the query (`?instance_id=`, `?state=`) from the URL search params, so the Sync page can link to them.
  - "Apply suggested" with a checked `differs` row that has a `bind` suggestion POSTs `/api/resolutions` with that resolution. With a checked `ahead` row, it opens the push preview for those cards first.
  - "Review one by one" opens the first review-type change: `differs`, `both_changed` or `new`. It goes to `reviewPath(instance, "items", remote_id, { job: job_id, queue: 1 })`.

- [ ] **Step 2: Implement `Changes.tsx`**
  - **Route:** `/sync/changes`.
  - **Columns:** a checkbox, Card (a link to `/cards/:id`, or the orphan's name), one column per app with a change anywhere in the result (states as badges), and Action.
  - **Actions per entry:**
    - Review, for `differs`, `both_changed` and `new`. It links to `reviewPath(..., { job, queue: 1 })`.
    - Push, for `ahead`. It opens the push preview for that card and app.
    - Push again or Unbind, for `missing`.
    - The error text, for `error`.
  - **Defaults:** a row is checked by default when every entry in it has a suggestion or is `ahead`.
  - **Apply suggested:** sends one bulk resolve for the review entries that have a suggestion, built with the `resolution` helper from the entry's `suggested`, `remote_id`, `app_hash` and `job_id`. For the `ahead` entries, it sends one push with the preview first, using `/pushes/preview` and then `/pushes` with the `ids` filter and the confirm, as in last week's bulk push. Afterwards it invalidates `["sync", "changes"]`.
  - **Empty state:** "Nothing needs you."

- [ ] **Step 3: Sync page**
  - Columns: Target, In sync (cards), Needs you, Ahead, Missing, Last sync.
  - **Needs you** is `counts[id].differs + both_changed + error + new` from `GET /sync/changes` (query key `["sync", "changes", "counts"]`). It links to `/sync/changes?instance_id=<id>`.
  - **Ahead** and **Missing** use the same counts and link to `?instance_id=<id>&state=ahead|missing`.
  - Keep the facet's in-sync count, labelled "In sync (cards)".
  - Show **Can't check** only when it isn't zero. Drop "New in app" and "Drifted" and the `target.scan` query, unless something still needs it.
  - Add a "Changes" link above the table, to `/sync/changes`.
  - The Last sync link stays, opening that run's record, without the applied count.
  - Invalidate `["sync", "changes"]` when a Sync or Refresh job finishes.
  - Update `SyncPage.test.tsx` to match.

- [ ] **Step 4:** run `cd web && npm test && npx tsc --noEmit -p .`, then commit:

```bash
git add web/src
git commit -m "feat(web): one changes queue with a row per card"
```

---

### Task 4: web — Review steps through the queue; the old queue is a record

**Files:**
- Modify: `web/src/sync/Review.tsx`, `web/src/sync/Review.test.tsx`, `web/src/sync/Queue.tsx`, `web/src/sync/Queue.test.tsx`, `web/src/card/Panel.tsx`, `web/src/card/Card.test.tsx`

- [ ] **Step 1: Tests first**
  - **Review with `?queue=1`:**
    - It shows "N of M", where M counts the review-type changes in `GET /sync/changes` and N is this change's position.
    - After a resolve, it navigates to the next review-type change's `reviewPath` with `queue: 1`. With none left, it goes to `/sync/changes`.
    - **Later** navigates to the next change without resolving.
    - Without `?queue`, Review behaves as today.
  - **Queue** (`/sync/reconciles/:job`):
    - There are no checkboxes and no Apply button.
    - It has a heading note: "What this sync found. Work through what needs you in Changes." That links to `/sync/changes?instance_id=<id>`.
  - **Card panel:** a binding row whose status is `ahead`, `missing` or `diverged`, or that has a change, links to `/sync/changes?instance_id=<id>`. Point the existing Review link at the queue instead.

- [ ] **Step 2: Implement**
  - In Review, fetch `["sync", "changes"]` only when `queue` is set. Compute the ordered review-type entries and find the current one by instance and remote ID. Add the Later button next to the existing actions, and the counter near the heading.
  - In Queue, remove the selection state, the bulk apply mutation and its button. Keep the table and the verdict chips as a read-only record, plus the summary and the note.
  - In Panel, add the link.

- [ ] **Step 3:** run `cd web && npm test && npx tsc --noEmit -p .`, then commit:

```bash
git add web/src
git commit -m "feat(web): review steps through the changes queue, and a sync's page is its record"
```

---

### Task 5: full check

- [ ] `go vet ./... && INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres go test -count=1 -timeout 20m ./...`
- [ ] `cd web && npm run gen && git diff --exit-code -- openapi.json src/api/schema.d.ts && npm test && npx tsc --noEmit -p .`
- [ ] `git log --format=%B <base>..HEAD | grep -E '!:|BREAKING'` prints nothing.
