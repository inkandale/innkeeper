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
| Merge | `github.com/devsisters/go-diff3`, over tokens chosen per field kind: `Diff3Merge` with an ancestor, `DiffComm` without one, so every difference is a conflict. An empty ancestor would turn a whole field into one conflict. |
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
`external_id`, `url`, `linked_by` (`auto` or `owner`), `created_at`. Unique on
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
which a detector parses. The owner can remove any source. Removing an `auto`
one marks the row removed rather than deleting it, so detection doesn't add
it back.

**As an ancestor.** When the library card and the app item share a source and
a catalog plugin answers `catalog.get(kind, ref)`, its content is the ancestor
for `Compare`. `catalog.get` is defined by the archive catalog sub-project.
Until a catalog plugin is installed, such matches compare two-way.

## Merge engine

`Compare(library, app, ancestor)` returns one entry per field, each a list of
hunks. The field's kind picks the tokenizer:

| Kind | Fields | Token |
|---|---|---|
| prose | `description`, `personality`, `scenario`, `first_mes`, `mes_example`, `system_prompt`, `post_history_instructions`, `creator_notes`, lorebook entry `content` | A sentence: text up to and including `.`, `!`, `?`, or `…` followed by whitespace, or a newline. The token keeps its trailing whitespace. |
| list | `tags`, `alternate_greetings` | One element. A changed greeting is then compared as prose. |
| json | `extensions` | One key, recursively. |
| scalar | `name`, `creator`, `character_version` | The whole value. |
| blob | avatar | The whole value, with the phash distance. Never merged. |
| entries | lorebook entries | One entry, matched by `comment` when it is unique in the book, else by its sorted keys. Unmatched entries are `library_only` or `app_only`. |

Concatenating a field's tokens reproduces it byte for byte. Normalization
belongs to `content_hash`, never to the merge.

Hunk statuses: `same`, `auto_library`, `auto_app`, `conflict`,
`library_only`, `app_only`. The `auto_*` statuses need an ancestor: a hunk
only one side changed merges to that side.

A hunk's ID is `<field path>#<index>` and is stable for the same three inputs.
Changed prose hunks carry word-level highlight spans for display only.

## Reconcile job

Input, with defaults:

```json
{ "instance_id": 3,
  "match": { "name": true, "text_distance": 8, "avatar_distance": 8 },
  "split_fields": 3 }
```

The job lists the app's characters and world books, reads each one with its
avatar, and gives it a verdict:

- `bound`: a binding already names this remote ID. Compared against the
  accepted copy as ancestor.
- `exact`: a library card has the same `content_hash`.
- `likely`: candidates matched by a shared source, normalized name
  (case-folded, trimmed, spaces collapsed) when `match.name` is on,
  `text_simhash` within `match.text_distance` bits, or `avatar_phash` within
  `match.avatar_distance` bits. A shared source ranks first; the rest rank by
  signals matched, then by total distance.
- `new`: no candidate.
- `error`: this item couldn't be read or parsed. The message is kept and the
  run continues.

It also lists `missing` (library cards bound to this instance whose app item
is gone) and `unpushed` (library cards with no binding to this instance).

Each item lists its `options`: `bind` to each candidate, `import`, and
`skip`. One option is marked `suggested`, with a reason:

| Verdict | Suggested |
|---|---|
| `exact` | `bind` to the match |
| `likely`, conflicts in fewer than `split_fields` of `description`, `personality`, `scenario`, `first_mes` | `bind` to the top candidate |
| `likely`, conflicts in `split_fields` or more of them | `import`, because it has diverged |
| `new` | `import` |
| `bound` with hunks | `bind`, to resolve the drift |

A character's linked world books are listed under it as nested items, each
with its own verdict, options, and suggestion. A world book no character
links to is a top-level item. A world book several characters link to is
nested under each, and resolving it once resolves it for all.

The snapshot is the job's result, read with `GET /jobs/{id}`. Avatars read
during a run go through the normal blob path; unreferenced ones are removed by
blob GC.

If the app can't be listed, the job fails. If `auto_bind_exact` is on, an
`exact` item is bound with `default: library` as the run goes, only when all
its nested world books are `exact` or `bound` too, and the snapshot says so.

## Resolve

A resolution decides one app item:

```json
{ "instance_id": 3, "remote_id": "lv-abc", "app_hash": "…",
  "action": "bind",
  "card_id": 42,
  "choices": { "first_mes#1": "app", "extensions.lumiverse_ui#0": "keep_in_app" },
  "default": "library" }
```

`action` is one of:

- `bind`: the app item and the library card are the same card.
- `import`: make a new library card from the app item and bind it. Use this to
  split an item that has diverged into a different character.
- `skip`: do nothing. The next reconcile reports it again.

`card_id` may be any library card, not only a candidate from the snapshot.
Clients find one with `POST /search` and check it with `compare`.

`choices` apply only to `bind`, one per hunk. A hunk not named takes `default`.

A card's resolution carries one nested resolution per linked world book, in
the same shape under `lorebooks`. A nested item left out takes its suggested
option. The card's lorebook links are set from the outcome: a world book that
is bound or imported is linked to the card; a skipped one is not.

| Choice | Library card | Next push to this app | Other apps |
|---|---|---|---|
| `library` | unchanged | library value | library value |
| `app` | takes the app value | app value | app value |
| `keep_in_app` | unchanged | app value, from this instance's override | library value |
| `both` | library then app | both | both |

`both` is allowed only on list hunks. Lorebook entries match by identity, so an entry only one side has is kept (`app` or `library`, whichever has it) or dropped. `keep_in_app` writes
the existing per-instance JSON merge patch, so the difference is recorded and
stops showing as drift.

Applying a `bind`:

1. Read the app item live. If its hash isn't `app_hash`, return `409` with a
   fresh comparison.
2. Refuse a choice that names a hunk not in that comparison with `422`.
3. In one transaction, for the card and each nested world book: write one
   library revision (source `reconcile`) based on the current head, extend
   the override, and create or update the binding with the app's current copy
   as accepted. A moved head returns `409`, and nothing is written.

Nothing is pushed during resolve. Hunks resolved to `library` show as the
library being ahead, and the next push, with its preview, sends them.

The existing `POST /bindings/{id}/adopt` stays and becomes a resolution with
`default` set to `app` (into `base`), `keep_in_app` (into `override`), or
`library` (`ignore`).

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
