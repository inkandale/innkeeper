# Source detection in plugins

Date: 2026-10-13

## Purpose

`internal/library/detect.go` knows chub.ai, RisuRealm, and Character Tavern:
how each site's data marks a card, the shape of its page URLs, and how its
refs validate. That is site-specific behavior in core, and `url-import`
already parses the same URLs, so adding a site takes a core change and a
plugin change (the [URL import design](2026-10-13-url-import-design.md)
says "one resolver plus one core detector"). This design moves all of it
into plugins through a new `sites` capability. After it, adding a site
touches only a plugin.

## Decisions

| Topic | Decision |
|---|---|
| Capability | New `sites` capability with two methods: `sites.detect` (data → sources) and `sites.parse` (URL, or kind and ref → source). No third method: a stored source already holds its page URL. |
| Plugin | `url-import` gains `sites`. Each site file parses its URLs once, for both the fetch and detection. |
| Which plugins | One instance per plugin that declares `sites`, the oldest one that isn't broken, in order of that instance's ID. Results are merged; the first plugin wins a conflict. |
| Failure | Detection never fails an import, a sync, or an adopt. A plugin that errors or times out is skipped and logged. With no `sites` plugin, nothing is detected, and adding a source by URL answers 422. |
| Import path | Imports queue their new entities for detection, and a `detect-sources` job drains the queue. No plugin call runs inside a notification handler or an import's transaction. |
| Sync path | Reconcile and resolve call `sites.detect` directly on the app's copy, as they call `target.get` today. |
| Stored rows | Unchanged. Kinds `chub`, `risu`, and `character-tavern`, and their ref forms, stay exactly as they are. |
| API | No field or path changes. Only the wording of two 422 messages changes. |
| Core code removed | Everything site-specific in `detect.go`; `Detect`, `ParseSourceURL`, and `ValidateSource` become calls through `internal/plugins`. |

## What core uses detection for today

| Path | Where | Calls | Needs |
|---|---|---|---|
| Import job (`import.run` emits) | `importer/importer.go:200-207` → `library/import.go:226` `ImportItem` → `Import` (`import.go:94`, `:106`) | `DetectSources` after commit, also on a repeat import | data → sources, for the card and its split lorebook |
| Catalog adopt | `api/catalogs.go:312` → `ImportItem` | as above | data → sources |
| Sync resolve, import action | `targets/resolve.go:391` → `ImportCardTx` (`import.go:256`) | `DetectSourcesTx` on the split lorebook, in the resolve transaction | data → sources |
| Sync resolve, bind | `targets/resolve.go:467-472` | `DetectSourcesTx` on the app's copy (`it.raw`), in the resolve transaction | data → sources, on data the library may not store |
| Reconcile match | `targets/reconcile.go:305-313` | `library.Detect`, then `BySource` | data → sources, to find library items with the same source |
| Add a source (API, MCP `add_source`, `ink … sources add`) | `api/entities.go:362-387` | `ParseSourceURL`, then `ValidateSource` | URL → source, or kind and ref → source with its canonical URL |
| Display | `web/src/card/Organize.tsx:105-107` | none: shows the stored `url`, `kind`, and `ref` | nothing new |
| Search filter and facet | `library/search.go:136`, `:212-214`; `web/src/cards/query.ts:59`, `Facets.tsx:66` | none: SQL on stored `kind` | stable kind names |

Not detection paths: creating or editing a card in the UI or over MCP never
detects; there is no backfill job today.

## Protocol

Add `sites` to `protocol.Capabilities` (`protocol/manifest.go:16`). No new
manifest keys. Both methods are local parsing: answer within 5 seconds,
don't contact any service, and give the same answer for any instance config.

A **source** in a reply:

| Field | Rule |
|---|---|
| `kind` | The site, `^[a-z][a-z0-9-]*$`, as `card_source.kind` checks. A plugin never renames a kind it has sent: stored rows, search filters, and reconcile matching use it |
| `ref` | The site's ID for the page, not empty, at most 512 bytes |
| `external_id` | Optional, at most 128 bytes |
| `url` | The page, `https://`, at most 2048 bytes |

Core checks every returned source with one function in `protocol`, shared
with the contract kit, and drops one that fails, with a log line naming the
plugin.

**`sites.detect`** reads the sources a card's or lorebook's data names.

| Param | Meaning |
|---|---|
| `entity` | `card` or `lorebook` |
| `data` | A card's CCv3 `data` object, or a lorebook's `character_book` object |

Reply with `sources`, a list of at most 32, or `[]`. Return only sources
that fit the entity: a lorebook gets no RisuRealm source, and a card gets no
chub lorebook page. Reply `-32602` if `entity` is unknown or `data` isn't an
object.

**`sites.parse`** turns what the owner typed into a source.

| Param | Meaning |
|---|---|
| `entity` | `card` or `lorebook` |
| `url` | A page URL. Send either `url`, or `kind` and `ref` |
| `kind`, `ref` | A source the owner named directly |

Reply with `source`, the source with its canonical URL, or `null` when the
URL or kind isn't yours. Reply `-32602` with a message the owner can read
when the kind is yours and the ref isn't valid, or when params send both or
neither form.

```json
{"jsonrpc": "2.0", "id": 9, "method": "sites.parse", "params": {"entity": "lorebook", "url": "https://www.chub.ai/lorebooks/u/orcs?x=1"}}
{"jsonrpc": "2.0", "id": 9, "result": {"source": {"kind": "chub", "ref": "u/orcs", "url": "https://chub.ai/lorebooks/u/orcs"}}}
```

### How core calls it

The shared code lives in `internal/plugins/sites.go`, beside
`CodecInstance` (`plugins.go:506`), which already picks an instance by
capability. It is named for the capability, since "source" in that package
already means a plugin index:

- `(*Registry).SitesInstances(ctx)`: for each plugin that declares `sites`,
  its oldest working instance, ordered by that instance's ID.
- `(*Registry).DetectSources(ctx, mgr, k, data) ([]library.Source, error)`:
  calls each with a 5-second timeout, drops invalid sources, and merges them
  first-wins on `(kind, ref)`, the rule in `detect.go:176`. A plugin error is
  logged and skipped; `Registry.Call` already marks an instance that fails
  to start as broken (`plugins.go:586`). A reply with more than 32 sources
  is skipped. The error is non-nil only when `ctx` ended or a plugin failed
  retryably, so the job can keep the entity queued; the sources are then the
  other plugins'. Reconcile and resolve ignore that error.
- `(*Registry).ParseSource(ctx, mgr, k, in) (library.Source, error)`:
  `in` is a URL alone, or a kind and ref. The first non-null `source` wins,
  keeping the owner's `external_id` if they sent one. A `-32602` from a
  plugin becomes `library.ErrInvalid` with its message, and an invalid
  source `plugins.ErrBadSource`, which the API answers with 502.

| Case | `DetectSources` | `PUT …/sources` |
|---|---|---|
| No `sites` plugin | `nil` | 422 "no installed plugin recognizes that URL" (was "no known site matches that URL") |
| Every plugin answers `null` | `nil` | 422, same message; for kind and ref, "no installed plugin knows source kind %q" (was "unknown source kind %q") |
| Plugin errors, times out, or is broken | skipped, logged | skipped, logged; the next plugin is asked |
| Two plugins return the same `(kind, ref)` | the first wins | the first non-null wins |
| Plugin returns an invalid source | dropped, logged | 502, naming the plugin |

Owner-linked and removed rows are never touched, as now
(`sources.go:80-84`): detection inserts with `ON CONFLICT DO NOTHING`.

### The import path

The importer handles `emit.card` on the plugin connection's reading
goroutine (`importer/importer.go:188-191`), and a notification handler must
not call the same connection (`protocol/jsonrpc.go:58-61`). With `url-import`
both importing and detecting, an inline `sites.detect` from `emit.card`
would wait on a reply that goroutine can never read. So imports queue
instead of calling:

1. Migration `0020_source_detect.sql`: `source_detect (kind text, entity_id
   bigint, PRIMARY KEY (kind, entity_id))`. Backups leave it out, as they
   leave out jobs.
2. `Entities.Import` inserts the entity into `source_detect` inside its own
   transaction, also on a repeat import. `Entities.DetectSources` and
   `DetectSourcesTx` go: no plugin call happens in `library`, so it needs no
   new dependency. `Library.DetectQueue` pages through the queue, and
   `Library.Detected` records an entity's `auto` rows and deletes its queue
   row in one transaction.
3. A `detect-sources` job, `importer.DetectKind` run by `Importer.RunDetect`,
   reads each queued entity's head revision, calls `DetectSources`, and calls
   `Detected`; an entity that is gone is only dequeued. It makes passes over
   the queue until one detects nothing, so entities queued behind its cursor
   are not missed. A retryable failure keeps that entity queued and the job
   ends with `jobs.ErrRetry`.
4. `importer.EnqueueDetect` (`EnqueueOnce` of that job) at the end of every
   import job and after an adopt. A failure to enqueue is logged, not
   returned, so detection never fails an import. The worker also schedules
   the job hourly: that catches an entity queued while a job was finishing,
   which `EnqueueOnce` treats as already running.

Reconcile and resolve call `Registry.DetectSources` directly. They run in a
job or a request, not a notification handler, and resolve already calls
`target.get` inside its transaction (`resolve.go:193`). `appItem` gains
`sources`, filled once in `readItemData` (`reconcile.go:101`), so reconcile
and resolve share one call per item. `Entities.RecordSourcesTx` records
given sources, for resolve's bind (`resolve.go:470`). Resolve's import
action detects the split lorebook itself and passes the sources to
`ImportCardTx`, which gains a `bookSources` parameter.

## Performance

| Path | Calls | Cost |
|---|---|---|
| Bulk import of N cards | N `sites.detect` calls in the job, after the import | One local JSON-RPC round trip each, under a millisecond plus encoding `data`, which the import already sent across a pipe once. 10,000 cards take seconds |
| Reconcile of N app items | N calls beside the N `target.get` calls it already makes | Small next to `target.get`, which goes to the app |
| Add a source | One call per `sites` plugin | Interactive; one plugin today |

No batch method and no cache. A batch `sites.detect_many` is an additive
method if profiling shows the round trips matter; a cache keyed by content
hash would need invalidating on every plugin upgrade.

Known ceiling: a plugin that hangs costs 5 seconds per item until it is
stopped. The job marks it in the log; skipping that instance for the rest of
the job is the upgrade if it happens.

## Compatibility

- **Stored rows** stay as they are: no data migration. `card_source` and
  `lorebook_source` (`db/migrations/0009_sources.sql`) already accept any
  kind matching the regex.
- **Kind names are public.** They appear in the API's `Source.kind`,
  `SourceCount.kind` (`search.go:103-107`), the search filter
  `{"source": "chub"}` (`search.go:45`), the web's `f=source:chub` URLs, MCP
  `list_sources`, and `ink` examples (`cmd/ink/library.go:100`). Reconcile
  matches new detections against stored rows by `(kind, ref)`
  (`reconcile.go:306`), so a renamed kind or a differently escaped ref would
  silently stop matching.
- **Refs keep their current form**: chub `user/slug` unescaped, as found;
  RisuRealm the lowercase UUID; Character Tavern `author/slug` unescaped. The
  stored URL path-escapes them. `url-import`'s `chubPath` returns an escaped
  ref (`url-import/chub.go:64-78`), so the shared parser returns raw
  segments and each caller escapes for itself.
- **API**: `PUT /cards/{id}/sources` and the lorebook route keep both body
  forms and every response field. Only the 422 wording above changes.

## Where the site code goes

Into `url-import`, which already holds a file per site:

| File | Change |
|---|---|
| `chub.go`, `risu.go`, `tavern.go` | Each exports one page parser, such as `chubPage(u) (dir, user, slug, ok)`, used by the resolver and by detection |
| `sites.go` (new) | `sites.detect`: reads `extensions.chub` (`full_path`, and `id` decoded with `UseNumber`), then each string in `data.source` through the page parsers, plus `risurealm:{uuid}`. `sites.parse`: URL, or kind and ref |
| `main.go` | Declares and answers `sites` |
| `plugin.toml` | `capabilities = ["import", "sites"]`, version 0.2.0 |
| `sources_test.go` (new) | `detect_test.go`'s tables, moved verbatim, so output matches what core stored |

The hostile-input checks in `pathRef` (`detect.go:130-143`) move with the
code; `segment` (`url-import/import.go:153`) is weaker and is replaced by
them, for the fetch too.

Deleted from core: `detect.go:26-164` (the detectors, `pathRef`, `webURL`,
`chubDir`, `uuidPattern`) and the bodies of `Detect`, `ParseSourceURL`, and
`ValidateSource` (`detect.go:166-227`). `Source` moves to `sources.go`.
`detect_test.go` goes; the README's Sources section stops naming sites and
says `url-import` detects them.

`chub-archive` needs nothing: its catalog items keep `extensions.chub`, and
adopt queues them like any import.

## Getting a `sites` plugin onto every server

Detection needs a running instance, and today it works with no plugin at
all. To keep that:

- The image ships `url-import` built in: `Dockerfile:32` gains it in
  `PLUGINS`.
- On startup, core creates one instance of each built-in plugin that
  declares `sites` and has no required config or secrets, once. A setting
  records that it did, so an instance the owner deletes stays deleted. It
  keys on the capability, never the plugin's name.

## Contract checks

`internal/contract/sites.go`, run when the manifest declares `sites`:

| Check | Passes when |
|---|---|
| `sites.detect` answers a plain card | `{"entity": "card", "data": {"name": "Contract"}}` returns `sources: []` within 5 seconds |
| `sites.detect` answers a plain lorebook | The same with `entity` `lorebook` and `{"entries": []}` |
| `sites.detect` refuses bad data | `data: "x"` returns `-32602` |
| `sites.parse` ignores a foreign URL | `https://example.invalid/contract` returns `source: null` |
| `sites.parse` refuses bad params | Neither `url` nor `kind` returns `-32602` |
| Every returned source is valid | Each reply above passes the shared `protocol` check |

`internal/fakeplugin` gains a `sites` mode that recognizes
`https://fake.example/{a}/{b}` as kind `fake`, plus one bad mode per check.
`docs/protocol.md` gains a `### sources` section and the checks in "Checking
a plugin".

## Tests

| Where | Covers |
|---|---|
| `protocol` | The source check: kind regex, empty ref, lengths, non-https URL |
| `internal/plugins/sites_test.go` | Order and first-wins merge across two fake plugins; a broken, erroring, retryable, and hanging plugin each skipped; invalid sources dropped; no plugin falls back to `detect.go`; `ParseSource`'s answers and errors |
| `internal/library` | `Import` queues and doesn't record, also on a repeat; the queue pages in order; owner and removed rows survive `Detected` |
| `internal/importer/detect_test.go` | A `url-import`-style fake that imports and detects on one process: the import finishes and its sources appear after the job, with no deadlock; a retryable failure keeps the row until a later run; the fallback without a `sites` plugin |
| `internal/api` | Adding a source through the fake `sites` plugin, its 422s and 502; adopt queues the item and the job |
| `internal/targets/sites_test.go` | Reconcile matches, bind records, and resolve's import records the split lorebook's sources through the fake `sites` plugin |
| `internal/contract` | Each check against the fake's `sites` mode and one bad mode per check |
| `url-import/sources_test.go` | `detect_test.go`'s cases, verbatim |

## Rollout

Older cores reject an unknown capability (`protocol/manifest.go:140-142`),
so `url-import` 0.2.0 fails to load on them, `import` included. The core
must ship first, and the protocol version stays 1.

1. **Core PR 1.** `sites` capability and protocol types, `internal/plugins`
   shared code, queue, job, callers switched, contract checks, fakeplugin,
   `docs/protocol.md`. `detect.go`'s detectors stay as the fallback used only
   when no working instance declares `sites`, for both `DetectSources` and
   `ParseSource`, so `main` keeps detecting and keeps the old 422 wording.
   The existing chub-data tests stay as the fallback's tests; new tests use
   the fake `sites` plugin.
2. **Plugins PR.** `url-import` 0.2.0 with `sites`, `go.mod` pinned to PR
   1's commit. Its README and index description say which core it needs.
   Merge only after PR 1 is on core `main`, since the image builds plugins
   from `main` (`Dockerfile:23-26`).
3. **Core PR 3.** `url-import` built in, the startup instance, and deletion
   of the fallback and `detect_test.go`. Cut the core release after this PR,
   so no release carries the fallback. With the fallback gone, entities must
   stay queued while no `sites` plugin exists, so this PR also has
   `DetectSources` report that case for the job to keep the rows, and
   enqueues the job when a `sites` instance is created, edited, or checked.
   PR 1 needs neither, since the fallback drains the queue.

## Alternatives considered

| Option | For | Against |
|---|---|---|
| Keep detection in core | No work; no plugin call per item | Breaks the AGENTS.md plugin rules; every site stays a two-repo change; parsers stay duplicated |
| Add `import.detect` and `import.parse` to `import` | No new capability, so older cores load `url-import` 0.2.0 | The manifest can't say which import plugins detect, so core calls every one and treats `-32601` as "no"; a catalog plugin couldn't detect without claiming `import` |
| Site rules in the manifest (host lists, path templates, JSON paths) | No process call | Core becomes an interpreter for site rules, which is the same site logic in a worse form; new manifest keys that older cores reject |
| Separate `site-sources` plugin | Detection without the fetcher | Either duplicates `url-import`'s parsers or needs a shared package for one consumer; two plugins to update per site |
| Detect inline on import, as now | No queue or job | Deadlocks when the importing plugin is also the detecting one (see "The import path") |
| **New `sites` capability in `url-import`, queued on import** | One place per site; works with any number of plugins; imports never wait on detection | A migration, a job, and a rollout across two repos |

Recommendation: the last row.

## Decided

| Question | Decision |
|---|---|
| Built-in plugin | The image ships `url-import`, and core creates one instance of each built-in `sites` plugin at startup, once |
| Capability name | `sites`, since "source" already names plugin indexes |
| Delay after import | Sources appear when the job runs, seconds after the import |

## Out of scope

- Re-detecting the whole library on demand. The job's queue is the hook if
  it's wanted.
- New sites, such as JanitorAI.
- The name check in `web/src/welcome/BringInCards.tsx:23`, which predates
  `inputs`.
