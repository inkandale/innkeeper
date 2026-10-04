# HTTP API, Auth, and CLI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `innkeeper api`: a REST API described by one OpenAPI spec, with SSE job progress, `POST /search`, login through `auth` plugins, API tokens, and encrypted instance secrets. Add the CLI commands that use it.

**Architecture:** `internal/api` registers Huma operations on a standard `http.ServeMux`. One middleware enforces auth on every operation not marked public. `internal/auth` stores API tokens and sessions as SHA-256 hashes. `internal/secrets` seals instance secrets with AES-256-GCM. Search is a SQL builder in `internal/library` over a generated `tsvector` column. The CLI is a thin HTTP client, except `token` and `backup`, which work on the database directly.

**Tech Stack:** Go 1.27, `github.com/jackc/pgx/v5` v5.11.0, PostgreSQL 18, and one new dependency: `github.com/danielgtaylor/huma/v2` v2.39.1 with its `humago` adapter (standard library router) and `sse` package.

**Spec:** `docs/superpowers/specs/2026-10-03-innkeeper-core-design.md`, sections "HTTP API", "CLI", "Auth", "Search and enrichment", "Sync" (`plugin_instance` secrets), and "Error handling".

**Kaneo:** INN-4.

---

## Scope

In this plan:

- `innkeeper api` with routes for cards, lorebooks, revisions, restore, tags, collections, lorebook links, blobs, JSON export, search, plugin instances, jobs, imports, and bulk archive.
- SSE for job progress. AI action streams come with sub-project 4.
- `POST /search` over the library: full-text `text`, `filter` with `and`, `or`, `not`, `tag`, `collection`, and `archived`, plus `include`, `fields`, and paging.
- Login through `auth` plugins, sessions, and API tokens. Auth cannot be turned off.
- Encrypted instance secrets, instance editing, and restarting plugins whose settings changed.
- Job cancelling and deletion of old finished jobs, plus the INN-3 review fix for the child-job scan.
- CLI: `api`, `call`, `import`, `export`, `bulk`, `jobs`, `token`, and `backup`.

Left out, each for the plan that needs it:

| Left out | Why | Where |
|---|---|---|
| `push` command, `target_state` filter | Bindings and pushes don't exist yet | Plan 5 |
| PNG export | Needs a `codec` plugin; `export` writes CCv3 JSON | Plan 6 |
| `semantic` search, `enrichment` filter, the `enrichment` and `card_embedding` tables | Nothing writes enrichments or embeddings until an enricher plugin exists. `semantic: true` returns `400` | Enricher plugin plan |
| `sources` other than `library` | Needs the `search` capability, which the archive catalog defines | Archive catalog |
| Saved searches | Only the UI uses them | Plan 7 |

## Plan series

This is plan 4 of the core. See plan 1 for the full list.

Constraints this plan leaves for later plans:

- **Plan 5:**
  - Pushes and drift scans are new job kinds. Add `target_state` to `library.Filter` and a `push` CLI command.
  - Deleting a plugin instance sets its jobs' `instance_id` to null. Bindings need their own rule: probably refuse to delete an instance that has bindings.
- **Plan 6:**
  - The local password plugin implements `auth.begin` and `auth.complete` as `docs/protocol.md` describes. It keeps the password hash in an instance secret, and its subject is a fixed string such as `owner`, which the owner sets as the instance's `owner_subject`.
  - Uploads: the client sends the file to `POST /blobs`, then starts an import whose params name the blob's sha256. The importer reads the file at `<blob_stores.library>/ab/cd/<sha256>`. Blob GC keeps unreferenced blobs for 24 hours, so the import must start within that window.
  - When a `codec` plugin exists, `GET /cards/{id}/export` can take a `format`.
- **Plan 7:**
  - The web UI is served from the same origin as the API and logs in with cookies. There is no CORS.
  - `GET /jobs/{id}/events` polls the database once a second per watcher.
  - The MCP server authenticates with an API token and sends `source: "mcp"` on writes.
- **Container image:** include `pg_dump` for `innkeeper backup`, and set `INNKEEPER_SECRETS_KEY` for both services.

## Design notes

- **Owner.** An auth instance names the subject it verifies as the owner in `plugin_instance.owner_subject`. A login succeeds only when the instance's plugin verifies that subject. A session stays valid only while its instance still names its subject, so changing or clearing `owner_subject` ends those sessions.
- **First login.** No one can log in before an auth instance has an owner. `innkeeper token create` works on the database directly, so the owner makes a token first and then configures the auth instance through the API.
- **Cross-site requests.** Session cookies are `HttpOnly` and `SameSite=Lax`. A request authenticated by cookie that changes anything must have `Sec-Fetch-Site` of `same-origin` or `none`, or no such header. Huma also rejects bodies that aren't JSON with `415`, so a plain HTML form can't post to the API.
- **Errors.** Every handler returns `s.fail(err)`. It maps known errors to `400`, `404`, and `409`, and logs anything else and returns a bare `500`, so database errors never reach clients.
- **Plugin restarts.** `pluginhost.Instance` gets a `Version`, the instance's `updated_at`. The manager keys processes by ID and version, so after an edit both the API and the worker start a fresh process on the next call. `StopIdle` stops the old one once its calls finish. This replaces the `Manager.Stop(id)` that plan 1 asked for, which could not reach the other service's process.
- **Secrets.** `INNKEEPER_SECRETS_KEY` is 32 random bytes in base64. Both services refuse to start without it. The API returns secret names, never values.

## File structure

| File | Responsibility |
|---|---|
| `internal/db/migrations/0003_api.sql` | Instance secrets, owner, and version; job foreign keys; tokens; sessions; search column |
| `internal/secrets/secrets.go` | AES-256-GCM sealing of instance secrets |
| `internal/plugins/plugins.go` | Instance create, edit, delete, list, with secrets and owner |
| `internal/pluginhost/manager.go` | Processes keyed by instance version |
| `internal/jobs/queue.go` | Split parent completion, cancel, list, child counts, prune |
| `internal/library/entities.go` | `CreateFromName`, overrides check |
| `internal/library/organize.go` | Tag counts, collection list, rename, delete |
| `internal/library/blobs.go` | `OpenBlob` |
| `internal/library/export.go` | CCv3 export with lorebooks embedded |
| `internal/library/search.go` | `Query`, `Filter`, `Search`, `SearchIDs` |
| `internal/auth/auth.go` | API tokens and sessions |
| `protocol/messages.go` | `auth.begin` and `auth.complete` |
| `internal/fakeplugin/main.go` | A fake login provider |
| `internal/api/server.go` | `Server`, Huma setup, error mapping |
| `internal/api/auth.go` | Auth middleware, login, logout, tokens |
| `internal/api/entities.go` | Cards and lorebooks: CRUD, history, restore, tags, links, export |
| `internal/api/organize.go` | Tags, collections, blobs |
| `internal/api/search.go` | `POST /search` |
| `internal/api/instances.go` | Installed plugins and plugin instances |
| `internal/api/jobs.go` | Jobs, SSE, imports, bulk |
| `cmd/innkeeper/services.go` | Setup shared by `api` and `worker` |
| `cmd/innkeeper/api.go` | `innkeeper api` |
| `cmd/innkeeper/token.go` | `innkeeper token` |
| `cmd/innkeeper/client.go` | HTTP client for CLI commands |
| `cmd/innkeeper/commands.go` | `call`, `import`, `export`, `bulk`, `jobs` |
| `cmd/innkeeper/backup.go` | `innkeeper backup` |
| `docs/protocol.md` | Documents the `auth` capability |
| `README.md` | New commands and settings |

Run the database tests with a local server:

```bash
docker run -d --name innkeeper-pg -e POSTGRES_PASSWORD=postgres -p 55432:5432 pgvector/pgvector:pg18
export INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres
```

---

### Task 1: API schema

**Files:**
- Create: `internal/db/migrations/0003_api.sql`
- Create: `internal/db/migrations_test.go`
- Modify: `internal/db/db_test.go` (migration count)

- [ ] **Step 1: Write the failing tests**

In `internal/db/db_test.go`, `TestMigrateTwice` expects 2 migrations. Change both `2`s to `3`:

```go
	if n != 3 {
		t.Fatalf("schema_migration has %d rows, want 3", n)
	}
```

Create `internal/db/migrations_test.go`:

```go
package db_test

import (
	"context"
	"testing"

	"github.com/davidgibbons/innkeeper/internal/testdb"
)

func TestDeletingInstanceKeepsItsJobs(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	var inst, job int64
	if err := pool.QueryRow(ctx, `INSERT INTO plugin_instance (plugin, name) VALUES ('fake', 'Fake') RETURNING id`).Scan(&inst); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO job (kind, instance_id) VALUES ('import', $1) RETURNING id`, inst).Scan(&job); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM plugin_instance WHERE id = $1`, inst); err != nil {
		t.Fatal(err)
	}
	var got *int64
	if err := pool.QueryRow(ctx, `SELECT instance_id FROM job WHERE id = $1`, job).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("instance_id = %d, want null", *got)
	}
}

func TestDeletingParentJobDeletesChildren(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	var parent int64
	if err := pool.QueryRow(ctx, `INSERT INTO job (kind, status) VALUES ('bulk', 'succeeded') RETURNING id`).Scan(&parent); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO job (kind, parent_id, status) VALUES ('card.archive', $1, 'succeeded')`, parent); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM job WHERE id = $1`, parent); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM job`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("%d jobs left, want 0", n)
	}
}

func TestCardRevisionSearchColumn(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	var match bool
	err := pool.QueryRow(ctx, `
		WITH c AS (INSERT INTO card (slug, provenance) VALUES ('a', 'upload') RETURNING id),
		r AS (INSERT INTO card_revision (card_id, data, content_hash, source)
			SELECT id, '{"name": "Brakka", "description": "A dwarf blacksmith."}', 'x', 'ui' FROM c RETURNING search)
		SELECT search @@ websearch_to_tsquery('english', 'blacksmiths') FROM r`).Scan(&match)
	if err != nil {
		t.Fatal(err)
	}
	if !match {
		t.Fatal("search column doesn't match a stemmed word from the description")
	}
}

func TestDeletingAuthInstanceEndsSessions(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	var inst int64
	if err := pool.QueryRow(ctx, `INSERT INTO plugin_instance (plugin, name, owner_subject) VALUES ('fake', 'Login', 'owner') RETURNING id`).Scan(&inst); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO session (token_hash, instance_id, subject, expires_at) VALUES ('h1', $1, 'owner', now())`, inst); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO api_token (name, token_hash) VALUES ('cli', 'h2')`); err != nil {
		t.Fatal(err)
	}
	// Deleting the auth instance ends its sessions.
	if _, err := pool.Exec(ctx, `DELETE FROM plugin_instance WHERE id = $1`, inst); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM session`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("%d sessions left, want 0", n)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/db/`
Expected: FAIL. `TestMigrateTwice` finds 2 rows, and the other tests fail on missing columns and tables.

- [ ] **Step 3: Write the migration**

Create `internal/db/migrations/0003_api.sql`:

```sql
ALTER TABLE plugin_instance
    -- Sealed with INNKEEPER_SECRETS_KEY; see internal/secrets.
    ADD COLUMN secrets bytea,
    -- On an auth instance, the subject it verifies as the owner. Null lets no one log in.
    ADD COLUMN owner_subject text CHECK (owner_subject <> ''),
    -- Plugin processes started before the last edit are replaced.
    ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now();

ALTER TABLE job
    DROP CONSTRAINT job_instance_id_fkey,
    ADD FOREIGN KEY (instance_id) REFERENCES plugin_instance ON DELETE SET NULL,
    DROP CONSTRAINT job_parent_id_fkey,
    ADD FOREIGN KEY (parent_id) REFERENCES job ON DELETE CASCADE;
-- Workers look for waiting parents whose children have all finished.
CREATE INDEX job_waiting ON job (id) WHERE status = 'waiting';

-- Tokens and sessions are stored as the hex SHA-256 of the secret.
CREATE TABLE api_token (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name text NOT NULL CHECK (name <> ''),
    token_hash text NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz
);

CREATE TABLE session (
    token_hash text PRIMARY KEY,
    instance_id bigint NOT NULL REFERENCES plugin_instance ON DELETE CASCADE,
    subject text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL
);
CREATE INDEX ON session (expires_at);

-- Search matches the head revision's row. Names rank above descriptions,
-- which rank above the first message.
ALTER TABLE card_revision ADD COLUMN search tsvector GENERATED ALWAYS AS (
    setweight(to_tsvector('english', coalesce(data->>'name', '')), 'A') ||
    setweight(to_tsvector('english',
        coalesce(data->>'description', '') || ' ' || coalesce(data->>'personality', '') || ' ' ||
        coalesce(data->>'scenario', '') || ' ' || coalesce(data->>'creator_notes', '')), 'B') ||
    setweight(to_tsvector('english', coalesce(data->>'first_mes', '')), 'C')
) STORED;
CREATE INDEX card_revision_search ON card_revision USING gin (search);
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/db/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/db
git commit -m "Add schema for tokens, sessions, instance secrets, and search"
```

---

### Task 2: Secrets

**Files:**
- Create: `internal/secrets/secrets.go`
- Test: `internal/secrets/secrets_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/secrets/secrets_test.go`:

```go
package secrets

import (
	"bytes"
	"encoding/base64"
	"testing"
)

func box(t *testing.T, fill byte) *Box {
	t.Helper()
	b, err := New(bytes.Repeat([]byte{fill}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSealOpen(t *testing.T) {
	b := box(t, 1)
	sealed, err := b.Seal(map[string]string{"api_key": "hunter2"})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte("hunter2")) {
		t.Fatal("sealed secrets contain the plain value")
	}
	got, err := b.Open(sealed)
	if err != nil {
		t.Fatal(err)
	}
	if got["api_key"] != "hunter2" || len(got) != 1 {
		t.Fatalf("opened %v", got)
	}

	if _, err := box(t, 2).Open(sealed); err == nil {
		t.Fatal("opened with the wrong key")
	}
	sealed[len(sealed)-1] ^= 1
	if _, err := b.Open(sealed); err == nil {
		t.Fatal("opened damaged data")
	}
}

func TestEmpty(t *testing.T) {
	b := box(t, 1)
	sealed, err := b.Seal(nil)
	if err != nil || sealed != nil {
		t.Fatalf("Seal(nil) = %v, %v; want nil, nil", sealed, err)
	}
	got, err := b.Open(nil)
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("Open(nil) = %v, %v; want empty map", got, err)
	}
}

func TestFromEnv(t *testing.T) {
	t.Setenv(EnvVar, "")
	if _, err := FromEnv(); err == nil {
		t.Fatal("no error for a missing key")
	}
	t.Setenv(EnvVar, base64.StdEncoding.EncodeToString([]byte("short")))
	if _, err := FromEnv(); err == nil {
		t.Fatal("no error for a short key")
	}
	t.Setenv(EnvVar, base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)))
	if _, err := FromEnv(); err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/secrets/`
Expected: FAIL with `undefined: New`.

- [ ] **Step 3: Write the implementation**

Create `internal/secrets/secrets.go`:

```go
// Package secrets seals plugin instance secrets for storage.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// EnvVar holds the key: 32 random bytes, base64-encoded.
const EnvVar = "INNKEEPER_SECRETS_KEY"

// Box seals and opens secrets with AES-256-GCM.
type Box struct{ aead cipher.AEAD }

// FromEnv returns a Box with the key in EnvVar.
func FromEnv() (*Box, error) {
	v := os.Getenv(EnvVar)
	if v == "" {
		return nil, fmt.Errorf("%s is not set; generate one with: openssl rand -base64 32", EnvVar)
	}
	key, err := base64.StdEncoding.DecodeString(v)
	if err != nil {
		return nil, fmt.Errorf("%s is not valid base64: %w", EnvVar, err)
	}
	return New(key)
}

// New returns a Box with a 32-byte key.
func New(key []byte) (*Box, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("secrets key is %d bytes, want 32", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

// Seal encrypts secrets behind a random nonce. No secrets seal to nil.
func (b *Box) Seal(secrets map[string]string) ([]byte, error) {
	if len(secrets) == 0 {
		return nil, nil
	}
	plain, err := json.Marshal(secrets)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, b.aead.NonceSize())
	rand.Read(nonce)
	return b.aead.Seal(nonce, nonce, plain, nil), nil
}

// Open decrypts what Seal returned. Nil opens to an empty map.
func (b *Box) Open(sealed []byte) (map[string]string, error) {
	out := map[string]string{}
	if sealed == nil {
		return out, nil
	}
	n := b.aead.NonceSize()
	if len(sealed) < n {
		return nil, errors.New("sealed secrets are too short")
	}
	plain, err := b.aead.Open(nil, sealed[:n], sealed[n:], nil)
	if err != nil {
		return nil, fmt.Errorf("open secrets: wrong key or damaged data: %w", err)
	}
	return out, json.Unmarshal(plain, &out)
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/secrets/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/secrets
git commit -m "Add AES-GCM sealing for instance secrets"
```

---

### Task 3: Instance settings

`plugins.New` takes a `*secrets.Box`. `CreateInstance` takes a `NewInstance`, so it can set secrets and an owner. New methods edit, delete, list, and show instances. `Instance` passes decrypted secrets and a `Version` to the plugin host. The worker now needs `INNKEEPER_SECRETS_KEY`.

**Files:**
- Modify: `internal/plugins/plugins.go`
- Modify: `internal/pluginhost/manager.go:11-17` (add `Version` to `Instance`)
- Modify: `internal/plugins/plugins_test.go`
- Modify: `internal/importer/importer_test.go:48,67`
- Modify: `cmd/innkeeper/worker.go`

- [ ] **Step 1: Write the failing tests**

In `internal/plugins/plugins_test.go`, replace `TestInstance` and add the tests below it. Add `"slices"`, `"strings"`, and `"github.com/davidgibbons/innkeeper/internal/secrets"` to the imports.

```go
func newRegistry(t *testing.T) (*Registry, *blobstore.Store) {
	t.Helper()
	store := &blobstore.Store{Name: "library", Root: t.TempDir()}
	box, err := secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	m := &protocol.Manifest{Name: "fake", Version: "0.0.1", Secrets: []string{"api_key", "region"}}
	return New(testdb.New(t), map[string]*protocol.Manifest{"fake": m}, store, box), store
}

func TestInstance(t *testing.T) {
	ctx := context.Background()
	reg, store := newRegistry(t)

	if _, err := reg.CreateInstance(ctx, NewInstance{Plugin: "missing", Name: "x"}); !errors.Is(err, ErrUnknownPlugin) {
		t.Fatalf("create for missing plugin: %v", err)
	}
	id, err := reg.CreateInstance(ctx, NewInstance{Plugin: "fake", Name: "Fake",
		Config: map[string]any{"path": "/cards"}, Secrets: map[string]string{"api_key": "hunter2"}})
	if err != nil {
		t.Fatal(err)
	}
	inst, err := reg.Instance(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if inst.Manifest != reg.Manifests()["fake"] || inst.Params.Config["path"] != "/cards" ||
		inst.Params.BlobTmp != store.TmpDir() || inst.Params.Secrets["api_key"] != "hunter2" || inst.Version == "" {
		t.Fatalf("instance = %+v", inst)
	}
	if _, err := os.Stat(store.TmpDir()); err != nil {
		t.Fatalf("blob tmp folder: %v", err)
	}

	if err := reg.MarkBroken(ctx, id, errors.New("wrong protocol")); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Instance(ctx, id); !errors.Is(err, ErrBroken) {
		t.Fatalf("broken instance: %v", err)
	}
	if _, err := reg.Instance(ctx, id+1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing instance: %v", err)
	}
}

func TestInstanceInfoHidesSecretValues(t *testing.T) {
	ctx := context.Background()
	reg, _ := newRegistry(t)
	owner := "owner"
	id, err := reg.CreateInstance(ctx, NewInstance{Plugin: "fake", Name: "Fake",
		Secrets: map[string]string{"region": "eu", "api_key": "hunter2"}, OwnerSubject: &owner})
	if err != nil {
		t.Fatal(err)
	}
	info, err := reg.GetInstance(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(info.Secrets, []string{"api_key", "region"}) || *info.OwnerSubject != "owner" || info.Config == nil {
		t.Fatalf("info = %+v", info)
	}
	if strings.Contains(fmt.Sprintf("%+v", info), "hunter2") {
		t.Fatal("instance info contains a secret value")
	}
	list, err := reg.ListInstances(ctx)
	if err != nil || len(list) != 1 || list[0].ID != id {
		t.Fatalf("list = %+v, %v", list, err)
	}
}

func TestCreateInstanceRejectsBadSettings(t *testing.T) {
	ctx := context.Background()
	reg, _ := newRegistry(t)
	if _, err := reg.CreateInstance(ctx, NewInstance{Plugin: "fake", Name: "Fake",
		Secrets: map[string]string{"password": "x"}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("undeclared secret: %v", err)
	}
	if _, err := reg.CreateInstance(ctx, NewInstance{Plugin: "fake", Name: ""}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty name: %v", err)
	}
	if _, err := reg.CreateInstance(ctx, NewInstance{Plugin: "fake", Name: "Fake"}); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.CreateInstance(ctx, NewInstance{Plugin: "fake", Name: "Fake"}); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("duplicate name: %v", err)
	}
}

func TestUpdateInstance(t *testing.T) {
	ctx := context.Background()
	reg, _ := newRegistry(t)
	id, err := reg.CreateInstance(ctx, NewInstance{Plugin: "fake", Name: "Fake",
		Config: map[string]any{"path": "/a"}, Secrets: map[string]string{"api_key": "old", "region": "eu"}})
	if err != nil {
		t.Fatal(err)
	}
	before, err := reg.Instance(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.MarkBroken(ctx, id, errors.New("bad key")); err != nil {
		t.Fatal(err)
	}
	newKey, name, owner := "new", "Renamed", "owner"
	err = reg.UpdateInstance(ctx, id, InstanceEdit{Name: &name, OwnerSubject: &owner,
		Secrets: map[string]*string{"api_key": &newKey, "region": nil}})
	if err != nil {
		t.Fatal(err)
	}
	after, err := reg.Instance(ctx, id)
	if err != nil {
		t.Fatalf("instance still broken after an edit: %v", err)
	}
	if after.Version == before.Version {
		t.Fatal("version didn't change")
	}
	if after.Params.Secrets["api_key"] != "new" || len(after.Params.Secrets) != 1 || after.Params.Config["path"] != "/a" {
		t.Fatalf("after edit: %+v", after.Params)
	}
	info, err := reg.GetInstance(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "Renamed" || *info.OwnerSubject != "owner" || info.BrokenError != nil {
		t.Fatalf("info = %+v", info)
	}

	empty := ""
	if err := reg.UpdateInstance(ctx, id, InstanceEdit{OwnerSubject: &empty}); err != nil {
		t.Fatal(err)
	}
	if info, _ := reg.GetInstance(ctx, id); info.OwnerSubject != nil {
		t.Fatalf("owner not cleared: %q", *info.OwnerSubject)
	}
	bad := "x"
	if err := reg.UpdateInstance(ctx, id, InstanceEdit{Secrets: map[string]*string{"password": &bad}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("undeclared secret: %v", err)
	}
	if err := reg.UpdateInstance(ctx, id+1, InstanceEdit{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing instance: %v", err)
	}
}

func TestDeleteInstance(t *testing.T) {
	ctx := context.Background()
	reg, _ := newRegistry(t)
	id, err := reg.CreateInstance(ctx, NewInstance{Plugin: "fake", Name: "Fake"})
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.DeleteInstance(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := reg.DeleteInstance(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
}
```

Also add `"fmt"` to the imports.

In `internal/importer/importer_test.go`, change the registry and instance setup:

```go
	box, err := secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	reg := plugins.New(pool, map[string]*protocol.Manifest{"fake": m}, store, box)
```

```go
	inst, err := reg.CreateInstance(ctx, plugins.NewInstance{Plugin: "fake", Name: "Fake"})
```

and add `"github.com/davidgibbons/innkeeper/internal/secrets"` to its imports.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/plugins/ ./internal/importer/`
Expected: FAIL to compile: `too many arguments in call to New`, `undefined: NewInstance`.

- [ ] **Step 3: Add `Version` to `pluginhost.Instance`**

In `internal/pluginhost/manager.go`, replace the `Instance` type:

```go
// Instance is a configured plugin: what to run and what to initialize it
// with.
type Instance struct {
	ID string
	// Version changes whenever the instance's settings do.
	Version  string
	Manifest *protocol.Manifest
	Params   protocol.InitializeParams
}
```

- [ ] **Step 4: Write the registry changes**

Replace `internal/plugins/plugins.go` from the `var (` block through the end of the file with:

```go
var (
	ErrNotFound      = errors.New("plugin instance not found")
	ErrUnknownPlugin = errors.New("plugin is not installed")
	// ErrBroken means the instance's plugin failed to start. It stays broken
	// until the owner changes the instance.
	ErrBroken    = errors.New("plugin instance is broken")
	ErrInvalid   = errors.New("invalid plugin instance")
	ErrNameTaken = errors.New("plugin instance name already in use")
)

// LoadDir loads the manifest in each folder of dir. A folder with an invalid
// manifest is logged and left out, so one bad plugin doesn't stop the rest.
func LoadDir(dir string, log *slog.Logger) (map[string]*protocol.Manifest, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := map[string]*protocol.Manifest{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		m, err := protocol.LoadManifest(filepath.Join(dir, e.Name()))
		if err != nil {
			log.Error("skip plugin", "dir", e.Name(), "err", err)
			continue
		}
		if _, dup := out[m.Name]; dup {
			log.Error("skip plugin: name already loaded", "dir", e.Name(), "plugin", m.Name)
			continue
		}
		out[m.Name] = m
	}
	return out, nil
}

// Registry resolves plugin instances to what the plugin host needs to run
// them.
type Registry struct {
	pool      *pgxpool.Pool
	manifests map[string]*protocol.Manifest
	blobs     *blobstore.Store
	box       *secrets.Box
}

// New returns a Registry for the installed plugins in manifests, whose
// plugins write new blobs to blobs. box seals instance secrets.
func New(pool *pgxpool.Pool, manifests map[string]*protocol.Manifest, blobs *blobstore.Store, box *secrets.Box) *Registry {
	return &Registry{pool: pool, manifests: manifests, blobs: blobs, box: box}
}

// Manifests returns the installed plugins by name.
func (r *Registry) Manifests() map[string]*protocol.Manifest { return r.manifests }

// InstanceInfo is an instance as the owner sees it. It names the secrets
// that are set but never holds their values.
type InstanceInfo struct {
	ID           int64          `json:"id"`
	Plugin       string         `json:"plugin"`
	Name         string         `json:"name"`
	Config       map[string]any `json:"config"`
	Secrets      []string       `json:"secrets"`
	OwnerSubject *string        `json:"owner_subject,omitempty"`
	BrokenError  *string        `json:"broken_error,omitempty"`
	BrokenAt     *time.Time     `json:"broken_at,omitempty"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
}

// NewInstance is a new instance's settings.
type NewInstance struct {
	Plugin       string
	Name         string
	Config       map[string]any
	Secrets      map[string]string
	OwnerSubject *string
}

// InstanceEdit changes an instance. Nil fields stay as they are. A nil value
// in Secrets removes that secret, and an empty OwnerSubject clears it.
type InstanceEdit struct {
	Name         *string
	Config       map[string]any
	Secrets      map[string]*string
	OwnerSubject *string
}

func checkSecret(m *protocol.Manifest, name string) error {
	if !slices.Contains(m.Secrets, name) {
		return fmt.Errorf("%w: plugin %s has no secret %q", ErrInvalid, m.Name, name)
	}
	return nil
}

// writeErr maps constraint violations on plugin_instance.
func writeErr(err error) error {
	var pgErr *pgconn.PgError
	switch {
	case errors.As(err, &pgErr) && pgErr.Code == "23505":
		return ErrNameTaken
	case errors.As(err, &pgErr) && pgErr.Code == "23514":
		return fmt.Errorf("%w: %s", ErrInvalid, pgErr.ConstraintName)
	}
	return err
}

// CreateInstance configures a new instance of an installed plugin.
func (r *Registry) CreateInstance(ctx context.Context, n NewInstance) (int64, error) {
	m, ok := r.manifests[n.Plugin]
	if !ok {
		return 0, fmt.Errorf("%w: %s", ErrUnknownPlugin, n.Plugin)
	}
	for name := range n.Secrets {
		if err := checkSecret(m, name); err != nil {
			return 0, err
		}
	}
	sealed, err := r.box.Seal(n.Secrets)
	if err != nil {
		return 0, err
	}
	if n.Config == nil {
		n.Config = map[string]any{}
	}
	var id int64
	err = r.pool.QueryRow(ctx, `INSERT INTO plugin_instance (plugin, name, config, secrets, owner_subject)
		VALUES ($1, $2, $3, $4, $5) RETURNING id`, n.Plugin, n.Name, n.Config, sealed, n.OwnerSubject).Scan(&id)
	return id, writeErr(err)
}

// UpdateInstance applies e and clears the instance's broken state, so the
// next call starts its plugin with the new settings.
func (r *Registry) UpdateInstance(ctx context.Context, id int64, e InstanceEdit) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var plugin string
	var sealed []byte
	err = tx.QueryRow(ctx, `SELECT plugin, secrets FROM plugin_instance WHERE id = $1 FOR UPDATE`, id).Scan(&plugin, &sealed)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if e.Secrets != nil {
		m, ok := r.manifests[plugin]
		if !ok {
			return fmt.Errorf("%w: %s", ErrUnknownPlugin, plugin)
		}
		current, err := r.box.Open(sealed)
		if err != nil {
			return err
		}
		for name, v := range e.Secrets {
			if v == nil {
				delete(current, name)
				continue
			}
			if err := checkSecret(m, name); err != nil {
				return err
			}
			current[name] = *v
		}
		if sealed, err = r.box.Seal(current); err != nil {
			return err
		}
	}
	// An untyped nil sends SQL NULL, which keeps the current config.
	var config any
	if e.Config != nil {
		config = e.Config
	}
	owner := ""
	if e.OwnerSubject != nil {
		owner = *e.OwnerSubject
	}
	_, err = tx.Exec(ctx, `UPDATE plugin_instance SET name = coalesce($2, name), config = coalesce($3, config),
			secrets = $4, owner_subject = CASE WHEN $5 THEN nullif($6::text, '') ELSE owner_subject END,
			broken_error = NULL, broken_at = NULL, updated_at = now()
		WHERE id = $1`, id, e.Name, config, sealed, e.OwnerSubject != nil, owner)
	if err != nil {
		return writeErr(err)
	}
	return tx.Commit(ctx)
}

// DeleteInstance removes an instance. Its jobs stay, without an instance,
// and logins through it end.
func (r *Registry) DeleteInstance(ctx context.Context, id int64) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM plugin_instance WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

const infoCols = `id, plugin, name, config, secrets, owner_subject, broken_error, broken_at, created_at, updated_at`

func (r *Registry) scanInfo(row pgx.Row) (InstanceInfo, error) {
	var i InstanceInfo
	var sealed []byte
	err := row.Scan(&i.ID, &i.Plugin, &i.Name, &i.Config, &sealed, &i.OwnerSubject,
		&i.BrokenError, &i.BrokenAt, &i.CreatedAt, &i.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return i, ErrNotFound
	}
	if err != nil {
		return i, err
	}
	s, err := r.box.Open(sealed)
	if err != nil {
		return i, err
	}
	i.Secrets = slices.Sorted(maps.Keys(s))
	return i, nil
}

// GetInstance returns one instance's settings, without secret values.
func (r *Registry) GetInstance(ctx context.Context, id int64) (InstanceInfo, error) {
	return r.scanInfo(r.pool.QueryRow(ctx, `SELECT `+infoCols+` FROM plugin_instance WHERE id = $1`, id))
}

// ListInstances returns every instance, oldest first.
func (r *Registry) ListInstances(ctx context.Context) ([]InstanceInfo, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+infoCols+` FROM plugin_instance ORDER BY id`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (InstanceInfo, error) { return r.scanInfo(row) })
}

// Instance returns what the plugin host needs to run an instance.
func (r *Registry) Instance(ctx context.Context, id int64) (pluginhost.Instance, error) {
	var plugin string
	var config map[string]any
	var sealed []byte
	var broken *string
	var updated time.Time
	err := r.pool.QueryRow(ctx, `SELECT plugin, config, secrets, broken_error, updated_at FROM plugin_instance WHERE id = $1`, id).
		Scan(&plugin, &config, &sealed, &broken, &updated)
	if errors.Is(err, pgx.ErrNoRows) {
		return pluginhost.Instance{}, ErrNotFound
	}
	if err != nil {
		return pluginhost.Instance{}, err
	}
	if broken != nil {
		return pluginhost.Instance{}, fmt.Errorf("%w: %s", ErrBroken, *broken)
	}
	m, ok := r.manifests[plugin]
	if !ok {
		return pluginhost.Instance{}, fmt.Errorf("%w: %s", ErrUnknownPlugin, plugin)
	}
	secrets, err := r.box.Open(sealed)
	if err != nil {
		return pluginhost.Instance{}, err
	}
	if err := os.MkdirAll(r.blobs.TmpDir(), 0o755); err != nil {
		return pluginhost.Instance{}, err
	}
	// ponytail: no plugin database role yet; the archive catalog needs it.
	return pluginhost.Instance{
		ID:       strconv.FormatInt(id, 10),
		Version:  strconv.FormatInt(updated.UnixMicro(), 10),
		Manifest: m,
		Params: protocol.InitializeParams{
			Config:     config,
			Secrets:    secrets,
			BlobStores: map[string]string{r.blobs.Name: r.blobs.Root},
			BlobTmp:    r.blobs.TmpDir(),
		},
	}, nil
}

// MarkBroken records that an instance's plugin failed to start, so nothing
// starts it again until the owner changes the instance.
func (r *Registry) MarkBroken(ctx context.Context, id int64, cause error) error {
	_, err := r.pool.Exec(ctx, `UPDATE plugin_instance SET broken_error = $2, broken_at = now() WHERE id = $1`,
		id, cause.Error())
	return err
}
```

Set the imports to:

```go
import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/davidgibbons/innkeeper/internal/blobstore"
	"github.com/davidgibbons/innkeeper/internal/pluginhost"
	"github.com/davidgibbons/innkeeper/internal/secrets"
	"github.com/davidgibbons/innkeeper/protocol"
)
```

- [ ] **Step 5: Give the worker the secrets key**

In `cmd/innkeeper/worker.go`, add `"github.com/davidgibbons/innkeeper/internal/secrets"` to the imports. Right after the `INNKEEPER_WORKER_CONCURRENCY` block, add:

```go
	box, err := secrets.FromEnv()
	if err != nil {
		return err
	}
```

and change the importer line to:

```go
	im := importer.New(lib, plugins.New(pool, manifests, store, box), log)
```

Task 16 moves this setup into `services.go`.

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go build ./... && go test ./internal/plugins/ ./internal/importer/ ./internal/pluginhost/`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/plugins internal/pluginhost/manager.go internal/importer/importer_test.go cmd/innkeeper/worker.go
git commit -m "Store encrypted instance secrets and an owner subject"
```

---

### Task 4: Restart plugins after an edit

**Files:**
- Modify: `internal/pluginhost/manager.go`
- Modify: `internal/pluginhost/manager_test.go`

- [ ] **Step 1: Point the test helpers at process keys**

The manager will key processes by `inst.key()`, not `inst.ID`:

```bash
sed -i '' -e 's/mgr\.proc(inst\.ID)/mgr.proc(inst.key())/g' -e 's/waitInflight(t, mgr, inst\.ID)/waitInflight(t, mgr, inst.key())/g' internal/pluginhost/manager_test.go
grep -n 'inst\.ID' internal/pluginhost/manager_test.go
```

Expected: `grep` prints nothing. If it prints a line, change that call by hand the same way.

- [ ] **Step 2: Write the failing test**

Append to `internal/pluginhost/manager_test.go`:

```go
func TestNewVersionStartsNewProcess(t *testing.T) {
	mgr, inst := newManager(t, time.Minute)
	ctx := context.Background()
	inst.Version = "1"
	if err := mgr.Call(ctx, inst, "echo", 1, nil); err != nil {
		t.Fatal(err)
	}
	oldKey := inst.key()
	old := mgr.proc(oldKey)

	inst.Version = "2"
	if err := mgr.Call(ctx, inst, "echo", 1, nil); err != nil {
		t.Fatal(err)
	}
	if p := mgr.proc(inst.key()); p == nil || p == old {
		t.Fatal("new version reused the old process")
	}

	// The old process is idle and superseded, so it stops long before the
	// idle timeout.
	mgr.StopIdle()
	if mgr.proc(oldKey) != nil {
		t.Fatal("old version still running after StopIdle")
	}
	if mgr.proc(inst.key()) == nil {
		t.Fatal("StopIdle stopped the current version")
	}
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./internal/pluginhost/`
Expected: FAIL to compile with `inst.key undefined`.

- [ ] **Step 4: Write the implementation**

In `internal/pluginhost/manager.go`:

Add below the `Instance` type:

```go
func (i Instance) key() string { return i.ID + "@" + i.Version }
```

Change the `Manager` and `entry` types, and `NewManager`:

```go
// Manager runs at most one process per instance version, starting it on
// first use and again after a crash. The API and the worker each have their
// own Manager, so an instance can have one process in each.
type Manager struct {
	core protocol.Handler
	idle time.Duration
	log  *slog.Logger

	mu      sync.Mutex
	running map[string]*entry
	// latest maps an instance ID to the key of its newest version.
	latest map[string]string
	closed bool
}

type entry struct {
	key      string
	id       string
	proc     *Process
	inflight int
	lastUsed time.Time
}

// NewManager returns a Manager whose plugins send their calls to core.
// StopIdle stops plugins unused for longer than idle.
func NewManager(core protocol.Handler, idle time.Duration, log *slog.Logger) *Manager {
	return &Manager{core: core, idle: idle, log: log, running: map[string]*entry{}, latest: map[string]string{}}
}
```

In `acquire`, replace the lookup and start with:

```go
	key := inst.key()
	e, ok := m.running[key]
	if !ok || !e.proc.Alive() {
		params := inst.Params
		params.InstanceID = inst.ID
		p, err := Start(ctx, inst.Manifest, params, m.core, m.log)
		if err != nil {
			return nil, &StartError{Err: err}
		}
		e = &entry{key: key, id: inst.ID, proc: p}
		m.running[key] = e
	}
	m.latest[inst.ID] = key
```

Replace `StopIdle`:

```go
// StopIdle stops plugins that have exited, have had no call in flight for
// longer than the idle timeout, or run an old version with no call in
// flight. Run it periodically.
func (m *Manager) StopIdle() {
	m.stopWhere(func(e *entry) bool {
		if !e.proc.Alive() {
			return true
		}
		return e.inflight == 0 && (time.Since(e.lastUsed) > m.idle || m.latest[e.id] != e.key)
	})
}
```

`stopWhere` already deletes by map key and calls `match` while holding `m.mu`, so it needs no change.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test -race ./internal/pluginhost/`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/pluginhost
git commit -m "Start a new plugin process when an instance's settings change"
```

---

### Task 5: Job list, cancel, and prune

This task also does the INN-3 review fix. `reap` called `completeParents(nil)`, which scanned every child job ever created, every 5 seconds, from every worker. It is now two statements: one parent by `parent_id = $1`, and a sweep limited to children of waiting parents, which uses the new `job_waiting` index.

**Files:**
- Modify: `internal/jobs/queue.go`
- Test: `internal/jobs/queue_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/jobs/queue_test.go`:

```go
func TestCancel(t *testing.T) {
	q := newQueue(t)
	ctx := context.Background()
	j, _ := q.Enqueue(ctx, Spec{Kind: "test"})
	if err := q.Cancel(ctx, j.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := q.Get(ctx, j.ID)
	if got.Status != StatusFailed || errText(got) != "cancelled" || got.FinishedAt == nil {
		t.Fatalf("cancelled job = %+v", got)
	}
	if err := q.Cancel(ctx, j.ID); !errors.Is(err, ErrFinished) {
		t.Fatalf("second cancel: %v", err)
	}
	if err := q.Cancel(ctx, j.ID+100); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cancel missing job: %v", err)
	}
}

func TestCancelParentCancelsChildren(t *testing.T) {
	q := newQueue(t)
	ctx := context.Background()
	p, _ := q.EnqueueBulk(ctx, Spec{Kind: "bulk"}, []Spec{{Kind: "item"}, {Kind: "item"}})
	if err := q.Cancel(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	counts, err := q.ChildCounts(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if counts[StatusFailed] != 2 || len(counts) != 1 {
		t.Fatalf("children = %v, want 2 failed", counts)
	}
	if got, _ := q.Get(ctx, p.ID); got.Status != StatusFailed {
		t.Fatalf("parent = %+v", got)
	}
}

func TestCancelLastChildFinishesParent(t *testing.T) {
	q := newQueue(t)
	ctx := context.Background()
	p, _ := q.EnqueueBulk(ctx, Spec{Kind: "bulk"}, []Spec{{Kind: "item"}})
	children, err := q.List(ctx, ListOptions{Parent: &p.ID})
	if err != nil || len(children) != 1 {
		t.Fatalf("children = %v, %v", children, err)
	}
	if err := q.Cancel(ctx, children[0].ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := q.Get(ctx, p.ID); got.Status != StatusFailed || errText(got) != "1 of 1 jobs failed" {
		t.Fatalf("parent = %+v", got)
	}
}

func TestCancelRunningJobTakesItsLease(t *testing.T) {
	q := newQueue(t)
	ctx := context.Background()
	j, _ := q.Enqueue(ctx, Spec{Kind: "test"})
	c, err := q.claim(ctx, "w1", []string{"test"}, time.Minute)
	if err != nil || c == nil {
		t.Fatalf("claim = %v, %v", c, err)
	}
	if err := q.Cancel(ctx, j.ID); err != nil {
		t.Fatal(err)
	}
	if err := q.touch(ctx, c, "w1", time.Minute, nil, nil); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("touch after cancel: %v", err)
	}
}

func TestReapFinishesOrphanedParent(t *testing.T) {
	q := newQueue(t)
	ctx := context.Background()
	p, _ := q.EnqueueBulk(ctx, Spec{Kind: "bulk"}, []Spec{{Kind: "item"}})
	// A worker that died between finishing the child and checking the parent.
	if _, err := q.pool.Exec(ctx, `UPDATE job SET status = 'succeeded', finished_at = now() WHERE parent_id = $1`, p.ID); err != nil {
		t.Fatal(err)
	}
	if err := q.reap(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := q.Get(ctx, p.ID); got.Status != StatusSucceeded {
		t.Fatalf("parent = %+v", got)
	}
}

func TestListAndPrune(t *testing.T) {
	q := newQueue(t)
	ctx := context.Background()
	a, _ := q.Enqueue(ctx, Spec{Kind: "a"})
	b, _ := q.Enqueue(ctx, Spec{Kind: "b"})
	p, _ := q.EnqueueBulk(ctx, Spec{Kind: "bulk"}, []Spec{{Kind: "item"}})

	top, err := q.List(ctx, ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(top) != 3 || top[0].ID != p.ID || top[2].ID != a.ID {
		t.Fatalf("top-level jobs = %v", ids(top))
	}
	if got, _ := q.List(ctx, ListOptions{Kind: "b"}); len(got) != 1 || got[0].ID != b.ID {
		t.Fatalf("kind b = %v", ids(got))
	}
	if got, _ := q.List(ctx, ListOptions{Status: StatusWaiting}); len(got) != 1 || got[0].ID != p.ID {
		t.Fatalf("waiting = %v", ids(got))
	}
	if got, _ := q.List(ctx, ListOptions{Before: b.ID}); len(got) != 1 || got[0].ID != a.ID {
		t.Fatalf("before b = %v", ids(got))
	}
	if got, _ := q.List(ctx, ListOptions{Limit: 1}); len(got) != 1 {
		t.Fatalf("limit 1 = %v", ids(got))
	}

	if err := q.Cancel(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if err := q.Cancel(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := q.pool.Exec(ctx, `UPDATE job SET finished_at = now() - interval '40 days' WHERE id = $1 OR parent_id = $1`, p.ID); err != nil {
		t.Fatal(err)
	}
	n, err := q.Prune(ctx, 30*24*time.Hour)
	if err != nil || n != 1 {
		t.Fatalf("Prune = %d, %v; want 1", n, err)
	}
	var left int
	if err := q.pool.QueryRow(ctx, `SELECT count(*) FROM job`).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 2 {
		t.Fatalf("%d jobs left, want a and b", left)
	}
}

func ids(js []Job) []int64 {
	out := make([]int64, len(js))
	for i, j := range js {
		out[i] = j.ID
	}
	return out
}
```

`errText` is in `worker_test.go`, in the same package.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/jobs/`
Expected: FAIL to compile: `q.Cancel undefined`.

- [ ] **Step 3: Split parent completion**

In `internal/jobs/queue.go`, in `finish`, replace the last `if`:

```go
	if j.ParentID != nil && o.status != StatusQueued {
		return q.completeParent(ctx, *j.ParentID)
	}
	return nil
```

Replace `completeParents` with:

```go
// completeParent finishes a waiting parent whose children have all
// finished. It runs after the child's update commits, so when two children
// finish at once, whichever checks second sees both.
func (q *Queue) completeParent(ctx context.Context, id int64) error {
	return q.completeParents(ctx, `parent_id = $1`, id)
}

// completeWaitingParents checks every waiting parent, which catches a worker
// that died between finishing a child and checking its parent.
func (q *Queue) completeWaitingParents(ctx context.Context) error {
	return q.completeParents(ctx, `parent_id IN (SELECT id FROM job WHERE status = 'waiting')`)
}

// completeParents finishes waiting parents of the children that match where.
// Each caller passes a fixed condition, so each gets its own query plan.
func (q *Queue) completeParents(ctx context.Context, where string, args ...any) error {
	_, err := q.pool.Exec(ctx, `UPDATE job p SET
			status = CASE WHEN s.failed > 0 THEN 'failed' ELSE 'succeeded' END,
			error = CASE WHEN s.failed > 0 THEN s.failed || ' of ' || s.total || ' jobs failed' END,
			finished_at = now()
		FROM (SELECT parent_id, count(*) AS total,
				count(*) FILTER (WHERE status = 'failed') AS failed,
				count(*) FILTER (WHERE status NOT IN ('succeeded', 'failed')) AS open
			FROM job WHERE `+where+` GROUP BY parent_id) s
		WHERE p.id = s.parent_id AND p.status = 'waiting' AND s.open = 0`, args...)
	return err
}
```

In `reap`, change the last line to:

```go
	return q.completeWaitingParents(ctx)
```

- [ ] **Step 4: Add cancel, list, child counts, and prune**

Add `ErrFinished` to the `var` block in `internal/jobs/queue.go`:

```go
	// ErrFinished means the job already succeeded or failed.
	ErrFinished = errors.New("job already finished")
```

Append to `internal/jobs/queue.go`:

```go
// Cancel fails a job that hasn't finished, along with its unfinished
// children. A running job's worker loses its lease at the next renewal,
// which cancels the handler.
func (q *Queue) Cancel(ctx context.Context, id int64) error {
	tx, err := q.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	j, err := scanJob(tx.QueryRow(ctx, `SELECT `+jobCols+` FROM job WHERE id = $1 FOR UPDATE`, id))
	if err != nil {
		return err
	}
	if j.Status == StatusSucceeded || j.Status == StatusFailed {
		return ErrFinished
	}
	if _, err := tx.Exec(ctx, `UPDATE job SET status = 'failed', error = 'cancelled', finished_at = now(),
			locked_by = NULL, locked_until = NULL
		WHERE (id = $1 OR parent_id = $1) AND status IN ('queued', 'running', 'waiting')`, id); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	if j.ParentID != nil {
		return q.completeParent(ctx, *j.ParentID)
	}
	return nil
}

// ListOptions filters List. Zero values match everything.
type ListOptions struct {
	Status string
	Kind   string
	// Parent lists that parent's children. Without it, List returns only
	// top-level jobs.
	Parent *int64
	// Before returns only jobs with smaller IDs, for paging.
	Before int64
	// Limit defaults to 50.
	Limit int
}

// List returns jobs, newest first.
func (q *Queue) List(ctx context.Context, o ListOptions) ([]Job, error) {
	if o.Limit <= 0 {
		o.Limit = 50
	}
	rows, err := q.pool.Query(ctx, `SELECT `+jobCols+` FROM job
		WHERE ($1::text = '' OR status = $1) AND ($2::text = '' OR kind = $2)
			AND parent_id IS NOT DISTINCT FROM $3::bigint AND ($4::bigint = 0 OR id < $4)
		ORDER BY id DESC LIMIT $5`, o.Status, o.Kind, o.Parent, o.Before, o.Limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Job, error) { return scanJob(row) })
}

// ChildCounts returns how many of a parent's children have each status.
func (q *Queue) ChildCounts(ctx context.Context, parent int64) (map[string]int, error) {
	rows, err := q.pool.Query(ctx, `SELECT status, count(*)::int FROM job WHERE parent_id = $1 GROUP BY status`, parent)
	if err != nil {
		return nil, err
	}
	out := map[string]int{}
	var status string
	var n int
	_, err = pgx.ForEachRow(rows, []any{&status, &n}, func() error {
		out[status] = n
		return nil
	})
	return out, err
}

// Prune deletes top-level jobs that finished more than age ago, with their
// children.
func (q *Queue) Prune(ctx context.Context, age time.Duration) (int64, error) {
	tag, err := q.pool.Exec(ctx, `DELETE FROM job WHERE parent_id IS NULL AND status IN ('succeeded', 'failed')
		AND finished_at < now() - make_interval(secs => $1)`, age.Seconds())
	return tag.RowsAffected(), err
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test -race ./internal/jobs/ ./internal/bulk/ ./internal/importer/`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/jobs
git commit -m "Add job cancel, list, and prune; scan only waiting parents' children"
```

---

### Task 6: Library additions for the API

**Files:**
- Modify: `internal/library/entities.go` (overrides check)
- Modify: `internal/library/import.go` (`CreateFromName`)
- Modify: `internal/library/organize.go` (tag counts, collections)
- Modify: `internal/library/library.go` (`ErrNameTaken`)
- Modify: `internal/library/blobs.go` (`OpenBlob`, JSON names)
- Modify: `internal/library/similar.go` (JSON names)
- Create: `internal/library/export.go`
- Test: `internal/library/api_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/library/api_test.go`:

```go
package library

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
)

func TestCreateFromName(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	a, _, err := lib.Cards.CreateFromName(ctx, "Brakka Ironfist", "api", edit(`{"name": "Brakka Ironfist"}`))
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := lib.Cards.CreateFromName(ctx, "Brakka Ironfist", "api", edit(`{"name": "Brakka Ironfist"}`))
	if err != nil {
		t.Fatal(err)
	}
	if a.Slug != "brakka-ironfist" || b.Slug != "brakka-ironfist-2" {
		t.Fatalf("slugs = %q, %q", a.Slug, b.Slug)
	}
}

func TestOverridesMustBeAnObject(t *testing.T) {
	lib := newLibrary(t)
	e := edit(`{"name": "A"}`)
	e.Overrides = json.RawMessage(`[]`)
	if _, _, err := lib.Cards.Create(context.Background(), "a", "api", e); !errors.Is(err, ErrInvalid) {
		t.Fatalf("got %v, want ErrInvalid", err)
	}
}

func TestTags(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	a, _ := newCard(t, lib, "a", `{"name": "A"}`)
	b, _ := newCard(t, lib, "b", `{"name": "B"}`)
	if err := lib.SetCardTags(ctx, a, []string{"fantasy", "Dwarf"}); err != nil {
		t.Fatal(err)
	}
	if err := lib.SetCardTags(ctx, b, []string{"fantasy"}); err != nil {
		t.Fatal(err)
	}
	got, err := lib.Tags(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []TagCount{{"Dwarf", 1}, {"fantasy", 2}}
	if !slices.Equal(got, want) {
		t.Fatalf("tags = %v, want %v", got, want)
	}
}

func TestCollections(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	card, _ := newCard(t, lib, "a", `{"name": "A"}`)
	c, err := lib.CreateCollection(ctx, "Favorites", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.CreateCollection(ctx, "Favorites", ""); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("duplicate name: %v", err)
	}
	if err := lib.AddToCollection(ctx, c.ID, card); err != nil {
		t.Fatal(err)
	}
	name, desc := "Best", "The best ones"
	if _, err := lib.UpdateCollection(ctx, c.ID, &name, &desc); err != nil {
		t.Fatal(err)
	}
	list, err := lib.Collections(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Name != "Best" || list[0].Description != "The best ones" || list[0].Cards != 1 {
		t.Fatalf("collections = %+v", list)
	}
	if err := lib.DeleteCollection(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if err := lib.DeleteCollection(ctx, c.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
	if _, err := lib.UpdateCollection(ctx, c.ID, &name, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update deleted: %v", err)
	}
}

func TestOpenBlob(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	b, err := lib.PutBlob(ctx, strings.NewReader("hello"))
	if err != nil {
		t.Fatal(err)
	}
	got, f, err := lib.OpenBlob(ctx, b.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	body, _ := io.ReadAll(f)
	if string(body) != "hello" || got.Size != 5 {
		t.Fatalf("blob = %+v, %q", got, body)
	}
	if _, _, err := lib.OpenBlob(ctx, strings.Repeat("0", 64)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing blob: %v", err)
	}
}

func TestExportCardEmbedsLorebooks(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	card, _ := newCard(t, lib, "brakka", `{"name": "Brakka"}`)
	for i, data := range []string{
		`{"name": "Forge", "entries": [{"keys": ["anvil"], "content": "Heavy."}]}`,
		`{"name": "Clan", "entries": [{"keys": ["clan"], "content": "Ironfist."}]}`,
	} {
		lb, _, err := lib.Lorebooks.Create(ctx, []string{"forge", "clan"}[i], "api", edit(data))
		if err != nil {
			t.Fatal(err)
		}
		if err := lib.LinkLorebook(ctx, card, lb.ID); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := lib.ExportCard(ctx, card)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Spec string `json:"spec"`
		Data struct {
			Name string `json:"name"`
			Book struct {
				Name    string `json:"name"`
				Entries []struct {
					Content string `json:"content"`
				} `json:"entries"`
			} `json:"character_book"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out.Spec != "chara_card_v3" || out.Data.Name != "Brakka" || out.Data.Book.Name != "Forge" ||
		len(out.Data.Book.Entries) != 2 || out.Data.Book.Entries[1].Content != "Ironfist." {
		t.Fatalf("export = %s", raw)
	}

	plain, _ := newCard(t, lib, "plain", `{"name": "Plain"}`)
	raw, err = lib.ExportCard(ctx, plain)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "character_book") {
		t.Fatalf("card without lorebooks exported a book: %s", raw)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/library/`
Expected: FAIL to compile: `lib.Cards.CreateFromName undefined`.

- [ ] **Step 3: Check overrides**

In `internal/library/entities.go`, in `prepare`, replace

```go
	if r.overrides == nil {
		r.overrides = []byte(`{}`)
	}
```

with

```go
	if r.overrides == nil {
		r.overrides = []byte(`{}`)
	}
	var overrides map[string]json.RawMessage
	if err := json.Unmarshal(r.overrides, &overrides); err != nil || overrides == nil {
		return revisionRow{}, fmt.Errorf("%w: overrides must be a JSON object", ErrInvalid)
	}
```

- [ ] **Step 4: Add `CreateFromName`**

In `internal/library/import.go`, replace the retry loop at the end of `Import` (from `// Retried because` to the end of the function) with:

```go
	ent, _, err := s.CreateFromName(ctx, name, provenance, e)
	if err != nil {
		return 0, false, err
	}
	return ent.ID, true, nil
}

// CreateFromName creates an entity with a free slug made from name.
func (s *Entities) CreateFromName(ctx context.Context, name, provenance string, e Edit) (Entity, Revision, error) {
	// Retried because another writer can take the same slug first.
	for range 5 {
		slug, err := s.freeSlug(ctx, Slugify(name))
		if err != nil {
			return Entity{}, Revision{}, err
		}
		ent, rev, err := s.Create(ctx, slug, provenance, e)
		if !errors.Is(err, ErrSlugTaken) {
			return ent, rev, err
		}
	}
	return Entity{}, Revision{}, ErrSlugTaken
}
```

- [ ] **Step 5: Add tag counts and collection management**

In `internal/library/library.go`, add to the `var` block:

```go
	// ErrNameTaken means another collection has this name.
	ErrNameTaken = errors.New("name already in use")
```

In `internal/library/organize.go`, replace the `Collection` type and `CreateCollection` with:

```go
// Collection is a named group of cards. A card can be in many.
type Collection struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
	// Cards is how many cards it has. Only Collections sets it.
	Cards int `json:"cards"`
}

// collectionErr maps constraint violations on collection.
func collectionErr(err error) error {
	var pgErr *pgconn.PgError
	switch {
	case errors.As(err, &pgErr) && pgErr.Code == "23505":
		return ErrNameTaken
	case errors.As(err, &pgErr) && pgErr.Code == "23514":
		return fmt.Errorf("%w: collection name may not be empty", ErrInvalid)
	}
	return mapErr(err)
}

// CreateCollection adds an empty collection.
func (l *Library) CreateCollection(ctx context.Context, name, description string) (Collection, error) {
	var c Collection
	err := l.pool.QueryRow(ctx, `INSERT INTO collection (name, description) VALUES ($1, $2)
		RETURNING id, name, description, created_at`, name, description).
		Scan(&c.ID, &c.Name, &c.Description, &c.CreatedAt)
	return c, collectionErr(err)
}

// UpdateCollection renames a collection or changes its description. Nil
// leaves a field as it is.
func (l *Library) UpdateCollection(ctx context.Context, id int64, name, description *string) (Collection, error) {
	var c Collection
	err := l.pool.QueryRow(ctx, `UPDATE collection SET name = coalesce($2, name), description = coalesce($3, description)
		WHERE id = $1 RETURNING id, name, description, created_at`, id, name, description).
		Scan(&c.ID, &c.Name, &c.Description, &c.CreatedAt)
	return c, collectionErr(err)
}

// DeleteCollection removes a collection. Its cards stay in the library.
func (l *Library) DeleteCollection(ctx context.Context, id int64) error {
	tag, err := l.pool.Exec(ctx, `DELETE FROM collection WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Collections returns every collection with its card count, by name.
func (l *Library) Collections(ctx context.Context) ([]Collection, error) {
	rows, err := l.pool.Query(ctx, `SELECT c.id, c.name, c.description, c.created_at, count(cc.card_id)::int
		FROM collection c LEFT JOIN collection_card cc ON cc.collection_id = c.id
		GROUP BY c.id ORDER BY lower(c.name)`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Collection])
}

// TagCount is a tag and how many cards have it.
type TagCount struct {
	Name  string `json:"name"`
	Cards int    `json:"cards"`
}

// Tags returns every tag with its card count, alphabetically.
func (l *Library) Tags(ctx context.Context) ([]TagCount, error) {
	rows, err := l.pool.Query(ctx, `SELECT t.name, count(ct.card_id)::int
		FROM tag t LEFT JOIN card_tag ct ON ct.tag_id = t.id
		GROUP BY t.id ORDER BY lower(t.name)`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[TagCount])
}
```

Add `"errors"`, `"fmt"`, and `"github.com/jackc/pgx/v5/pgconn"` to its imports.

- [ ] **Step 6: Add `OpenBlob` and JSON names**

In `internal/library/blobs.go`, give `Blob` JSON names:

```go
// Blob is a stored file.
type Blob struct {
	SHA256      string    `json:"sha256"`
	Size        int64     `json:"size"`
	MIME        string    `json:"mime"`
	Store       string    `json:"store"`
	AvatarPhash *int64    `json:"avatar_phash,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}
```

and append:

```go
// OpenBlob returns a blob's record and its file. The caller closes the file.
func (l *Library) OpenBlob(ctx context.Context, sha string) (Blob, *os.File, error) {
	b, err := l.Blob(ctx, sha)
	if err != nil {
		return Blob{}, nil, err
	}
	path, err := l.blobs.Path(sha)
	if err != nil {
		return Blob{}, nil, err
	}
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Blob{}, nil, ErrNotFound
	}
	return b, f, err
}
```

Add `"errors"` and `"io/fs"` to its imports.

In `internal/library/similar.go`, give `Match` JSON names:

```go
type Match struct {
	CardID int64 `json:"card_id"`
	// Reason is content (same content_hash), text (text_simhash), or avatar
	// (avatar_phash).
	Reason string `json:"reason"`
	// Distance is the Hamming distance between hashes; 0 for content.
	Distance int `json:"distance"`
}
```

- [ ] **Step 7: Add the export**

Create `internal/library/export.go`:

```go
package library

import (
	"context"
	"encoding/json"
)

// ExportCard returns a card's head revision as a CCv3 card. Its linked
// lorebooks become data.character_book: the first one's fields, with every
// linked lorebook's entries.
//
// ponytail: merged entries keep their own id fields, which can repeat across
// lorebooks; renumber them if an app minds.
func (l *Library) ExportCard(ctx context.Context, id int64) (json.RawMessage, error) {
	c, err := l.Cards.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	rev, err := l.Cards.Revision(ctx, c.HeadRevisionID)
	if err != nil {
		return nil, err
	}
	var data map[string]json.RawMessage
	if err := json.Unmarshal(rev.Data, &data); err != nil {
		return nil, err
	}
	links, err := l.CardLorebooks(ctx, id)
	if err != nil {
		return nil, err
	}
	var book map[string]any
	entries := []any{}
	for _, lid := range links {
		lb, err := l.Lorebooks.Get(ctx, lid)
		if err != nil {
			return nil, err
		}
		lrev, err := l.Lorebooks.Revision(ctx, lb.HeadRevisionID)
		if err != nil {
			return nil, err
		}
		var b map[string]any
		if err := json.Unmarshal(lrev.Data, &b); err != nil {
			return nil, err
		}
		if book == nil {
			book = b
		}
		if e, ok := b["entries"].([]any); ok {
			entries = append(entries, e...)
		}
	}
	if book != nil {
		book["entries"] = entries
		if data["character_book"], err = json.Marshal(book); err != nil {
			return nil, err
		}
	}
	return json.Marshal(map[string]any{"spec": "chara_card_v3", "spec_version": "3.0", "data": data})
}
```

- [ ] **Step 8: Run the tests to verify they pass**

Run: `go test ./internal/library/ ./internal/importer/`
Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/library
git commit -m "Add tag counts, collection editing, blob reads, and CCv3 export"
```

---

### Task 7: Search

**Files:**
- Create: `internal/library/search.go`
- Test: `internal/library/search_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/library/search_test.go`:

```go
package library

import (
	"context"
	"errors"
	"slices"
	"testing"
)

type searchCards struct{ brakka, carrow, dellin int64 }

func seedSearch(t *testing.T, lib *Library) searchCards {
	t.Helper()
	ctx := context.Background()
	var s searchCards
	s.brakka, _ = newCard(t, lib, "brakka", `{"name": "Brakka", "description": "A grumpy dwarf blacksmith."}`)
	s.carrow, _ = newCard(t, lib, "carrow", `{"name": "Carrow", "description": "A sailor who owes Brakka money."}`)
	s.dellin, _ = newCard(t, lib, "dellin", `{"name": "Dellin", "description": "A retired dwarf blacksmith."}`)
	if err := lib.SetCardTags(ctx, s.brakka, []string{"fantasy"}); err != nil {
		t.Fatal(err)
	}
	if err := lib.SetCardTags(ctx, s.carrow, []string{"pirate"}); err != nil {
		t.Fatal(err)
	}
	if err := lib.Cards.SetArchived(ctx, s.dellin, true); err != nil {
		t.Fatal(err)
	}
	return s
}

func hitIDs(r Results) []int64 {
	out := []int64{}
	for _, h := range r.Hits {
		out = append(out, h.ID)
	}
	return out
}

func ptr[T any](v T) *T { return &v }

func TestSearch(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	s := seedSearch(t, lib)

	for name, tc := range map[string]struct {
		q    Query
		want []int64
	}{
		"everything not archived, newest first": {Query{}, []int64{s.carrow, s.brakka}},
		"text, stemmed":                         {Query{Text: "blacksmiths"}, []int64{s.brakka}},
		"text ranks names first":                {Query{Text: "brakka"}, []int64{s.brakka, s.carrow}},
		"text terms are ANDed":                  {Query{Text: "sailor blacksmith"}, []int64{}},
		"tag, any case":                         {Query{Filter: &Filter{Tag: ptr("Fantasy")}}, []int64{s.brakka}},
		"not tag":                               {Query{Filter: &Filter{Not: &Filter{Tag: ptr("fantasy")}}}, []int64{s.carrow}},
		"archived only":                         {Query{Filter: &Filter{Archived: ptr(true)}}, []int64{s.dellin}},
		"archived or not": {Query{Text: "blacksmith", Filter: &Filter{Or: []Filter{
			{Archived: ptr(true)}, {Archived: ptr(false)}}}}, []int64{s.dellin, s.brakka}},
		"and": {Query{Filter: &Filter{And: []Filter{{Tag: ptr("fantasy")}, {Tag: ptr("pirate")}}}}, []int64{}},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := lib.Search(ctx, tc.q)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(hitIDs(got), tc.want) {
				t.Fatalf("hits = %v, want %v", hitIDs(got), tc.want)
			}
		})
	}
}

func TestSearchCollection(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	s := seedSearch(t, lib)
	c, err := lib.CreateCollection(ctx, "Crew", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.AddToCollection(ctx, c.ID, s.carrow); err != nil {
		t.Fatal(err)
	}
	got, err := lib.Search(ctx, Query{Filter: &Filter{Collection: &c.ID}})
	if err != nil || !slices.Equal(hitIDs(got), []int64{s.carrow}) {
		t.Fatalf("hits = %v, %v", hitIDs(got), err)
	}
}

func TestSearchOutput(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	s := seedSearch(t, lib)
	got, err := lib.Search(ctx, Query{Filter: &Filter{Tag: ptr("fantasy")},
		Fields: []string{"name", "avatar", "missing"}, Include: []string{"tags", "lorebooks"}})
	if err != nil {
		t.Fatal(err)
	}
	h := got.Hits[0]
	if h.ID != s.brakka || h.Slug != "brakka" || h.Fields["name"] != "Brakka" || len(h.Fields) != 1 ||
		!slices.Equal(h.Tags, []string{"fantasy"}) || h.Lorebooks == nil {
		t.Fatalf("hit = %+v", h)
	}
	plain, _ := lib.Search(ctx, Query{})
	if plain.Hits[0].Fields["name"] == nil || plain.Hits[0].Tags != nil {
		t.Fatalf("default output = %+v", plain.Hits[0])
	}
}

func TestSearchPaging(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	s := seedSearch(t, lib)
	first, err := lib.Search(ctx, Query{Page: Page{Size: 1}})
	if err != nil || !slices.Equal(hitIDs(first), []int64{s.carrow}) || first.NextCursor == nil {
		t.Fatalf("first page = %+v, %v", first, err)
	}
	second, err := lib.Search(ctx, Query{Page: Page{Size: 1, Cursor: first.NextCursor}})
	if err != nil || !slices.Equal(hitIDs(second), []int64{s.brakka}) || second.NextCursor != nil {
		t.Fatalf("second page = %+v, %v", second, err)
	}
}

func TestSearchRejectsBadQueries(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	for name, q := range map[string]Query{
		"two conditions": {Filter: &Filter{Tag: ptr("a"), Archived: ptr(true)}},
		"no condition":   {Filter: &Filter{Not: &Filter{}}},
		"semantic":       {Semantic: true},
		"catalog source": {Sources: []string{"catalog:chub-archive"}},
		"bad include":    {Include: []string{"enrichments"}},
		"bad cursor":     {Page: Page{Cursor: ptr("abc")}},
	} {
		if _, err := lib.Search(ctx, q); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: got %v, want ErrInvalid", name, err)
		}
	}
}

func TestSearchIDs(t *testing.T) {
	lib := newLibrary(t)
	s := seedSearch(t, lib)
	got, err := lib.SearchIDs(context.Background(), Query{Text: "brakka", Page: Page{Size: 1}})
	if err != nil {
		t.Fatal(err)
	}
	// Paging is ignored, and the archived card is left out.
	if !slices.Equal(got, []int64{s.brakka, s.carrow}) {
		t.Fatalf("ids = %v", got)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -run 'TestSearch' ./internal/library/`
Expected: FAIL to compile: `undefined: Query`.

- [ ] **Step 3: Write the implementation**

Create `internal/library/search.go`:

```go
package library

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Query is a structured card search, as POST /search takes it. Bulk actions
// select cards with it too.
type Query struct {
	Text     string   `json:"text,omitempty" doc:"Words to match in name, description, personality, scenario, creator notes, and first message. Supports \"quoted phrases\", or, and -excluded words."`
	Semantic bool     `json:"semantic,omitempty" doc:"Not available yet: needs an embedding enricher."`
	Filter   *Filter  `json:"filter,omitempty"`
	Sources  []string `json:"sources,omitempty" doc:"Only library for now."`
	Include  []string `json:"include,omitempty" doc:"Extra data per hit: tags, lorebooks."`
	Fields   []string `json:"fields,omitempty" doc:"Card data keys to return per hit, plus avatar. Default: name."`
	Page     Page     `json:"page,omitempty"`
}

// Page selects one page of results.
type Page struct {
	Size   int     `json:"size,omitempty" minimum:"0" maximum:"500" doc:"Default 50."`
	Cursor *string `json:"cursor,omitempty" nullable:"true" doc:"next_cursor from the previous page."`
}

// Filter is one condition on a card. Set exactly one field. Archived cards
// are left out unless a filter mentions archived.
type Filter struct {
	And        []Filter `json:"and,omitempty"`
	Or         []Filter `json:"or,omitempty"`
	Not        *Filter  `json:"not,omitempty"`
	Tag        *string  `json:"tag,omitempty" doc:"Has this tag, in any case."`
	Collection *int64   `json:"collection,omitempty" doc:"Is in this collection."`
	Archived   *bool    `json:"archived,omitempty"`
}

// Hit is one matching card.
type Hit struct {
	ID         int64          `json:"id"`
	Slug       string         `json:"slug"`
	RevisionID int64          `json:"revision_id"`
	ArchivedAt *time.Time     `json:"archived_at,omitempty"`
	Fields     map[string]any `json:"fields"`
	Tags       []string       `json:"tags,omitempty"`
	Lorebooks  []int64        `json:"lorebooks,omitempty"`
}

// Results is one page of hits.
type Results struct {
	Hits       []Hit   `json:"hits"`
	NextCursor *string `json:"next_cursor,omitempty"`
}

// where collects SQL conditions and their arguments.
type where struct{ args []any }

func (w *where) arg(v any) string {
	w.args = append(w.args, v)
	return "$" + strconv.Itoa(len(w.args))
}

func (w *where) filter(f Filter) (string, error) {
	set := 0
	for _, ok := range []bool{f.And != nil, f.Or != nil, f.Not != nil, f.Tag != nil, f.Collection != nil, f.Archived != nil} {
		if ok {
			set++
		}
	}
	if set != 1 {
		return "", fmt.Errorf("%w: each filter needs exactly one condition", ErrInvalid)
	}
	switch {
	case f.And != nil:
		return w.join(f.And, " AND ", "TRUE")
	case f.Or != nil:
		return w.join(f.Or, " OR ", "FALSE")
	case f.Not != nil:
		s, err := w.filter(*f.Not)
		return "NOT (" + s + ")", err
	case f.Tag != nil:
		return `EXISTS (SELECT 1 FROM card_tag ct JOIN tag t ON t.id = ct.tag_id
			WHERE ct.card_id = c.id AND lower(t.name) = lower(` + w.arg(*f.Tag) + `))`, nil
	case f.Collection != nil:
		return `EXISTS (SELECT 1 FROM collection_card cc
			WHERE cc.card_id = c.id AND cc.collection_id = ` + w.arg(*f.Collection) + `)`, nil
	}
	return `((c.archived_at IS NOT NULL) = ` + w.arg(*f.Archived) + `)`, nil
}

func (w *where) join(fs []Filter, op, empty string) (string, error) {
	if len(fs) == 0 {
		return empty, nil
	}
	parts := make([]string, len(fs))
	for i, f := range fs {
		s, err := w.filter(f)
		if err != nil {
			return "", err
		}
		parts[i] = s
	}
	return "(" + strings.Join(parts, op) + ")", nil
}

func mentionsArchived(f *Filter) bool {
	if f == nil {
		return false
	}
	if f.Archived != nil || mentionsArchived(f.Not) {
		return true
	}
	return slices.ContainsFunc(f.And, func(g Filter) bool { return mentionsArchived(&g) }) ||
		slices.ContainsFunc(f.Or, func(g Filter) bool { return mentionsArchived(&g) })
}

// compile returns the WHERE condition for q over card c joined to its head
// revision r, and the placeholder holding the text query, if any.
func compile(q Query) (string, *where, string, error) {
	if q.Semantic {
		return "", nil, "", fmt.Errorf("%w: semantic search needs an embedding enricher, and none is installed", ErrInvalid)
	}
	for _, s := range q.Sources {
		if s != "library" {
			return "", nil, "", fmt.Errorf("%w: unknown source %q", ErrInvalid, s)
		}
	}
	w := &where{}
	conds := []string{"TRUE"}
	if q.Filter != nil {
		s, err := w.filter(*q.Filter)
		if err != nil {
			return "", nil, "", err
		}
		conds = append(conds, s)
	}
	if !mentionsArchived(q.Filter) {
		conds = append(conds, "c.archived_at IS NULL")
	}
	text := ""
	if q.Text != "" {
		text = w.arg(q.Text)
		conds = append(conds, "r.search @@ websearch_to_tsquery('english', "+text+")")
	}
	return strings.Join(conds, " AND "), w, text, nil
}

// Search returns one page of the library's cards that match q, best match
// first when q has text, newest first otherwise.
//
// ponytail: the cursor is an offset, so a page can repeat or skip a card
// that was added or archived since the last page; switch to keyset paging
// if that matters.
func (l *Library) Search(ctx context.Context, q Query) (Results, error) {
	cond, w, text, err := compile(q)
	if err != nil {
		return Results{}, err
	}
	size := q.Page.Size
	if size == 0 {
		size = 50
	}
	offset := 0
	if q.Page.Cursor != nil {
		if offset, err = strconv.Atoi(*q.Page.Cursor); err != nil || offset < 0 {
			return Results{}, fmt.Errorf("%w: bad cursor", ErrInvalid)
		}
	}
	fields := q.Fields
	if len(fields) == 0 {
		fields = []string{"name"}
	}
	tags, books := `NULL::text[]`, `NULL::bigint[]`
	for _, inc := range q.Include {
		switch inc {
		case "tags":
			tags = `array(SELECT t.name FROM card_tag ct JOIN tag t ON t.id = ct.tag_id
				WHERE ct.card_id = c.id ORDER BY lower(t.name))`
		case "lorebooks":
			books = `array(SELECT lorebook_id FROM card_lorebook WHERE card_id = c.id ORDER BY lorebook_id)`
		default:
			return Results{}, fmt.Errorf("%w: unknown include %q", ErrInvalid, inc)
		}
	}
	order := "c.id DESC"
	if text != "" {
		order = "ts_rank(r.search, websearch_to_tsquery('english', " + text + ")) DESC, c.id DESC"
	}
	sql := `SELECT c.id, c.slug, r.id, c.archived_at, r.data, r.avatar_blob, ` + tags + `, ` + books + `
		FROM card c JOIN card_revision r ON r.id = c.head_revision_id
		WHERE ` + cond + ` ORDER BY ` + order + ` LIMIT ` + w.arg(size+1) + ` OFFSET ` + w.arg(offset)
	rows, err := l.pool.Query(ctx, sql, w.args...)
	if err != nil {
		return Results{}, err
	}
	res := Results{Hits: []Hit{}}
	var h Hit
	var data []byte
	var avatar *string
	_, err = pgx.ForEachRow(rows, []any{&h.ID, &h.Slug, &h.RevisionID, &h.ArchivedAt, &data, &avatar, &h.Tags, &h.Lorebooks},
		func() error {
			var all map[string]any
			if err := json.Unmarshal(data, &all); err != nil {
				return err
			}
			h.Fields = map[string]any{}
			for _, f := range fields {
				if f == "avatar" && avatar != nil {
					h.Fields[f] = *avatar
				} else if v, ok := all[f]; ok {
					h.Fields[f] = v
				}
			}
			res.Hits = append(res.Hits, h)
			h = Hit{}
			return nil
		})
	if err != nil {
		return Results{}, err
	}
	if len(res.Hits) > size {
		res.Hits = res.Hits[:size]
		next := strconv.Itoa(offset + size)
		res.NextCursor = &next
	}
	return res, nil
}

// SearchIDs returns the IDs of every card q matches, ignoring its paging and
// output options.
func (l *Library) SearchIDs(ctx context.Context, q Query) ([]int64, error) {
	cond, w, _, err := compile(q)
	if err != nil {
		return nil, err
	}
	rows, err := l.pool.Query(ctx, `SELECT c.id FROM card c JOIN card_revision r ON r.id = c.head_revision_id
		WHERE `+cond+` ORDER BY c.id`, w.args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[int64])
}
```

`pgx.ForEachRow` scans into the same variables for every row. Resetting `h` after each append keeps one row's `Tags` from leaking into the next when a value is NULL.

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test -run 'TestSearch' ./internal/library/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/library/search.go internal/library/search_test.go
git commit -m "Add structured card search with full text, filters, and paging"
```

---

### Task 8: Tokens and sessions

**Files:**
- Create: `internal/auth/auth.go`
- Test: `internal/auth/auth_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/auth/auth_test.go`:

```go
package auth

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/davidgibbons/innkeeper/internal/testdb"
)

func TestTokens(t *testing.T) {
	pool := testdb.New(t)
	a := New(pool)
	ctx := context.Background()
	tok, secret, err := a.CreateToken(ctx, "cli")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(secret, "ink_") || tok.Name != "cli" {
		t.Fatalf("token = %+v, %q", tok, secret)
	}
	var stored string
	if err := pool.QueryRow(ctx, `SELECT token_hash FROM api_token`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored, secret) {
		t.Fatal("database holds the token itself")
	}

	for _, s := range []string{secret, "ink_wrong", ""} {
		ok, err := a.CheckToken(ctx, s)
		if err != nil || ok != (s == secret) {
			t.Fatalf("CheckToken(%q) = %v, %v", s, ok, err)
		}
	}
	list, err := a.Tokens(ctx)
	if err != nil || len(list) != 1 || list[0].LastUsedAt == nil {
		t.Fatalf("tokens = %+v, %v", list, err)
	}

	if err := a.RevokeToken(ctx, tok.ID); err != nil {
		t.Fatal(err)
	}
	if ok, _ := a.CheckToken(ctx, secret); ok {
		t.Fatal("revoked token still works")
	}
	if err := a.RevokeToken(ctx, tok.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second revoke: %v", err)
	}
}

func loginInstance(t *testing.T, pool *pgxpool.Pool) int64 {
	t.Helper()
	var id int64
	err := pool.QueryRow(context.Background(), `INSERT INTO plugin_instance (plugin, name, owner_subject)
		VALUES ('fake', 'Login', 'owner') RETURNING id`).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestSessions(t *testing.T) {
	pool := testdb.New(t)
	a := New(pool)
	ctx := context.Background()
	inst := loginInstance(t, pool)

	if _, err := a.CreateSession(ctx, inst, "someone-else"); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("non-owner login: %v", err)
	}
	if _, err := a.CreateSession(ctx, inst+1, "owner"); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("login through missing instance: %v", err)
	}
	secret, err := a.CreateSession(ctx, inst, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := a.CheckSession(ctx, secret); !ok || err != nil {
		t.Fatalf("CheckSession = %v, %v", ok, err)
	}

	// Changing the owner ends the old owner's sessions.
	if _, err := pool.Exec(ctx, `UPDATE plugin_instance SET owner_subject = 'new-owner'`); err != nil {
		t.Fatal(err)
	}
	if ok, _ := a.CheckSession(ctx, secret); ok {
		t.Fatal("session survived an owner change")
	}
	if _, err := pool.Exec(ctx, `UPDATE plugin_instance SET owner_subject = 'owner'`); err != nil {
		t.Fatal(err)
	}

	if _, err := pool.Exec(ctx, `UPDATE session SET expires_at = now() - interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	if ok, _ := a.CheckSession(ctx, secret); ok {
		t.Fatal("expired session still works")
	}

	secret, _ = a.CreateSession(ctx, inst, "owner")
	if err := a.DeleteSession(ctx, secret); err != nil {
		t.Fatal(err)
	}
	if ok, _ := a.CheckSession(ctx, secret); ok {
		t.Fatal("deleted session still works")
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM session`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("%d sessions left; the expired one should be cleaned up at login", n)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/auth/`
Expected: FAIL to compile: `undefined: New`.

- [ ] **Step 3: Write the implementation**

Create `internal/auth/auth.go`:

```go
// Package auth issues and checks API tokens and login sessions. Only the
// SHA-256 of each secret is stored.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound = errors.New("token not found")
	// ErrNotOwner means a login provider verified someone other than the
	// owner it names.
	ErrNotOwner = errors.New("identity is not the owner")
)

// SessionTTL is how long a login lasts.
const SessionTTL = 30 * 24 * time.Hour

// Auth stores tokens and sessions.
type Auth struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Auth { return &Auth{pool: pool} }

// Token is an API token's record. The secret is shown only when created.
type Token struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

// newSecret returns a random secret starting with prefix, and its hash.
func newSecret(prefix string) (string, string) {
	b := make([]byte, 32)
	rand.Read(b)
	s := prefix + base64.RawURLEncoding.EncodeToString(b)
	return s, hash(s)
}

func hash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// CreateToken makes an API token and returns its record and secret.
func (a *Auth) CreateToken(ctx context.Context, name string) (Token, string, error) {
	secret, h := newSecret("ink_")
	var t Token
	err := a.pool.QueryRow(ctx, `INSERT INTO api_token (name, token_hash) VALUES ($1, $2)
		RETURNING id, name, created_at, last_used_at`, name, h).Scan(&t.ID, &t.Name, &t.CreatedAt, &t.LastUsedAt)
	return t, secret, err
}

// Tokens returns every API token, oldest first.
func (a *Auth) Tokens(ctx context.Context) ([]Token, error) {
	rows, err := a.pool.Query(ctx, `SELECT id, name, created_at, last_used_at FROM api_token ORDER BY id`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Token])
}

// RevokeToken deletes an API token.
func (a *Auth) RevokeToken(ctx context.Context, id int64) error {
	tag, err := a.pool.Exec(ctx, `DELETE FROM api_token WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// CheckToken reports whether secret is an API token, and records its use at
// most once a minute, so reads don't each cost a write.
func (a *Auth) CheckToken(ctx context.Context, secret string) (bool, error) {
	var ok bool
	err := a.pool.QueryRow(ctx, `WITH t AS (SELECT id, last_used_at FROM api_token WHERE token_hash = $1),
		used AS (UPDATE api_token SET last_used_at = now() WHERE id IN (
			SELECT id FROM t WHERE last_used_at IS NULL OR last_used_at < now() - interval '1 minute'))
		SELECT EXISTS (SELECT 1 FROM t)`, hash(secret)).Scan(&ok)
	return ok, err
}

// CreateSession logs in subject, verified by an auth instance, if that
// instance names subject as the owner. It returns the session's secret.
func (a *Auth) CreateSession(ctx context.Context, instanceID int64, subject string) (string, error) {
	secret, h := newSecret("")
	tag, err := a.pool.Exec(ctx, `INSERT INTO session (token_hash, instance_id, subject, expires_at)
		SELECT $1, id, $3, now() + make_interval(secs => $4) FROM plugin_instance
		WHERE id = $2 AND owner_subject = $3`, h, instanceID, subject, SessionTTL.Seconds())
	if err != nil {
		return "", err
	}
	if tag.RowsAffected() == 0 {
		return "", ErrNotOwner
	}
	if _, err := a.pool.Exec(ctx, `DELETE FROM session WHERE expires_at < now()`); err != nil {
		return "", err
	}
	return secret, nil
}

// CheckSession reports whether secret is a live session whose instance still
// names its subject as the owner.
func (a *Auth) CheckSession(ctx context.Context, secret string) (bool, error) {
	var ok bool
	err := a.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM session s JOIN plugin_instance i ON i.id = s.instance_id
		WHERE s.token_hash = $1 AND s.expires_at > now() AND i.owner_subject = s.subject)`, hash(secret)).Scan(&ok)
	return ok, err
}

// DeleteSession logs a session out.
func (a *Auth) DeleteSession(ctx context.Context, secret string) error {
	_, err := a.pool.Exec(ctx, `DELETE FROM session WHERE token_hash = $1`, hash(secret))
	return err
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/auth/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/auth
git commit -m "Add API tokens and owner sessions"
```

---

### Task 9: The `auth` capability

**Files:**
- Modify: `protocol/messages.go`
- Modify: `internal/fakeplugin/main.go`
- Modify: `docs/protocol.md`

No test in this task: Task 10's login tests run the fake provider through the API.

- [ ] **Step 1: Add the messages**

Append to `protocol/messages.go`:

```go
// The auth capability. A login calls auth.begin, then auth.complete with
// what the user sent back.
const (
	MethodAuthBegin    = "auth.begin"
	MethodAuthComplete = "auth.complete"
)

// AuthBeginParams starts a login. CallbackURL is where a redirect login must
// send the browser back to, or empty if the core has no public URL.
type AuthBeginParams struct {
	CallbackURL string `json:"callback_url"`
}

// AuthBeginResult says how to log in: send the browser to Redirect, or show
// Form, a JSON Schema of the fields to ask for. State comes back unchanged
// in auth.complete.
type AuthBeginResult struct {
	Redirect string          `json:"redirect,omitempty"`
	Form     json.RawMessage `json:"form,omitempty"`
	State    string          `json:"state,omitempty"`
}

// AuthCompleteParams carries what the user sent back: the form's fields, or
// the query parameters of the redirect to CallbackURL.
type AuthCompleteParams struct {
	Params      map[string]string `json:"params"`
	State       string            `json:"state"`
	CallbackURL string            `json:"callback_url"`
}

// AuthCompleteResult is the verified identity. Subject must be stable for
// the same person on the same instance.
type AuthCompleteResult struct {
	Subject string `json:"subject"`
	Name    string `json:"name,omitempty"`
}
```

- [ ] **Step 2: Make the fake plugin a login provider**

In `internal/fakeplugin/main.go`, add these cases to the method `switch`, after the `protocol.MethodImportRun` case:

```go
		case protocol.MethodAuthBegin:
			return protocol.AuthBeginResult{
				Form:  json.RawMessage(`{"type": "object", "properties": {"password": {"type": "string"}}, "required": ["password"]}`),
				State: "fake-state",
			}, nil
		case protocol.MethodAuthComplete:
			var p protocol.AuthCompleteParams
			_ = json.Unmarshal(params, &p)
			if p.State != "fake-state" {
				return nil, protocol.NewError(-32000, "login state doesn't match", false)
			}
			switch p.Params["password"] {
			case "letmein":
				return protocol.AuthCompleteResult{Subject: "owner", Name: "Owner"}, nil
			case "stranger":
				return protocol.AuthCompleteResult{Subject: "someone-else"}, nil
			}
			return nil, protocol.NewError(-32000, "wrong password", false)
```

- [ ] **Step 3: Document the capability**

In `docs/protocol.md`, add this section after the `import` section, before `## Checking a plugin`:

````markdown
### `auth`

A login provider verifies who is logging in. The core decides whether that
person is the owner: each auth instance names its owner's `subject`.

**`auth.begin`** starts a login.

| Param | Meaning |
|---|---|
| `callback_url` | Where a redirect login sends the browser back to. Empty if the core has no public URL |

Reply with one of:

- `form`: a JSON Schema object of the fields to ask for, such as a password.
- `redirect`: a URL to send the browser to, such as an OAuth provider's.

Add `state` to have it passed back to `auth.complete`, such as an OAuth
state value. The core keeps it in a cookie for 10 minutes. Keep nothing
about the login in memory: `auth.complete` may reach a different process.

**`auth.complete`** finishes it.

| Param | Meaning |
|---|---|
| `params` | The form's fields, or the query parameters of the redirect back to `callback_url` |
| `state` | The `state` from `auth.begin` |
| `callback_url` | As in `auth.begin` |

Reply with `subject`, a stable ID for this person, and optionally `name`.
Reply with an error if the login failed. The core answers every failure the
same way and waits a second, so don't add your own delay.

```json
{"jsonrpc": "2.0", "id": 3, "method": "auth.complete", "params": {"params": {"password": "…"}, "state": "", "callback_url": ""}}
{"jsonrpc": "2.0", "id": 3, "result": {"subject": "owner"}}
```
````

- [ ] **Step 4: Check it builds**

Run: `go vet ./protocol/ ./internal/fakeplugin/ && go test ./internal/importer/ ./internal/contract/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add protocol/messages.go internal/fakeplugin/main.go docs/protocol.md
git commit -m "Define the auth capability"
```

---

### Task 10: API server and login

**Files:**
- Modify: `go.mod`, `go.sum`
- Create: `internal/api/server.go`
- Create: `internal/api/auth.go`
- Test: `internal/api/api_test.go` (test helpers)
- Test: `internal/api/auth_test.go`

- [ ] **Step 1: Add Huma**

```bash
go get github.com/danielgtaylor/huma/v2@v2.39.1
```

- [ ] **Step 2: Write the test helpers**

Create `internal/api/api_test.go`:

```go
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/davidgibbons/innkeeper/internal/auth"
	"github.com/davidgibbons/innkeeper/internal/blobstore"
	"github.com/davidgibbons/innkeeper/internal/jobs"
	"github.com/davidgibbons/innkeeper/internal/library"
	"github.com/davidgibbons/innkeeper/internal/pluginhost"
	"github.com/davidgibbons/innkeeper/internal/plugins"
	"github.com/davidgibbons/innkeeper/internal/secrets"
	"github.com/davidgibbons/innkeeper/internal/testdb"
	"github.com/davidgibbons/innkeeper/internal/testutil"
	"github.com/davidgibbons/innkeeper/protocol"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

type testEnv struct {
	t     *testing.T
	s     *Server
	srv   *httptest.Server
	pool  *pgxpool.Pool
	token string
	// loginID is a fake login provider whose owner subject is "owner".
	loginID int64
	// importID is a fake plugin instance that can import.
	importID int64
}

// newEnv serves the API over a fresh database, with the fake plugin and a
// "vault" plugin that has an api_key secret but is never run.
func newEnv(t *testing.T) *testEnv {
	t.Helper()
	failedLoginDelay = 0
	ctx := context.Background()
	pool := testdb.New(t)
	store := &blobstore.Store{Name: "library", Root: t.TempDir()}
	box, err := secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	fake, err := protocol.LoadManifest(testutil.FakePluginWith(t, []string{"auth", "import"}))
	if err != nil {
		t.Fatal(err)
	}
	vault := &protocol.Manifest{Name: "vault", Version: "1.0.0", Protocol: protocol.Version,
		Command: []string{"./vault"}, Capabilities: []string{"push"}, Secrets: []string{"api_key"}}
	reg := plugins.New(pool, map[string]*protocol.Manifest{"fake": fake, "vault": vault}, store, box)
	mgr := pluginhost.NewManager(NoCore, time.Minute, quiet)
	t.Cleanup(mgr.Close)
	e := &testEnv{t: t, pool: pool, s: &Server{Lib: library.New(pool, store), Queue: jobs.New(pool),
		Registry: reg, Auth: auth.New(pool), Plugins: mgr, Log: quiet}}
	e.srv = httptest.NewServer(e.s.Handler())
	t.Cleanup(e.srv.Close)
	if _, e.token, err = e.s.Auth.CreateToken(ctx, "test"); err != nil {
		t.Fatal(err)
	}
	owner := "owner"
	if e.loginID, err = reg.CreateInstance(ctx, plugins.NewInstance{Plugin: "fake", Name: "Login", OwnerSubject: &owner}); err != nil {
		t.Fatal(err)
	}
	if e.importID, err = reg.CreateInstance(ctx, plugins.NewInstance{Plugin: "fake", Name: "Importer"}); err != nil {
		t.Fatal(err)
	}
	return e
}

type result struct {
	Status int
	Header http.Header
	Body   []byte
}

func (r result) String() string { return fmt.Sprintf("%d %s", r.Status, r.Body) }

// cookie returns the cookie named name that the response set.
func (r result) cookie(t *testing.T, name string) *http.Cookie {
	t.Helper()
	for _, line := range r.Header.Values("Set-Cookie") {
		if c, err := http.ParseSetCookie(line); err == nil && c.Name == name {
			return c
		}
	}
	t.Fatalf("response set no %s cookie: %v", name, r.Header.Values("Set-Cookie"))
	return nil
}

// client returns redirects to the test instead of following them. The
// timeout ends a job stream that never finishes.
var client = &http.Client{
	Timeout:       30 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// send makes a request without credentials. A []byte body is sent as
// application/octet-stream, anything else as JSON. headers are name, value
// pairs.
func (e *testEnv) send(method, path string, body any, headers ...string) result {
	e.t.Helper()
	var r io.Reader
	contentType := "application/json"
	switch b := body.(type) {
	case nil:
	case []byte:
		r, contentType = bytes.NewReader(b), "application/octet-stream"
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			e.t.Fatal(err)
		}
		r = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, e.srv.URL+path, r)
	if err != nil {
		e.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", contentType)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := client.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		e.t.Fatal(err)
	}
	return result{resp.StatusCode, resp.Header, raw}
}

// call sends a request with the API token and fails the test unless the
// status is want. If out is not nil, it decodes the response body into out.
func (e *testEnv) call(method, path string, body any, want int, out any) result {
	e.t.Helper()
	r := e.send(method, path, body, "Authorization", "Bearer "+e.token)
	if r.Status != want {
		e.t.Fatalf("%s %s: got %s, want %d", method, path, r, want)
	}
	if out != nil {
		if err := json.Unmarshal(r.Body, out); err != nil {
			e.t.Fatalf("%s %s: decode %s: %v", method, path, r.Body, err)
		}
	}
	return r
}
```

- [ ] **Step 3: Write the failing auth tests**

Create `internal/api/auth_test.go`:

```go
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/davidgibbons/innkeeper/internal/auth"
	"github.com/davidgibbons/innkeeper/internal/plugins"
)

// TestEveryOperationNeedsAuth reads the OpenAPI spec, so it also covers
// operations added later.
func TestEveryOperationNeedsAuth(t *testing.T) {
	e := newEnv(t)
	var spec struct {
		Paths map[string]map[string]struct {
			Security *[]map[string][]string `json:"security"`
		} `json:"paths"`
	}
	r := e.send("GET", "/openapi.json", nil)
	if err := json.Unmarshal(r.Body, &spec); err != nil {
		t.Fatalf("openapi.json: %s: %v", r, err)
	}
	param := regexp.MustCompile(`\{[^}]+\}`)
	checked, public := 0, 0
	for path, ops := range spec.Paths {
		for method, op := range ops {
			if op.Security != nil && len(*op.Security) == 0 {
				public++
				continue
			}
			checked++
			url := param.ReplaceAllString(path, "1")
			if r := e.send(strings.ToUpper(method), url, nil); r.Status != http.StatusUnauthorized {
				t.Errorf("%s %s without credentials: got %d, want 401", method, path, r.Status)
			}
		}
	}
	if checked == 0 || public == 0 {
		t.Fatalf("checked %d operations and skipped %d public ones", checked, public)
	}
}

func TestBadCredentials(t *testing.T) {
	e := newEnv(t)
	for _, h := range [][]string{
		{"Authorization", "Bearer ink_wrong"},
		{"Authorization", "Basic " + e.token},
		{"Cookie", sessionCookie + "=nope"},
	} {
		if r := e.send("GET", "/tokens", nil, h...); r.Status != http.StatusUnauthorized {
			t.Errorf("%v: got %s, want 401", h, r)
		}
	}
}

func TestTokenRoutes(t *testing.T) {
	e := newEnv(t)
	var created struct {
		ID     int64  `json:"id"`
		Secret string `json:"secret"`
	}
	e.call("POST", "/tokens", map[string]string{"name": "mcp"}, 201, &created)
	if r := e.send("GET", "/tokens", nil, "Authorization", "Bearer "+created.Secret); r.Status != 200 {
		t.Fatalf("new token: %s", r)
	}
	var list []auth.Token
	e.call("GET", "/tokens", nil, 200, &list)
	if len(list) != 2 || list[1].Name != "mcp" {
		t.Fatalf("tokens = %+v", list)
	}
	e.call("DELETE", fmt.Sprintf("/tokens/%d", created.ID), nil, 204, nil)
	if r := e.send("GET", "/tokens", nil, "Authorization", "Bearer "+created.Secret); r.Status != 401 {
		t.Fatalf("revoked token: %s", r)
	}
	e.call("DELETE", fmt.Sprintf("/tokens/%d", created.ID), nil, 404, nil)
	e.call("POST", "/tokens", map[string]string{"name": ""}, 422, nil)
}

// begin starts a login with the fake provider and returns the state cookie
// as a Cookie header value.
func (e *testEnv) begin() string {
	e.t.Helper()
	r := e.send("POST", fmt.Sprintf("/auth/%d/begin", e.loginID), nil)
	if r.Status != 200 || !strings.Contains(string(r.Body), `"password"`) {
		e.t.Fatalf("begin: %s", r)
	}
	c := r.cookie(e.t, stateCookie)
	return c.Name + "=" + c.Value
}

func (e *testEnv) login(password string) result {
	e.t.Helper()
	return e.send("POST", fmt.Sprintf("/auth/%d/complete", e.loginID),
		map[string]any{"params": map[string]string{"password": password}}, "Cookie", e.begin())
}

func TestLogin(t *testing.T) {
	e := newEnv(t)
	var providers []provider
	if r := e.send("GET", "/auth/providers", nil); r.Status != 200 || json.Unmarshal(r.Body, &providers) != nil {
		t.Fatalf("providers: %s", r)
	}
	// The importer runs the same plugin but names no owner, so no one can log in with it.
	if len(providers) != 1 || providers[0].ID != e.loginID || providers[0].Name != "Login" {
		t.Fatalf("providers = %+v", providers)
	}

	for _, pw := range []string{"wrong", "stranger"} {
		if r := e.login(pw); r.Status != 401 {
			t.Errorf("password %q: got %s, want 401", pw, r)
		}
	}
	ok := e.login("letmein")
	if ok.Status != 200 {
		t.Fatalf("login: %s", ok)
	}
	session := ok.cookie(t, sessionCookie)
	if !session.HttpOnly || !session.Secure || session.SameSite != http.SameSiteLaxMode {
		t.Fatalf("session cookie = %+v", session)
	}
	cookie := sessionCookie + "=" + session.Value
	if r := e.send("GET", "/tokens", nil, "Cookie", cookie); r.Status != 200 {
		t.Fatalf("GET with session: %s", r)
	}
	body := map[string]string{"name": "x"}
	if r := e.send("POST", "/tokens", body, "Cookie", cookie, "Sec-Fetch-Site", "cross-site"); r.Status != 401 {
		t.Fatalf("cross-site POST with session: %s", r)
	}
	if r := e.send("POST", "/tokens", body, "Cookie", cookie, "Sec-Fetch-Site", "same-origin"); r.Status != 201 {
		t.Fatalf("same-origin POST with session: %s", r)
	}
	if r := e.send("POST", "/auth/logout", nil, "Cookie", cookie); r.Status != 204 {
		t.Fatalf("logout: %s", r)
	}
	if r := e.send("GET", "/tokens", nil, "Cookie", cookie); r.Status != 401 {
		t.Fatalf("GET after logout: %s", r)
	}
}

func TestLoginCallback(t *testing.T) {
	e := newEnv(t)
	r := e.send("GET", fmt.Sprintf("/auth/%d/callback?password=letmein", e.loginID), nil, "Cookie", e.begin())
	if r.Status != http.StatusSeeOther || r.Header.Get("Location") != "/" {
		t.Fatalf("callback: %s %v", r, r.Header)
	}
	session := r.cookie(t, sessionCookie)
	if r := e.send("GET", "/tokens", nil, "Cookie", sessionCookie+"="+session.Value); r.Status != 200 {
		t.Fatalf("GET with session: %s", r)
	}
}

func TestLoginNeedsState(t *testing.T) {
	e := newEnv(t)
	r := e.send("POST", fmt.Sprintf("/auth/%d/complete", e.loginID), map[string]any{"params": map[string]string{"password": "letmein"}})
	if r.Status != 401 {
		t.Fatalf("complete without begin: %s", r)
	}
}

func TestLoginOnlyThroughAuthPlugins(t *testing.T) {
	e := newEnv(t)
	id, err := e.s.Registry.CreateInstance(context.Background(), plugins.NewInstance{Plugin: "vault", Name: "Vault"})
	if err != nil {
		t.Fatal(err)
	}
	if r := e.send("POST", fmt.Sprintf("/auth/%d/begin", id), nil); r.Status != 404 {
		t.Fatalf("begin through a push plugin: %s", r)
	}
}
```

- [ ] **Step 4: Run the tests to verify they fail**

Run: `go test ./internal/api/`
Expected: FAIL to compile: `undefined: Server`.

- [ ] **Step 5: Write the server**

Create `internal/api/server.go`:

```go
// Package api serves the core's HTTP API, described by one OpenAPI spec at
// /openapi.json.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sync"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"github.com/davidgibbons/innkeeper/internal/auth"
	"github.com/davidgibbons/innkeeper/internal/jobs"
	"github.com/davidgibbons/innkeeper/internal/library"
	"github.com/davidgibbons/innkeeper/internal/pluginhost"
	"github.com/davidgibbons/innkeeper/internal/plugins"
	"github.com/davidgibbons/innkeeper/protocol"
)

// Server holds what the API's handlers use.
type Server struct {
	Lib      *library.Library
	Queue    *jobs.Queue
	Registry *plugins.Registry
	Auth     *auth.Auth
	// Plugins runs plugins for interactive calls, such as logins. Its core
	// handler should be NoCore.
	Plugins *pluginhost.Manager
	Log     *slog.Logger
	// PublicURL is where browsers reach the API, such as
	// https://innkeeper.example.com. Optional. Redirect logins need it, and
	// an http:// URL sends cookies without Secure.
	PublicURL string

	loginMu sync.Mutex
}

// NoCore answers plugin callbacks. The API offers plugins none yet.
func NoCore(_ context.Context, method string, _ json.RawMessage) (any, error) {
	return nil, protocol.MethodNotFound(method)
}

// public marks an operation that needs no credentials.
var public = map[string]any{"public": true}

// noAuth shows a public operation in the spec with no security requirement.
var noAuth = []map[string][]string{}

type idIn struct {
	ID int64 `path:"id"`
}

// orEmpty makes a nil slice encode as [] instead of null.
func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// Handler returns the HTTP handler for every route.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	cfg := huma.DefaultConfig("Innkeeper", "0.1.0")
	cfg.Components.SecuritySchemes = map[string]*huma.SecurityScheme{
		"token":   {Type: "http", Scheme: "bearer"},
		"session": {Type: "apiKey", In: "cookie", Name: sessionCookie},
	}
	cfg.Security = []map[string][]string{{"token": {}}, {"session": {}}}
	api := humago.New(mux, cfg)
	api.UseMiddleware(s.authenticate(api))
	s.authRoutes(api)
	// Browsers arrive here from a login provider; it isn't part of the API.
	mux.HandleFunc("GET /auth/{id}/callback", s.callback)
	return mux
}

// fail turns an error into an HTTP error. Unexpected errors are logged and
// hidden from the client. A nil error stays nil.
func (s *Server) fail(err error) error {
	var se huma.StatusError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &se):
		return se
	case errors.Is(err, library.ErrNotFound), errors.Is(err, jobs.ErrNotFound),
		errors.Is(err, plugins.ErrNotFound), errors.Is(err, auth.ErrNotFound):
		return huma.Error404NotFound("not found")
	case errors.Is(err, library.ErrConflict), errors.Is(err, library.ErrSlugTaken), errors.Is(err, library.ErrNameTaken),
		errors.Is(err, plugins.ErrNameTaken), errors.Is(err, jobs.ErrFinished):
		return huma.Error409Conflict(err.Error())
	case errors.Is(err, library.ErrInvalid), errors.Is(err, plugins.ErrInvalid), errors.Is(err, plugins.ErrUnknownPlugin):
		return huma.Error400BadRequest(err.Error())
	}
	s.Log.Error("request failed", "err", err)
	return huma.Error500InternalServerError("internal error")
}
```

- [ ] **Step 6: Write auth and login**

Create `internal/api/auth.go`:

```go
package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/davidgibbons/innkeeper/internal/auth"
	"github.com/davidgibbons/innkeeper/internal/pluginhost"
	"github.com/davidgibbons/innkeeper/internal/plugins"
	"github.com/davidgibbons/innkeeper/protocol"
)

const (
	sessionCookie = "innkeeper_session"
	stateCookie   = "innkeeper_login_state"
)

// failedLoginDelay slows password guessing. Tests set it to 0.
var failedLoginDelay = time.Second

// authenticate lets a request through if it is public or carries a valid API
// token or session cookie.
func (s *Server) authenticate(api huma.API) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		if ctx.Operation().Metadata["public"] == true {
			next(ctx)
			return
		}
		ok, err := s.authorized(ctx)
		if err != nil {
			s.Log.Error("check credentials", "err", err)
			_ = huma.WriteErr(api, ctx, http.StatusInternalServerError, "internal error")
			return
		}
		if !ok {
			_ = huma.WriteErr(api, ctx, http.StatusUnauthorized, "log in, or send an API token as Authorization: Bearer <token>")
			return
		}
		next(ctx)
	}
}

func (s *Server) authorized(ctx huma.Context) (bool, error) {
	if h := ctx.Header("Authorization"); h != "" {
		token, ok := strings.CutPrefix(h, "Bearer ")
		if !ok {
			return false, nil
		}
		return s.Auth.CheckToken(ctx.Context(), token)
	}
	c, err := huma.ReadCookie(ctx, sessionCookie)
	if err != nil {
		return false, nil
	}
	// Browsers attach cookies to requests that other sites start, so only
	// this site's pages may change things.
	if m := ctx.Method(); m != http.MethodGet && m != http.MethodHead {
		if site := ctx.Header("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
			return false, nil
		}
	}
	return s.Auth.CheckSession(ctx.Context(), c.Value)
}

// cookie returns an HttpOnly cookie for the whole site. A negative maxAge
// deletes it.
func (s *Server) cookie(name, value string, maxAge time.Duration) *http.Cookie {
	return &http.Cookie{Name: name, Value: value, Path: "/", MaxAge: int(maxAge.Seconds()), HttpOnly: true,
		Secure: !strings.HasPrefix(s.PublicURL, "http://"), SameSite: http.SameSiteLaxMode}
}

func (s *Server) callbackURL(id int64) string {
	if s.PublicURL == "" {
		return ""
	}
	return fmt.Sprintf("%s/auth/%d/callback", strings.TrimSuffix(s.PublicURL, "/"), id)
}

// authInstance returns an instance whose plugin can log people in.
func (s *Server) authInstance(ctx context.Context, id int64) (pluginhost.Instance, error) {
	inst, err := s.Registry.Instance(ctx, id)
	if errors.Is(err, plugins.ErrBroken) {
		return inst, huma.Error503ServiceUnavailable(err.Error())
	}
	if err != nil {
		return inst, s.fail(err)
	}
	if !slices.Contains(inst.Manifest.Capabilities, "auth") {
		return inst, huma.Error404NotFound("no such login provider")
	}
	return inst, nil
}

// callPlugin calls an instance's plugin. A plugin that fails to start marks
// its instance broken.
func (s *Server) callPlugin(ctx context.Context, id int64, inst pluginhost.Instance, method string, params, result any) error {
	err := s.Plugins.Call(ctx, inst, method, params, result)
	var startErr *pluginhost.StartError
	if errors.As(err, &startErr) && ctx.Err() == nil {
		if merr := s.Registry.MarkBroken(context.WithoutCancel(ctx), id, err); merr != nil {
			s.Log.Error("mark plugin instance broken", "instance", id, "err", merr)
		}
		return huma.Error503ServiceUnavailable("plugin failed to start: " + err.Error())
	}
	return err
}

// login completes a login and returns the cookies to set: the new session,
// and the deleted login state.
func (s *Server) login(ctx context.Context, id int64, state string, params map[string]string) ([]*http.Cookie, error) {
	inst, err := s.authInstance(ctx, id)
	if err != nil {
		return nil, err
	}
	// ponytail: one login at a time across all providers; fine for one owner.
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	decoded, _ := base64.RawURLEncoding.DecodeString(state)
	var res protocol.AuthCompleteResult
	err = s.callPlugin(ctx, id, inst, protocol.MethodAuthComplete,
		protocol.AuthCompleteParams{Params: params, State: string(decoded), CallbackURL: s.callbackURL(id)}, &res)
	var secret string
	if err == nil {
		secret, err = s.Auth.CreateSession(ctx, id, res.Subject)
	}
	var rpcErr *protocol.Error
	if errors.As(err, &rpcErr) || errors.Is(err, auth.ErrNotOwner) {
		s.Log.Warn("login failed", "instance", id, "err", err)
		time.Sleep(failedLoginDelay)
		return nil, huma.Error401Unauthorized("login failed")
	}
	if err != nil {
		return nil, s.fail(err)
	}
	return []*http.Cookie{s.cookie(sessionCookie, secret, auth.SessionTTL), s.cookie(stateCookie, "", -time.Second)}, nil
}

// callback finishes a redirect login: the provider sends the browser here
// with its result in the query string.
func (s *Server) callback(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	params := map[string]string{}
	for k, v := range r.URL.Query() {
		params[k] = v[0]
	}
	state := ""
	if c, err := r.Cookie(stateCookie); err == nil {
		state = c.Value
	}
	cookies, err := s.login(r.Context(), id, state, params)
	if err != nil {
		status := http.StatusInternalServerError
		var se huma.StatusError
		if errors.As(err, &se) {
			status = se.GetStatus()
		}
		http.Error(w, http.StatusText(status), status)
		return
	}
	for _, c := range cookies {
		http.SetCookie(w, c)
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

type provider struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Plugin string `json:"plugin"`
}

type providersOut struct{ Body []provider }

type beginOut struct {
	SetCookie []*http.Cookie `header:"Set-Cookie"`
	Body      struct {
		Redirect string          `json:"redirect,omitempty" doc:"Send the browser here."`
		Form     json.RawMessage `json:"form,omitempty" doc:"JSON Schema of the fields to ask for. Send them to complete-login."`
	}
}

type completeIn struct {
	ID    int64  `path:"id"`
	State string `cookie:"innkeeper_login_state"`
	Body  struct {
		Params map[string]string `json:"params"`
	}
}

type cookiesOut struct {
	SetCookie []*http.Cookie `header:"Set-Cookie"`
}

type logoutIn struct {
	Session string `cookie:"innkeeper_session"`
}

type tokensOut struct{ Body []auth.Token }

type createTokenIn struct {
	Body struct {
		Name string `json:"name" minLength:"1"`
	}
}

type createdToken struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	Secret    string    `json:"secret" doc:"Send as Authorization: Bearer <secret>. Shown only once."`
}

type createTokenOut struct{ Body createdToken }

func (s *Server) authRoutes(api huma.API) {
	tags := []string{"Auth"}
	huma.Register(api, huma.Operation{OperationID: "list-login-providers", Method: http.MethodGet,
		Path: "/auth/providers", Summary: "List login providers", Tags: tags, Metadata: public, Security: noAuth},
		func(ctx context.Context, _ *struct{}) (*providersOut, error) {
			insts, err := s.Registry.ListInstances(ctx)
			if err != nil {
				return nil, s.fail(err)
			}
			out := &providersOut{Body: []provider{}}
			for _, i := range insts {
				m := s.Registry.Manifests()[i.Plugin]
				if m != nil && slices.Contains(m.Capabilities, "auth") && i.OwnerSubject != nil && i.BrokenError == nil {
					out.Body = append(out.Body, provider{i.ID, i.Name, i.Plugin})
				}
			}
			return out, nil
		})

	huma.Register(api, huma.Operation{OperationID: "begin-login", Method: http.MethodPost,
		Path: "/auth/{id}/begin", Summary: "Start a login",
		Description: "Returns a form to fill in or a URL to send the browser to, and sets a 10-minute login state cookie.",
		Tags:        tags, Metadata: public, Security: noAuth},
		func(ctx context.Context, in *idIn) (*beginOut, error) {
			inst, err := s.authInstance(ctx, in.ID)
			if err != nil {
				return nil, err
			}
			var res protocol.AuthBeginResult
			if err := s.callPlugin(ctx, in.ID, inst, protocol.MethodAuthBegin,
				protocol.AuthBeginParams{CallbackURL: s.callbackURL(in.ID)}, &res); err != nil {
				return nil, s.fail(err)
			}
			// Encoded because plugins may use characters cookies can't hold.
			state := base64.RawURLEncoding.EncodeToString([]byte(res.State))
			out := &beginOut{SetCookie: []*http.Cookie{s.cookie(stateCookie, state, 10*time.Minute)}}
			out.Body.Redirect, out.Body.Form = res.Redirect, res.Form
			return out, nil
		})

	huma.Register(api, huma.Operation{OperationID: "complete-login", Method: http.MethodPost,
		Path: "/auth/{id}/complete", Summary: "Finish a login", Description: "Sets the session cookie.",
		Tags: tags, Metadata: public, Security: noAuth},
		func(ctx context.Context, in *completeIn) (*cookiesOut, error) {
			cookies, err := s.login(ctx, in.ID, in.State, in.Body.Params)
			if err != nil {
				return nil, err
			}
			return &cookiesOut{SetCookie: cookies}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "logout", Method: http.MethodPost, Path: "/auth/logout",
		Summary: "Log out", Tags: tags, Metadata: public, Security: noAuth, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *logoutIn) (*cookiesOut, error) {
			if in.Session != "" {
				if err := s.Auth.DeleteSession(ctx, in.Session); err != nil {
					return nil, s.fail(err)
				}
			}
			return &cookiesOut{SetCookie: []*http.Cookie{s.cookie(sessionCookie, "", -time.Second)}}, nil
		})

	tags = []string{"Tokens"}
	huma.Register(api, huma.Operation{OperationID: "list-tokens", Method: http.MethodGet, Path: "/tokens",
		Summary: "List API tokens", Tags: tags},
		func(ctx context.Context, _ *struct{}) (*tokensOut, error) {
			list, err := s.Auth.Tokens(ctx)
			if err != nil {
				return nil, s.fail(err)
			}
			return &tokensOut{Body: orEmpty(list)}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "create-token", Method: http.MethodPost, Path: "/tokens",
		Summary: "Create an API token", Tags: tags, DefaultStatus: http.StatusCreated},
		func(ctx context.Context, in *createTokenIn) (*createTokenOut, error) {
			t, secret, err := s.Auth.CreateToken(ctx, in.Body.Name)
			if err != nil {
				return nil, s.fail(err)
			}
			return &createTokenOut{Body: createdToken{ID: t.ID, Name: t.Name, CreatedAt: t.CreatedAt, Secret: secret}}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "revoke-token", Method: http.MethodDelete, Path: "/tokens/{id}",
		Summary: "Revoke an API token", Tags: tags, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *idIn) (*struct{}, error) {
			return nil, s.fail(s.Auth.RevokeToken(ctx, in.ID))
		})
}
```

- [ ] **Step 7: Run the tests to verify they pass**

Run: `go test -race ./internal/api/`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add go.mod go.sum internal/api
git commit -m "Add the HTTP API server with tokens and plugin logins"
```

---

### Task 11: Card and lorebook routes

**Files:**
- Create: `internal/api/entities.go`
- Modify: `internal/api/server.go` (register routes)
- Test: `internal/api/entities_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/api/entities_test.go`:

```go
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

type entityResult struct {
	ID         int64          `json:"id"`
	Slug       string         `json:"slug"`
	Provenance string         `json:"provenance"`
	ArchivedAt *string        `json:"archived_at"`
	Tags       []string       `json:"tags"`
	Lorebooks  []int64        `json:"lorebooks"`
	Revision   revisionResult `json:"revision"`
}

type revisionResult struct {
	ID        int64           `json:"id"`
	Data      json.RawMessage `json:"data"`
	Avatar    *string         `json:"avatar"`
	Overrides json.RawMessage `json:"overrides"`
	Message   string          `json:"message"`
	Source    string          `json:"source"`
}

func (e *testEnv) createCard(data string) entityResult {
	e.t.Helper()
	var c entityResult
	e.call("POST", "/cards", map[string]any{"data": json.RawMessage(data)}, 201, &c)
	return c
}

func TestCardLifecycle(t *testing.T) {
	e := newEnv(t)
	c := e.createCard(`{"name": "Brakka Ironfist", "description": "A dwarf."}`)
	if c.Slug != "brakka-ironfist" || c.Provenance != "api" || c.Revision.Source != "ui" {
		t.Fatalf("created %+v", c)
	}
	first := c.Revision.ID
	path := fmt.Sprintf("/cards/%d", c.ID)

	var updated entityResult
	e.call("PUT", path, map[string]any{"base_revision_id": first,
		"data": json.RawMessage(`{"name": "Brakka", "description": "A grumpy dwarf."}`), "message": "grumpier", "source": "mcp"}, 200, &updated)
	if updated.Revision.ID == first || updated.Revision.Source != "mcp" || updated.Revision.Message != "grumpier" {
		t.Fatalf("updated %+v", updated.Revision)
	}
	e.call("PUT", path, map[string]any{"base_revision_id": first, "data": json.RawMessage(`{"name": "Stale"}`)}, 409, nil)

	var history []revisionResult
	e.call("GET", path+"/revisions", nil, 200, &history)
	if len(history) != 2 || history[0].ID != updated.Revision.ID || history[0].Data != nil {
		t.Fatalf("history = %+v", history)
	}
	var old revisionResult
	e.call("GET", fmt.Sprintf("%s/revisions/%d", path, first), nil, 200, &old)
	if !strings.Contains(string(old.Data), "Brakka Ironfist") {
		t.Fatalf("old revision = %s", old.Data)
	}

	var restored entityResult
	e.call("POST", path+"/restore", map[string]any{"revision_id": first, "base_revision_id": updated.Revision.ID}, 200, &restored)
	if !strings.Contains(string(restored.Revision.Data), "Brakka Ironfist") || restored.Revision.Message != fmt.Sprintf("Restore revision %d", first) {
		t.Fatalf("restored %+v", restored.Revision)
	}

	e.call("PUT", path+"/archived", map[string]bool{"archived": true}, 204, nil)
	var archived entityResult
	e.call("GET", path, nil, 200, &archived)
	if archived.ArchivedAt == nil {
		t.Fatal("card not archived")
	}
}

func TestCardErrors(t *testing.T) {
	e := newEnv(t)
	other := e.createCard(`{"name": "Other"}`)
	e.call("GET", "/cards/999", nil, 404, nil)
	e.call("POST", "/cards", map[string]any{"data": json.RawMessage(`{"name": "A", "character_book": {}}`)}, 400, nil)
	e.call("POST", "/cards", map[string]any{"slug": "Not A Slug", "data": json.RawMessage(`{"name": "A"}`)}, 422, nil)
	e.call("POST", "/cards", map[string]any{"slug": "other", "data": json.RawMessage(`{"name": "A"}`)}, 409, nil)
	e.call("POST", "/cards", map[string]any{"data": json.RawMessage(`{"name": "A"}`), "avatar": strings.Repeat("a", 64)}, 404, nil)

	c := e.createCard(`{"name": "Card"}`)
	// A revision of another card is never this card's base, and never restorable here.
	e.call("PUT", fmt.Sprintf("/cards/%d", c.ID), map[string]any{"base_revision_id": other.Revision.ID,
		"data": json.RawMessage(`{"name": "X"}`)}, 409, nil)
	e.call("GET", fmt.Sprintf("/cards/%d/revisions/%d", c.ID, other.Revision.ID), nil, 404, nil)
	e.call("POST", fmt.Sprintf("/cards/%d/restore", c.ID), map[string]any{"revision_id": other.Revision.ID,
		"base_revision_id": c.Revision.ID}, 404, nil)
}

func TestUpdateKeepsAvatarAndOverrides(t *testing.T) {
	e := newEnv(t)
	blob, err := e.s.Lib.PutBlob(context.Background(), strings.NewReader("avatar"))
	if err != nil {
		t.Fatal(err)
	}
	var c entityResult
	e.call("POST", "/cards", map[string]any{"data": json.RawMessage(`{"name": "A"}`), "avatar": blob.SHA256}, 201, &c)
	path := fmt.Sprintf("/cards/%d", c.ID)

	e.call("PUT", path, map[string]any{"base_revision_id": c.Revision.ID, "data": json.RawMessage(`{"name": "B"}`),
		"overrides": json.RawMessage(`{"1": {"name": "B for Lumiverse"}}`)}, 200, &c)
	e.call("PUT", path, map[string]any{"base_revision_id": c.Revision.ID, "data": json.RawMessage(`{"name": "C"}`)}, 200, &c)
	if c.Revision.Avatar == nil || *c.Revision.Avatar != blob.SHA256 || !strings.Contains(string(c.Revision.Overrides), "Lumiverse") {
		t.Fatalf("kept %+v", c.Revision)
	}
	e.call("PUT", path, map[string]any{"base_revision_id": c.Revision.ID, "data": json.RawMessage(`{"name": "D"}`), "avatar": ""}, 200, &c)
	if c.Revision.Avatar != nil {
		t.Fatalf("avatar not removed: %+v", c.Revision)
	}
	e.call("PUT", path, map[string]any{"base_revision_id": c.Revision.ID, "data": json.RawMessage(`{"name": "E"}`),
		"overrides": json.RawMessage(`[]`)}, 400, nil)
}

func TestCardTagsLinksAndExport(t *testing.T) {
	e := newEnv(t)
	c := e.createCard(`{"name": "Brakka"}`)
	path := fmt.Sprintf("/cards/%d", c.ID)

	var tags []string
	e.call("PUT", path+"/tags", map[string]any{"tags": []string{"fantasy", "dwarf"}}, 200, &tags)
	if strings.Join(tags, ",") != "dwarf,fantasy" {
		t.Fatalf("tags = %v", tags)
	}

	var book entityResult
	e.call("POST", "/lorebooks", map[string]any{"data": json.RawMessage(`{"name": "Forge", "entries": [{"keys": ["anvil"], "content": "Heavy."}]}`)}, 201, &book)
	if book.Slug != "forge" || book.Tags != nil {
		t.Fatalf("lorebook = %+v", book)
	}
	e.call("PUT", fmt.Sprintf("%s/lorebooks/%d", path, book.ID), nil, 204, nil)
	e.call("PUT", fmt.Sprintf("%s/lorebooks/%d", path, 999), nil, 404, nil)

	var got entityResult
	e.call("GET", path, nil, 200, &got)
	if len(got.Tags) != 2 || len(got.Lorebooks) != 1 || got.Lorebooks[0] != book.ID {
		t.Fatalf("card = %+v", got)
	}

	r := e.call("GET", path+"/export", nil, 200, nil)
	if !strings.Contains(string(r.Body), `"character_book"`) || !strings.Contains(r.Header.Get("Content-Disposition"), "brakka.json") {
		t.Fatalf("export: %s %v", r, r.Header)
	}

	e.call("DELETE", fmt.Sprintf("%s/lorebooks/%d", path, book.ID), nil, 204, nil)
	e.call("GET", path, nil, 200, &got)
	if len(got.Lorebooks) != 0 {
		t.Fatalf("lorebook still linked: %+v", got)
	}

	other := e.createCard(`{"name": "Brakka"}`)
	var matches []struct {
		CardID int64  `json:"card_id"`
		Reason string `json:"reason"`
	}
	e.call("GET", path+"/similar", nil, 200, &matches)
	if len(matches) == 0 || matches[0].CardID != other.ID || matches[0].Reason != "content" {
		t.Fatalf("similar = %+v", matches)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -run 'TestCard|TestUpdate' ./internal/api/`
Expected: FAIL: `POST /cards: got 404`.

- [ ] **Step 3: Write the routes**

Create `internal/api/entities.go`:

```go
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/davidgibbons/innkeeper/internal/library"
)

type revisionOut struct {
	ID          int64           `json:"id"`
	ParentID    *int64          `json:"parent_id,omitempty"`
	Data        json.RawMessage `json:"data,omitempty"`
	Avatar      *string         `json:"avatar,omitempty"`
	Overrides   json.RawMessage `json:"overrides,omitempty"`
	ContentHash string          `json:"content_hash"`
	Message     string          `json:"message"`
	Source      string          `json:"source"`
	CreatedAt   time.Time       `json:"created_at"`
}

// toRevision converts a revision. History lists leave out data and
// overrides.
func toRevision(r library.Revision, full bool) revisionOut {
	out := revisionOut{ID: r.ID, ParentID: r.ParentID, Avatar: r.AvatarBlob, ContentHash: r.ContentHash,
		Message: r.Message, Source: r.Source, CreatedAt: r.CreatedAt}
	if full {
		out.Data, out.Overrides = r.Data, r.Overrides
	}
	return out
}

type entityOut struct {
	ID         int64       `json:"id"`
	Slug       string      `json:"slug"`
	Provenance string      `json:"provenance"`
	ArchivedAt *time.Time  `json:"archived_at,omitempty"`
	CreatedAt  time.Time   `json:"created_at"`
	Revision   revisionOut `json:"revision" doc:"The head revision"`
	Tags       []string    `json:"tags,omitempty" doc:"Cards only"`
	Lorebooks  []int64     `json:"lorebooks,omitempty" doc:"Cards only: IDs of linked lorebooks"`
}

type entityResp struct{ Body entityOut }

type createEntityIn struct {
	Body struct {
		Slug    string          `json:"slug,omitempty" pattern:"^[a-z0-9]+(-[a-z0-9]+)*$" doc:"Default: made from data.name"`
		Data    json.RawMessage `json:"data" doc:"The CCv3 data object. A card's may not contain character_book; link a lorebook instead."`
		Avatar  string          `json:"avatar,omitempty" doc:"sha256 of an uploaded blob"`
		Message string          `json:"message,omitempty"`
		Source  string          `json:"source,omitempty" enum:"ui,mcp" default:"ui"`
	}
}

type updateEntityIn struct {
	ID   int64 `path:"id"`
	Body struct {
		BaseRevisionID int64           `json:"base_revision_id" doc:"The head revision this edit is based on. A stale one returns 409."`
		Data           json.RawMessage `json:"data"`
		Avatar         *string         `json:"avatar,omitempty" doc:"Omit to keep the base revision's avatar; empty to remove it."`
		Overrides      json.RawMessage `json:"overrides,omitempty" doc:"JSON merge patches by plugin instance ID. Omit to keep the base revision's."`
		Message        string          `json:"message,omitempty"`
		Source         string          `json:"source,omitempty" enum:"ui,mcp" default:"ui"`
	}
}

type revisionsOut struct{ Body []revisionOut }

type revisionIn struct {
	ID         int64 `path:"id"`
	RevisionID int64 `path:"revision_id"`
}

type revisionResp struct{ Body revisionOut }

type restoreIn struct {
	ID   int64 `path:"id"`
	Body struct {
		RevisionID     int64 `json:"revision_id" doc:"The old revision to restore"`
		BaseRevisionID int64 `json:"base_revision_id" doc:"The current head revision"`
	}
}

type archiveIn struct {
	ID   int64 `path:"id"`
	Body struct {
		Archived bool `json:"archived"`
	}
}

type tagsIn struct {
	ID   int64 `path:"id"`
	Body struct {
		Tags []string `json:"tags"`
	}
}

type tagsOut struct{ Body []string }

type similarIn struct {
	ID          int64 `path:"id"`
	MaxDistance int   `query:"max_distance" default:"8" minimum:"0" maximum:"32" doc:"Most differing hash bits to count as similar"`
}

type matchesOut struct{ Body []library.Match }

type linkIn struct {
	ID         int64 `path:"id"`
	LorebookID int64 `path:"lorebook_id"`
}

type exportOut struct {
	ContentDisposition string `header:"Content-Disposition"`
	Body               json.RawMessage
}

// nameOf returns data.name, for making a slug.
func nameOf(data json.RawMessage) string {
	var d struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(data, &d)
	return d.Name
}

// entity returns an entity with its head revision, and a card's tags and
// lorebooks.
func (s *Server) entity(ctx context.Context, ents *library.Entities, id int64) (*entityResp, error) {
	e, err := ents.Get(ctx, id)
	if err != nil {
		return nil, s.fail(err)
	}
	rev, err := ents.Revision(ctx, e.HeadRevisionID)
	if err != nil {
		return nil, s.fail(err)
	}
	out := &entityResp{Body: entityOut{ID: e.ID, Slug: e.Slug, Provenance: e.Provenance, ArchivedAt: e.ArchivedAt,
		CreatedAt: e.CreatedAt, Revision: toRevision(rev, true)}}
	if ents == s.Lib.Cards {
		if out.Body.Tags, err = s.Lib.CardTags(ctx, id); err != nil {
			return nil, s.fail(err)
		}
		if out.Body.Lorebooks, err = s.Lib.CardLorebooks(ctx, id); err != nil {
			return nil, s.fail(err)
		}
	}
	return out, nil
}

// revisionOf returns a revision if it belongs to entity id.
func (s *Server) revisionOf(ctx context.Context, ents *library.Entities, id, revID int64) (library.Revision, error) {
	r, err := ents.Revision(ctx, revID)
	if err == nil && r.EntityID != id {
		err = library.ErrNotFound
	}
	if err != nil {
		return r, s.fail(err)
	}
	return r, nil
}

// entityRoutes registers the routes cards and lorebooks share, under
// /<plural>.
func (s *Server) entityRoutes(api huma.API, ents *library.Entities, plural, singular, tag string) {
	tags := []string{tag}
	base := "/" + plural
	item := base + "/{id}"

	huma.Register(api, huma.Operation{OperationID: "create-" + singular, Method: http.MethodPost, Path: base,
		Summary: "Create a " + singular, Tags: tags, DefaultStatus: http.StatusCreated},
		func(ctx context.Context, in *createEntityIn) (*entityResp, error) {
			e := library.Edit{Data: in.Body.Data, AvatarBlob: in.Body.Avatar, Message: in.Body.Message, Source: in.Body.Source}
			var ent library.Entity
			var err error
			if in.Body.Slug != "" {
				ent, _, err = ents.Create(ctx, in.Body.Slug, "api", e)
			} else {
				ent, _, err = ents.CreateFromName(ctx, nameOf(in.Body.Data), "api", e)
			}
			if err != nil {
				return nil, s.fail(err)
			}
			return s.entity(ctx, ents, ent.ID)
		})

	huma.Register(api, huma.Operation{OperationID: "get-" + singular, Method: http.MethodGet, Path: item,
		Summary: "Get a " + singular, Tags: tags},
		func(ctx context.Context, in *idIn) (*entityResp, error) {
			return s.entity(ctx, ents, in.ID)
		})

	huma.Register(api, huma.Operation{OperationID: "update-" + singular, Method: http.MethodPut, Path: item,
		Summary: "Edit a " + singular, Description: "Adds a revision on top of base_revision_id.", Tags: tags},
		func(ctx context.Context, in *updateEntityIn) (*entityResp, error) {
			if _, err := ents.Get(ctx, in.ID); err != nil {
				return nil, s.fail(err)
			}
			base, err := ents.Revision(ctx, in.Body.BaseRevisionID)
			if errors.Is(err, library.ErrNotFound) || err == nil && base.EntityID != in.ID {
				return nil, s.fail(library.ErrConflict)
			}
			if err != nil {
				return nil, s.fail(err)
			}
			e := library.Edit{Data: in.Body.Data, Overrides: in.Body.Overrides, Message: in.Body.Message, Source: in.Body.Source}
			if e.Overrides == nil {
				e.Overrides = base.Overrides
			}
			if in.Body.Avatar != nil {
				e.AvatarBlob = *in.Body.Avatar
			} else if base.AvatarBlob != nil {
				e.AvatarBlob = *base.AvatarBlob
			}
			if _, err := ents.Update(ctx, in.ID, base.ID, e); err != nil {
				return nil, s.fail(err)
			}
			return s.entity(ctx, ents, in.ID)
		})

	huma.Register(api, huma.Operation{OperationID: "list-" + singular + "-revisions", Method: http.MethodGet,
		Path: item + "/revisions", Summary: "List a " + singular + "'s revisions", Description: "Newest first, without data.", Tags: tags},
		func(ctx context.Context, in *idIn) (*revisionsOut, error) {
			if _, err := ents.Get(ctx, in.ID); err != nil {
				return nil, s.fail(err)
			}
			revs, err := ents.History(ctx, in.ID)
			if err != nil {
				return nil, s.fail(err)
			}
			out := &revisionsOut{Body: make([]revisionOut, len(revs))}
			for i, r := range revs {
				out.Body[i] = toRevision(r, false)
			}
			return out, nil
		})

	huma.Register(api, huma.Operation{OperationID: "get-" + singular + "-revision", Method: http.MethodGet,
		Path: item + "/revisions/{revision_id}", Summary: "Get one revision of a " + singular, Tags: tags},
		func(ctx context.Context, in *revisionIn) (*revisionResp, error) {
			r, err := s.revisionOf(ctx, ents, in.ID, in.RevisionID)
			if err != nil {
				return nil, err
			}
			return &revisionResp{Body: toRevision(r, true)}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "restore-" + singular, Method: http.MethodPost,
		Path: item + "/restore", Summary: "Restore an old revision",
		Description: "Adds a revision with the old revision's content on top of base_revision_id.", Tags: tags},
		func(ctx context.Context, in *restoreIn) (*entityResp, error) {
			old, err := s.revisionOf(ctx, ents, in.ID, in.Body.RevisionID)
			if err != nil {
				return nil, err
			}
			e := library.Edit{Data: old.Data, Overrides: old.Overrides, Source: "ui",
				Message: fmt.Sprintf("Restore revision %d", old.ID)}
			if old.AvatarBlob != nil {
				e.AvatarBlob = *old.AvatarBlob
			}
			if _, err := ents.Update(ctx, in.ID, in.Body.BaseRevisionID, e); err != nil {
				return nil, s.fail(err)
			}
			return s.entity(ctx, ents, in.ID)
		})

	huma.Register(api, huma.Operation{OperationID: "archive-" + singular, Method: http.MethodPut,
		Path: item + "/archived", Summary: "Archive or unarchive a " + singular, Tags: tags, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *archiveIn) (*struct{}, error) {
			return nil, s.fail(ents.SetArchived(ctx, in.ID, in.Body.Archived))
		})
}

// cardRoutes registers the routes only cards have.
func (s *Server) cardRoutes(api huma.API) {
	tags := []string{"Cards"}
	huma.Register(api, huma.Operation{OperationID: "set-card-tags", Method: http.MethodPut, Path: "/cards/{id}/tags",
		Summary: "Replace a card's tags", Tags: tags},
		func(ctx context.Context, in *tagsIn) (*tagsOut, error) {
			if _, err := s.Lib.Cards.Get(ctx, in.ID); err != nil {
				return nil, s.fail(err)
			}
			if err := s.Lib.SetCardTags(ctx, in.ID, in.Body.Tags); err != nil {
				return nil, s.fail(err)
			}
			tags, err := s.Lib.CardTags(ctx, in.ID)
			if err != nil {
				return nil, s.fail(err)
			}
			return &tagsOut{Body: orEmpty(tags)}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "list-similar-cards", Method: http.MethodGet, Path: "/cards/{id}/similar",
		Summary: "Find likely duplicates", Description: "Same content, near-identical text, or a similar avatar; closest first.", Tags: tags},
		func(ctx context.Context, in *similarIn) (*matchesOut, error) {
			if _, err := s.Lib.Cards.Get(ctx, in.ID); err != nil {
				return nil, s.fail(err)
			}
			m, err := s.Lib.SimilarCards(ctx, in.ID, in.MaxDistance)
			if err != nil {
				return nil, s.fail(err)
			}
			return &matchesOut{Body: orEmpty(m)}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "link-lorebook", Method: http.MethodPut,
		Path: "/cards/{id}/lorebooks/{lorebook_id}", Summary: "Link a lorebook to a card", Tags: tags, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *linkIn) (*struct{}, error) {
			return nil, s.fail(s.Lib.LinkLorebook(ctx, in.ID, in.LorebookID))
		})

	huma.Register(api, huma.Operation{OperationID: "unlink-lorebook", Method: http.MethodDelete,
		Path: "/cards/{id}/lorebooks/{lorebook_id}", Summary: "Unlink a lorebook from a card", Tags: tags, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *linkIn) (*struct{}, error) {
			return nil, s.fail(s.Lib.UnlinkLorebook(ctx, in.ID, in.LorebookID))
		})

	huma.Register(api, huma.Operation{OperationID: "export-card", Method: http.MethodGet, Path: "/cards/{id}/export",
		Summary: "Export a card as CCv3 JSON", Description: "Linked lorebooks are embedded as data.character_book.", Tags: tags},
		func(ctx context.Context, in *idIn) (*exportOut, error) {
			c, err := s.Lib.Cards.Get(ctx, in.ID)
			if err != nil {
				return nil, s.fail(err)
			}
			raw, err := s.Lib.ExportCard(ctx, in.ID)
			if err != nil {
				return nil, s.fail(err)
			}
			return &exportOut{ContentDisposition: fmt.Sprintf(`attachment; filename="%s.json"`, c.Slug), Body: raw}, nil
		})
}
```

In `Handler` in `internal/api/server.go`, after `s.authRoutes(api)`, add:

```go
	s.entityRoutes(api, s.Lib.Cards, "cards", "card", "Cards")
	s.cardRoutes(api)
	s.entityRoutes(api, s.Lib.Lorebooks, "lorebooks", "lorebook", "Lorebooks")
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./internal/api/`
Expected: PASS, including `TestEveryOperationNeedsAuth`, which now covers the new routes.

- [ ] **Step 5: Commit**

```bash
git add internal/api
git commit -m "Add card and lorebook routes"
```

---

### Task 12: Tag, collection, and blob routes

**Files:**
- Create: `internal/api/organize.go`
- Modify: `internal/api/server.go` (register routes)
- Test: `internal/api/organize_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/api/organize_test.go`:

```go
package api

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/davidgibbons/innkeeper/internal/library"
)

func TestTagsAndCollections(t *testing.T) {
	e := newEnv(t)
	c := e.createCard(`{"name": "Brakka"}`)
	e.call("PUT", fmt.Sprintf("/cards/%d/tags", c.ID), map[string]any{"tags": []string{"fantasy"}}, 200, nil)
	var tags []library.TagCount
	e.call("GET", "/tags", nil, 200, &tags)
	if len(tags) != 1 || tags[0] != (library.TagCount{Name: "fantasy", Cards: 1}) {
		t.Fatalf("tags = %+v", tags)
	}

	var col library.Collection
	e.call("POST", "/collections", map[string]string{"name": "Favorites"}, 201, &col)
	e.call("POST", "/collections", map[string]string{"name": "Favorites"}, 409, nil)
	path := fmt.Sprintf("/collections/%d", col.ID)
	e.call("PUT", fmt.Sprintf("%s/cards/%d", path, c.ID), nil, 204, nil)
	e.call("PUT", fmt.Sprintf("/collections/999/cards/%d", c.ID), nil, 404, nil)
	e.call("PATCH", path, map[string]string{"name": "Best"}, 200, &col)
	if col.Name != "Best" {
		t.Fatalf("renamed = %+v", col)
	}
	var list []library.Collection
	e.call("GET", "/collections", nil, 200, &list)
	if len(list) != 1 || list[0].Cards != 1 || list[0].Name != "Best" {
		t.Fatalf("collections = %+v", list)
	}
	e.call("DELETE", fmt.Sprintf("%s/cards/%d", path, c.ID), nil, 204, nil)
	e.call("DELETE", path, nil, 204, nil)
	e.call("DELETE", path, nil, 404, nil)
}

func TestBlobs(t *testing.T) {
	e := newEnv(t)
	png := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 32)...)
	var b library.Blob
	e.call("POST", "/blobs", png, 201, &b)
	if b.MIME != "image/png" || b.Size != int64(len(png)) {
		t.Fatalf("blob = %+v", b)
	}
	r := e.call("GET", "/blobs/"+b.SHA256, nil, 200, nil)
	if !bytes.Equal(r.Body, png) || r.Header.Get("Content-Type") != "image/png" ||
		r.Header.Get("X-Content-Type-Options") != "nosniff" || r.Header.Get("Content-Security-Policy") != "sandbox" {
		t.Fatalf("download: %d bytes, %v", len(r.Body), r.Header)
	}

	// Anything but an image downloads instead of rendering.
	e.call("POST", "/blobs", []byte("<html><script>alert(1)</script></html>"), 201, &b)
	r = e.call("GET", "/blobs/"+b.SHA256, nil, 200, nil)
	if r.Header.Get("Content-Type") != "application/octet-stream" {
		t.Fatalf("html blob served as %q", r.Header.Get("Content-Type"))
	}

	e.call("GET", "/blobs/"+strings.Repeat("0", 64), nil, 404, nil)
	e.call("GET", "/blobs/not-a-hash", nil, 422, nil)
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -run 'TestTagsAndCollections|TestBlobs' ./internal/api/`
Expected: FAIL: `GET /tags: got 404`.

- [ ] **Step 3: Write the routes**

Create `internal/api/organize.go`:

```go
package api

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/davidgibbons/innkeeper/internal/library"
)

// maxBlobBytes caps uploads. Card PNGs are a few MB at most.
const maxBlobBytes = 32 << 20

type tagCountsOut struct{ Body []library.TagCount }

type collectionsOut struct{ Body []library.Collection }

type collectionOut struct{ Body library.Collection }

type createCollectionIn struct {
	Body struct {
		Name        string `json:"name" minLength:"1"`
		Description string `json:"description,omitempty"`
	}
}

type updateCollectionIn struct {
	ID   int64 `path:"id"`
	Body struct {
		Name        *string `json:"name,omitempty" minLength:"1"`
		Description *string `json:"description,omitempty"`
	}
}

type collectionCardIn struct {
	ID     int64 `path:"id"`
	CardID int64 `path:"card_id"`
}

type uploadIn struct {
	RawBody []byte
}

type blobOut struct{ Body library.Blob }

type blobIn struct {
	SHA256 string `path:"sha256" pattern:"^[0-9a-f]{64}$"`
}

func (s *Server) organizeRoutes(api huma.API) {
	tags := []string{"Organize"}
	huma.Register(api, huma.Operation{OperationID: "list-tags", Method: http.MethodGet, Path: "/tags",
		Summary: "List tags with card counts", Tags: tags},
		func(ctx context.Context, _ *struct{}) (*tagCountsOut, error) {
			t, err := s.Lib.Tags(ctx)
			if err != nil {
				return nil, s.fail(err)
			}
			return &tagCountsOut{Body: orEmpty(t)}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "list-collections", Method: http.MethodGet, Path: "/collections",
		Summary: "List collections with card counts", Tags: tags},
		func(ctx context.Context, _ *struct{}) (*collectionsOut, error) {
			c, err := s.Lib.Collections(ctx)
			if err != nil {
				return nil, s.fail(err)
			}
			return &collectionsOut{Body: orEmpty(c)}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "create-collection", Method: http.MethodPost, Path: "/collections",
		Summary: "Create a collection", Tags: tags, DefaultStatus: http.StatusCreated},
		func(ctx context.Context, in *createCollectionIn) (*collectionOut, error) {
			c, err := s.Lib.CreateCollection(ctx, in.Body.Name, in.Body.Description)
			if err != nil {
				return nil, s.fail(err)
			}
			return &collectionOut{Body: c}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "update-collection", Method: http.MethodPatch, Path: "/collections/{id}",
		Summary: "Rename a collection or change its description", Tags: tags},
		func(ctx context.Context, in *updateCollectionIn) (*collectionOut, error) {
			c, err := s.Lib.UpdateCollection(ctx, in.ID, in.Body.Name, in.Body.Description)
			if err != nil {
				return nil, s.fail(err)
			}
			return &collectionOut{Body: c}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "delete-collection", Method: http.MethodDelete, Path: "/collections/{id}",
		Summary: "Delete a collection", Description: "Its cards stay in the library.", Tags: tags, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *idIn) (*struct{}, error) {
			return nil, s.fail(s.Lib.DeleteCollection(ctx, in.ID))
		})

	huma.Register(api, huma.Operation{OperationID: "add-to-collection", Method: http.MethodPut,
		Path: "/collections/{id}/cards/{card_id}", Summary: "Add a card to a collection", Tags: tags, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *collectionCardIn) (*struct{}, error) {
			return nil, s.fail(s.Lib.AddToCollection(ctx, in.ID, in.CardID))
		})

	huma.Register(api, huma.Operation{OperationID: "remove-from-collection", Method: http.MethodDelete,
		Path: "/collections/{id}/cards/{card_id}", Summary: "Remove a card from a collection", Tags: tags, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *collectionCardIn) (*struct{}, error) {
			return nil, s.fail(s.Lib.RemoveFromCollection(ctx, in.ID, in.CardID))
		})

	tags = []string{"Blobs"}
	huma.Register(api, huma.Operation{OperationID: "upload-blob", Method: http.MethodPost, Path: "/blobs",
		Summary: "Upload a file", Description: "Send any file as application/octet-stream. Uploading the same bytes twice returns the same blob.",
		Tags: tags, DefaultStatus: http.StatusCreated, MaxBodyBytes: maxBlobBytes,
		RequestBody: &huma.RequestBody{Required: true, Content: map[string]*huma.MediaType{"application/octet-stream": {}}}},
		func(ctx context.Context, in *uploadIn) (*blobOut, error) {
			b, err := s.Lib.PutBlob(ctx, bytes.NewReader(in.RawBody))
			if err != nil {
				return nil, s.fail(err)
			}
			return &blobOut{Body: b}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "get-blob", Method: http.MethodGet, Path: "/blobs/{sha256}",
		Summary: "Download a file", Tags: tags},
		func(ctx context.Context, in *blobIn) (*huma.StreamResponse, error) {
			b, f, err := s.Lib.OpenBlob(ctx, in.SHA256)
			if err != nil {
				return nil, s.fail(err)
			}
			return &huma.StreamResponse{Body: func(hc huma.Context) {
				defer f.Close()
				// Only images display inline. Anything else downloads, so an
				// uploaded HTML file can't run as this site.
				mime := b.MIME
				if !strings.HasPrefix(mime, "image/") {
					mime = "application/octet-stream"
				}
				hc.SetHeader("Content-Type", mime)
				hc.SetHeader("Content-Length", strconv.FormatInt(b.Size, 10))
				hc.SetHeader("X-Content-Type-Options", "nosniff")
				hc.SetHeader("Content-Security-Policy", "sandbox")
				hc.SetHeader("Cache-Control", "private, max-age=31536000, immutable")
				if _, err := io.Copy(hc.BodyWriter(), f); err != nil {
					s.Log.Warn("send blob", "sha256", b.SHA256, "err", err)
				}
			}}, nil
		})
}
```

In `Handler`, after the entity routes, add:

```go
	s.organizeRoutes(api)
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./internal/api/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api
git commit -m "Add tag, collection, and blob routes"
```

---

### Task 13: `POST /search`

**Files:**
- Create: `internal/api/search.go`
- Modify: `internal/api/server.go` (register routes)
- Test: `internal/api/search_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/api/search_test.go`:

```go
package api

import (
	"fmt"
	"testing"

	"github.com/davidgibbons/innkeeper/internal/library"
)

func TestSearchRoute(t *testing.T) {
	e := newEnv(t)
	a := e.createCard(`{"name": "Brakka", "description": "A dwarf blacksmith."}`)
	e.createCard(`{"name": "Carrow", "description": "A sailor."}`)
	e.call("PUT", fmt.Sprintf("/cards/%d/tags", a.ID), map[string]any{"tags": []string{"fantasy"}}, 200, nil)

	var res library.Results
	e.call("POST", "/search", map[string]any{"text": "blacksmith", "filter": map[string]any{"tag": "fantasy"},
		"include": []string{"tags"}}, 200, &res)
	if len(res.Hits) != 1 || res.Hits[0].ID != a.ID || res.Hits[0].Fields["name"] != "Brakka" || res.Hits[0].Tags[0] != "fantasy" {
		t.Fatalf("results = %+v", res)
	}

	e.call("POST", "/search", map[string]any{"page": map[string]any{"size": 1, "cursor": nil}}, 200, &res)
	if len(res.Hits) != 1 || res.NextCursor == nil {
		t.Fatalf("first page = %+v", res)
	}

	e.call("POST", "/search", map[string]any{"semantic": true}, 400, nil)
	e.call("POST", "/search", map[string]any{"filter": map[string]any{"tag": "a", "archived": true}}, 400, nil)
	e.call("POST", "/search", map[string]any{"filter": map[string]any{"bogus": 1}}, 422, nil)
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -run TestSearchRoute ./internal/api/`
Expected: FAIL: `POST /search: got 404`.

- [ ] **Step 3: Write the route**

Create `internal/api/search.go`:

```go
package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/davidgibbons/innkeeper/internal/library"
)

type searchIn struct{ Body library.Query }

type searchOut struct{ Body library.Results }

func (s *Server) searchRoutes(api huma.API) {
	huma.Register(api, huma.Operation{OperationID: "search-cards", Method: http.MethodPost, Path: "/search",
		Summary: "Search the library", Description: "Send {} to list every card that isn't archived, newest first.",
		Tags: []string{"Search"}},
		func(ctx context.Context, in *searchIn) (*searchOut, error) {
			res, err := s.Lib.Search(ctx, in.Body)
			if err != nil {
				return nil, s.fail(err)
			}
			return &searchOut{Body: res}, nil
		})
}
```

In `Handler`, after `s.organizeRoutes(api)`, add:

```go
	s.searchRoutes(api)
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./internal/api/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api
git commit -m "Add POST /search"
```

---

### Task 14: Plugin and instance routes

**Files:**
- Create: `internal/api/instances.go`
- Modify: `internal/api/server.go` (register routes)
- Test: `internal/api/instances_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/api/instances_test.go`:

```go
package api

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/davidgibbons/innkeeper/internal/plugins"
)

func TestInstanceRoutes(t *testing.T) {
	e := newEnv(t)
	var installed []pluginOut
	e.call("GET", "/plugins", nil, 200, &installed)
	if len(installed) != 2 || installed[0].Name != "fake" || installed[1].Name != "vault" || installed[1].Secrets[0] != "api_key" {
		t.Fatalf("plugins = %+v", installed)
	}

	var inst plugins.InstanceInfo
	r := e.call("POST", "/instances", map[string]any{"plugin": "vault", "name": "Vault",
		"config": map[string]any{"url": "http://example.com"}, "secrets": map[string]string{"api_key": "hunter2"}}, 201, &inst)
	if strings.Contains(string(r.Body), "hunter2") || len(inst.Secrets) != 1 || inst.Config["url"] != "http://example.com" {
		t.Fatalf("created %s", r)
	}
	e.call("POST", "/instances", map[string]any{"plugin": "vault", "name": "Vault"}, 409, nil)
	e.call("POST", "/instances", map[string]any{"plugin": "missing", "name": "X"}, 400, nil)
	e.call("POST", "/instances", map[string]any{"plugin": "vault", "name": "Y", "secrets": map[string]string{"password": "x"}}, 400, nil)
	e.call("POST", "/instances", map[string]any{"plugin": "vault", "name": "Z", "owner_subject": ""}, 400, nil)

	path := fmt.Sprintf("/instances/%d", inst.ID)
	if err := e.s.Registry.MarkBroken(context.Background(), inst.ID, errors.New("bad key")); err != nil {
		t.Fatal(err)
	}
	e.call("PATCH", path, map[string]any{"name": "Vault 2", "secrets": map[string]any{"api_key": nil}}, 200, &inst)
	if inst.Name != "Vault 2" || len(inst.Secrets) != 0 || inst.BrokenError != nil || inst.Config["url"] != "http://example.com" {
		t.Fatalf("patched %+v", inst)
	}

	var list []plugins.InstanceInfo
	e.call("GET", "/instances", nil, 200, &list)
	if len(list) != 3 || list[2].Name != "Vault 2" {
		t.Fatalf("instances = %+v", list)
	}
	e.call("DELETE", path, nil, 204, nil)
	e.call("GET", path, nil, 404, nil)
	e.call("PATCH", path, map[string]any{"name": "X"}, 404, nil)
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -run TestInstanceRoutes ./internal/api/`
Expected: FAIL to compile: `undefined: pluginOut`.

- [ ] **Step 3: Write the routes**

Create `internal/api/instances.go`:

```go
package api

import (
	"context"
	"net/http"
	"slices"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/davidgibbons/innkeeper/internal/plugins"
)

type enrichmentOut struct {
	Type  string    `json:"type"`
	Label string    `json:"label"`
	Range []float64 `json:"range,omitempty"`
}

type pluginOut struct {
	Name         string                   `json:"name"`
	Version      string                   `json:"version"`
	Capabilities []string                 `json:"capabilities"`
	Secrets      []string                 `json:"secrets"`
	Config       map[string]any           `json:"config,omitempty" doc:"JSON Schema for an instance's config"`
	Enrichments  map[string]enrichmentOut `json:"enrichments,omitempty"`
}

type pluginsOut struct{ Body []pluginOut }

type instancesOut struct{ Body []plugins.InstanceInfo }

type instanceOut struct{ Body plugins.InstanceInfo }

type createInstanceIn struct {
	Body struct {
		Plugin       string            `json:"plugin"`
		Name         string            `json:"name" minLength:"1"`
		Config       map[string]any    `json:"config,omitempty"`
		Secrets      map[string]string `json:"secrets,omitempty" doc:"Write-only. Responses list only the names that are set."`
		OwnerSubject *string           `json:"owner_subject,omitempty" doc:"For a login provider: the subject that logs in as the owner."`
	}
}

type updateInstanceIn struct {
	ID   int64 `path:"id"`
	Body struct {
		Name         *string            `json:"name,omitempty" minLength:"1"`
		Config       map[string]any     `json:"config,omitempty" doc:"Replaces the whole config."`
		Secrets      map[string]*string `json:"secrets,omitempty" doc:"Sets each secret named; null removes it. Secrets not named stay."`
		OwnerSubject *string            `json:"owner_subject,omitempty" doc:"Empty clears it, which logs the owner out of this provider."`
	}
}

func (s *Server) instance(ctx context.Context, id int64) (*instanceOut, error) {
	i, err := s.Registry.GetInstance(ctx, id)
	if err != nil {
		return nil, s.fail(err)
	}
	return &instanceOut{Body: i}, nil
}

func (s *Server) instanceRoutes(api huma.API) {
	tags := []string{"Plugins"}
	huma.Register(api, huma.Operation{OperationID: "list-plugins", Method: http.MethodGet, Path: "/plugins",
		Summary: "List installed plugins", Tags: tags},
		func(ctx context.Context, _ *struct{}) (*pluginsOut, error) {
			out := &pluginsOut{Body: []pluginOut{}}
			for _, m := range s.Registry.Manifests() {
				p := pluginOut{Name: m.Name, Version: m.Version, Capabilities: m.Capabilities,
					Secrets: orEmpty(m.Secrets), Config: m.Config}
				for k, e := range m.Enrichments {
					if p.Enrichments == nil {
						p.Enrichments = map[string]enrichmentOut{}
					}
					p.Enrichments[k] = enrichmentOut{e.Type, e.Label, e.Range}
				}
				out.Body = append(out.Body, p)
			}
			slices.SortFunc(out.Body, func(a, b pluginOut) int { return strings.Compare(a.Name, b.Name) })
			return out, nil
		})

	huma.Register(api, huma.Operation{OperationID: "list-instances", Method: http.MethodGet, Path: "/instances",
		Summary: "List plugin instances", Tags: tags},
		func(ctx context.Context, _ *struct{}) (*instancesOut, error) {
			list, err := s.Registry.ListInstances(ctx)
			if err != nil {
				return nil, s.fail(err)
			}
			return &instancesOut{Body: orEmpty(list)}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "create-instance", Method: http.MethodPost, Path: "/instances",
		Summary: "Configure a plugin instance", Tags: tags, DefaultStatus: http.StatusCreated},
		func(ctx context.Context, in *createInstanceIn) (*instanceOut, error) {
			id, err := s.Registry.CreateInstance(ctx, plugins.NewInstance{Plugin: in.Body.Plugin, Name: in.Body.Name,
				Config: in.Body.Config, Secrets: in.Body.Secrets, OwnerSubject: in.Body.OwnerSubject})
			if err != nil {
				return nil, s.fail(err)
			}
			return s.instance(ctx, id)
		})

	huma.Register(api, huma.Operation{OperationID: "get-instance", Method: http.MethodGet, Path: "/instances/{id}",
		Summary: "Get a plugin instance", Tags: tags},
		func(ctx context.Context, in *idIn) (*instanceOut, error) {
			return s.instance(ctx, in.ID)
		})

	huma.Register(api, huma.Operation{OperationID: "update-instance", Method: http.MethodPatch, Path: "/instances/{id}",
		Summary: "Change a plugin instance", Description: "Clears a broken instance, and its plugin restarts with the new settings.", Tags: tags},
		func(ctx context.Context, in *updateInstanceIn) (*instanceOut, error) {
			err := s.Registry.UpdateInstance(ctx, in.ID, plugins.InstanceEdit{Name: in.Body.Name, Config: in.Body.Config,
				Secrets: in.Body.Secrets, OwnerSubject: in.Body.OwnerSubject})
			if err != nil {
				return nil, s.fail(err)
			}
			return s.instance(ctx, in.ID)
		})

	huma.Register(api, huma.Operation{OperationID: "delete-instance", Method: http.MethodDelete, Path: "/instances/{id}",
		Summary: "Delete a plugin instance", Description: "Its jobs stay, and logins through it end.", Tags: tags, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *idIn) (*struct{}, error) {
			return nil, s.fail(s.Registry.DeleteInstance(ctx, in.ID))
		})
}
```

In `Handler`, after `s.searchRoutes(api)`, add:

```go
	s.instanceRoutes(api)
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./internal/api/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api
git commit -m "Add plugin and instance routes"
```

---

### Task 15: Job routes

**Files:**
- Create: `internal/api/jobs.go`
- Modify: `internal/api/server.go` (register routes)
- Modify: `internal/api/api_test.go` (`newEnv` shortens the watch interval)
- Test: `internal/api/jobs_test.go`

- [ ] **Step 1: Write the failing tests**

In `newEnv` in `internal/api/api_test.go`, below `failedLoginDelay = 0`, add:

```go
	watchInterval = 20 * time.Millisecond
```

Create `internal/api/jobs_test.go`:

```go
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/davidgibbons/innkeeper/internal/bulk"
	"github.com/davidgibbons/innkeeper/internal/jobs"
	"github.com/davidgibbons/innkeeper/internal/library"
	"github.com/davidgibbons/innkeeper/internal/plugins"
)

func TestImportAndCancel(t *testing.T) {
	e := newEnv(t)
	var j jobOut
	e.call("POST", "/imports", map[string]any{"instance_id": e.importID, "params": map[string]int{"cards": 2}}, 202, &j)
	if j.Kind != "import" || j.Status != "queued" || *j.InstanceID != e.importID || !strings.Contains(string(j.Payload), `"cards"`) {
		t.Fatalf("import job = %+v", j)
	}
	var list []jobOut
	e.call("GET", "/jobs", nil, 200, &list)
	if len(list) != 1 || list[0].ID != j.ID {
		t.Fatalf("jobs = %+v", list)
	}
	e.call("GET", "/jobs?status=failed", nil, 200, &list)
	if len(list) != 0 {
		t.Fatalf("failed jobs = %+v", list)
	}

	path := fmt.Sprintf("/jobs/%d", j.ID)
	e.call("POST", path+"/cancel", nil, 200, &j)
	if j.Status != "failed" || *j.Error != "cancelled" {
		t.Fatalf("cancelled = %+v", j)
	}
	e.call("POST", path+"/cancel", nil, 409, nil)
	e.call("GET", "/jobs/999", nil, 404, nil)
	e.call("GET", "/jobs/999/events", nil, 404, nil)

	vault, err := e.s.Registry.CreateInstance(context.Background(), plugins.NewInstance{Plugin: "vault", Name: "Vault"})
	if err != nil {
		t.Fatal(err)
	}
	e.call("POST", "/imports", map[string]any{"instance_id": vault}, 400, nil)
	e.call("POST", "/imports", map[string]any{"instance_id": 999}, 404, nil)
}

func TestBulkArchiveAndWatch(t *testing.T) {
	e := newEnv(t)
	for _, name := range []string{"A", "B", "C"} {
		c := e.createCard(fmt.Sprintf(`{"name": %q}`, name))
		if name != "C" {
			e.call("PUT", fmt.Sprintf("/cards/%d/tags", c.ID), map[string]any{"tags": []string{"old"}}, 200, nil)
		}
	}
	w := &jobs.Worker{Queue: e.s.Queue, Log: quiet, Poll: 50 * time.Millisecond,
		Handlers: map[string]jobs.Handler{bulk.ArchiveKind: bulk.Archive(e.s.Lib)}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })

	var j jobOut
	e.call("POST", "/bulk", map[string]any{"query": map[string]any{"filter": map[string]any{"tag": "old"}}, "action": "archive"}, 202, &j)
	total := 0
	for _, n := range j.Children {
		total += n
	}
	if j.Kind != bulk.Kind || total != 2 {
		t.Fatalf("bulk job = %+v", j)
	}

	// The stream ends when the job finishes; the last event shows how.
	r := e.call("GET", fmt.Sprintf("/jobs/%d/events", j.ID), nil, 200, nil)
	if r.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("events content type %q", r.Header.Get("Content-Type"))
	}
	var last jobOut
	for _, line := range strings.Split(string(r.Body), "\n") {
		if data, ok := strings.CutPrefix(line, "data: "); ok {
			if err := json.Unmarshal([]byte(data), &last); err != nil {
				t.Fatal(err)
			}
		}
	}
	if last.Status != "succeeded" || last.Children["succeeded"] != 2 {
		t.Fatalf("last event = %+v\n%s", last, r.Body)
	}

	var res library.Results
	e.call("POST", "/search", map[string]any{}, 200, &res)
	if len(res.Hits) != 1 || res.Hits[0].Fields["name"] != "C" {
		t.Fatalf("cards left = %+v", res.Hits)
	}
	var children []jobOut
	e.call("GET", fmt.Sprintf("/jobs?parent=%d", j.ID), nil, 200, &children)
	if len(children) != 2 {
		t.Fatalf("children = %+v", children)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -run 'TestImportAndCancel|TestBulkArchiveAndWatch' ./internal/api/`
Expected: FAIL to compile: `undefined: watchInterval`.

- [ ] **Step 3: Write the routes**

Create `internal/api/jobs.go`:

```go
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/davidgibbons/innkeeper/internal/bulk"
	"github.com/davidgibbons/innkeeper/internal/importer"
	"github.com/davidgibbons/innkeeper/internal/jobs"
	"github.com/davidgibbons/innkeeper/internal/library"
)

// watchInterval is how often a job stream checks the job. Tests shorten it.
var watchInterval = time.Second

type jobOut struct {
	ID          int64           `json:"id"`
	Kind        string          `json:"kind"`
	InstanceID  *int64          `json:"instance_id,omitempty"`
	Payload     json.RawMessage `json:"payload"`
	Status      string          `json:"status" enum:"queued,running,waiting,succeeded,failed"`
	Attempts    int             `json:"attempts"`
	MaxAttempts int             `json:"max_attempts"`
	Progress    json.RawMessage `json:"progress" doc:"An import's has done, total, message, imported, skipped, failed, and errors."`
	ParentID    *int64          `json:"parent_id,omitempty"`
	Error       *string         `json:"error,omitempty"`
	RunAfter    time.Time       `json:"run_after"`
	CreatedAt   time.Time       `json:"created_at"`
	FinishedAt  *time.Time      `json:"finished_at,omitempty"`
	Children    map[string]int  `json:"children,omitempty" doc:"A bulk job's children, counted by status"`
}

func toJob(j jobs.Job) jobOut {
	return jobOut{ID: j.ID, Kind: j.Kind, InstanceID: j.InstanceID, Payload: j.Payload, Status: j.Status,
		Attempts: j.Attempts, MaxAttempts: j.MaxAttempts, Progress: j.Progress, ParentID: j.ParentID, Error: j.Error,
		RunAfter: j.RunAfter, CreatedAt: j.CreatedAt, FinishedAt: j.FinishedAt}
}

type jobResp struct{ Body jobOut }

type jobsOut struct{ Body []jobOut }

type listJobsIn struct {
	Status string `query:"status" enum:"queued,running,waiting,succeeded,failed"`
	Kind   string `query:"kind"`
	Parent int64  `query:"parent" doc:"List this bulk job's children instead of top-level jobs"`
	Before int64  `query:"before" doc:"Only jobs with smaller IDs, for the next page"`
	Limit  int    `query:"limit" default:"50" minimum:"1" maximum:"200"`
}

type importIn struct {
	Body struct {
		InstanceID int64           `json:"instance_id"`
		Params     json.RawMessage `json:"params,omitempty" doc:"The import plugin's settings, such as a folder path"`
	}
}

type bulkIn struct {
	Body struct {
		Query  library.Query `json:"query" doc:"Selects the cards, as in POST /search. Paging is ignored."`
		Action string        `json:"action" enum:"archive,unarchive"`
	}
}

// job returns a job, with child counts for a bulk job.
func (s *Server) job(ctx context.Context, id int64) (jobOut, error) {
	j, err := s.Queue.Get(ctx, id)
	if err != nil {
		return jobOut{}, s.fail(err)
	}
	out := toJob(j)
	if j.Kind == bulk.Kind {
		if out.Children, err = s.Queue.ChildCounts(ctx, id); err != nil {
			return jobOut{}, s.fail(err)
		}
	}
	return out, nil
}

// watch sends the job as an SSE event each time it changes, until it
// finishes or the client goes away.
//
// ponytail: polls the database every watchInterval per watcher; use
// LISTEN/NOTIFY if many clients watch at once.
func (s *Server) watch(hc huma.Context, j jobOut) {
	ctx := hc.Context()
	hc.SetHeader("Content-Type", "text/event-stream")
	hc.SetHeader("Cache-Control", "no-cache")
	w := hc.BodyWriter()
	flusher, _ := w.(http.Flusher)
	t := time.NewTicker(watchInterval)
	defer t.Stop()
	var last []byte
	for {
		data, err := json.Marshal(j)
		if err != nil {
			return
		}
		if !bytes.Equal(data, last) {
			if _, err := fmt.Fprintf(w, "event: job\ndata: %s\n\n", data); err != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
			last = data
		}
		if j.Status == jobs.StatusSucceeded || j.Status == jobs.StatusFailed {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if j, err = s.job(ctx, j.ID); err != nil {
			return
		}
	}
}

func (s *Server) jobRoutes(api huma.API) {
	tags := []string{"Jobs"}
	huma.Register(api, huma.Operation{OperationID: "list-jobs", Method: http.MethodGet, Path: "/jobs",
		Summary: "List jobs", Description: "Newest first.", Tags: tags},
		func(ctx context.Context, in *listJobsIn) (*jobsOut, error) {
			o := jobs.ListOptions{Status: in.Status, Kind: in.Kind, Before: in.Before, Limit: in.Limit}
			if in.Parent != 0 {
				o.Parent = &in.Parent
			}
			list, err := s.Queue.List(ctx, o)
			if err != nil {
				return nil, s.fail(err)
			}
			out := &jobsOut{Body: make([]jobOut, len(list))}
			for i, j := range list {
				out.Body[i] = toJob(j)
			}
			return out, nil
		})

	huma.Register(api, huma.Operation{OperationID: "get-job", Method: http.MethodGet, Path: "/jobs/{id}",
		Summary: "Get a job", Tags: tags},
		func(ctx context.Context, in *idIn) (*jobResp, error) {
			j, err := s.job(ctx, in.ID)
			if err != nil {
				return nil, err
			}
			return &jobResp{Body: j}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "cancel-job", Method: http.MethodPost, Path: "/jobs/{id}/cancel",
		Summary: "Cancel a job", Description: "Fails the job and its unfinished children. A running job stops within seconds.", Tags: tags},
		func(ctx context.Context, in *idIn) (*jobResp, error) {
			if err := s.Queue.Cancel(ctx, in.ID); err != nil {
				return nil, s.fail(err)
			}
			j, err := s.job(ctx, in.ID)
			if err != nil {
				return nil, err
			}
			return &jobResp{Body: j}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "watch-job", Method: http.MethodGet, Path: "/jobs/{id}/events",
		Summary: "Stream a job's progress",
		Description: "Server-sent events: a job event with the whole job each time it changes. The stream ends when the job finishes.",
		Tags:        tags,
		Responses: map[string]*huma.Response{"200": {Description: "A stream of job events",
			Content: map[string]*huma.MediaType{"text/event-stream": {}}}}},
		func(ctx context.Context, in *idIn) (*huma.StreamResponse, error) {
			first, err := s.job(ctx, in.ID)
			if err != nil {
				return nil, err
			}
			return &huma.StreamResponse{Body: func(hc huma.Context) { s.watch(hc, first) }}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "start-import", Method: http.MethodPost, Path: "/imports",
		Summary: "Start an import", Description: "Queues an import job for an instance of an import plugin.",
		Tags: tags, DefaultStatus: http.StatusAccepted},
		func(ctx context.Context, in *importIn) (*jobResp, error) {
			inst, err := s.Registry.GetInstance(ctx, in.Body.InstanceID)
			if err != nil {
				return nil, s.fail(err)
			}
			if m := s.Registry.Manifests()[inst.Plugin]; m == nil || !slices.Contains(m.Capabilities, "import") {
				return nil, huma.Error400BadRequest(fmt.Sprintf("plugin %s can't import", inst.Plugin))
			}
			params := in.Body.Params
			if params == nil {
				params = json.RawMessage(`{}`)
			}
			j, err := s.Queue.Enqueue(ctx, jobs.Spec{Kind: importer.Kind, InstanceID: &in.Body.InstanceID,
				Payload: importer.Payload{Params: params}})
			if err != nil {
				return nil, s.fail(err)
			}
			return &jobResp{Body: toJob(j)}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "start-bulk", Method: http.MethodPost, Path: "/bulk",
		Summary: "Archive or unarchive many cards", Description: "Queues one child job per card the query matches.",
		Tags: tags, DefaultStatus: http.StatusAccepted},
		func(ctx context.Context, in *bulkIn) (*jobResp, error) {
			ids, err := s.Lib.SearchIDs(ctx, in.Body.Query)
			if err != nil {
				return nil, s.fail(err)
			}
			p, err := bulk.EnqueueArchive(ctx, s.Queue, ids, in.Body.Action == "archive")
			if err != nil {
				return nil, s.fail(err)
			}
			j, err := s.job(ctx, p.ID)
			if err != nil {
				return nil, err
			}
			return &jobResp{Body: j}, nil
		})
}
```

In `Handler`, after `s.instanceRoutes(api)`, add:

```go
	s.jobRoutes(api)
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./internal/api/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api
git commit -m "Add job, import, and bulk routes with SSE progress"
```

---

### Task 16: `innkeeper api` and `innkeeper token`

**Files:**
- Create: `cmd/innkeeper/services.go`
- Create: `cmd/innkeeper/api.go`
- Create: `cmd/innkeeper/token.go`
- Modify: `cmd/innkeeper/worker.go` (use `services.go`; schedule pruning)
- Modify: `cmd/innkeeper/main.go` (dispatch; `migrate` uses `openDB`)

This task is wiring. Step 6 checks it against a real database.

- [ ] **Step 1: Write the shared setup**

Create `cmd/innkeeper/services.go`:

```go
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/davidgibbons/innkeeper/internal/blobstore"
	"github.com/davidgibbons/innkeeper/internal/db"
	"github.com/davidgibbons/innkeeper/internal/library"
	"github.com/davidgibbons/innkeeper/internal/pluginhost"
	"github.com/davidgibbons/innkeeper/internal/plugins"
	"github.com/davidgibbons/innkeeper/internal/secrets"
	"github.com/davidgibbons/innkeeper/protocol"
)

// services are what the api and worker both use.
type services struct {
	pool      *pgxpool.Pool
	lib       *library.Library
	registry  *plugins.Registry
	manifests map[string]*protocol.Manifest
}

// openServices reads the settings both services need, loads the installed
// plugins, and connects to and migrates the database.
func openServices(ctx context.Context, log *slog.Logger) (*services, error) {
	cfg := map[string]string{}
	for _, k := range []string{"INNKEEPER_BLOB_DIR", "INNKEEPER_PLUGINS_DIR"} {
		if cfg[k] = os.Getenv(k); cfg[k] == "" {
			return nil, fmt.Errorf("%s is not set", k)
		}
	}
	box, err := secrets.FromEnv()
	if err != nil {
		return nil, err
	}
	// Plugins run in their own folder, so a relative blob dir would point elsewhere for them.
	blobDir, err := filepath.Abs(cfg["INNKEEPER_BLOB_DIR"])
	if err != nil {
		return nil, err
	}
	manifests, err := plugins.LoadDir(cfg["INNKEEPER_PLUGINS_DIR"], log)
	if err != nil {
		return nil, err
	}
	pool, err := openDB(ctx)
	if err != nil {
		return nil, err
	}
	store := &blobstore.Store{Name: "library", Root: blobDir}
	return &services{pool: pool, lib: library.New(pool, store),
		registry: plugins.New(pool, manifests, store, box), manifests: manifests}, nil
}

func (s *services) close() { s.pool.Close() }

// openDB connects to INNKEEPER_DATABASE_URL and migrates the schema.
func openDB(ctx context.Context) (*pgxpool.Pool, error) {
	url := os.Getenv("INNKEEPER_DATABASE_URL")
	if url == "" {
		return nil, errors.New("INNKEEPER_DATABASE_URL is not set")
	}
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, err
	}
	if err := db.Migrate(ctx, pool); err != nil {
		pool.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return pool, nil
}

// signalContext ends on SIGINT or SIGTERM. After the first signal, default
// handling returns, so a second one kills a stuck shutdown.
func signalContext() (context.Context, context.CancelFunc) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	go func() { <-ctx.Done(); stop() }()
	return ctx, stop
}

// stopIdlePlugins stops mgr's idle plugins every minute until ctx ends.
func stopIdlePlugins(ctx context.Context, mgr *pluginhost.Manager) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			mgr.StopIdle()
		}
	}
}
```

- [ ] **Step 2: Move the worker onto it**

Replace `cmd/innkeeper/worker.go` with:

```go
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/davidgibbons/innkeeper/internal/bulk"
	"github.com/davidgibbons/innkeeper/internal/importer"
	"github.com/davidgibbons/innkeeper/internal/jobs"
	"github.com/davidgibbons/innkeeper/internal/pluginhost"
)

const (
	blobGCKind   = "blob.gc"
	jobPruneKind = "job.prune"
	// jobRetention is how long finished jobs stay in the job list.
	jobRetention = 30 * 24 * time.Hour
)

func worker() error {
	concurrency := 4
	if v := os.Getenv("INNKEEPER_WORKER_CONCURRENCY"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return errors.New("INNKEEPER_WORKER_CONCURRENCY must be a positive number")
		}
		concurrency = n
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	ctx, stop := signalContext()
	defer stop()
	svc, err := openServices(ctx, log)
	if err != nil {
		return err
	}
	defer svc.close()

	im := importer.New(svc.lib, svc.registry, log)
	mgr := pluginhost.NewManager(im.Handle, 5*time.Minute, log)
	defer mgr.Close()
	im.Plugins = mgr
	go stopIdlePlugins(ctx, mgr)

	q := jobs.New(svc.pool)
	w := &jobs.Worker{
		Queue:       q,
		Log:         log,
		Concurrency: concurrency,
		Handlers: map[string]jobs.Handler{
			importer.Kind:    im.Run,
			bulk.ArchiveKind: bulk.Archive(svc.lib),
			blobGCKind: func(ctx context.Context, r *jobs.Run) error {
				blobs, leftovers, err := svc.lib.CollectGarbage(ctx, 24*time.Hour)
				r.SetProgress(fmt.Appendf(nil, `{"blobs": %d, "leftovers": %d}`, blobs, leftovers))
				return err
			},
			jobPruneKind: func(ctx context.Context, r *jobs.Run) error {
				n, err := q.Prune(ctx, jobRetention)
				r.SetProgress(fmt.Appendf(nil, `{"deleted": %d}`, n))
				return err
			},
		},
		Schedules: []jobs.Schedule{
			{Kind: blobGCKind, Every: 24 * time.Hour},
			{Kind: jobPruneKind, Every: 24 * time.Hour},
		},
	}
	log.Info("worker started", "plugins", len(svc.manifests), "concurrency", concurrency)
	return w.Run(ctx)
}
```

- [ ] **Step 3: Write `innkeeper api`**

Create `cmd/innkeeper/api.go`:

```go
package main

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/davidgibbons/innkeeper/internal/api"
	"github.com/davidgibbons/innkeeper/internal/auth"
	"github.com/davidgibbons/innkeeper/internal/jobs"
	"github.com/davidgibbons/innkeeper/internal/pluginhost"
)

func apiServer() error {
	listen := os.Getenv("INNKEEPER_LISTEN")
	if listen == "" {
		listen = ":8080"
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	ctx, stop := signalContext()
	defer stop()
	svc, err := openServices(ctx, log)
	if err != nil {
		return err
	}
	defer svc.close()
	mgr := pluginhost.NewManager(api.NoCore, 5*time.Minute, log)
	defer mgr.Close()
	go stopIdlePlugins(ctx, mgr)

	s := &api.Server{Lib: svc.lib, Queue: jobs.New(svc.pool), Registry: svc.registry, Auth: auth.New(svc.pool),
		Plugins: mgr, Log: log, PublicURL: os.Getenv("INNKEEPER_PUBLIC_URL")}
	srv := &http.Server{
		Addr:              listen,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		// Requests end with ctx, so open job streams don't hold up shutdown.
		BaseContext: func(net.Listener) context.Context { return ctx },
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("api listening", "addr", listen, "plugins", len(svc.manifests))
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(sctx)
}
```

- [ ] **Step 4: Write `innkeeper token`**

Create `cmd/innkeeper/token.go`:

```go
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"text/tabwriter"
	"time"

	"github.com/davidgibbons/innkeeper/internal/auth"
)

// token runs `innkeeper token create|list|revoke` against the database, so
// the owner can make the first token before anyone can log in.
func token(args []string) error {
	ctx := context.Background()
	pool, err := openDB(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	a := auth.New(pool)
	switch {
	case len(args) == 2 && args[0] == "create":
		t, secret, err := a.CreateToken(ctx, args[1])
		if err != nil {
			return err
		}
		fmt.Println(secret)
		fmt.Fprintf(os.Stderr, "created token %d; it is not shown again\n", t.ID)
		return nil
	case len(args) == 1 && args[0] == "list":
		list, err := a.Tokens(ctx)
		if err != nil {
			return err
		}
		tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tNAME\tCREATED\tLAST USED")
		for _, t := range list {
			used := "never"
			if t.LastUsedAt != nil {
				used = t.LastUsedAt.Local().Format(time.DateTime)
			}
			fmt.Fprintf(tw, "%d\t%s\t%s\t%s\n", t.ID, t.Name, t.CreatedAt.Local().Format(time.DateTime), used)
		}
		return tw.Flush()
	case len(args) == 2 && args[0] == "revoke":
		id, err := strconv.ParseInt(args[1], 10, 64)
		if err != nil {
			return fmt.Errorf("token ID %q is not a number", args[1])
		}
		return a.RevokeToken(ctx, id)
	}
	return errors.New(usage)
}
```

- [ ] **Step 5: Dispatch the new commands**

In `cmd/innkeeper/main.go`, replace `run` and `migrate`:

```go
func run(args []string) error {
	if len(args) == 0 {
		return errors.New(usage)
	}
	cmd, rest := args[0], args[1:]
	switch {
	case cmd == "plugin" && len(rest) == 2 && rest[0] == "check":
		return pluginCheck(rest[1])
	case cmd == "migrate" && len(rest) == 0:
		return migrate()
	case cmd == "worker" && len(rest) == 0:
		return worker()
	case cmd == "api" && len(rest) == 0:
		return apiServer()
	case cmd == "token":
		return token(rest)
	}
	return errors.New(usage)
}
```

```go
func migrate() error {
	pool, err := openDB(context.Background())
	if err != nil {
		return err
	}
	pool.Close()
	return nil
}
```

Replace `usage`:

```go
const usage = `usage:
  innkeeper plugin check <plugin-dir>
  innkeeper migrate
  innkeeper worker
  innkeeper api
  innkeeper token create <name> | list | revoke <id>

See README.md for the environment variables each command reads.`
```

Remove the now-unused `pgxpool` and `db` imports from `main.go`.

- [ ] **Step 6: Check it builds and serves**

```bash
go build ./... && go vet ./cmd/innkeeper/
docker exec innkeeper-pg createdb -U postgres innkeeper_smoke
tmp=$(mktemp -d) && mkdir -p "$tmp/blobs" "$tmp/plugins"
go build -o "$tmp/innkeeper" ./cmd/innkeeper
export INNKEEPER_DATABASE_URL=postgres://postgres:postgres@localhost:55432/innkeeper_smoke
export INNKEEPER_BLOB_DIR=$tmp/blobs INNKEEPER_PLUGINS_DIR=$tmp/plugins INNKEEPER_LISTEN=127.0.0.1:18080
INNKEEPER_SECRETS_KEY= "$tmp/innkeeper" api; echo "exit $?"
export INNKEEPER_SECRETS_KEY=$(openssl rand -base64 32)
"$tmp/innkeeper" api & sleep 2
TOKEN=$("$tmp/innkeeper" token create smoke)
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:18080/tags
curl -s -H "Authorization: Bearer $TOKEN" http://127.0.0.1:18080/tags
kill %1; wait
```

Expected:
- The first `api` run prints `innkeeper: INNKEEPER_SECRETS_KEY is not set; generate one with: openssl rand -base64 32` and `exit 1`.
- The `curl` without a token prints `401`, the one with it prints `[]`.
- `kill` stops the server cleanly.

Keep `$tmp` and `innkeeper_smoke` for Task 18.

- [ ] **Step 7: Commit**

```bash
git add cmd/innkeeper
git commit -m "Add innkeeper api and innkeeper token"
```

---

### Task 17: CLI commands

**Files:**
- Create: `cmd/innkeeper/client.go`
- Create: `cmd/innkeeper/commands.go`
- Modify: `cmd/innkeeper/main.go` (dispatch, usage)
- Test: `cmd/innkeeper/client_test.go`

- [ ] **Step 1: Write the failing test**

Create `cmd/innkeeper/client_test.go`:

```go
package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientReportsAPIErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		fmt.Fprint(w, `{"status": 422, "detail": "validation failed", "errors": [{"message": "expected number", "location": "body.instance_id"}]}`)
	}))
	defer srv.Close()
	c := &client{base: srv.URL, token: "tok"}
	_, err := c.do(context.Background(), "POST", "/imports", []byte(`{}`))
	if err == nil || !strings.Contains(err.Error(), "422 validation failed; body.instance_id: expected number") {
		t.Fatalf("err = %v", err)
	}
	c.token = "wrong"
	if _, err := c.do(context.Background(), "GET", "/jobs", nil); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("err = %v", err)
	}
}

func TestClientStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: job\ndata: {\"n\": 1}\n\nevent: job\ndata: {\"n\": 2}\n\n")
	}))
	defer srv.Close()
	c := &client{base: srv.URL, token: "tok"}
	var got []string
	err := c.stream(context.Background(), "/jobs/1/events", func(data []byte) error {
		got = append(got, string(data))
		return nil
	})
	if err != nil || strings.Join(got, ",") != `{"n": 1},{"n": 2}` {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestNewClientNeedsSettings(t *testing.T) {
	t.Setenv("INNKEEPER_URL", "http://localhost:8080")
	t.Setenv("INNKEEPER_TOKEN", "")
	if _, err := newClient(); err == nil {
		t.Fatal("no error without a token")
	}
}

func TestParseID(t *testing.T) {
	if _, err := parseID("../tokens"); err == nil {
		t.Fatal("accepted a path as an ID")
	}
	if id, err := parseID("12"); err != nil || id != "12" {
		t.Fatalf("parseID(12) = %q, %v", id, err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./cmd/innkeeper/`
Expected: FAIL to compile: `undefined: client`.

- [ ] **Step 3: Write the client**

Create `cmd/innkeeper/client.go`:

```go
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
)

// client calls the HTTP API at INNKEEPER_URL with INNKEEPER_TOKEN.
type client struct{ base, token string }

func newClient() (*client, error) {
	c := &client{base: strings.TrimSuffix(os.Getenv("INNKEEPER_URL"), "/"), token: os.Getenv("INNKEEPER_TOKEN")}
	if c.base == "" || c.token == "" {
		return nil, errors.New("set INNKEEPER_URL and INNKEEPER_TOKEN; make a token with: innkeeper token create <name>")
	}
	return c, nil
}

func (c *client) request(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, r)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 300 {
		return resp, nil
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var p struct {
		Detail string `json:"detail"`
		Errors []struct {
			Message  string `json:"message"`
			Location string `json:"location"`
		} `json:"errors"`
	}
	if json.Unmarshal(raw, &p) != nil || p.Detail == "" {
		return nil, fmt.Errorf("%s %s: %s", method, path, resp.Status)
	}
	msg := p.Detail
	for _, e := range p.Errors {
		msg += fmt.Sprintf("; %s: %s", e.Location, e.Message)
	}
	return nil, fmt.Errorf("%s %s: %d %s", method, path, resp.StatusCode, msg)
}

// do sends body as JSON, or nothing if it is nil, and returns the response
// body. An error status becomes an error with the API's explanation.
func (c *client) do(ctx context.Context, method, path string, body []byte) ([]byte, error) {
	resp, err := c.request(ctx, method, path, body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

// stream calls fn with each server-sent event's data until the stream ends.
func (c *client) stream(ctx context.Context, path string, fn func(data []byte) error) error {
	resp, err := c.request(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		if data, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
			if err := fn([]byte(data)); err != nil {
				return err
			}
		}
	}
	return sc.Err()
}

// parseID checks that s is a numeric ID before it goes into a URL path.
func parseID(s string) (string, error) {
	if _, err := strconv.ParseInt(s, 10, 64); err != nil {
		return "", fmt.Errorf("%q is not an ID", s)
	}
	return s, nil
}
```

- [ ] **Step 4: Write the commands**

Create `cmd/innkeeper/commands.go`:

```go
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"
)

// jsonArg returns args[i] if it is valid JSON, or def if args has no index i.
func jsonArg(args []string, i int, def string) (json.RawMessage, error) {
	if len(args) <= i {
		return json.RawMessage(def), nil
	}
	if !json.Valid([]byte(args[i])) {
		return nil, fmt.Errorf("%q is not valid JSON", args[i])
	}
	return json.RawMessage(args[i]), nil
}

// call runs `innkeeper call METHOD PATH [JSON]` and prints the response body.
func call(args []string) error {
	if len(args) < 2 || len(args) > 3 {
		return errors.New(usage)
	}
	c, err := newClient()
	if err != nil {
		return err
	}
	var body []byte
	if len(args) == 3 {
		if body, err = jsonArg(args, 2, ""); err != nil {
			return err
		}
	}
	out, err := c.do(context.Background(), strings.ToUpper(args[0]), args[1], body)
	if err != nil {
		return err
	}
	fmt.Println(strings.TrimSuffix(string(out), "\n"))
	return nil
}

// startJob posts body to path, which starts a job, and says how to follow it.
func startJob(path string, body any) error {
	c, err := newClient()
	if err != nil {
		return err
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	out, err := c.do(context.Background(), "POST", path, raw)
	if err != nil {
		return err
	}
	var j struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(out, &j); err != nil {
		return err
	}
	fmt.Printf("started job %d; follow it with: innkeeper jobs watch %d\n", j.ID, j.ID)
	return nil
}

// importCmd runs `innkeeper import INSTANCE_ID [PARAMS_JSON]`.
func importCmd(args []string) error {
	if len(args) < 1 || len(args) > 2 {
		return errors.New(usage)
	}
	id, err := parseID(args[0])
	if err != nil {
		return err
	}
	params, err := jsonArg(args, 1, `{}`)
	if err != nil {
		return err
	}
	return startJob("/imports", map[string]any{"instance_id": json.Number(id), "params": params})
}

// bulkCmd runs `innkeeper bulk archive|unarchive [QUERY_JSON]`.
func bulkCmd(args []string) error {
	if len(args) < 1 || len(args) > 2 || args[0] != "archive" && args[0] != "unarchive" {
		return errors.New(usage)
	}
	query, err := jsonArg(args, 1, `{}`)
	if err != nil {
		return err
	}
	return startJob("/bulk", map[string]any{"query": query, "action": args[0]})
}

// export runs `innkeeper export CARD_ID [FILE]`.
func export(args []string) error {
	if len(args) < 1 || len(args) > 2 {
		return errors.New(usage)
	}
	id, err := parseID(args[0])
	if err != nil {
		return err
	}
	c, err := newClient()
	if err != nil {
		return err
	}
	out, err := c.do(context.Background(), "GET", "/cards/"+id+"/export", nil)
	if err != nil {
		return err
	}
	if len(args) == 1 {
		_, err = os.Stdout.Write(out)
		return err
	}
	return os.WriteFile(args[1], out, 0o644)
}

// jobsCmd runs `innkeeper jobs [show|cancel|watch ID]`.
func jobsCmd(args []string) error {
	c, err := newClient()
	if err != nil {
		return err
	}
	ctx := context.Background()
	if len(args) == 0 {
		out, err := c.do(ctx, "GET", "/jobs", nil)
		if err != nil {
			return err
		}
		var list []struct {
			ID        int64     `json:"id"`
			Kind      string    `json:"kind"`
			Status    string    `json:"status"`
			CreatedAt time.Time `json:"created_at"`
			Error     *string   `json:"error"`
		}
		if err := json.Unmarshal(out, &list); err != nil {
			return err
		}
		tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tKIND\tSTATUS\tCREATED\tERROR")
		for _, j := range list {
			e := ""
			if j.Error != nil {
				e = *j.Error
			}
			fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\n", j.ID, j.Kind, j.Status, j.CreatedAt.Local().Format(time.DateTime), e)
		}
		return tw.Flush()
	}
	if len(args) != 2 {
		return errors.New(usage)
	}
	id, err := parseID(args[1])
	if err != nil {
		return err
	}
	switch args[0] {
	case "show", "cancel":
		method, path := "GET", "/jobs/"+id
		if args[0] == "cancel" {
			method, path = "POST", path+"/cancel"
		}
		out, err := c.do(ctx, method, path, nil)
		if err != nil {
			return err
		}
		fmt.Println(strings.TrimSuffix(string(out), "\n"))
		return nil
	case "watch":
		return watchJob(ctx, c, id)
	}
	return errors.New(usage)
}

// watchJob prints a line each time the job changes, and fails if the job
// does.
func watchJob(ctx context.Context, c *client, id string) error {
	status := ""
	err := c.stream(ctx, "/jobs/"+id+"/events", func(data []byte) error {
		var j struct {
			Status   string          `json:"status"`
			Progress json.RawMessage `json:"progress"`
			Children map[string]int  `json:"children"`
			Error    *string         `json:"error"`
		}
		if err := json.Unmarshal(data, &j); err != nil {
			return err
		}
		status = j.Status
		line := j.Status + " " + string(j.Progress)
		if len(j.Children) > 0 {
			line += fmt.Sprintf(" children %v", j.Children)
		}
		if j.Error != nil {
			line += " error: " + *j.Error
		}
		fmt.Println(line)
		return nil
	})
	if err != nil {
		return err
	}
	if status == "failed" {
		return errors.New("job failed")
	}
	return nil
}
```

`json.Number(id)` sends the checked ID as a JSON number without converting it.

- [ ] **Step 5: Dispatch them**

In `run` in `cmd/innkeeper/main.go`, add before the closing brace of the `switch`:

```go
	case cmd == "call":
		return call(rest)
	case cmd == "import":
		return importCmd(rest)
	case cmd == "export":
		return export(rest)
	case cmd == "bulk":
		return bulkCmd(rest)
	case cmd == "jobs":
		return jobsCmd(rest)
```

Replace `usage`:

```go
const usage = `usage:
  innkeeper plugin check <plugin-dir>
  innkeeper migrate
  innkeeper worker
  innkeeper api
  innkeeper token create <name> | list | revoke <id>

API commands (read INNKEEPER_URL and INNKEEPER_TOKEN):
  innkeeper call <method> <path> [json]
  innkeeper import <instance-id> [params-json]
  innkeeper export <card-id> [file]
  innkeeper bulk archive|unarchive [query-json]
  innkeeper jobs [show|cancel|watch <job-id>]

See README.md for the environment variables each command reads.`
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./cmd/innkeeper/ && go vet ./cmd/innkeeper/`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add cmd/innkeeper
git commit -m "Add call, import, export, bulk, and jobs CLI commands"
```

---

### Task 18: `innkeeper backup`

**Files:**
- Create: `cmd/innkeeper/backup.go`
- Modify: `cmd/innkeeper/main.go` (dispatch, usage)
- Test: `cmd/innkeeper/backup_test.go`

- [ ] **Step 1: Write the failing test**

Create `cmd/innkeeper/backup_test.go`:

```go
package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCopyBlobs(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	write(t, filepath.Join(src, "ab", "cd", "abcd1"), "one")
	write(t, filepath.Join(src, "tmp", "upload"), "unfinished")
	write(t, filepath.Join(src, "staging", "x"), "unfinished")

	n, err := copyBlobs(src, dst)
	if err != nil || n != 1 {
		t.Fatalf("copyBlobs = %d, %v; want 1", n, err)
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "ab", "cd", "abcd1")); string(b) != "one" {
		t.Fatalf("copied %q", b)
	}
	for _, skipped := range []string{"tmp", "staging"} {
		if _, err := os.Stat(filepath.Join(dst, skipped)); err == nil {
			t.Fatalf("copied %s", skipped)
		}
	}

	write(t, filepath.Join(src, "ef", "01", "ef012"), "two")
	if n, err := copyBlobs(src, dst); err != nil || n != 1 {
		t.Fatalf("second copyBlobs = %d, %v; want only the new blob", n, err)
	}
}

func TestPgDumpKeepsPasswordOffCommandLine(t *testing.T) {
	cmd := pgDumpCommand("postgres://app:s3cret@db:5432/innkeeper?sslmode=disable", "/backups/x.dump")
	if strings.Contains(strings.Join(cmd.Args, " "), "s3cret") {
		t.Fatalf("password in args: %v", cmd.Args)
	}
	if !slices.Contains(cmd.Env, "PGPASSWORD=s3cret") {
		t.Fatal("PGPASSWORD not set")
	}
	if !slices.Contains(cmd.Args, "--dbname=postgres://app@db:5432/innkeeper?sslmode=disable") {
		t.Fatalf("args = %v", cmd.Args)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -run 'TestCopyBlobs|TestPgDump' ./cmd/innkeeper/`
Expected: FAIL to compile: `undefined: copyBlobs`.

- [ ] **Step 3: Write the command**

Create `cmd/innkeeper/backup.go`:

```go
package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// backup runs `innkeeper backup DIR`: a database dump, plus a copy of every
// blob file DIR doesn't have yet. Blob files are named by their hash, so
// later runs copy only new ones.
func backup(args []string) error {
	if len(args) != 1 {
		return errors.New(usage)
	}
	dbURL, blobDir := os.Getenv("INNKEEPER_DATABASE_URL"), os.Getenv("INNKEEPER_BLOB_DIR")
	if dbURL == "" || blobDir == "" {
		return errors.New("INNKEEPER_DATABASE_URL and INNKEEPER_BLOB_DIR must be set")
	}
	dir := args[0]
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	// Dump first: no revision ever drops its avatar, so every blob the dump
	// refers to is still on disk when the copy runs.
	dump := filepath.Join(dir, "innkeeper-"+time.Now().UTC().Format("20060102T150405Z")+".dump")
	if err := pgDumpCommand(dbURL, dump).Run(); err != nil {
		return fmt.Errorf("pg_dump: %w", err)
	}
	n, err := copyBlobs(blobDir, filepath.Join(dir, "blobs"))
	if err != nil {
		return err
	}
	fmt.Printf("wrote %s and copied %d new blob files\n", dump, n)
	return nil
}

// pgDumpCommand runs pg_dump on dbURL. The password goes in the environment,
// since other users can see a process's arguments.
func pgDumpCommand(dbURL, file string) *exec.Cmd {
	env := os.Environ()
	if u, err := url.Parse(dbURL); err == nil && u.User != nil {
		if pw, ok := u.User.Password(); ok {
			env = append(env, "PGPASSWORD="+pw)
			u.User = url.User(u.User.Username())
			dbURL = u.String()
		}
	}
	cmd := exec.Command("pg_dump", "--format=custom", "--file="+file, "--dbname="+dbURL)
	cmd.Env, cmd.Stdout, cmd.Stderr = env, os.Stderr, os.Stderr
	return cmd
}

// copyBlobs copies blob files from src that dst lacks and returns how many
// it copied. It skips tmp and staging, which hold unfinished files.
func copyBlobs(src, dst string) (int, error) {
	n := 0
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if d.IsDir() {
			if rel == "tmp" || rel == "staging" {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, rel)
		if _, err := os.Stat(target); err == nil {
			return nil
		}
		if err := copyFile(path, target); err != nil {
			return err
		}
		n++
		return nil
	})
	return n, err
}

// copyFile writes to a temporary name first, so an interrupted copy never
// looks finished to the next run.
func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	part := dst + ".part"
	out, err := os.Create(part)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(part, dst)
}
```

In `run` in `main.go`, add:

```go
	case cmd == "backup":
		return backup(rest)
```

In `usage`, below the `token` line, add:

```
  innkeeper backup <dir>
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./cmd/innkeeper/`
Expected: PASS.

- [ ] **Step 5: Check it against the smoke database**

Run in the shell from Task 16, Step 6. It needs PostgreSQL 18 client tools on the host (`brew install postgresql@18`).

```bash
go build -o "$tmp/innkeeper" ./cmd/innkeeper
mkdir -p "$tmp/blobs/ab/cd" && printf 'avatar' > "$tmp/blobs/ab/cd/abcd"
"$tmp/innkeeper" backup "$tmp/backup"
"$tmp/innkeeper" backup "$tmp/backup"
```

Expected: the first run prints `wrote <tmp>/backup/innkeeper-<time>.dump and copied 1 new blob files`, the second `copied 0`, with no `pg_dump` error.

- [ ] **Step 6: Commit**

```bash
git add cmd/innkeeper
git commit -m "Add innkeeper backup"
```

---

### Task 19: README and final check

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Document the commands and settings**

In `README.md`, replace everything from `` `innkeeper migrate` applies `` to the end of the file with:

````markdown
## Running

One binary runs both services and the CLI. Both services migrate the
database at startup and stop cleanly on `SIGTERM`.

- `innkeeper api` serves the HTTP API. The OpenAPI spec is at
  `/openapi.json`, and browsable docs at `/docs`.
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
innkeeper bulk archive|unarchive [query-json]   archive the cards a search matches
innkeeper jobs [show|cancel|watch <job-id>]     list or follow jobs
innkeeper token create <name> | list | revoke <id>
innkeeper backup <dir>
```

## Backup and restore

`innkeeper backup <dir>` writes a `pg_dump` file and copies blob files
`<dir>/blobs` doesn't have yet. It needs `pg_dump` on the `PATH`. Run it
on a schedule; old dumps are never deleted.

To restore, stop both services, point `INNKEEPER_DATABASE_URL` at a new,
empty database, then:

```bash
pg_restore --dbname="$INNKEEPER_DATABASE_URL" <dir>/innkeeper-<time>.dump
cp -Rn <dir>/blobs/. "$INNKEEPER_BLOB_DIR"/
```

Restore with the same `INNKEEPER_SECRETS_KEY`, or plugin secrets can't be
read.
````

- [ ] **Step 2: Check the restore commands**

Run in the shell from Task 18:

```bash
docker exec innkeeper-pg createdb -U postgres innkeeper_restore
pg_restore --dbname=postgres://postgres:postgres@localhost:55432/innkeeper_restore "$(ls "$tmp"/backup/innkeeper-*.dump | tail -1)"
psql postgres://postgres:postgres@localhost:55432/innkeeper_restore -Atc 'SELECT count(*) FROM api_token'
mkdir "$tmp/restored" && cp -Rn "$tmp/backup/blobs/." "$tmp/restored"/ && ls "$tmp/restored/ab/cd"
```

Expected: `1` (the `smoke` token), then `abcd`.

- [ ] **Step 3: Run everything**

```bash
gofmt -l . && go vet ./... && go test -race ./...
```

Expected: `gofmt` prints nothing and every package passes.

- [ ] **Step 4: Clean up the smoke databases**

These drop only the two databases this plan created:

```bash
docker exec innkeeper-pg dropdb -U postgres innkeeper_smoke
docker exec innkeeper-pg dropdb -U postgres innkeeper_restore
rm -rf "$tmp"
```

- [ ] **Step 5: Commit**

```bash
git add README.md
git commit -m "Document the API, CLI, first login, and backups"
```
