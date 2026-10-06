# Reconcile Job Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A read-only job that lines every item in an app up against the library and records a snapshot: a verdict, candidates, options, and a suggestion per item, with linked world books nested under their characters.

**Architecture:** `internal/targets/reconcile.go` adds job kind `target.reconcile`. It reads every app item through the plugin, compares it with the library's head revisions in memory, and writes the snapshot as the job's final progress. `CompareItem` gives the full field-by-field comparison of one pair, live, for the compare endpoint and for resolve.

**Tech Stack:** Go, pgx. Uses `internal/merge` (13c) and sources (13d).

**Spec:** `2026-10-05-reconcile-design.md`, Reconcile job.

**Kaneo:** INN-27, part of INN-22.

---

## Design notes

- **Inputs.**
  ```go
  type ReconcileParams struct {
  	InstanceID int64 `json:"instance_id"`
  	Match      struct {
  		Name           *bool `json:"name"`            // default true
  		TextDistance   *int  `json:"text_distance"`   // default 8, 0..32
  		AvatarDistance *int  `json:"avatar_distance"` // default 8, 0..32
  	} `json:"match"`
  	SplitFields *int `json:"split_fields"` // default 3, 1..4
  }
  func (s *Service) EnqueueReconcile(ctx, q *jobs.Queue, p ReconcileParams) (jobs.Job, error)
  func (s *Service) RunReconcile(ctx, r *jobs.Run) error
  ```
  The target must declare `pull`; otherwise `ErrUnsupported`, as import does.
- **Comparing like with like.** An app stores only some fields, and `Compare` and hashes must see the same fields on both sides. The library side is the head revision rendered for this instance (`render`, override applied) and then projected to the target's fields with `Project`. The app side is `fetch`'s projected data. Exact means equal `cardhash.ContentHash` of those two.
- **Library index.** Load every unarchived card's head once: ID, name, base data, `text_simhash`, avatar `avatar_phash`, and the instance's bindings. Precompute the projected hash for each. ponytail: in memory and pairwise, as the spec allows at library scale; move the distance search to SQL if a library outgrows it.
- **Per item.**
  1. `target.get` with `avatar: true`. A read error makes the item `error`, and the run continues.
  2. Move the avatar into the blob store (`IngestBlob`) to get its phash. Blob GC removes it later if nothing references it.
  3. `bound` if a binding on this instance has this remote ID: compare against the binding's `accepted_data` as ancestor.
  4. Else `exact` for every card with the same projected hash.
  5. Else `likely` for cards matched by source (`library.Detect` on the app data, then `BySource`), normalized name, `text_simhash` distance, or `avatar_phash` distance, within the run's settings. Sort: shared source first, then number of signals matched, then total distance, then card ID.
  6. Else `new`.
  A candidate already bound to another remote ID on this instance is kept, with `bound_to` set, so the owner can see the duplicate.
- **Suggestion.** For the top `likely` candidate, run `merge.Compare` and count the core fields (`description`, `personality`, `scenario`, `first_mes`) with any non-`same` hunk: without an ancestor, added or removed text is `app_only` or `library_only`, not `conflict`. Fewer than `split_fields`: suggest `bind`; otherwise `import`, with the reason. `exact`: `bind`. `new`: `import`. `bound` with any non-`same` hunk: `bind`, to resolve the drift; `bound` with none: no suggestion.
- **Lorebooks.** World books are reconciled with the same steps using lorebook data, `LorebookSimhash`, and `merge.Lorebook`, with no avatar signal. A world book in a character's `lorebook_remote_ids` is nested under that character. One linked by no character is a top-level item. Each world book is read once per run.
- **Snapshot**, the job's final progress:
  ```go
  type Snapshot struct {
  	Params    ReconcileParams  `json:"params"`
  	Items     []ReconcileItem  `json:"items"`     // characters
  	Lorebooks []ReconcileItem  `json:"lorebooks"` // world books no character links to
  	Missing   []int64          `json:"missing"`   // card IDs bound here whose app item is gone
  	Unpushed  []int64          `json:"unpushed"`  // card IDs with no binding here
  	Totals    map[string]int   `json:"totals"`    // by verdict
  }
  type ReconcileItem struct {
  	RemoteID   string          `json:"remote_id"`
  	Name       string          `json:"name"`
  	AppHash    string          `json:"app_hash"`
  	Verdict    string          `json:"verdict"`
  	Error      string          `json:"error,omitempty"`
  	BindingID  int64           `json:"binding_id,omitempty"`
  	Candidates []Candidate     `json:"candidates,omitempty"`
  	Options    []Option        `json:"options"`
  	Suggested  *Option         `json:"suggested,omitempty"`
  	Lorebooks  []ReconcileItem `json:"lorebooks,omitempty"`
  }
  type Candidate struct {
  	CardID          int64          `json:"card_id"` // or the lorebook's ID
  	Name            string         `json:"name"`
  	Reasons         map[string]int `json:"reasons"` // source, name, content: 0; text, avatar: distance
  	DifferingFields int            `json:"differing_fields"`
  	Conflicts       int            `json:"conflicts"`
  	BoundTo         string         `json:"bound_to,omitempty"`
  }
  type Option struct {
  	Action string `json:"action"` // bind, import, skip
  	CardID int64  `json:"card_id,omitempty"`
  	Why    string `json:"why,omitempty"`
  }
  ```
  While running, progress is `{"done": n, "total": m}`.
- **Comparing one pair, live.**
  ```go
  type Comparison struct {
  	InstanceID int64         `json:"instance_id"`
  	RemoteID   string        `json:"remote_id"`
  	CardID     int64         `json:"card_id"`
  	Base       string        `json:"base"` // none or accepted
  	AppHash    string        `json:"app_hash"`
  	Fields     []merge.Field `json:"fields"`
  	Avatar     *AvatarDiff   `json:"avatar,omitempty"` // when the two differ
  }
  type AvatarDiff struct {
  	Library string `json:"library,omitempty"` // blob sha256
  	App     string `json:"app,omitempty"`
  	PhashDistance *int `json:"phash_distance,omitempty"`
  }
  func (s *Service) CompareItem(ctx, instanceID int64, remoteID string, cardID int64) (Comparison, error)
  func (s *Service) CompareLorebookItem(ctx, instanceID int64, remoteID string, lorebookID int64) (Comparison, error)
  ```
  `cardID` 0 means the card bound to `remoteID`; none is `ErrNotFound`.

## Tasks

### Task 1: Library index

**Files:** `internal/targets/reconcile_index.go`, `internal/targets/reconcile_index_test.go`

- [ ] Test, against the test database: two cards, one bound to the fake instance; the index has both with projected hashes equal to `ContentHash(Project(render(…)))`, the simhash, the phash, and the binding.
- [ ] Implement `loadIndex(ctx, k kind, inst, info) (index, error)` and the name normalizer (`strings.Fields(strings.ToLower(…))` joined by one space). Commit: `feat(targets): index library heads for reconcile`.

### Task 2: Verdicts

**Files:** `internal/targets/reconcile.go`, `internal/targets/reconcile_test.go`

- [ ] Tests with the fake plugin, one per verdict: a bound item, an exact copy, a copy with one edited sentence and the same name (`likely`, reason `name`), a copy renamed but with the same text (`likely`, reason `text`), a copy with `extensions.chub` matching a library card's source (`likely`, ranked first), an unrelated card (`new`), an item the fake fails to read (`error`, the run still succeeds).
- [ ] Test: `match.name: false` moves the name-only item to `new`; `text_distance: 0` moves the text-only item to `new`.
- [ ] Test: a candidate bound to another remote ID has `bound_to`.
- [ ] Implement per the design notes. Commit: `feat(targets): give every app item a reconcile verdict`.

### Task 3: Suggestions and options

- [ ] Tests: an item differing in 1 core field suggests `bind`; one differing in 3 suggests `import` with a `why`, and still lists `bind` to every candidate among its options; `split_fields: 4` turns that one back to `bind`; `exact` suggests `bind`; `new` suggests `import`; every item lists `skip`.
- [ ] Implement. Commit: `feat(targets): suggest an action for each reconciled item`.

### Task 4: Lorebooks, missing, unpushed

- [ ] Tests with a standalone-lorebook fake: a character linking two world books has both nested with their own verdicts; a world book linked by two characters is read once (count fake calls) and nested under both; an unlinked world book is top-level; a bound card whose remote ID the app dropped is in `missing`; a never-pushed card is in `unpushed`.
- [ ] Implement. Commit: `feat(targets): reconcile world books inside the characters that link them`.

### Task 5: The job

**Files:** `internal/targets/jobs.go`, `cmd/innkeeper/worker.go` (registration), tests

- [ ] `ReconcileKind = "target.reconcile"`, registered with the worker alongside `ImportKind`.
- [ ] Test: run the job through the worker; while running, progress has `done` and `total`; when finished, progress is the `Snapshot`, and `Totals` add up to the items.
- [ ] Test: a target without `pull` is refused at enqueue.
- [ ] Commit: `feat(targets): run reconcile as a job`.

### Task 6: CompareItem

- [ ] Tests: an unbound pair gives `base: none` and the expected hunks; a bound pair gives `base: accepted` with `auto_app` hunks for app-only edits; avatars that differ give `AvatarDiff` with a distance; equal avatars give none; `cardID` 0 on an unbound item is `ErrNotFound`.
- [ ] Implement, sharing the projection and `merge.Compare` call with the job. Commit: `feat(targets): compare one app item with one library card`.
- [ ] Push, open the PR.
