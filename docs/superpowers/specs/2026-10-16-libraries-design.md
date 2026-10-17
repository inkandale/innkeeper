# Libraries

Date: 2026-10-16

Ticket: INN-65

## Purpose

The library and the archive catalog are built twice. Each has its own schema,
search, filters, sorts, paging, similar lookup, thumbnails, and web grid; only
`cardhash` is shared. Enrichments reach library cards and not archive items, so
scoring the archive (INN-66) would need a third path.

This design makes a **library** a container of cards and lorebooks. The
curated library is one library; an ingested archive is another, filled by an
ordinary import plugin. Search, similar lookup, enrichments, the grid, the
card page, and MCP then serve both through one code path.

## Decisions

| Topic | Decision |
|---|---|
| Model | A `library` table. Cards and lorebooks belong to exactly one library. The existing library becomes library 1. |
| Who creates libraries | The owner, in Settings. An import can target any library, including library 1. |
| Editing | Every library is editable. There is no read-only flag. |
| Archives | An archive plugin is an `import` plugin. `catalog.ingest` and catalogkit's index go away. |
| Item identity | One source item is one card. Re-importing a changed item adds a revision, or a source update if the owner edited the card. |
| Plugin values | Values such as chub's `rating` are enrichment rows from the importing instance. |
| Blobs | One store for every library, as today. Garbage collection cleans up after a deleted library. |
| Pass-through catalogs | Unchanged. `catalog.describe`, `catalog.search`, and `catalog.get` stay for catalogs that store nothing (INN-38). |
| Migrating the current archive | None. chub-archive re-imports from the dump once this lands. |

## Data model

- **`library`**: `id`, `name`, `created_at`. A migration creates library 1,
  "Library", and sets every existing card and lorebook to it.
- **`card.library_id`** and **`lorebook.library_id`**: not null, indexed with
  the existing sort columns. Revisions inherit their entity's library.
- **Collections and variants** are scoped to one library. A card can't be a
  variant of a card in another library.
- **Forks** can target any library. The fork gets a `card_source` row pointing
  at the original, so the link is something code can read,
  unlike today's `provenance` string after adopt.
- **Bindings, sync, and push** work on cards in any library.
- **Enrichments** are already keyed by card and need no change.
- **Blobs** don't change: one content-addressed store shared by every
  library. A blob belongs to no library; garbage collection deletes it once no
  revision references it.
- **Deleting a library** deletes its cards and lorebooks; the next garbage
  collection removes the blobs only they used. It runs as a job behind a typed
  confirmation, since an archive holds ~270k cards.

## Import

### Run

The owner starts an import from a library's page or with MCP `start_import`,
choosing the plugin instance and the target library. Core passes the library
to the import job; the plugin never names one, so it can't write where the
owner didn't choose.

chub-archive reads the dump as it does now and sends one `emit.card` per
version, in dump order, with `progress` and its byte-offset `checkpoint`.

### `emit.card` additions

All optional, so `url-import` is unaffected:

| Field | Meaning |
|---|---|
| `version` | The source's version label, stored as the revision's `message`. |
| `created` | The source's date for the version, stored as the revision's `created_at`. |
| `values` | Values for keys the manifest declares under `[enrichments]`, stored as enrichment rows from the importing instance. |

### One item, one card

Core looks up the card by `(library_id, provenance)`, where provenance is
`plugin:<instance>:<item>`:

| Found | Action |
|---|---|
| No card | Create it. |
| A revision with this content | Nothing. |
| Head is the last imported revision | Append a revision. |
| The owner edited the card since the last import | Record a pending source update: a three-way merge in the changes queue, with the last imported revision as the ancestor, as in the [source updates design](2026-10-14-source-updates-design.md). Autosync applies a clean merge. |

This replaces the `ponytail:` on `Entities.Import`
(`internal/library/import.go`), which imports a changed item as a new card.
URL imports get the same behavior: re-importing a changed page updates its
card.

### Bulk mode

A plugin sets `bulk = true` in its manifest for imports of large sources. In
bulk mode, core:

- skips queuing source detection for each card,
- inserts each checkpoint's rows, about 1,000, in one transaction,
- leaves thumbnails to be made on demand, as the library already does.

### Avatars

Avatars arrive through `blob_tmp` and go into the blob store, as today.
chub-archive's image store and its `image_store` setting go away.

### Restarts

A restarted import resumes from its last checkpoint. Versions already stored
are no-ops, so rows re-read after the checkpoint add nothing.

## Scale

An archive library holds ~270k cards and ~350k revisions. Each change below
also fixes a limit the curated library has today.

- **Similar lookup.** Move catalogkit's LSH (9 bands of 7 bits, GIN on
  `int[]`) into `internal/library/similar.go`. Store bands on `card_revision`
  beside `text_simhash`, and phash bands on `blob`. `SimilarCards` and
  `MatchHashes` select candidates by band overlap, then check the exact
  distance, which stays 8. The band function moves into `cardhash`, and the
  migration backfills bands from stored hashes.
- **Paging.** Replace the offset cursor in `internal/library/search.go` (its
  `ponytail:`) with a keyset cursor over `(sort key, id)`, and `(rank, id)`
  for relevance.
- **Search.** Queries filter on `library_id` first and read head revisions
  only. Check the plans with `EXPLAIN ANALYZE` on a generated 300k-card library
  before merging.
- **Facets and family grouping** count within the current library. Facets
  stop counting at a cap and show "1,000+".
- **Library-wide jobs**, such as "Check for updates" and bulk Evaluate, take a
  library and page through its cards instead of loading every ID.
- **Backup.** `innkeeper backup` still copies every blob, so with an archive
  library a backup holds ~100 GB of images. Limiting a backup to some
  libraries is out of scope.

## API, MCP, and web

- **API.** Card and lorebook IDs stay global, so `/cards/{id}` and the routes
  under it don't change. Search, create, import, fork, and facets take an
  optional `library`, defaulting to 1. New routes: `GET` and `POST /libraries`,
  and `PATCH` and `DELETE /libraries/{id}`.
- **MCP.** A `list_libraries` tool. `search_cards`, `create_card`,
  `create_lorebook`, `start_import`, and `fork_card` take an optional
  `library`, defaulting to 1.
- **Web.**
  - A library switcher at the top of the rail. The current library is in the
    URL as `?library=`, and the last one used is remembered.
  - Cards, Lorebooks, filters, and facets show the current library.
  - The card page shows the card's library. "Fork into…" lists every library.
  - **Found in archives** becomes **In other libraries**, using similar lookup
    across libraries.
  - Settings → Libraries creates, renames, and deletes libraries.

## Removed

| What | Replaced by |
|---|---|
| `catalog.ingest`, `catalog.similar`, catalogkit's index and its put and read code | Import plugins writing to a library |
| `POST /catalogs/{id}/ingest`, `POST /catalogs/{id}/items/{item}/adopt`, `GET /{kind}/{id}/catalog-matches` | Import, fork, and **In other libraries** |
| `web/src/archives/` and the Archive rail entry | The library grid and the card page |
| chub-archive's `item`, `item_version`, `parent`, and `source_row` tables and its image store | Cards and blobs in the target library; only the checkpoint remains plugin state |

## Build steps

Each step ships on its own. Step 5 is in innkeeper-plugins; the rest are core.

1. **Libraries.** The `library` table, and `library_id` on cards and
   lorebooks, set to 1.
2. **Scale.** LSH similar lookup, keyset paging, facets and family grouping
   within a library, and the 300k `EXPLAIN ANALYZE` check.
3. **Import.** One item, one card; the `emit.card` fields; values as
   enrichments; bulk mode; source updates for edited cards.
4. **Scoping.** The `library` parameter in the API and MCP, the switcher,
   Settings → Libraries, "Fork into…", and **In other libraries**.
5. **chub-archive as an importer.** It emits cards; a plugin migration drops
   its tables, and its image store is deleted by hand.
6. **Removal.** Everything in [Removed](#removed).

## Homelab rollout

Each step is its own Forgejo PR. Never change an app spec and its source path
in one push.

1. Mount the `innkeeper-archive` share at `/nas` in both containers with
   `extraVolumes` and `extraVolumeMounts`. Not `/data`: the chart already
   mounts `/data/blobs` and `/data/plugins`.
2. Scale the deployment to 0, copy the blob directory to `/nas/blobs`, set
   `INNKEEPER_BLOB_DIR=/nas/blobs` in `extraEnv`, scale back up, and check
   that avatars load. This needs no core change and can happen before build
   step 1.
3. Set `persistence.blobs.enabled=false`, which drops the `blobs` volume and
   its claim, and remove its Velero annotation and the `/chub-archive` mount.
   The NAS's own snapshots now cover blobs.
4. After build step 5, create a "Chub archive" library and import into it from
   `/takeout`. Then remove the `takeout` mount.

## Testing

- **Database tests against PostgreSQL:**
  - importing the same item twice adds nothing;
  - a changed item adds a revision;
  - a changed item on an edited card records a source update;
  - an interrupted import resumes without duplicates;
  - search, facets, and similar lookup stay within a library unless asked;
  - deleting a library removes its cards, and garbage collection then removes
    blobs only it used while keeping ones another library references;
  - keyset paging is stable while rows are inserted;
  - LSH similar lookup returns what the exact scan returns.
- **Contract test.** The new `emit.card` fields through the real broker,
  including `values` checked against the manifest.
- **Scale script, outside CI.** Generate a 300k-card library and record
  `EXPLAIN ANALYZE` for search, similar lookup, and facets in the step 2 PR.
- **Web.** The switcher, `?library=` links, "Fork into…", and **In other
  libraries**.

## Out of scope

- Archive quality scoring, duplicate groups, hiding, and purging (INN-66).
- Discovery beyond search (INN-67).
- Pass-through catalog browsing (INN-38).
- A blob location per library.
- Backing up only some libraries.
