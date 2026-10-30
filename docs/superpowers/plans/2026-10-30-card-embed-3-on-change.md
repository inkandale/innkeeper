# Card embeddings 3: update cards as they change

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** An enrich instance can keep its values current: core runs it on cards an import or an edit changes, and an hourly sweep catches changes it missed, without ever starting a backfill on its own.

**Architecture:** `enrich_on_import` becomes `enrich_on_change`, a manifest default that each instance can override and that records when it was turned on. Imports and API edits queue enrich runs at once. An hourly `enrich-sweep` job queues, per instance, the cards changed since it was turned on that hold no value from it and haven't failed on it, one run per instance at a time.

**Tech stack:** Go, PostgreSQL, huma, React.

**Spec:** [2026-10-30-card-embed-design.md](../specs/2026-10-30-card-embed-design.md), "Updating cards as they change". Plans 1 and 2 come first.

---

## Conventions

- Work in `~/git/innkeeper-card-embed` (branch `card-embed-spec`).
- `$DB` stands for
  `INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres`.
  Without it database tests skip, and a skip is not a pass.
- After any API change, run `cd web && npm run gen`.
- Comments say why, in one line. Follow `AGENTS.md`.

## Names

| Name | Meaning |
|---|---|
| updates on change | An instance's effective setting: its own `enrich_on_change`, or its plugin's manifest default when unset. |
| since | `plugin_instance.enrich_on_change_since`: when updates on change was last turned on. |
| changed | A card whose head revision was created, or which was unarchived, after since. |
| failed | A revision the plugin answered with an error or no valid value, recorded in `enrich_failure`. |

## Files

| File | Change |
|---|---|
| `protocol/manifest.go`, `protocol/manifest_test.go` | `enrich_on_change`; `enrich_on_import` as its retired alias |
| `internal/db/migrations/0053_enrich_on_change.sql` | Instance columns, `restored_at`, `enrich_failure` |
| `internal/plugins/plugins.go` | `EnrichOnChange`, `EnrichOnChangeSince` on instances |
| `internal/api/instances.go` | Patch `enrich_on_change` |
| `internal/libsettings/libsettings.go` | `EnqueueEnrichOnImport` → `EnqueueEnrichOnChange` |
| `internal/importer/importer.go` | Follow the rename |
| `internal/api/entities.go` | Queue on edit and unarchive |
| `internal/library/entities.go` | Set `restored_at` on unarchive |
| `internal/library/enrichment.go` | `RecordEnrichFailures`, `ChangedUnenriched` |
| `internal/enrich/enrich.go` | Record failures |
| `internal/enrich/sweep.go` | `SweepKind`, `Sweeper` |
| `internal/jobs/queue.go` | `EnqueueOnceForInstance` |
| `cmd/innkeeper/worker.go`, `internal/api/jobs.go` | Register and label the sweep |
| `internal/testutil/fakeplugin.go` | The fake's manifest flag |
| `web/src/settings/Instance.tsx` | "Update cards as they change" checkbox |
| `docs/protocol.md` | `enrich_on_change` |

---

### Task 1: `enrich_on_change` in the manifest

**Files:** `protocol/manifest.go`, `protocol/manifest_test.go`,
`internal/testutil/fakeplugin.go`

- [ ] **Step 1: Write the failing test**

In `protocol/manifest_test.go`, using the file's existing way of loading a
manifest from TOML text (find the helper that writes a `plugin.toml` to a
temp dir and calls `LoadManifest`; the test calls it `loadTOML`):

```go
func TestEnrichOnImportStillMeansEnrichOnChange(t *testing.T) {
	for _, key := range []string{"enrich_on_change", "enrich_on_import"} {
		m, err := loadTOML(t, enrichTOML+key+" = true\n")
		if err != nil || !m.EnrichOnChange {
			t.Errorf("%s: EnrichOnChange = %v, %v", key, m.EnrichOnChange, err)
		}
	}
}
```

`enrichTOML` is a minimal valid enrich manifest's text without tables; if
the file has one, reuse it.

- [ ] **Step 2: Run it and see it fail**

```bash
go test ./protocol -run TestEnrichOnImportStillMeansEnrichOnChange
```

Expected: FAIL: `m.EnrichOnChange undefined`.

- [ ] **Step 3: Implement**

In `protocol/manifest.go`, replace the `EnrichOnImport` field:

```go
	// EnrichOnChange is the default for whether each instance runs on the
	// cards an import or an edit changes. The owner can change it per
	// instance, so set it when the plugin is worth running on every change.
	EnrichOnChange bool `toml:"enrich_on_change"`
	// EnrichOnImport is enrich_on_change's retired name.
	EnrichOnImport bool `toml:"enrich_on_import"`
```

In `LoadManifest`, right after the TOML is decoded and before validation:

```go
	m.EnrichOnChange = m.EnrichOnChange || m.EnrichOnImport
```

In the capability-keys table (line ~368), replace the `enrich_on_import`
row with one for `enrich_on_change` reading `m.EnrichOnChange`. A manifest
setting the old key without `enrich` still fails, because the alias sets
`EnrichOnChange`.

In `internal/testutil/fakeplugin.go`, the `-mode=enrich-on-import` branch
writes `enrich_on_change = true` instead.

- [ ] **Step 4: Follow the rename and test**

```bash
go build ./... 2>&1 | head
```

Every remaining `EnrichOnImport` reference outside `manifest.go` becomes
`EnrichOnChange` (libsettings is renamed properly in Task 3; a mechanical
rename is fine here).

```bash
go test ./protocol/...
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add protocol internal
git commit -m "feat(protocol): rename enrich_on_import to enrich_on_change"
```

---

### Task 2: The per-instance setting

**Files:** create `internal/db/migrations/0053_enrich_on_change.sql`,
`internal/plugins/plugins.go`, `internal/api/instances.go`,
`internal/api/instances_test.go`, `web/src/settings/Instance.tsx`

- [ ] **Step 1: Write the migration**

`internal/db/migrations/0053_enrich_on_change.sql`:

```sql
-- Null follows the plugin's enrich_on_change. since bounds the hourly
-- sweep, so turning updates on never backfills older cards.
ALTER TABLE plugin_instance ADD COLUMN enrich_on_change boolean;
ALTER TABLE plugin_instance ADD COLUMN enrich_on_change_since timestamptz NOT NULL DEFAULT now();

-- When an archived card or lorebook was last restored: the sweep counts it
-- as changed.
ALTER TABLE card ADD COLUMN restored_at timestamptz;
ALTER TABLE lorebook ADD COLUMN restored_at timestamptz;

-- Revisions an instance's plugin failed on. The sweep doesn't send them
-- again; a bulk run the owner starts does.
CREATE TABLE enrich_failure (
    instance_id bigint NOT NULL REFERENCES plugin_instance ON DELETE CASCADE DEFERRABLE INITIALLY IMMEDIATE,
    card_id bigint NOT NULL,
    revision_id bigint NOT NULL,
    failed_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (instance_id, revision_id),
    FOREIGN KEY (revision_id, card_id) REFERENCES card_revision (id, card_id) ON DELETE CASCADE DEFERRABLE INITIALLY IMMEDIATE
);
```

- [ ] **Step 2: Write the failing test**

In `internal/api/instances_test.go`, following how its other tests create
an enrich instance (or reuse `newEnrichEnv` from `enrichments_test.go`,
which is in the same package):

```go
func TestInstanceEnrichOnChangeFollowsThePluginUntilSet(t *testing.T) {
	e := newEnrichEnv(t)
	var inst plugins.InstanceInfo
	e.call("GET", fmt.Sprintf("/instances/%d", e.instID), nil, 200, &inst)
	if inst.EnrichOnChange {
		t.Fatal("judge's manifest doesn't set enrich_on_change, so the instance shouldn't")
	}
	before := inst.EnrichOnChangeSince
	time.Sleep(10 * time.Millisecond)
	e.call("PATCH", fmt.Sprintf("/instances/%d", e.instID), map[string]any{"enrich_on_change": true}, 200, &inst)
	if !inst.EnrichOnChange || !inst.EnrichOnChangeSince.After(before) {
		t.Fatalf("turned on: %+v", inst)
	}
	since := inst.EnrichOnChangeSince
	e.call("PATCH", fmt.Sprintf("/instances/%d", e.instID), map[string]any{"enrich_on_change": true}, 200, &inst)
	if !inst.EnrichOnChangeSince.Equal(since) {
		t.Fatal("turning it on again moved since")
	}
	e.call("PATCH", fmt.Sprintf("/instances/%d", e.instID), map[string]any{"enrich_on_change": false}, 200, &inst)
	if inst.EnrichOnChange {
		t.Fatal("turned off, still on")
	}
}
```

- [ ] **Step 3: Run it and see it fail**

```bash
$DB go test ./internal/api -run TestInstanceEnrichOnChange -count=1
```

Expected: compile error: `inst.EnrichOnChange undefined`.

- [ ] **Step 4: Implement in the registry**

In `internal/plugins/plugins.go`:

Add to `InstanceInfo`:

```go
	// EnrichOnChange is whether an enrich instance runs on cards as they
	// change: the owner's choice, or the plugin's default until they make one.
	EnrichOnChange bool `json:"enrich_on_change"`
	// EnrichOnChangeSince is when EnrichOnChange was last turned on.
	EnrichOnChangeSince time.Time `json:"enrich_on_change_since"`
```

Add `EnrichOnChange *bool` to `InstanceEdit` (beside `LibraryAccess *bool`).

Append `enrich_on_change, enrich_on_change_since` to `infoCols`, and in
`scanInfo` scan them into a local `var onChange *bool` and
`&i.EnrichOnChangeSince`. Where `scanInfo` sets `i.Label` from the plugin's
manifest, also set:

```go
	m := r.manifests[i.Plugin]
	i.EnrichOnChange = m != nil && m.EnrichOnChange
	if onChange != nil {
		i.EnrichOnChange = *onChange
	}
```

Read `r.manifests` the same way the surrounding code does, under `r.mu` if
it locks there.

In `UpdateInstance`'s `UPDATE`, add parameter `$12` = `e.EnrichOnChange`:

```sql
			enrich_on_change_since = CASE WHEN $12 AND enrich_on_change IS DISTINCT FROM true
				THEN now() ELSE enrich_on_change_since END,
			enrich_on_change = coalesce($12, enrich_on_change),
```

`SET` expressions read the row's old values, so `since` moves only when the
stored setting wasn't already explicitly on.

- [ ] **Step 5: Accept it in the API**

In `internal/api/instances.go`, add to `updateInstanceIn.Body`:

```go
		EnrichOnChange *bool `json:"enrich_on_change,omitempty" doc:"For an enrich instance: run it on the cards an import or an edit changes. Turning it on doesn't score older cards; start a run for those."`
```

and pass `EnrichOnChange: in.Body.EnrichOnChange` in the
`plugins.InstanceEdit` literal.

- [ ] **Step 6: Run the test, regenerate**

```bash
$DB go test ./internal/api -run 'TestInstance' -count=1
cd web && npm run gen
```

Expected: PASS.

- [ ] **Step 7: The checkbox**

In `web/src/settings/Instance.tsx`, follow `libraryAccess` exactly: a
stored value from `instance?.enrich_on_change ?? false`, a `useState`, the
dirty check, the reset, and the PATCH field
(`enrich_on_change: enrichOnChange !== instance.enrich_on_change ? enrichOnChange : undefined`).
Render it only for plugins with the enrich capability, next to the other
checkboxes:

```tsx
            {plugin?.capabilities.includes("enrich") && (
              <label title="Turning this on doesn't score older cards; start a run from the library for those.">
                <input type="checkbox" checked={enrichOnChange} onChange={(e) => setEnrichOnChange(e.target.checked)} />{" "}
                Update cards as they change
              </label>
            )}
```

Use whatever variable `Instance.tsx` already has for the plugin's manifest
in place of `plugin`. Add a case to `Instance.test.tsx` that ticks it and
asserts the PATCH body has `enrich_on_change: true`.

```bash
cd web && npm run typecheck && npx vitest run src/settings/Instance.test.tsx
```

- [ ] **Step 8: Commit**

```bash
git add internal web
git commit -m "feat: let each enrich instance choose whether to update cards as they change"
```

---

### Task 3: Run on imports and edits

**Files:** `internal/libsettings/libsettings.go`, `internal/importer/importer.go`,
`internal/api/entities.go`, `internal/api/enrichments_test.go`

- [ ] **Step 1: Write the failing test**

In `internal/api/enrichments_test.go`:

```go
func TestEditQueuesEnrichForInstancesThatUpdateOnChange(t *testing.T) {
	e := newEnrichEnv(t)
	id := e.card("Brakka", "x")
	edit := func(desc string) {
		var c entityOut
		e.call("GET", fmt.Sprintf("/cards/%d", id), nil, 200, &c)
		e.call("PUT", fmt.Sprintf("/cards/%d", id), map[string]any{"base_revision_id": c.Revision.ID,
			"data": map[string]any{"name": "Brakka", "description": desc}}, 200, nil)
	}
	queued := func() int {
		var n int
		if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM job WHERE kind = $1 AND instance_id = $2`,
			enrich.Kind, e.instID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	edit("xx")
	if queued() != 0 {
		t.Fatal("queued with updates off")
	}
	e.call("PATCH", fmt.Sprintf("/instances/%d", e.instID), map[string]any{"enrich_on_change": true}, 200, nil)
	edit("xxx")
	if queued() != 1 {
		t.Fatal("an edit queued no run")
	}
	e.call("PUT", fmt.Sprintf("/cards/%d/archived", id), map[string]any{"archived": true}, 204, nil)
	e.call("PUT", fmt.Sprintf("/cards/%d/archived", id), map[string]any{"archived": false}, 204, nil)
	if queued() != 2 {
		t.Fatal("unarchiving queued no run")
	}
}
```

The worker in `newEnrichEnv` may already have run the job; counting all
jobs of the kind, not only queued ones, keeps the test independent of
timing.

- [ ] **Step 2: Run it and see it fail**

```bash
$DB go test ./internal/api -run TestEditQueuesEnrich -count=1
```

Expected: FAIL: "an edit queued no run".

- [ ] **Step 3: Rename and generalize the trigger**

In `internal/libsettings/libsettings.go`, replace `EnqueueEnrichOnImport`:

```go
// EnqueueEnrichOnChange queues one enrich run for cardIDs, which an import
// or an edit just changed, on each enabled instance that updates cards as
// they change. A run takes at most its plugin's card cap; the sweep picks
// up the rest.
func (s *Settings) EnqueueEnrichOnChange(ctx context.Context, q *jobs.Queue, cardIDs []int64) error {
	ctx = context.WithoutCancel(ctx)
	insts, err := s.Enrichers(ctx)
	if err != nil {
		return err
	}
	ids := slices.Compact(slices.Sorted(slices.Values(cardIDs)))
	manifests := s.reg.Manifests()
	var errs []error
	for _, inst := range insts {
		if !inst.EnrichOnChange {
			continue
		}
		p := enrich.Payload{CardIDs: ids[:min(len(ids), enrich.MaxCards(manifests[inst.Plugin]))]}
		if _, err := q.Enqueue(ctx, jobs.Spec{Kind: enrich.Kind, InstanceID: &inst.ID, Payload: p}); err != nil {
			errs = append(errs, fmt.Errorf("queue enrich on instance %d: %w", inst.ID, err))
		}
	}
	return errors.Join(errs...)
}
```

Check `s.Enrichers` returns `plugins.InstanceInfo` (or something carrying
it); if it returns another type, add `EnrichOnChange` to that type from the
same scan rather than looking instances up again.

In `internal/importer/importer.go`, call `EnqueueEnrichOnChange`.

- [ ] **Step 4: Call it on edit and unarchive**

In `internal/api/entities.go`, add one helper:

```go
// cardChanged queues enrich runs for a card an edit or a restore changed.
// A failure is only logged: the hourly sweep catches the card.
func (s *Server) cardChanged(ctx context.Context, k *library.Kind, id int64) {
	if k != library.Card {
		return
	}
	if err := s.libSettings().EnqueueEnrichOnChange(ctx, s.Queue, []int64{id}); err != nil {
		s.Log.Error("queue enrich on change", "card", id, "err", err)
	}
}
```

Call `s.cardChanged(ctx, k, in.ID)` after each successful `ents.Update`
(lines ~304 and ~380), and in the archive route after `bulk.SetArchived`
succeeds when `!in.Body.Archived`:

```go
		func(ctx context.Context, in *archiveIn) (*struct{}, error) {
			if in.Body.Unpublish && !in.Body.Archived {
				return nil, huma.Error422UnprocessableEntity("unpublish only goes with archived: true")
			}
			if err := bulk.SetArchived(ctx, s.Lib, s.Targets, k, in.ID, in.Body.Archived, in.Body.Unpublish); err != nil {
				return nil, s.fail(err)
			}
			if !in.Body.Archived {
				s.cardChanged(ctx, k, in.ID)
			}
			return nil, nil
		})
```

Check `k` is the `*library.Kind` in scope in these route closures and that
`s.libSettings()` exists (it's used in `settings.go`).

- [ ] **Step 5: Run the tests**

```bash
$DB go test ./internal/api ./internal/importer ./internal/libsettings -count=1
```

Expected: PASS. Importer tests that set `-mode=enrich-on-import` still pass,
since the fake writes `enrich_on_change` and a new instance follows it.

- [ ] **Step 6: Commit**

```bash
git add internal
git commit -m "feat(enrich): run on cards as imports and edits change them"
```

---

### Task 4: Record failures and restores

**Files:** `internal/library/entities.go`, `internal/library/enrichment.go`,
`internal/enrich/enrich.go`, `internal/library/enrichment_test.go`

- [ ] **Step 1: Write the failing test**

In `internal/library/enrichment_test.go` (create it if needed, `package
library`), a test of the query the sweep uses:

```go
func TestChangedUnenrichedListsOnlyNewUnscoredUnfailedCards(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	inst := newInstanceRow(t, lib)
	old, _ := newCard(t, lib, "old", `{"name": "Old"}`)
	since := time.Now()
	time.Sleep(10 * time.Millisecond)
	fresh, _ := newCard(t, lib, "fresh", `{"name": "Fresh"}`)
	scored, scoredRev := newCard(t, lib, "scored", `{"name": "Scored"}`)
	failed, failedRev := newCard(t, lib, "failed", `{"name": "Failed"}`)
	archived, _ := newCard(t, lib, "archived", `{"name": "Archived"}`)
	if err := lib.AddEnrichments(ctx, inst, []NewEnrichment{{CardID: scored, RevisionID: scoredRev, Key: "k", Value: json.RawMessage(`"v"`)}}); err != nil {
		t.Fatal(err)
	}
	if err := lib.RecordEnrichFailures(ctx, inst, []RevisionRef{{CardID: failed, RevisionID: failedRev}}); err != nil {
		t.Fatal(err)
	}
	if err := lib.Cards.SetArchived(ctx, archived, true); err != nil {
		t.Fatal(err)
	}

	got, err := lib.ChangedUnenriched(ctx, inst, since, 10)
	if err != nil || !slices.Equal(got, []int64{fresh}) {
		t.Fatalf("changed = %v, %v", got, err)
	}

	// Restoring an old card counts as a change.
	if err := lib.Cards.SetArchived(ctx, old, true); err != nil {
		t.Fatal(err)
	}
	if err := lib.Cards.SetArchived(ctx, old, false); err != nil {
		t.Fatal(err)
	}
	if got, _ = lib.ChangedUnenriched(ctx, inst, since, 10); !slices.Equal(got, []int64{old, fresh}) {
		t.Fatalf("after restore = %v", got)
	}
}
```

`RevisionRef` is defined in Step 3. If the package already has a type
holding exactly a card ID and a revision ID, use that instead.

- [ ] **Step 2: Run it and see it fail**

```bash
$DB go test ./internal/library -run TestChangedUnenriched -count=1
```

Expected: compile error: `lib.ChangedUnenriched undefined`.

- [ ] **Step 3: Implement in the library**

In `internal/library/entities.go`, `SetArchived`'s update becomes:

```go
		SET archived_at = CASE WHEN $2 THEN coalesce(archived_at, now()) END,
			restored_at = CASE WHEN NOT $2 AND archived_at IS NOT NULL THEN now() ELSE restored_at END,
			archived_reason = NULL WHERE id = $1`, id, archived)
```

(keep the table name the function already interpolates).

In `internal/library/enrichment.go`:

```go
// RevisionRef names one revision of one card.
type RevisionRef struct {
	CardID     int64
	RevisionID int64
}

// RecordEnrichFailures records that instance's plugin failed on revs, so
// the sweep doesn't send them again.
func (l *Library) RecordEnrichFailures(ctx context.Context, instance int64, revs []RevisionRef) error {
	cards, ids := make([]int64, len(revs)), make([]int64, len(revs))
	for i, r := range revs {
		cards[i], ids[i] = r.CardID, r.RevisionID
	}
	_, err := l.pool.Exec(ctx, `INSERT INTO enrich_failure (instance_id, card_id, revision_id)
		SELECT $1, c, r FROM unnest($2::bigint[], $3::bigint[]) x (c, r)
		ON CONFLICT (instance_id, revision_id) DO UPDATE SET failed_at = now()`, instance, cards, ids)
	if err != nil {
		return fmt.Errorf("record %d enrich failures for instance %d: %w", len(revs), instance, err)
	}
	return nil
}

// ChangedUnenriched returns up to limit live cards changed after since
// whose head holds no value or vector from instance and hasn't failed on
// it, lowest ID first.
func (l *Library) ChangedUnenriched(ctx context.Context, instance int64, since time.Time, limit int) ([]int64, error) {
	vectors, err := l.HasVectors(ctx)
	if err != nil {
		return nil, err
	}
	noVector := ""
	if vectors {
		noVector = ` AND NOT EXISTS (SELECT 1 FROM card_embedding v
			WHERE v.card_id = c.id AND v.instance_id = $1 AND v.revision_id = c.head_revision_id)`
	}
	rows, err := l.pool.Query(ctx, `SELECT c.id FROM card c JOIN card_revision r ON r.id = c.head_revision_id
		WHERE c.archived_at IS NULL AND greatest(r.created_at, c.restored_at) > $2
			AND NOT EXISTS (SELECT 1 FROM enrichment e WHERE e.card_id = c.id AND e.instance_id = $1 AND e.revision_id = c.head_revision_id)
			AND NOT EXISTS (SELECT 1 FROM enrich_failure f WHERE f.instance_id = $1 AND f.revision_id = c.head_revision_id)`+noVector+`
		ORDER BY c.id LIMIT $3`, instance, since, limit)
	if err != nil {
		return nil, fmt.Errorf("list changed cards for instance %d: %w", instance, err)
	}
	return pgx.CollectRows(rows, pgx.RowTo[int64])
}
```

`greatest` ignores a null `restored_at`.

- [ ] **Step 4: Record failures in the enrich job**

In `internal/enrich/enrich.go`, add to `Checked`:

```go
	// FailedRevisions are the revisions the plugin answered with an error,
	// no valid value, or nothing. A failed call records none: the plugin
	// may work later.
	FailedRevisions []library.RevisionRef
```

In `Check`, at each of the three places that do `out.Failed++` for a
revision (error, no valid values, no result), also append
`library.RevisionRef{CardID: rev.CardID, RevisionID: rev.RevisionID}` (for
the no-result loop, `r.CardID, r.RevisionID`).

In `batch`, after `checked := Check(...)`:

```go
	if len(checked.FailedRevisions) > 0 {
		if err := rn.Lib.RecordEnrichFailures(ctx, instanceID, checked.FailedRevisions); err != nil {
			return Checked{}, err
		}
	}
```

Add to `TestCheck` in `internal/enrich/enrich_test.go` an assertion that
the error and missing revisions it already builds land in
`FailedRevisions`.

- [ ] **Step 5: Run the tests**

```bash
$DB go test ./internal/library ./internal/enrich/... ./internal/api -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal
git commit -m "feat(enrich): record per-card failures and restores for the sweep"
```

---

### Task 5: The hourly sweep

**Files:** `internal/jobs/queue.go`, `internal/jobs/queue_test.go`, create
`internal/enrich/sweep.go`, `cmd/innkeeper/worker.go`, `internal/api/jobs.go`,
`internal/api/enrichments_test.go`

- [ ] **Step 1: Write the failing tests**

In `internal/jobs/queue_test.go`, following its existing `EnqueueOnce`
test:

```go
func TestEnqueueOnceForInstanceIsPerInstance(t *testing.T) {
	q := newQueue(t)
	ctx := context.Background()
	a, b := newInstanceID(t, q), newInstanceID(t, q)
	j1, err := q.EnqueueOnceForInstance(ctx, Spec{Kind: "k", InstanceID: &a})
	if err != nil {
		t.Fatal(err)
	}
	j2, _ := q.EnqueueOnceForInstance(ctx, Spec{Kind: "k", InstanceID: &a})
	j3, _ := q.EnqueueOnceForInstance(ctx, Spec{Kind: "k", InstanceID: &b})
	if j2.ID != j1.ID || j3.ID == j1.ID {
		t.Fatalf("jobs %d %d %d", j1.ID, j2.ID, j3.ID)
	}
}
```

Use the file's real helpers for a queue and an instance row.

In `internal/api/enrichments_test.go`:

```go
func TestSweepQueuesChangedCardsOncePerInstance(t *testing.T) {
	e := newEnrichEnv(t)
	e.card("Before", "x")
	e.call("PATCH", fmt.Sprintf("/instances/%d", e.instID), map[string]any{"enrich_on_change": false}, 200, nil)
	e.call("PATCH", fmt.Sprintf("/instances/%d", e.instID), map[string]any{"enrich_on_change": true}, 200, nil)
	// Inserted directly, so no edit trigger queues it: the sweep must find it.
	after := e.card("After", "x")
	if _, err := e.pool.Exec(context.Background(), `DELETE FROM job WHERE kind = $1`, enrich.Kind); err != nil {
		t.Fatal(err)
	}

	sw := &enrich.Sweeper{Lib: e.s.Lib, Registry: e.s.Registry, Queue: e.s.Queue}
	if err := sw.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := sw.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	var payloads []string
	rows, _ := e.pool.Query(context.Background(), `SELECT payload::text FROM job WHERE kind = $1 AND instance_id = $2`, enrich.Kind, e.instID)
	for rows.Next() {
		var p string
		rows.Scan(&p)
		payloads = append(payloads, p)
	}
	if len(payloads) != 1 || !strings.Contains(payloads[0], fmt.Sprint(after)) {
		t.Fatalf("payloads = %v", payloads)
	}
}
```

`e.card` goes through `POST /cards`, which doesn't queue enrich (only
edits, unarchives and imports do), so `After` is found only by the sweep.
If `POST /cards` turns out to queue, keep the `DELETE` and the test still
holds. The worker in `newEnrichEnv` may pick up the swept job; if that makes
the count flaky, build the env with the worker stopped, using whatever
option `newEnrichEnv` grows for it, rather than sleeping.

- [ ] **Step 2: Run them and see them fail**

```bash
$DB go test ./internal/jobs ./internal/api -run 'TestEnqueueOnceForInstance|TestSweep' -count=1
```

Expected: compile errors: undefined `EnqueueOnceForInstance`, `enrich.Sweeper`.

- [ ] **Step 3: `EnqueueOnceForInstance`**

In `internal/jobs/queue.go`, after `EnqueueOnce`:

```go
// EnqueueOnceForInstance returns the queued or running top-level job of
// s's kind on s's instance, or enqueues s when there is none.
func (q *Queue) EnqueueOnceForInstance(ctx context.Context, s Spec) (Job, error) {
	return q.enqueueUnless(ctx, s, `kind = $1 AND instance_id = $2 AND status IN ('queued', 'running')`, s.Kind, s.InstanceID)
}
```

- [ ] **Step 4: The sweeper**

`internal/enrich/sweep.go`:

```go
package enrich

import (
	"context"
	"errors"
	"fmt"

	"github.com/inkandale/innkeeper/internal/jobs"
	"github.com/inkandale/innkeeper/internal/library"
	"github.com/inkandale/innkeeper/internal/plugins"
	"github.com/inkandale/innkeeper/protocol"
)

// SweepKind is the hourly job that queues enrich runs for changed cards an
// instance missed.
const SweepKind = "enrich-sweep"

// Sweeper runs SweepKind jobs.
type Sweeper struct {
	Lib      *library.Library
	Registry *plugins.Registry
	Queue    *jobs.Queue
}

// Run is the jobs.Handler for SweepKind.
func (sw *Sweeper) Run(ctx context.Context, _ *jobs.Run) error { return sw.Sweep(ctx) }

// Sweep queues, for each enabled instance that updates cards as they
// change, one run over the cards changed since that it hasn't scored or
// failed on. An instance with a run queued or running is left for the
// next sweep, so runs never stack; each takes at most the plugin's card cap.
func (sw *Sweeper) Sweep(ctx context.Context) error {
	insts, err := sw.Registry.InstancesWith(ctx, protocol.CapabilityEnrich)
	if err != nil {
		return fmt.Errorf("list enrich instances: %w", err)
	}
	manifests := sw.Registry.Manifests()
	var errs []error
	for _, inst := range insts {
		if inst.Disabled || !inst.EnrichOnChange {
			continue
		}
		ids, err := sw.Lib.ChangedUnenriched(ctx, inst.ID, inst.EnrichOnChangeSince, MaxCards(manifests[inst.Plugin]))
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if len(ids) == 0 {
			continue
		}
		spec := jobs.Spec{Kind: Kind, InstanceID: &inst.ID, Payload: Payload{CardIDs: ids}}
		if _, err := sw.Queue.EnqueueOnceForInstance(ctx, spec); err != nil {
			errs = append(errs, fmt.Errorf("queue enrich on instance %d: %w", inst.ID, err))
		}
	}
	return errors.Join(errs...)
}
```

Check `InstancesWith` returns `InstanceInfo` with `Disabled`,
`EnrichOnChange` and `EnrichOnChangeSince` (it's the type Task 2 extended);
the old `embed.Sources` used it the same way.

`EnqueueOnceForInstance` matching any queued or running enrich run on the
instance also means a big bulk run the owner started holds the sweep off
until it ends. That's intended: the bulk run covers those cards.

- [ ] **Step 5: Register and label it**

In `cmd/innkeeper/worker.go`, beside the other handlers:

```go
	sweeper := &enrich.Sweeper{Lib: svc.lib, Registry: svc.registry, Queue: queue}
```

(use the queue variable the file already has), add
`enrich.SweepKind: sweeper.Run` to the handlers map, and
`{Kind: enrich.SweepKind, Every: time.Hour}` to `Schedules`.

In `internal/api/jobs.go`'s labels: `enrich.SweepKind: "Catch up changed cards"`.

- [ ] **Step 6: Run the tests**

```bash
$DB go test ./internal/jobs ./internal/enrich/... ./internal/api ./cmd/... -count=1
```

Expected: PASS. `cmd/innkeeper/worker_test.go` may list every handler or
schedule; add the sweep there if it does.

- [ ] **Step 7: Commit**

```bash
git add internal cmd
git commit -m "feat(enrich): sweep hourly for changed cards an instance missed"
```

---

### Task 6: Docs and full check

**Files:** `docs/protocol.md`

- [ ] **Step 1: Document `enrich_on_change`**

In `docs/protocol.md`, replace the `enrich_on_import` row (line ~103) with:

```markdown
| `enrich_on_change` | no; only for `enrich` | `true` makes each new instance run on the cards an import or an edit changes, up to the plugin's card cap, and an hourly sweep catches changes it missed. The owner can turn it on or off per instance. Cards changed before it was turned on are left to a run the owner starts. `enrich_on_import` is its retired name |
```

and fix the other mention (line ~1012) to say `enrich_on_change`.

- [ ] **Step 2: Run what CI runs**

```bash
go vet ./...
$DB go test -race ./...
cd web && npm run typecheck && npm test
```

Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add docs/protocol.md
git commit -m "docs: document enrich_on_change"
```
