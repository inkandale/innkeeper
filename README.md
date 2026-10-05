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

## Running

One binary runs both services and the CLI. Both services migrate the
database at startup and stop cleanly on `SIGTERM`.

- `innkeeper api` serves the HTTP API. The OpenAPI spec is at
  `/openapi.json`, and browsable docs at `/docs`. Job progress streams from
  `GET /jobs/{id}/events`, which sends a `: ping` comment every 15 seconds
  while idle.
- `innkeeper worker` runs jobs: imports, bulk actions, pushes, an hourly
  check of target apps for changes, and daily cleanup of unused blobs and of
  jobs that finished over 30 days ago.

| Variable | Used by | Meaning |
|---|---|---|
| `INNKEEPER_DATABASE_URL` | `migrate`, `api`, `worker`, `token`, `backup` | PostgreSQL URL |
| `INNKEEPER_SECRETS_KEY` | `api`, `worker` | Encrypts plugin secrets. Generate with `openssl rand -base64 32`. Losing it loses every stored secret |
| `INNKEEPER_BLOB_DIR` | `api`, `worker`, `backup` | The library's blob store folder |
| `INNKEEPER_PLUGINS_DIR` | `api`, `worker` | Folder with one subfolder per installed plugin |
| `INNKEEPER_LISTEN` | `api` | Listen address; default `:8080` |
| `INNKEEPER_PUBLIC_URL` | `api` | Where browsers reach the API, such as `https://innkeeper.example.com`. Needed for redirect logins and for MCP clients that connect through OAuth, such as claude.ai. An `http://` URL sends cookies without `Secure` |
| `INNKEEPER_WORKER_CONCURRENCY` | `worker` | Jobs run at once; default 4 |
| `INNKEEPER_URL`, `INNKEEPER_TOKEN` | API commands | API address and token |

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
curl -H "Authorization: Bearer $INNKEEPER_TOKEN" "$INNKEEPER_URL/tags"
```

If something else already listens on port 8080, set `INNKEEPER_PORT` in
`.env`. Docker doesn't report the clash; requests just reach the other
program.

The image includes the plugins from `innkeeper-plugins` at the branch or
commit in the `PLUGINS_REF` build argument, `main` by default. A rebuild
reuses its cached clone of `main`; to bundle newer plugins, pass a commit with
`--build-arg PLUGINS_REF=<commit>` or build with `--no-cache`. Releases from CI
always build `main`'s latest commit. Building clones that private
repo, so it needs an SSH agent holding a key that can read it
(`ssh-add -l`). For a key that isn't in the agent, build with
`docker build --ssh default=$HOME/.ssh/<key> -t innkeeper:dev .`, then start
with `docker compose up -d --no-build`.

`docker compose down -v` deletes the database and blobs.

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
the blob volume. It creates no Secrets; point it at yours:

```bash
helm install innkeeper oci://ghcr.io/davidgibbons/charts/innkeeper --version <version> \
  --set database.secretName=innkeeper-db --set database.key=uri \
  --set secretsKey.secretName=innkeeper --set secretsKey.key=INNKEEPER_SECRETS_KEY \
  --set publicUrl=https://innkeeper.example.com \
  --set ingress.enabled=true --set ingress.host=innkeeper.example.com
```

`values.yaml` lists the rest: storage, resources, `extraEnv`, and pod
annotations for a backup tool. Back up the blob volume along with the
database. Create the first token with
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
read -rs PW && printf '%s\n' "$PW" | docker compose exec -T api /plugins/password-login/password-login hash; unset PW
innkeeper call POST /instances '{"plugin": "password-login", "name": "Login", "owner_subject": "owner", "secrets": {"password_hash": "<hash>"}}'
```

For another login provider, `owner_subject` is the identity it reports for
you; its README says what that is. Changing or clearing `owner_subject` logs
that identity out.

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

```text
innkeeper call <method> <path> [json]           any API call, such as: call GET /jobs
innkeeper import <instance-id> [params-json]    start an import
innkeeper export <card-id> [file [format]]      a card as CCv3 JSON, or a codec's format (.png means png)
innkeeper bulk archive|unarchive <query-json>   archive or unarchive the cards a search matches
innkeeper push <instance-id> <card-id>|<query-json> [--new-copy]
                                                push one card, or the cards a search matches, to a target
innkeeper jobs [show|cancel|watch <job-id>]     list or follow jobs
innkeeper token create <name> [tier] | list | revoke <id> | revoke-grant <id>
innkeeper backup <dir>
```

`bulk` needs a query with a `filter` or `text`; to select every card, filter
on `archived`. It touches only cards not already in the target state.

`jobs watch` exits non-zero unless the job succeeds.

`push --new-copy` is for apps that can't update a card: pushing there again
makes another copy, so the push fails without the flag.

Exporting in a format other than `json` needs an instance of a codec plugin
that writes it. Export uses the oldest working codec instance for the format:

```bash
innkeeper call POST /instances '{"plugin": "card-codec", "name": "Codec"}'
innkeeper export 12 brakka.png
```

## Importing

The bundled `card-import` plugin reads CCv3, V2, and V1 cards as PNG or
JSON, and lorebooks as CCv3 or SillyTavern world info JSON. Importing the
same file again adds nothing.

To import a folder, put it under `./import`, or set `INNKEEPER_IMPORT_DIR`
in `.env`. Compose mounts it read-only at `/import`:

```bash
innkeeper call POST /instances '{"plugin": "card-import", "name": "Import", "config": {"folder": "/import"}}'
innkeeper import <instance-id>                         # everything in /import
innkeeper import <instance-id> '{"path": "fantasy"}'   # only /import/fantasy
```

To import one file, upload it, then import the `sha256` from the reply
within 24 hours:

```bash
curl -sf -H "Authorization: Bearer $INNKEEPER_TOKEN" -H 'Content-Type: application/octet-stream' \
  --data-binary @brakka.png "$INNKEEPER_URL/blobs"
innkeeper import <instance-id> '{"blob": "<sha256>", "name": "brakka.png"}'
```

## Sync

The image ships a target for [Lumiverse](https://github.com/prolix-oc/Lumiverse).
Add one per Lumiverse account; its
[README](https://github.com/davidgibbons/innkeeper-plugins/tree/main/lumiverse)
covers what it syncs:

```bash
innkeeper call POST /instances '{"plugin": "lumiverse", "name": "Lumiverse",
  "config": {"url": "https://lumiverse.example.com", "username": "owner"},
  "secrets": {"password": "<password>"}}'
innkeeper call POST /cards/<id>/push '{"instance_id": <instance-id>}'
```

It also ships a target for [SillyTavern](https://github.com/SillyTavern/SillyTavern).
SillyTavern accepts only localhost by default, so first add Innkeeper's
address to its whitelist, or turn the whitelist off and use a login; the
[README](https://github.com/davidgibbons/innkeeper-plugins/tree/main/sillytavern)
covers that and what it syncs. Set `auth` to `none`, `basic`, or `account` to
match the server:

```bash
innkeeper call POST /instances '{"plugin": "sillytavern", "name": "SillyTavern",
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
innkeeper call GET  /instances/<instance-id>/items
innkeeper call POST /cards/<id>/bind '{"instance_id": <instance-id>, "remote_id": "<remote-id>"}'
```

Binding changes nothing in the app or the library. The library's card
becomes what the app is compared against, so the binding is `in_sync` if the
app's copy matches and `remote_changed` if it doesn't. Adopt the changes or
push over them as above. An item already bound shows its `binding_id`, and
binding it again is refused with `409`. In the push history, a bind shows
as `"kind": "bind"`.

For lorebooks in an app that keeps them separately, use
`/instances/<instance-id>/lorebook-items` and `/lorebooks/<id>/bind`. A bound
card is `ahead` when the app links it to lorebooks other than the library's
bound ones, or when the plugin can't tell which it links. The next push links
the library's.

### Importing what the app already has

To take an app's whole library into Innkeeper, import from its instance:

```bash
innkeeper import <instance-id>                                  # every unbound character and lorebook
innkeeper import <instance-id> '{"remote_ids": ["<remote-id>"]}' # only these items
```

Each item becomes a library card or lorebook bound to the app's copy, so it
starts `in_sync`. Cards keep the lorebooks the app links them to, and their
avatars where the plugin supplies them. The job runs one child per item and
resumes after a restart. `innkeeper jobs show <job-id>` counts the children
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

### Lorebooks in apps that keep them separately

Some apps, such as Lumiverse, keep lorebooks apart from cards. Lorebooks then
sync on their own, with their own bindings, statuses, checks, and adopts:

```bash
innkeeper call GET  '/lorebooks/<id>/push-preview?instance_id=<instance-id>'
innkeeper call POST /lorebooks/<id>/push '{"instance_id": <instance-id>}'
innkeeper call GET  /lorebooks/<id>/bindings
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

`innkeeper backup <dir>` writes a `pg_dump` file named
`innkeeper-<UTC time>.dump` and copies the blob files `<dir>/blobs` doesn't
have yet. It needs `pg_dump` on the `PATH` and a `postgres://`
`INNKEEPER_DATABASE_URL`. Run it on a schedule; it never overwrites or
deletes an earlier dump. A dump still ending in `.part` is unfinished. The
directory and every file it writes are readable only by their owner.

To restore, stop both services, point `INNKEEPER_DATABASE_URL` at a new,
empty database, then:

```bash
pg_restore --dbname="$INNKEEPER_DATABASE_URL" <dir>/innkeeper-<time>.dump
cp -Rn <dir>/blobs/. "$INNKEEPER_BLOB_DIR"/
```

Restore with the same `INNKEEPER_SECRETS_KEY`, or plugin secrets can't be
read. The next start gives each plugin's schema, and everything in it, back to
its role.

Removing a plugin from the image leaves its schema and role. To delete them,
and the plugin's data with them, run this in the Innkeeper database, with
`-` in the plugin's name as `_`. It can't be undone:

```sql
DROP SCHEMA plugin_<name> CASCADE;
DROP OWNED BY "<database>_plugin_<name>";
DROP ROLE "<database>_plugin_<name>";
```
