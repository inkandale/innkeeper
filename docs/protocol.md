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
| `command` | yes | Program and arguments. Use `./name` for a program in the plugin folder: a relative path containing `/` is inside the folder and can't leave it, an absolute path is used as-is, and a bare name is found on `PATH` |
| `capabilities` | yes | Any of `import`, `search`, `push`, `pull`, `action`, `enrich`, `codec`, `auth` |
| `secrets` | no | Names of secrets the owner enters for each instance, in lowercase `snake_case` |
| `config` | no | JSON Schema for instance settings; the UI renders a form from it |
| `enrichments` | no | Keys the plugin writes, in lowercase `snake_case`. Each has a `label` for the UI and a `type`: `text`, `score` (needs `range = [low, high]`), or `list` |

Unknown keys are errors.

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
   | `database_url` | PostgreSQL DSN for a role limited to the `plugin_<name>` schema |

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

## Checking a plugin

```bash
innkeeper plugin check path/to/plugin
```

It loads the manifest, then checks that:

- the `initialize` reply matches the manifest
- an unknown method returns `-32601` within 5 seconds
- `shutdown` gets a reply within 5 seconds
- the plugin exits after `shutdown`, rather than having exited earlier

It exits non-zero if any check fails.
