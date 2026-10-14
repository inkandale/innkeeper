# Source updates

Date: 2026-10-14

Ticket: INN-51

## Purpose

Sources record which site page a card or lorebook came from, but nothing goes
back to the page to see whether its author changed it. This design checks each
source and offers the author's changes as a three-way merge in the changes
queue. It reuses app sync: a source becomes a binding to a target that can pull
but never push.

**Upstream** in this document means the site's current copy of a source.

## Decisions

| Topic | Decision |
|---|---|
| Model | A source is a **pull-only binding** on the instance whose plugin pulls that source's kind. `remote_id` is `kind:ref`. |
| Plugin | `url-import` answers `target.get` for `chub`, `risu`, and `character-tavern`, reusing its resolvers. Its manifest lists the source kinds it pulls. A new site touches only a plugin. |
| What counts as a change | Upstream differs from `accepted_data`, the upstream copy last accepted. The owner's own edits never count. |
| Avatar | Compared separately, by the hash of upstream's bytes against the last-seen hash. |
| Baseline | The entity's first revision with a `plugin:` source, else its first revision. For an imported card, that is the imported content. The avatar hash is stored silently on the first check. |
| Pull | The existing three-way merge and resolve: head, upstream, and `accepted_data` as the ancestor. The owner can take the data, the avatar, or both. |
| Auto-apply | Off by default. With the binding's `autosync` on, the check applies an update that merges with no conflicting hunks and leaves the avatar as the library's. A different avatar waits for review: there's no ancestor avatar to say whose change it is. |
| Schedule | `INNKEEPER_SOURCE_CHECK_EVERY`, default `24h`, `0` turns it off. Plus "Check for updates" on the card page and for the whole library. |
| Where it shows | An entry on the card's row in the changes queue, and a banner on the card page. |
| Shared sources | Every card with the source gets its own binding. Suggesting them as variants belongs to INN-52. |
| MCP | List pending source updates and start a check. No pull tool. |

## Core changes

Bindings today assume the target can push. These changes let a target pull
without pushing:

| Where | Today | Change |
|---|---|---|
| `targets.describe` (`internal/targets/targets.go:182`) | Refuses an instance without the `push` capability | Accepts one with `pull`. Check, Bind, and resolve go through it. |
| `binding` table | No pull-only marker | New columns: `push boolean NOT NULL DEFAULT true`, `paused boolean NOT NULL DEFAULT false`, `avatar_hash text`, `check_failures int NOT NULL DEFAULT 0`, `check_error text` |
| `binding_card_remote` and `binding_lorebook_remote` indexes | One card per `(instance_id, remote_id)` | Add `WHERE push`, so cards that share a source each get a binding |
| `binding_status` view | Any library edit since the last push makes a binding `ahead` | A binding with `push = false` is never `ahead` |
| `targets.Check` (`check.go`) | Compares projected data only | Also fetches the avatar and compares `avatar_hash`. Skips `paused` bindings. |
| Changes queue (`changes.go`) | `changed` comes only from sync's reconcile snapshot | A pull-only binding with `remote_state = changed` adds an `update` entry |
| Scan | Hourly, every binding | Skips pull-only bindings. A source-check job kind checks them on its own schedule |
| Source check (`internal/targets`) | None | Before checking, binds each live source of a followed kind that has no binding, and unbinds pull-only bindings whose source was removed. Source writes stay in `internal/library`, which never calls targets |

Bind writes its usual `bind` push row, so `binding_status` and the queries that
join `push` keep working unchanged.

## Owner actions

| Entry | Shown as | Actions |
|---|---|---|
| `update` | "Update on chub" | **Review**: the merge, then resolve. **Ignore this change**: a two-way resolve keeping the library side, which moves `accepted_data` to upstream. Three-way would still take the author's edits to fields the owner never touched. **Ignore all**: set `paused`. |
| `missing` | "No longer on chub" | **Ignore all**, or **Remove source**: set the source's `removed_at` so detection doesn't add it back. The queue drops the entry at once, and the next check unbinds it. The card is left as it is. |
| `error` | The last error | None. Shown only after 3 failed checks in a row. |

"Check again" in the card's sources panel clears `paused`. The next check
compares against the `accepted_data` stored before the pause, so changes made
during it still show.

## Failures

A transient error (a timeout, a 5xx, rate limiting) increments
`check_failures` and retries on the next run. A successful check resets it to
0. A permanent error, such as a 404 or a hidden definition, sets
`remote_state = missing`.

## Tests

Against a real PostgreSQL:

- A pull-only binding stays out of `ahead` after a library edit.
- Two cards with one source each get a binding.
- An existing card's baseline is its first `plugin:` revision.
- An upstream change to a field the owner never edited merges with no
  conflicting hunks. With `autosync` on, the check job applies it.
- An upstream change to a field the owner edited stays in the queue, even with
  `autosync` on.
- Ignore this change clears the entry, and a later upstream change brings it
  back.
- A paused binding isn't fetched, and its upstream change shows after "Check
  again".
- A new upstream avatar alone produces an `update` entry.
- Three transient failures produce an `error` entry; a 404 produces `missing`.

In `url-import`: `target.get` for each kind against recorded responses.

## Out of scope

- An MCP tool that pulls an update.
- Undoing "Ignore this change".
- A library-wide setting that turns on auto-apply for every source.
- Cheap "has it changed?" hints before fetching, such as chub's
  `lastActivityAt`.
- Lorebooks embedded in a card's page. Reconcile already leaves
  `character_book` out of comparisons, so a pull-only target doesn't list it
  in `fields`. A lorebook with its own page follows its own source.
