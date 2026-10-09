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
| `capabilities` | yes | Any of `import`, `search`, `push`, `pull`, `action`, `enrich`, `codec`, `auth`, `mcp`, or `theme`. A theme lists no other capability |
| `secrets` | no | Names of secrets the owner enters for each instance, in lowercase `snake_case` |
| `config` | no | JSON Schema for instance settings; the UI renders a form from it |
| `enrichments` | no | Keys the plugin writes, in lowercase `snake_case`. Each has a `label` for the UI and a `type`: `text`, `score` (needs `range = [low, high]`), or `list` |
| `formats` | only for `codec` | What `codec.encode` writes, each listed once: lowercase words of letters and digits joined by single hyphens, such as `png`. A codec plugin must list at least one. `json` is taken: the core exports CCv3 JSON itself |
| `stylesheet` | only for `theme` | The theme's CSS file: a regular file inside the plugin folder, named `*.css`. A symlink that leaves the folder is rejected |
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
| `avatar` | `true` when the core imports the card and wants its image too. Checks don't set it |

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
- a `push` plugin's `target.describe` reply is valid, and claims `pull` only
  if the manifest declares it
- a `push` plugin that claims `pull` answers `target.list` and
  `target.list_lorebooks` within 5 seconds, with a `remote_id` on every item
  it returns. The check sends an empty config, so an error other than
  `-32601` counts as an answer.
- a `codec` plugin encodes a sample card in each of its `formats` into
  `blob_tmp` with a valid extension, decodes it back, and returns an error
  when asked to decode a file that isn't a card
- an `mcp` plugin's `mcp.tools` reply has valid, unique names, valid input
  schemas, and comes within 5 seconds
- `shutdown` gets a reply within 5 seconds
- the plugin exits after `shutdown`, rather than having exited earlier

It exits non-zero if any check fails. For a plugin with `database = true`,
set `INNKEEPER_PLUGIN_DATABASE_URL` to send a test database as `database_url`.

## Publishing a source

A source is an `index.toml` that the owner adds to Innkeeper by its
`https://` or `file://` URL. It lists each plugin version, with a gzipped
tarball per platform:

```toml
[[plugin]]
name = "lumiverse"
version = "0.2.0"
protocol = 1
capabilities = ["push", "pull", "search"]
description = "Push and pull cards and lorebooks with a Lumiverse server."

[[plugin.artifact]]
os = "linux"
arch = "amd64"
url = "lumiverse-0.2.0-linux-amd64.tar.gz"
sha256 = "<hex sha256 of the tarball>"
```

- List every version you want owners to be able to install, not only the
  newest.
- `os` and `arch` use Go's names, such as `linux` and `arm64`.
- `url` may be relative to the index. An `https://` index must point at
  `https://` tarballs, and a `file://` index at local files.
- The tarball holds `plugin.toml` and the files it needs, at the top level.
  It may contain only regular files and folders, no links. Its manifest's
  `name`, `version`, and `protocol` must match the index entry.
- Innkeeper refuses a tarball whose sha256 doesn't match the index. That
  catches a corrupted or swapped download, but whoever controls the index
  controls what installs.
