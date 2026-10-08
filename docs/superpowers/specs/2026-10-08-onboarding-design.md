# Onboarding design

Date: 2026-10-08 · Ticket: INN-36

## Purpose

A fresh server has no login provider, so no one can log in until the owner
hashes a password by hand and creates a `password-login` instance with
`ink call`. The web UI only points at the README. After the first login,
connecting apps and importing cards means finding the right Settings pages.

This spec adds a way to **claim** a fresh server from the browser, a config
alternative for deployments managed as code, and a setup **wizard** that runs
after the claim.

In this spec, a server is **unclaimed** while it has no usable login provider:
no instance of an `auth` plugin with an `owner_subject` and no broken error.
This is the check `loginProviders` already makes.

## Decisions

| Topic | Decision |
|---|---|
| Claim proof | A one-time setup code in the API log, as Jenkins and Grafana do. |
| Config-only deploys | `INNKEEPER_OWNER_PASSWORD_HASH` creates the password login at startup. |
| Who hashes the password | The plugin, through new optional protocol methods. The core never knows a plugin's secret format. |
| Login providers at claim | `password-login` only. OIDC and others are added in Settings afterwards. |
| Wizard layout | Side rail of steps, like Settings. |
| Wizard steps | Choose plugins, set up each plugin, bring in cards. Each can be skipped. |
| Plugin groups | A `category` field in `index.toml`, not derived from capabilities. |
| When the wizard shows | Once after the claim, until finished or skipped. Settings reopens it. |
| Slices | A: claim and env var. B: wizard. |

## Slice A: claim

### Protocol

Two optional methods for `auth` plugins. Both run with an empty
`initialize`, as `innkeeper plugin check` already does, because no instance
exists yet. A plugin without them returns "method not found" and can't be
used to claim.

**`auth.setup`** takes no params and replies with `form`: a JSON Schema
object in the same shape `auth.begin` uses.

**`auth.setup.complete`** takes `params`, the form's fields, and replies
with:

| Field | Meaning |
|---|---|
| `secrets` | The instance's secrets, such as `password_hash` |
| `subject` | The subject this provider will report for the owner; the core stores it as `owner_subject` |

Reply with an error to reject the form. The core shows its message, since it
describes the owner's own input.

```json
{"jsonrpc": "2.0", "id": 7, "method": "auth.setup.complete", "params": {"params": {"password": "…", "confirm": "…"}}}
{"jsonrpc": "2.0", "id": 7, "result": {"secrets": {"password_hash": "$2a$12$…"}, "subject": "owner"}}
```

`password-login` asks for `password` and `confirm`, rejects a mismatch or a
password under 12 characters, and returns a bcrypt hash at its existing cost.

`innkeeper plugin check` calls `auth.setup` on `auth` plugins and accepts
either a form or "method not found".

### Setup code

While the server is unclaimed, `innkeeper api` makes a random code (128
bits, base32, grouped for typing) and logs it at WARN. It does this at
startup, or on the first `GET /api/setup` if the server becomes unclaimed
later, such as when the owner deletes the only login:

```text
level=WARN msg="server is unclaimed; open /setup and enter the setup code" code=ABCD-EFGH-…
```

The code lives only in that process's memory, so a restart makes a new one.
With more than one API replica, the claim must reach the process that logged
the code; one replica is the supported setup.

### Endpoints

Both are unauthenticated. `GET` returns 404 once the server is claimed.

- `GET /api/setup` lists the `auth` plugins that answer `auth.setup`, with
  their forms. Today that is `password-login`.
- `POST /api/setup` with `{code, plugin, params}`:
  1. Takes the login slot, so claims and logins run one at a time, then
     compares `code` in constant time. A wrong code fails without calling
     the plugin.
  2. Calls `auth.setup.complete` with `params`.
  3. Re-checks that the server is unclaimed, then creates an instance named
     "Login" with the returned secrets and `owner_subject`.
  4. Starts a session, as a completed login does.

A wrong code, an already-claimed server and a lost race all get the same
error after the same one-second delay as a failed login. A plugin's
rejection of the form is shown as the plugin wrote it.

### Env var

With `INNKEEPER_OWNER_PASSWORD_HASH` set:

| Server state | Behavior |
|---|---|
| Unclaimed | The API creates the `password-login` instance with that hash and `owner_subject` `owner`, and logs no setup code. |
| Claimed | The API logs a WARN that the variable is ignored and changes nothing. |
| Not a bcrypt hash | The API refuses to start and says to make one with `password-login hash`. |

Only the hash is accepted, so the password never sits in plain text in the
deployment.

### Web UI

`/setup` replaces the "No login provider is set up" message: the app
redirects there whenever `GET /api/setup` succeeds. It asks for the setup
code, then renders the plugin's form. Under the button it says the password
can instead be set with `INNKEEPER_OWNER_PASSWORD_HASH`, and that other login
providers can be added in Settings after setup. A successful claim goes to
`/welcome`. If the server gets claimed while the page is open, it says so and
links to login.

### Docs

The README's "First login" section leads with the claim, then the env var.
`docs/protocol.md` documents both methods.

## Slice B: wizard

### Catalog fields

Each `index.toml` entry in innkeeper-plugins gains two optional fields:

| Field | Example | Use |
|---|---|---|
| `category` | `"Apps"` | The wizard's group heading |
| `released` | `2026-10-06` | Shown next to the version |

`/plugin-catalog` passes both through. The plugin repo's release step sets
`released` when it bumps `version`. An entry without `released` shows only
its version.

### Route and flag

`/welcome` is a side-rail page like Settings. The rail lists Claimed, Choose
plugins, Set up plugins (with one sub-item per chosen plugin) and Bring in
cards.

Finishing or "Finish later" sets the `onboarding_done` setting. After login,
the app redirects to `/welcome` while that setting is unset. Settings has a
"Set up apps and import" link that opens the wizard again.

The wizard keeps no state of its own. Each step reads the installed plugins,
instances and jobs, so leaving and coming back loses nothing.

### Step: choose plugins

A panel titled "Plugins from the official catalog", with a grid of compact
cards grouped by `category`. Each card shows the name, version, `released`
date and a description cut to two lines. Installed plugins show an
"installed" badge. Known categories come first in a fixed order (Apps,
Import, Login); unknown ones follow under their own names, and entries
without a category go under "Other". Formats (`card-codec`) are built in and
not listed.

"Install and continue" installs the selected plugins through
`POST /plugins/{name}/install`. A failed install shows its error on that
card, and the rest continue.

### Step: set up plugins

One sub-step per selected plugin: an instance name, then the plugin's config
through `SchemaForm`. Saving creates the instance and runs `check`. A failed
check keeps the instance and shows the error, with retry and skip. A plugin
with no config fields gets its instance created without a sub-step.

### Step: bring in cards

Built from the instances that exist:

- each `pull` instance gets "Import from <name>", which starts the existing
  whole-app import (`POST /imports` with the instance);
- a `card-import` instance adds "Upload files", which uploads through
  `/blobs` and imports each file.

Job progress shows inline. Leaving the page doesn't stop a job. Reconcile
stays in Settings.

## Testing

- **Core (Go, against the test database):** wrong code, right code, double
  claim, 404 after claim, env var with a valid hash, an invalid hash and an
  already-claimed server. A fakeplugin case for `auth.setup`. `plugin check`
  with and without the setup methods.
- **innkeeper-plugins:** `password-login` setup methods; catalog parsing with
  and without `category` and `released`.
- **Web:** the claim page; the wizard's steps, skipping, and the redirect
  while `onboarding_done` is unset.
- **End to end:** a fresh `docker compose up`, claim with the logged code,
  install lumiverse, import. The env var path against innkeeper.pureroot.com.
