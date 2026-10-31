# Running Innkeeper

## Commands and settings

One binary runs both services and the CLI. Both services migrate the
database at startup and stop cleanly on `SIGTERM`.

- `innkeeper api` serves the HTTP API under `/api` and the
  [web UI](using.md#web-ui) at the root. The OpenAPI spec is at `/api/openapi.json`,
  and browsable docs at `/api/docs`. Job progress streams from
  `GET /api/jobs/{id}/events`, which sends a `: ping` comment every 15 seconds
  while idle.
- `innkeeper worker` runs jobs: imports, bulk actions, pushes, an hourly
  check of target apps for changes, [scheduled backups](#backup-and-restore),
  and daily cleanup of unused blobs and of finished jobs and their logs
  older than `INNKEEPER_JOB_RETENTION`.
- `innkeeper serve` runs both in one process, and reads the settings of
  both. It is the image's default command and what Compose runs. If either
  half fails, the process exits.

| Variable | Used by | Meaning |
|---|---|---|
| `INNKEEPER_DATABASE_URL` | `migrate`, `api`, `worker`, `token`, `backup`, `restore` | PostgreSQL URL |
| `INNKEEPER_SECRETS_KEY` | `api`, `worker`, `backup`, `restore` | Encrypts plugin secrets. Generate with `openssl rand -base64 32`. Losing it loses every stored secret |
| `INNKEEPER_SECRETS_KEY_FILE` | `api`, `worker`, `backup`, `restore` | Where the key lives when `INNKEEPER_SECRETS_KEY` is unset; the first start creates it. `/data/secrets.key` in the image |
| `INNKEEPER_BLOB_DIR` | `api`, `worker`, `backup`, `restore` | The library's blob store folder |
| `INNKEEPER_PLUGINS_DIR` | `api`, `worker`, `restore` | Writable folder that plugins install into; `/data/plugins` in the image. The API and worker must share it |
| `INNKEEPER_BUILTIN_PLUGINS_DIR` | `api`, `worker`, `restore` | Read-only folder of plugins that ship with Innkeeper, one subfolder each; an installed plugin of the same name overrides one |
| `INNKEEPER_LISTEN` | `api` | Listen address; default `:8080` |
| `INNKEEPER_PUBLIC_URL` | `api` | Where browsers reach the API, such as `https://innkeeper.example.com`. Needed for redirect logins, `ink login`, and MCP clients that connect through OAuth, such as claude.ai. An `http://` URL sends cookies without `Secure`. Unset, cookies get `Secure` only over https or behind a proxy that sends `X-Forwarded-Proto: https` |
| `INNKEEPER_OWNER_PASSWORD_HASH` | `api` | A bcrypt hash from `password-login hash`. On a server with no login provider, the API creates the owner's password login from it at startup. Ignored once the server is claimed |
| `INNKEEPER_WORKER_CONCURRENCY` | `worker` | Jobs run at once; default 4 |
| `INNKEEPER_METRICS_LISTEN` | `worker` | Address to serve Prometheus metrics on at `/metrics`, such as `:9090`: `innkeeper_job_duration_seconds{kind,status}`, `innkeeper_cards_changed_total{kind}`, and Go runtime metrics. Unset serves none |
| `INNKEEPER_BACKUP_DIR` | `api`, `worker` | Where backups go. Needed for any backup or restore through the API, and for schedules |
| `INNKEEPER_BACKUP_ASSETS_EVERY`, `INNKEEPER_BACKUP_CONFIG_EVERY` | `api`, `worker` | How often to back up that type, such as `24h` or `7d`. Unset means no schedule |
| `INNKEEPER_SYNC_EVERY` | `worker` | How often to sync every app, such as `30m` or `6h`. `0` turns the schedule off. Default `1h` |
| `INNKEEPER_SOURCE_CHECK_EVERY` | `worker` | How often to check the sites cards and lorebooks came from for updates, such as `12h`. `0` turns the schedule off. Default `24h` |
| `INNKEEPER_JOB_RETENTION` | `worker` | How long finished jobs and their logs stay, such as `72h`. `0` keeps them forever. Default `168h` (7 days) |
| `INNKEEPER_BACKUP_ASSETS_KEEP`, `INNKEEPER_BACKUP_CONFIG_KEEP` | `api`, `worker` | How many backups of that type to keep; default 7 |
| `INNKEEPER_URL`, `INNKEEPER_TOKEN` | `ink` | The site root, such as `http://localhost:8080`, and a token. When `INNKEEPER_TOKEN` is set, `ink` uses these instead of a login from `ink login`. The CLI adds `/api` itself |

Innkeeper needs PostgreSQL 15 or later. pgvector is optional: with it,
similar cards rank by meaning and semantic search works; without it,
similar cards rank by shared tags. At startup Innkeeper runs
`CREATE EXTENSION IF NOT EXISTS vector`, which needs a superuser. If
pgvector isn't installed or the role can't create the extension, Innkeeper
logs a warning and runs without vector features. To turn them on, install
pgvector, or have an admin run `CREATE EXTENSION vector`, then restart.

If any installed plugin sets `database = true`, the API and worker give it a
schema and a login role limited to it at startup. That needs PostgreSQL 15 or
later, where other roles can't create tables in `public`, and a database user
with `CREATEROLE` and `CREATE` on the database. The Compose stack's user is a
superuser.

## Docker Compose

`compose.yaml`, from the [Quick start](../README.md#quick-start), runs PostgreSQL and
`innkeeper serve` from the released image. If you used an earlier
`compose.yaml` with a separate `worker` service, run
`docker compose up -d --remove-orphans` once to remove it. The port listens on this
machine only; to
reach it from other devices, change `127.0.0.1:` in the `api` service's
`ports`.

If something else already listens on port 8080, set `INNKEEPER_PORT` in
`.env`. Docker doesn't report the clash; requests just reach the other
program.

In a checkout, Compose also reads `compose.override.yaml`, which builds the
local source: run `docker compose up -d --build`. The image builds its three built-in plugins, `password-login`,
`card-codec`, and `url-import`, from `innkeeper-plugins` at the branch or commit in the
`PLUGINS_REF` build argument, `main` by default. A rebuild reuses its cached
clone of `main`; pass `--build-arg PLUGINS_REF=<commit>` or build with
`--no-cache` for newer ones. If a built-in plugin needs a newer core than the
checkout, Innkeeper won't start and logs which one; update the checkout or
pass an older `PLUGINS_REF`. Install other plugins at runtime; see
[Plugins](#plugins).

Compose keeps blobs, installed plugins, and the secrets key in one volume at
`/data`. `docker compose down -v` deletes the database and that volume.

## Kubernetes

Each release publishes the image `ghcr.io/inkandale/innkeeper` as
`:<version>` and `:<major>.<minor>`, for amd64 and arm64, and the Helm chart
to `oci://ghcr.io/inkandale/charts/innkeeper`. The chart runs the image
matching its `appVersion` unless `image.tag` says otherwise. `:latest`
follows the `develop` branch; to test it, set `image.tag=latest` and
`image.pullPolicy=Always`, and restart the deployment to pick up a new build.

The chart runs the API and the worker as two containers in one pod, sharing
the blob volume and a plugins volume (`persistence.plugins.size`, 1Gi). It creates no Secrets; point it at yours:

```bash
helm install innkeeper oci://ghcr.io/inkandale/charts/innkeeper --version <version> \
  --set database.secretName=innkeeper-db --set database.key=uri \
  --set secretsKey.secretName=innkeeper --set secretsKey.key=INNKEEPER_SECRETS_KEY \
  --set publicUrl=https://innkeeper.example.com \
  --set ingress.enabled=true --set ingress.host=innkeeper.example.com
```

`values.yaml` lists the rest: storage, resources, `extraEnv`, pod
annotations, and [scheduled backups](#backup-and-restore). `extraVolumes` and
`extraVolumeMounts` mount folders that a plugin reads, such as an import's
dump, into both containers. To keep blobs elsewhere, such as on a NAS share,
set `persistence.blobs.enabled=false`, mount the share with those two, and set
`INNKEEPER_BLOB_DIR` in `extraEnv`; the chart then creates no blob volume, and
refuses to render without `INNKEEPER_BLOB_DIR`.

`worker.metrics.enabled=true` serves the worker's
[job metrics](#commands-and-settings) on port 9090 and adds a PodMonitor, which needs
the Prometheus Operator CRDs.

> **Warning:** on an existing release, `persistence.blobs.enabled=false`
> deletes the blobs PVC and, with most storage classes, every blob in it.
> Scale the deployment to 0 and copy the blob directory to the new location
> first.

Create the first token with
`kubectl exec deploy/innkeeper -c api -- innkeeper token create admin destructive`.

## First login

Auth cannot be turned off. A new server has no login provider, so the first
step is to claim it. While it is unclaimed, the API logs a setup code:

```bash
docker compose logs api | grep "setup code"
```

On Kubernetes, `kubectl logs deploy/innkeeper -c api` shows the same line.
Open the site, which sends you to `/setup`, and enter the code with a
password. This creates a login with the bundled `password-login` plugin and
opens the setup wizard (see [Web UI](using.md#web-ui)).
The code changes each time the API restarts. Add other login providers, such
as OIDC, in Settings after setup.

To claim the server from config instead, set `INNKEEPER_OWNER_PASSWORD_HASH`
to a hash made by the plugin and restart the API. It is ignored once the
server is claimed. With Compose, uncomment its line in `compose.yaml` and
put the hash in `.env` in single quotes, so Compose leaves its `$` signs alone.

```bash
read -rs PW && printf '%s\n' "$PW" | docker compose exec -T api /usr/lib/innkeeper/plugins/password-login/password-login hash; unset PW
```

To claim with another login provider, create its instance with an API token
(`innkeeper token create admin destructive`) and set `owner_subject` to the
identity it reports for you; its README says what that is. Changing or
clearing `owner_subject` logs that identity out.

## Plugins

Innkeeper ships `password-login`, `card-codec`, and `url-import`. Install other plugins
from a source, an `index.toml` that lists plugin versions. The official
`innkeeper-plugins` source is there by default:

```bash
ink plugins catalog
ink plugins install card-import          # the newest version
ink plugins install card-import 0.1.0
```

Installing a newer version updates the plugin; jobs already running finish
on the old one. Innkeeper keeps the previous version, so installing it again
rolls back without a download.

Add another source by its `https://` or `file://` URL. Its plugins run as
code inside Innkeeper with their own database role, so add only sources you
trust. To publish one, see
[Publishing a source](protocol.md#publishing-a-source).

```bash
ink plugins sources add mine https://example.com/index.toml
```

`ink plugins remove <name>` uninstalls a plugin but keeps its instances and
database, which come back if you install it again. To delete those too, run
`ink plugins purge <name>` afterwards. That can't be undone.

## Backup and restore

Innkeeper backs up two types separately, each with its own schedule and
retention:

| Type | Holds |
|---|---|
| `assets` | Libraries and their setting overrides; cards, lorebooks, and their revisions; avatars; collections, sources, and lorebook links |
| `config` | Plugin instances with their settings and encrypted secrets; plugin databases; bindings and push history; core settings |

Jobs, API tokens, logins, and OAuth grants aren't backed up. After a restore,
make a new token with `innkeeper token create`.

### Scheduled backups

Scheduled backups are off until `INNKEEPER_BACKUP_DIR` and at least one
`_EVERY` setting are set (see [Commands and settings](#commands-and-settings)). The worker writes each
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

A failed backup shows as a failed job and in `GET /api/backups`. A backup
that's written but not pruned still succeeds; the worker logs a warning and
the next run prunes again.
`GET /api/health` needs no token and reports when each scheduled type last
backed up. It answers 503 once a type's latest backup is older than two
intervals, for an uptime monitor to watch. Don't use it as a liveness probe:
a stale backup shouldn't restart the pod.

### Backing up now and downloading

These need a destructive token, since an assets backup holds every card and a
config backup holds the encrypted secrets:

```bash
ink backups create assets
ink backups
ink backups download <name> assets.tar.gz
```

A download is one unencrypted `.tar.gz`. Keep the secrets key with config
backups, stored separately: without it, restored plugin secrets can't be read.
Backups never include it. Unless you set `INNKEEPER_SECRETS_KEY`, it is the
file `INNKEEPER_SECRETS_KEY_FILE` names; copy it with
`docker compose cp api:/data/secrets.key .`.

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

`ink` uploads one archive at a time to the API, which restores it as a job:

```bash
ink backups restore assets.tar.gz
ink backups restore config.tar.gz --replace
ink backups restore assets.tar.gz --yes   # in a script: skip the question
```

The API writes an upload straight into `restores/` in the backup folder, so a
large archive needs room only there. The daily `blob.gc` job removes uploads
left behind when a restore job is cancelled before it runs.

Replacing config deletes the existing plugin instances, so queued and
finished jobs that named one lose the link: their `instance_id` becomes null.

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
