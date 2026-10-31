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

Keeps the newest N values, by revision, then `created_at`, then `id`, for
each instance, card and key, and deletes older ones. That is the order
search, facets and the grid pick a card's current value by, so the action
never changes what they show. N defaults to 3 and must be 1 to 100; the
job refuses any other N. It deletes 10,000 rows per statement, with no
statement timeout.

Preview: the number of rows it would delete for the chosen N.

### Remove stale vectors

Deletes the rows in `card_embedding` that Similar cards and semantic
search can't read:

- vectors from instances other than the vector instance;
- vectors in a space other than the vector instance's current one. Core
  learns that space by sending the vector instance one `vector.query` for
  a fixed text.

With no vector instance, nothing is removed: a disabled instance's vectors
are kept for when it's back. The cards whose vectors go fall back to similar-by-tags until their plugin runs on them again.

Shown only when vectors are available.

### Clear recorded failures

Empties `enrich_failure`, so the hourly sweep tries those cards again, for
example after a prompt is fixed or a model is changed.

### Vacuum and analyze

Runs `VACUUM (ANALYZE)` on the database, which makes space freed by
deletes reusable and refreshes query plans. It doesn't shrink the files on
disk. It runs with no statement timeout. It needs the database user to own the tables; if it doesn't, the job
fails and says so.

Preview: database size and the five largest tables. The job's progress
shows the size after.

### Purge archived cards

Permanently deletes cards and lorebooks archived at least D days ago. D
defaults to 30 and must be at least 1; the job refuses any other D. A lorebook is purged only if no
live card links to it.

What happens to everything that points at a purged card:

| Points at it | Rule |
|---|---|
| Its revisions, tags, enrichment values, vectors, failure records, collection memberships, lorebook links, sources | Deleted with it |
| A live card whose `variant_of_id` is a purged card | Re-linked to the nearest ancestor that isn't purged, or made a root if there is none, so variant families stay connected |
| A binding to an app it was pushed to | Deleted. The app's copy is left alone; archiving's "unpublish" option removes copies |
| Its blobs, such as avatars | Left to the daily blob garbage job, which deletes those no remaining revision uses |

Purge reuses core's delete code: `Entities.deleteIn`, which already sets
`innkeeper.delete` to get past the revision trigger and deletes what hangs
off each card, and `Library.DeleteCards`, which `POST /cards/purge` uses.
Two changes to that shared code make every delete path, including library
deletion, follow the rules above:

- `DeleteCards` stops keeping cards bound to an app: it deletes the
  binding like library deletion does. `POST /cards/purge` and its job lose
  their "refused" list.
- `deletes()` re-links live variants to their nearest surviving ancestor
  instead of making them roots.

Lorebooks are deleted through the same `deleteIn`.

The job purges in batches of 500, one transaction each, so it holds no
long lock and reports progress. Each batch selects and locks its rows with
the purge rule applied, so a card unarchived or a lorebook linked after the
preview is kept.

The tab shows the number of cards and lorebooks that qualify, roughly how
much space they take, and when the last backup ran if a backup schedule is
set. The button stays disabled until the owner types `purge`.

## API

| Route | Does |
|---|---|
| `GET /maintenance?keep=N&days=D` | Previews every action: row and card counts for N and D, the database size and five largest tables, and the last backup time |
| `POST /maintenance/runs` | Body `{"action": "trim-history" \| "stale-vectors" \| "clear-failures" \| "vacuum" \| "purge", "keep": N, "days": D}`. Starts the action's job and returns it |

One job kind, `maintenance`, whose payload names the action. At most one
runs at a time: starting another while one is queued or running returns
that one. A failed run can't be retried: the owner starts a new one after
reading the preview.

## Testing

- Trim keeps exactly N newest values per instance, card and key, and the
  current value of each is unchanged.
- Trim keeps the head revision's value over a newer row on an older revision.
- Trim refuses N outside 1 to 100, and purge refuses D below 1, deleting nothing.
- Stale vectors deletes other instances' rows and other spaces' rows, and
  nothing in the current space; with no vector instance it deletes nothing.
- Purge:
  - deletes a card archived D+1 days ago with all its rows, and keeps one
    archived D−1 days ago;
  - re-links a live variant to its grandparent when its parent is purged,
    including when the grandparent is purged in the same batch;
  - deletes a binding and leaves its target untouched;
  - keeps an archived lorebook a live card links to;
  - keeps a card unarchived after the preview;
  - an ordinary `DELETE FROM card_revision` still fails;
  - `POST /cards/purge` deletes a pushed card's binding too.
- Vacuum without table ownership fails the job with a message that says so.
- Every route refuses a `write` token.
