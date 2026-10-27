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

## Conventions

New parts of the protocol follow these rules. Older names that break them
stay as they are, so existing plugins keep working; each is listed as an
exception.

### Manifest or RPC

Declare a fact in the [manifest](#manifest) when it is fixed for a plugin
version. Answer it over RPC only when it depends on the instance's config,
as `target.describe`, `action.list`, `mcp.tools`, and `initialize`'s
`services` do.

Exceptions:

- `pull` is both a capability and a field of `target.describe`. The core
  trusts the field only when the manifest declares the capability.
- `settings.defaults` is RPC, but the core sends it to a process started
  with an empty config.

### Method names

New methods are named `<capability>.<verb>`, or `<capability>.<verb>_<kind>`
for a second item kind, such as `target.put_lorebook`.

Exceptions:

- The `push` and `pull` capabilities share the `target.` prefix.
- `auth.setup.complete` has two dotted parts after the capability.
- `mcp.tools`, `settings.defaults`, `settings.options`, and the `ai.delta`
  notification name a noun, not a verb.
- `target.list_lorebooks` uses the plural kind.
- `library.call` belongs to `action`, and the plugin sends it to the core.
- `initialize`, `shutdown`, and the notifications `emit.card`,
  `emit.lorebook`, `progress`, `checkpoint`, `log`, and `cancel` aren't
  named after a capability.

### Identifiers

| Identifier | Pattern |
|---|---|
| Secret names | `^[a-z][a-z0-9_]*$` |
| Enrichment keys | `^[a-z][a-z0-9_]*$` |
| Record types | `^[a-z][a-z0-9_]*$` |
| Catalog kinds and filter names | `^[a-z][a-z0-9_]*$` |
| Settings action IDs | `^[a-z][a-z0-9_]*$` |
| Action IDs | `^[a-z0-9-]{1,40}$` |
| MCP tool names | `^[a-z0-9_]{1,40}$` |
| Skill names | `^[a-z0-9-]{1,64}$` |
| Source kinds | `^[a-z][a-z0-9-]*$` |
| Codec formats | `^[a-z0-9]+(-[a-z0-9]+)*$` |

### Shapes that differ by method

- **Instance IDs.** `initialize`'s `instance_id` is a string: the instance's
  ID in decimal, or another value for a process that serves no instance,
  such as the [`settings`](#settings) process. A service call's `instance`
  and a config field with `"format": "instance"` hold the ID as an integer.
- **`data`.** `catalog.get` returns a whole CCv3 card as `data`. In
  `action.run`, `enrich.run`, and `sites.detect`, a card's `data` is the
  CCv3 `data` object only. A lorebook's `data` is its `character_book`
  everywhere.
- **File inputs.** A `file` input sends `blob`, an uploaded blob's sha256,
  and `name` in [`import.run`](#import)'s `params`, but `path`, a file in
  `blob_tmp`, and `name` to [`records.import`](#records).

## Manifest

Each plugin folder has a `plugin.toml`:

| Key | Required | Meaning |
|---|---|---|
| `name` | yes | Lowercase words of letters and digits joined by single hyphens, starting with a letter; at most 41 characters |
| `version` | yes | The plugin's own version |
| `protocol` | yes | Protocol version; must be `1` |
| `command` | yes, except for themes | Program and arguments. Use `./name` for a program in the plugin folder: a relative path containing `/` is inside the folder and can't leave it, an absolute path is used as-is, and a bare name is found on `PATH` |
| `capabilities` | yes | Any of `import`, `push`, `pull`, `action`, `records`, `enrich`, `codec`, `auth`, `mcp`, `catalog`, `sites`, `ai`, `settings`, or `theme`. A theme lists no other capability |
| `secrets` | no | Names of secrets the owner enters for each instance, in lowercase `snake_case` |
| `config` | no | JSON Schema with `type = "object"` for instance settings; the UI renders a form from it. See [The config form](#the-config-form) |
| `enrichments` | for `enrich` | Keys the plugin writes, in lowercase `snake_case`; see [`enrich`](#enrich), and `emit.card`'s `values` under [`import`](#import). Each has a `label` for the UI and a `type`: `text`, `score` (needs `range = [low, high]`), or `list`. `grid = true` shows the key on the library grid, for at most one key. The grid key's `priority`, an integer, orders plugins: a tile shows the highest-priority score a card has, and the top three wear gold, silver, and bronze. Space priorities apart, such as by 100, so other plugins fit between. An `enrich` plugin must declare at least one |
| `enrich_on_import` | no; only for `enrich` | `true` makes the core score the cards each import creates or revises, with one run per enabled instance of the plugin, up to the plugin's card cap. Set it only when your calls cost the owner nothing, since the owner didn't ask for the run |
| `formats` | only for `codec` | What `codec.encode` writes, each listed once: lowercase words of letters and digits joined by single hyphens, such as `png`. A codec plugin must list at least one. `json` is taken: the core exports CCv3 JSON itself |
| `bulk` | no; only for `import` | `true` for imports of large sources, such as a site dump. The core stores the cards sent between two checkpoints, about 1,000, in one transaction with the second checkpoint, and doesn't queue them for source detection |
| `inputs` | no; only for `import` | What the web UI may send to `import.run`, each listed once: `file` sends params `blob` and `name`, `url` sends `url`, and `folder` sends no params, for an import of the folder the instance's config names. Leave it out and the UI's upload, page URL, and folder controls don't use the plugin |
| `source_kinds` | no; only for `pull` without `push` | The site source kinds the plugin follows, such as `chub`, each listed once, in the same form as a source's `kind`. The core binds each source of these kinds to the plugin's instance and checks it for updates; see [Following site sources](#following-site-sources) |
| `stylesheet` | only for `theme` | The theme's CSS file: a regular file inside the plugin folder, named `*.css`. A symlink that leaves the folder is rejected |
| `icon` | no | The app's logo, shown wherever a card is linked to one of the plugin's instances: a regular file inside the plugin folder, named `*.svg`, `*.png`, or `*.webp`. A symlink that leaves the folder is rejected. The core serves it at `/api/plugins/<name>/icon` without login. Without an icon, the UI shows the plugin name's first letter |
| `records` | only for `records` | The record types the plugin keeps; see [`records`](#records). A `records` plugin must declare at least one |
| `database` | no | `true` gives the plugin its own PostgreSQL schema, `plugin_<name>` with `-` as `_`, and a login role limited to it |
| `multiple_instances` | no | `true` lets the owner create more than one instance, such as one per app server. Without it, the plugin has at most one, and the core creates it when the plugin needs no secrets and no required config, except for an `auth` plugin, which always allows many. A theme can't set it |
| `title` | no | The name the UI shows, such as `SillyTavern`, and an unnamed instance's label. Without one, the UI shows `name` |
| `description` | no | One line saying what the plugin does, shown on the Plugins page |
| `category` | no | The group the Plugins page shows the plugin under, such as `Apps`. Without one, it shows under Other |
| `uses` | no | Service methods the plugin calls on other instances, each listed once, such as `["ai.chat"]`; see [`ai`](#ai) |
| `settings` | no; only for `settings` | Buttons on the instance form, under `[settings.actions]`, each keyed by an ID matching `^[a-z][a-z0-9_]*$` with a required `label`; see [`settings`](#settings) |

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

### The config form

The UI renders the instance form from `config`. Each top-level property that
is an object with `properties` gets its own tab, titled by its `title`; the
other properties, the secrets, and the [`settings`](#settings) buttons go on
**General**. Fields show in the order `plugin.toml` lists them.

| Schema | Renders as |
|---|---|
| `string`, `number`, `integer`, `boolean` | A text box, number box, or checkbox |
| `string` with `enum` | A drop-down |
| `string` with `oneOf`, each `{const = "<value>", title = "<label>"}` | A drop-down that shows each `title` and saves its `const` |
| `string` with `contentMediaType` | A multi-line box, whatever the media type |
| `string` with `"x-options": true` | A text box with **Load choices**, which asks the plugin for suggestions with `settings.options`. Needs the `settings` capability |
| `integer` with `"format": "instance"` and `"x-offers": "<method>"` | A drop-down of the working instances that offer that service, such as `ai.chat`. The value is the instance's ID |
| `array` whose `items` is a `string` with `enum` or `oneOf` | A checkbox per choice |
| `array` whose `items` is any other `string` | A multi-line box, one item per line |
| `object` with `properties` | A group of its properties |

Anything else, or a saved value of the wrong type, is edited as raw JSON.

`x-when` on a property shows it only while each named sibling, or that
sibling's default, has one of the listed values. It hides the field and
nothing else, so require the field with `if` and `then` as well:

```toml
[config.properties.username]
type = "string"
x-when = { auth = ["basic", "account"] }
```

`x-options-needs` on an `x-options` field names the siblings **Load
choices** needs first, each with an `x-when` condition under which it is
needed; `{}` means always. The button stays disabled, and says which fields
to fill in, until each needed sibling or its default has a value:

```toml
[config.properties.model]
type = "string"
x-options = true
x-options-needs = { provider = {}, base_url = { provider = ["openai-compatible"] } }
```

`"x-advanced": true` on a property moves it into a collapsed **Advanced**
section at the end of its group. The section starts open when one of its
fields is set.

`x-secrets` on `config` describes the secrets: a `title` and `description`
for each, and an `x-when` that works as above. Each key must be in `secrets`.
Without a `title`, the form labels a secret from its name, so `api_key` reads
"API key".

```toml
[config.x-secrets.password]
title = "Password"
x-when = { auth = ["basic", "account"] }
```

A field with a `default` shows it until the owner changes it, and then offers
**Reset**. A field left at its default, or typed back to it, is absent from
the saved config, so apply your defaults yourself. When a default changes
after the owner overrode it, the field says **Default updated**.

The core refuses to save config that doesn't match the schema, ignoring
`required`: an instance missing a required key is saved and shows **Needs
setup**. Config saved before a schema change is checked when it is next saved.

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
   match the manifest, or the core stops the plugin. An `ai` plugin also
   replies with `services`: the service methods this instance offers, such
   as `["ai.chat"]`. The core stops a plugin that lists a service outside its
   capabilities.

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

| Code | Go constant | Meaning |
|---|---|---|
| `-32700` | `CodeParseError` | The message isn't valid JSON |
| `-32600` | `CodeInvalidRequest` | The request isn't valid, such as a call before `initialize` |
| `-32601` | `CodeMethodNotFound` | The receiver doesn't implement the method. Answer every unknown method with it |
| `-32602` | `CodeInvalidParams` | The params are malformed or name something the method can't accept |
| `-32603` | `CodeInternalError` | The receiver failed unexpectedly, such as a handler panic |
| `-32000` | `CodeFailed` | The call failed for a reason the message tells the owner, such as the app refusing a card |
| `-32003` | `CodeForbidden` | The core refuses a call the plugin isn't allowed to make |
| `-32004` | `CodeRemoteNotFound` | The plugin doesn't have the ID: a `remote_id`, a catalog item or version, or a record |
| `-32010` | `CodeAIRejected` | A model provider refuses the request as invalid, such as tools for a model without tool support |

`data` may be any JSON value. When it is an object, set `data.retryable` to
`true` if the same call might succeed later, such as after a timeout or a
rate limit:

```json
{"jsonrpc": "2.0", "id": 7, "error": {"code": -32000, "message": "rate limited", "data": {"retryable": true}}}
```

The core re-queues jobs that fail with a retryable error and fails the rest.

## Writing a plugin in Go

The Go package `github.com/inkandale/innkeeper/protocol` has a type for
every message in this document. `protocol.Serve` runs the plugin:

```go
func main() {
	protocol.Serve(protocol.Plugin[instance]{
		Init: func(ctx context.Context, p protocol.InitializeParams) (*instance, error) {
			return &instance{blobTmp: p.BlobTmp}, nil
		},
		Methods: map[string]func(*instance, context.Context, json.RawMessage) (any, error){
			protocol.MethodCodecDecode: (*instance).decode,
		},
	})
}
```

`Serve`:

- answers `initialize` with the `name`, `version`, and `capabilities` in
  `./plugin.toml`, so the version lives only in the manifest
- passes each later call to its entry in `Methods`, with the state `Init`
  built for this instance
- answers a call before `initialize` with `-32600`, an unknown method with
  `-32601`, and `shutdown` with an empty reply unless `Methods` has it
- exits with status 1 when the connection fails

An `ai` plugin sets `Services` to list each instance's service methods. To
notify or call the core from a method, use `protocol.Peer(ctx)`. A test can
run the plugin over pipes with `protocol.NewConn` and
`Plugin.Handler(manifest)`.

Build errors with `protocol.Fail(msg, retryable)` for `-32000`,
`protocol.InvalidParams(err)` for `-32602`, and `protocol.NewError` for any
other code.

## Cancelling a request

When either side stops waiting for a reply, such as when the owner stops an
action run or cancels an import, it sends a `cancel` notification with the
request's `id`:

```json
{"jsonrpc": "2.0", "method": "cancel", "params": {"id": 7}}
```

Stop the work for that request, including calls to paid services. A reply is
optional; the sender ignores it. Ignore a `cancel` for an `id` you aren't
answering, since your reply may already be on its way. Plugins built on the
Go `protocol.Conn` get this for free: the handler's context is cancelled.

## Logging a run

During any call that carries a `run` token, such as `import.run`,
`action.run`, or `enrich.run`, send a `log` notification to add a line to the
log of the job the run belongs to. The owner reads it on the job's page.

| Param | Meaning |
|---|---|
| `run` | The run's token |
| `level` | `info`, `warn`, or `error`. Optional; the default is `info` |
| `message` | One line, up to 4 KiB; longer lines are cut |

```json
{"jsonrpc": "2.0", "method": "log", "params": {"run": "Q5MBGXK2YHRWZ7PJ3NCV4D6TLA", "level": "warn", "message": "chub:812 version 3 skipped: no date"}}
```

A job keeps its last 1,000 lines. Write to stderr for anything that isn't
about one run: the core logs it per plugin process, not per job.

## Capability methods

Each capability's methods are added here as the core implements them. The
planned set is in the design spec's "Plugin protocol" section.

### `import`

The core sends `import.run` and waits for the reply. Until it replies, the
plugin streams what it finds as notifications.

| Param | Meaning |
|---|---|
| `run` | Token for this call, an opaque string. Put it in every notification you send for it |
| `params` | The import's settings, defined by the plugin, such as a folder path |
| `checkpoint` | `null` on the first run. After an interruption, the last checkpoint you sent |

Reply with `{}` when the import is done, or with an error. A plugin crash or
a retryable error re-runs the import later from the last checkpoint.

Notifications, each with `run`:

| Method | Params | Meaning |
|---|---|---|
| `emit.card` | `item`, `card`, `avatar`, `version`, `created`, `values` | One card. `item` is your stable ID for the source, such as a file path. `card` is a CCv3 card (`spec` must be `chara_card_v3`). The rest are optional. `avatar` is a file you wrote to `blob_tmp`. `version` is the source's label for this version, stored as the revision's message. `created` is the source's date for it, an RFC 3339 time, stored as the revision's date. `values` maps keys your manifest's `enrichments` declares to values, stored as enrichments from your instance |
| `emit.lorebook` | `item`, `lorebook` | One standalone lorebook, a CCv3 `character_book` |
| `progress` | `done`, `total`, `message` | How far you've got. `total` and `message` are optional |
| `checkpoint` | `checkpoint` | Any JSON value that tells you where to resume |

```json
{"jsonrpc": "2.0", "method": "emit.card", "params": {"run": "Q5MBGXK2YHRWZ7PJ3NCV4D6TLA", "item": "brakka.png", "card": {"spec": "chara_card_v3", "spec_version": "3.0", "data": {"name": "Brakka"}}, "avatar": "/blobs/tmp/brakka.png"}}
{"jsonrpc": "2.0", "method": "checkpoint", "params": {"run": "Q5MBGXK2YHRWZ7PJ3NCV4D6TLA", "checkpoint": {"next": "carrow.png"}}}
```

- Send a checkpoint after each item, or every few items. The core saves it
  only after storing everything you sent before it.
- One `item` is one card in the import's library, so send each version of
  an item in order, oldest first. A version the card already has adds
  nothing, so importing the same source twice, or items sent again after a
  resume, adds nothing. A new version becomes the card's new revision, unless
  the owner edited the card since the last import: then it waits in the
  changes queue as an update to review, merged against the last imported
  version.
- A version whose `created` is older than the last one stored or waiting for
  that item is skipped, so a version sent out of order can't replace a newer one.
- A value whose key the manifest doesn't declare, or that doesn't fit its
  type or range, is dropped with a warning in the job's progress. The card
  is still stored. Values are stored only when the card changed.
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
| `delete` | `true` if `target.delete` works, and `target.delete_lorebook` for `standalone` lorebooks |
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
pushing a copy. Both need the `pull` capability, and the core sends them only
to a plugin that also pushes. Page through the app yourself and reply with
every item:

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

**`target.delete`** and **`target.delete_lorebook`** remove the app's copy
when the owner archives a card or lorebook and asks to remove it from linked
apps. Both take `remote_id` and reply with `{}`, or with error `-32004` if the
app no longer has it. This deletes the owner's data in the app, so delete only
the item named.

#### Following site sources

A plugin with `pull`, no `push`, and `source_kinds` follows site sources. The
core binds each live source of those kinds to the plugin's oldest working
instance. The `remote_id` is `kind:ref`, such as `chub:alice/vyr`. The core
checks each source on a schedule and offers the author's changes in the
changes queue.

| Method | What to return |
|---|---|
| `target.describe` | `pull` true, `update` false. Leave `character_book` out of `fields`; the core doesn't compare embedded books. Set `lorebooks` to `standalone` to follow lorebook sources |
| `target.get`, `target.get_lorebook` | The site's current copy. A card check sets `avatar`, so return the image when asked. Leave `lorebook_remote_ids` out of `target.get` |
| `target.put`, `target.put_lorebook`, `target.list`, `target.list_lorebooks` | Don't implement. The core never pushes to a pull-only plugin or lists its items |

Errors and stability:

- Return `-32004` when the page is gone, private, or hides its definition.
  The core shows "no longer on the site".
- Return `-32004` for a `remote_id` whose kind isn't in your `source_kinds`.
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

A catalog lets the owner search a remote site's cards and lorebooks without
putting them in a library. It forwards each call to the site and stores
nothing. To copy a source into Innkeeper, write an [`import`](#import) plugin
instead.

Every item has an `id` you assign that never changes, a `kind` in lowercase
`snake_case`, and one or more versions, each named by a string you choose.
The core matches kinds `card` and `lorebook` against the library.
Reply with error `-32004` for an ID or version you don't have. Check params
before reaching your source, so a bad request gets `-32602` even when the
source is down.

**`catalog.describe`** takes no params. Don't contact your source to answer.

| Field | Meaning |
|---|---|
| `kinds` | The kinds you hold, such as `["card", "lorebook"]` |
| `filters` | The filters `catalog.search` accepts. Each has `name` (lowercase `snake_case`), `label`, `type`, and, for `enum` only, `options`: a list of `{value, label}` |
| `sorts` | The orders `catalog.search` accepts, each `{value, label}`. The first is the default |

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
| `hashes` | The latest version's `content_hash` and, if you have them, `text_simhash` and `image_phash`. Compute them with the Go package `github.com/inkandale/innkeeper/cardhash`, over `cardhash.CardData` of a card (its `data` without `character_book` or the SillyTavern `fav` and `world` extensions) or the whole lorebook, so they match the library's. `text_simhash` and `image_phash` are signed 64-bit integers |
| `thumbnail` | Optional. A base64 JPEG at most 160 pixels wide and 32 KiB |

**`catalog.get`** returns one version of an item.

| Param | Meaning |
|---|---|
| `id` | The item |
| `version` | A version name, or empty for the latest |
| `avatar` | `true` to write the card's image to `blob_tmp`. `innkeeper plugin check` sets it |

Reply with `item` (as in search), `version`, `versions` (newest first, each
`{version, created}`, with `created` in RFC 3339 if you know it), `data` (a
CCv3 card for a card, a CCv3 `character_book` for a lorebook), and `avatar`,
the file you wrote to `blob_tmp`, or empty. The core moves the file, so don't
reuse the path.

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

At startup and after an install, the core creates the instance of every plugin
that allows one instance, isn't a theme or `auth` plugin, and has no `secrets`
and no `required` keys in `config`. An instance the owner deletes stays deleted.

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

### `action`

An `action` plugin changes or judges a card or lorebook when the owner asks,
such as an AI that rewrites a field. Each run is a job, and the owner reviews
what it proposes before anything is saved.

**`action.list`** takes no params. Reply with `actions`, each with:

| Field | Meaning |
|---|---|
| `id` | 1 to 40 lowercase letters, digits, and hyphens, unique within the plugin |
| `label` | Short name for buttons and menus. Not empty |
| `description` | One line saying what it does |
| `applies` | `card`, `lorebook`, or `new` (a card that doesn't exist yet) |
| `fields` | For a field action: the data keys it can run on, each listed once, such as `["description", "personality"]`. Leave it out for an action on the whole entity. Not allowed with `new` |
| `whole_card` | With `fields`: `true` if the action also runs on the whole entity, on all its `fields` at once. The UI offers it in each field's menu and with the whole-entity actions |
| `instruction` | `true` if the action's main input is free text the owner types |
| `inputs` | A JSON Schema with `"type": "object"` for its other inputs. The UI renders a form from it |
| `report_only` | `true` if it never proposes changes |
| `library` | `true` if it may send `library.call` |
| `images` | `true` if it takes images. The core sends none to an action without it |

The reply may also hold `skills`: what the owner can force on with a
`@skills/<name>` mention, each `{"name", "description"}`. A `name` is 1 to 64
lowercase letters, digits, and hyphens, unique within the reply;
`description` is optional. List only the skills that are enabled. A run's
`context.skills` holds the names the owner mentioned.

The core leaves out an action or skill that breaks these rules, and logs it.
It caches the list for a minute per instance, so keep it fixed for a given
version and config. Don't contact any service to answer.

**`action.run`** runs one action.

| Param | Meaning |
|---|---|
| `run` | Token for this call, an opaque string. Put it in every notification and `library.call` you send for it |
| `action` | The action's `id` |
| `field` | For a field action: the key it runs on, one of its `fields`. Absent for a `whole_card` action run on the whole entity |
| `entity` | `{"kind": "card" \| "lorebook", "data": {...}}`: the owner's draft. A card's `data` is CCv3 `data` without `character_book`; a lorebook's is a `character_book`. `data` is absent for `new` |
| `instruction` | The owner's text, or absent. Mentions such as `@cards/Billy` stay as typed |
| `inputs` | Values for `inputs`, or `{}` |
| `context` | `{"cards": [...], "lorebooks": [...], "skills": [...]}`: what the owner attached. Cards and lorebooks are each `{"id", "name", "data"}` at their newest revision. Skills are the names of skills the owner mentioned; the core doesn't check them against `action.list`, so reply `-32602` for one you don't have |
| `images` | `{"reference": "...", "avatar": "..."}`: absolute paths in a blob store, either absent. Read them; don't move or change them |

While it runs, send `progress` notifications with `run`, `message`, and
optional `done` and `total`, as in `import`. The owner sees the latest
message, such as "Writing personality (3 of 8)", and a bar when you send a
total.

Reply with:

| Field | Meaning |
|---|---|
| `proposal` | The entity's complete new `data`, or absent. Include unchanged keys: a key you leave out counts as removed |
| `report` | A report, or absent |
| `raw` | The model's raw output, which the owner can open. Optional |
| `values`, `meta` | Enrichment values about the entity, as in an `enrich.run` result. Optional |

The core stores `values` only when the run was on a saved card and the data
it sent is the card's head revision, unchanged. Values are then checked as
`enrich.run`'s are, and an enrich run without rescore skips that revision.

A report:

```json
{
  "summary": "Markdown text",
  "score": 78,
  "dimensions": [{"key": "voice", "label": "Voice", "score": 88, "comment": "…"}],
  "findings": [
    {"severity": "high", "text": "…", "fields": ["description", "scenario"]},
    {"severity": "medium", "text": "…", "fields": ["scenario"], "move": {"excerpt": "…", "to": "description"}}
  ],
  "suggestions": ["…"]
}
```

`summary` is required. Everything else is optional. `severity` is `high`,
`medium`, or `low`. A finding's `text`, a dimension's `key` and `label`, and a
move's `to` must not be empty. A reply with an invalid report, or a `proposal`
that isn't an object, fails the run.

A finding is a problem with what the entity already holds. A suggestion is an
improvement that no finding covers, such as content the entity lacks. Don't
repeat a finding as a suggestion: the owner can tick both, and Refine then asks
for the same change twice.

The core then keeps only the changes in the action's scope:

- A field action may change only its `field`. Run without a field, a
  `whole_card` action may change any of its `fields`.
- A `card`, `lorebook`, or `new` action may change any key, except that a
  card never gets `character_book`.
- A `report_only` action's proposal is dropped.

It drops every other change, keeping the draft's value, and warns the owner
of each key it dropped.

The owner reviews a lorebook proposal entry by entry, not their order. The
Forge matches your `entries` to the draft's by `id`, then by identical
content, then by identical `keys`; an `id` or `keys` that two entries share
matches nothing. It shows the rest as added or removed. Keep each entry's
`id` when you keep or change the entry, and leave `id` out of an entry you
add. The lorebook page offers field actions on `description` only; there are
no per-entry fields.

A run isn't retried. Reply with an error, retryable or not, and the run fails
with its message. When the owner stops a run, the core stops waiting and
ignores your reply.

```json
{"jsonrpc": "2.0", "id": 9, "method": "action.run", "params": {"run": "7RJW2K5QXN3HBVYD4M6PCZTLGA", "action": "tighten", "field": "description", "entity": {"kind": "card", "data": {"name": "Brakka", "description": "A very big, very tall orc."}}, "inputs": {}, "context": {"cards": [], "lorebooks": [], "skills": []}, "images": {}}}
{"jsonrpc": "2.0", "method": "progress", "params": {"run": "7RJW2K5QXN3HBVYD4M6PCZTLGA", "message": "Tightening description"}}
{"jsonrpc": "2.0", "id": 9, "result": {"proposal": {"name": "Brakka", "description": "A towering orc."}, "raw": "{\"description\": \"A towering orc.\"}"}}
```

#### Reading the library

An action that declares `library: true` may send **`library.call`** to the
core while it answers `action.run`:

| Param | Meaning |
|---|---|
| `run` | The run's token |
| `name` | `search_cards`, `get_card`, `get_lorebook`, or `get_history` |
| `arguments` | The tool's arguments, as Innkeeper's MCP server takes them |

The reply has `content` and `is_error`, as `mcp.call`'s does. A failure the
model should see, such as an unknown card ID, comes back with
`is_error: true`.

The core refuses with error `-32003` when the action doesn't declare
`library`, when the owner hasn't turned on **Library access** for the
instance (it's off by default), or for any other tool. Access ends with the
run: once the owner stops it, calls get `-32003` within a few seconds, and a
call with the token of a run that has finished gets `-32602`.

```json
{"jsonrpc": "2.0", "id": "c1", "method": "library.call", "params": {"run": "7RJW2K5QXN3HBVYD4M6PCZTLGA", "name": "get_card", "arguments": {"id": 12}}}
{"jsonrpc": "2.0", "id": "c1", "result": {"content": [{"type": "text", "text": "{\"entity\": {…}, \"bindings\": []}"}]}}
```

### `records`

A `records` plugin keeps data the owner manages on the instance's settings
page, such as an AI plugin's skills. The core stores none of it: it shows a
list, an editor, and Import for each record type, and passes each change to
the plugin. Keep records in your own schema (`database = true`).

The manifest declares each record type under `[records]`, keyed by a type name
in lowercase `snake_case`:

```toml
capabilities = ["action", "records"]

[records.skills]
label = "Skills"
import = ["file", "url"]

[records.skills.schema]
type = "object"
required = ["name", "body"]
properties.name = {type = "string", title = "Name"}
properties.body = {type = "string", title = "Instructions", contentMediaType = "text/markdown"}
```

| Key | Required | Meaning |
|---|---|---|
| `label` | yes | The type's heading in the UI |
| `schema` | yes | JSON Schema with `"type": "object"` for one record. The UI renders the editor from it, as it does `config`; a string with `contentMediaType` gets a multi-line box |
| `import` | no | What Import offers, each listed once: `file` sends `path` and `name`, `url` sends `url`. Leave it out and the type has no Import |

Every method takes `type`, one of your record types. Reply `-32602` for a type
you don't declare. A record's `id` is yours: not empty, at most 256 bytes, no
`/`, and not `.` or `..`. The core refuses any other ID before it reaches
you. Reply `-32004` for an `id` you don't have.

For `records.put` and `records.import`, reply `-32602` with a message the owner
can read when the input is wrong, such as a record that breaks your schema or
a file that isn't a skill. The core shows the owner that message.

**`records.list`** takes `type`. Reply with `records`, each a summary:

| Field | Meaning |
|---|---|
| `id` | The record's ID |
| `name` | The name to show. Not empty |
| `description` | One line, optional |

The core leaves out a summary that breaks these rules, and logs it.

**`records.get`** takes `type` and `id`. Reply with `record`, the record as an
object that fits the type's schema.

**`records.put`** creates or replaces one record.

| Param | Meaning |
|---|---|
| `type` | The record type |
| `id` | The record to replace, or absent to create one |
| `record` | The whole record, an object |

Reply with `id`, the record's ID. You may change a record's ID when it is
replaced, such as a skill renamed, and reply with the new one.

A create must never replace a record. If you derive the ID from the record,
such as a skill's name, and a record with that ID exists, reply `-32602`
saying so.

**`records.delete`** takes `type` and `id`. Reply with `{}`.

**`records.import`** turns a file or a page into records.

| Param | Meaning |
|---|---|
| `type` | The record type |
| `url` | An `http` or `https` URL the owner typed. Send either `url`, or `path` and `name` |
| `path` | An uploaded file, directly inside `blob_tmp`. Read it; don't move or change it. The core deletes it after your reply |
| `name` | The uploaded file's name, such as `SKILL.md` or `college.zip`. The owner's browser sent it, so don't trust it; never use it as a path. The core refuses a name with `/`, `\`, or NUL |

Reply with `records`, a summary of each record imported. An import may replace
a record with the same ID.

```json
{"jsonrpc": "2.0", "id": 10, "method": "records.put", "params": {"type": "skills", "record": {"name": "college", "body": "# College\n…"}}}
{"jsonrpc": "2.0", "id": 10, "result": {"id": "college"}}
{"jsonrpc": "2.0", "id": 11, "method": "records.import", "params": {"type": "skills", "path": "/blobs/tmp/upload-1234", "name": "college.zip"}}
{"jsonrpc": "2.0", "id": 11, "result": {"records": [{"id": "college", "name": "college", "description": "Campus life and classes"}]}}
```

### `enrich`

An `enrich` plugin computes values about a card revision, such as an AI's
quality score. The core stores every result, and filters and sort can use
any instance's values. A card's **grid score**, on its tile, is its value of
the grid key from the highest-priority instance that has one. The core sends
revisions only when the owner asks, never on save, except to a plugin
that sets `enrich_on_import`, which also gets each import's new cards.

Declare each key in the manifest:

```toml
capabilities = ["enrich"]

[enrichments]
quality = { type = "score", range = [0, 100], label = "Quality", grid = true, priority = 300 }
voice = { type = "score", range = [0, 100], label = "Voice" }
summary = { type = "text", label = "Summary" }
```

| Type | Value |
|---|---|
| `text` | A JSON string |
| `score` | A JSON number within `range`, ends included |
| `list` | A JSON array of strings |

**`enrich.run`** computes values for up to 20 revisions.

| Param | Meaning |
|---|---|
| `run` | Token for this call, an opaque string. Put it in every notification you send for it |
| `revisions` | `[{"card_id", "revision_id", "data"}]`, 1 to 20 of them. `data` is CCv3 `data` without `character_book` |
| `thorough` | `true` when the owner rescored a card from its page and wants your most careful values, not a fast bulk path. Optional |

While it runs, send `progress` notifications with `run`, `message`, and
optional `done` and `total`, as in `action.run`. The owner sees the latest
message.

Reply with `results`, one per revision:

| Field | Meaning |
|---|---|
| `revision_id` | The revision, from `revisions` |
| `values` | Values by key |
| `meta` | An object stored with each value, such as the model and the prompt's version. Optional; at most 16 KiB |
| `error` | Why this revision has no values, in a line. Set this or `values` |

The core stores each value whose key you declared, whose type matches, and,
for a score, that is within its range. A value must be at most 64 KiB of
valid UTF-8 with no NUL character (`\u0000`) in any string, and so must
`meta`. The core drops any other value or `meta` and shows the owner a
warning for each. A revision with `error`, or missing from
`results`, stores nothing and counts as failed; the rest of the reply still
counts. Reply with a JSON-RPC error only when the whole call failed, such as
a missing API key: every revision in it fails.

A run isn't retried. When the owner stops one, the core sends `cancel` and
keeps what earlier calls stored.

```json
{"jsonrpc": "2.0", "id": 12, "method": "enrich.run", "params": {"run": "Q4M6PCZTLGA7RJW2K5QXN3HBVY", "revisions": [{"card_id": 4, "revision_id": 812, "data": {"name": "Brakka", "description": "A towering orc."}}, {"card_id": 5, "revision_id": 813, "data": {"name": "Mira"}}]}}
{"jsonrpc": "2.0", "method": "progress", "params": {"run": "Q4M6PCZTLGA7RJW2K5QXN3HBVY", "message": "Evaluating Brakka", "done": 0, "total": 2}}
{"jsonrpc": "2.0", "id": 12, "result": {"results": [{"revision_id": 812, "values": {"quality": 78, "voice": 88}, "meta": {"model": "claude-opus-5-5", "prompt": "evaluate-2"}}, {"revision_id": 813, "error": "the model returned no JSON"}]}}
```

### `ai`

An `ai` plugin offers model services to other plugins. One instance is one
connection to a model. The owner picks which instance a consumer uses, and
the core forwards the consumer's calls to it.

| Method | Does |
|---|---|
| `ai.chat` | One model turn: messages and optional tools in, text and tool calls out |
| `ai.embed` | One vector per text |
| `ai.decide` | Typed questions about one state in, an answer with probabilities per question out |

**Offering.** List the methods the instance offers in initialize's
`services`, from its config. Don't contact the model during `initialize`.

**Calling.** Declare the methods in `uses`, then send them to the core with
`instance`, the provider instance's ID from your config. The core replies
`-32003` when your manifest doesn't declare the method, when the instance
doesn't offer it, or when you call during your own `initialize`. It forwards
everything else to the provider without reading it. The provider gets the
same params without `instance`, and with `run` replaced by the core's own
token.

**`ai.chat`**

| Param | Meaning |
|---|---|
| `instance` | The provider instance; the core removes it before forwarding |
| `run` | Optional token. When set, the provider streams `ai.delta` notifications and the core relays each to the caller with this token |
| `messages` | `[{"role", "content", "parts", "tool_calls", "tool_call_id"}]`. `role` is `system`, `user`, `assistant`, or `tool`. `parts` replaces `content` for a message with images: `[{"type": "text", "text"}, {"type": "image_url", "image_url"}]`, where `image_url` may be a `data:` URL |
| `tools` | `[{"name", "description", "parameters"}]`, `parameters` a JSON Schema. Optional |
| `temperature`, `max_tokens` | Optional; the provider's defaults otherwise |

Reply with `text`, `model` (the model that answered), and, when the model
called tools, `tool_calls`:
`[{"id", "name", "arguments", "extra"}]`, `arguments` a JSON string. `extra`
is optional provider data, such as Gemini's thought signature. Put the tool
calls on the next `assistant` message unchanged, `extra` included, answer each
with a `tool` message carrying its `tool_call_id`, then call `ai.chat` again.

While it streams, the provider sends `ai.delta` with `run` and `text`, the
next piece of the reply, in order. The caller gets every delta before the
reply.

A provider that refuses the request as invalid, such as tools for a model
without tool support, replies `-32010`. Rate limits and timeouts are
retryable errors.

**`ai.embed`** takes `instance` and `texts`, and replies with `vectors`, one
array of numbers per text, in order. Reply `-32010` only when the provider
refuses a text, so the core can skip that card. Any other error, such as a
refused key, fails the embed job.

**`ai.decide`** asks questions about one state and gets each answer as
probabilities, not text, in the shape of a decision model's `/v1/systemone`.
Each question is answered on its own against the same state.

| Param | Meaning |
|---|---|
| `instance` | The provider instance |
| `state` | What the questions are about: a JSON string, or any JSON value |
| `questions` | Questions by an ID you choose: `{"type", "instructions", "criteria"}`. A `choice` picks one option; its `criteria` is an object of option name to description. A `score` places the state on 2 to 10 levels; its `criteria` is an array of level descriptions, low to high. A `noul` asks whether `instructions`, a statement, is true; it has no `criteria` |
| `images` | Image URLs, which may be `data:` URLs, that go with the state. Optional |

Reply with `answers` under the same IDs, and `model`. Each answer has its
`type` and:

| Type | Fields |
|---|---|
| `choice` | `choice`, the likeliest option; `probabilities` by option; `confidence` |
| `score` | `score`, the probability-weighted level from 0 to the top level, which can fall between levels; `probabilities` by level number, as a string; `confidence` |
| `noul` | `noul`, the probability the statement is true |

`confidence`, from 0 to 1, is how concentrated `probabilities` are: 1 when
one option or level has it all. A provider may answer through a chat model,
whose probabilities are rougher; set its confidence lower to match.

When the caller cancels a call, the core cancels the provider's.

```json
{"jsonrpc": "2.0", "id": "c4", "method": "ai.chat", "params": {"instance": 9, "run": "mine-1", "messages": [{"role": "user", "content": "Say hello."}]}}
{"jsonrpc": "2.0", "method": "ai.delta", "params": {"run": "mine-1", "text": "Hello"}}
{"jsonrpc": "2.0", "id": "c4", "result": {"text": "Hello", "model": "gpt-4o-mini"}}
```

### `settings`

A `settings` plugin helps the owner fill in its [config form](#the-config-form):
defaults too long or too changeable for the manifest, choices for a field, and
buttons such as **Test connection**. Declare the buttons in the manifest:

```toml
capabilities = ["ai", "settings"]

[settings.actions]
test = { label = "Test connection" }
```

The core sends these calls to a separate process of the plugin, started with
an empty config and no secrets, so `initialize` must succeed with an empty
config. The core replaces that process when the plugin's install changes.
Keep nothing between calls: `settings.options` and `settings.run` each carry
the form's unsaved draft.

| Draft param | Meaning |
|---|---|
| `config` | The form's unsaved config |
| `secrets` | The saved secrets with the owner's typed changes on top, by name |

**`settings.defaults`** takes no params. Reply with `defaults`, values by JSON
pointer into the config, such as `{"/prompts/tighten": "Make it shorter."}`.
They replace the schema's `default` at the same pointer. Reply `-32601` if
you have none.

**`settings.options`** takes the draft and `field`, the JSON pointer of an
`x-options` field. Reply with `options`, each `{"value", "label"}`; `label`
is optional and defaults to `value`. When you can't list choices yet, reply
with an error saying why, such as "set the URL first".

**`settings.run`** takes the draft and `action`, one of your
`settings.actions` IDs; the core refuses any other. Reply with:

| Field | Meaning |
|---|---|
| `ok` | `true` if it passed |
| `message` | A line for the owner. Optional |
| `checks` | The report's rows, each `{"name", "ok", "message", "latency_ms"}`; `message` and `latency_ms` are optional |

A failed test is `ok: false`, not an error. Reply with an error only when the
action couldn't run.

The owner sees your error messages. The core replaces each secret's value in
your replies and error messages with `[redacted]`, but it matches values
literally, so an encoded or partial secret gets through. Don't echo secrets.

```json
{"jsonrpc": "2.0", "id": 14, "method": "settings.run", "params": {"action": "test", "config": {"url": "https://api.example.com"}, "secrets": {"api_key": "…"}}}
{"jsonrpc": "2.0", "id": 14, "result": {"ok": false, "message": "The key was refused.", "checks": [{"name": "chat", "ok": false, "latency_ms": 212}]}}
```

The UI reaches these calls through the API:

| Route | Tier | Does |
|---|---|---|
| `GET /api/plugins/{name}/settings/defaults?instance=<id>` | `read` | Replies with `defaults`, the schema's and the plugin's, and `updated`: the pointers of the instance's overrides whose default changed since they were saved. `instance` is optional. Sends the plugin no secrets |
| `POST /api/plugins/{name}/settings/options` | `destructive` | Takes `field` and the draft: `config`, `secrets` (`null` removes a saved one), and optionally `instance`, the saved instance of this plugin whose secrets the draft starts from |
| `POST /api/plugins/{name}/settings/run` | `destructive` | Takes `action` and the draft, as for options |

Options and run need `destructive` because a draft can send the saved
secrets to any address. `POST /api/instances` and `PATCH /api/instances/{id}`
take `default_bases`: for each pointer the owner just overrode, the default it
replaced, which is how `updated` knows. `GET /api/instances?offers=<method>`
lists the working instances that offer a service, for the instance picker.

## Checking a plugin

```bash
innkeeper plugin check path/to/plugin
```

It loads the manifest, then checks that:

- the `initialize` reply matches the manifest
- an unknown method returns `-32601` within 5 seconds
- a `push` or `pull` plugin's `target.describe` reply is valid, and claims `pull` only
  if the manifest declares it
- an `auth` plugin's `auth.begin` returns a `redirect` or a `form` with
  `"type": "object"`, or an error other than `-32601`, and its `auth.setup`
  returns such a form or `-32601`
- a `push` plugin whose `target.describe` claims `pull` answers `target.list` and
  `target.list_lorebooks` within 5 seconds, with a `remote_id` on every item
  it returns. A target checked without its app can only return an error, so
  an error other than `-32601` counts as an answer.
- a plugin whose `target.describe` claims `delete` answers `target.delete`,
  and `target.delete_lorebook` if it keeps standalone lorebooks, in the same
  way
- a `push` plugin whose `target.describe` claims `delete` answers
  `target.put` of a sample card, and `target.put_lorebook` of a sample
  lorebook if it keeps standalone lorebooks, with a `remote_id` or an error
  other than `-32601`, then deletes what it put. A target that can't delete
  isn't sent a put, so a check against a real app leaves nothing in it.
- a plugin that pulls without pushing answers `-32004` to `target.get` for
  `innkeeper-contract:none`, and to `target.get_lorebook` if it keeps standalone
  lorebooks
- a `codec` plugin encodes a sample card in each of its `formats` into
  `blob_tmp` with a valid extension, decodes it back, and returns an error
  when asked to decode a file that isn't a card
- an `mcp` plugin's `mcp.tools` reply has valid, unique names, valid input
  schemas, and comes within 5 seconds; `mcp.call` of each tool with `{}` as
  its arguments returns a result or an error other than `-32601`
- an `action` plugin's `action.list` reply has valid actions with unique IDs,
  and `action.run` of each, on a sample card or lorebook with the action's
  first field and empty inputs, returns a valid reply or an error other than
  `-32601` within 5 seconds. The check answers `library.call` with `-32601`
- an `action` plugin's `skills`, if any, are valid with unique names
- a `records` plugin's `records.list` for each type returns valid summaries with
  unique IDs, or an error other than `-32601`. When it answers with records,
  `records.get` of the first returns an object, and `records.put` of that object
  with its `id` replies with an ID. When it lists records, `records.get` and
  `records.delete` of `innkeeper-contract:none` return `-32004`, and
  `records.import` from each input a type takes, a URL that can't resolve or
  a small text file in `blob_tmp`, returns valid summaries or an error other
  than `-32601`
- an `import` plugin's `import.run` with `{}` as its params and a null
  checkpoint returns a reply or an error other than `-32601` within 5 seconds
- an `enrich` plugin's `enrich.run` of a sample card returns, within 5
  seconds, a result for its revision whose values are all declared and valid,
  or an error other than `-32601`
- an `ai` plugin's `ai.chat`, `ai.embed`, and `ai.decide`, for each it lists in `services`,
  return a valid reply or an error other than `-32601` within 5 seconds. A
  chat reply has text or tool calls, an embed reply one vector per text, and
  a decide reply an answer of each question's type. Checked without a
  connection, a provider can only fail, which counts as an answer
- a `settings` plugin's `settings.defaults` returns defaults or `-32601`, and
  `settings.run` of each of its `settings.actions` on an empty draft returns
  a reply or an error other than `-32601`
- a plugin that declares `uses` runs with the provider plugin in
  `INNKEEPER_PROVIDER_PLUGIN` behind the core's service broker, and the
  provider talks to a fake OpenAI-compatible server. Its config fields with
  `format = "instance"` point at the provider. Making the calls above for its
  tools, actions, records, imports, `enrich.run`, and settings actions: a
  service call gets the provider's reply; the reply's two
  streamed pieces reach the plugin as `ai.delta`; a tool call the model makes
  comes back with the same id, name, and arguments, and a `tool` message
  answering it; and cancelling the plugin's call ends the provider's request
  within 5 seconds. A tool call's `extra` isn't checked, because the OpenAI
  format has no field for it
- a `catalog` plugin's `catalog.describe` reply is valid; `catalog.search`
  pages one item at a time without repeats and refuses an undeclared filter
  with `-32602`; `catalog.get` returns `-32004` for an unknown ID, and for the
  first search result returns data that hashes to its `content_hash` and, if
  it returns an avatar, one inside `blob_tmp`. Without `INNKEEPER_PLUGIN_CONFIG`, a
  catalog may refuse search with an error other than `-32601`, and may then
  answer `catalog.get` the same way; the checks that need an item pass
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

For a plugin that declares `uses`, set `INNKEEPER_PROVIDER_PLUGIN` to the
provider's plugin folder and `INNKEEPER_PROVIDER_CONFIG` to its instance
config. The check replaces `${INNKEEPER_FAKE_AI_URL}` in that config with its
fake server's URL:

```json
{"provider": "openai-compatible", "base_url": "${INNKEEPER_FAKE_AI_URL}", "model": "contract"}
```

## Publishing a source

A source is an `index.toml` that the owner adds to Innkeeper by its
`https://` or `file://` URL. It lists each plugin version, with a gzipped
tarball per platform:

```toml
categories = ["Apps", "Import", "Login"]

[[plugin]]
name = "lumiverse"
title = "Lumiverse"
version = "0.2.0"
protocol = 1
capabilities = ["push", "pull"]
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
  the wizard shows them in; unlisted ones follow alphabetically. The wizard
  folds a category named `Advanced`, for plugins few owners need.
- `title` is the name the setup wizard shows, as in the manifest.
- `uses` lists the service methods the plugin calls, as in the manifest. The
  setup wizard then chooses a plugin that offers them, and sets that one up
  first.
- `recommended = true` marks a plugin the Plugins page suggests, and the setup
  wizard starts with it chosen. Innkeeper does both only for the `official`
  source. When a category recommends several plugins with the same
  capabilities, such as two apps, the wizard chooses none of them and asks the
  owner to pick.
- `os` and `arch` use Go's names, such as `linux` and `arm64`.
- `url` may be relative to the index. An `https://` index must point at
  `https://` tarballs, and a `file://` index at local files.
- The tarball holds `plugin.toml` and the files it needs, at the top level.
  It may contain only regular files and folders, no links. Its manifest's
  `name`, `version`, and `protocol` must match the index entry.
- Innkeeper refuses a tarball whose sha256 doesn't match the index. That
  catches a corrupted or swapped download, but whoever controls the index
  controls what installs.
