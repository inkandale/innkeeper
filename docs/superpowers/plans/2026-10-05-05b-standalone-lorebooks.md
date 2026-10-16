# Standalone Lorebook Sync and Push Diffs Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Sync lorebooks to targets that keep them on their own, such as Lumiverse world books, with their own bindings, drift checks, and adopt, and show a diff against the app's copy before any push replaces it.

**Architecture:** The `targets` package gains a `kind` (card or lorebook) that every binding operation takes, as `library.Entities` does for storage. A target that declares `lorebooks: "standalone"` gets `target.put_lorebook` and `target.get_lorebook` calls, and each card push sends the remote IDs of the card's lorebooks. A card push first creates any linked lorebook the app doesn't have yet, which replaces nothing; it never updates a lorebook that is already bound. The push preview reads the app's current copy and lists the fields the push would change.

**Tech Stack:** Go 1.27, PostgreSQL with pgx, Huma. No new dependencies.

**Spec:** `docs/superpowers/specs/2026-10-03-innkeeper-core-design.md`, "Sync" and "Targets". Constraints from plan 5's "Plan series" section, "Standalone lorebooks".

**Kaneo:** INN-13. Blocks INN-12 (Lumiverse target).

---

## Scope

In this plan:

- Protocol: `target.put_lorebook`, `target.get_lorebook`, `lorebook_fields` in `target.describe`, `lorebook_remote_ids` in `target.put`.
- Lorebook bindings, pushes, push jobs, checks, scans, and adopt.
- Cards on standalone targets: linked lorebooks are created first; the card is ahead when its lorebooks' remote IDs change.
- Push preview for cards and lorebooks lists `changes` against the app's copy, and for cards the lorebooks the push would create.
- API: `POST /lorebooks/{id}/push`, `GET /lorebooks/{id}/push-preview`, `GET /lorebooks/{id}/bindings`. The binding routes serve both kinds.
- The fake target keeps lorebooks, so tests cover both kinds.

Left out:

| Left out | Why | Where |
|---|---|---|
| Bulk lorebook push | A bulk card push creates the lorebooks it needs; updating many lorebooks at once has no user yet | When the UI wants it |
| `target_state` filter for lorebooks | Lorebook search has no filters by target yet | Plan 7, if the UI needs it |
| Binding a lorebook the app already has (`target.list_lorebooks`) | Same gap as for cards | With `target.list` |
| Entry-level lorebook diffs | A change lists the whole `entries` array before and after; the UI can diff entries | Plan 7 |

## Design notes

- **Kind.** `kind` names a binding's column (`card_id` or `lorebook_id`), the library's `Entities`, and the plugin methods. `stored`, `Check`, `Adopt`, and `Unbind` read the kind from the binding row. `Push` and `Preview` take it.
- **Lorebook data.** A lorebook revision's `data` is a CCv3 `character_book` object, so it crosses the protocol as is. Overrides work as for cards: a merge patch keyed by instance ID.
- **Lorebook fields.** `target.describe` returns `lorebook_fields`, the `character_book` keys the app keeps, such as `name`, `description`, `entries`. `Validate` requires it when `lorebooks` is `standalone`. Lorebook drift hashes cover only these fields.
- **Card pushes on standalone targets.** Before taking the card's lock, a card push pushes each linked lorebook that is unbound on this instance, or whose binding is `missing`. Neither case replaces anything in the app. A bound lorebook is never updated by a card push: it has its own status and preview. The put key for such a lorebook is the card push's key plus `:lorebook:<id>`, so a re-run reuses the copy. Then `target.put` gets `lorebook_remote_ids`, sorted, which the app links to the card in place of any earlier links.
- **Card ahead on standalone targets.** `push.lorebook_remote_ids` records what the card push sent. The card is ahead when the remote IDs of its linked lorebooks' bindings on that instance, sorted, differ. An unbound linked lorebook has no remote ID, so linking a new lorebook makes the card ahead.
- **Lorebook ahead.** A lorebook binding is ahead when the lorebook's head `content_hash` or its override for the instance differs from the pushed revision's.
- **Status view.** `binding_status` becomes a `UNION ALL` of a card branch and a lorebook branch, with both `card_id` and `lorebook_id` columns, so the `target_state` filter keeps working.
- **Preview diff.** `Preview` renders what the push would send, projected to the target's fields, and compares it with the app's copy: read now through `get` when the target can pull, or else `accepted_data` from the last push. `changes` uses the existing `Change` type, with `before` the app's value and `after` the pushed value. `replaces` is true when the push would overwrite a copy in the app. A create, a new copy, and a push after `missing` replace nothing and list no changes. If the read fails, the preview falls back to `accepted_data` and sets `stale: true`.
- **Concurrent card pushes.** A card push checks its lorebooks' bindings before taking any lock, so two card pushes can both decide to create the same lorebook. Its lorebook pushes are create-only: inside the lorebook's lock, a push that finds the lorebook bound gives up, and the card push uses that binding's remote ID.
- **Sort order.** Go sorts remote IDs bytewise, so the view sorts them with `COLLATE "C"`. The database's default collation would order `Zed` and `abc` differently, and the card would stay `ahead`.
- **Keys.** A lorebook push job sends `<instance>:lorebook:<id>:<job id>`. A card push job keeps `<instance>:<card>:<job id>`.

## File structure

- `protocol/target.go`: lorebook methods, params, and results; `LorebookFields`; `LorebookRemoteIDs`.
- `internal/db/migrations/0005_lorebook_sync.sql`: `push.lorebook_remote_ids`; the `binding_status` view with both branches.
- `internal/targets/kind.go`: `kind`, `cardKind`, `lorebookKind`.
- `internal/targets/targets.go`, `push.go`, `check.go`, `adopt.go`, `jobs.go`: take a kind.
- `internal/targets/preview.go`: `Preview` moves here and gains the diff.
- `internal/fakeplugin/target.go`, `main.go`: lorebooks.
- `internal/api/targets.go`: lorebook routes.
- `README.md`, `docs/protocol.md`: lorebook sync.

## Tasks

### Task 1: Protocol

**Files:** Modify `protocol/target.go`, `protocol/target_test.go` (create if absent), `docs/protocol.md`.

- [ ] **Step 1: Test `Validate`.** A `standalone` description without `lorebook_fields` fails; with them it passes; `embedded` doesn't need them.
- [ ] **Step 2: Add the types.**

```go
const (
	MethodTargetPutLorebook = "target.put_lorebook"
	MethodTargetGetLorebook = "target.get_lorebook"
)

// In TargetInfo:
	// LorebookFields are the character_book keys the app keeps. Required
	// when Lorebooks is standalone.
	LorebookFields []string `json:"lorebook_fields,omitempty"`

// In TargetPutParams:
	// LorebookRemoteIDs are the card's lorebooks in the app, sorted, for a
	// target with standalone lorebooks. The app links the card to exactly these.
	LorebookRemoteIDs []string `json:"lorebook_remote_ids,omitempty"`

// TargetPutLorebookParams sends one lorebook, a CCv3 character_book. RemoteID
// and Key work as in TargetPutParams.
type TargetPutLorebookParams struct {
	Lorebook json.RawMessage `json:"lorebook"`
	RemoteID string          `json:"remote_id,omitempty"`
	Key      string          `json:"key,omitempty"`
}

// TargetGetLorebookResult is the app's copy of a lorebook.
type TargetGetLorebookResult struct {
	Lorebook json.RawMessage `json:"lorebook"`
}
```

`target.put_lorebook` replies with `TargetPutResult`; `target.get_lorebook` takes `TargetGetParams`.

- [ ] **Step 3: Document both methods** in `docs/protocol.md` under "`push` and `pull`", replacing the "reserved" wording, and `lorebook_fields` and `lorebook_remote_ids` in their tables.
- [ ] **Step 4: Run `go test ./protocol/ ./internal/contract/`** and commit.

### Task 2: Migration

**Files:** Create `internal/db/migrations/0005_lorebook_sync.sql`.

- [ ] **Step 1: Write the migration.**

```sql
-- Remote IDs of the card's lorebooks the push sent, sorted. Null unless the
-- target keeps lorebooks on their own.
ALTER TABLE push ADD COLUMN lorebook_remote_ids text[];

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
JOIN LATERAL (SELECT * FROM push WHERE binding_id = b.id ORDER BY id DESC LIMIT 1) p ON TRUE
CROSS JOIN LATERAL (
    -- A card is ahead when its data, avatar, override for this instance, or
    -- lorebooks changed since the last push.
    SELECT hr.content_hash <> pr.content_hash
        OR hr.avatar_blob IS DISTINCT FROM pr.avatar_blob
        OR hr.overrides -> b.instance_id::text IS DISTINCT FROM pr.overrides -> b.instance_id::text
        OR coalesce(p.lorebook_revision_ids <> array(
            SELECT l.head_revision_id FROM card_lorebook cl JOIN lorebook l ON l.id = cl.lorebook_id
            WHERE cl.card_id = b.card_id ORDER BY l.head_revision_id), FALSE)
        OR coalesce(p.lorebook_remote_ids <> array(
            SELECT lb.remote_id FROM card_lorebook cl
            JOIN binding lb ON lb.lorebook_id = cl.lorebook_id AND lb.instance_id = b.instance_id
            WHERE cl.card_id = b.card_id ORDER BY lb.remote_id), FALSE) AS ahead
    FROM card c JOIN card_revision hr ON hr.id = c.head_revision_id
    JOIN card_revision pr ON pr.id = p.revision_id
    WHERE c.id = b.card_id
    UNION ALL
    -- A lorebook is ahead when its data or override for this instance changed.
    SELECT hr.content_hash <> pr.content_hash
        OR hr.overrides -> b.instance_id::text IS DISTINCT FROM pr.overrides -> b.instance_id::text
    FROM lorebook l JOIN lorebook_revision hr ON hr.id = l.head_revision_id
    JOIN lorebook_revision pr ON pr.id = p.revision_id
    WHERE l.id = b.lorebook_id
) a;
```

A linked lorebook with no binding on this instance adds a NULL to the array, which no push sent, so linking a lorebook makes the card ahead. A push that sent no lorebooks records an empty array, not NULL, for the same comparison.

- [ ] **Step 2: Run `go test ./internal/db/ ./internal/targets/ ./internal/library/`**: existing status tests still pass. Commit.

### Task 3: Kinds in the targets package

**Files:** Create `internal/targets/kind.go`; modify `targets.go`, `push.go`, `check.go`, `adopt.go`, `jobs.go`.

- [ ] **Step 1: Add `kind`.**

```go
// kind is what a binding syncs: a card or a lorebook.
type kind struct {
	name     string // card or lorebook, for errors and lock names
	col      string // the binding column
	entities func(*library.Library) *library.Entities
	put, get string
}

var (
	cardKind     = kind{"card", "card_id", func(l *library.Library) *library.Entities { return l.Cards }, protocol.MethodTargetPut, protocol.MethodTargetGet}
	lorebookKind = kind{"lorebook", "lorebook_id", func(l *library.Library) *library.Entities { return l.Lorebooks }, protocol.MethodTargetPutLorebook, protocol.MethodTargetGetLorebook}
)

// fields are the data keys the target keeps for this kind.
func (k kind) fields(info protocol.TargetInfo) []string {
	if k.name == "lorebook" {
		return info.LorebookFields
	}
	return info.Fields
}
```

`kind` holds a func, so compare by `name`, not `==`.

- [ ] **Step 2: Thread the kind.** `lock`, `stored` (reads both columns and sets the kind), `fetch` (calls `k.get`; for lorebooks reads `lorebook` from the result instead of the card's `data`), `render` (lorebook: head data plus override; card on a standalone target: no embedding), `push`, `readBack`, `Check`, `adopt` (uses `k.entities`). `Binding` gains `LorebookID int64 json:"lorebook_id,omitempty"`; `CardID` becomes `omitempty`. `Push` gains `LorebookRemoteIDs []string json:"lorebook_remote_ids,omitempty"`. `Bindings` becomes `Bindings(ctx, k, id)`.
- [ ] **Step 3: Keep the public card API.** `Push(ctx, instanceID, cardID, newCopy)` stays; add `PushLorebook(ctx, instanceID, lorebookID)`.
- [ ] **Step 4: Run the whole targets suite.** Nothing about cards changes, so every existing test passes unchanged except for the signature of `Bindings`. Commit.

### Task 4: Fake target lorebooks

**Files:** Modify `internal/fakeplugin/target.go`, `internal/fakeplugin/main.go`.

- [ ] **Step 1:** Config `lorebook_fields`; default `["name", "description", "entries"]` when `lorebooks` is `standalone`.
- [ ] **Step 2:** `put_lorebook` and `get_lorebook` store `<dir>/l<n>.json` as `{"lorebook": {...}, "key": "..."}`, keeping only `lorebook_fields`, honoring `remote_id` (`-32004` when gone) and `key` as cards do. `byKey` searches only the files of the kind it's asked for.
- [ ] **Step 3:** `put` stores `lorebook_remote_ids` in the card file, so tests can see the links.
- [ ] **Step 4:** Dispatch the two methods in `main.go`. Commit.

### Task 5: Lorebook push, check, and adopt

**Files:** Test `internal/targets/lorebook_test.go`.

- [ ] **Step 1: Write the tests** with `setup(t, map[string]any{"lorebooks": "standalone"})` and a helper `e.newLorebook(name)`:
  - `TestPushLorebook`: a push creates `l1` with only the lorebook fields; the binding is `in_sync`; editing the lorebook makes it `ahead`; pushing again updates `l1`.
  - `TestCheckLorebook`: editing `l1`'s entries makes the binding `remote_changed` with one change on `entries`; `Adopt(... IntoBase ...)` writes a lorebook revision with source `adopt` and the binding becomes `ahead`.
  - `TestLorebookOverride`: an override `{"<instance>": {"description": "x"}}` reaches the app, and adopting into the override keeps the lorebook's own data.
- [ ] **Step 2: Run, watch them fail, implement until they pass.** Commit.

### Task 6: Cards on standalone targets

**Files:** Test `internal/targets/lorebook_test.go`.

- [ ] **Step 1: Write the tests.**
  - `TestCardPushCreatesItsLorebooks`: a card linked to two lorebooks pushes; both lorebooks get bindings; the card file holds both remote IDs, sorted; the card push's `last_push.lorebook_remote_ids` matches.
  - `TestCardPushLeavesBoundLorebooksAlone`: a lorebook pushed, then edited; the card push doesn't update `l1`, and the lorebook stays `ahead`.
  - `TestCardAheadWhenLorebooksChange`: linking a third lorebook makes the card `ahead`; so does a lorebook binding going `missing` and being pushed again under a new remote ID.
  - `TestCardPushRerunReusesLorebooks`: with the fake's `fail_put: crash` for the card put only (new fake mode `crash_card`), a re-run with the same key makes no second lorebook.
- [ ] **Step 2: Implement** in `push`: when `k` is the card kind and `info.Lorebooks` is standalone, before the transaction, push each linked lorebook whose binding here is absent or `missing`, with key `key + ":lorebook:" + id`, then collect the sorted remote IDs. Pass them in `TargetPutParams` and record them in `push.lorebook_remote_ids`.
- [ ] **Step 3: Run and commit.**

### Task 7: Preview diff

**Files:** Create `internal/targets/preview.go` (move `Preview` there); test `internal/targets/preview_test.go`.

- [ ] **Step 1: Write the tests.**
  - Unbound card: `replaces` false, no changes.
  - Pushed card, then edited `first_mes` in the library: one change, `before` the app's value, `after` the new value, `replaces` true.
  - Pushed card, edited in the app: the change shows the app's edit being overwritten.
  - Push-only target: compares against `accepted_data`.
  - Card on a standalone target with an unbound linked lorebook: `create_lorebooks` lists it.
  - Lorebook preview after an entries edit: one change on `entries`.
- [ ] **Step 2: Implement.**

```go
// Preview is what a push would send, for the owner to confirm first.
type Preview struct {
	// Card or Lorebook is what the target would get.
	Card     json.RawMessage `json:"card,omitempty"`
	Lorebook json.RawMessage `json:"lorebook,omitempty"`
	// DroppedFields are fields with content that the target doesn't store.
	DroppedFields []string `json:"dropped_fields"`
	// NewCopy means the push makes another copy, because the target can't
	// update the one it has.
	NewCopy bool `json:"new_copy"`
	// Replaces means the push overwrites the app's copy; Changes lists what
	// it changes there. Before is the app's value, After the pushed one.
	Replaces bool     `json:"replaces"`
	Changes  []Change `json:"changes"`
	// Stale means the app couldn't be read, so Changes compare against what
	// the last push sent.
	Stale bool `json:"stale,omitempty"`
	// CreateLorebooks are linked lorebooks the push creates in the app first.
	CreateLorebooks []int64 `json:"create_lorebooks,omitempty"`
}
```

- [ ] **Step 3: Run and commit.**

### Task 8: Jobs and API

**Files:** Modify `internal/targets/jobs.go`, `internal/api/targets.go`; test `internal/api/targets_test.go`.

- [ ] **Step 1:** `pushPayload` gains `LorebookID int64 json:"lorebook_id,omitempty"`; `EnqueueLorebookPush`; `RunPush` picks the kind from the payload.
- [ ] **Step 2:** Routes `POST /lorebooks/{id}/push`, `GET /lorebooks/{id}/push-preview`, `GET /lorebooks/{id}/bindings`. The adopt route's description says lorebook bindings adopt into the lorebook.
- [ ] **Step 3:** API test: push a lorebook through the route, run the job, list its bindings, preview it. Commit.

### Task 9: Docs

- [ ] **Step 1:** README "Syncing" gains lorebooks: standalone targets, the two-step push, `create_lorebooks`, and the preview's `changes`.
- [ ] **Step 2:** Run `go test -race ./...` and commit.

## Plan series

Constraints this plan leaves for later plans:

- **Plan 6c (Lumiverse target):** declare `lorebooks: "standalone"` and `lorebook_fields`. `target.put` links the character to exactly `lorebook_remote_ids` through `extensions.world_book_ids`, keeping any world book the owner linked in Lumiverse that Innkeeper didn't make out of it only if the plugin can tell them apart; otherwise replace the list. `target.put_lorebook` creates or replaces a world book's entries.
- **Plan 7 (UI):** show `changes` from the push preview before every push that `replaces`, and `create_lorebooks` before a card push. Lorebook bindings have their own status and push button.
