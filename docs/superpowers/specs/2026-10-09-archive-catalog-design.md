# Archive catalog design

Date: 2026-10-09

## Purpose

An **archive** is a collection of cards and lorebooks the owner wants to
search and adopt from without putting them in the library. Each archive is a
**catalog**: an instance of a catalog plugin. One catalog might be the chub.ai
archive takeout. Another might be a folder of the owner's own files, or later,
the live chub.ai site.

This is sub-project 2 of the core spec
(`2026-10-03-innkeeper-core-design.md`). It replaces `~/git/charexplorer`.

First real case: the chub.ai archive takeout, loaded from its 12GB SQL dump
(`database_01-05-2025`), which covers over 500k cards across six sites.

## Decisions

| Topic | Decision |
|---|---|
| Archives | Each archive is one catalog plugin instance. Core and the web UI treat every catalog alike. |
| Catalog kinds | **Ingested** catalogs copy their source into an index in their own schema. **Pass-through** catalogs forward each request to a remote site and store nothing. One protocol covers both. |
| Index | Each ingested catalog owns its index in its `plugin_<name>` schema and answers search itself. Core owns no catalog tables. |
| Search | Postgres full-text search plus the filters each catalog declares. No embeddings in v1. Semantic search can come later as an enricher. |
| Item kinds | `kind` is an open string the plugin declares: `card` and `lorebook` in v1, and `preset` once the library has presets (INN-39). |
| Images | Stay where the catalog's source keeps them. The chub archive keeps them as files named by hash, beside the dump. Core keeps one blob store. |
| Adopt | Copies one version into the library as a new card or lorebook through the existing import path, as `emit.card` and `emit.lorebook` do. It doesn't bring the version chain or track later versions. |
| Near-duplicates | Matched both ways. Core badges catalog results that match library items, and catalogs answer `catalog.similar` for the library card page. |
| Language | Go, like every other plugin. `cardhash` becomes a public package so plugins use the same hashes as core. |
| v1 plugins | `chub-archive` (the SQL dump) and `folder`. The live chub.ai catalog is INN-38. |

## Terms

- **Catalog**: a plugin instance with the `catalog` capability.
- **Item**: one card or lorebook in a catalog, with a stable ID the catalog
  assigns.
- **Version**: one revision of an item. The chub dump's `increment` is a
  version. A folder item has one version per file content it has had.
- **Adopt**: copy one item version into the library.

## Protocol

A plugin declares `capabilities = ["catalog"]`. Methods:

| Method | Params | Result |
|---|---|---|
| `catalog.describe` | none | The kinds it holds, the filters it supports (name, label, type `enum`, `text`, `date_range` or `number_range`, and options for `enum`), its sort orders, and whether it supports `ingest` and `similar`. |
| `catalog.search` | `q`, `kind`, `filters`, `sort`, `page` (`size`, `cursor`) | Items with ID, kind, name, author, tags, summary, latest version, an inline JPEG thumbnail of at most 32 KiB, and hashes (`content_hash`, `text_simhash`, `image_phash`), plus the next cursor. |
| `catalog.get` | `id`, `version` (omitted means latest), `avatar` | The item's metadata, its version list, and the version as CCv3: a card, or a `character_book` for a lorebook. With `avatar`, the card's image as a file in `blob_tmp`, as `target.get` returns it. |
| `catalog.similar` | `kind`, `hashes`, `max_distance`, `limit` | Items with the same `content_hash`, or whose simhash or phash falls within `max_distance`, closest first. Optional. |
| `catalog.ingest` | `run`, `checkpoint` | Runs as a core job. Sends the existing `progress` and `checkpoint` notifications, and returns counts: added, updated, removed, skipped. Optional. |

Unknown IDs and versions return `-32004`, the error targets use for a missing `remote_id`. Filters the catalog didn't
declare are refused as invalid.

## Core

- **`cardhash`** moves from `internal/cardhash` to a public package, with its
  test vectors.
- **Routes:**
  - `GET /catalogs` lists catalog instances with their `describe` result and
    the last ingest job.
  - `GET /catalogs/{id}/search` forwards to `catalog.search`, with `filters`
    as one JSON-object query parameter, and adds library matches to each
    result.
  - `GET /catalogs/{id}/items/{item}` forwards to `catalog.get` without
    `avatar`. An item ID escapes `/` as `%2F`.
  - `POST /catalogs/{id}/items/{item}/adopt` takes an optional `version`
    query parameter. It returns 201 with the new library card or lorebook,
    or 200 with the one an earlier adopt of that version made. A catalog
    that sends an avatar the server can't accept gets 502.
  - `POST /catalogs/{id}/ingest` queues a `catalog.ingest` job, or returns
    409 while the catalog's last ingest is unfinished.
  - `GET /cards/{id}/catalog-matches` and `GET /lorebooks/{id}/catalog-matches`
    return matches from every catalog that supports `similar`, and name the
    catalogs that didn't answer.
- **`/search`** accepts `catalog:<instance>` in `sources`, so the CLI and MCP
  can search the library and catalogs together. Each catalog source adds its
  first page, matched on `text` alone, to a `catalogs` list beside the
  library's hits. The library is searched when `sources` is empty or names
  `library`. Catalogs are searched only on the first library page: a request
  with a cursor or a page number above 1 leaves them out.
- **Library matches** are one query per kind over the page's hashes: exact by
  `content_hash`, near by simhash or phash within 8 bits, against each
  entry's head revision, as `/cards/{id}/similar` compares.
- **Adopt** calls `catalog.get` with `avatar`, and imports the data and
  avatar with provenance `catalog:<instance>:<item>@<version>` and source
  `plugin:<name>`. Import dedupe already keys on provenance and
  `content_hash`, so adopting the same version twice returns the existing
  card.
- **Tiers:** search, get, and catalog matches are `read`. Adopt and ingest are
  `write`. Catalog instances are configured through the existing instance
  routes and their tiers.

## Plugins

All three live in `innkeeper-plugins`.

### `catalogkit`

A shared Go package, not a plugin. It holds:

- The `item` and `item_version` tables and their migrations.
- Every row keyed by instance ID. The core gives a plugin one schema for all
  its instances, so two `folder` archives share tables. A deleted instance's
  rows stay until purged (INN-41).
- Full-text search over name, author, description, and tags (a generated
  `tsvector` with a GIN index).
- Declared filters compiled to SQL, and cursor paging.
- Tags stored and filtered in lowercase.
- Versions numbered by the plugin, such as the dump's `increment`, or
  numbered 1, 2, 3 as the data changes.
- Entries that set their own author, tags, date added, and numbers for
  `number_range` filters.
- Entries over a Postgres limit, such as a description too long for a
  `tsvector`, refused as bad data so an ingest skips them.
- LSH bands over `text_simhash` and `image_phash`, in an `lsh` array column
  with a GIN index. Each hash splits into 9 bands of 7 bits, so hashes within
  8 bits share a band. `catalog.similar` refuses a `max_distance` over 8.
- A new version when an item's whole data changes, including a card's
  embedded lorebook, which `content_hash` leaves out.

### `chub-archive`

- **Config:** the path to the dump file, and the archive's image folders.
- **Ingest:**
  1. Streams the dump one `COPY` block at a time. It parses the `COPY` text
     format itself, so it doesn't need psql.
  2. Normalizes `chub_character_def`, `booru_character_def`,
     `nyaime_character_def`, `risuai_character_def`,
     `webring_character_def`, and `generic_character_def` into cards, and
     `chub_lorebook_def` into lorebooks. Each row is one version.
     `webring_character_def` and `generic_character_def` have no source ID,
     so each row is its own item, named by `card_data_hash`.
  3. Reads `chub_character` and `chub_lorebook`, which come before their
     versions in the dump, for tags, ratings, dates, and the hidden flag.
  4. Reads each version's image from the image folders, at
     `<a>/<b>/<c>/<rest>` of its `image_hash`. A missing image leaves the
     version without one. Any other read error stops the ingest, which
     retries from its checkpoint.
- **Item ID:** `<table>:<source id>`. A version is the row's `increment`.
  `booru_character_def` has no `increment`, so its items have one version,
  `1`. Re-ingesting skips versions already stored, so IDs stay stable.
- **Checkpoint:** the current block, its byte offset, and the dump's size. A
  checkpoint from a dump of another size is refused. `progress` counts bytes
  of the dump.
- **Filters:** site (the source table), tag, author, date added, rating, and
  hidden.
- **Skipped:** `aicg_chronicles`, `stored_rentry_links`, `chub_user`,
  `nyaime_user`, and the chat logs. They aren't cards or lorebooks.

JanitorAI and the historical Discord and mega.nz scrapes exist only as files,
not in the dump. Each needs its own file ingester, added to this plugin later.

### `folder`

- **Config:** a directory path.
- **Ingest:** walks the directory and reads PNG, JSON, and CharX cards and
  lorebook JSON into CCv3 with the codec's parsers. It upserts on path, mtime, and size.
  The checkpoint is the last path processed.
- **Item ID:** the path relative to the directory. A changed file adds a
  version.
- **Versions:** numbered 1, 2, 3, each dated by the file's modification time.
  Only the latest version of a file unchanged since ingest has an avatar,
  read from the file.
- **Removal:** a file that disappears marks its item removed instead of
  deleting it, so cards already adopted from it keep their provenance.
- **Filters:** subdirectory, tag, and date modified. `catalog.search` takes
  `kind` already.

## Web UI

- **Archive page:**
  - A catalog picker, a Cards/Lorebooks switch, a search box, and a filter
    bar built from the catalog's `describe` result.
  - Results show "In library" for an exact match and "Similar in library" for
    a near match.
  - A read-only item view with a version picker and an Adopt button. After
    adopting, it links to the new library card.
  - For ingested catalogs, an ingest status line with the last job's counts
    and a button to start a new ingest.
- **Card and lorebook pages:** a "Found in archives" panel listing matches by
  catalog, each linking to the item view.

## Errors

- **A plugin is down or times out:** a catalog that won't start, exits, takes
  over 30 seconds, or returns a retryable error makes that catalog's routes
  return 503 naming the catalog, and the Archive page shows the error inline.
  In `/search`, the catalog's entry carries the error instead. "Found in
  archives" skips the catalog and names the ones it couldn't reach.
- **A catalog returns any other error:** its routes return 502 naming the
  catalog. The error goes to the server log, since it can name files on the
  server.
- **A catalog instance is broken:** its routes return 409, as for any other
  broken instance.
- **A dump row won't decode:** the plugin skips it, logs the table, ID, and
  increment, and counts it in `skipped`.
- **Ingest fails partway:** the job retries from its last checkpoint. Search
  keeps serving what is already ingested.

## Testing

- **Contract kit:** `innkeeper plugin check` gains catalog checks: `describe`
  is valid, `search` pages correctly and refuses undeclared filters, `get`
  returns data that hashes to the search result's `content_hash`, and unknown
  IDs return `-32004`. When a catalog ingests and the check has a config, the
  kit runs `catalog.ingest` first. Both plugins run it in CI, with
  `INNKEEPER_PLUGIN_CONFIG` pointing at test data and
  `INNKEEPER_PLUGIN_DATABASE_URL` at a test database.
- **`cardhash`:** its test vectors pass after the move.
- **`catalogkit`:** LSH bands find pairs within the distance threshold and
  miss pairs outside it.
- **`chub-archive`:** a fixture dump cut from the real one, with a few rows
  from every table, multiple increments, and a bad row. Tests cover counts,
  resuming from a checkpoint in the middle of a block, and an idempotent
  re-ingest.
- **`folder`:** a temp directory of PNG, JSON, CharX, and lorebook files, with
  a file added, changed, and deleted between runs.
- **Core:** API tests against a fake catalog plugin cover search routing,
  library-match badges, adopt dedupe and provenance, partial failure in
  catalog matches, and tiers.
- **Web:** component tests for the filter bar built from `describe` and for
  the adopt flow.

## Build order

Each step merges on its own.

1. Public `cardhash`, the catalog protocol, and contract kit checks.
2. Core: catalog routes, `catalog:` sources in `/search`, adopt, ingest jobs,
   and catalog matches.
3. `catalogkit` and the `folder` plugin, the smaller plugin, which proves the
   protocol first.
4. The `chub-archive` plugin.
5. Web: the Archive page and the "Found in archives" panel.

## Out of scope

- The live chub.ai catalog (INN-38).
- Presets (INN-39).
- Semantic search.
- Adopting a version chain as library history, and offering newer catalog
  versions of an adopted card.
- A second blob store.
- JanitorAI and the historical scrapes, which need file ingesters.
- Refreshing the ratings and tags of `chub-archive` versions already
  ingested.
- Using a catalog item as the merge ancestor in reconcile. The reconcile spec
  allows it, but nothing calls `catalog.get` for it yet.
