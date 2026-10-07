# Reconcile design

Date: 2026-10-05

## Purpose

Most owners start Innkeeper with apps that already hold cards, often the same
cards in several apps, edited apart over time. **Reconcile** lines an app's
items up against the library, says which ones are the same card, shows how
they differ, and lets the owner resolve each one. The first reconcile with an
app is the largest; later ones catch drift and new items.

This is the core loop the product sells, so the web UI, CLI, and MCP all drive
it through the same API, and the data needed to decide (verdicts,
suggestions, hunks) comes from the backend, not from any client.

First real case: a Lumiverse server with 96 characters and 33 world books,
overlapping a library imported from SillyTavern.

## Decisions

| Topic | Decision |
|---|---|
| State | Reconcile is a job whose result is a snapshot. Nothing is stored beyond the job row; a new run always reads fresh. Old snapshots are history only. |
| Writes | Reconcile only reads, unless the instance sets `auto_bind_exact` (default off), which binds exact matches. Everything else is an explicit resolution. |
| Merge | `github.com/devsisters/go-diff3`, over tokens chosen per field kind: `Diff3MergeIndices` with an ancestor, `DiffComm` without one, so every difference is a conflict. An empty ancestor would turn a whole field into one conflict. |
| Ancestor | A bound item's ancestor is the binding's accepted copy. A first-time match whose sides share a source uses the source's content, when a catalog plugin can return it. Otherwise there is none. |
| Sources | A card or lorebook links to the sites it came from, as a Stash scene links to stash-box IDs. Links are detected from known extensions or added by the owner. A shared source is the strongest match signal. |
| Manual targets | `bind` takes any library card, not only the listed candidates. |
| Granularity | The owner decides per hunk, inside an item-level action. |
| Suggestions | The backend lists every option for an item and marks one as suggested. Clients display them; they don't compute them. A suggestion never removes an option. |
| Thresholds | Match thresholds are parameters of each run, with defaults. |
| Linked lorebooks | A card's linked lorebooks are resolved inside the card's resolution, as Stash resolves a scene's performers. |
| Corpus | Real SFW cards from public sites, committed with attribution, plus each app's captured read of them. |

## Terms

- **App item**: a character or world book in a target app, named by its
  remote ID.
- **Hunk**: one contiguous run of tokens that is the same, or differs, between
  the library and the app copy of a field.
- **Verdict**: what reconcile concluded about an app item: `bound`, `exact`,
  `likely`, `new`, or `error`.
- **Source**: a reference to where a card or lorebook was published, such as
  a chub.ai character, given as a kind and a ref.

## Work, in order

Each piece is built and tested on its own.

1. **Lumiverse `get` parity.** Return the avatar and lorebook links
   (`extensions.world_book_ids`), as the SillyTavern plugin does.
2. **Corpus and capture.** See [Testing](#testing).
3. **Merge engine**, `internal/merge`. Pure functions, no database.
4. **Sources**: the tables, detection, and manual links.
5. **Reconcile job**, `target.reconcile`.
6. **Resolve.**
7. **Surfaces**: API, CLI, MCP.

Lorebooks go through 3 to 7 alongside cards.

## Sources

`card_source` and `lorebook_source`: `id`, the entity ID, `kind`, `ref`,
`external_id`, `url`, `linked_by` (`auto` or `owner`), `removed_at`, `created_at`. Unique on
entity, `kind`, and `ref`. An entity can have several sources. The existing
`provenance` string stays as the record of how the entity entered the library.

**Detection.** Import, bind, and reconcile read known extension keys from the
incoming data and record what they find, with `linked_by: auto`. The first is
`extensions.chub`, which both SillyTavern and Lumiverse write when they import
from chub.ai (81 of 96 characters on the first Lumiverse server):

```json
{ "id": 3604386, "full_path": "frerena/the-demon-of-desire-6bf0cd2c0915" }
```

becomes `kind: chub`, `ref: frerena/the-demon-of-desire-6bf0cd2c0915`,
`external_id: 3604386`. Each further site is one more detector.

**Manual.** The owner adds a source by kind and ref, or by pasting a URL,
which a detector parses. The owner can remove any source. Removal marks the
row removed rather than deleting it, whether it is `auto` or `owner`, so
detection never adds it back. Adding the same kind and ref again restores it
as `owner`.

**As an ancestor.** When the library card and the app item share a source and
a catalog plugin answers `catalog.get(kind, ref)`, its content is the ancestor
for `Compare`. `catalog.get` is defined by the archive catalog sub-project.
Until a catalog plugin is installed, such matches compare two-way.

## Merge engine

`Compare(library, app, ancestor)` returns one entry per field, each a list of
hunks. The field's kind picks the tokenizer:

| Kind | Fields | Token |
|---|---|---|
| prose | `description`, `personality`, `scenario`, `first_mes`, `mes_example`, `system_prompt`, `post_history_instructions`, `creator_notes`, lorebook entry `content` | A sentence: text up to and including `.`, `!`, `?`, or `…`, plus any closing quote, `*`, or `)`, followed by whitespace; or up to a newline. The token keeps its trailing whitespace. |
| list | `tags`, `alternate_greetings` | One element. |
| json | `extensions` | One key, recursively. |
| scalar | `name`, `creator`, `character_version` | The whole value. |
| blob | avatar | The whole value, with the phash distance. Never merged. |
| entries | lorebook entries | One entry, matched by `comment` when it is unique on that side (`c:<comment>`), else by its sorted keys (`k:<keys>`); key order is ignored. Unmatched entries are `library_only` or `app_only`. |

Concatenating a field's tokens reproduces it byte for byte. Normalization
belongs to `content_hash`, never to the merge.

Hunk statuses: `same`, `auto_library`, `auto_app`, `conflict`,
`library_only`, `app_only`. The `auto_*` statuses need an ancestor: a hunk
only one side changed merges to that side.

A hunk's ID is `<field path>#<index>`, with `\ . [ ] # , ~` backslash-escaped in each path segment, and is stable for the same three inputs. A prose or list field missing on one side is one whole-value hunk.
Changed prose hunks carry word-level highlight spans for display only.

## Reconcile job

Input, with defaults:

```json
{ "instance_id": 3,
  "match": { "name": true, "text_distance": 8, "avatar_distance": 8 },
  "split_fields": 3 }
```

The job lists the app's characters and world books, reads each one with its
avatar, and gives it a verdict. Both sides are compared as the target sees
them: the library's head with this instance's override, limited to the fields
the target stores, and without `character_book`.

- `bound`: a binding already names this remote ID. Compared against the
  accepted copy as ancestor. The avatar isn't read. A live binding's ID is
  the item's `binding_id`.
- `exact`: a library card has the same content hash.
- `likely`: candidates matched by a shared source, normalized name
  (case-folded, trimmed, spaces collapsed) when `match.name` is on, text
  simhash within `match.text_distance` bits, or `avatar_phash` within
  `match.avatar_distance` bits. A shared source ranks first; the rest rank by
  signals matched, then by total distance, then by ID. An item keeps its top
  10 candidates.
- `new`: no candidate.
- `error`: this item couldn't be read or parsed. The message is kept and the
  run continues.

An avatar that can't be stored only costs the item its avatar signal. A
library error, such as a failed source lookup, fails the run.

It also lists `missing` (library cards bound to this instance whose app item
is gone) and `unpushed` (library cards with no binding to this instance). An
app can reuse a missing item's remote ID: the live binding owns it, and the
old binding's card is `missing`.

Each item lists its `options`: `bind` to each candidate, `import`, and
`skip`. One option is marked `suggested`, with a reason. A candidate already
bound to another app item on this instance is marked `bound_to` and passed
over for the suggestion. Core fields are `description`, `personality`,
`scenario`, and `first_mes`; a field differs when it has any hunk that isn't
`same`.

| Verdict | Suggested |
|---|---|
| `exact` | `bind` to the first match not bound elsewhere; `skip` if all are, because the app has a duplicate |
| `likely`, fewer than `split_fields` core fields differ from the top unbound candidate | `bind` to that candidate |
| `likely`, `split_fields` or more differ | `import`, because it has diverged |
| `likely`, every candidate bound elsewhere | `import` |
| `new` | `import` |
| `bound` with any hunk that isn't `same` | `bind`, to resolve the drift |

A character's linked world books are listed under it as nested items, each
with its own verdict, options, and suggestion. A world book no character
links to is a top-level item. A world book several characters link to is
read once and nested under each, and resolving it once resolves it for all.
World books have no avatar signal and ignore `split_fields`: a `likely` world
book suggests `bind` to its top unbound candidate.

The snapshot is the job's result, read with `GET /jobs/{id}`. Job lists and
the job stream show only its `totals`. Avatars read
during a run go through the normal blob path; unreferenced ones are removed by
blob GC.

If the app can't be listed, the job fails. If `auto_bind_exact` is on, an
`exact` item is bound with `default: library` once every item is read, only
when each nested world book is `exact`, or `bound` with nothing differing.
The binds use the hashes the run read, so an item that changed since is left
for the owner, and they never write a library revision. The snapshot marks
each item `auto_bound` and counts them in `totals.auto_bound`. An auto-bind
that fails leaves the item as reported and doesn't fail the run.

## Resolve

A resolution decides one app item:

```json
{ "instance_id": 3, "remote_id": "lv-abc", "app_hash": "…",
  "base_revision_id": 118, "accepted_hash": "", "binding_id": 0,
  "action": "bind",
  "card_id": 42,
  "choices": { "first_mes#1": "app", "extensions.lumiverse_ui#0": "keep_in_app" },
  "default": "library" }
```

`action` is one of:

- `bind`: the app item and the library card are the same card.
- `import`: make a new library card from the app item and bind it. Use this to
  split an item that has diverged into a different character. It always makes
  a new card, even when one matches exactly. A card already bound to the item
  on this instance becomes `missing` there, as when an app reuses an ID, so
  pushing that card again creates a new copy in the app. What the import
  leaves out of the card, like SillyTavern's favorite star, goes into this
  instance's override. On a target that keeps world books on their own and
  doesn't list `character_book` among its fields, the card's embedded
  `character_book` is ignored; its linked world books are the nested items.
  It needs `app_hash` and `binding_id`. If the item's live binding isn't
  `binding_id`, the import is stale, so sending it twice makes one card.
- `skip`: do nothing, including the nested world books. The next reconcile
  reports it again.

`card_id` may be any library card, not only a candidate from the snapshot.
Clients find one with `POST /search` and check it with `compare`.

`app_hash`, `base_revision_id`, `accepted_hash`, and `binding_id` come from
the comparison. `accepted_hash` is empty when the item isn't bound to the
card; a missing value means empty. `base_revision_id` is required.
`binding_id` is the item's live binding, `0` or absent when it has none; a
snapshot item carries it too, for an import without a comparison. Only
`import` checks it, so splitting a bound item needs its `binding_id`.

`choices` apply only to `bind`, one per hunk. A hunk not named takes `default`.
In a field or setting this instance overrides, choices change the override,
never the base. On a list the override already holds whole, `keep_in_app` is
allowed.

The `avatar` key takes `library` or `app`, and only when the avatars differ.
`app` needs an app avatar, so an app without one can't clear the library's.

A card's resolution carries one nested resolution per linked world book, in
the same shape under `lorebooks`. A nested item left out takes its suggested
option, from a fresh read; one the app can't give back is skipped, and a bound
one with no drift binds again, which changes nothing. The card's lorebook links
are set from the outcome: a world book that is bound or imported is linked to
the card; a skipped one is not. On a target that keeps world books on their
own, skipping one the app links means the next push removes that link in the
app. A world book listed twice in one resolution is resolved once. The card and
its world books resolve in one transaction, so one failure writes nothing.

Suggestions for left-out world books are all read before anything is written.
If an earlier world book in the same resolution changed a suggestion's match,
the resolve fails with `409` naming both world books, and isn't stale: give
the left-out one an explicit resolution. A stale error names its item, as
`card:<remote_id>` or `lorebook:<remote_id>`.

A bulk resolve takes a list of resolutions, each with `"kind": "lorebook"`
for a top-level world book (default `card`). Every resolution's shape is
checked before the job is queued. Each runs as its own child job in its own
transaction, and its progress has an `outcome`: the action, or `stale` (with
the fresh comparison), `conflict`, or `failed`. A failed child doesn't stop
the others; a plugin error that may pass is retried. Because children don't
share a transaction, a world book two cards both name explicitly is bound by
whichever runs first, and the other's resolution goes stale. Leave it out of
the second card to have it bind as its suggestion instead. A child retried
after its resolve already applied reports `stale`, so the client compares
again.

| Choice | Library card | Next push to this app | Other apps |
|---|---|---|---|
| `library` | unchanged | library value | library value |
| `app` | takes the app value | app value | app value |
| `keep_in_app` (not in lists or lorebook entries) | unchanged | app value, from this instance's override | library value |
| `both` | library then app | both | both |

`keep_in_app` writes the existing per-instance JSON merge patch, so the
difference is recorded and stops showing as drift. It isn't offered on hunks
in a list field or in lorebook entries: a merge patch replaces arrays whole,
so it would pin the app's whole list or book, and that app would stop getting
library edits to it. A `keep_in_app` default takes `library` on those hunks.

Lists offer `both` as the merge instead; `both` is allowed only on list hunks.
Lorebook entries match by identity, so an entry only one side has is kept
(`app` or `library`, whichever has it) or dropped, and a differing field takes
one side.

Applying a `bind`:

1. Read the app item live. If its hash isn't `app_hash`, the library head
   isn't `base_revision_id`, or the binding's accepted hash isn't
   `accepted_hash`, return `409` with a fresh comparison. A gone item is `404`.
2. Refuse a choice that names a hunk not in that comparison with `422`.
3. In one transaction, for the card and each nested world book: write one
   library revision (source `reconcile`) based on the current head, extend
   the override, and create or update the binding with the app's current copy
   as accepted. A moved head returns `409`, and nothing is written. A card
   bound to another live item is refused; one whose copy went missing is
   rebound to this item.
4. If none of that would change anything, nothing is written.

Nothing is pushed during resolve. Hunks resolved to `library` show as the
library being ahead, and the next push, with its preview, sends them.

Both stay available. `POST /bindings/{id}/adopt` stays its own path for drift
on a bound item: it takes the app's changes since the last push into the
card, into this target's override, or nowhere, for the whole card. Resolve is
for reconciling an item with per-hunk choices. Under three-way merge an app
edit since the last push merges on its own, so adopt's `override` and
`ignore` can't be expressed as a resolve default.

`POST /cards/{id}/bind` is a resolve: `{action: bind, card_id, default:
library}` with a fresh comparison's hashes. The app's copy becomes the
accepted one, and the card is `ahead` where it differs. Its linked world books
are skipped and the bind refuses any library write, so it changes nothing in
the library; reconcile resolves the world books.

## Surfaces

| Need | API | CLI | MCP |
|---|---|---|---|
| Run a reconcile | `POST /reconciles {"instance_id"}` → job | `innkeeper reconcile <instance>` | `start_reconcile` |
| Read the snapshot | `GET /jobs/{id}` | `innkeeper jobs show <id>` | `get_job` |
| Compare one pair | `GET /instances/{id}/items/{remote_id}/compare?card_id=` | `innkeeper compare <instance> <remote_id> [card]` | `compare_item` |
| Resolve one item | `POST /instances/{id}/items/{remote_id}/resolve` | `innkeeper resolve <instance> <remote_id> …` | `resolve_item` |
| Resolve many | `POST /resolutions` → job, per-item results | `innkeeper resolve --suggested <job>` | `resolve_items` |
| Auto-bind setting | `PATCH /instances/{id} {"auto_bind_exact"}` | `innkeeper call PATCH /instances/<id> …` | none |
| List, add, remove sources | `GET`, `PUT`, `DELETE /cards/{id}/sources`, and the same under `/lorebooks/{id}` | `innkeeper sources <card> [add <url> \| remove <id>]` | `list_sources`, `add_source`, `remove_source` |

`card_id` is omitted for a bound item. To start from a library card instead,
pick the app item from `GET /instances/{id}/items` and resolve it with that
`card_id`; `POST /cards/{id}/bind` becomes this call with `default: library`. `resolve --suggested <job>` applies
every suggestion in a snapshot, each re-checked live.

## Testing

- **Corpus.** About 15 SFW cards from Character Tavern and RisuRealm, chosen
  to cover V2 PNG, V3 PNG, JSON, CharX, an embedded lorebook, alternate
  greetings, depth prompt, and regex scripts. `testdata/corpus/README.md`
  records each card's creator, URL, and license. The owner approves the list
  before it is committed. chub.ai is unreachable from the build network.
- **Capture.** A script pushes each corpus card into throwaway SillyTavern and
  Lumiverse containers and saves what the codec and each plugin's `get`
  return, next to the source card. Rerun it after an app upgrade; the golden
  diff shows what the app changed. CharX fails in the codec today; capture
  records that, and adding CharX is a separate ticket.
- **Normalization.** Every captured variant of a card has the source's
  `content_hash`, and `Compare` between them has only `same` hunks. Each known
  app difference is a named case, including one app's keys carried into
  another: the first Lumiverse server's characters hold SillyTavern's `fav`
  (67 of 96) and `world` (38), and an embedded `character_book` (26).
- **Sources.** Detection from each captured variant, URL parsing, removal
  that survives re-detection, and a shared source ranking first.
- **Merge engine.** Table tests per tokenizer: byte-exact round trip, hunk
  statuses with and without an ancestor, stable hunk IDs.
- **Reconcile.** Integration tests against real PostgreSQL with the fake
  plugin, seeded with an exact match, a near match, a split, a new item, a
  missing card, an unreadable item, and a world book shared by two cards.
  Assert verdicts, options, and suggestions, and that changing the thresholds
  moves items between `likely` and `new`.
- **Resolve.** Each choice, nested world books (bound, imported, skipped,
  shared), `409` on a changed app item, `422` on an unknown hunk, rollback
  when a write fails.
- **Live.** Opt-in, never in CI: reconcile the homelab Lumiverse and print
  the verdict counts. Writes nothing unless `auto_bind_exact` is on.

## Out of scope

- `catalog.get` itself, and checking a source for upstream updates. Both come
  with the archive catalog.
- Merging the avatar.
- The web UI screens. They use the API above.
