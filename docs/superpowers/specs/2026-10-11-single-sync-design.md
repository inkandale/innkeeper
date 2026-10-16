# Single Sync Design

**Goal:** One process changes the library and the apps: **Sync**. It runs
through every app in turn, applies what is obvious, pushes the bindings the
owner chose to autosync, and leaves the rest in one review queue.
**Refresh** only reads and shows what a sync would find.

**Builds on:** `2026-10-09-sync-design.md`, which made Sync the one way to
bring one app's items in. This spec widens a run to every app, adds pulling
app-only edits and autosync pushes, schedules the run, and removes the other
write paths.

**Why:** Per-app syncs ran side by side on the worker, and each read the
library once at its start, so two apps imported the same card twice. Every
extra path that writes is another chance to interleave.

---

## Terms

- **Autosync:** a switch on one binding (a card in one app). With it on, a
  sync pushes the library's copy to that app when the card is ahead.
- **App-only edit:** a bound item whose app copy changed since the last push
  or resolve while the library's head did not.

## The sync run

Job kind `target.sync`, with no instance. It takes a session advisory lock
on `sync` for the whole run and handles every target instance that can pull,
in ID order, in two passes.

**Pass 1, for each app:**

1. **Reconcile and apply the obvious.** As `2026-10-09-sync-design.md`
   describes: bind exact matches, import new cards and world books. The
   import's exact-match lock (`exactMatch` in `internal/targets/resolve.go`)
   stays as a backstop.
2. **Pull app-only edits.** Resolve the item as `bind` to its card with every
   hunk from the app. The library gets a new head revision with source
   `plugin:<name>`.

**Pass 2, for each app:** push every binding with autosync on whose status is
`ahead`. A binding whose app copy changed since its last check is not pushed;
it goes to review.

Pass 1 runs for every app before pass 2, so an edit pulled from one app
reaches the others in the same run.

**Left for review:** `likely` matches, items changed on both sides, missing
items, and errors.

**Failures:** an app that fails to list, read, or push fails its own part of
the run; the run logs it, records it in the result, and goes on. An item that
changed between the read and the resolve is stale and stays in the queue.

**One run at a time:** starting a sync while one is queued or running returns
that job instead of enqueueing a second, as `Queue.EnsureScheduled` does for
schedules.

**Result:** the job result holds per-app totals (`auto_bound`,
`auto_imported`, `pulled`, `pushed`, `review`, `errors`) and the snapshot
items of every app, which the review queue reads.

## Autosync

- Migration: `binding.autosync boolean NOT NULL DEFAULT false`. Every
  existing binding starts off, so nothing pushes on deploy.
- `PATCH /bindings/{id}` takes `{"autosync": bool}`, tier `destructive`, as
  push is: autosync replaces the app's copy without asking.
- Turning it on pushes nothing until the next sync.

ponytail: one switch per binding; add per-app defaults if switching cards one
by one gets tedious.

## Refresh

Refresh starts `target.scan`. The scan keeps checking every binding and adds
one read-only pass: list each app's items and count those with no binding as
"new in app". It does not match them against the library; sync does that.

Scan progress gains per-app counts: `new_in_app`, `remote_changed`, `ahead`,
`diverged`, `missing`.

## Schedule

The worker schedules `target.sync` every hour. `INNKEEPER_SYNC_EVERY` sets
the interval; `0` turns the schedule off. `target.scan` keeps its hourly
schedule, so counts stay current with sync off.

ponytail: one interval for all apps; add per-app intervals if one app is slow
or rate-limited.

## Locking

A sync holds `pg_advisory_lock` on `sync` for its run. A manual push and a
review apply take `pg_advisory_xact_lock` on the same key, so they wait for a
running sync instead of interleaving with it.

## Avatar comparison

Bind and push compare avatars by blob `sha256`. SillyTavern embeds the card
in its PNG, so the same picture from two apps has different bytes, and every
card bound to a second app shows `ahead`.

- Migration: `blob.pixel_hash text`, the SHA-256 of the decoded RGBA pixels,
  set at ingest where the image is already decoded for `avatar_phash`.
- Avatars are equal when both have a `pixel_hash` and it matches, or when
  their `sha256` matches.
- An older blob gets its `pixel_hash` the first time it is compared. Bindings wrongly
  `ahead` clear at their next resolve or push.

ponytail: exact pixels; an app that resizes or recompresses would still
differ. Use `avatar_phash` distance 0 if that turns up.

## Surfaces

| Surface | Added or changed | Removed |
|---|---|---|
| API | `POST /syncs` starts or joins the run; `{"instance_id"}` limits it to one app, `{"apply": "none"}` previews. `POST /scans` refreshes. `PATCH /bindings/{id}` takes `autosync`. | `POST /bindings/{id}/adopt`. `POST /reconciles` stays as a deprecated one-app alias. |
| CLI | `ink sync run [instance-id]`, `ink sync refresh`, `ink binding autosync <id> on\|off` | `ink sync adopt`, which errors naming `ink sync resolve` |
| MCP | `start_sync` with an optional instance, `refresh`, `autosync` on the binding tool | `adopt` |
| Web, Sync page | Refresh and Sync buttons; per-app table of scan counts and the last sync's totals; link to the review queue | Per-app Sync and Push ahead buttons |
| Web, card panel | Autosync toggle per app; a changed row links to the item in review | Adopt |

Adopt's base and ignore modes map onto review's app and library choices. Its
override mode has no full equivalent: review keeps a change in one app only
when both sides changed that field, and a sync pulls a change made only in an
app into the library. That is decided: the library wins, and a version meant
for one app is a separate card.

Kept: the push preview and confirm from a card or a query, and the review
queue.

Removing adopt is not marked as a breaking change: every client ships from
this repo, so it doesn't need a major version.

## Testing

All in `internal/targets` against the test database unless noted.

- Two fake apps holding the same card, plus one card edited in each: one run
  ends with one card per character bound in both apps, and both edits pulled
  as revisions.
- An app-only edit in app A with autosync on for app B is pushed to B in the
  same run.
- An item edited on both sides writes nothing and is in review.
- With autosync off, a library change is not pushed.
- One app failing to list: the other app still syncs, and the result records
  the error.
- A second start while a sync runs returns the running job. A manual push
  started during a sync waits for it.
- The same PNG with and without a `tEXt` chunk is not `ahead`; one changed
  pixel is.
- API, CLI, MCP, and web tests updated for the new and removed surfaces.

## Rollout

Each step ships on its own:

1. Avatar comparison.
2. The sync run, pulling app-only edits, the schedule, and the lock.
3. Autosync.
4. Surfaces, including removing adopt.
