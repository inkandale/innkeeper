# Innkeeper

The single source of truth for roleplay character cards and lorebooks across
RP apps. Plugins live in
[innkeeper-plugins](https://github.com/davidgibbons/innkeeper-plugins).

Design: [docs/superpowers/specs/2026-10-03-innkeeper-core-design.md](docs/superpowers/specs/2026-10-03-innkeeper-core-design.md).

## Development

Database tests need PostgreSQL with pgvector and a role that can create
databases. Without `INNKEEPER_TEST_DATABASE_URL` they are skipped locally and
fail in CI.

```bash
docker run -d --name innkeeper-pg -e POSTGRES_PASSWORD=postgres -p 55432:5432 pgvector/pgvector:pg18
export INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres
go test -race ./...
```

### Card corpus

`testdata/corpus` holds real cards and golden files of what the codec,
SillyTavern and Lumiverse return for each. `TestCorpusHashes` checks that
every app's copy hashes like the codec's, reading only the goldens. After an
app or plugin upgrade, recapture them against throwaway servers, from the
repo root:

```bash
make -C ../innkeeper-plugins install DEST=/tmp/innkeeper-plugins
docker compose -f testdata/corpus/compose.yaml up -d --wait
INNKEEPER_CORPUS_CAPTURE=1 INNKEEPER_PLUGINS_DIR=/tmp/innkeeper-plugins go test ./internal/corpus -run TestCapture -count=1
docker compose -f testdata/corpus/compose.yaml down -v
```

The capture refuses to run against a server that already has cards or
lorebooks, so start the servers fresh each time. The golden diff shows any
change in an app's format; if `TestCorpusHashes` then fails, fix the cause
before committing the goldens.

## Running

One binary runs both services and the CLI. Both services migrate the
database at startup and stop cleanly on `SIGTERM`.

- `innkeeper api` serves the HTTP API under `/api` and the
  [web UI](#web-ui) at the root. The OpenAPI spec is at `/api/openapi.json`,
  and browsable docs at `/api/docs`. Job progress streams from
  `GET /api/jobs/{id}/events`, which sends a `: ping` comment every 15 seconds
  while idle.
- `innkeeper worker` runs jobs: imports, bulk actions, pushes, an hourly
  check of target apps for changes, [scheduled backups](#backup-and-restore),
  and daily cleanup of unused blobs and of jobs that finished over 30 days
  ago.

| Variable | Used by | Meaning |
|---|---|---|
| `INNKEEPER_DATABASE_URL` | `migrate`, `api`, `worker`, `token`, `backup`, `restore` | PostgreSQL URL |
| `INNKEEPER_SECRETS_KEY` | `api`, `worker`, `backup`, `restore` | Encrypts plugin secrets. Generate with `openssl rand -base64 32`. Losing it loses every stored secret |
| `INNKEEPER_BLOB_DIR` | `api`, `worker`, `backup`, `restore` | The library's blob store folder |
| `INNKEEPER_PLUGINS_DIR` | `api`, `worker`, `restore` | Writable folder that plugins install into; `/data/plugins` in the image. The API and worker must share it |
| `INNKEEPER_BUILTIN_PLUGINS_DIR` | `api`, `worker`, `restore` | Read-only folder of plugins that ship with Innkeeper, one subfolder each; an installed plugin of the same name overrides one |
| `INNKEEPER_LISTEN` | `api` | Listen address; default `:8080` |
| `INNKEEPER_PUBLIC_URL` | `api` | Where browsers reach the API, such as `https://innkeeper.example.com`. Needed for redirect logins and for MCP clients that connect through OAuth, such as claude.ai. An `http://` URL sends cookies without `Secure` |
| `INNKEEPER_WORKER_CONCURRENCY` | `worker` | Jobs run at once; default 4 |
| `INNKEEPER_BACKUP_DIR` | `api`, `worker` | Where backups go. Needed for any backup or restore through the API, and for schedules |
| `INNKEEPER_BACKUP_ASSETS_EVERY`, `INNKEEPER_BACKUP_CONFIG_EVERY` | `api`, `worker` | How often to back up that type, such as `24h` or `7d`. Unset means no schedule |
| `INNKEEPER_BACKUP_ASSETS_KEEP`, `INNKEEPER_BACKUP_CONFIG_KEEP` | `api`, `worker` | How many backups of that type to keep; default 7 |
| `INNKEEPER_URL`, `INNKEEPER_TOKEN` | API commands | The site root, such as `http://localhost:8080`, and a token. The CLI adds `/api` itself |

If any installed plugin sets `database = true`, the API and worker give it a
schema and a login role limited to it at startup. That needs PostgreSQL 15 or
later, where other roles can't create tables in `public`, and a database user
with `CREATEROLE` and `CREATE` on the database. The Compose stack's user is a
superuser.

## Try it with Docker Compose

`compose.yaml` runs PostgreSQL, the API, and the worker from the local
source:

```bash
echo "INNKEEPER_SECRETS_KEY=$(openssl rand -base64 32)" > .env
docker compose up -d --build
export INNKEEPER_URL=http://localhost:8080
export INNKEEPER_TOKEN=$(docker compose exec -T api innkeeper token create admin destructive)
curl -H "Authorization: Bearer $INNKEEPER_TOKEN" "$INNKEEPER_URL/api/tags"
```

If something else already listens on port 8080, set `INNKEEPER_PORT` in
`.env`. Docker doesn't report the clash; requests just reach the other
program.

The image builds its two built-in plugins, `password-login` and
`card-codec`, from `innkeeper-plugins` at the branch or commit in the
`PLUGINS_REF` build argument, `main` by default. A rebuild reuses its cached
clone of `main`; pass `--build-arg PLUGINS_REF=<commit>` or build with
`--no-cache` for newer ones. Install other plugins at runtime; see
[Plugins](#plugins).

Compose keeps blobs and installed plugins in one volume at `/data`.
`docker compose down -v` deletes the database and that volume.

## Kubernetes

Every push to `main` publishes `ghcr.io/davidgibbons/innkeeper:latest` and
`:sha-<commit>`, for amd64 only. Releases come from release-please: merging
its release PR tags `v<version>`, publishes the image as `:<version>` and
`:<major>.<minor>`, and publishes the Helm chart in `chart/` to
`oci://ghcr.io/davidgibbons/charts/innkeeper`. The chart's `version` and
`appVersion` both follow the release, and the chart runs the image matching
its `appVersion` unless `image.tag` says otherwise. For testing, set
`image.tag=latest` and `image.pullPolicy=Always`, and restart the deployment
to pick up a new build.

Commit messages follow [Conventional Commits](https://www.conventionalcommits.org/):
`feat:` and `fix:` decide the next version, and `feat!:` marks a breaking change.

The chart runs the API and the worker as two containers in one pod, sharing
the blob volume and a plugins volume (`persistence.plugins.size`, 1Gi). It creates no Secrets; point it at yours:

```bash
helm install innkeeper oci://ghcr.io/davidgibbons/charts/innkeeper --version <version> \
  --set database.secretName=innkeeper-db --set database.key=uri \
  --set secretsKey.secretName=innkeeper --set secretsKey.key=INNKEEPER_SECRETS_KEY \
  --set publicUrl=https://innkeeper.example.com \
  --set ingress.enabled=true --set ingress.host=innkeeper.example.com
```

`values.yaml` lists the rest: storage, resources, `extraEnv`, pod
annotations, and [scheduled backups](#backup-and-restore). Create the first token with
`kubectl exec deploy/innkeeper -c api -- innkeeper token create admin destructive`.

## First login

Auth cannot be turned off. No one can log in until a login provider names
an owner, so start with an API token:

```bash
export INNKEEPER_TOKEN=$(innkeeper token create admin destructive)
export INNKEEPER_URL=http://localhost:8080
```

The bundled `password-login` plugin logs in with one password, kept as a
bcrypt hash. Make the hash with the plugin, then create a login instance
with it:

```bash
read -rs PW && printf '%s\n' "$PW" | docker compose exec -T api /usr/lib/innkeeper/plugins/password-login/password-login hash; unset PW
ink call POST /instances '{"plugin": "password-login", "name": "Login", "owner_subject": "owner", "secrets": {"password_hash": "<hash>"}}'
```

For another login provider, `owner_subject` is the identity it reports for
you; its README says what that is. Changing or clearing `owner_subject` logs
that identity out.

## Web UI

`innkeeper api` serves the web UI at its root, for example
`http://localhost:8080/`. Log in with any login provider (see
[First login](#first-login)).

To work on the UI, run `innkeeper api` and then, in `web/`:

```bash
npm install
npm run dev
```

Vite serves the UI with hot reload and proxies `/api`, `/auth`, and `/oauth`
to `INNKEEPER_URL`, which defaults to `http://localhost:8080`. After changing
the API, run `npm run gen` to regenerate the client; it needs Go.

To restyle the UI, install a [theme plugin](docs/protocol.md#themes) and
select it with `ink call PATCH /settings '{"theme": "<name>"}'`; an
empty name restores the default. A theme's CSS runs on the UI's origin, so
install only themes you trust.

## Plugins

Innkeeper ships `password-login` and `card-codec`. Install other plugins
from a source, an `index.toml` that lists plugin versions. The official
`innkeeper-plugins` source is there by default:

```bash
ink call GET /plugin-catalog
ink call POST /plugins/card-import/install '{"source": "official", "version": "0.1.0"}'
```

Installing a newer version updates the plugin; jobs already running finish
on the old one. Innkeeper keeps the previous version, so installing it again
rolls back without a download.

Add another source by its `https://` or `file://` URL. Its plugins run as
code inside Innkeeper with their own database role, so add only sources you
trust. To publish one, see
[Publishing a source](docs/protocol.md#publishing-a-source).

```bash
ink call POST /plugin-sources '{"id": "mine", "url": "https://example.com/index.toml"}'
```

`DELETE /plugins/<name>` uninstalls a plugin but keeps its instances and
database, which come back if you install it again. To delete those too, run
`DELETE /plugins/<name>/data` afterwards. That can't be undone.

## Access tiers

Every API token has a tier, and every route needs one:

| Tier | Allows |
|---|---|
| `read` | Anything that changes nothing: lists, search, push previews, drift checks |
| `write` | Also changes to the library, which revision history can undo: edits, adopts, imports |
| `destructive` | Also what Innkeeper can't undo, or what grants more access: pushes, unbinding, deleting collections, creating or changing plugin instances, managing tokens |

`innkeeper token create <name>` makes a `read` token unless you name a tier.
A token can't create one with a higher tier. A browser login has every tier.

## MCP

The API serves MCP at `/mcp`, so Claude can search and edit the library,
push to apps, run imports, and set up plugins. Connect Claude Code with an
API token; its tier decides which tools Claude sees:

```bash
claude mcp add --transport http innkeeper "$INNKEEPER_URL/mcp" \
  --header "Authorization: Bearer $(innkeeper token create claude write)"
```

The `push` tool needs the `confirm` value from `preview_push`, so a push
always matches a preview Claude has shown you. Tools that need the
`destructive` tier are marked destructive, so Claude asks before running
them.

To connect claude.ai, or another client that signs in through OAuth, set
`INNKEEPER_PUBLIC_URL`, then add `<public URL>/mcp` as a custom connector
with no credentials. The client sends you to Innkeeper to log in, then asks
which tier to grant; read is the default. `innkeeper token list` shows
approved clients, and `innkeeper token revoke-grant <id>` cuts one off.
No MCP client can change login providers or who the owner is, whatever its
tier.

Plugins can add tools too. They show as `<instance>_<tool>`, such as
`lumiverse_list_untracked`, and each declares its tier; one that declares
none counts as `destructive`. `GET /plugin-tools` lists them.

## CLI

`ink` works with a server's API from any machine. The server binary,
`innkeeper`, runs the server and the commands that need its database.

### ink

Download `ink` for your platform from the
[latest release](https://github.com/davidgibbons/innkeeper/releases/latest),
or build it:

```bash
go install github.com/davidgibbons/innkeeper/cmd/ink@latest
```

The image includes `ink` too. It reads `INNKEEPER_URL` and an API token in
`INNKEEPER_TOKEN`; make one on the server with
`innkeeper token create <name> [read|write|destructive]`.

```text
ink call <method> <path> [json]                   any API call, such as: ink call GET /jobs
ink import <instance-id> [params-json]            start an import
ink jobs [show|cancel|watch <job-id>]             list or follow jobs
ink cards export <card-id> [file] [--format f]    a card as CCv3 JSON, or a codec's format (.png means png)
ink cards archive|unarchive <query-json>          archive or unarchive the cards a search matches
ink cards sources <card-id>                       list a card's sources
ink cards sources add <card-id> <url>
ink cards sources remove <card-id> <source-id>
ink sync push <instance-id> <card-id>|<query-json> [--new-copy]
                                                  push one card, or the cards a search matches, to an app
ink sync reconcile <instance-id> [params-json]    line an app up against the library
ink sync compare <instance-id> <remote-id> [card-id]
                                                  show how an app character differs from a card
ink sync resolve <instance-id> <remote-id> bind|import|skip [card-id] [hunk=choice ...] [--default choice]
                                                  bind, import, or skip one app character
ink sync apply <reconcile-job-id> [remote-id ...] [--verdict v,...]
                                                  resolve a reconcile's items as it suggested
```

`--json` prints JSON instead of tables, and every command takes `--help`.
`ink completion bash|zsh|fish|powershell` prints a shell completion script;
`ink completion <shell> --help` says where to put it.

`cards archive` needs a query with a `filter` or `text`; to select every card,
filter on `archived`. It touches only cards not already in the target state.
The API's `POST /bulk` also tags, collects, and exports; see `/api/docs`.

`jobs watch` exits non-zero unless the job succeeds.

`sync push` replaces the app's copy, so it overwrites changes made in the app.
`--new-copy` is for apps that can't update a card: pushing there again makes
another copy, so the push fails without the flag.

Exporting in a format other than `json` needs an instance of a codec plugin
that writes it. Export uses the oldest working codec instance for the format:

```bash
ink call POST /instances '{"plugin": "card-codec", "name": "Codec"}'
ink cards export 12 brakka.png
```

### innkeeper

```text
innkeeper api | worker | migrate                  run the server; see Running
innkeeper token create <name> [tier] | list | revoke <id> | revoke-grant <id>
innkeeper openapi                                 print the OpenAPI spec
innkeeper plugin check <plugin-dir>               run the plugin contract checks
innkeeper backup <dir> [assets|config]            back up both types, or one, into dir
innkeeper restore [--replace] <archive-or-backup-dir>...
                                                  restore backups, assets first
```

## Importing

The bundled `card-import` plugin reads CCv3, V2, and V1 cards as PNG or
JSON, and lorebooks as CCv3 or SillyTavern world info JSON. Importing the
same file again adds nothing.

To import a folder, put it under `./import`, or set `INNKEEPER_IMPORT_DIR`
in `.env`. Compose mounts it read-only at `/import`:

```bash
ink call POST /instances '{"plugin": "card-import", "name": "Import", "config": {"folder": "/import"}}'
ink import <instance-id>                         # everything in /import
ink import <instance-id> '{"path": "fantasy"}'   # only /import/fantasy
```

To import one file, upload it, then import the `sha256` from the reply
within 24 hours:

```bash
curl -sf -H "Authorization: Bearer $INNKEEPER_TOKEN" -H 'Content-Type: application/octet-stream' \
  --data-binary @brakka.png "$INNKEEPER_URL/api/blobs"
ink import <instance-id> '{"blob": "<sha256>", "name": "brakka.png"}'
```

## Sources

A source is a site a card or lorebook was published on, such as its chub.ai
page. Importing from a folder or an app, and binding to an app item, records
the `extensions.chub` entry that chub.ai imports leave in the data. Currently
chub.ai is the only site Innkeeper knows.

Add or remove one yourself with the API, MCP tools, or CLI:

```bash
ink cards sources add 12 https://chub.ai/characters/user/name
ink cards sources remove 12 3
ink call GET /lorebooks/5/sources    # the CLI covers cards; use the API or MCP for lorebooks
```

A removed source stays removed: detection won't add it back.

## Sync

The image ships a target for [Lumiverse](https://github.com/prolix-oc/Lumiverse).
Add one per Lumiverse account; its
[README](https://github.com/davidgibbons/innkeeper-plugins/tree/main/lumiverse)
covers what it syncs:

```bash
ink call POST /instances '{"plugin": "lumiverse", "name": "Lumiverse",
  "config": {"url": "https://lumiverse.example.com", "username": "owner"},
  "secrets": {"password": "<password>"}}'
ink call POST /cards/<id>/push '{"instance_id": <instance-id>}'
```

It also ships a target for [SillyTavern](https://github.com/SillyTavern/SillyTavern).
SillyTavern accepts only localhost by default, so first add Innkeeper's
address to its whitelist, or turn the whitelist off and use a login; the
[README](https://github.com/davidgibbons/innkeeper-plugins/tree/main/sillytavern)
covers that and what it syncs. Set `auth` to `none`, `basic`, or `account` to
match the server:

```bash
ink call POST /instances '{"plugin": "sillytavern", "name": "SillyTavern",
  "config": {"url": "https://sillytavern.example.com", "auth": "basic", "username": "owner"},
  "secrets": {"password": "<password>"}}'
```

A card pushed to a target app gets a binding. `GET /cards/{id}/bindings`
shows its status on each target:

| Status | Meaning |
|---|---|
| `in_sync` | Nothing changed on either side since the last push |
| `ahead` | The card, its avatar, its override for this target, or its lorebooks changed since the last push |
| `remote_changed` | Someone changed the card in the app |
| `diverged` | Both of the above |
| `missing` | The app no longer has the card. The next push makes a new copy |
| `cant_check` | The app can't be read back, so changes made there are unknown |

When the app's copy changed, the binding lists the changed fields. Take them
with `POST /bindings/{id}/adopt`, `into` one of:

- `base`: into the card itself. Adopted fields leave this target's override,
  so the app keeps what it has.
- `override`: into this target's override, so only this app keeps them
- `ignore`: leave the library as it is and stop reporting them

Changes to `character_book` can't be adopted; edit the lorebooks instead. To
throw the app's changes away, push: a push overwrites them.

Before a push, `GET /cards/{id}/push-preview?instance_id=<id>` reads the
app's copy and shows what the push would do:

- `changes`: the fields it would change in the app, with the app's value as
  `before` and the pushed value as `after`
- `replaces`: whether it overwrites a copy the app has
- `dropped_fields`: fields with content the app doesn't store
- `new_copy`: whether it makes another copy, on an app that can't update cards
- `stale`: the app couldn't be read, so `changes` compare against the last push
- `confirm`: send it with the push, and the push is refused with `409` and
  the new preview if anything changed since

### Binding what the app already has

To link a card to a character the app already has, instead of pushing a new
copy, list the app's characters and bind one:

```bash
ink call GET  /instances/<instance-id>/items
ink call POST /cards/<id>/bind '{"instance_id": <instance-id>, "remote_id": "<remote-id>"}'
```

Binding changes nothing in the app or the library. The app's copy is
accepted as it is, so the binding is `in_sync` if it matches the library and
`ahead` if it doesn't; the next push, with its preview, sends the library's
card. An item already bound shows its `binding_id`, and binding it
again is refused with `409`, as is a bind whose app copy or card changed
while it ran. In the push history, a bind shows as `"kind": "bind"`.

For lorebooks in an app that keeps them separately, use
`/instances/<instance-id>/lorebook-items` and `/lorebooks/<id>/bind`. Binding
a card leaves the app's linked world books for reconcile: none are imported,
bound, or changed. A bound card is `ahead` when the app links it to lorebooks
other than the library's bound ones, or when the plugin can't tell which it
links. The next push links the library's.

### Importing what the app already has

To take an app's whole library into Innkeeper, import from its instance:

```bash
ink import <instance-id>                                  # every unbound character and lorebook
ink import <instance-id> '{"remote_ids": ["<remote-id>"]}' # only these items
```

Each item becomes a library card or lorebook bound to the app's copy, so it
starts `in_sync`. Cards keep the lorebooks the app links them to, and their
avatars where the plugin supplies them. The job runs one child per item and
resumes after a restart. `ink jobs show <job-id>` counts the children
by outcome:

| Outcome | Meaning |
|---|---|
| `created` | Added to the library |
| `bound` | The library already had the same content, so that card or lorebook was bound instead of copied |
| `already_bound` | Another bind or import bound the item while the job ran |
| `duplicate` | The library item with this content is already bound to another item in the app, such as a duplicated character. Skipped |
| `missing` | The app no longer has the item |

Items bound before the import started are skipped and counted in the job's
`payload` as `already_bound`.

### Reconciling an app

When the app and the library both have characters, and some are probably the
same, reconcile lines them up and suggests what to do with each:

```bash
ink sync reconcile <instance-id>
```

It reads the app, changes nothing, and prints one row per app item, with
each linked world book under its character:

| Verdict | Meaning |
|---|---|
| `bound` | Already bound |
| `exact` | A library card has the same content |
| `likely` | A library card has the same name, or similar text or avatar |
| `new` | Nothing in the library matches |
| `error` | The app item couldn't be read |

Each row has a suggested action: `bind` to a card, `import` as a new card, or
`skip`. Pass `'{"match": {"name": false}}'` to stop matching by name alone,
and `--json` for the whole snapshot.

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

Resolving changes the library and the binding, never the app. Keeping the
library's side of a hunk leaves the binding `ahead`; push to send the result.
If the app's copy, the card, or the binding changed since the comparison,
`resolve` is refused with `409`; compare again.

To take every suggestion from a reconcile, or only some verdicts or items,
as one bulk job:

```bash
ink sync apply <reconcile-job-id> --verdict exact
ink sync apply <reconcile-job-id> <remote-id>   # one new item, imported
```

A suggested bind keeps the library's side of every hunk. An item with no
binding or card has nothing to compare with, so import it this way.

For world books in an app that keeps them separately, `sync compare` and
`sync resolve` cover characters only; use `ink call` with
`/instances/<instance-id>/lorebook-items/<remote-id>/compare?lorebook_id=<id>`
and `.../resolve`. The MCP tools `start_reconcile`, `compare_item`,
`resolve_item`, and `resolve_items` cover both.

### Lorebooks in apps that keep them separately

Some apps, such as Lumiverse, keep lorebooks apart from cards. Lorebooks then
sync on their own, with their own bindings, statuses, checks, and adopts:

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

## Backup and restore

Innkeeper backs up two types separately, each with its own schedule and
retention:

| Type | Holds |
|---|---|
| `assets` | Cards, lorebooks, and their revisions; avatars; tags, collections, sources, and lorebook links |
| `config` | Plugin instances with their settings and encrypted secrets; plugin databases; bindings and push history; core settings |

Jobs, API tokens, logins, and OAuth grants aren't backed up. After a restore,
make a new token with `innkeeper token create`.

### Scheduled backups

Scheduled backups are off until `INNKEEPER_BACKUP_DIR` and at least one
`_EVERY` setting are set (see [Running](#running)). The worker writes each
backup to `<type>/<type>-<UTC time>/` in the backup folder and deletes the
oldest past `_KEEP`. Assets backups share one copy of each avatar, so each
backup copies only new ones. The folder and its files are readable only by
their owner.

- **Compose:** uncomment the backup settings and the `./backups` mount in
  `compose.yaml`. On Linux, make the folder writable by the container's user
  first: `mkdir backups && sudo chown 10001 backups`.
- **Kubernetes:** set `backup.persistence.enabled=true` and
  `backup.assets.every` or `backup.config.every`. The chart adds a backup
  volume to the pod.

A failed backup shows as a failed job and in `GET /api/backups`.
`GET /api/health` needs no token and reports when each scheduled type last
backed up. It answers 503 once a type's latest backup is older than two
intervals, for an uptime monitor to watch. Don't use it as a liveness probe:
a stale backup shouldn't restart the pod.

### Backing up now and downloading

These need a destructive token, since an assets backup holds every card and a
config backup holds the encrypted secrets:

```bash
ink call POST /backups '{"type": "assets"}'
ink call GET /backups
curl -fH "Authorization: Bearer $INNKEEPER_TOKEN" -o assets.tar.gz "$INNKEEPER_URL/api/backups/<name>"
```

A download is one unencrypted `.tar.gz`. Keep `INNKEEPER_SECRETS_KEY` with
config backups, stored separately: without it, restored plugin secrets can't
be read.

`innkeeper backup <dir> [assets|config]` writes the same backups into `<dir>`
without the API. When a plugin has a database, backing up config needs
`pg_dump` on the `PATH`; the image includes it.

### Restoring

A restore covers one type and leaves the other alone. It refuses to restore
over existing data of that type unless told to replace it. Restore assets
before config: config keeps only bindings whose card or lorebook exists and
reports the rest.

```bash
innkeeper restore assets.tar.gz config.tar.gz   # archives or backup folders, in any order
innkeeper restore --replace assets.tar.gz       # replaces the whole library
```

Over the API, upload one archive at a time. It restores as a job:

```bash
curl -fH "Authorization: Bearer $INNKEEPER_TOKEN" -F archive=@assets.tar.gz "$INNKEEPER_URL/api/restores"
curl -fH "Authorization: Bearer $INNKEEPER_TOKEN" -F archive=@config.tar.gz -F replace=true "$INNKEEPER_URL/api/restores"
```

The API holds an upload in the system temp folder before copying it into the
backup folder, so a large archive needs room in both. On Kubernetes, the
chart points the temp folder at the backup volume, so both copies land there
rather than on the node's disk.

A backup restores only into the Innkeeper version that made it. A plugin
database restores only where that plugin is installed with a database; others
are skipped and reported.

Removing a plugin from the image leaves its schema and role. To delete them,
and the plugin's data with them, run this in the Innkeeper database, with
`-` in the plugin's name as `_`. It can't be undone:

```sql
DROP SCHEMA plugin_<name> CASCADE;
DROP OWNED BY "<database>_plugin_<name>";
DROP ROLE "<database>_plugin_<name>";
```
