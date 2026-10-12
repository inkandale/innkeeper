# Changes Queue Design

**Goal:** One queue of everything that needs the owner, with one row per
library card, because cards are the source of truth. Each row shows how the
card stands in every app and offers the action that settles it.

**Builds on:** `2026-10-11-single-sync-design.md`. A sync applies the obvious
cases and leaves the rest. This spec replaces the per-app review queue with a
per-card one and points the Sync page at it.

**Why:** After a sync, what the owner must do was spread over per-app queues
reached from a small "Last sync" link, and the Sync page's counts ("New in
app", "Drifted") didn't say what to do next.

---

## Terms

- **Change:** an app's state for one card that the owner must settle: the app
  differs from the card, the card is ahead in the app, or the app lost its
  copy. In sync and auto-applied items aren't changes.
- **Orphan:** an app item that isn't bound and has no candidate card. It gets a
  row of its own.

## The rows

`GET /sync/changes` builds the queue on request; nothing new is stored. It
reads:

- each app's latest finished reconcile snapshot (the per-app jobs a sync
  records), for the items the run left: likely matches, exact matches it
  didn't bind, bound items edited on both sides, and errors;
- `binding_status`, for bindings that are `ahead` or `missing`.

| App state | From | Row | Actions |
|---|---|---|---|
| `differs` | a snapshot item left unbound whose best candidate is this card (likely, or exact but not auto-bound) | the candidate card | review: bind to it, or import as a new card |
| `both_changed` | a bound snapshot item that isn't app-only and has differing fields | the bound card | review |
| `ahead` | `binding_status` `ahead` | the bound card | push, with its preview |
| `missing` | `binding_status` `missing` | the bound card | push again, or unbind |
| `error` | a snapshot item with an error | the bound or candidate card, else an orphan row | none; shows the error |
| `new` | an unbound snapshot item with no candidate | an orphan row, named "*name* (*app* only)" | import or skip |

A row holds the card's ID and name (or the orphan's app item), and one entry
per app with a change: the instance, the state, the remote ID, the snapshot job
ID when the state came from one, and the item's suggestion. Rows with orphans
come first, then cards by name.

An app item can be a candidate for one card only in the queue: its best
unbound candidate, as the snapshot ranks them. Its other candidates stay
reachable in review.

ponytail: rows are built from every app's latest snapshot on each request.
Store them if a library outgrows that.

## Surfaces

**API:** `GET /sync/changes`, tier `read`, with optional `instance_id` and
`state` filters. It returns `{rows: [...], counts: {<instance_id>:
{<state>: n}}}`.

**Web:**

- **Sync page:** columns Target, In sync (cards), Needs you, Ahead, Missing,
  Last sync. Needs you is that app's `differs`, `both_changed`, `error`, and
  `new` count and links to the queue filtered to the app. Ahead and Missing
  link to the queue filtered to that app and state. Can't check shows only
  when it isn't zero. A Changes link above the table opens the whole queue.
- **Queue** (`/sync/changes`): filters by app and by state; a checkbox per
  row; "Apply suggested" resolves the checked reviews that have a suggestion
  and pushes the checked `ahead` entries, asking with the push preview first.
  "Review one by one" opens the first review.
- **Review:** after a decision it opens the next change from the queue, with
  "3 of 32", and a Later button that moves on without deciding. With nothing
  left it returns to the queue.
- **Card panel:** an app's row that has a change links to the card's queue
  row.
- **Retired:** `/sync/reconciles/:job` becomes a read-only record of the run,
  reached from the job list, with no apply. "New in app" and "Drifted" leave
  the Sync page.

**CLI:** `ink sync changes [--app <id>] [--state <state>]` lists the rows.

**MCP:** `list_changes`, tier `read`, with the same filters.

## Testing

- `internal/targets`: two fake apps and a sync that leaves one likely match,
  one bound item edited on both sides, one ahead binding, one missing binding,
  one error, and one orphan. `Changes` returns one row per card with the
  right state per app, and the orphan row first; each filter narrows it.
- An exact match the run auto-bound and an app-only edit it pulled are not
  rows.
- API: the route and its tier. CLI and MCP: the request and output.
- Web: the Sync page links, the queue's filters and Apply suggested, and
  Review's next, Later, and counter.
