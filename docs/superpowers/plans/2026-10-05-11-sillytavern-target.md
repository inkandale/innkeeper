# SillyTavern Target Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Push cards to a SillyTavern server and read them back, with lorebooks synced as SillyTavern world info files, through a `sillytavern` plugin in `innkeeper-plugins`.

**Architecture:** A `push` and `pull` target with standalone lorebooks, built like the `lumiverse` plugin. Cards map to character PNGs: created by importing a PNG that the shared `card` package writes, updated with `/merge-attributes`. Lorebooks map to world info files, which SillyTavern overwrites whole on save, so a replace keeps the file name. Each create carries the core's key, stored in the app, so a re-run finds its earlier copy.

**Tech Stack:** Go 1.27, `net/http`, `net/http/cookiejar`, the core's `protocol` package, `innkeeper-plugins/card`. Tests run against an in-memory fake of the SillyTavern routes on `httptest`. No new dependencies.

**Spec:** `docs/superpowers/specs/2026-10-03-innkeeper-core-design.md`, "Targets". SillyTavern behavior is from its source at commit `06bde93` on the `release` branch (github.com/SillyTavern/SillyTavern). Paths below are in that tree.

**Kaneo:** INN-17.

---

## Scope

In this plan:

- `sillytavern/`: manifest, plugin, client, card and world info mapping, tests against a fake server, an opt-in live test.
- `index.toml` entry, `.gitignore`, the plugin's README.
- Core: move `PLUGINS_REF` so the image ships the plugin; README section on adding a SillyTavern target.

Left out:

| Left out | Why | Where |
|---|---|---|
| More than one lorebook per character | A card links one world file (`extensions.world`). Extra books live in the browser's settings (`world_info.charLore`), which the plugin shouldn't rewrite | When the owner needs it |
| Deleting characters or world files | The core doesn't delete in apps | When it does |
| Chats, group chats, sprites, backgrounds | Not part of CCv3 | Not planned |
| A per-plugin database for keys | The app holds the keys, as in `lumiverse` | Not needed |

## Design notes

- **Config and secrets.** Config `url` (such as `https://sillytavern.example.com`) and `auth`: `none`, `basic` (the server's `basicAuthMode`), or `account` (`enableUserAccounts`). `username` is required for `basic` and `account`. Secret `password`. SillyTavern has no API keys.
- **Reaching the server.** `whitelistMode` is on by default and allows only `127.0.0.1` and `::1`, so SillyTavern answers Innkeeper with a `403` HTML page. The README tells the owner to add Innkeeper's address to `whitelist`, or turn whitelist mode off and use `basic` or `account`.
- **Session.** One `cookiejar` per client. Every call goes through `ensureSession`: for `basic`, send the `Authorization` header on every request and `GET /login` once, which binds the session to the account when accounts are on (`src/users.js:751-773`). For `account`, `POST /api/users/login {handle, password}`. Then `GET /csrf-token` and send its `token` as `X-CSRF-Token` on every `POST`. A `"disabled"` token is sent as is.
- **Login limits.** SillyTavern allows 5 login attempts per IP per 60 seconds; a successful login resets the count (`src/endpoints/users-public.js:12-22, 94`). The client logs in once and reuses the session. On a `403` from an API call, it gets a new session once. A rejected login (`401` or `403` from the login itself) is not retryable, and the plugin refuses to log in again until it restarts, as `lumiverse` does. A `429` is retryable.
- **A `403` after a fresh session** is not retryable. Its error says the cause is the whitelist, CSRF, or the account, since SillyTavern returns `403` for all three.
- **Describe.** `fields`: `name`, `description`, `personality`, `scenario`, `first_mes`, `mes_example`, `creator`, `creator_notes`, `system_prompt`, `post_history_instructions`, `alternate_greetings`, `tags`, `character_version`, `extensions`, plus the V3 fields SillyTavern keeps unchanged: `nickname`, `group_only_greetings`, `creator_notes_multilingual`, `source`, `assets`. `lorebooks: "standalone"`; `lorebook_fields`: `name`, `description`, `scan_depth`, `token_budget`, `recursive_scanning`, `extensions`, `entries`. `pull` and `update` are true.
- **Remote IDs.** A character's ID is its PNG file name, such as `Brakka.png`. A world file's ID is its `file_id` from `/api/worldinfo/list`. Renaming a character in SillyTavern changes its file name, so the next push gets `-32004` and the core pushes a new copy. The README says so.
- **Card create.** Set `data.extensions.innkeeper = {key, avatar}` and `data.extensions.world` to the one lorebook remote ID (or `""`), remove `data.character_book`, write a PNG with `card.Encode(card, card.FormatPNG, avatar)`, and `POST /api/characters/import` (multipart field `avatar`, `file_type=png`). The reply is `{file_name}` without `.png`; append it. The route answers `200 {error: true}` when it fails, so check the body.
- **Finding an earlier create.** `POST /api/characters/all` returns full characters unless the server has `lazyLoadCharacters` on. Match `data.extensions.innkeeper.key`. For a shallow item (`shallow: true`), read it with `/get` first. A copy renamed between the crash and the re-run is still found, since the key is in the card.
- **Card update.** `POST /api/characters/get {avatar_url}` first (`404` → `-32004`), because `/merge-attributes` answers `500` for a missing card and `/edit` silently creates one. Then `POST /api/characters/merge-attributes` with `avatar` and the card:
  - Every `fields` key, with `""` or `[]` for one the card lacks, since the merge keeps what it isn't sent.
  - The V1 top-level copies (`name`, `description`, `personality`, `scenario`, `first_mes`, `mes_example`, `creatorcomment` from `creator_notes`, `tags`), since PNG export writes the stored JSON as is.
  - `data.extensions`: the card's own, `world`, and `innkeeper`. Keys in the current card that the new one lacks get `"__@@UNSET@@__"`, except SillyTavern's local `fav`. Arrays are replaced whole.
  - `data.character_book: "__@@UNSET@@__"`.
  - Never `/edit`: it resets every field it isn't sent.
- **Lorebook links.** More than one `lorebook_remote_ids` entry fails the put with a non-retryable error naming the limit. Pushing silently without the others would lose content.
- **Avatars.** A blob's file name is its sha256. On update, the plugin sends the avatar to `POST /api/characters/edit-avatar` (multipart `avatar_url` and `avatar`) only when that differs from `extensions.innkeeper.avatar`. SillyTavern re-encodes the image, so the hash can't be checked against the app. A put without an avatar leaves SillyTavern's.
- **Card read-back.** `POST /api/characters/get`, then parse `json_data`, the stored card. Drop `data.character_book` (SillyTavern rebuilds it from the world file when the browser saves), and from `data.extensions` drop `innkeeper`, `world`, and `fav`. Return `{spec: "chara_card_v3", spec_version: "3.0", data}`; the V1 top-level fields are ignored. A browser save adds `talkativeness` and `depth_prompt` defaults, which show as a change once; adopting brings them in.
- **World file create.** Pick a free file name from `POST /api/worldinfo/list`: the lorebook's `name`, else `Lorebook`, with ` 2`, ` 3`, … until unused, since `/edit` overwrites an existing file without asking. Then `POST /api/worldinfo/edit {name, data}`. The book's `extensions.innkeeper` holds `sync_key` and the book-level fields SillyTavern doesn't use: `description`, `scan_depth`, `token_budget`, `recursive_scanning`, and the book's own `extensions`.
- **Name races.** The core locks each lorebook's push, not the whole instance, so two new lorebooks with the same name can pick the same file. A mutex around pick-and-write covers one process. Two workers pushing same-named new books at once can still overwrite one; mark it with a `ponytail:` comment.
- **Finding an earlier world file.** `/api/worldinfo/list` returns each file's book-level `extensions`. Match `extensions.innkeeper.sync_key`.
- **World file replace.** `/api/worldinfo/list` to confirm the file exists (`/get` answers `200 {entries: {}}` for a missing file), else `-32004`. Then `/edit` with the whole book. One write, so a failure leaves the old file.
- **Entries.** Mirror SillyTavern's own `convertCharacterBook` (`public/scripts/world-info.js:5617`) and `convertWorldInfoToCharacterBook` (`src/endpoints/characters.js:663`), so a file the browser saves reads back the same way:
  - Out: `entries` is an object keyed by index; `uid` and `displayIndex` are the index; `keys`→`key`, `secondary_keys`→`keysecondary`, `content`, `comment` (or `name` if empty), `enabled`→`disable`, `insertion_order`→`order`, `position` (`before_char` 0, `after_char` 1, or `extensions.position`), `selective`, `constant`, `case_sensitive`→`caseSensitive`, and SillyTavern's own settings from `entry.extensions` (`depth`, `probability`, `group`, `sticky`, …, snake case to camel case as the converter does). `extensions.innkeeper` keeps the entry's original `id`, `name`, `position`, and `priority`.
  - Back: the reverse, sorted by `displayIndex`, with `id`, `name`, `position`, and `priority` restored from the stash and the stash removed. Book-level fields come back from `extensions.innkeeper`.
  - SillyTavern fills about 30 template fields into an entry when the browser saves it. They come back under `entry.extensions` as the converter writes them, and show as a change once, as Lumiverse's do.
- **Open browser tabs.** A SillyTavern tab keeps its own copy of the open character and world file, and its next save overwrites a push. Drift checks find this as a remote change. The README says to reload the tab after a push.
- **Errors.** Network errors, `429`, and `5xx` are retryable. A retried update runs `/get` again, so a character deleted between `/get` and `/merge-attributes` becomes `-32004`. `4xx` other than `403`, `404`, and `429` are not retryable. A `404` for the remote ID is `-32004`.
- **List.** `target.list`: `/api/characters/all`, `remote_id` = `avatar`, `name` = `name`, sorted by name. `target.list_lorebooks`: `/api/worldinfo/list`, `remote_id` = `file_id`, `name` = `name`.

### Changes during the build

Merged as inkandale/innkeeper-plugins PR #5 at `34d5362`. Where these notes differ from the code, the code is right. The main changes:

- **Logins.** Only a `401`, or a `403` from `POST /api/users/login`, stops further logins. A `403` from `/login` or `/csrf-token` is the whitelist: a final error, but the next call tries again. With user accounts on, `basic` also needs SillyTavern's `perUserBasicAuth`.
- **Redirects.** The client follows a redirect only to the same scheme and host with the same method. Otherwise a proxy's `301` turns a `POST` into a `GET`, SillyTavern answers `404`, and the core would push duplicates.
- **Card update.** Removed keys are unset at any depth, not only in `extensions`. V3-only fields the card lacks are unset rather than sent empty. SillyTavern's deep merge drops an object sent over a stored non-object, so those paths are unset in a first `/merge-attributes`.
- **V1 characters.** `get` builds a V1-only character from the V2 conversion in SillyTavern's `/get` reply, since its `json_data` has no `data`.
- **World files.** The file name follows sanitize-filename 1.6.3 exactly, and a create takes its remote ID from `/list` by `sync_key`. An entry without `enabled` is pushed enabled. `display_index` and `use_regex` round-trip. Entries sort by `uid`.
- **Live test.** `TestLive` passes against `ghcr.io/sillytavern/sillytavern:1.19.0` with basic auth and with an account.

## File structure

- `sillytavern/plugin.toml`, `sillytavern/README.md`
- `sillytavern/main.go`: JSON-RPC handler, `describe`, initialize.
- `sillytavern/client.go`: session, login, CSRF, requests, error mapping.
- `sillytavern/cards.go`: `put`, `get`, `list`, key search, merge body.
- `sillytavern/books.go`: `putLorebook`, `getLorebook`, `listLorebooks`, entry mapping, file naming.
- `sillytavern/fake_test.go`: in-memory SillyTavern for tests.
- `sillytavern/client_test.go`, `sillytavern/cards_test.go`, `sillytavern/books_test.go`.
- `sillytavern/live_test.go`: opt-in, against a real server.

## Tasks

### Task 1: Fake SillyTavern and client

- [ ] Fake server, each route as the source behaves: whitelist (`403` HTML for a refused address), basic auth, `/login`, `/api/users/login` with the 5-per-minute limit that a success resets, `/csrf-token` (token or `"disabled"`), CSRF check on `POST`; characters `/all` (full, and shallow when a flag is set), `/get` (`404`), `/import` (PNG, `Name1` on collision, `200 {error: true}` for a bad file), `/merge-attributes` (deep merge, arrays replaced, `__@@UNSET@@__`, `500` when missing), `/edit-avatar`; world info `/list`, `/get` (`{entries: {}}` when missing), `/edit` (overwrites verbatim).
- [ ] Client tests: logs in once and reuses the session for both `basic` and `account`; sends `X-CSRF-Token` on `POST`; gets a new session once after a `403`; a second `403` is a non-retryable error naming the whitelist; after a rejected login, refuses to try again and sends nothing; `429` and `5xx` are retryable; `404` maps to `-32004`.
- [ ] Implement `client.go`. Commit.

### Task 2: Cards

- [ ] Tests: create imports a PNG whose card carries the key, `world`, and no `character_book`, and returns `<file_name>.png`; an import that answers `{error: true}` fails; a put with a key already in SillyTavern returns that character, including from a shallow `/all`; update sends every field, the V1 copies, and `__@@UNSET@@__` for a removed extension key, and keeps `fav`; update of a missing character is `-32004` and sends no merge; an unchanged avatar isn't sent again; two lorebook IDs fail without writing; `get` returns CCv3 without `character_book`, `innkeeper`, `world`, or `fav`; `list` returns file names and names.
- [ ] Implement `cards.go`. Commit.

### Task 3: World files

- [ ] Tests: create picks `Name 2` when `Name` exists and stores `sync_key` and the book-level fields; a put with a known key replaces that file; replace keeps the file name; a missing file is `-32004`; `get_lorebook` round-trips `id`, `name`, `position`, `priority`, order, and book-level fields; an entry the browser re-saved with template defaults reads back with them under `extensions`; `list_lorebooks` returns file IDs and names.
- [ ] Implement `books.go`. Commit.

### Task 4: Plugin wiring

- [ ] `main.go`, `plugin.toml` with the config schema (`auth` as an enum, `username` required unless `auth` is `none`), `index.toml`, `.gitignore`, README: whitelist setup, auth modes, renamed characters, one lorebook per character, reload open tabs after a push.
- [ ] `make check`: the contract kit passes (`describe` is valid and needs no app; `target.list` answers an empty config with an error, not a hang).
- [ ] Commit.

### Task 5: Live test

- [ ] `live_test.go`, skipped unless `SILLYTAVERN_URL` is set: push a card with a lorebook, read both back unchanged, push again, list both. Run it against `ghcr.io/sillytavern/sillytavern` at the release matching `06bde93`, published on host port 18000 (8080 is taken on the dev Mac), with `SILLYTAVERN_WHITELISTMODE=false` and basic auth on.
- [ ] Commit, open a PR in `innkeeper-plugins`, merge after CI.

### Task 6: Core

- [ ] Move `PLUGINS_REF` in the core image to the merged commit. README: adding a SillyTavern target.

## Plan series

- **Deploy (INN-16):** a SillyTavern instance in the cluster must whitelist the Innkeeper pod's address or use basic auth.
- **More than one lorebook per character:** would mean writing `world_info.charLore` through `/api/settings`, which also holds the browser's other settings.
