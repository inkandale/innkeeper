# Card advisor and plugin UI

Date: 2026-11-01

## Purpose

An owner with a large library knows the mood they want, not the card. This
design adds `card-advisor`, a plugin that answers "a slow-burn rival, not too
dark" in a live chat with a shortlist from the library and a reason for each
pick.

Plugins can't reach the browser today: they are stdio processes, and the only
web assets they ship are theme stylesheets and icons. So the design has two
parts:

- **Core:** a generic `ui` capability that lets any plugin show a sandboxed
  panel. Core knows nothing about chat.
- **Plugin:** `card-advisor` owns the panel, conversations, prompts, and model
  loop.

## Decisions

| Topic | Decision |
|---|---|
| Main job | Recommend cards from the library for a described mood, refined over several turns. |
| Surface | A chat bubble in the corner of every page, opening a panel. |
| Plugin UI | A plugin ships a web bundle. Core mounts it in a sandboxed iframe. Core has no chat code. |
| Slots | `corner` only. Add others when a plugin needs one. |
| Writes | The plugin never writes to the library. Its buttons open core's own dialogs, and the owner confirms there. Plugin code can't prove a click was real, so one click plus a confirm is the limit. |
| Memory | Conversations are saved and can be reopened. Each new chat starts fresh; there is no taste profile. |
| Scope | The current library. A "Look across libraries" toggle in the panel widens similar and related cards; search stays in one library, because core's search has no all-libraries scope. Archived cards stay out, as they do in search. |
| Turn execution | In the API process, not as a job. Job streams poll the database, which is too coarse for text as it's written. |
| Taste signals | Out of scope. Innkeeper has no ratings, favorites, or chat history to learn from. |

## Core: the `ui` capability

### Manifest

```toml
capabilities = ["ui"]
uses = ["ai.chat"]
database = true

[ui]
entry = "ui/index.html"
slot = "corner"
permissions = ["library.read", "cards.push", "cards.collect"]
```

| Permission | Lets the plugin |
|---|---|
| `library.read` | Send `library.call` during a run |
| `cards.push` | Open core's push dialog with `host.request` |
| `cards.collect` | Open core's add-to-collection dialog with `host.request` |

A manifest with `[ui]` must declare the `ui` capability and an `entry` in a
subfolder of the plugin folder, since core serves that whole folder. An unknown slot or permission fails validation.

### Grants

A declared permission does nothing until the owner grants it on the instance.
Every grant is off by default. A new `plugin_instance.grants text[]` column
holds them, and the migration turns `library_access = true` into the
`library.read` grant, then drops `library_access`. The instance settings page
lists one switch per declared permission. `PATCH /instances/{id}` takes
`grants` in place of `library_access`. An action plugin keeps its **Library
access** switch, which now sets the `library.read` grant.

### Serving the bundle

`GET /api/plugin-ui/{name}/{path...}` serves files from the folder holding `entry`
with `servePluginFile`, so symlinks can't escape the plugin folder. Each
response sets:

- `X-Content-Type-Options: nosniff`
- `Cache-Control: no-cache`
- `Access-Control-Allow-Origin: *`
- `Content-Security-Policy: sandbox allow-scripts; default-src 'none'; script-src <base>; style-src <base> 'unsafe-inline'; img-src <base> data:; font-src <base>; connect-src 'none'; frame-ancestors 'self'`

`<base>` is the request's scheme and host plus `/api/plugin-ui/<name>/`, so a
bundle loads only its own files. `'self'` can't be relied on in a sandboxed
frame, whose origin is opaque, and a bare origin would let the bundle load the
app's other scripts.

The route is public, like theme stylesheets and icons, and a bundle must hold
nothing secret. It can't need a session: requests from an opaque origin don't
carry the `SameSite=Lax` cookie. CORS is open so module scripts and fonts load
from that origin. An uninstalled plugin returns 404.

### Launcher and sandbox

The Shell shows one bubble per enabled `ui` instance with slot `corner`, using
the plugin's title and icon. Clicking it opens a panel with the iframe:

```html
<iframe sandbox="allow-scripts" src="/api/plugin-ui/card-advisor/index.html"></iframe>
```

Without `allow-same-origin`, the iframe has an opaque origin. It can't read the
session cookie or call `/api`. With `connect-src 'none'`, it can't reach any
other server. Its only way out is the bridge.

### Bridge

The Shell handles `postMessage` requests from the panel's iframe, at most 4
`ui.call`s at once. It compares
`event.source` with that iframe's `contentWindow` and ignores anything else.

| Call | Does | Needs |
|---|---|---|
| `ui.call(method, params)` | Calls the plugin's backend and streams events back to the iframe | Nothing |
| `host.context()` | Returns the current library ID, the card on screen if any, theme tokens, and the instance's grants | Nothing |
| `host.openCard(id)` | Opens the card page in the parent | Nothing |
| `host.request("push", cardId)` | Opens core's push dialog | `cards.push` |
| `host.request("add-to-collection", cardId)` | Opens a new core dialog listing the library's collections | `cards.collect` |

### Protocol

`POST /api/instances/{id}/ui/call` takes `{"method", "params"}`, and replies
with an SSE stream. It needs the write tier, since a turn spends the owner's
model budget. The Shell makes this call with its session, on the iframe's
behalf. Core starts a run, then calls the plugin:

| Message | Direction | Meaning |
|---|---|---|
| `ui.call` | Core to plugin | `run`, `method`, `params`. The reply ends the stream |
| `ui.event` | Plugin to core | `run` and `data`, any JSON, forwarded as one SSE event |
| `cancel` | Core to plugin | The existing notification, sent when the client disconnects |
| `library.call` | Plugin to core | Allowed with the `library.read` grant while the run lasts |
| `ai.chat` | Plugin to core | The existing service broker |

A `ui.event` is limited to 64 KiB. The `-32601` reply for an unknown method and
any other plugin error become a final `error` SSE event.

### Plugin calls in the API process

The API process refuses plugin calls today, through `api.NoCore`. It now runs
the same `pluginhost.Runs` and `services.Broker` as the worker, so a `ui` run can
use `ai.chat` and `library.call`. `Runs.Handle` refuses a plugin's call outside
a run, but a service call through `Broker.call` needs no active run; the
plugin's `uses` still applies, as in the worker.

### Packaging

`make install` and `internal/publish` in `innkeeper-plugins` copy only the
binary, manifest, and icon. Both also copy the folder holding `[ui].entry`.

### Shared run plumbing

`library.call` moves out of the action session in `internal/actions` into shared
run code that both actions and `ui` runs use. Each caller supplies its own
permission check. The allowed tools gain `find_similar_cards` and
`find_related_cards`.

## Plugin: `card-advisor`

The plugin lives in `innkeeper-plugins` and is written in Go. Its config has one
field, `connection`, the AI connection that offers `ai.chat`.

### Data

The plugin's schema holds:

```sql
CREATE TABLE conversation (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    instance text NOT NULL,
    title text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE message (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    conversation_id bigint NOT NULL REFERENCES conversation ON DELETE CASCADE,
    role text NOT NULL CHECK (role IN ('user', 'assistant')),
    content text NOT NULL,
    picks jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);
```

Uninstalling leaves this data until the owner uses **Remove data**.

### UI methods

| Method | Does |
|---|---|
| `list_conversations` | Lists conversations, newest first |
| `get_conversation` | Returns one conversation's messages |
| `delete_conversation` | Deletes one conversation |
| `send` | Runs one turn |

### One turn

1. The panel calls `send` with `conversation`, `text`, `library`, and `all_libraries`.
2. The plugin saves the user's message, then calls `ai.chat` with the
   conversation and these tools:

   | Tool | Runs |
   |---|---|
   | `search` | `search_cards` with `semantic: true` |
   | `similar` | `find_similar_cards` |
   | `related` | `find_related_cards` |
   | `read_card` | `get_card` |
   | `recommend` | Ends the turn |

3. Search results reach the model as names, tags, scores, and the first 300
   characters of the description; `library.call` can't reach card-embed's
   premises and themes. Only `read_card` sends a whole card.
4. The loop stops after 8 rounds. On the last round the plugin offers only
   `recommend`, so the turn still ends with an answer.
5. `recommend` takes up to 6 `{card_id, reason}` picks. The plugin drops any ID
   that this turn's tool results didn't return, so an invented card can't appear.
6. The plugin saves the reply, sends it as `ui.event`s, and replies to `ui.call`.

Reply text streams as `ai.delta`s arrive. Picks show as cards with Open, Push,
and Add to collection buttons. Push and Add to collection show only when the
instance has the grant.

### Failures

| Case | Behavior |
|---|---|
| No connection, or `library.read` not granted | The panel explains what's missing and links to the instance's settings. No turn runs |
| Model rejects tools (`-32010`) | The panel says the connection needs a model with tool support |
| No vector source | `search` falls back to text and tag search, and the reply says so |
| A pick's card was deleted later | Its button fails with core's normal not-found message |
| Stream drops | The user's message is already saved, so the panel offers Retry |

### Prompt injection

Card text is untrusted. The system prompt marks tool results as data, not
instructions. Tools only read, so a hostile card can skew a recommendation but
can't change anything. Replies render as sanitized Markdown with images and raw
HTML removed, which together with the CSP keeps a reply from sending library
data to another server.

## Testing

- **Protocol:** manifest validation for `[ui]`, slots, and permissions.
- **Core, against real PostgreSQL:** the grants migration; `ui.call` routing,
  streaming, and cancel through `fakeplugin`; `library.call` refused without the
  grant and after the run ends; `/api/plugin-ui` headers, path escapes, and 404s.
- **Browser:** before building the rest, prove in Chrome and Firefox that a
  sandboxed bundle with a module script, stylesheet, and font loads under these
  headers.
- **Web:** the bridge rejects messages from any other source, and `host.request`
  without its grant; dialogs open for a granted request.
- **`plugin check`:** a `ui` plugin's `entry` exists and `ui.call` of an unknown
  method returns `-32601`.
- **Plugin, against real PostgreSQL:** the tool loop with the fake
  OpenAI-compatible server, covering the round limit, invented IDs dropped, and
  the no-vectors fallback.

## Out of scope

- Taste profiles, ratings, and "play next" suggestions.
- Recommending cards outside the library, such as from catalogs.
- Model-initiated writes.
- Slots other than `corner`.
