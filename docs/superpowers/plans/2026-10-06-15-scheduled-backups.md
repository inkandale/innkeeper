# Scheduled Backups Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Anyone running Innkeeper can turn on scheduled backups of assets and config, each with its own schedule and retention, download one, and restore it.

**Architecture:** A new `internal/backup` package writes a backup of one type into `INNKEEPER_BACKUP_DIR/<type>/`, streams it as a `.tar.gz` archive, and restores an archive inside one transaction on a dedicated connection. The worker runs backups and restores as jobs and schedules each configured type. The API lists, starts, downloads, and restores backups, and reports backup age on a new health endpoint. `innkeeper backup` and `innkeeper restore` call the same code directly.

**Tech Stack:** Go 1.27, pgx `CopyTo`/`CopyFrom`, `archive/tar`, `compress/gzip`, Huma v2 multipart upload, PostgreSQL 15+. `pg_dump` only for plugin schemas. No new dependencies.

**Spec:** Kaneo INN-20, "Back up the library on a schedule".

---

## Design notes

- **Tables by type.**
  - `assets`, in load order: `blob`, `card`, `card_revision`, `lorebook`, `lorebook_revision`, `card_lorebook`, `tag`, `card_tag`, `collection`, `collection_card`, `card_source`, `lorebook_source`.
  - `config`: `plugin_instance`, `setting`, `binding`, `push`, and every `plugin_%` schema.
  - Never backed up: `job`, `api_token`, `session`, `oauth_*`, `schema_migration`.
- **Only referenced blobs.** The `blob` copy keeps rows a revision's `avatar_blob` points to. Revisions are never deleted, so those files can't vanish mid-backup, and export zips stay out.
- **Go, not `pg_dump`, for core tables.** One `REPEATABLE READ READ ONLY` transaction runs `COPY (SELECT <cols> FROM t) TO STDOUT` per table, in text format. Both ends read the column list from the database, skipping generated columns (`card_revision.search`). Restoring one type into a live database needs per-table control that `pg_restore` can't give inside our transaction.
- **Plugin schemas use `pg_dump`.** Plugins own their DDL, so a config backup runs `pg_dump --format=plain --inserts --rows-per-insert=1000 --no-owner --no-privileges --schema=<schema>` once per `plugin_%` schema, into `plugins/<schema>.sql`, with `--snapshot` set to the core transaction's `pg_export_snapshot()`, so it sees the same moment. Most installs have no plugin schemas and never call `pg_dump`. Restore runs each file as that plugin's role, so an uploaded archive can't run SQL as the core user. Restore needs no `pg_restore`.
- **Layout on disk.**
  - `<dir>/<type>/<type>-<UTC time>/` holds `manifest.json`, `tables/<table>.copy`, and for config `plugins/<schema>.sql`.
  - `<dir>/assets/blobs/ab/cd/<sha256>` is shared by every assets backup and copied incrementally. Each assets manifest lists its blob hashes.
  - A backup is written as `<name>.part/` and renamed when done. The next backup of that type removes leftover `.part` folders, under a per-type advisory lock, so a manual and a scheduled run can't overlap.
- **Manifest.** `{"format": 1, "type", "created_at", "schema": "<last schema_migration name>", "tables": {"<t>": <rows>}, "blobs": [...], "plugin_schemas": [...], "secrets_check": "<box.Derive(\"backup-check\")>"}`. `secrets_check` is set only for config.
- **Archive.** A gzip tar holding `manifest.json`, then `blobs/<sha256>`, then `tables/*.copy`, then `plugins/*.sql`, so restore reads it in one pass. `GET /backups/{name}` and `innkeeper restore <backup-dir>` both build it from a backup folder with `WriteArchive`.
- **Restore,** on its own connection (plugin SQL changes `search_path` for the session), in one transaction:
  1. Read the manifest. Refuse an unknown format or type, or a `schema` other than this database's latest migration ("made by another Innkeeper version"). Before 1.0.0 an archive restores only into the version that wrote it.
  2. Take `pg_advisory_xact_lock(hashtext('innkeeper-restore'))`, then `SET CONSTRAINTS ALL DEFERRED`.
  3. If the type has data (assets: any `card`, `lorebook`, `tag`, or `collection` row; config: any `plugin_instance` or `setting` row), refuse with `ErrHasData` unless `Replace` is set.
  4. With `Replace`, clear the type. For assets: `SET LOCAL innkeeper.restore = 'on'`, which the revision immutability trigger allows, then delete in reverse load order. For config: delete `push`, `binding`, `setting`, `plugin_instance`. A config restore always drops every `plugin_%` schema; the core user is a member of each plugin role, so it may.
  5. Write each blob with `Store.Put` and check that the hash matches its name. Blobs land outside the transaction; a failed restore leaves only unreferenced files, which blob cleanup removes.
  6. `COPY t (<cols>) FROM STDIN` per table. `blob` goes through a temp table with `ON CONFLICT DO NOTHING`, so leftover rows don't collide. `binding` and `push` go into temp tables first, and only bindings whose card or lorebook exists are inserted (`OVERRIDING SYSTEM VALUE`). Pushes follow their bindings. The skipped binding IDs go in the result.
  7. For each `plugins/<schema>.sql`: if the plugin's role exists and the core user is a member, recreate the schema owned by that role and run the file through a `SECURITY DEFINER` function the role owns, which can't switch back to the core user. Otherwise skip it and report it in the result.
  8. Move each restored table's identity sequence past its largest `id`.
  9. For config, bump `plugin_instance.updated_at` so running plugin processes restart with the restored settings. Report `SecretsKeyMismatch` when `secrets_check` differs from this key's.
  10. Commit. A deferred foreign key failure (`23503`) on `binding` becomes: "bindings refer to cards or lorebooks this backup doesn't have; restore a matching config backup or unbind them first".
- **Deferrable foreign keys.** Migration `0013_backup.sql` makes every foreign key in `public` `DEFERRABLE INITIALLY IMMEDIATE`. Normal writes behave as before. Restore defers the checks, which `card` ↔ `card_revision` needs (they reference each other), as does replacing assets while bindings point at cards. It also lets the revision immutability trigger allow changes when `innkeeper.restore` is `on`.
- **Order.** Restore assets before config, or config keeps no bindings. `innkeeper restore` sorts its arguments by type. The README says so for the API.
- **Settings.** `INNKEEPER_BACKUP_DIR`, `INNKEEPER_BACKUP_ASSETS_EVERY`, `INNKEEPER_BACKUP_ASSETS_KEEP`, `INNKEEPER_BACKUP_CONFIG_EVERY`, `INNKEEPER_BACKUP_CONFIG_KEEP`.
  - `EVERY` takes a Go duration or whole days (`7d`).
  - `KEEP` defaults to 7 and must be at least 1.
  - `EVERY` without `DIR` stops the API and worker at startup.
  - `KEEP` without `EVERY` still prunes manual backups of that type.
- **Jobs.** Kinds `backup.assets`, `backup.config`, `backup.restore`. A backup job sets progress `{"name", "size"}`, then prunes past `KEEP`. Pruning also removes shared blobs that no remaining assets manifest lists. A restore job takes `{"path", "replace"}`, deletes the uploaded file when it ends, and on success, after a config restore, calls `Registry.ProvisionDatabases` so plugin roles get their schemas back. A handler with no `INNKEEPER_BACKUP_DIR` fails with a message saying so, rather than leaving the job queued.
- **API.** All `/backups` and `/restores` routes are the destructive tier. Without `INNKEEPER_BACKUP_DIR` they return 409 naming the setting.
  - `GET /backups`: one list, newest first. Finished backups from disk have status `complete`, with name, type, time, and size. Queued, running, and failed backup jobs newer than their type's latest complete backup have status and `job_id`, and `error` when failed.
  - `POST /backups {"type"}`: 202 with the job.
  - `GET /backups/{name}`: streams the archive. The name must match a listed backup, which rules out path tricks.
  - `POST /restores`: multipart, with an `archive` file and an optional `replace` field. It checks the manifest first, so a bad archive is a 400 at once. It saves the file to `<dir>/restores/` and returns 202 with the `backup.restore` job.
- **Health.** `GET /api/health`, public. It returns `{"backups": {"<type>": {"every", "last", "ok"}}}` for each scheduled type. `ok` is a backup younger than twice `EVERY`. Any type not ok makes it 503, so uptime monitors can alert. Backup ages are low-sensitivity, and monitors often can't send tokens. The chart's probes don't use it, because a stale backup mustn't restart the pod.
- **Volume.** The chart runs the API and worker in one pod. Both containers mount the backup volume, since the API serves downloads and saves uploads. Compose mounts `./backups` into both, commented out with the settings.
- **CLI.** `innkeeper backup <dir> [assets|config]` replaces the single `pg_dump`, both types by default. `innkeeper restore [--replace] <archive-or-backup-dir>...` migrates first, restores in type order, and prints skipped bindings and a key-mismatch warning. Both read `INNKEEPER_DATABASE_URL`, `INNKEEPER_BLOB_DIR`, and `INNKEEPER_SECRETS_KEY`.
- **Not atomic across a failed restore's blobs, nor across two archives.** Each archive restores in its own transaction.
- **Out of scope:** off-site copies (S3), restoring by backup name over the API, a web UI page, encryption.

## Files

| File | Change |
|---|---|
| `internal/db/migrations/0013_backup.sql` | Deferrable foreign keys; revision trigger allows restore |
| `internal/backup/backup.go`, `backup_test.go` | New: types, `Config`, `Write`, `List`, `Prune` |
| `internal/backup/pgdump.go`, `pgdump_test.go` | Moved from `cmd/innkeeper/backup.go`: `pgDumpCommand`, now taking schemas and a snapshot |
| `internal/backup/archive.go`, `archive_test.go` | New: `WriteArchive`, `ReadManifest` |
| `internal/backup/restore.go`, `restore_test.go` | New: `Restore` |
| `cmd/innkeeper/backup.go`, `backup_test.go` | `backup` and `restore` commands over `internal/backup` |
| `cmd/innkeeper/main.go` | Usage, `restore` |
| `cmd/innkeeper/worker.go` | Backup and restore handlers, schedules |
| `cmd/innkeeper/api.go`, `services.go` | Read `backup.Settings`, pass it to the server |
| `internal/api/backups.go`, `backups_test.go` | New: `/backups`, `/restores`, `/health` |
| `internal/api/server.go` | `Backups` field, routes |
| `web/openapi.json`, `web/src/api/schema.d.ts` | Regenerated |
| `compose.yaml` | Commented backup settings and mount |
| `chart/values.yaml`, `templates/deployment.yaml`, `templates/pvc.yaml` | Backup values, volume, env |
| `.github/workflows/ci.yml` | PostgreSQL 18 client for `pg_dump`; helm lint with backups on |
| `README.md` | "Backup and restore", settings table |

Go tests need PostgreSQL:

```bash
export INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres
```

The plugin-schema tests also need `pg_dump` 18 on the `PATH`. They skip without it, except in CI, where they fail.

---

### Task 1: Deferrable foreign keys

- [ ] Test in `internal/db/migrations_test.go`: after `Migrate`, no foreign key in `public` has `condeferrable = false`.
- [ ] `0013_backup.sql`:

```sql
-- Restore defers foreign key checks: card and card_revision reference each
-- other, and replacing assets briefly removes cards that bindings point to.
-- New foreign keys must be DEFERRABLE too.
DO $$
DECLARE r record;
BEGIN
    FOR r IN SELECT conrelid::regclass AS tbl, conname FROM pg_constraint
        WHERE contype = 'f' AND connamespace = 'public'::regnamespace AND NOT condeferrable
    LOOP
        EXECUTE format('ALTER TABLE %s ALTER CONSTRAINT %I DEFERRABLE INITIALLY IMMEDIATE', r.tbl, r.conname);
    END LOOP;
END $$;
```

- [ ] `go test ./internal/...` passes.
- [ ] Commit `feat(db): make foreign keys deferrable for restore`.

### Task 2: Settings

- [ ] `internal/backup/backup.go`: `type Type string`, `Assets`, `Config`, `Types = []Type{Assets, Config}`, and:

```go
// Settings is the backup settings from the environment.
type Settings struct {
	Dir   string
	Every map[Type]time.Duration // only scheduled types
	Keep  map[Type]int           // every type with a schedule or a KEEP
}

func SettingsFromEnv() (Settings, error)
```

- [ ] Table test: nothing set gives an empty config; `EVERY=24h` and `EVERY=7d` parse; `EVERY` without `DIR` fails; `KEEP=0`, `KEEP=x`, and `EVERY=-1h` fail; `KEEP` defaults to 7 when `EVERY` is set.
- [ ] Commit `feat(backup): read backup settings`.

### Task 3: Write, list, prune

- [ ] Move `pgDumpCommand` and its tests to `internal/backup/pgdump.go`. Its signature becomes `pgDumpCommand(dbURL string, out io.Writer, args ...string)`, keeping the password-in-environment behavior.
- [ ] `backup.go`:

```go
type Options struct {
	Dir         string
	DatabaseURL string // for pg_dump of plugin schemas
	Store       *blobstore.Store
	Box         *secrets.Box // nil skips secrets_check
}

// Info describes one finished backup.
type Info struct {
	Name      string    `json:"name"`
	Type      Type      `json:"type"`
	CreatedAt time.Time `json:"created_at"`
	Size      int64     `json:"size"` // folder plus the shared blobs it lists
}

func Write(ctx context.Context, pool *pgxpool.Pool, t Type, o Options) (Info, error)
func List(dir string) ([]Info, error)                  // newest first; skips .part
func Prune(dir string, t Type, keep int) (int, error)  // removed count; then unreferenced shared blobs
```

- [ ] `Write` follows the design notes: per-type advisory lock in the snapshot transaction, remove stale `.part` folders, `COPY` each table, copy listed blobs into the shared store (skipping ones already there), run `pg_dump` with `--snapshot` when plugin schemas exist, write the manifest last, then rename. Files `0600`, folders `0700`.
- [ ] Tests (`testdb`):
  - An assets backup of a seeded library has the manifest, one `.copy` per table with the right row counts, and the avatar under `assets/blobs`. A second backup copies no new blobs.
  - A config backup has no `blobs/` and sets `secrets_check`. With a `plugin_fake` schema holding a table and a row, `plugins/plugin_fake.sql` creates and fills it (needs `pg_dump`).
  - A failing `pg_dump` (`PATH` pointed at an empty folder) leaves only the `.part` folder, which the next run removes.
  - `List` skips `.part` folders and sorts newest first. `Prune(dir, Assets, 1)` keeps the newest and removes blobs only older backups listed.
- [ ] Commit `feat(backup): write, list, and prune backups by type`.

### Task 4: Archive

- [ ] `archive.go`:

```go
// WriteArchive streams backup name in dir as a gzip tar: manifest.json,
// blobs/, tables/, then plugins/.
func WriteArchive(w io.Writer, dir, name string) error

// ReadManifest reads an archive's first entry, which must be manifest.json.
func ReadManifest(r io.Reader) (Manifest, error)
```

- [ ] Tests: entry order; blob entries match the manifest's list; `ReadManifest` rejects a non-gzip file, a tar not starting with `manifest.json`, and `format: 2`.
- [ ] Commit `feat(backup): stream a backup as one archive`.

### Task 5: Restore

- [ ] `restore.go`:

```go
var ErrHasData = errors.New("this database already has data of that type; restore with replace to overwrite it")

type RestoreOptions struct {
	Conn    *pgx.ConnConfig // restore opens its own connection
	Store   *blobstore.Store
	Box     *secrets.Box
	Replace bool
}

type Result struct {
	Type               Type             `json:"type"`
	Rows               map[string]int64 `json:"rows"`
	Blobs              int              `json:"blobs"`
	SkippedBindings    []int64          `json:"skipped_bindings,omitempty"`
	SecretsKeyMismatch bool             `json:"secrets_key_mismatch,omitempty"`
}

func Restore(ctx context.Context, r io.Reader, o RestoreOptions) (Result, error)
```

- [ ] Implement the ten steps in the design notes.
- [ ] Tests (`testdb`; seed one source database with a card with an avatar, a lorebook linked to it, a tag, a collection, a source, an instance with a sealed secret, a card binding with a push, a setting, and a `plugin_fake` table with a row):
  - **Assets alone** into a fresh database: the card's head revision, its avatar file, the lorebook link, tag, collection, and source are there. A new card gets an ID above the restored ones.
  - **Config alone** into a fresh database: the instance's secret opens with the same key, the plugin table row is there, and the binding is in `SkippedBindings`.
  - **Both** (assets, then config) into a fresh database: the binding is kept and `binding_status` shows it `in_sync`.
  - Restoring over data without `Replace` gives `ErrHasData` and changes nothing.
  - `Replace` on assets swaps the library for the archive's.
  - `Replace` on assets when a binding points at a card missing from the archive fails with the binding message, and the database is unchanged.
  - Another secrets key sets `SecretsKeyMismatch`.
  - A manifest whose `schema` differs from the database's is refused before anything is written.
  - Plugin-schema cases skip without `pg_dump`, except in CI.
- [ ] Commit `feat(backup): restore an archive of one type`.

### Task 6: CLI

- [ ] `cmd/innkeeper/backup.go`: `backup <dir> [assets|config]` over `backup.Write`; `restore [--replace] <path>...`. A folder path is turned into an archive through `io.Pipe` and `WriteArchive`. Archives restore assets first, using `ReadManifest`'s type. Print each `Result`, skipped binding IDs, and a warning on `SecretsKeyMismatch`.
- [ ] `main.go`: usage lines and the `restore` case.
- [ ] Tests: argument errors; type sorting puts assets first.
- [ ] Commit `feat(cli): back up by type and restore archives`.

### Task 7: Worker jobs and schedules

- [ ] `services.go` reads `backup.SettingsFromEnv()` into `services.backups`, so the API and worker both fail early on bad settings.
- [ ] `worker.go`: handlers for `backup.assets`, `backup.config`, `backup.restore`, and a `jobs.Schedule` for each type in `Settings.Every`. Export the kind names from `internal/backup` (`backup.Kind(t)`, `backup.RestoreKind`).
- [ ] Test in `internal/backup`: `Handler(cfg, …)` writes a backup and prunes to `KEEP`; with no `Dir` it fails with the setting's name.
- [ ] Commit `feat(worker): run scheduled backups and restores`.

### Task 8: API

- [ ] `internal/api/backups.go`: the routes from the design notes, with `needs(tier.Destructive)` on all but `/health`, which is `public` with `noAuth` security. Add `Backups backup.Settings` to `Server` and register `s.backupRoutes(api)`.
- [ ] `POST /restores` input:

```go
type restoreIn struct {
	RawBody huma.MultipartFormFiles[struct {
		Archive huma.FormFile `form:"archive" required:"true"`
		Replace bool          `form:"replace"`
	}]
}
```

- [ ] Tests (`api_test.go` helpers): a write-tier token gets 403 on each route; with no dir, 409; `POST /backups` queues `backup.assets`; `GET /backups` merges a backup folder with a failed job; `GET /backups/../x` is 404; download returns a gzip whose manifest matches; upload of a non-archive is 400; upload of a good archive queues `backup.restore` with the file under `restores/`; `/health` is 200 with a fresh backup, 503 with a stale or missing one, and `{}` with nothing scheduled.
- [ ] `npm run gen` in `web/`, commit the regenerated files.
- [ ] Commit `feat(api): list, start, download, and restore backups`.

### Task 9: Compose, chart, CI

- [ ] `compose.yaml`: in `x-innkeeper`, commented `INNKEEPER_BACKUP_DIR: /backups`, the four schedule settings, and `- ./backups:/backups`.
- [ ] Chart values:

```yaml
# Scheduled backups; see "Backup and restore" in the README. An empty
# `every` turns that type's schedule off.
backup:
  assets:
    every: ""
    keep: 7
  config:
    every: ""
    keep: 7
  persistence:
    enabled: false
    size: 10Gi
    storageClass: ""
    accessModes: [ReadWriteOncePod]
```

  With `persistence.enabled`, add a `<release>-backups` PVC, mount it at `/backups` in both containers, and set `INNKEEPER_BACKUP_DIR`. Set each type's `EVERY` and `KEEP` only when `every` is set. A non-empty `every` without persistence fails the render.
- [ ] CI: install `postgresql-client-18` from the PGDG apt repo before `go test`, and put `/usr/lib/postgresql/18/bin` on the `PATH`. Add a second `helm lint` with backups on.
- [ ] `helm template` with and without backups renders as expected.
- [ ] Commit `feat(deploy): backup volume and schedules in Compose and the chart`.

### Task 10: README

- [ ] Settings table: the five backup variables and the `restore` command.
- [ ] Rewrite "Backup and restore": what each type holds and what's never backed up; turning on schedules in Compose (with `mkdir backups` and owner `10001` on Linux) and the chart; downloading; restoring with the CLI and API, assets before config; `--replace`; keep `INNKEEPER_SECRETS_KEY` with config backups; make a new token after a restore with `innkeeper token create`; `/api/health` for monitoring.
- [ ] Commit `docs: scheduled backups and restore`.
