# Innkeeper plugin protocol

Protocol version 1. The design behind it is in
[the core design spec](superpowers/specs/2026-10-03-innkeeper-core-design.md).

## Transport

- The core starts the manifest's `command` with the plugin folder as its
  working directory.
- The plugin's environment holds only `PATH`, `HOME`, `LANG`, `TZ`, and
  `TMPDIR`. Everything it needs from the core arrives in `initialize`.
- Messages are JSON-RPC 2.0, one JSON value per line, on stdin and stdout.
  Write nothing else to stdout: invalid JSON there gets the plugin killed.
- Write logs to stderr. The core logs each line under the plugin's name.
- Both sides send requests. Answer every request with the same `id`, which
  may be a number or a string.
- The core may send several requests without waiting for replies, including
  while the plugin is waiting on its own call to the core. Read stdin
  continuously and match responses to your requests by `id`.
- The core handles requests concurrently and notifications in the order they
  arrive, so a plugin can rely on the order of what it sends.

## Manifest

Each plugin folder has a `plugin.toml`:

| Key | Required | Meaning |
|---|---|---|
| `name` | yes | Lowercase words of letters and digits joined by single hyphens, starting with a letter; at most 41 characters |
| `version` | yes | The plugin's own version |
| `protocol` | yes | Protocol version; must be `1` |
| `command` | yes, except for themes | Program and arguments. Use `./name` for a program in the plugin folder: a relative path containing `/` is inside the folder and can't leave it, an absolute path is used as-is, and a bare name is found on `PATH` |
| `capabilities` | yes | Any of `import`, `search`, `push`, `pull`, `action`, `enrich`, `codec`, `auth`, `mcp`, `catalog`, `sites`, or `theme`. A theme lists no other capability |
| `secrets` | no | Names of secrets the owner enters for each instance, in lowercase `snake_case` |
| `config` | no | JSON Schema for instance settings; the UI renders a form from it |
| `enrichments` | no | Keys the plugin writes, in lowercase `snake_case`. Each has a `label` for the UI and a `type`: `text`, `score` (needs `range = [low, high]`), or `list` |
| `formats` | only for `codec` | What `codec.encode` writes, each listed once: lowercase words of letters and digits joined by single hyphens, such as `png`. A codec plugin must list at least one. `json` is taken: the core exports CCv3 JSON itself |
| `inputs` | no; only for `import` | What the web UI may send to `import.run`, each listed once: `file` sends params `blob` and `name`, `url` sends `url`. Leave it out and the UI's upload and page URL boxes don't use the plugin |
| `source_kinds` | no; only for `pull` without `push` | The site source kinds the plugin follows, such as `chub`, each listed once, in the same form as a source's `kind`. The core binds each source of these kinds to the plugin's instance and checks it for updates; see [Following site sources](#following-site-sources) |
| `stylesheet` | only for `theme` | The theme's CSS file: a regular file inside the plugin folder, named `*.css`. A symlink that leaves the folder is rejected |
| `icon` | no | The app's logo, shown wherever a card is linked to one of the plugin's instances: a regular file inside the plugin folder, named `*.svg`, `*.png`, or `*.webp`. A symlink that leaves the folder is rejected. The core serves it at `/api/plugins/<name>/icon` without login. Without an icon, the UI shows the plugin name's first letter |
| `database` | no | `true` gives the plugin its own PostgreSQL schema, `plugin_<name>` with `-` as `_`, and a login role limited to it |

Unknown keys are errors.

The owner can roll a plugin back to its previous version. A plugin that
changes its own tables must keep working with what its next version wrote,
or a rollback strands its data.

### Themes

A theme plugin is a stylesheet with no process. Its manifest lists only the
`theme` capability, sets `stylesheet`, and has no `command`, `database`,
`secrets`, `config`, or `enrichments`. A theme gets no instance.

The owner selects a theme with `PATCH /api/settings {"theme": "<name>"}`; an
empty name selects the default. The core serves the selected theme at
`/api/theme.css` and any installed theme at `/api/themes/<name>.css`, both
without login. The UI loads the selected theme after its own CSS, so the theme
can override any custom property the UI defines on `:root` and any page's
`grid-template-areas`.

Themes can't run script: the UI's CSP is `script-src 'self'`. Their CSS still
runs on the UI's origin, so install only themes you trust.

## Lifecycle

1. **`initialize`**: the core's first request. Answer within 10 seconds.

   | Param | Meaning |
   |---|---|
   | `protocol` | The core's protocol version |
   | `instance_id` | The configured instance this process serves |
   | `config` | Instance settings |
   | `secrets` | Decrypted secrets, by name |
   | `blob_stores` | Blob store paths, by store name |
   | `blob_tmp` | Folder for new blob files |
   | `database_url` | PostgreSQL URL for the plugin's role, whose `search_path` is its schema. Empty unless the manifest sets `database = true` |

   Reply with `protocol`, `name`, `version`, and `capabilities`. These must
   match the manifest, or the core stops the plugin.

   Don't contact external services during `initialize`. Report bad
   settings on the first call that needs them.

2. **Calls**: the core sends capability methods as it needs them.

3. **`shutdown`**: reply, then exit when stdin closes. The core kills a
   plugin that is still running 5 seconds after `shutdown`.

The plugin runs in its own process group and receives no signals. Exit when
stdin closes, which also happens when the core itself exits. When the plugin
exits, closes stdout, or is stopped, the core kills its whole process group,
including any child processes.

The core starts a plugin on first use, stops it when idle, and starts it
again after a crash. The API and the worker may each run the same instance,
so keep anything that must persist in the plugin's schema or the blob store,
not in memory.

## Errors

Use standard JSON-RPC errors. Answer unknown methods with `-32601`.

`data` may be any JSON value. When it is an object, set `data.retryable` to
`true` if the same call might succeed later, such as after a timeout or a
rate limit:

```json
{"jsonrpc": "2.0", "id": 7, "error": {"code": -32000, "message": "rate limited", "data": {"retryable": true}}}
```

The core re-queues jobs that fail with a retryable error and fails the rest.

## Capability methods

Each capability's methods are added here as the core implements them. The
planned set is in the design spec's "Plugin protocol" section.

### `import`

The core sends `import.run` and waits for the reply. Until it replies, the
plugin streams what it finds as notifications.

| Param | Meaning |
|---|---|
| `run` | Token for this call. Put it in every notification you send for it |
| `params` | The import's settings, defined by the plugin, such as a folder path |
| `checkpoint` | `null` on the first run. After an interruption, the last checkpoint you sent |

Reply with `{}` when the import is done, or with an error. A plugin crash or
a retryable error re-runs the import later from the last checkpoint.

Notifications, each with `run`:

| Method | Params | Meaning |
|---|---|---|
| `emit.card` | `item`, `card`, `avatar` | One card. `item` is your stable ID for the source, such as a file path. `card` is a CCv3 card (`spec` must be `chara_card_v3`). `avatar` is optional: a file you wrote to `blob_tmp` |
| `emit.lorebook` | `item`, `lorebook` | One standalone lorebook, a CCv3 `character_book` |
| `progress` | `done`, `total`, `message` | How far you've got. `total` and `message` are optional |
| `checkpoint` | `checkpoint` | Any JSON value that tells you where to resume |

```json
{"jsonrpc": "2.0", "method": "emit.card", "params": {"run": "12.1", "item": "brakka.png", "card": {"spec": "chara_card_v3", "spec_version": "3.0", "data": {"name": "Brakka"}}, "avatar": "/blobs/tmp/brakka.png"}}
{"jsonrpc": "2.0", "method": "checkpoint", "params": {"run": "12.1", "checkpoint": {"next": "carrow.png"}}}
```

- Send a checkpoint after each item, or every few items. The core saves it
  only after storing everything you sent before it.
- After a resume you may send items again. The core skips an item whose
  `item` and content it has imported before, so importing the same source
  twice adds nothing.
- An embedded `character_book` becomes a separate lorebook linked to the card.
- A card the core can't read counts as failed. The import goes on.
- The core moves each `avatar` file into its blob store, so don't reuse the
  path.

### `auth`

A login provider verifies who is logging in. The core decides whether that
person is the owner: each auth instance names its owner's `subject`.

**`auth.begin`** starts a login.

| Param | Meaning |
|---|---|
| `callback_url` | Where a redirect login sends the browser back to. Empty if the core has no public URL |

Reply with one of:

- `form`: a JSON Schema object of the fields to ask for, such as a password.
- `redirect`: a URL to send the browser to, such as an OAuth provider's.

Add `state` to have it passed back to `auth.complete`, such as an OAuth
state value. The core keeps it in a cookie for 10 minutes. Keep nothing
about the login in memory: `auth.complete` may reach a different process.
The browser can read and change that cookie, so `state` must hold no secrets,
and a redirect provider must check that the redirect's state parameter matches
it.

**`auth.complete`** finishes it.

| Param | Meaning |
|---|---|
| `params` | The form's fields, or the query parameters of the redirect back to `callback_url` |
| `state` | The `state` from `auth.begin` |
| `callback_url` | As in `auth.begin` |

Reply with `subject`, a stable ID for this person, and optionally `name`.
Reply with an error if the login failed. The core answers every failure the
same way and waits a second, so don't add your own delay.

```json
{"jsonrpc": "2.0", "id": 3, "method": "auth.complete", "params": {"params": {"password": "…"}, "state": "", "callback_url": ""}}
{"jsonrpc": "2.0", "id": 3, "result": {"subject": "owner"}}
```

Two optional methods let the owner claim a new server with this provider.
Both run before any instance exists, with an empty config and no secrets. A
plugin without them returns -32601 and can't be used to claim.
`innkeeper plugin check` accepts either a form from `auth.setup` or -32601.

**`auth.setup`** takes no params and replies with `form`, a JSON Schema
object as in `auth.begin`.

**`auth.setup.complete`** creates the owner's login.

| Param | Meaning |
|---|---|
| `params` | The setup form's fields |

| Field | Meaning |
|---|---|
| `secrets` | The new instance's secrets, such as `password_hash` |
| `subject` | The `subject` `auth.complete` will report for the owner |

Reply with an error to reject the form. The core shows the owner its
message, so say what is wrong with the input.

```json
{"jsonrpc": "2.0", "id": 7, "method": "auth.setup.complete", "params": {"params": {"password": "…", "confirm": "…"}}}
{"jsonrpc": "2.0", "id": 7, "result": {"secrets": {"password_hash": "$2a$12$…"}, "subject": "owner"}}
```

### `push` and `pull`

A target plugin sends cards to an app (`push`) and reads them back (`pull`).
Cards cross as CCv3 in both directions; translate to and from the app's own
format. Put app data that has no CCv3 field under `data.extensions.<plugin>`.

**`target.describe`** takes no params and says what the app stores. The core
calls it before every push and check, so the answer may depend on the
instance's config. Don't contact the app to answer it.

| Field | Meaning |
|---|---|
| `fields` | The CCv3 `data` keys the app keeps, such as `name`, `description`, `first_mes`. List `character_book` if the app keeps embedded lorebooks |
| `pull` | `true` if `target.get` works. Needs the `pull` capability |
| `update` | `true` if a `target.put` with a `remote_id` changes that card. `false` for an app where every put makes a new copy |
| `delete` | Reserved. The core doesn't delete cards in apps yet |
| `lorebooks` | `embedded`: linked lorebooks arrive as `data.character_book`. `standalone`: the app keeps lorebooks on their own, sent with `target.put_lorebook`. `none`: the app has no lorebooks |
| `lorebook_fields` | For `standalone`: the CCv3 `character_book` keys the app keeps, such as `name`, `description`, `entries` |

The core compares only `fields` when it looks for changes made in the app, and
warns the owner before a push that would drop content in other fields.

**`target.put`** creates or updates one card.

| Param | Meaning |
|---|---|
| `card` | The CCv3 card, with this instance's override applied |
| `remote_id` | Your ID for the app's copy. Absent for a new card, or when `update` is `false` |
| `avatar` | Absolute path to the avatar image in a blob store, or absent. Read it; don't move or change it |
| `key` | Present for a new card. See below |
| `lorebook_remote_ids` | For `standalone` lorebooks: the remote IDs of the card's lorebooks, sorted, or absent when it has none. Link the card to exactly these, replacing earlier links |

Reply with `remote_id`. If the app no longer has `remote_id`, reply with error
`-32004`; the core then sends the card again as a new one.

The core sends the same `key` again when it retries a create, such as after
it was killed before recording your reply. Make creates idempotent: remember
which card each `key` created, and if a later put has the same `key` and the
app still has that card, reply with its `remote_id` without creating another.
Keep the key with the card in the app where you can, such as in a note or an
extension field, so it survives your plugin restarting.

**`target.get`** reads one card back.

| Param | Meaning |
|---|---|
| `remote_id` | The ID from `target.put` |
| `avatar` | `true` when the core wants the card and wants its image too. Checks don't set it |

| Reply field | Meaning |
|---|---|
| `card` | The app's copy as a CCv3 card |
| `avatar` | Only when asked: the card's image, as a file you wrote to `blob_tmp`, or absent if it has none |
| `lorebook_remote_ids` | For `standalone` lorebooks: the remote IDs of the lorebooks the app links to the card, or `[]`. Omit it if you can't tell |

If the app no longer has the card, reply with error `-32004`.

```json
{"jsonrpc": "2.0", "id": 4, "method": "target.put", "params": {"card": {"spec": "chara_card_v3", "spec_version": "3.0", "data": {"name": "Brakka"}}, "key": "3:12:57"}}
{"jsonrpc": "2.0", "id": 4, "result": {"remote_id": "42"}}
```

**`target.list`** and **`target.list_lorebooks`** take no params and say what
the app already has, so the owner can link a library item to one instead of
pushing a copy. Both need the `pull` capability. Page through the app
yourself and reply with every item:

```json
{"items": [{"remote_id": "42", "name": "Brakka"}]}
```

| Field | Meaning |
|---|---|
| `remote_id` | The ID `target.get` takes. Not empty |
| `name` | The name to show the owner |

**`target.put_lorebook`** and **`target.get_lorebook`** work like `target.put`
and `target.get`, for a target whose `lorebooks` is `standalone`. The core
pushes a card's lorebooks before the card, so `lorebook_remote_ids` name
lorebooks you already have. `target.get_lorebook` needs the `pull` capability.

| Param of `target.put_lorebook` | Meaning |
|---|---|
| `lorebook` | The CCv3 `character_book` object, with this instance's override applied |
| `remote_id` | Your ID for the app's copy. Absent for a new lorebook |
| `key` | Present for a new lorebook. Make creates idempotent, as for cards |

Reply with `remote_id`. `target.get_lorebook` takes `remote_id` and replies
with `lorebook`, the app's copy as a `character_book` object. Both reply with
error `-32004` if the app no longer has `remote_id`.

#### Following site sources

A plugin with `pull`, no `push`, and `source_kinds` follows site sources. The
core binds each live source of those kinds to the plugin's oldest working
instance. The `remote_id` is `kind:ref`, such as `chub:alice/vyr`. The core
checks each source on a schedule and offers the author's changes in the
changes queue.

| Method | What to return |
|---|---|
| `target.describe` | `pull` true, `update` false. Leave `character_book` out of `fields`; the core doesn't compare embedded books. Set `lorebooks` to `standalone` to follow lorebook sources |
| `target.get`, `target.get_lorebook` | The site's current copy. A card check sets `avatar`, so return the image when asked |
| `target.list`, `target.list_lorebooks` | `{"items": []}`. Instance checks and `innkeeper plugin check` call them |
| `target.put`, `target.put_lorebook` | Don't implement. The core never pushes to a pull-only plugin |

Errors and stability:

- Return `-32004` when the page is gone, private, or hides its definition.
  The core shows "no longer on the site".
- Make a timeout, a 5xx, or rate limiting retryable. The core lists a source
  with a failing check only after 3 failures in a row.
- Return identical `data` for the described fields on every fetch of an
  unchanged page, or every check reports an update. Drop volatile values,
  such as view counts.
- The core compares avatars by their pixels, so re-encoding one isn't an
  update.
- The core checks sources one at a time. Space out requests to one site
  yourself.

### `codec`

A codec reads and writes card files in the formats its manifest lists.

**`codec.decode`** reads one card file.

| Param | Meaning |
|---|---|
| `path` | Absolute path of the file. Read it; don't move or change it |

Reply with `card`, a CCv3 card, and optionally `avatar`, the card's image as
a file you wrote to `blob_tmp`. Reply with error `-32602` if the file isn't a
card you can read.

**`codec.encode`** writes one card file.

| Param | Meaning |
|---|---|
| `card` | The CCv3 card, with linked lorebooks embedded as `data.character_book` |
| `format` | One of your manifest's `formats` |
| `avatar` | Absolute path to the avatar image in a blob store, or absent. Read it; don't move or change it |

Reply with `path`, the file you wrote directly inside `blob_tmp`, and
`extension`, its file name extension without the dot: 1 to 10 lowercase
letters and digits. The core moves the file, so don't reuse the path.

```json
{"jsonrpc": "2.0", "id": 5, "method": "codec.encode", "params": {"card": {"spec": "chara_card_v3", "spec_version": "3.0", "data": {"name": "Brakka"}}, "format": "png"}}
{"jsonrpc": "2.0", "id": 5, "result": {"path": "/blobs/tmp/card-1234.png", "extension": "png"}}
```

### `catalog`

A catalog serves a collection of cards and lorebooks the owner searches and
adopts from, without putting them in the library. An **ingested** catalog
copies its source into its own schema (`database = true`) and answers
`catalog.ingest`. A **pass-through** catalog forwards each call to a remote
site and stores nothing.

Every item has an `id` you assign that never changes, a `kind` in lowercase
`snake_case`, and one or more versions, each named by a string you choose.
The core adopts kinds `card` and `lorebook`.
Reply with error `-32004` for an ID or version you don't have. Check params
before reaching your source, so a bad request gets `-32602` even when the
source is down.

**`catalog.describe`** takes no params. Don't contact your source to answer.

| Field | Meaning |
|---|---|
| `kinds` | The kinds you hold, such as `["card", "lorebook"]` |
| `filters` | The filters `catalog.search` accepts. Each has `name` (lowercase `snake_case`), `label`, `type`, and, for `enum` only, `options`: a list of `{value, label}` |
| `sorts` | The orders `catalog.search` accepts, each `{value, label}`. The first is the default |
| `ingest` | `true` if you answer `catalog.ingest` |
| `similar` | `true` if you answer `catalog.similar` |

| Filter `type` | Value in a search |
|---|---|
| `enum` | One of the option values, as a string |
| `text` | A non-empty string |
| `date_range` | `{"from": "2024-01-01", "to": "2024-12-31"}`, both inclusive, either optional |
| `number_range` | `{"min": 1, "max": 5}`, both inclusive, either optional |

**`catalog.search`** returns one page of items.

| Param | Meaning |
|---|---|
| `q` | Search text, or empty for everything |
| `kind` | One of your `kinds`, or empty for all |
| `filters` | Filter values by name |
| `sort` | One of your `sorts`, or empty for the first |
| `page` | `size`, 1 to 100 or 0 for your default, and `cursor`, the `next` of the previous page or empty for the first |

Reply with `-32602` for a kind, sort, or filter you didn't declare, a filter
value of the wrong shape, or a page size out of range. Otherwise reply with `items` and `next`, the
cursor for the following page, or empty on the last. Each item has:

| Field | Meaning |
|---|---|
| `id`, `kind`, `name` | Required |
| `author`, `tags`, `summary` | Optional |
| `latest_version` | The newest version's name |
| `hashes` | The latest version's `content_hash` and, if you have them, `text_simhash` and `image_phash`. Compute them with the Go package `github.com/davidgibbons/innkeeper/cardhash`, over `cardhash.CardData` of a card (its `data` without `character_book` or the SillyTavern `fav` and `world` extensions) or the whole lorebook, so they match the library's. `text_simhash` and `image_phash` are signed 64-bit integers |
| `thumbnail` | Optional. A base64 JPEG at most 160 pixels wide and 32 KiB |

**`catalog.get`** returns one version of an item.

| Param | Meaning |
|---|---|
| `id` | The item |
| `version` | A version name, or empty for the latest |
| `avatar` | `true` to write the card's image to `blob_tmp`. Adopt and `innkeeper plugin check` set it |

Reply with `item` (as in search), `version`, `versions` (newest first, each
`{version, created}`, with `created` in RFC 3339 if you know it), `data` (a
CCv3 card for a card, a CCv3 `character_book` for a lorebook), and `avatar`,
the file you wrote to `blob_tmp`, or empty. The core moves the file, so don't
reuse the path.

**`catalog.similar`** finds near-duplicates of a library item.

| Param | Meaning |
|---|---|
| `kind` | The kind to search |
| `hashes` | The library item's hashes |
| `max_distance` | The most bits `text_simhash` or `image_phash` may differ by |
| `limit` | The most items to return, 1 to 100 |

Reply with `items` that share `content_hash` or fall within `max_distance`
on either other hash, closest first.

**`catalog.ingest`** copies your source into your schema. It takes `run` and
`checkpoint` and sends `progress` and `checkpoint` notifications, all as in
`import.run`. Reply with the counts `added`, `updated`, `removed`, and
`skipped`. Search keeps serving what you have while an ingest runs.

```json
{"jsonrpc": "2.0", "id": 8, "method": "catalog.search", "params": {"q": "dwarf", "kind": "card", "filters": {"site": "chub"}, "page": {"size": 1}}}
{"jsonrpc": "2.0", "id": 8, "result": {"items": [{"id": "chub:42", "kind": "card", "name": "Brakka", "latest_version": "3", "hashes": {"content_hash": "90b4…a1e0", "text_simhash": -6846279246937013872}}], "next": "1"}}
```

### `sites`

A `sites` plugin knows one or more sites cards are published on: how their
data names a page, and the shape of their page URLs. The core asks it which
sources a card or lorebook has after an import, while syncing an app, and when
the owner adds a source by URL. Both methods only parse: answer within 5
seconds, don't contact any service, and give the same answer for any instance
config.

A **source** in a reply:

| Field | Rule |
|---|---|
| `kind` | The site, matching `^[a-z][a-z0-9-]*$`. Never rename a kind you have sent: stored sources, search filters, and sync matching use it |
| `ref` | The site's ID for the page, not empty, at most 512 bytes |
| `external_id` | Optional, at most 128 bytes |
| `url` | The page, `https://`, at most 2048 bytes |

The core drops a source that breaks these rules and logs it.

**`sites.detect`** reads the sources an item's data names.

| Param | Meaning |
|---|---|
| `entity` | `card` or `lorebook` |
| `data` | A card's CCv3 `data` object, or a lorebook's `character_book` object |

Reply with `sources`, at most 32, or `[]`. Return only sources that fit the
entity, such as no lorebook page for a card. Reply `-32602` if `entity` is
unknown or `data` isn't an object.

**`sites.parse`** turns what the owner typed into a source.

| Param | Meaning |
|---|---|
| `entity` | `card` or `lorebook` |
| `url` | A page URL. Send either `url`, or `kind` and `ref` |
| `kind`, `ref` | A source the owner named directly |

Reply with `source`, the source with its canonical URL, or `null` when the URL
or kind isn't yours. Reply `-32602`, with a message the owner can read, when
the kind is yours and the ref isn't valid, or when params send both forms or
neither.

```json
{"jsonrpc": "2.0", "id": 9, "method": "sites.parse", "params": {"entity": "lorebook", "url": "https://www.chub.ai/lorebooks/u/orcs?x=1"}}
{"jsonrpc": "2.0", "id": 9, "result": {"source": {"kind": "chub", "ref": "u/orcs", "url": "https://chub.ai/lorebooks/u/orcs"}}}
```

With several `sites` plugins, the core asks the oldest working instance of
each, in order of that instance's ID. The first source with a given `kind` and
`ref` wins, and the first non-null `sites.parse` reply wins. A plugin that
errors or times out is skipped and logged; detection never fails an import or
a sync. While no `sites` instance works, imported items wait for detection
until one does.

At startup the core creates one instance of each built-in `sites` plugin whose
manifest has no `secrets` and no `required` keys in `config`. It does this once
per plugin, so an instance the owner deletes stays deleted.

### `mcp`

An `mcp` plugin adds tools to Innkeeper's MCP server. Each tool runs with the
plugin instance's config and secrets.

**`mcp.tools`** takes no params. Reply with `tools`, each with:

| Field | Meaning |
|---|---|
| `name` | 1 to 40 lowercase letters, digits, and underscores, unique within the plugin. Innkeeper shows it as `<instance>_<name>` |
| `description` | What the tool does, for the model choosing it |
| `input_schema` | A JSON Schema with `"type": "object"` for the arguments |
| `tier` | `read` if it changes nothing, `write` if what it changes can be undone, `destructive` otherwise. A missing or unknown tier counts as `destructive` |

The core caches the list for a minute, so keep it fixed for a given version
and config. Don't contact the app to answer.

**`mcp.call`** takes `name` and `arguments`, the object the model sent.
Reply with `content`, a list of `{"type": "text", "text": "…"}` items, and
`is_error: true` for a failure the model should see, such as bad
arguments. A JSON-RPC error also reaches the model as a tool error.

```json
{"jsonrpc": "2.0", "id": 6, "method": "mcp.call", "params": {"name": "list_untracked", "arguments": {}}}
{"jsonrpc": "2.0", "id": 6, "result": {"content": [{"type": "text", "text": "[\"Brakka\"]"}]}}
```

## Checking a plugin

```bash
innkeeper plugin check path/to/plugin
```

It loads the manifest, then checks that:

- the `initialize` reply matches the manifest
- an unknown method returns `-32601` within 5 seconds
- a `push` or `pull` plugin's `target.describe` reply is valid, and claims `pull` only
  if the manifest declares it
- a plugin whose `target.describe` claims `pull` answers `target.list` and
  `target.list_lorebooks` within 5 seconds, with a `remote_id` on every item
  it returns. A target checked without its app can only return an error, so
  an error other than `-32601` counts as an answer.
- a plugin that pulls without pushing answers `-32004` to `target.get` for
  `innkeeper-contract:none`, and to `target.get_lorebook` if it keeps standalone
  lorebooks
- a `codec` plugin encodes a sample card in each of its `formats` into
  `blob_tmp` with a valid extension, decodes it back, and returns an error
  when asked to decode a file that isn't a card
- an `mcp` plugin's `mcp.tools` reply has valid, unique names, valid input
  schemas, and comes within 5 seconds
- a `catalog` plugin's `catalog.describe` reply is valid; `catalog.search`
  pages one item at a time without repeats and refuses an undeclared filter
  with `-32602`; `catalog.get` returns `-32004` for an unknown ID, and for the
  first search result returns data that hashes to its `content_hash` and, if
  it returns an avatar, one inside `blob_tmp`; and, if it claims `similar`,
  `catalog.similar` finds that item by its own hashes. Without `INNKEEPER_PLUGIN_CONFIG`, a
  catalog may refuse search with an error other than `-32601`, and may then
  answer `catalog.get` and `catalog.similar` the same way; the checks that
  need an item pass
- a `sites` plugin's `sites.detect` finds no source in a plain card
  (`{"name": "Contract"}`) or a plain lorebook (`{"entries": []}`) and
  refuses `data` that isn't an object with `-32602`; its `sites.parse`
  returns `null` for `https://example.invalid/contract` and refuses params
  with neither `url` nor `kind` with `-32602`; and every source those replies
  hold is valid
- `shutdown` gets a reply within 5 seconds
- the plugin exits after `shutdown`, rather than having exited earlier

It exits non-zero if any check fails. For a plugin with `database = true`,
set `INNKEEPER_PLUGIN_DATABASE_URL` to send a test database as `database_url`.
Set `INNKEEPER_PLUGIN_CONFIG` to a JSON object to send it as the instance
config, such as a catalog's test data.

## Publishing a source

A source is an `index.toml` that the owner adds to Innkeeper by its
`https://` or `file://` URL. It lists each plugin version, with a gzipped
tarball per platform:

```toml
categories = ["Apps", "Import", "Login"]

[[plugin]]
name = "lumiverse"
version = "0.2.0"
protocol = 1
capabilities = ["push", "pull", "search"]
description = "Push and pull cards and lorebooks with a Lumiverse server."
category = "Apps"
recommended = true

[[plugin.artifact]]
os = "linux"
arch = "amd64"
url = "lumiverse-0.2.0-linux-amd64.tar.gz"
sha256 = "<hex sha256 of the tarball>"
```

- List every version you want owners to be able to install, not only the
  newest.
- `category` groups a plugin in the setup wizard. `categories` is the order
  the wizard shows them in; unlisted ones follow alphabetically.
- `recommended = true` marks a plugin the Plugins page suggests. Innkeeper
  shows the mark only for the `official` source.
- `os` and `arch` use Go's names, such as `linux` and `arm64`.
- `url` may be relative to the index. An `https://` index must point at
  `https://` tarballs, and a `file://` index at local files.
- The tarball holds `plugin.toml` and the files it needs, at the top level.
  It may contain only regular files and folders, no links. Its manifest's
  `name`, `version`, and `protocol` must match the index entry.
- Innkeeper refuses a tarball whose sha256 doesn't match the index. That
  catches a corrupted or swapped download, but whoever controls the index
  controls what installs.
