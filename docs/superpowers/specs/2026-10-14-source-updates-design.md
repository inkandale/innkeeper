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
| What counts as a change | Upstream differs from `accepted_data`, the upstream copy last accepted. The owner's own edits never count, nor do the app-local `extensions.fav` and `extensions.world`, which the library drops. |
| Avatar | Compared separately, by the pixel hash of upstream's image (`blob.pixel_hash`) against the last-seen one, so a re-encode or new metadata isn't a change. An image that doesn't decode is compared by its sha256. |
| Baseline | The entity's first revision with a `plugin:` source, else its first revision. For an imported card, that is the imported content. The avatar hash is stored silently on the first check. |
| Pull | The existing three-way merge and resolve: head, upstream, and `accepted_data` as the ancestor. The owner can take the data, the avatar, or both. |
| Auto-apply | Off by default. With the binding's `autosync` on, the check applies an update that merges with no conflicting hunks and leaves the avatar as the library's. A different avatar waits for review: there's no ancestor avatar to say whose change it is. |
| Schedule | `INNKEEPER_SOURCE_CHECK_EVERY`, default `24h`, `0` turns it off. Plus "Check for updates" on the card page and for the whole library. |
| Where it shows | An entry on the card's or lorebook's row in the changes queue; a banner on the card page and the lorebook page; the Sync page's Sources line, with its count and a library-wide "Check for updates"; and the Sources panel, with Ignore all, Check again, and autosync for each source. |
| Shared sources | Every card with the source gets its own binding. An entity has at most one followed binding per follower instance, so a card with a chub and a risu source on one instance follows only the older. Suggesting cards as variants belongs to INN-52. |
| MCP | List pending source updates and start a check. No pull tool. |

## Core changes

Bindings today assume the target can push. These changes let a target pull
without pushing:

| Where | Today | Change |
|---|---|---|
| `targets.describe` (`internal/targets/targets.go:182`) | Refuses an instance without the `push` capability | Accepts one with `pull`. Check, Bind, and resolve go through it. Push, preview, and `CheckLorebookPush` go through `describePush`, which refuses an instance without `push` |
| Reconcile and sync | Run on any instance that can pull | Refuse an instance without `push` with `ErrCannot` |
| `binding` table | No pull-only marker | New columns: `push boolean NOT NULL DEFAULT true`, `paused boolean NOT NULL DEFAULT false`, `avatar_hash text`, `check_failures int NOT NULL DEFAULT 0`, `check_error text` |
| `binding_card_remote` and `binding_lorebook_remote` indexes | One card per `(instance_id, remote_id)` | Add `WHERE push`, so cards that share a source each get a binding |
| `binding_status` view | Any library edit since the last push makes a binding `ahead` | A binding with `push = false` is never `ahead` |
| `targets.Check` (`check.go`) | Compares projected data only | Also fetches the avatar and compares its pixel hash with `avatar_hash`. Skips `paused` bindings. |
| Changes queue (`changes.go`) | `changed` comes only from sync's reconcile snapshot | A pull-only binding with `remote_state = changed` adds an `update` entry carrying `app_hash`. Every entry carries `push`, and a source's entry its `source_id`. A row may carry `lorebook_id` instead of `card_id`. Paused bindings and removed sources are left out |
| Search (`internal/library/search.go`) | Target facets, filter, and per-hit targets read every binding | They read push bindings only, so a followed source isn't a target |
| `innkeeper plugin check` (`internal/contract`) | Checks target methods only for `push` | Also for `pull`, and a pull-only plugin must answer `-32004` for an item it doesn't have: from `target.get`, and from `target.get_lorebook` only when `lorebooks` is `standalone` |
| Scan | Hourly, every binding | Skips pull-only bindings. A source-check job kind checks them on its own schedule |
| Source check (`internal/targets`) | None | Before checking, binds each live source of a followed kind that has no binding, and unbinds pull-only bindings whose source was removed or whose kind the plugin no longer lists. A card's "Check for updates" follows only through plugins that follow one of its source kinds, removed ones included, and answers with the first failure's error status. Progress has an `applied` count. Source writes stay in `internal/library`, which never calls targets |
| Remove source (`internal/api`) | Sets `removed_at` | Also deletes the entity's followed bindings that no longer have a live source, at once and without calling a plugin, so it works while the plugin is down |
| Pause and dismiss | Any binding | Only a followed binding; an app binding is refused with `ErrCannot`. Autosync stays settable on both |

Bind writes its usual `bind` push row, so `binding_status` and the queries that
join `push` keep working unchanged.

## Owner actions

| Entry | Shown as | Actions |
|---|---|---|
| `update` | "Update on chub" | **Review**: the merge, then resolve. **Ignore this change**: a two-way resolve keeping the library side, which moves `accepted_data` to upstream. Three-way would still take the author's edits to fields the owner never touched. It takes the `app_hash` the owner saw and answers 409 with a fresh three-way comparison only when the site's data changed since; an avatar change alone doesn't count, and the avatar read at dismiss time is accepted. **Ignore all**: set `paused`. |
| `missing` | "No longer on chub" | **Ignore all**, or **Remove source**: set the source's `removed_at` so detection doesn't add it back, and unbind it at once. The card is left as it is. |
| `error` | The last error | None. Shown only after 3 failed checks in a row. |

"Check again" in the card's sources panel clears `paused`. The next check
compares against the `accepted_data` stored before the pause, so changes made
during it still show.

## Failures

`-32004`, which the plugin answers for a 404 or a hidden definition, sets
`remote_state = missing`. Every other error from reading the source increments
`check_failures` and retries on the next run; a `target.describe` failure isn't
counted. A successful check resets it to 0. A changed binding that keeps
failing still shows `update`.

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
- Lorebooks embedded in a card's page. Following, checking, and review leave
  `character_book` out of a followed card, as reconcile does. A lorebook with
  its own page follows its own source.
