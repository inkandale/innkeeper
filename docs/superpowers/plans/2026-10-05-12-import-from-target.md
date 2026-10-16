# Import From a Target Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Take an app's characters and lorebooks into the library, each bound to the item it came from, so the owner can start managing an existing SillyTavern library in Innkeeper.

**Architecture:** `POST /imports` on an instance of a push and pull target starts a bulk job with one child per unbound app item. A child reads the item with `target.get`, finds or creates the library card, and binds it with the existing bind path, so it starts `in_sync`. Plugins can now return the avatar and the lorebooks the app links to the card.

**Tech Stack:** Go, pgx, huma. No new dependencies.

**Kaneo:** INN-18. Builds on INN-15 (`2026-10-05-09-bind-existing.md`).

---

## Design notes

- **Protocol.**
  - `target.get` takes `avatar: true` to ask for the image as well. The plugin writes it to `blob_tmp` and replies with its path in `avatar`, as importers do. Only imports ask, so the hourly check doesn't download every image.
  - `target.get` may reply with `lorebook_remote_ids` for a `standalone` target: the lorebooks the app links to the card. Absent means unknown.
- **Bind records the app's lorebook links.** When `target.get` returns `lorebook_remote_ids`, a bind records them, sorted, on the bind row instead of `[]`. A card whose library lorebooks are bound to those same app lorebooks is then not ahead. This applies to INN-15's manual bind too.
- **One route.** `POST /imports` already starts imports. For an instance whose plugin has `push` and `pull` but not `import`, it starts a target import instead. `params` takes an optional `remote_ids`; without it, every unbound card and lorebook is imported. A remote ID in neither list is a `400`. The CLI's `innkeeper import` and MCP's `start_import` work unchanged; `start_import` gains `remote_ids`.
- **Job shape.** The route lists the app with `target.list` and `target.list_lorebooks`, skips items already bound on the instance, and queues a bulk job with one `target.import` child per item. Its payload counts the skipped items as `already_bound`. Each child reports an `outcome` in its progress:

  | Outcome | Meaning |
  |---|---|
  | `created` | A new library card or lorebook, bound to the item |
  | `bound` | A library item with the same content, now bound to the app item |
  | `already_bound` | The app item got a binding after the job was queued |
  | `duplicate` | Every library item with this content is already bound to another app item on this instance, such as a character duplicated in SillyTavern. Nothing changes |
  | `missing` | The app no longer has the item |

  A bulk job's response gains `outcomes`, its children counted by outcome, next to `children`.
- **Finding the library item.** A card's content hash is of its data without `character_book`, as the importer stores it.
  1. A card whose head revision has that hash and isn't bound on the instance: bind it (`bound`). Prefer unarchived cards, then the oldest.
  2. Otherwise, if such cards exist but all are bound here: `duplicate`.
  3. Otherwise, import it through the importer's path, with provenance `<plugin>:<remote_id>` (`created`, or `bound` if that provenance already had the content).

  Lorebooks follow the same steps.
- **Shared ingest.** The importer's `ingestCard`, which splits an embedded `character_book` into a linked lorebook and stores the avatar, moves to `library.ImportCard` so both imports use it.
- **Lorebooks first, in any order.** A card child imports each lorebook in the card's `lorebook_remote_ids` before the card, through the same function a lorebook child uses, so it doesn't depend on the order children run in. Links are added only to a card the import created. A matched existing card keeps its links, and shows as ahead if they differ from the app's.
- **Concurrency.** Two children with the same content would both miss the lookup and create two cards. A child takes a transaction-level advisory lock on the content hash before the lookup and holds it through the bind. It takes no lock while importing lorebooks, so it holds at most one locked connection. The worker's pool, `2 × concurrency + 2`, already allows that, as it does for push.
- **Re-runs.** A child that finds a binding for its remote ID reports `already_bound`. One that created a card but died before binding finds that card on the re-run and reports `bound`.
- **Plugins.** SillyTavern returns the original image from `/api/characters/export` with format `png`, and `extensions.world` as `lorebook_remote_ids`. Lumiverse can follow; its import works without avatars until then.

## Tasks

### Task 1: Protocol

- [ ] `protocol/target.go`: `TargetGetParams.Avatar`; `TargetGetResult.Avatar` and `LorebookRemoteIDs`.
- [ ] `internal/fakeplugin/target.go`: with `avatar`, copy the stored avatar into `blob_tmp` and reply with it; reply with stored `lorebook_remote_ids`.
- [ ] `docs/protocol.md`: both additions under `target.get`.
- [ ] Commit.

### Task 2: Shared ingest and bind links

- [ ] Move `ingestCard`'s body to `library.ImportCard(ctx, provenance, source, card, avatarPath)`; the importer calls it. Importer tests still pass.
- [ ] `fetch` also returns the reply's `lorebook_remote_ids`; `bind` records them, sorted, when present.
- [ ] Test: binding a card whose library lorebook is bound to the app's linked lorebook is `in_sync`, not `ahead`.
- [ ] Commit.

### Task 3: `targets` import

- [ ] `EnqueueImport(ctx, q, instanceID, remoteIDs)` and `RunImport`, kind `target.import`.
- [ ] `jobs.Queue.ChildOutcomes`; the job response's `outcomes`.
- [ ] Tests, with the fake plugin:
  - An embedded-lorebook app: cards and their lorebooks arrive, bound and `in_sync`, with avatars.
  - A standalone app: lorebooks are bound, cards link to them, and all are `in_sync`.
  - A card already in the library is bound, not copied. Two app items with the same content: one `bound` or `created`, the other `duplicate`.
  - Running the import again queues nothing. A re-run child reports `already_bound`.
  - `remote_ids` limits the import; an unknown one is refused.
- [ ] Commit.

### Task 4: API, CLI, MCP

- [ ] `POST /imports` routes a target instance to `EnqueueImport`; tier test.
- [ ] `start_import` takes `remote_ids`.
- [ ] Commit.

### Task 5: SillyTavern plugin (`innkeeper-plugins`)

- [ ] Keep `blob_tmp` from `initialize`. `target.get` with `avatar` writes the export PNG there; every `target.get` replies with `lorebook_remote_ids` from `extensions.world`.
- [ ] Fake-server tests. Check the export route against a local SillyTavern; ask before importing from the homelab's.
- [ ] Commit, open a PR, and bump the pinned core version once the core work merges.

### Task 6: Docs

- [ ] README: importing an app's library, with an `innkeeper import` example.
- [ ] Commit.
