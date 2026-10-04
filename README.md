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
- `innkeeper worker` runs jobs: imports, bulk actions, and daily cleanup of
  unused blobs and of jobs that finished over 30 days ago.

| Variable | Used by | Meaning |
|---|---|---|
| `INNKEEPER_DATABASE_URL` | `migrate`, `api`, `worker`, `token`, `backup` | PostgreSQL URL |
| `INNKEEPER_SECRETS_KEY` | `api`, `worker` | Encrypts plugin secrets. Generate with `openssl rand -base64 32`. Losing it loses every stored secret |
| `INNKEEPER_BLOB_DIR` | `api`, `worker`, `backup` | The library's blob store folder |
| `INNKEEPER_PLUGINS_DIR` | `api`, `worker` | Folder with one subfolder per installed plugin |
| `INNKEEPER_LISTEN` | `api` | Listen address; default `:8080` |
| `INNKEEPER_PUBLIC_URL` | `api` | Where browsers reach the API, such as `https://innkeeper.example.com`. Needed for redirect logins. An `http://` URL sends cookies without `Secure` |
| `INNKEEPER_WORKER_CONCURRENCY` | `worker` | Jobs run at once; default 4 |
| `INNKEEPER_URL`, `INNKEEPER_TOKEN` | API commands | API address and token |

## First login

Auth cannot be turned off. No one can log in until a login provider names
an owner, so start with an API token:

```bash
export INNKEEPER_TOKEN=$(innkeeper token create admin)
export INNKEEPER_URL=http://localhost:8080
innkeeper call GET /plugins    # find a plugin with the auth capability
innkeeper call POST /instances '{"plugin": "<auth plugin>", "name": "Login", "owner_subject": "<your subject>"}'
```

`owner_subject` is the identity that provider reports for you; its README
says what that is. Changing or clearing it logs that identity out.

## CLI

```text
innkeeper call <method> <path> [json]           any API call, such as: call GET /jobs
innkeeper import <instance-id> [params-json]    start an import
innkeeper export <card-id> [file]               a card as CCv3 JSON
innkeeper bulk archive|unarchive <query-json>   archive or unarchive the cards a search matches
innkeeper jobs [show|cancel|watch <job-id>]     list or follow jobs
innkeeper token create <name> | list | revoke <id>
innkeeper backup <dir>
```

`bulk` needs a query with a `filter` or `text`; to select every card, filter
on `archived`. It touches only cards not already in the target state.

`jobs watch` exits non-zero unless the job succeeds.

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
read.
