# Lumiverse Target Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Push cards to a Lumiverse server and read them back, with lorebooks synced as Lumiverse world books, through a `lumiverse` plugin in `innkeeper-plugins`.

**Architecture:** A `push` and `pull` target with standalone lorebooks. Cards map to Lumiverse characters through its import, update, and JSON export routes. Lorebooks map to world books, replaced in place so their IDs stay stable. Each create carries the core's key, stored in the app, so a re-run finds its earlier copy.

**Tech Stack:** Go 1.27, `net/http`, the core's `protocol` package. Tests run against an in-memory fake of the Lumiverse routes on `httptest`. No new dependencies.

**Spec:** `docs/superpowers/specs/2026-10-03-innkeeper-core-design.md`, "Targets". Constraints from plan 5 and plan 5b "Plan series". Lumiverse behavior is from its source at commit `7398fa5` (github.com/prolix-oc/Lumiverse).

**Kaneo:** INN-12. Needs INN-9 (put keys) and INN-13 (standalone lorebooks), both merged.

---

## Scope

In this plan:

- `lumiverse/`: manifest, plugin, Lumiverse client, card and world book mapping, tests against a fake server.
- `index.toml` entry, `.gitignore`, the plugin's README.
- Core: the image ships the plugin (the Dockerfile builds every `index.toml` plugin already), README section on adding a Lumiverse target.

Left out:

| Left out | Why | Where |
|---|---|---|
| `target.list` (binding characters already in Lumiverse) | The core has no `target.list` yet | With `target.list` |
| Deleting characters or world books | The core doesn't delete in apps | When it does |
| Gallery images, expressions, regex scripts in the global store | Not part of CCv3; Lumiverse keeps them | Not planned |
| A per-plugin database for keys | The core doesn't provide `database_url` yet (INN-14); the app holds the keys instead | INN-14 |

## Design notes

- **Config and secrets.** Config `url` (such as `https://lumiverse.example.com`) and `username` (an email signs in by email, anything else by username). Secret `password`.
- **Sign-in.** `POST /api/auth/sign-in/email` or `/sign-in/username`, then `Authorization: Bearer <token>` on `/api/v1`. Sessions last 7 days and slide. On a `401` from an API call, the plugin signs in again once. A failed sign-in is not retryable, and the plugin refuses to sign in again until it restarts: five failures lock the server's IP out of all of Lumiverse for 15 minutes or more, and a bulk push would otherwise reach five in seconds. Changing the instance's config or secrets restarts the plugin.
- **Describe.** `fields`: `name`, `description`, `personality`, `scenario`, `first_mes`, `mes_example`, `creator`, `creator_notes`, `system_prompt`, `post_history_instructions`, `alternate_greetings`, `tags`, `character_version`, `extensions`. `lorebooks: "standalone"`; `lorebook_fields`: `name`, `description`, `scan_depth`, `token_budget`, `recursive_scanning`, `extensions`, `entries`. `pull` and `update` are true.
- **Card create.** `POST /api/v1/characters/import` with the CCv3 card as JSON. `data.character_book` is removed. `data.extensions` gets `innkeeper: {key, avatar}` and `world_book_ids: lorebook_remote_ids`; a non-empty `world_book_ids` also stops Lumiverse making its own world book. Then the avatar goes to `POST /characters/{id}/avatar` (multipart `avatar`).
- **Finding an earlier create.** Lumiverse can't search by extension values. A put with a key first searches `GET /characters/summary?search=<name>` and reads each match with `GET /characters/{id}`, looking for `extensions.innkeeper.key`. A copy renamed in Lumiverse between the crash and the re-run is missed.
- **Card update.** `GET /characters/{id}` (`404` → `-32004`), then `PUT /characters/{id}` with every field and the whole `extensions`: the card's own, plus Lumiverse's local keys from the current character (`_lumiverse_*`, `expressions`, `expression_groups`, `alternate_fields`, `alternate_avatars`, `avatar_crop_image_id`, `original_image_id`, `risu_asset_map`, `gallery_reference_sequence`, `gallery_reference_names`, `landing_perspective_layers`, `ttsVoice`), `world_book_ids` from the put, `character_version`, and `innkeeper`. `PUT` replaces `extensions` wholesale, so nothing else survives.
- **Avatars.** A blob's file name is its sha256. The plugin uploads the avatar only when that differs from `extensions.innkeeper.avatar`, since each upload makes new images in Lumiverse. A put without an avatar leaves Lumiverse's.
- **Card read-back.** `GET /characters/{id}/export?format=json`. The plugin drops `data.character_book` (built from world books) and, from `data.extensions`, `innkeeper`, `character_book`, `world_book_ids`, `world_book_id`, the local keys above, and `_lumiverse_*`. `character_version` moves from `extensions` back to `data`. What remains is what Lumiverse would export and the owner can edit.
- **World book create.** `POST /api/v1/world-books` with `name`, `description`, and `metadata: {source: "innkeeper", sync_key, ccv3: {scan_depth, token_budget, recursive_scanning, extensions}}`: the book-level fields Lumiverse doesn't store. Then one `POST /world-books/{id}/entries` per entry. Never `source: "character"`: Lumiverse treats those books as its own.
- **Finding an earlier world book.** `GET /world-books` pages through every book (1000 a page) for `metadata.sync_key`. A book found this way is replaced in full, since the earlier run may have stopped partway through its entries.
- **World book replace.** `PUT /world-books/{id}` for name, description, and metadata; list the entry IDs; one `POST /world-books/{id}/entries/bulk {"action": "delete"}`; then create every entry. Not atomic: a failure partway leaves a partial book, and the push fails with a retryable error so the job tries again.
- **Entries.** CCv3 to Lumiverse, following Lumiverse's own import: `keys`→`key`, `secondary_keys`→`keysecondary`, `content`, `comment` (or `name` if empty), `enabled`→`disabled`, `insertion_order`→`order_value`, `position` (`before_char` 0, `after_char` 1, numbers as is), `selective`, `constant`, `case_sensitive`, `priority`, and `extensions`. `extensions.innkeeper` keeps the entry's original `id`, `name`, `position`, and index.
- **World book read-back.** `GET /world-books/{id}/export?format=character_book` (`404` → `-32004`). Entries are sorted by the stored index and get their `id`, `name`, and `position` back; the stash is removed. Book-level fields come back from `metadata.ccv3`. Lumiverse adds about 17 keys to each entry's `extensions` (`sticky`, `probability`, a stable `uid`, …). They stay: they are settings the owner can change in Lumiverse. Since the core takes the read-back after a push as the baseline, they don't show as changes.
- **Errors.** Network errors and `5xx` are retryable. `4xx` other than `401` and `404` are not. A `404` for the remote ID is `-32004`.

## File structure

- `lumiverse/plugin.toml`, `lumiverse/README.md`
- `lumiverse/main.go`: JSON-RPC handler and initialize.
- `lumiverse/client.go`: sign-in, requests, error mapping.
- `lumiverse/cards.go`: `put`, `get`, key search, extension merging.
- `lumiverse/books.go`: `putLorebook`, `getLorebook`, entry mapping.
- `lumiverse/fake_test.go`: in-memory Lumiverse for tests.
- `lumiverse/cards_test.go`, `lumiverse/books_test.go`, `lumiverse/client_test.go`.

## Tasks

### Task 1: Fake Lumiverse and client

- [ ] Fake server: sign-in (email and username, wrong password `401`, counts attempts), characters (`import`, `get`, `put`, `summary?search`, `avatar`, `export?format=json` stripping `world_book_ids` and rebuilding `character_book` from linked books, as Lumiverse does), world books (create, get, put, list, entries create/list, bulk delete, export as `character_book` with Lumiverse's added entry keys).
- [ ] Client tests: signs in once and reuses the token; signs in again after a `401`; after a failed sign-in, refuses to try again and makes no request; `5xx` is retryable, `404` maps to `-32004`.
- [ ] Implement `client.go`. Commit.

### Task 2: Cards

- [ ] Tests: create sends the key and `world_book_ids`, uploads the avatar; a put with a key already in Lumiverse returns that character; update keeps `_lumiverse_*` and `ttsVoice` and replaces `world_book_ids`; an unchanged avatar isn't uploaded again; `get` strips internal keys and restores `character_version`; a missing character is `-32004`.
- [ ] Implement `cards.go`. Commit.

### Task 3: World books

- [ ] Tests: create stores `sync_key` and `ccv3` metadata and every entry; replace keeps the ID and leaves exactly the new entries; a put with a known key replaces that book; `get_lorebook` round-trips `id`, `name`, `position`, order, and book-level fields; a missing book is `-32004`.
- [ ] Implement `books.go`. Commit.

### Task 4: Plugin wiring

- [ ] `main.go`, `plugin.toml` with the config schema, `index.toml`, `.gitignore`, README.
- [ ] `make check`: the contract kit passes (`describe` is valid and needs no app).
- [ ] Commit, open a PR in `innkeeper-plugins`, merge after CI.

### Task 5: Core

- [ ] Move `PLUGINS_REF` in the core image to the merged commit. README: adding a Lumiverse target.

## Plan series

- **Plan 7 (UI):** a Lumiverse instance's settings form comes from `plugin.toml`; the password is a secret.
- **INN-14:** once plugins get a database, the key search can use it instead of the app.
