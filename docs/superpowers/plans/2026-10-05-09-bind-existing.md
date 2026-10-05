# Bind Existing App Items Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the owner link a library card or lorebook to a character or world book the app already has, instead of pushing a new copy. Later pushes, checks, and adopts use that item.

**Architecture:** Plugins list what the app has with `target.list` and `target.list_lorebooks`. Binding reads the chosen item with `target.get` and records the library's copy, limited to the target's fields, as the binding's accepted data. A check then compares the app with that. If they match, the binding is `in_sync`. If they differ, it's `remote_changed`, with the differing fields as its changes, and the owner adopts them or pushes over them. A push shows the diff first, as it does today.

**Tech Stack:** Go, pgx, huma. No new dependencies.

**Spec:** `docs/superpowers/plans/2026-10-04-05-sync-and-drift.md`, "Plan series", last bullet.

**Kaneo:** INN-15.

---

## Design notes

- **Bind row in `push`.** Binding writes a `push` row with a new `kind` column, `push` or `bind`. Its revision is the library's head, and `pushed_hash` is the hash of the library's copy limited to the target's fields. `binding_status` and `bindingQuery` keep their inner join, and "ahead" means the library changed since the bind, the same as since a push. This replaces the plan-5 note's left join, which couldn't tell when the library changed after a bind. `GET /bindings/{id}/pushes` shows `kind`, so the history doesn't claim a push that never happened.
  - Embedded lorebooks: the row records the lorebooks' head revisions, as a push would. `character_book` is in the compared fields, so a difference there shows as a change.
  - Standalone lorebooks: the core can't see which world books the app links to a character, so the row's `lorebook_remote_ids` is empty. A card with lorebooks then shows as ahead, and the next push links them.
- **Needs pull.** Binding reads the app, so the target must have `pull: true`. Otherwise `ErrCannot`.
- **Refusals.**
  - The card or lorebook is already bound on that instance: `409`, from the existing unique constraint.
  - Another binding on the instance already uses that `remote_id`: `409`. Add `UNIQUE (instance_id, remote_id)` to `binding`. Before adding it, the migration checks for duplicates and fails with their IDs, since today's data can't have them unless a create-only target reused an ID.
  - The app has no such item: `404`.
- **Listing.** `target.list` and `target.list_lorebooks` take no params and reply `{"items": [{"remote_id": "…", "name": "…"}]}`. The plugin pages through the app itself. Both are part of `pull`: a target that claims `pull` must answer them, and the contract check calls them. The core's reply marks each item with the binding ID that already uses it, if any.
- **Routes.**
  - `GET /instances/{id}/items` and `GET /instances/{id}/lorebook-items`: `read`.
  - `POST /cards/{id}/bind` and `POST /lorebooks/{id}/bind`, body `{"instance_id": …, "remote_id": "…"}`: `write`. Binding changes nothing in the app or the library. It returns the binding after its first check.
- **MCP.** `list_app_items` (`read`) and `bind` (`write`) call those routes.
- **No UI.** The core has no UI to pick from yet. The owner picks through the CLI's `innkeeper call`, or through MCP.

## Tasks

### Task 1: Protocol

- [ ] `protocol`: `MethodTargetList`, `MethodTargetListLorebooks`, `TargetListResult`, `TargetItem`.
- [ ] `internal/fakeplugin/target.go`: both methods, listing what it stores.
- [ ] `internal/contract`: a `pull` target must answer both.
- [ ] `docs/protocol.md`: the two methods under "`push` and `pull`".
- [ ] Commit.

### Task 2: Storage

- [ ] Migration `0008_bind.sql`: `push.kind text NOT NULL DEFAULT 'push' CHECK (kind IN ('push', 'bind'))`; the duplicate check; `UNIQUE (instance_id, remote_id)` on `binding`.
- [ ] Migration test: an existing push becomes `kind = 'push'`.
- [ ] Commit.

### Task 3: `targets.Service.Bind`

- [ ] `Bind(ctx, instanceID, cardID, remoteID)` and `BindLorebook`, on the shared `kind` path that `push` uses: describe, require pull, render, fetch, insert the binding and the bind row, then `Check`.
- [ ] `Items(ctx, instanceID)` and `LorebookItems`, marking items already bound.
- [ ] `Push` has `kind` in its JSON.
- [ ] Tests, with the fake plugin:
  - The app matches the library: `in_sync`.
  - The app differs: `remote_changed`, with the fields that differ. Adopt takes them in. A push previews them as `Changes` and then overwrites them.
  - The library changes after a bind: `ahead`. Both: `diverged`.
  - A standalone-lorebook card with lorebooks shows `ahead` after a bind.
  - Each refusal in the design notes, and a target without pull.
- [ ] Commit.

### Task 4: API and MCP

- [ ] The four routes in `internal/api/targets.go`, with tier tests.
- [ ] `list_app_items` and `bind` in `internal/mcp/tools.go`.
- [ ] Commit.

### Task 5: Lumiverse plugin (`innkeeper-plugins`)

- [ ] `target.list` over `/characters/summary` and `target.list_lorebooks` over `/world-books`, both paged with `limit` and `offset` like `findBook` in `books.go`. `findCharacter` asks for one page of 1000; check whether `/characters/summary` takes `offset` too.
- [ ] Fake-server tests; the live test lists, binds, and checks one character.
- [ ] Commit and open a PR there; bump the pinned core version once this core work merges.

### Task 6: Docs

- [ ] README: binding an item the app already has, with an `innkeeper call` example.
- [ ] `2026-10-04-05-sync-and-drift.md`: point the last "Plan series" bullet at this plan.
- [ ] Commit.
