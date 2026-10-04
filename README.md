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

`innkeeper migrate` applies the schema to the database in
`INNKEEPER_DATABASE_URL`.

`innkeeper worker` runs jobs: imports, bulk actions, and daily blob cleanup.
It migrates the database at startup and stops cleanly on `SIGTERM`.

| Variable | Meaning |
|---|---|
| `INNKEEPER_DATABASE_URL` | PostgreSQL URL |
| `INNKEEPER_BLOB_DIR` | The library's blob store folder |
| `INNKEEPER_PLUGINS_DIR` | Folder with one subfolder per installed plugin |
| `INNKEEPER_WORKER_CONCURRENCY` | Jobs run at once; default 4 |
