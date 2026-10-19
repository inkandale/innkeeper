# Job metrics design

Date: 2026-10-18.

## Purpose

The Jobs page doesn't show how long a job took or how many cards it changed:

- The `job` table has `created_at` and `finished_at`, but no start time. Their difference mixes queue wait and retry backoff into run time.
- Each kind writes its own progress shape (`{deleted}`, `ScanProgress`, `HideProgress`, …), so there is no common count of cards changed.

This spec records both on every job and shows them in the UI. A Prometheus endpoint built on the same values is the next stage, not this one.

## Decisions

| Topic | Decision |
|---|---|
| Where metrics live | Stage 1: on the job row, shown on the Jobs pages. Stage 2: a `/metrics` endpoint for Prometheus and Grafana, fed by the same values. |
| Cards changed | A card counts when the job changes it in the library or in an app: a new revision, archive or unarchive, tags, collections, enrichment values, deletion, or a push. Cards only read or checked don't count, and neither do lorebooks. The count adds up across every attempt. |
| Start time | The first attempt's claim. An automatic retry keeps it, so duration covers the whole job, retries included. A manual retry from the job page restarts the clock. |
| Bulk parents | Not stored. A parent's start is its earliest child's start, and its count is the sum of its children's counts. |
| Out of scope | Cards processed (as opposed to changed), per-attempt timings, aggregate views in the UI, and a general per-job metric table. |

## Schema

Migration `0042_job_metrics.sql` adds two columns to `job`:

```sql
ALTER TABLE job
    ADD COLUMN started_at timestamptz,
    ADD COLUMN cards_changed int NOT NULL DEFAULT 0 CHECK (cards_changed >= 0);
```

- `claim` sets `started_at = COALESCE(started_at, now())`.
- `Retry` sets `started_at` back to null.
- `Record` sets `started_at = created_at`, since the work happened inside another job, and takes the cards it changed.
- Jobs that finished before the migration keep `started_at` null and show no duration.

## Core API

`Run.AddCardsChanged(n int)` adds to a count held on the `Run`, under the mutex that already guards progress. The count starts from the job's saved value, so it carries across attempts. `touch` and `finish` write it as an absolute value, so lease renewals never add it twice. A job that fails partway keeps the count of cards it had already changed.

`Job` gains `StartedAt *time.Time` and `CardsChanged int`.

`ChildCounts` and `ChildCountsFor` become `ChildrenFor`, which returns each parent's child counts by status, earliest child start, and summed cards changed in one query.

## Handlers

Each handler that changes cards calls `AddCardsChanged` where it does:

| Package | Counts |
|---|---|
| `importer` | Cards imported or revised, counted once their batch commits. Pending source updates and skips don't count. |
| `bulk` | Each card a child archives, tags or collects; cards purged; cards hide archives or unarchives; cards a library delete removes |
| `enrich` | Cards that got values |
| `targets` | Cards a push sends, a resolve revises, a reconcile or sync imports or pulls, autosync pushes, or a source check applies |

Export, backup, restore, blob collection, job prune, lorebook archive, detect, drift scans and action runs change no cards, and stay at 0. Action runs reach the library read-only.

A sync's count includes the cards in the reconcile jobs it records, so summing the Jobs list counts those cards twice.

`DeleteLibrary`'s progress callback reports each batch's kind and size, so the job can tell cards from lorebooks.

Known gap: updates an import applies through autosync after its items are stored aren't counted, because the import holds only binding IDs at that point.

## API and UI

`JobOut` gains:

- `started_at`, nullable.
- `duration_ms`: `finished_at − started_at`, null until the job finishes or when there is no start.
- `cards_changed`.

For a bulk parent, the API fills these from `ChildrenFor`. Regenerate the schema with `npm run gen`.

- **Jobs list** (`web/src/settings/Jobs.tsx`): adds *Duration* and *Cards* columns.
- **Job page** (`web/src/settings/Job.tsx`): shows queue wait (`started_at − created_at`), run time, and cards changed.
- Durations reuse `age()`'s format from `jobUtil.ts`. In the list, a cards count of 0 shows blank.

## Stage 2: Prometheus

This gets its own ticket. `Worker` observes each finished top-level or child job:

- `innkeeper_job_duration_seconds{kind,status}`, a histogram.
- `innkeeper_cards_changed_total{kind}`, a counter.

It adds the `prometheus/client_golang` dependency and a `/metrics` route.

## Testing

Against real PostgreSQL in `internal/jobs`:

- `TestClaimSetsStartedAtOnce`: an automatic retry keeps the first `started_at`; a manual retry clears it.
- `TestCardsChangedAddsUpAcrossAttempts`: the count is saved before the job finishes, carries into a retry, and is kept when the job fails.
- `TestChildrenForSumsChildMetrics`: a parent reports its children's earliest start and summed count.

Each handler's existing test asserts its count, and `TestBulkArchiveAndWatch` checks the API fields. Vitest in `Jobs.test.tsx` checks that the list renders duration and cards, and that a job with no start shows no duration.
