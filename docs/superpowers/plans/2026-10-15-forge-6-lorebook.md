# Forge Phase 6 (core): Actions on the Lorebook Page Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The lorebook page runs plugin actions as the card page does: tabs, Reforge and ⌘K, field menus, the running header, locks, Stop, pickup after a reload, Report, and a Review with one row per changed entry.

**Architecture:** INN-59, the core half of phase 6 of `docs/superpowers/specs/2026-10-14-forge-design.md`. The forge-ai plugin's `alternate-greetings` and `lore-entries` actions are a later task in `innkeeper-plugins`. The Forge components stay one set: what differs between a card and a lorebook moves into a **subject**, passed to `useForge` and read from the Forge state by every component.

```ts
/** Subject is what the Forge works on: the entity kind, how its data keys are named, and how a proposal is reviewed. */
export type Subject = {
  kind: "card" | "lorebook";
  label: (key: string) => string;
  review: (start: Data, draft: Data, proposal: Data) => Proposed;
};
/** Row is one change Review offers. A missing mine or after shows as (empty). */
export type Row = { key: string; label: string; change?: "added" | "removed" | "changed"; mine?: string; before: string; after?: string; edited: boolean };
/** Proposed is a proposal's rows, and the draft changes taking some of them makes. */
export type Proposed = { rows: Row[]; apply: (taken: Set<string>) => Data };
```

**Tech Stack:** React, TypeScript, TanStack Query, Vitest and Testing Library; Go and PostgreSQL.

**Tests:** Go DB tests run with `INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres`. Web tests run with `npm test` in `web/`.

**Decisions:**
- **Entry matching.** CCv3 entries have an optional `id`, and imported books often lack it. Review pairs the run's starting entries with the proposal's, and with the draft's, in three passes: the same `id`; then identical entries; then the same `keys`. What stays unpaired is added or removed. A changed entry with neither an `id` nor its old keys shows as one removed and one added; taking both still gives the proposal's entry. `docs/protocol.md` asks a lorebook action to keep each entry's `id` and leave it off new entries.
- **Entry fields.** A field action names lorebook data keys only, such as `description`. No per-entry field actions: the protocol's `field` is one data key and scope compares whole keys, so a per-entry action needs a new selector in the protocol. Nobody needs it yet; `lore-entries` is a whole-lorebook action.
- **Description moves to the main column.** The Fields tab holds the Description section, with its hammer menu, then the entries. The aside keeps the name, as the card page's panel keeps identity fields. A field menu lives inside the Forge component, which owns the Reforge panel and the tabs it opens.
- **Context.** No default context. The spec says the owner picks what travels to a third party, and a lorebook can link many cards. The owner attaches the card with `@cards/` in the instruction box, so `lore-entries` declares `instruction: true` and is the lorebook's revise action.
- **Pickup.** `GET /jobs` takes `lorebook_id`, matching the payload's `lorebook_id` that phase 2 already stores.
- **Locks.** A whole-lorebook run locks the name, the description, and the entries. A field action locks its field. The entries lock as one: `entries` is one data key.

---

## File map

| File | Change |
|---|---|
| `internal/api/jobs.go`, `internal/api/actions_test.go` | `lorebook_id` filter on `GET /jobs` |
| `internal/fakeplugin/actions.go` | `lore` (whole lorebook, instruction) and `tighten-lore` (lorebook field action on `description`) |
| `docs/protocol.md` | Entry `id`s in a lorebook proposal |
| `web/openapi.json`, `web/src/api/schema.d.ts` | Regenerated |
| `web/src/forge/review.ts`, `review.test.ts` | New: `Subject`, `Row`, `Proposed`, `fieldRows`, `cardSubject` |
| `web/src/lorebook/review.ts`, `review.test.ts` | New: `pairEntries`, `lorebookSubject` |
| `web/src/forge/actions.ts` | `instructionAction(list, kind)` |
| `web/src/forge/useForge.ts` | Takes `subject` and `id`; job list by `card_id` or `lorebook_id` |
| `web/src/forge/Forge.tsx`, `Reforge.tsx`, `Review.tsx`, `Report.tsx`, `RunHeader.tsx` | Read `forge.subject`; Forge takes a `fields` render prop |
| `web/src/card/Fields.tsx` | Export `Section` |
| `web/src/card/Card.tsx`, `NewCard.tsx` | Pass `cardSubject` and the Fields render prop |
| `web/src/lorebook/Lorebook.tsx`, `EntryEditor.tsx` | Render the Forge; entries take a lock |
| `web/src/lorebook/Lorebook.test.tsx`, `web/src/forge/Review.test.tsx` | Tests |

---

### Task 1: Plan

- [ ] Commit this plan: `docs: plan the Forge on the lorebook page`.

### Task 2: Find a lorebook's runs

**Files:** `internal/api/jobs.go`, `internal/api/actions_test.go`, regenerated schema.

- [ ] **Step 1: Failing test.** In `actions_test.go`, start a lorebook run with the fake plugin's `tighten-lore` (added in Task 3, so do Task 3's declaration first) on a lorebook, then:

```go
e.call("GET", fmt.Sprintf("/jobs?kind=action&lorebook_id=%d", book.ID), nil, 200, &list)
// one job, the lorebook run; the card filter doesn't find it
```

- [ ] **Step 2:** Run `go test ./internal/api -run TestActionRun` with the database URL: fails, the filter is ignored.
- [ ] **Step 3: Implement.**

```go
LorebookID int64 `query:"lorebook_id" doc:"Only top-level jobs whose payload names this lorebook, such as its action runs"`
...
if in.LorebookID != 0 {
	o.Payload = fmt.Appendf(nil, `{"lorebook_id": %d}`, in.LorebookID)
}
```

A request with both `card_id` and `lorebook_id` matches nothing, since no payload names both; reject it with `400` rather than silently using one.
- [ ] **Step 4:** Tests pass; `cd web && npm run gen`.
- [ ] **Step 5:** Commit `feat(api): find a lorebook's action runs`.

### Task 3: Fake lorebook actions

**Files:** `internal/fakeplugin/actions.go`, `docs/protocol.md`.

- [ ] Declarations:

```go
{ID: "lore", Label: "Rework lore", Description: "Reworks the entries from an instruction.", Applies: "lorebook", Instruction: true, Inputs: fakeInputs},
{ID: "tighten-lore", Label: "Tighten", Description: "Shortens a field.", Applies: "lorebook", Fields: []string{"description"}},
```

- [ ] Runs: `tighten-lore` is `tighten`'s case. `lore` changes the first entry's content to `Reworked: …`, removes the second, and appends `{"keys": ["new"], "content": <instruction>, "enabled": true, "insertion_order": 100, "extensions": {}}`, so one run shows each kind of row.
- [ ] `docs/protocol.md`, after the scope list: a lorebook proposal keeps each entry's `id`, leaves it off new entries, and the Forge matches entries without one by content, then by keys.
- [ ] `go test ./internal/contract ./internal/fakeplugin/...` passes: the contract suite runs every listed action, and `lore` on the sample lorebook's empty entries only appends.
- [ ] Commit `feat(fakeplugin): offer lorebook actions` and `docs(protocol): keep entry ids in a lorebook proposal` (or one commit with both if small).

### Task 4: Subjects and review rows

**Files:** `web/src/forge/review.ts`, `review.test.ts`, `web/src/lorebook/review.ts`, `review.test.ts`.

- [ ] **Step 1: Failing tests.**
  - `cardSubject.review`: rows for changed keys only; `edited` when the draft differs from start; `apply(new Set(["description"]))` returns `{description: proposal.description}`.
  - `pairEntries`: pairs by `id` across a reorder; pairs identical id-less entries; pairs id-less entries with the same keys and changed content; leaves a new entry and a removed one unpaired.
  - `lorebookSubject.review`: start `[anvil, bellows, forge]`, proposal `[anvil′, forge, hammer]` gives rows `changed` (anvil), `removed` (bellows), `added` (hammer), plus a `description` row when it changed; taking only the added row appends hammer to the draft's entries and leaves the rest; taking the removed row drops bellows from the draft; a changed row whose draft entry was edited is `edited`; an entry the owner added during the run survives Apply.
- [ ] **Step 2:** `npm test -- review` fails.
- [ ] **Step 3: Implement** `fieldRows(start, draft, proposal, label, keys)` and `cardSubject` in `forge/review.ts`; `pairEntries(a, b): [number, number][]` (`-1` for no partner) and `lorebookSubject` in `lorebook/review.ts`. An entry row's text is its keys, secondary keys, then content, then any other changed property as `key: JSON`, so a toggled `enabled` still shows. Apply builds the entries from the draft's list: replace a taken changed entry's draft partner (append it if the owner deleted it), drop a taken removed entry's partner, append taken added entries.
- [ ] **Step 4:** Tests pass. Commit `feat(web): review a lorebook proposal entry by entry`.

### Task 5: The Forge takes a subject

**Files:** `web/src/forge/*`, `web/src/card/Card.tsx`, `NewCard.tsx`, `Fields.tsx`.

- [ ] `useForge({subject, id, data})`: posts `entity: {kind: subject.kind, id, data}`; lists jobs with `{kind: "action", card_id | lorebook_id: id}` under query key `["jobs", "action", subject.kind, id]`; returns `subject`.
- [ ] `instructionAction(list, kind)` and the other filters on `applies === "card"` use `forge.subject.kind`.
- [ ] `Review` takes `proposed: Proposed` instead of `start`, `draft`, `proposal`; the tab count is `rows.length`.
- [ ] `Forge` takes `fields: (lock, extra) => ReactNode` for the Fields tab. Report, Reforge, RunHeader, and field menus name keys with `forge.subject.label` and the entity with `forge.subject.kind` ("Reforge this lorebook…", "Or change the whole lorebook", "The whole lorebook").
- [ ] Card and NewCard pass `cardSubject` and `(lock, extra) => <Fields … lock={lock} extra={extra} />`.
- [ ] Existing card tests pass unchanged, except `Review.test.tsx`, which builds its `proposed` with `cardSubject.review`.
- [ ] Commit `refactor(web): give the Forge a subject so it can work on lorebooks`.

### Task 6: The lorebook page

**Files:** `web/src/lorebook/Lorebook.tsx`, `EntryEditor.tsx`, `Lorebook.test.tsx`, `web/src/card/Fields.tsx`.

- [ ] **Step 1: Failing tests** in `Lorebook.test.tsx`, with `/api/actions` serving a card `reforge`, a lorebook `lore` (instruction), and a lorebook `tighten-lore` on `description`:
  - A running `tighten-lore` job from `GET /jobs?lorebook_id=3` locks Description and leaves the entries and name editable; a running `lore` job locks all three.
  - ⌘K opens Reforge; typing an instruction and Enter posts `action: "lore"`, `entity.kind: "lorebook"`, `entity.id: 3`.
  - A finished `lore` job opens Review with added, removed, and changed rows; keeping the removed row and applying adds the new entry and changes the first while the second stays.
- [ ] **Step 2:** Fail.
- [ ] **Step 3: Implement.** Export `Section` from `card/Fields.tsx`. `Entries` takes `lock?: string`, wrapping its rows and toolbar in a disabled fieldset with the "locked while … runs" note. The page renders `<Forge forge={forge} data dirty edit fields={…} />` whose Fields tab is a Description `Section` with `extra("description")`, then `Entries`. The aside's name input is disabled by `forge.lock("name")`.
- [ ] **Step 4:** Tests pass. Commit `feat(web): run Forge actions on the lorebook page`.

### Task 7: Check and try it

- [ ] CI: `go vet ./... && go test -race ./...` with the database URL; `cd web && npm run typecheck && npm test`.
- [ ] Browser: run the server on a free port with the fake plugin as an action instance; on a lorebook, run Tighten on the description and Rework lore with `@cards/…`; reload mid-run (pickup); Stop; Review taking some rows; Save. Screenshots `p6-*.png` in the session scratchpad.
- [ ] Reread the diff.
