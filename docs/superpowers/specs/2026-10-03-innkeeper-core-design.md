# Innkeeper core design

Date: 2026-10-03

## Purpose

Innkeeper is the single source of truth for one owner's roleplay character
cards and lorebooks across every RP app they use (Lumiverse, SillyTavern,
Talemate, and others). It is app-agnostic: nothing in the core assumes
SillyTavern.

Innkeeper is split into four sub-projects, each with its own spec:

1. **Core** (this spec): library, card model, history, sync state, jobs,
   plugin host, API, web UI, MCP server, CLI.
2. **Archive catalog**: the chub.ai archive (416GB takeout, 500k+ cards) as a
   searchable catalog plugin. Replaces `~/git/charexplorer`.
3. **App targets**: SillyTavern, Talemate, and other target plugins.
4. **Editor and AI**: AI actions and the generation/revision UI from
   `~/git/character-card-generator`.

## Decisions

| Topic | Decision |
|---|---|
| Users | Single owner per instance. |
| Sync | Innkeeper pushes to apps. Where an app supports reads, Innkeeper also detects drift and lets the owner adopt changes deliberately. No automatic two-way merge. |
| Per-app differences | One base card plus per-target overrides (JSON merge patches). |
| Storage | PostgreSQL with pgvector for all structured data, a content-addressed blob store on disk for images and files. Not git. |
| Entities | Cards and lorebooks, both first-class and versioned. Personas are out of scope. |
| Internal card format | CCv3. V2 is handled by a codec. |
| Organization | Tags and collections. A card can be in many collections. |
| Runtime | Two services from one Go binary and image: `innkeeper api` and `innkeeper worker`. The same binary is the CLI. |
| Plugins | Child processes speaking JSON-RPC over stdio. Any language. |
| Repositories | Core in `innkeeper`. All plugins in `innkeeper-plugins`, listed in an index file. |
| API | REST described by one OpenAPI spec, plus SSE for streams. No GraphQL. |
| Interfaces | Web UI, MCP server, CLI, all clients of the HTTP API. |
| Language | Core in Go. Web UI in React and TypeScript, with a client generated from the OpenAPI spec. Each plugin picks its own: Go for the codec, importer, targets, and catalogs; Python for AI actions and enrichers. |
| Core libraries | `pgx` for PostgreSQL, Huma for the OpenAPI spec, the official MCP Go SDK. River for the job queue if its job metadata supports checkpointing while a job runs; otherwise our own `job` table. |

## Architecture

```
 Web UI ─┐
 CLI ────┼──► innkeeper-api ──────┐
 MCP ────┘     │ plugin host      │
               │ (sync calls)     ▼
               │              PostgreSQL ◄─── innkeeper-worker
               │              (library,        │ plugin host
               │               jobs,           │ (long jobs)
               │               plugin          │
               │               schemas)        │
               └──────► blob store ◄───────────┘
```

- **innkeeper-api** serves HTTP, enforces auth, and reads and writes the
  library. It calls plugins directly only for interactive work: catalog
  search, decoding an upload, pulling one card for a drift check.
- **innkeeper-worker** claims jobs from PostgreSQL with
  `SELECT … FOR UPDATE SKIP LOCKED`, woken by `LISTEN/NOTIFY`. It runs imports,
  bulk actions, enrichers, pushes, and scheduled drift scans. Several workers
  can run at once.
- **Plugins** run as child processes of whichever service needs them. Both
  services may run the same plugin at once, so plugins keep no
  correctness-relevant state in memory. Persistent state goes in the plugin's
  own schema (`plugin_<name>`) or the blob store.
- **Blob store** is a directory shared by both services, laid out as
  `blobs/ab/cd/<sha256>`. More than one store can be configured, so the
  archive's blobs can live on a network share and the library's on fast disk.
- **Deployment** is one image, two services, and PostgreSQL with pgvector.

## Data model

### Library

Library history is append-only.

- **`card`**: `id`, `slug`, `head_revision_id`, `archived_at`, `provenance`
  (where the card came from, such as `chub-archive:author/slug@hash` or
  `upload`).
- **`card_revision`**, immutable: `id`, `card_id`, `parent_id`, `data` (CCv3
  without `character_book`), `avatar_blob`, `overrides`
  (`{instance_id: merge_patch}`), `content_hash`, `text_simhash`, `message`,
  `source` (`ui`, `mcp`, `plugin:<name>`, `adopt`), `created_at`.
  - Overrides live in the revision, so they share history and restore with
    the base card.
  - Every write takes the revision it was based on. If the head has moved,
    the write fails with `409`.
- **`lorebook`** / **`lorebook_revision`**: same shape as cards.
- **`card_lorebook`**: links a card to lorebooks. A link follows the
  lorebook's head revision. On import an embedded `character_book` is split
  out into a lorebook. On export and push it is embedded again for apps that
  need it.
- **`blob`**: `sha256`, `size`, `mime`, `store`, `avatar_phash` (images only),
  `created_at`.
- **`tag`**, **`card_tag`**, **`collection`**, **`collection_card`**.

### Duplicate detection

- **`content_hash`** is an exact hash of normalized card data.
- **`text_simhash`** is a 64-bit near-duplicate fingerprint of the normalized
  description, personality, scenario, and first message.
- **`avatar_phash`** is a 64-bit perceptual image hash.

The core computes all three on every write. Near-duplicates are matched by
Hamming distance. A pairwise scan is fine at library scale. The archive
catalog plugin uses the same hash functions with LSH bucketing.

### Sync

- **`plugin_instance`**: a configured plugin: `id`, `plugin`, `name`,
  `config`, `secrets`. One plugin can have many instances, such as two
  SillyTavern servers. Secrets are encrypted at rest with a key from the
  environment.
- **`binding`**: links a library card to a target: `card_id`, `instance_id`,
  `remote_id` (nullable), `state`.
- **`push`**: one row per push: `binding_id`, `revision_id`, `pushed_hash`,
  `remote_id`, `pushed_at`. Create-only targets accumulate several rows.

There are two kinds of drift:

- **Library ahead**: the card's head revision is newer than the last push.
  Known for every target.
- **Remote changed**: the app's copy hashes differently from what was pushed.
  Only for targets with `pull`. The hash covers only the fields the target
  declares it supports.

When remote changes are found, the owner sees a diff and chooses to adopt them
into the base card, adopt them into that target's override, or discard them.

### Jobs

**`job`**: `id`, `kind`, `instance_id`, `payload`, `status`, `attempts`,
`progress`, `checkpoint`, `run_after`, `locked_by`, `locked_until`,
`parent_id`, `error`.

- Plugins write `checkpoint` as they go, so long imports resume where they
  stopped.
- A bulk action is a parent job with one child job per card. Export is one
  job instead, because it writes a single zip.

### Search and enrichment

- Full-text search uses a `tsvector` built from each card's head revision.
- An **enrichment** is plugin-generated data about a card: AI summaries,
  judgments, quality scores, auto-tags, classifications. Enrichments go in the
  core so the UI, search, MCP, and later automations can use them.
  - **`enrichment`**: `card_id`, `revision_id`, `plugin`, `key`, `value`
    (JSON), `meta` (model, prompt version), `created_at`.
  - History is kept, so results from different models can be compared, and a
    result whose `revision_id` is not the head is shown as stale.
- **`card_embedding`**: `card_id`, `revision_id`, `model`, `embedding`. One
  index per model, so models with different dimensions coexist.
- A plugin's own schema holds only its private working data: restored source
  tables, caches, rate-limit state. Rule: if anything other than the plugin
  reads it, it goes in the core.

## Plugin protocol

### Manifest

Each plugin is a folder with a `plugin.toml`:

```toml
name = "lumiverse"
version = "0.1.0"
protocol = 1
command = ["./lumiverse-plugin"]
capabilities = ["push", "pull", "search"]
secrets = ["api_key"]

[config]       # JSON Schema; the UI renders the settings form from it

[enrichments]  # keys this plugin writes and how the UI shows them
summary = { type = "text",  label = "AI summary" }
quality = { type = "score", range = [1, 5], label = "Quality" }
```

### Plugin repository

Plugins live in the `innkeeper-plugins` repo, one folder per plugin. An
`index.toml` at the repo root lists them, so the core can show what is
available without reading every folder:

```toml
[[plugin]]
name = "lumiverse"
path = "lumiverse"
version = "0.1.0"
protocol = 1
capabilities = ["push", "pull", "search"]
description = "Push and pull cards and lorebooks with a Lumiverse server."
```

CI in `innkeeper-plugins` checks that every entry matches its folder's
`plugin.toml`, and runs the contract kit against each plugin. CI also
publishes each plugin version as a gzipped tarball per platform on a GitHub
Release, with an `index.toml` that lists them and their sha256. The core
image ships only `password-login` and `card-codec`; the owner installs the
rest from the Plugins page.

The protocol reference and contract kit live in the `innkeeper` repo,
because the core defines the protocol. `innkeeper-plugins` CI pins a core
version to test against.

### Lifecycle

1. The core starts `command` and sends `initialize` with the instance config,
   decrypted secrets, blob store paths, and a PostgreSQL DSN for a role that
   can only access `plugin_<name>`.
2. The plugin replies with its protocol version and capabilities. The core
   refuses a plugin whose protocol version it does not support.
3. Plugins start on first use, stop when idle, and restart after a crash.

### Install and update

- **Sources.** A source is the `https://` or `file://` URL of an
  `index.toml`. The official index is there by default; the owner can add
  others. A plugin's identity is `source/name`, and only one plugin per name
  is installed at a time, so a fork takes over the original's instances and
  database.
- **Install.** The core downloads the tarball for its platform, checks its
  sha256 against the index and its manifest and protocol against the entry,
  and unpacks it into `<plugins dir>/<name>/<source>@<version>/`. A `current`
  symlink switches in one rename.
- **Running jobs.** A job already running finishes on the old version. New
  calls start the new one, and the old process stops once idle. The worker
  reloads before each job when an install happened.
- **Rollback.** The current and previous versions stay on disk. Installing
  the previous one switches back without a download.
- **Built-in plugins.** An install overrides the image's copy, and
  uninstalling falls back to it.
- **Uninstall.** Removes the code. Instances and the plugin's database stay
  until the owner removes the data, a separate action.

### Methods

| Capability | Methods |
|---|---|
| `import` | `import.run(params, checkpoint)` streams `emit.card`, `emit.lorebook`, `progress`, `checkpoint` notifications |
| `search` | `search.query(q, filters, page)`, `search.get(id)`; filters declared in the manifest |
| `push` | `target.describe()`, `target.put(card, remote_id?)`, lorebook equivalents |
| `pull` | `target.list()`, `target.get(remote_id)`, lorebook equivalents |
| `action` | `action.run(name, input)` returns a draft and never writes; streams `stream.chunk` |
| `enrich` | `enrich.run(revisions[])` returns enrichment values, batched |
| `codec` | `codec.decode(blob_path)` returns CCv3 plus blobs; `codec.encode(card, format)` returns a blob; formats declared in the manifest |
| `auth` | `auth.begin()` returns a redirect or form; `auth.complete(params)` returns a verified identity |

Plugins can call back into the core: `core.card.get`, `core.lorebook.get`,
`core.search`.

### Targets

Cards cross the plugin boundary as CCv3. Each target plugin translates to and
from its app's own format. `target.describe()` declares what the app supports:

```
{
  fields: ["name", "description", "first_mes", ...],
  pull: true,
  update: true,
  delete: false,
  lorebooks: "standalone" | "embedded" | "none"
}
```

- Fields the target doesn't store are left out of its drift hash, and the UI
  warns before a push that would drop them.
- App-specific data with no CCv3 field goes under a namespaced key in
  `extensions` (`extensions.lumiverse.*`, `extensions.sillytavern.*`). The
  core stores it as-is, and overrides can set it.
- A push-only target such as Talemate declares `pull: false`. The UI shows
  "can't check remote changes" for it, not "in sync".
- On a create-only target (`update: false`), pushing again makes a new copy
  in the app. The UI says so and asks first.

### Blobs

Plugins write bytes to `blobs/tmp/` and refer to them by path. The core
hashes, deduplicates, and moves each file. It never trusts a hash the plugin
reports.

### Errors

Plugins return standard JSON-RPC errors with a `retryable` flag. A retryable
error or a crash re-queues the job, which resumes from its checkpoint. Any
other error fails the job and shows in the UI.

### Auth

Login providers are plugins. The core checks each verified identity against
the owner, issues sessions, and enforces auth on every request. Auth cannot
be turned off. API tokens for the CLI, MCP server, and scripts are built into
the core.

## API and interfaces

### HTTP API

REST, described by one OpenAPI spec. SSE streams job progress and AI action
output.

Search is `POST /search` with a structured query. Saved searches and bulk
actions use the same query object.

```json
{
  "text": "grumpy dwarf blacksmith",
  "semantic": true,
  "filter": { "and": [
    { "tag": "fantasy" },
    { "enrichment": "quality", "gte": 4 },
    { "not": { "target_state": { "instance": 3, "is": "in_sync" } } }
  ]},
  "sources": ["library", "catalog:chub-archive"],
  "include": ["enrichments", "lorebooks"],
  "fields": ["name", "description", "avatar"],
  "page": { "size": 50, "cursor": null }
}
```

`target_state.instance` is a plugin instance ID, the same key overrides use.

`sources` mixes library results with catalog plugin results. Catalog results
can be adopted into the library, which records their provenance.

### Web UI

- Library browse, search, and filter.
- Card and lorebook editors with history, diff, and restore.
- Per-target state (ahead, drifted, can't check) with a diff and adopt
  choices.
- Job list with progress.
- Plugin instance settings, rendered from each manifest's JSON Schema.
- Enrichment display, driven by manifest declarations.

The AI editor from character-card-generator is sub-project 4.

### MCP server

Tools: `search_cards`, `get_card`, `update_card`, `adopt`, `push`,
`check_drift`, `run_action`, `list_jobs`. Authenticates with an API token.

### CLI

Commands: `import`, `export`, `push`, `bulk`, `jobs`, `backup`.

## Error handling

- **Concurrent edits**: writes carry their base revision. A stale base returns
  `409`.
- **Pushes**: binding and push rows are written only after `target.put`
  succeeds.
- **Jobs**: retryable failures back off and retry up to a limit, then become
  `failed` and show in the UI. Nothing is dropped silently.
- **Blob GC**: deletes only unreferenced blobs older than a grace period, so
  in-flight imports are safe.
- **Startup**: a service exits if the secrets key is missing or PostgreSQL is
  unreachable. A plugin that fails to start or has the wrong protocol version
  is marked broken, and the rest of the system keeps running.

## Testing

- **Plugin contract kit**: one suite that tests any plugin against the
  protocol for its declared capabilities. Used for bundled plugins and
  published for third-party ones.
- **Fake target plugin** with configurable `describe()` flags, for drift and
  push tests across full-sync, push-only, and create-only targets.
- **Codec golden files**: real V2 and V3 cards, PNG and JSON, that must round
  trip without loss.
- **Integration tests** against real PostgreSQL with pgvector, not mocks.

## v1 scope

Core plus these bundled plugins from `innkeeper-plugins`, enough for the full
loop:

- V2/V3 PNG and JSON codec.
- Local password auth provider.
- Folder and upload importer.
- Lumiverse target (push and pull).

Bundled plugins use the same protocol as any other plugin, with no internal
shortcuts. That keeps the protocol complete enough for plugins to be built
separately.

## Build order

Each later part is a plugin, so once the core and protocol are stable, every
plugin can be built separately and in parallel against the contract kit.

1. Protocol: a written reference (manifest, methods, notifications, errors)
   and the contract kit. This is the boundary every plugin is built against.
2. Core: schema, services, job queue, plugin host, API, CLI.
3. v1 bundled plugins in `innkeeper-plugins`, built against the protocol
   reference only.
4. Web UI and MCP server.
5. Separate plugin work: archive catalog, other targets, AI actions,
   enrichers, each with its own spec.

## Out of scope for this spec

- Chub archive catalog (sub-project 2).
- SillyTavern, Talemate, and other targets (sub-project 3).
- AI actions and the generator UI (sub-project 4).
- Enricher plugins (embeddings, summaries, judgments). The core tables and
  protocol for them are in scope; the plugins are not.
- Automations: triggers plus step lists, built later on the job queue.
- A network transport for plugins that must run remotely.
- Personas.
- Multi-user.
