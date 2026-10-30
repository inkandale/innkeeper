# Maintenance

Date: 2026-10-30

## Purpose

Some of Innkeeper's tables only grow: enrichment values are append-only,
vectors outlive the model that made them, and archived cards stay forever.
A Settings tab, Maintenance, shows how much each takes and offers one
action per kind of cleanup. Nothing here runs on its own; the owner starts
each action and sees what it will remove first.

## Decisions

| Topic | Decision |
|---|---|
| Where | A Settings tab, Maintenance, at `/settings/maintenance`. |
| Who | Every action needs the `destructive` tier: none can be undone from Innkeeper. |
| How | Each action runs as a job, with progress in Jobs. The tab shows a preview from `GET /maintenance` before each one. |
| Automatic runs | None. Scheduled cleanup can come later if owners ask for it. |
| `VACUUM FULL` | Not offered: it locks tables, so Innkeeper would stop answering while it runs. |

## Actions

### Trim enrichment history

Keeps the newest N values, by `created_at` then `id`, for each instance,
card and key, and deletes older ones. N defaults to 3 and ranges from 1 to
100. A card's current value is its newest, so the action never changes
what search, facets or the grid show.

Preview: the number of rows and roughly how many bytes it would delete
for the chosen N.

### Remove stale vectors

Deletes the rows in `card_embedding` that Similar cards and semantic
search can't read:

- vectors from instances other than the vector instance;
- vectors in a space other than the vector instance's current one. Core
  learns that space by sending the vector instance one `vector.query` for
  a fixed text.

With no vector instance set, every vector is stale. The cards whose vectors
go fall back to similar-by-tags until their plugin runs on them again.

Shown only when vectors are available.

### Clear recorded failures

Empties `enrich_failure`, so the hourly sweep tries those cards again, for
example after a prompt is fixed or a model is changed.

### Vacuum and analyze

Runs `VACUUM (ANALYZE)` on the database, which makes space freed by
deletes reusable and refreshes query plans. It doesn't shrink the files on
disk. It needs the database user to own the tables; if it doesn't, the job
fails and says so.

Preview: database size and the five largest tables. The job's progress
shows the size after.

### Purge archived cards

Permanently deletes cards and lorebooks archived at least D days ago. D
defaults to 30 and must be at least 1. A lorebook is purged only if no
live card links to it.

What happens to everything that points at a purged card:

| Points at it | Rule |
|---|---|
| Its revisions, tags, enrichment values, vectors, failure records, collection memberships, lorebook links, sources | Deleted with it |
| A live card whose `variant_of_id` is a purged card | Re-linked to the nearest ancestor that isn't purged, or made a root if there is none, so variant families stay connected |
| A binding to an app it was pushed to | Deleted. The app's copy is left alone; archiving's "unpublish" option removes copies |
| Its blobs, such as avatars | Left to the daily blob garbage job, which deletes those no remaining revision uses |

Revisions are immutable: `forbid_revision_change` rejects every update and
delete. Purge is the one exception. A migration changes the trigger
function to allow a `DELETE` when the transaction has set
`innkeeper.purge` to `on` with `SET LOCAL`. Only the purge job sets it.
Updates stay forbidden.

The job purges in batches of 1,000 cards, one transaction each, so it
holds no long lock and reports progress.

The tab shows the number of cards and lorebooks that qualify, roughly how
much space they take, and when the last backup ran if a backup schedule is
set. The button stays disabled until the owner types `purge`.

## API

| Route | Does |
|---|---|
| `GET /maintenance?keep=N&days=D` | Previews every action: rows and bytes per table, what each action would remove for N and D, and the last backup time |
| `POST /maintenance/runs` | Body `{"action": "trim-history" \| "stale-vectors" \| "clear-failures" \| "vacuum" \| "purge", "keep": N, "days": D}`. Starts the action's job and returns it |

Job kinds: `maintenance.trim-history`, `maintenance.stale-vectors`,
`maintenance.clear-failures`, `maintenance.vacuum`, `maintenance.purge`.
At most one maintenance job runs at a time.

## Testing

- Trim keeps exactly N newest values per instance, card and key, and the
  current value of each is unchanged.
- Stale vectors deletes other instances' rows and other spaces' rows, and
  nothing in the current space.
- Purge:
  - deletes a card archived D+1 days ago with all its rows, and keeps one
    archived D−1 days ago;
  - re-links a live variant to its grandparent when its parent is purged,
    including when the grandparent is purged in the same batch;
  - deletes a binding and leaves its target untouched;
  - keeps an archived lorebook a live card links to;
  - an ordinary `DELETE FROM card_revision` still fails.
- Vacuum without table ownership fails the job with a message that says so.
- Every route refuses a `write` token.
