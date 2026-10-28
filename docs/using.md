# Using Innkeeper

## How it fits together

- Your **library** holds your cards and lorebooks, with every revision, so
  any edit can be undone. You can keep more than one, such as one for an
  imported archive.
- **Plugins** add what Innkeeper works with: the apps you chat in, ways to
  bring cards in, and AI tools. Install them from the setup wizard or
  Settings › Plugins.
- An **instance** is one configured copy of a plugin, such as one
  SillyTavern server. Two servers are two instances of the same plugin.
- An app plugin is a **target**: Innkeeper pushes cards to it and reads them
  back. Each app syncs with one library.
- Pushing a card to an app **binds** it to the app's copy, so later changes
  on either side show up on the Sync page. You choose what happens to them.
- A **source** is the page a card came from, such as on chub.ai. Innkeeper
  tells you when its author updates it.
- A **variant** is a card made from another one, such as a rewrite. Variants
  group under their original in the library.
- A **collection** is a hand-picked set of cards within a library.

## Web UI

The web UI is at the server's root, for example `http://localhost:8080/`.
Log in with any login provider (see
[First login](running.md#first-login)).

After the claim, the setup wizard at `/welcome` walks you through choosing
plugins from the catalog, setting up each one, and importing cards. It shows
once; to open it again, use "Run setup wizard" in Settings.

To restyle the UI, install a [theme plugin](protocol.md#themes) and
select it under Settings › General, which also shows Innkeeper's version.
A theme's CSS runs on the UI's origin, so install only themes you trust.

## Importing

The `card-import` plugin, which the setup wizard installs, reads CCv3, V2,
and V1 cards as PNG or JSON, and lorebooks as CCv3 or SillyTavern world info
JSON. Importing the same file again adds nothing.

To import a folder, put it under `./import`, or set `INNKEEPER_IMPORT_DIR`
in `.env`. Compose mounts it read-only at `/import`, the folder `card-import`
reads unless its settings name another. Then use **Import folder** in the
wizard's "Bring in cards" step or on the Cards page, or:

```bash
ink import <instance-id>                         # everything in /import
ink import <instance-id> '{"path": "fantasy"}'   # only /import/fantasy
```

To import one file, card or lorebook:

```bash
ink cards import <instance-id> brakka.png
```

Over the API, upload the file to `POST /api/blobs`, then within 24 hours
start an import with `{"blob": "<sha256>", "name": "brakka.png"}` as its
params.

To import a card from its page on chub.ai, RisuRealm, or Character Tavern,
give the built-in `url-import` plugin the page URL. The server creates its
instance, named `url-import`, the first time it starts:

```bash
ink instances list
ink cards import-url <instance-id> https://chub.ai/characters/user/name
```

The web's "Bring in cards" step has a URL field while the instance exists.
Over the API, start an import with `{"url": "<page URL>"}` as its params;
over MCP, send the same params to `start_job` with `start-import`.

## Sources

A source is a site a card or lorebook was published on, such as its chub.ai
page. Importing, syncing an app, and binding to an app item record the sources
the data names: the `extensions.chub` entry that chub.ai imports leave, and
each page URL in the card's `data.source` list, which `url-import` adds.
Plugins with the `sites` capability decide which sites these are; the
built-in `url-import` knows chub.ai, RisuRealm, and Character Tavern. An
import's sources appear a few seconds after it finishes, when the
`detect-sources` job runs.

At startup and after an install, the server creates the instance of every
plugin that allows one instance, isn't a theme or login plugin, and needs no
secrets or required settings. An instance you delete stays deleted. Until a
`sites` instance works, nothing is detected; imported entities wait and are
detected then.

Add or remove one yourself with the API, MCP tools, or CLI:

```bash
ink cards sources add 12 https://chub.ai/characters/user/name
ink cards sources remove 12 3
ink lorebooks sources add 5 https://chub.ai/lorebooks/user/name
```

A removed source stays removed: detection won't add it back.

## Variants

A **variant** is a card or lorebook that records the one it came from, such as
an NSFW rewrite or an author's v2. A **family** is a card and every variant
below it. The link only organizes the library: editing a card never changes its
variants, and exports and app pushes leave the link out.

| To | API | MCP tool |
|---|---|---|
| Copy a card into a new variant | `POST /cards/{id}/variants` | `fork_card` |
| Link a card to its parent | `PUT /cards/{id}/variant_of` `{"id": <parent>}` | `set_variant_of` |
| Unlink it | `DELETE /cards/{id}/variant_of` | `set_variant_of` with `variant_of` null |

Lorebooks have the same routes under `/lorebooks`, and the MCP tools take
`kind: "lorebook"`. A fork copies the content, image, overrides, collections,
and linked lorebooks, but not sources or app bindings. A link that would make a
card its own ancestor fails with 409.

Search takes three options for families:

- `group: "family"` returns one hit per family, showing its shallowest live
  member, with `family: {root_id, size, matched}`. Paging and `total` count
  families. The library list groups by default; "Ungroup variants" turns it
  off.
- The `variant_of_root: <id>` filter returns a family's members.
- The `shares_source: true` filter finds cards that share a source with another
  card, which are often variants. Select them and use "Make variants of…".

When reviewing a sync, "Import as variant of this" imports the app item as a
new card linked to the compared card. In the API, that is `variant_of` on an
`import` resolution.

## Moving to another library

`POST /cards/move` `{"ids": [...], "library": <id>}`, or the `move_to_library`
MCP tool, moves cards with every variant below them. A moved card whose parent
stays behind is unlinked from it, and moved cards leave their collections.
Lorebook links and app bindings stay. If any selected item is unknown or
archived, nothing moves. `POST /cards/move/preview` returns the
same result without moving anything. Lorebooks move through `/lorebooks/move`.

## Hiding and purging

A large library, such as an imported archive, holds many copies of the same
card. **Hiding** archives the extras, so search leaves them out, without
deleting anything. Set it per library under Settings > Libraries, or with
`PATCH /libraries/{id}/settings`; a library without its own value follows the
default in `PATCH /settings`. To leave weak cards out, filter by score.

| Setting | Hides |
|---|---|
| `duplicates` | `exact`: cards with the same content. `near`: also cards whose text, or whose avatar under the same name, differs by at most `duplicate_distance` bits, 3 by default. A matching image alone doesn't count, since archives reuse art |
| `duplicate_keep` | Which copy of each duplicate group stays: the `best` grid score, the `oldest`, or the `newest` |
| `duplicate_decide` | For `near`: an `ai` instance offering `ai.decide`, such as a decision model, asked whether each near pair is the same card. A pair it says isn't stays apart; one it fails on counts by distance. Exact copies aren't asked |

"Hide now", `POST /libraries/{id}/hide`, or the `hide_cards` MCP tool runs a
job that archives what the settings pick and records why: `duplicate`. A later
run unarchives the cards it hid that are no longer duplicates. It never
touches a card you archived yourself, and archiving or unarchiving a card by
hand clears its reason. An unarchived duplicate can be hidden again by the
next run.

In a library with `duplicates` on, an import that adds or changes cards runs
the same job when it finishes, so cards from a second source don't sit beside
their copies. If the settings can't run, the import's log says why. Moving
cards runs it in the libraries they left and the one they joined.

**Purging** deletes for good. Select cards, such as archived ones scoring
under 30, and use Delete…, typing how many there are. Cards pushed to an app
are kept. See `POST /cards/purge`.

## Sync

The `lumiverse` plugin is a target for
[Lumiverse](https://github.com/prolix-oc/Lumiverse). Add one instance per
Lumiverse account; its
[README](https://github.com/inkandale/innkeeper-plugins/tree/main/lumiverse)
covers what it syncs:

```bash
ink instances create lumiverse Lumiverse \
  '{"url": "https://lumiverse.example.com", "username": "owner"}' \
  --secrets lumiverse.json    # {"password": "<password>"}
ink sync push <instance-id> <card-id>
```

The `sillytavern` plugin is a target for
[SillyTavern](https://github.com/SillyTavern/SillyTavern).
SillyTavern accepts only localhost by default, so first add Innkeeper's
address to its whitelist, or turn the whitelist off and use a login; the
[README](https://github.com/inkandale/innkeeper-plugins/tree/main/sillytavern)
covers that and what it syncs. Set `auth` to `none`, `basic`, or `account` to
match the server:

```bash
ink instances create sillytavern SillyTavern \
  '{"url": "https://sillytavern.example.com", "auth": "basic", "username": "owner"}' \
  --secrets sillytavern.json  # {"password": "<password>"}
```

A card pushed to a target app gets a binding. `ink sync bindings <card-id>`
(`GET /cards/{id}/bindings`) shows its status on each target as last checked;
`ink sync status <binding-id>` reads the app's copy now:

| Status | Meaning |
|---|---|
| `in_sync` | Nothing changed on either side since the last push |
| `ahead` | The card, its avatar, its override for this target, or its lorebooks changed since the last push |
| `remote_changed` | Someone changed the card in the app |
| `diverged` | Both of the above |
| `missing` | The app no longer has the card. The next push makes a new copy |
| `cant_check` | The app can't be read back, so changes made there are unknown |

When the app's copy changed, the binding lists the changed fields. To take the
app's changes, keep them only in that app, or keep the library's copy, review
the item: `ink sync compare` and `ink sync resolve`, or Review in the web app.
To throw the app's changes away, push: a push overwrites them.

Before a push, `ink sync preview <instance-id> <card-id>`
(`GET /cards/{id}/push-preview?instance_id=<id>`) reads the app's copy and
shows what the push would do. `ink sync push` shows it too, and sends its
`confirm` itself:

- `changes`: the fields it would change in the app, with the app's value as
  `before` and the pushed value as `after`
- `replaces`: whether it overwrites a copy the app has
- `dropped_fields`: fields with content the app doesn't store
- `new_copy`: whether it makes another copy, on an app that can't update cards
- `stale`: the app couldn't be read, so `changes` compare against the last push
- `confirm`: send it with the push, and the push is refused with `409` and
  the new preview if anything changed since

A query push has its own preview, `POST /pushes/preview`, which takes the
`instance_id`, `query`, and `kind` of `POST /pushes`. It counts the matching cards that are `new`, `ahead`,
`in_sync`, `cant_check`, or `drifted` on the target, as of their last check,
and returns a `confirm`. `ink sync push` with a query and the web's bulk push
both ask from it and send its `confirm`:

- If a new preview would differ, because the matching cards changed or a
  check or push changed one's binding, `POST /pushes` is refused with `409`
  and the new preview.
- Each card's push reads the app's copy first. If it changed since the
  preview, that push fails and the app keeps its copy.
- Without `confirm`, a card's push replaces the app's copy only if it hasn't
  changed since the last push or resolve, so drifted copies fail instead of
  being overwritten.

An app that can't be read back can't be checked, so its cards are pushed as
counted.

### Binding what the app already has

To link a card to a character the app already has, instead of pushing a new
copy, list the app's characters and bind one:

```bash
ink call GET  /instances/<instance-id>/cards
ink call POST /cards/<id>/bind '{"instance_id": <instance-id>, "remote_id": "<remote-id>"}'
```

Binding changes nothing in the app or the library. The app's copy is
accepted as it is, so the binding is `in_sync` if it matches the library and
`ahead` if it doesn't; the next push, with its preview, sends the library's
card. An item already bound shows its `binding_id`, and binding it
again is refused with `409`, as is a bind whose app copy or card changed
while it ran. In the push history, a bind shows as `"kind": "bind"`.

For lorebooks in an app that keeps them separately, use
`/instances/<instance-id>/lorebooks` and `/lorebooks/<id>/bind`. Binding
a card leaves the app's linked world books for sync: none are imported,
bound, or changed. A bound card is `ahead` when the app links it to lorebooks
other than the library's bound ones, or when the plugin can't tell which it
links. The next push links the library's.

### Syncing an app

A sync reads the app, links the characters and world books that match library
items exactly, imports the ones the library doesn't have, and leaves the rest
for you to review:

```bash
ink sync run                           # every app, one at a time
ink sync run <instance-id>             # alias: ink sync reconcile
ink sync run <instance-id> --dry-run   # report only; link and import nothing
ink sync refresh                       # check every app; change nothing
```

Each app syncs with one library, library 1 unless you pick another under
"Syncs with" on its settings page or with `sync_library` in
`PATCH /instances/{id}`. A sync matches the app's items only against that
library's cards and lorebooks, and imports new ones into it, so an archive
library is never searched. Cards bound to the app from other libraries still
push and report drift.

A sync of every app prints a line per app with its totals and the job to
pass to `ink sync apply`.

With autosync on, each sync pushes the library's copy to that app when the
card is ahead and the app's copy hasn't changed, so it never overwrites an
edit made in the app. Turning it on or off needs a destructive token:
`ink sync autosync <binding-id> on|off`.

It prints one row per app item, with each linked world book under its
character, and ends with `<n> applied, <m> left to review`:

| Verdict | Meaning |
|---|---|
| `bound` | Already bound |
| `exact` | A library card has the same content |
| `likely` | A library card has the same name, or similar text or avatar |
| `new` | Nothing in the library matches |
| `error` | The app item couldn't be read |

`linked (auto)` and `imported (auto)` are items this run linked or imported. A
character is imported only when each world book it links is exact, new, or
already linked and unchanged; otherwise it stays for review. Each remaining
row has a suggested action: `bind` to a card, `import` as a new card, or
`skip`. Pass `'{"match": {"name": false}}'` to stop matching by name alone,
and `--json` for the whole snapshot.

The changes queue lists every card an app needs you for, built from each
app's latest sync and the binding statuses, with app items no card matches
first. Each app's state is `differs`, `both_changed`, `remote_changed`,
`ahead`, `missing`, `error`, or `new`. `remote_changed` is a card edited only
in the app, which the next sync brings in. List it with
`ink sync changes [--app <instance-id>] [--state <state>]`. The CLI and web
print states from the library's side: `ahead` is "not pushed",
`remote_changed` "changed in app", and `diverged` and `both_changed` "changed
in both".

Compare an item with a card before binding them. The output lists each
hunk, a part of a field, that differs, with changed words marked
`[-library-]` and `{+app+}`:

```bash
ink sync compare <instance-id> <remote-id> <card-id>
```

Then resolve it, choosing per hunk which side to keep: `library`, `app`,
`keep_in_app` (the library keeps its text and this app's override keeps the
app's), or `both` (lists only: the library's entries, then the app's). Hunks
you don't name take `--default`, or `library`:

```bash
ink sync resolve <instance-id> <remote-id> bind <card-id> first_mes#0=app --default library
ink sync resolve <instance-id> <remote-id> skip
```

A bind accepts the app's copy as it is, so a card bound to a copy that differs
is `ahead`, and a plain compare keeps the library's side of each difference
without asking. Add `--two-way` to both `compare` and `resolve` to choose a
side for every difference; the MCP tools take `two_way` and `base: "none"`.

Resolving changes the library and the binding, never the app. Keeping the
library's side of a hunk leaves the binding `ahead`; push to send the result.
If the app's copy, the card, or the binding changed since the comparison,
`resolve` is refused with `409`; compare again.

To take every suggestion from a sync, or only some verdicts or items,
as one bulk job:

```bash
ink sync apply <sync-job-id> --verdict exact
ink sync apply <sync-job-id> <remote-id>   # one new item, imported
```

A suggested bind keeps the library's side of every hunk. An item with no
binding or card has nothing to compare with, so import it this way.

For world books in an app that keeps them separately, `sync compare` and
`sync resolve` cover characters only; use `ink call` with
`/instances/<instance-id>/lorebooks/<remote-id>/compare?lorebook_id=<id>`
and `.../resolve`. The MCP tools `start_job`, `compare_item`,
`resolve_item`, and `resolve_items` cover both.

### Lorebooks in apps that keep them separately

Some apps, such as Lumiverse, keep lorebooks apart from cards. Lorebooks then
sync on their own, with their own bindings, statuses, and checks:

```bash
ink call GET  '/lorebooks/<id>/push-preview?instance_id=<instance-id>'
ink call POST /lorebooks/<id>/push '{"instance_id": <instance-id>}'
ink call GET  /lorebooks/<id>/bindings
```

A card push creates the card's lorebooks that the app doesn't have yet, then
links the card to them. It never updates a lorebook the app already has, so
every lorebook change goes through the lorebook's own preview. The card
preview lists the lorebooks it would create in `create_lorebooks`. A card is
`ahead` when its linked lorebooks change, or when one of them is pushed again
under a new ID.

If a plugin exits or times out while creating a card in an app, the push job
fails rather than retrying, because the app may already have the card. Check
the app before pushing again.
