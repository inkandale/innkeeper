# Plugin Database Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give each plugin that asks for one a PostgreSQL schema and a login role limited to it, and send the role's DSN as `initialize`'s `database_url`.

**Architecture:** A plugin opts in with `database = true` in its manifest. When the API or worker starts, the core creates or repairs each opted-in plugin's role and schema under an advisory lock. The role's password is derived from `INNKEEPER_SECRETS_KEY`, so every process computes the same DSN without storing it. `Registry.Instance` puts the DSN in `InitializeParams.DatabaseURL`.

**Tech Stack:** Go, pgx, `crypto/hmac`. No new dependencies.

**Spec:** `docs/superpowers/specs/2026-10-03-innkeeper-core-design.md`, "Plugin protocol" → "Lifecycle".

**Kaneo:** INN-14.

---

## Design notes

- **Opt in.** `database = true` in `plugin.toml`. Plugins without it get no role and an empty `database_url`. A plugin that never touches the database shouldn't hold a login to it.
- **Names.** The schema is `plugin_<name>`, with `-` as `_`, as `protocol/manifest.go` already says. The role is `<database>_plugin_<name>`, because roles belong to the whole cluster: two Innkeeper databases on one server, or two test databases, mustn't share a role and reset each other's password. `<database>` comes from `current_database()`. If the role name would pass 63 bytes, startup fails with an error naming the plugin.
- **Password.** HMAC-SHA256 of `"plugin-database:" + role`, keyed by the secrets key, hex-encoded. Add `(*secrets.Box).Derive(label string) string`. Rotating the secrets key rotates the passwords at the next start.
- **Provisioning,** in one transaction under `pg_advisory_xact_lock(hashtext('innkeeper-plugin-db'))`, so the API and worker can start together. Every step is idempotent and runs at every start, which also repairs a restored database:
  - `CREATE ROLE … LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT` if missing, then `ALTER ROLE … PASSWORD …` and `ALTER ROLE … SET search_path = plugin_<name>`.
  - `CREATE SCHEMA IF NOT EXISTS plugin_<name>`, then `ALTER SCHEMA … OWNER TO <role>`.
  - `GRANT ALL ON ALL TABLES, SEQUENCES, FUNCTIONS IN SCHEMA …` to the role, for objects `pg_restore` made under another owner.
  - `GRANT CONNECT ON DATABASE <database>` to the role.
  - Quote every name with `pgx.Identifier.Sanitize`. Pass the password as a literal through `quote_literal` in a `DO` block, or with `format('%L')`, since `ALTER ROLE` can't take parameters.
- **Privileges the core needs.** `CREATEROLE`, plus `CREATE` on the database. The Compose stack's user is a superuser and has both. Without them, startup fails only when an opted-in plugin exists, with an error that says which grant is missing.
- **What the plugin can't reach.** Core tables live in `public` and are never granted to `PUBLIC`, so the role can't read them. Other databases on the cluster keep PostgreSQL's default `CONNECT` for `PUBLIC`; out of scope here, and the README says so.
- **DSN.** Built from `INNKEEPER_DATABASE_URL` with `net/url`: same host, port, database, and query (`sslmode`), with the role's user and password. A key-value DSN can't be rewritten, so startup fails when an opted-in plugin exists and the URL isn't `postgres://`, as `innkeeper backup` already requires.
- **Contract check.** `internal/contract` gets an optional `DatabaseURL` so a plugin repo's CI can pass a test database. Empty means the check doesn't send one.
- **Deleting a plugin.** Removing it from the image leaves its role and schema. Dropping them deletes data, so the owner does it by hand; the README gives the two statements.

## Tasks

### Task 1: Manifest and secret derivation

- [ ] `Database bool \`toml:"database"\`` on `protocol.Manifest`.
- [ ] `(*secrets.Box).Derive(label)`. Test: same key and label give the same value; another key or label gives another.
- [ ] Commit.

### Task 2: Provisioning

- [ ] `internal/plugins/database.go`: `(*Registry).ProvisionDatabases(ctx, dbURL string) error` over the opted-in manifests, storing each plugin's DSN on the registry.
- [ ] Call it from `newServices` in `cmd/innkeeper/services.go`, after `openDB`.
- [ ] `Registry.Instance` sets `Params.DatabaseURL`. Replace the `ponytail:` comment there.
- [ ] Tests, with `testdb`:
  - The DSN connects, and the role can create and use a table in its schema without a schema prefix.
  - The role can't read `card` or create a table in `public`.
  - Running it twice works. After the schema's owner changes to another role, running it again gives the schema back.
  - A plugin without `database = true` gets no role and an empty DSN.
  - A key-value core URL fails only when an opted-in plugin exists.
- [ ] Commit.

### Task 3: Fake plugin and contract check

- [ ] `internal/fakeplugin` declares `database = true` in one manifest and, on a test method, writes and reads a row through `database_url`.
- [ ] A `pluginhost` or `plugins` test runs that fake plugin end to end.
- [ ] `internal/contract`: optional `DatabaseURL`.
- [ ] Commit.

### Task 4: Docs

- [ ] `docs/protocol.md`: `database = true` in "Manifest"; `database_url` is empty unless the manifest asks for one.
- [ ] `README.md`: the core's database user needs `CREATEROLE` when any plugin uses a database; restore repairs plugin schemas at the next start; how to drop a removed plugin's role and schema.
- [ ] Commit.
