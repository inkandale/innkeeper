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
| Cards changed | A card counts when the job writes a revision of it, archives or unarchives it, or deletes it. Cards only read or checked don't count. |
| Start time | The first attempt's claim. A retry keeps it, so duration covers the whole job, retries included. |
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
- `Record` sets `started_at = created_at`, since the work happened inside another job.
- Jobs that finished before the migration keep `started_at` null and show no duration.

## Core API

`Run.AddCardsChanged(n int)` adds to a count held on the `Run`, under the mutex that already guards progress. `touch` and `finish` write the count as an absolute value, so lease renewals never add it twice. A job that fails partway keeps the count of cards it had already changed.

`Job` gains `StartedAt *time.Time` and `CardsChanged int`.

## Handlers

Each handler that changes cards calls `AddCardsChanged` where it does:

| Package | Counts |
|---|---|
| `importer` | Cards imported or revised. Pending source updates and skips don't count. |
| `bulk` | Cards deleted, purged, archived or unarchived by hide |
| `enrich` | Cards it wrote a revision for |
| `targets` | Cards that reconcile, sync or autosync wrote a revision for |

Export, backup, blob collection, detect and action runs that only return a proposal change no cards, and stay at 0.

## API and UI

`JobOut` gains:

- `started_at`, nullable.
- `duration_ms`: `finished_at − started_at`, null until the job finishes or when there is no start.
- `cards_changed`.

For a bulk parent, the API fills these from its children in the same query as `ChildCountsFor`. Regenerate the schema with `npm run gen`.

- **Jobs list** (`web/src/settings/Jobs.tsx`): adds *Duration* and *Cards* columns.
- **Job page** (`web/src/settings/Job.tsx`): shows queue wait (`started_at − created_at`), run time, and cards changed.
- Durations reuse `age()`'s format from `jobUtil.ts`.

## Stage 2: Prometheus

This gets its own ticket. `Worker` observes each finished top-level or child job:

- `innkeeper_job_duration_seconds{kind,status}`, a histogram.
- `innkeeper_cards_changed_total{kind}`, a counter.

It adds the `prometheus/client_golang` dependency and a `/metrics` route.

## Testing

Against real PostgreSQL in `internal/jobs`:

- `TestClaimSetsStartedAtOnce`: a retried job keeps its first `started_at`.
- `TestCardsChangedSurvivesTouchAndFailure`: the count is saved by a lease touch and kept when the job fails.
- `TestBulkParentSumsChildMetrics`: a parent reports its children's earliest start and summed count.

Elsewhere:

- `internal/importer`: an import's `cards_changed` equals its imported plus revised cards.
- Vitest in `Jobs.test.tsx`: the list renders duration and cards, and a job with no start shows no duration.
