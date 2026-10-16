# Library Storage Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Store cards and lorebooks in PostgreSQL as append-only revisions, with a content-addressed blob store, duplicate-detection hashes, tags, and collections.

**Architecture:** `internal/db` embeds SQL migrations and applies them under an advisory lock. `internal/cardhash` computes `content_hash`, `text_simhash`, and `avatar_phash`, pinned by test vectors computed in Python so the archive catalog plugin can match them. `internal/blobstore` keeps files at `<root>/ab/cd/<sha256>` and safely ingests files plugins write to `<root>/tmp/`. `internal/library` is the store: one `Entities` implementation serves cards and lorebooks, because their tables have the same shape.

**Tech Stack:** Go 1.27, `github.com/jackc/pgx/v5` v5.11.0, `golang.org/x/image` v0.46.0, PostgreSQL 18 with pgvector.

**Spec:** `docs/superpowers/specs/2026-10-03-innkeeper-core-design.md`, section "Data model": "Library" and "Duplicate detection".

**Kaneo:** INN-2.

---

## Plan series

This is plan 2 of the core. See plan 1 for the full list.

Constraints this plan leaves for later plans:

- **Plan 3:** Imports must split an embedded `character_book` into a lorebook and link it. Card revisions reject `character_book`, in Go and with a `CHECK`. Plugin-written files go through `Library.IngestBlob`. Blob GC is unbuilt: delete unreferenced blobs older than a grace period, plus leftovers in `tmp/` and `staging/`. The worker should call `db.Migrate` at startup.
- **Plan 4:** Map `ErrNotFound` to `404`, `ErrConflict` to `409`, and `ErrSlugTaken` to `409`. Slugs must match `^[a-z0-9]+(-[a-z0-9]+)*$`; generating one from a card name is unbuilt. Restore is an `Update` with an old revision's content. The spec's "Search and enrichment" tables (`tsvector`, `enrichment`, `card_embedding`, and `CREATE EXTENSION vector`) are not in this plan; they belong with `POST /search`. Collection rename and delete and tag listing are unbuilt.
- **Plan 5:** `overrides` keys are plugin instance IDs as JSON strings. Nothing checks them yet, because `plugin_instance` does not exist.
- **Archive catalog plugin:** port the `cardhash` algorithms and check them against the vectors in `cardhash_test.go` and `phash_test.go`. `ContentHash` notes where a Python port can differ.

## File structure

| File | Responsibility |
|---|---|
| `internal/db/migrations/0001_library.sql` | Library schema: blobs, cards, lorebooks, revisions, tags, collections |
| `internal/db/db.go` | Applies embedded migrations once, under an advisory lock |
| `internal/testdb/testdb.go` | A fresh migrated database per test |
| `internal/cardhash/cardhash.go` | `ContentHash`, `Simhash`, `CardSimhash`, `LorebookSimhash`, `Distance` |
| `internal/cardhash/phash.go` | `ImagePhash`, the DCT perceptual hash |
| `internal/blobstore/blobstore.go` | Content-addressed files on disk; `Put` and untrusted-path `Ingest` |
| `internal/library/library.go` | `Library`, errors, error mapping |
| `internal/library/entities.go` | Card and lorebook create, update with conflict check, history, archive |
| `internal/library/blobs.go` | Blob rows with MIME type and `avatar_phash` |
| `internal/library/organize.go` | Tags, collections, card–lorebook links |
| `internal/library/similar.go` | Near-duplicate lookup by Hamming distance |
| `cmd/innkeeper/main.go` | Adds `innkeeper migrate` |

Run the database tests with a local server:

```bash
docker run -d --name innkeeper-pg -e POSTGRES_PASSWORD=postgres -p 55432:5432 pgvector/pgvector:pg18
export INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres
```

---

### Task 1: Schema, migrations, and test databases

**Files:**
- Create: `internal/db/migrations/0001_library.sql`
- Create: `internal/db/db.go`
- Create: `internal/db/db_test.go`
- Create: `internal/testdb/testdb.go`
- Modify: `.github/workflows/ci.yml`

- [ ] **Step 1: Add pgx**

```bash
go get github.com/jackc/pgx/v5@v5.11.0
```

- [ ] **Step 2: Write the failing tests**

Create `internal/db/db_test.go`:

```go
package db_test

import (
	"context"
	"strings"
	"testing"

	"github.com/inkandale/innkeeper/internal/db"
	"github.com/inkandale/innkeeper/internal/testdb"
)

func TestMigrateTwice(t *testing.T) {
	pool := testdb.New(t) // already migrated once
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM schema_migration`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("schema_migration has %d rows, want 1", n)
	}
}

func TestRevisionsAreImmutable(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	var revID int64
	err := pool.QueryRow(ctx, `
		WITH c AS (INSERT INTO card (slug, provenance) VALUES ('a', 'upload') RETURNING id)
		INSERT INTO card_revision (card_id, data, content_hash, source)
		SELECT id, '{}', 'x', 'ui' FROM c RETURNING id`).Scan(&revID)
	if err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{
		`UPDATE card_revision SET message = 'changed' WHERE id = $1`,
		`DELETE FROM card_revision WHERE id = $1`,
	} {
		_, err := pool.Exec(ctx, sql, revID)
		if err == nil || !strings.Contains(err.Error(), "immutable") {
			t.Errorf("%s: got %v, want immutable error", sql, err)
		}
	}
}

func TestCardRejectsEmbeddedLorebook(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	_, err := pool.Exec(ctx, `
		WITH c AS (INSERT INTO card (slug, provenance) VALUES ('a', 'upload') RETURNING id)
		INSERT INTO card_revision (card_id, data, content_hash, source)
		SELECT id, '{"character_book": {}}', 'x', 'ui' FROM c`)
	if err == nil || !strings.Contains(err.Error(), "card_revision_data_check") {
		t.Fatalf("got %v, want card_revision_data_check violation", err)
	}
}
```

Create `internal/testdb/testdb.go`:

```go
// Package testdb gives each test its own migrated PostgreSQL database.
package testdb

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/inkandale/innkeeper/internal/db"
)

// EnvVar names a PostgreSQL URL for a role that can create databases.
const EnvVar = "INNKEEPER_TEST_DATABASE_URL"

// New creates a database, migrates it, and drops it when the test ends. It
// skips the test when EnvVar is unset, except in CI, where it fails.
func New(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv(EnvVar)
	if url == "" {
		if os.Getenv("CI") != "" {
			t.Fatalf("%s is not set", EnvVar)
		}
		t.Skipf("%s is not set", EnvVar)
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("innkeeper_test_%x", rand.Uint64())
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.Database = name
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		if _, err := admin.Exec(ctx, "DROP DATABASE "+name+" WITH (FORCE)"); err != nil {
			t.Errorf("drop %s: %v", name, err)
		}
		admin.Close(ctx)
	})
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	return pool
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/db/`
Expected: build failure, `undefined: db.Migrate`.

- [ ] **Step 4: Write the schema**

Revisions reference their entity with composite foreign keys, so a card's head and a revision's parent must belong to the same card. A trigger makes revision rows immutable.

Create `internal/db/migrations/0001_library.sql`:

```sql
CREATE TABLE blob (
    sha256 text PRIMARY KEY CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    size bigint NOT NULL CHECK (size >= 0),
    mime text NOT NULL,
    store text NOT NULL,
    avatar_phash bigint,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- Revision rows never change. Undo and restore write new revisions.
CREATE FUNCTION forbid_revision_change() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION '% rows are immutable', TG_TABLE_NAME;
END
$$;

CREATE TABLE card (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    slug text NOT NULL UNIQUE CHECK (slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$'),
    -- Null only inside the transaction that creates the card.
    head_revision_id bigint,
    archived_at timestamptz,
    provenance text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE card_revision (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    card_id bigint NOT NULL REFERENCES card,
    parent_id bigint,
    data jsonb NOT NULL CHECK (jsonb_typeof(data) = 'object' AND NOT data ? 'character_book'),
    avatar_blob text REFERENCES blob,
    overrides jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(overrides) = 'object'),
    content_hash text NOT NULL,
    text_simhash bigint,
    message text NOT NULL DEFAULT '',
    source text NOT NULL CHECK (source ~ '^(ui|mcp|adopt|plugin:[a-z][a-z0-9-]*)$'),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (id, card_id),
    FOREIGN KEY (parent_id, card_id) REFERENCES card_revision (id, card_id)
);

ALTER TABLE card ADD FOREIGN KEY (head_revision_id, id) REFERENCES card_revision (id, card_id);
CREATE INDEX ON card_revision (card_id, id);
CREATE INDEX ON card_revision (content_hash);
CREATE TRIGGER card_revision_immutable BEFORE UPDATE OR DELETE ON card_revision
    FOR EACH ROW EXECUTE FUNCTION forbid_revision_change();

CREATE TABLE lorebook (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    slug text NOT NULL UNIQUE CHECK (slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$'),
    head_revision_id bigint,
    archived_at timestamptz,
    provenance text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE lorebook_revision (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    lorebook_id bigint NOT NULL REFERENCES lorebook,
    parent_id bigint,
    data jsonb NOT NULL CHECK (jsonb_typeof(data) = 'object'),
    avatar_blob text REFERENCES blob,
    overrides jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(overrides) = 'object'),
    content_hash text NOT NULL,
    text_simhash bigint,
    message text NOT NULL DEFAULT '',
    source text NOT NULL CHECK (source ~ '^(ui|mcp|adopt|plugin:[a-z][a-z0-9-]*)$'),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (id, lorebook_id),
    FOREIGN KEY (parent_id, lorebook_id) REFERENCES lorebook_revision (id, lorebook_id)
);

ALTER TABLE lorebook ADD FOREIGN KEY (head_revision_id, id) REFERENCES lorebook_revision (id, lorebook_id);
CREATE INDEX ON lorebook_revision (lorebook_id, id);
CREATE INDEX ON lorebook_revision (content_hash);
CREATE TRIGGER lorebook_revision_immutable BEFORE UPDATE OR DELETE ON lorebook_revision
    FOR EACH ROW EXECUTE FUNCTION forbid_revision_change();

CREATE TABLE card_lorebook (
    card_id bigint NOT NULL REFERENCES card,
    lorebook_id bigint NOT NULL REFERENCES lorebook,
    PRIMARY KEY (card_id, lorebook_id)
);
CREATE INDEX ON card_lorebook (lorebook_id);

CREATE TABLE tag (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name text NOT NULL CHECK (name <> '' AND name = btrim(name))
);
CREATE UNIQUE INDEX tag_name_key ON tag (lower(name));

CREATE TABLE card_tag (
    card_id bigint NOT NULL REFERENCES card,
    tag_id bigint NOT NULL REFERENCES tag,
    PRIMARY KEY (card_id, tag_id)
);
CREATE INDEX ON card_tag (tag_id);

CREATE TABLE collection (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name text NOT NULL UNIQUE CHECK (name <> ''),
    description text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE collection_card (
    collection_id bigint NOT NULL REFERENCES collection ON DELETE CASCADE,
    card_id bigint NOT NULL REFERENCES card,
    added_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (collection_id, card_id)
);
CREATE INDEX ON collection_card (card_id);
```

- [ ] **Step 5: Write the migration runner**

Create `internal/db/db.go`:

```go
// Package db holds the core's PostgreSQL schema and applies it.
package db

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"path"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Migrate applies every migration not yet recorded in schema_migration, in
// file name order, in one transaction.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	files, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// The api and worker both migrate at startup and may start together.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('innkeeper-migrate'))`); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migration (
		name text PRIMARY KEY,
		applied_at timestamptz NOT NULL DEFAULT now()
	)`); err != nil {
		return err
	}
	for _, f := range files {
		name := path.Base(f)
		var done bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM schema_migration WHERE name = $1)`, name).Scan(&done); err != nil {
			return err
		}
		if done {
			continue
		}
		sql, err := migrations.ReadFile(f)
		if err != nil {
			return err
		}
		// No arguments, so pgx uses the simple protocol, which allows several statements.
		if _, err := tx.Exec(ctx, string(sql)); err != nil {
			return fmt.Errorf("migration %s: %w", name, err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO schema_migration (name) VALUES ($1)`, name); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go mod tidy && go test ./internal/db/`
Expected: `ok`. Without `INNKEEPER_TEST_DATABASE_URL` the tests are skipped.

- [ ] **Step 7: Run PostgreSQL in CI**

In `.github/workflows/ci.yml`, add to the `test` job, above `steps:`:

```yaml
    services:
      postgres:
        image: pgvector/pgvector:pg18
        env:
          POSTGRES_PASSWORD: postgres
        ports:
          - 5432:5432
        options: >-
          --health-cmd pg_isready
          --health-interval 2s
          --health-timeout 5s
          --health-retries 15
    env:
      INNKEEPER_TEST_DATABASE_URL: postgres://postgres:postgres@localhost:5432/postgres
```

- [ ] **Step 8: Commit**

```bash
git add go.mod go.sum internal/db internal/testdb .github/workflows/ci.yml
git commit -m "Add library schema and migration runner"
```

---

### Task 2: Content hash and simhash

**Files:**
- Create: `internal/cardhash/cardhash_test.go`
- Create: `internal/cardhash/cardhash.go`

The vectors were computed in Python with `json.dumps(v, sort_keys=True, separators=(",", ":"), ensure_ascii=False)`, `hashlib.sha256`, and a hand-written FNV-1a simhash.

- [ ] **Step 1: Write the failing tests**

Create `internal/cardhash/cardhash_test.go`:

```go
package cardhash

import "testing"

// Vectors computed independently in Python, as the archive plugin will.
const (
	card = `{"name": "Brakka", "description": "A grumpy dwarf blacksmith.\r\n",
		"first_mes": "  Hm? <What> do you want & why?  ", "tags": ["dwarf", "smith"],
		"alternate_greetings": [], "creator_notes": "", "extensions": {"depth": 4, "x": null},
		"creation_date": 1700000000}`
	cardContentHash = "90b4d55b00d60bc47f6ae0caad4cdcbe0ded0c6a789d128ef15016e9ad14a1e0"
	cardSimhash     = -6846279246937013872
)

func TestContentHashVector(t *testing.T) {
	got, err := ContentHash([]byte(card))
	if err != nil {
		t.Fatal(err)
	}
	if got != cardContentHash {
		t.Fatalf("got %s, want %s", got, cardContentHash)
	}
}

func TestContentHashIgnoresFormatting(t *testing.T) {
	a, _ := ContentHash([]byte(`{"name": "A", "description": "x", "modification_date": 1}`))
	b, _ := ContentHash([]byte(`{"description":" x\n","personality":"","name":"A"}`))
	if a != b {
		t.Fatalf("hashes differ: %s, %s", a, b)
	}
	c, _ := ContentHash([]byte(`{"name": "A", "description": "y"}`))
	if a == c {
		t.Fatal("different descriptions hash the same")
	}
}

func TestContentHashRejectsNonObject(t *testing.T) {
	if _, err := ContentHash([]byte(`[1]`)); err == nil {
		t.Fatal("accepted an array")
	}
}

func TestSimhashVectors(t *testing.T) {
	got, ok, err := CardSimhash([]byte(card))
	if err != nil || !ok {
		t.Fatalf("CardSimhash: %v, ok=%v", err, ok)
	}
	if got != cardSimhash {
		t.Errorf("CardSimhash = %d, want %d", got, int64(cardSimhash))
	}
	if got, _ := Simhash("Hello, World"); got != 8618312879776256743 {
		t.Errorf("Simhash(short) = %d", got)
	}
	if _, ok := Simhash(" ... "); ok {
		t.Error("Simhash of no words reported ok")
	}
}

func TestSimhashNearDuplicates(t *testing.T) {
	base := "Brakka is a grumpy dwarf blacksmith who runs the forge at the edge of town. " +
		"She hates elves, loves good ale, and never turns away a customer with honest coin."
	a, _ := Simhash(base)
	b, _ := Simhash(base + " She has a cat.")
	c, _ := Simhash("A cheerful elven bard wanders the coast singing songs of the sea and the stars above.")
	if d := Distance(a, b); d > 10 {
		t.Errorf("near-duplicate distance %d, want <= 10", d)
	}
	if d := Distance(a, c); d < 16 {
		t.Errorf("unrelated distance %d, want >= 16", d)
	}
}

func TestLorebookSimhash(t *testing.T) {
	a, ok, err := LorebookSimhash([]byte(`{"entries": [{"content": "Hello"}, {"content": "World"}]}`))
	if err != nil || !ok {
		t.Fatalf("%v, ok=%v", err, ok)
	}
	if want, _ := Simhash("Hello\nWorld"); a != want {
		t.Fatalf("got %d, want %d", a, want)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/cardhash/`
Expected: build failure, `undefined: ContentHash`.

- [ ] **Step 3: Write the implementation**

Create `internal/cardhash/cardhash.go`:

```go
// Package cardhash computes the duplicate-detection fingerprints for cards
// and lorebooks. The archive catalog plugin must compute the same values, so
// the test vectors in this package pin each algorithm.
package cardhash

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash/fnv"
	"math/bits"
	"strings"
	"unicode"
)

// Fields that change on every save without changing the card.
var volatile = []string{"creation_date", "modification_date"}

// ContentHash returns the hex SHA-256 of the card or lorebook data in
// canonical form:
//
//   - top-level creation_date and modification_date removed
//   - every string with CRLF replaced by LF and surrounding whitespace trimmed
//   - object members that are then null, "", [], or {} removed
//   - compact JSON with object keys sorted and no HTML escaping, which matches
//     Python's json.dumps(v, sort_keys=True, separators=(",", ":"), ensure_ascii=False)
//
// ponytail: numbers keep their original text and Go escapes U+2028/U+2029,
// so a Python port can differ on those; normalize numbers if that matters.
func ContentHash(data []byte) (string, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return "", err
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return "", errors.New("data must be a JSON object")
	}
	for _, k := range volatile {
		delete(obj, k)
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(normalize(obj)); err != nil {
		return "", err
	}
	sum := sha256.Sum256(bytes.TrimSuffix(buf.Bytes(), []byte("\n")))
	return hex.EncodeToString(sum[:]), nil
}

func normalize(v any) any {
	switch v := v.(type) {
	case string:
		return strings.TrimSpace(strings.ReplaceAll(v, "\r\n", "\n"))
	case []any:
		for i := range v {
			v[i] = normalize(v[i])
		}
	case map[string]any:
		for k, x := range v {
			if x = normalize(x); isEmpty(x) {
				delete(v, k)
			} else {
				v[k] = x
			}
		}
	}
	return v
}

func isEmpty(v any) bool {
	switch v := v.(type) {
	case nil:
		return true
	case string:
		return v == ""
	case []any:
		return len(v) == 0
	case map[string]any:
		return len(v) == 0
	}
	return false
}

// CardSimhash returns the Simhash of a card's description, personality,
// scenario, and first_mes, joined with newlines. ok is false when they hold no
// words.
func CardSimhash(data []byte) (hash int64, ok bool, err error) {
	var c struct {
		Description string `json:"description"`
		Personality string `json:"personality"`
		Scenario    string `json:"scenario"`
		FirstMes    string `json:"first_mes"`
	}
	if err := json.Unmarshal(data, &c); err != nil {
		return 0, false, err
	}
	hash, ok = Simhash(strings.Join([]string{c.Description, c.Personality, c.Scenario, c.FirstMes}, "\n"))
	return hash, ok, nil
}

// LorebookSimhash returns the Simhash of a lorebook's entry contents, joined
// with newlines in entry order.
func LorebookSimhash(data []byte) (hash int64, ok bool, err error) {
	var b struct {
		Entries []struct {
			Content string `json:"content"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(data, &b); err != nil {
		return 0, false, err
	}
	parts := make([]string, len(b.Entries))
	for i, e := range b.Entries {
		parts[i] = e.Content
	}
	hash, ok = Simhash(strings.Join(parts, "\n"))
	return hash, ok, nil
}

// Simhash returns a 64-bit Charikar simhash of text. Words are lowercased runs
// of letters and digits. Each run of 3 consecutive words (or all the words, if
// there are fewer) is hashed with FNV-1a 64 over the words joined by one
// space. Bit b of the result is set when more shingles have bit b set than
// clear. ok is false when text has no words.
func Simhash(text string) (hash int64, ok bool) {
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
	if len(words) == 0 {
		return 0, false
	}
	n := min(3, len(words))
	var counts [64]int
	for i := 0; i+n <= len(words); i++ {
		h := fnv.New64a()
		h.Write([]byte(strings.Join(words[i:i+n], " ")))
		x := h.Sum64()
		for b := range counts {
			if x>>b&1 == 1 {
				counts[b]++
			} else {
				counts[b]--
			}
		}
	}
	var out uint64
	for b, c := range counts {
		if c > 0 {
			out |= 1 << b
		}
	}
	return int64(out), true
}

// Distance is the Hamming distance between two 64-bit hashes.
func Distance(a, b int64) int {
	return bits.OnesCount64(uint64(a ^ b))
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/cardhash/`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/cardhash/cardhash.go internal/cardhash/cardhash_test.go
git commit -m "Add content hash and simhash"
```

---

### Task 3: Avatar perceptual hash

**Files:**
- Create: `internal/cardhash/phash_test.go`
- Create: `internal/cardhash/testdata/avatar.png`
- Create: `internal/cardhash/phash.go`

- [ ] **Step 1: Add x/image**

```bash
go get golang.org/x/image@v0.46.0
```

- [ ] **Step 2: Write the failing tests**

Create `internal/cardhash/phash_test.go`:

```go
package cardhash

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// scene draws a gradient with a disk at any size, so the same picture can be
// rendered at two resolutions. invert swaps light and dark.
func scene(t *testing.T, w, h int, invert bool) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			fx, fy := float64(x)/float64(w), float64(y)/float64(h)
			v := uint8(255 * fx * (1 - fy))
			if dx, dy := fx-0.6, fy-0.4; dx*dx+dy*dy < 0.04 {
				v = 255 - v/2
			}
			if invert {
				v = 255 - v
			}
			img.Set(x, y, color.RGBA{v, v / 2, 255 - v, 255})
		}
	}
	path := filepath.Join(t.TempDir(), "scene.png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	return path
}

func phash(t *testing.T, path string) int64 {
	t.Helper()
	h, err := ImagePhash(path)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// imagehash.phash(Image.open("testdata/avatar.png")) in Python, as hex.
var avatarPhashPython = int64(-0x6767989867639365) // 98986767989c6c9b

func TestImagePhashVector(t *testing.T) {
	got := phash(t, filepath.Join("testdata", "avatar.png"))
	if d := Distance(got, avatarPhashPython); d > 6 {
		t.Fatalf("got %016x, %d bits from imagehash's %016x", uint64(got), d, uint64(avatarPhashPython))
	}
}

func TestImagePhashSimilarity(t *testing.T) {
	big := phash(t, scene(t, 400, 500, false))
	small := phash(t, scene(t, 100, 125, false))
	inverted := phash(t, scene(t, 400, 500, true))
	t.Logf("resized %d bits, inverted %d bits", Distance(big, small), Distance(big, inverted))
	if d := Distance(big, small); d > 6 {
		t.Errorf("resized copy is %d bits away, want <= 6", d)
	}
	if d := Distance(big, inverted); d < 20 {
		t.Errorf("inverted image is %d bits away, want >= 20", d)
	}
}

func TestImagePhashRejectsNonImage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.txt")
	os.WriteFile(path, []byte("not an image"), 0o644)
	if _, err := ImagePhash(path); err == nil {
		t.Fatal("hashed a text file")
	}
}
```

`testdata/avatar.png` is `scene(t, 256, 320, false)` saved to disk. Its expected hash comes from Python:

```bash
python3 -m venv venv && venv/bin/pip install imagehash pillow
venv/bin/python -c 'import imagehash; from PIL import Image; print(imagehash.phash(Image.open("internal/cardhash/testdata/avatar.png")))'
```

Expected: `98986767989c6c9b`.

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/cardhash/`
Expected: build failure, `undefined: ImagePhash`.

- [ ] **Step 4: Write the implementation**

Create `internal/cardhash/phash.go`:

```go
package cardhash

import (
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"os"
	"slices"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

// Images are untrusted; refuse ones whose decoded size could exhaust memory.
const maxPixels = 64 << 20

// ImagePhash returns the 64-bit DCT perceptual hash of the image at path. It
// follows imagehash.phash from Python: grayscale, resize to 32×32, 2D DCT-II,
// keep the top-left 8×8 block, and set a bit (row-major, first is the high
// bit) for each value above the block's median.
//
// ponytail: resampling differs from PIL's, so on some images a Python port
// lands a few bits away; the Hamming threshold absorbs it.
func ImagePhash(path string) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return 0, err
	}
	if cfg.Width*cfg.Height > maxPixels {
		return 0, fmt.Errorf("image is %d×%d, over the %d pixel limit", cfg.Width, cfg.Height, maxPixels)
	}
	if _, err := f.Seek(0, 0); err != nil {
		return 0, err
	}
	img, _, err := image.Decode(f)
	if err != nil {
		return 0, err
	}
	const n = 32
	gray := image.NewGray(image.Rect(0, 0, n, n))
	draw.CatmullRom.Scale(gray, gray.Bounds(), img, img.Bounds(), draw.Src, nil)

	var cos [8][n]float64
	for k := range 8 {
		for i := range n {
			cos[k][i] = math.Cos(math.Pi * float64(k) * float64(2*i+1) / (2 * n))
		}
	}
	var low [64]float64
	for u := range 8 {
		for v := range 8 {
			var s float64
			for y := range n {
				for x := range n {
					s += float64(gray.GrayAt(x, y).Y) * cos[u][y] * cos[v][x]
				}
			}
			low[u*8+v] = s
		}
	}
	sorted := slices.Clone(low[:])
	slices.Sort(sorted)
	median := (sorted[31] + sorted[32]) / 2
	var out uint64
	for _, s := range low {
		out <<= 1
		if s > median {
			out |= 1
		}
	}
	return int64(out), nil
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test -v -run Phash ./internal/cardhash/`
Expected: `ok`, with the resized copy 0 bits away and the inverted image 62 bits away.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/cardhash
git commit -m "Add avatar perceptual hash"
```

---

### Task 4: Blob store

**Files:**
- Create: `internal/blobstore/blobstore_test.go`
- Create: `internal/blobstore/blobstore.go`

`Ingest` takes a path from a plugin, so it accepts only a regular file directly inside `tmp/`, with no symlinks or extra hard links, and hashes the bytes itself.

- [ ] **Step 1: Write the failing tests**

Create `internal/blobstore/blobstore_test.go`:

```go
package blobstore

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	s := &Store{Name: "library", Root: t.TempDir()}
	if err := os.MkdirAll(s.TmpDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	return s
}

func sum(b string) string {
	h := sha256.Sum256([]byte(b))
	return hex.EncodeToString(h[:])
}

func TestPut(t *testing.T) {
	s := newStore(t)
	sha, size, err := s.Put(strings.NewReader("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if sha != sum("hello") || size != 5 {
		t.Fatalf("got %s %d", sha, size)
	}
	path, _ := s.Path(sha)
	if path != filepath.Join(s.Root, sha[:2], sha[2:4], sha) {
		t.Fatalf("unexpected path %s", path)
	}
	if b, err := os.ReadFile(path); err != nil || string(b) != "hello" {
		t.Fatalf("read back %q, %v", b, err)
	}
	// Same bytes again dedupe to the same file.
	if again, _, err := s.Put(strings.NewReader("hello")); err != nil || again != sha {
		t.Fatalf("second put: %s, %v", again, err)
	}
	if left, _ := os.ReadDir(s.stagingDir()); len(left) != 0 {
		t.Fatalf("staging not empty: %v", left)
	}
}

func TestIngest(t *testing.T) {
	s := newStore(t)
	src := filepath.Join(s.TmpDir(), "card.png")
	os.WriteFile(src, []byte("png bytes"), 0o644)
	sha, size, err := s.Ingest(src)
	if err != nil {
		t.Fatal(err)
	}
	if sha != sum("png bytes") || size != 9 {
		t.Fatalf("got %s %d", sha, size)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatal("source file still in tmp")
	}
}

func TestIngestRejectsUntrustedPaths(t *testing.T) {
	s := newStore(t)
	outside := filepath.Join(t.TempDir(), "secret")
	os.WriteFile(outside, []byte("secret"), 0o600)

	nested := filepath.Join(s.TmpDir(), "sub")
	os.Mkdir(nested, 0o755)
	os.WriteFile(filepath.Join(nested, "f"), []byte("x"), 0o644)

	symlink := filepath.Join(s.TmpDir(), "link")
	os.Symlink(outside, symlink)

	hardlink := filepath.Join(s.TmpDir(), "hard")
	os.Link(outside, hardlink)

	for name, path := range map[string]string{
		"outside tmp": outside,
		"dot-dot":     filepath.Join(s.TmpDir(), "..", "..", filepath.Base(filepath.Dir(outside)), "secret"),
		"nested":      filepath.Join(nested, "f"),
		"symlink":     symlink,
		"hard link":   hardlink,
		"directory":   nested,
	} {
		if _, _, err := s.Ingest(path); err == nil {
			t.Errorf("%s: Ingest accepted %s", name, path)
		}
	}
	if b, err := os.ReadFile(outside); err != nil || string(b) != "secret" {
		t.Fatalf("outside file disturbed: %q, %v", b, err)
	}
	if fi, _ := os.Stat(outside); fi.Mode().Perm() != 0o600 {
		t.Fatalf("outside file mode changed to %v", fi.Mode().Perm())
	}
}

func TestPathRejectsBadHash(t *testing.T) {
	s := newStore(t)
	for _, sha := range []string{"", "../../etc/passwd", strings.Repeat("A", 64)} {
		if _, err := s.Path(sha); err == nil {
			t.Errorf("Path(%q) accepted", sha)
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/blobstore/`
Expected: build failure, `undefined: Store`.

- [ ] **Step 3: Write the implementation**

Create `internal/blobstore/blobstore.go`:

```go
// Package blobstore keeps files on disk by SHA-256, laid out as
// <root>/ab/cd/<sha256>. Plugins write new files to <root>/tmp/.
package blobstore

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
)

var shaPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Store is one blob directory. Name is recorded on each blob row.
type Store struct {
	Name string
	Root string
}

// TmpDir is where plugins write files for Ingest.
func (s *Store) TmpDir() string { return filepath.Join(s.Root, "tmp") }

func (s *Store) stagingDir() string { return filepath.Join(s.Root, "staging") }

// Path returns where the blob with this hash lives.
func (s *Store) Path(sha string) (string, error) {
	if !shaPattern.MatchString(sha) {
		return "", fmt.Errorf("invalid blob hash %q", sha)
	}
	return filepath.Join(s.Root, sha[0:2], sha[2:4], sha), nil
}

// Put copies r into the store and returns the blob's hash and size.
func (s *Store) Put(r io.Reader) (sha string, size int64, err error) {
	f, err := s.stage()
	if err != nil {
		return "", 0, err
	}
	defer os.Remove(f.Name())
	h := sha256.New()
	size, err = io.Copy(io.MultiWriter(f, h), r)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", 0, err
	}
	sha = hex.EncodeToString(h.Sum(nil))
	return sha, size, s.place(f.Name(), sha)
}

// Ingest moves a file a plugin wrote into the store. path is untrusted: it
// must name a regular file directly inside TmpDir, and the hash is computed
// here, never taken from the plugin.
//
// ponytail: a plugin still holding the file open could change it after it is
// hashed. Plugins run as the same OS user and could write the store directly
// anyway; copy instead of moving if plugins ever get their own user.
func (s *Store) Ingest(path string) (sha string, size int64, err error) {
	tmp, err := filepath.EvalSymlinks(s.TmpDir())
	if err != nil {
		return "", 0, err
	}
	dir, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return "", 0, err
	}
	if dir != tmp {
		return "", 0, fmt.Errorf("%s is not inside %s", path, s.TmpDir())
	}
	// Move it out of the plugin-writable folder before reading it.
	staged, err := s.stage()
	if err != nil {
		return "", 0, err
	}
	staged.Close()
	defer os.Remove(staged.Name())
	if err := os.Rename(filepath.Join(dir, filepath.Base(path)), staged.Name()); err != nil {
		return "", 0, err
	}
	f, err := os.OpenFile(staged.Name(), os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return "", 0, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	if fi, err := f.Stat(); err != nil {
		return "", 0, err
	} else if !fi.Mode().IsRegular() {
		return "", 0, fmt.Errorf("%s is not a regular file", path)
	} else if st, ok := fi.Sys().(*syscall.Stat_t); !ok || uint64(st.Nlink) != 1 {
		// A hard link would let place chmod some other file.
		return "", 0, fmt.Errorf("%s has other hard links", path)
	}
	h := sha256.New()
	if size, err = io.Copy(h, f); err != nil {
		return "", 0, err
	}
	sha = hex.EncodeToString(h.Sum(nil))
	return sha, size, s.place(staged.Name(), sha)
}

func (s *Store) stage() (*os.File, error) {
	if err := os.MkdirAll(s.stagingDir(), 0o755); err != nil {
		return nil, err
	}
	return os.CreateTemp(s.stagingDir(), "blob-*")
}

// place renames a staged file to its final path. If the blob already exists
// the rename replaces it with identical bytes, so concurrent puts are safe.
func (s *Store) place(staged, sha string) error {
	dst, err := s.Path(sha)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.Chmod(staged, 0o444); err != nil {
		return err
	}
	return os.Rename(staged, dst)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/blobstore/`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/blobstore
git commit -m "Add content-addressed blob store"
```

---

### Task 5: Card and lorebook revisions

**Files:**
- Create: `internal/library/library_test.go`
- Create: `internal/library/library.go`
- Create: `internal/library/entities.go`

Every write names the revision it was based on. `Update` locks the entity row, and returns `ErrConflict` if the head has moved.

- [ ] **Step 1: Write the failing tests**

Create `internal/library/library_test.go`:

```go
package library

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/inkandale/innkeeper/internal/blobstore"
	"github.com/inkandale/innkeeper/internal/testdb"
)

func newLibrary(t *testing.T) *Library {
	t.Helper()
	return New(testdb.New(t), &blobstore.Store{Name: "library", Root: t.TempDir()})
}

func edit(data string) Edit {
	return Edit{Data: json.RawMessage(data), Source: "ui"}
}

// newCard creates a card with the given data and returns its ID and head revision ID.
func newCard(t *testing.T, lib *Library, slug, data string) (int64, int64) {
	t.Helper()
	c, rev, err := lib.Cards.Create(context.Background(), slug, "upload", edit(data))
	if err != nil {
		t.Fatal(err)
	}
	return c.ID, rev.ID
}

func TestCreateAndUpdateCard(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	c, first, err := lib.Cards.Create(ctx, "brakka", "upload", edit(`{"name": "Brakka", "description": "A dwarf."}`))
	if err != nil {
		t.Fatal(err)
	}
	if c.HeadRevisionID != first.ID || first.ParentID != nil || first.TextSimhash == nil || first.ContentHash == "" {
		t.Fatalf("unexpected create result: %+v %+v", c, first)
	}
	if string(first.Overrides) != "{}" {
		t.Fatalf("overrides = %s, want {}", first.Overrides)
	}

	second, err := lib.Cards.Update(ctx, c.ID, first.ID, Edit{
		Data:      json.RawMessage(`{"name": "Brakka", "description": "A grumpy dwarf."}`),
		Overrides: json.RawMessage(`{"7": {"name": "Brakka (ST)"}}`),
		Message:   "grumpier", Source: "mcp",
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.ParentID == nil || *second.ParentID != first.ID || second.ContentHash == first.ContentHash {
		t.Fatalf("unexpected second revision: %+v", second)
	}

	got, err := lib.Cards.GetBySlug(ctx, "brakka")
	if err != nil || got.HeadRevisionID != second.ID {
		t.Fatalf("head = %+v, %v; want revision %d", got, err, second.ID)
	}
	hist, err := lib.Cards.History(ctx, c.ID)
	if err != nil || len(hist) != 2 || hist[0].ID != second.ID || hist[1].ID != first.ID {
		t.Fatalf("history = %+v, %v", hist, err)
	}
	if hist[0].Message != "grumpier" || hist[0].Source != "mcp" {
		t.Fatalf("history[0] = %+v", hist[0])
	}
}

func TestUpdateFromStaleBaseConflicts(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	id, first := newCard(t, lib, "a", `{"name": "A"}`)
	if _, err := lib.Cards.Update(ctx, id, first, edit(`{"name": "B"}`)); err != nil {
		t.Fatal(err)
	}
	_, err := lib.Cards.Update(ctx, id, first, edit(`{"name": "C"}`))
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("got %v, want ErrConflict", err)
	}
	if _, err := lib.Cards.Update(ctx, 999, first, edit(`{"name": "C"}`)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing card: got %v, want ErrNotFound", err)
	}
}

func TestCreateValidation(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	newCard(t, lib, "taken", `{"name": "A"}`)
	cases := map[string]struct {
		slug string
		e    Edit
		want error
	}{
		"slug taken":     {"taken", edit(`{"name": "B"}`), ErrSlugTaken},
		"bad slug":       {"Not A Slug", edit(`{}`), nil},
		"array data":     {"x", edit(`[]`), nil},
		"embedded book":  {"x", edit(`{"character_book": {}}`), nil},
		"bad source":     {"x", Edit{Data: json.RawMessage(`{}`), Source: "cli"}, nil},
		"missing avatar": {"x", Edit{Data: json.RawMessage(`{}`), Source: "ui", AvatarBlob: "00"}, ErrNotFound},
	}
	for name, c := range cases {
		_, _, err := lib.Cards.Create(ctx, c.slug, "upload", c.e)
		if err == nil || (c.want != nil && !errors.Is(err, c.want)) {
			t.Errorf("%s: got %v, want error %v", name, err, c.want)
		}
	}
}

func TestSetArchived(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	id, _ := newCard(t, lib, "a", `{"name": "A"}`)
	if err := lib.Cards.SetArchived(ctx, id, true); err != nil {
		t.Fatal(err)
	}
	if c, _ := lib.Cards.Get(ctx, id); c.ArchivedAt == nil {
		t.Fatal("not archived")
	}
	if err := lib.Cards.SetArchived(ctx, id, false); err != nil {
		t.Fatal(err)
	}
	if c, _ := lib.Cards.Get(ctx, id); c.ArchivedAt != nil {
		t.Fatal("still archived")
	}
	if err := lib.Cards.SetArchived(ctx, 999, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

func TestLorebooks(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	b, rev, err := lib.Lorebooks.Create(ctx, "dwarf-lore", "upload",
		edit(`{"name": "Dwarf lore", "entries": [{"keys": ["forge"], "content": "The forge never cools."}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if rev.TextSimhash == nil {
		t.Fatal("lorebook has no simhash")
	}
	if _, err := lib.Lorebooks.Update(ctx, b.ID, rev.ID, edit(`{"name": "Dwarf lore", "entries": []}`)); err != nil {
		t.Fatal(err)
	}
	// Cards and lorebooks have separate slug namespaces.
	if _, _, err := lib.Cards.Create(ctx, "dwarf-lore", "upload", edit(`{"name": "x"}`)); err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/library/`
Expected: build failure, `undefined: Library`.

- [ ] **Step 3: Write the implementation**

Create `internal/library/library.go`:

```go
// Package library stores cards and lorebooks as append-only revisions, along
// with blobs, tags, collections, and duplicate lookups.
package library

import (
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/inkandale/innkeeper/internal/blobstore"
	"github.com/inkandale/innkeeper/internal/cardhash"
)

var (
	ErrNotFound = errors.New("not found")
	// ErrConflict means the write's base revision is no longer the head.
	ErrConflict  = errors.New("base revision is not the head")
	ErrSlugTaken = errors.New("slug already in use")
)

// Library is the core's card and lorebook store.
type Library struct {
	Cards     *Entities
	Lorebooks *Entities

	pool  *pgxpool.Pool
	blobs *blobstore.Store
}

// New returns a Library that writes new blobs to blobs.
func New(pool *pgxpool.Pool, blobs *blobstore.Store) *Library {
	return &Library{
		Cards: &Entities{pool: pool, k: kind{
			table: "card", revTable: "card_revision", fk: "card_id",
			simhash: cardhash.CardSimhash,
			forbid:  []string{"character_book"},
		}},
		Lorebooks: &Entities{pool: pool, k: kind{
			table: "lorebook", revTable: "lorebook_revision", fk: "lorebook_id",
			simhash: cardhash.LorebookSimhash,
		}},
		pool:  pool,
		blobs: blobs,
	}
}

// mapErr turns a missing row or a foreign key violation into ErrNotFound.
func mapErr(err error) error {
	var pgErr *pgconn.PgError
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return ErrNotFound
	case errors.As(err, &pgErr) && pgErr.Code == "23503":
		return errors.Join(ErrNotFound, err)
	}
	return err
}
```

Create `internal/library/entities.go`:

```go
package library

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/inkandale/innkeeper/internal/cardhash"
)

// kind holds what differs between cards and lorebooks. Their tables have the
// same shape, so one implementation serves both.
type kind struct {
	table, revTable, fk string
	simhash             func([]byte) (int64, bool, error)
	// Top-level keys data may not have.
	forbid []string
}

// Entities stores one kind of versioned entity: cards or lorebooks.
type Entities struct {
	pool *pgxpool.Pool
	k    kind
}

// Entity is a card or lorebook's current state.
type Entity struct {
	ID             int64
	Slug           string
	HeadRevisionID int64
	ArchivedAt     *time.Time
	Provenance     string
	CreatedAt      time.Time
}

// Revision is one immutable version of a card or lorebook.
type Revision struct {
	ID          int64
	EntityID    int64
	ParentID    *int64
	Data        json.RawMessage
	AvatarBlob  *string
	Overrides   json.RawMessage
	ContentHash string
	TextSimhash *int64
	Message     string
	Source      string
	CreatedAt   time.Time
}

// Edit is the content of a new revision.
type Edit struct {
	// Data is the CCv3 data object: a card's without character_book, or a
	// character_book itself.
	Data json.RawMessage
	// AvatarBlob is a blob's sha256, or empty for none.
	AvatarBlob string
	// Overrides maps plugin instance IDs to JSON merge patches. Nil means none.
	Overrides json.RawMessage
	Message   string
	// Source is ui, mcp, adopt, or plugin:<name>.
	Source string
}

const entityCols = `id, slug, coalesce(head_revision_id, 0), archived_at, provenance, created_at`

func scanEntity(row pgx.Row) (Entity, error) {
	var e Entity
	err := row.Scan(&e.ID, &e.Slug, &e.HeadRevisionID, &e.ArchivedAt, &e.Provenance, &e.CreatedAt)
	return e, mapErr(err)
}

func (s *Entities) revCols() string {
	return `id, ` + s.k.fk + `, parent_id, data, avatar_blob, overrides, content_hash, text_simhash, message, source, created_at`
}

func scanRevision(row pgx.Row) (Revision, error) {
	var r Revision
	err := row.Scan(&r.ID, &r.EntityID, &r.ParentID, &r.Data, &r.AvatarBlob, &r.Overrides,
		&r.ContentHash, &r.TextSimhash, &r.Message, &r.Source, &r.CreatedAt)
	return r, mapErr(err)
}

// revisionRow is an Edit checked and hashed, ready to insert.
type revisionRow struct {
	data, overrides []byte
	avatar          *string
	contentHash     string
	simhash         *int64
	message, source string
}

func (s *Entities) prepare(e Edit) (revisionRow, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(e.Data, &top); err != nil || top == nil {
		return revisionRow{}, errors.New("data must be a JSON object")
	}
	for _, k := range s.k.forbid {
		if _, ok := top[k]; ok {
			return revisionRow{}, fmt.Errorf("%s data may not contain %s", s.k.table, k)
		}
	}
	r := revisionRow{data: e.Data, overrides: e.Overrides, message: e.Message, source: e.Source}
	if r.overrides == nil {
		r.overrides = []byte(`{}`)
	}
	if e.AvatarBlob != "" {
		r.avatar = &e.AvatarBlob
	}
	var err error
	if r.contentHash, err = cardhash.ContentHash(e.Data); err != nil {
		return revisionRow{}, err
	}
	h, ok, err := s.k.simhash(e.Data)
	if err != nil {
		return revisionRow{}, fmt.Errorf("simhash: %w", err)
	}
	if ok {
		r.simhash = &h
	}
	return r, nil
}

func (s *Entities) insertRevision(ctx context.Context, tx pgx.Tx, id int64, parent *int64, r revisionRow) (Revision, error) {
	rev, err := scanRevision(tx.QueryRow(ctx, `INSERT INTO `+s.k.revTable+` (`+s.k.fk+`, parent_id, data, avatar_blob,
		overrides, content_hash, text_simhash, message, source)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING `+s.revCols(),
		id, parent, r.data, r.avatar, r.overrides, r.contentHash, r.simhash, r.message, r.source))
	if err != nil {
		return Revision{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE `+s.k.table+` SET head_revision_id = $1 WHERE id = $2`, rev.ID, id); err != nil {
		return Revision{}, err
	}
	return rev, nil
}

// Create adds a new entity with e as its first revision.
func (s *Entities) Create(ctx context.Context, slug, provenance string, e Edit) (Entity, Revision, error) {
	r, err := s.prepare(e)
	if err != nil {
		return Entity{}, Revision{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Entity{}, Revision{}, err
	}
	defer tx.Rollback(ctx)
	ent, err := scanEntity(tx.QueryRow(ctx, `INSERT INTO `+s.k.table+` (slug, provenance) VALUES ($1, $2) RETURNING `+entityCols,
		slug, provenance))
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return Entity{}, Revision{}, ErrSlugTaken
	}
	if err != nil {
		return Entity{}, Revision{}, err
	}
	rev, err := s.insertRevision(ctx, tx, ent.ID, nil, r)
	if err != nil {
		return Entity{}, Revision{}, err
	}
	ent.HeadRevisionID = rev.ID
	return ent, rev, tx.Commit(ctx)
}

// Update adds a revision on top of base. It returns ErrConflict if base is no
// longer the head.
func (s *Entities) Update(ctx context.Context, id, base int64, e Edit) (Revision, error) {
	r, err := s.prepare(e)
	if err != nil {
		return Revision{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Revision{}, err
	}
	defer tx.Rollback(ctx)
	var head int64
	if err := tx.QueryRow(ctx, `SELECT head_revision_id FROM `+s.k.table+` WHERE id = $1 FOR UPDATE`, id).Scan(&head); err != nil {
		return Revision{}, mapErr(err)
	}
	if head != base {
		return Revision{}, ErrConflict
	}
	rev, err := s.insertRevision(ctx, tx, id, &base, r)
	if err != nil {
		return Revision{}, err
	}
	return rev, tx.Commit(ctx)
}

// Get returns the entity with this ID.
func (s *Entities) Get(ctx context.Context, id int64) (Entity, error) {
	return scanEntity(s.pool.QueryRow(ctx, `SELECT `+entityCols+` FROM `+s.k.table+` WHERE id = $1`, id))
}

// GetBySlug returns the entity with this slug.
func (s *Entities) GetBySlug(ctx context.Context, slug string) (Entity, error) {
	return scanEntity(s.pool.QueryRow(ctx, `SELECT `+entityCols+` FROM `+s.k.table+` WHERE slug = $1`, slug))
}

// Revision returns one revision by its ID.
func (s *Entities) Revision(ctx context.Context, revID int64) (Revision, error) {
	return scanRevision(s.pool.QueryRow(ctx, `SELECT `+s.revCols()+` FROM `+s.k.revTable+` WHERE id = $1`, revID))
}

// History returns every revision of an entity, newest first.
func (s *Entities) History(ctx context.Context, id int64) ([]Revision, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+s.revCols()+` FROM `+s.k.revTable+` WHERE `+s.k.fk+` = $1 ORDER BY id DESC`, id)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Revision, error) { return scanRevision(row) })
}

// SetArchived archives or unarchives an entity.
func (s *Entities) SetArchived(ctx context.Context, id int64, archived bool) error {
	tag, err := s.pool.Exec(ctx, `UPDATE `+s.k.table+`
		SET archived_at = CASE WHEN $2 THEN coalesce(archived_at, now()) END WHERE id = $1`, id, archived)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
```

`library.go` references `blobs` on `Library`; it is used in Task 6.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/library/`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/library
git commit -m "Add card and lorebook revisions"
```

---

### Task 6: Blobs in the library

**Files:**
- Create: `internal/library/blobs_test.go`
- Create: `internal/library/blobs.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/library/blobs_test.go`:

```go
package library

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPutBlob(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	png, err := os.Open(filepath.Join("..", "cardhash", "testdata", "avatar.png"))
	if err != nil {
		t.Fatal(err)
	}
	defer png.Close()
	img, err := lib.PutBlob(ctx, png)
	if err != nil {
		t.Fatal(err)
	}
	if img.MIME != "image/png" || img.AvatarPhash == nil || img.Store != "library" {
		t.Fatalf("image blob = %+v", img)
	}

	txt, err := lib.PutBlob(ctx, strings.NewReader("plain text"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(txt.MIME, "text/plain") || txt.AvatarPhash != nil {
		t.Fatalf("text blob = %+v", txt)
	}
	again, err := lib.PutBlob(ctx, strings.NewReader("plain text"))
	if err != nil || again.SHA256 != txt.SHA256 {
		t.Fatalf("second put = %+v, %v", again, err)
	}

	// A card can use the image as its avatar.
	if _, _, err := lib.Cards.Create(ctx, "a", "upload", Edit{Data: []byte(`{"name": "A"}`), Source: "ui", AvatarBlob: img.SHA256}); err != nil {
		t.Fatal(err)
	}
}

func TestIngestBlob(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	os.MkdirAll(lib.blobs.TmpDir(), 0o755)
	src := filepath.Join(lib.blobs.TmpDir(), "from-plugin")
	os.WriteFile(src, []byte("plugin bytes"), 0o644)
	b, err := lib.IngestBlob(ctx, src)
	if err != nil {
		t.Fatal(err)
	}
	if b.Size != 12 {
		t.Fatalf("blob = %+v", b)
	}
	if _, err := lib.IngestBlob(ctx, "/etc/hosts"); err == nil {
		t.Fatal("ingested a file outside tmp")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/library/`
Expected: build failure, `lib.PutBlob undefined`.

- [ ] **Step 3: Write the implementation**

Create `internal/library/blobs.go`:

```go
package library

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/inkandale/innkeeper/internal/cardhash"
)

// Blob is a stored file.
type Blob struct {
	SHA256      string
	Size        int64
	MIME        string
	Store       string
	AvatarPhash *int64
	CreatedAt   time.Time
}

const blobCols = `sha256, size, mime, store, avatar_phash, created_at`

// PutBlob stores r and records it.
func (l *Library) PutBlob(ctx context.Context, r io.Reader) (Blob, error) {
	sha, size, err := l.blobs.Put(r)
	if err != nil {
		return Blob{}, err
	}
	return l.record(ctx, sha, size)
}

// IngestBlob moves a file a plugin wrote to the store's tmp folder into the
// store and records it. path is untrusted; see blobstore.Store.Ingest.
func (l *Library) IngestBlob(ctx context.Context, path string) (Blob, error) {
	sha, size, err := l.blobs.Ingest(path)
	if err != nil {
		return Blob{}, err
	}
	return l.record(ctx, sha, size)
}

// Blob returns a blob's record.
func (l *Library) Blob(ctx context.Context, sha string) (Blob, error) {
	return scanBlob(l.pool.QueryRow(ctx, `SELECT `+blobCols+` FROM blob WHERE sha256 = $1`, sha))
}

func scanBlob(row interface{ Scan(...any) error }) (Blob, error) {
	var b Blob
	err := row.Scan(&b.SHA256, &b.Size, &b.MIME, &b.Store, &b.AvatarPhash, &b.CreatedAt)
	return b, mapErr(err)
}

// record inserts the blob row. Images get an avatar_phash; an image that
// fails to decode is still stored, just without one.
func (l *Library) record(ctx context.Context, sha string, size int64) (Blob, error) {
	if b, err := l.Blob(ctx, sha); err == nil {
		return b, nil
	}
	path, err := l.blobs.Path(sha)
	if err != nil {
		return Blob{}, err
	}
	mime, err := sniff(path)
	if err != nil {
		return Blob{}, err
	}
	var phash *int64
	if strings.HasPrefix(mime, "image/") {
		if h, err := cardhash.ImagePhash(path); err == nil {
			phash = &h
		}
	}
	if _, err := l.pool.Exec(ctx, `INSERT INTO blob (sha256, size, mime, store, avatar_phash)
		VALUES ($1, $2, $3, $4, $5) ON CONFLICT (sha256) DO NOTHING`,
		sha, size, mime, l.blobs.Name, phash); err != nil {
		return Blob{}, err
	}
	return l.Blob(ctx, sha)
}

func sniff(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	head := make([]byte, 512)
	n, err := io.ReadFull(f, head)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return "", err
	}
	return http.DetectContentType(head[:n]), nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/library/`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/library/blobs.go internal/library/blobs_test.go
git commit -m "Record blobs with MIME type and avatar hash"
```

---

### Task 7: Tags, collections, and lorebook links

**Files:**
- Create: `internal/library/organize_test.go`
- Create: `internal/library/organize.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/library/organize_test.go`:

```go
package library

import (
	"context"
	"errors"
	"slices"
	"testing"
)

func TestCardTags(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	a, _ := newCard(t, lib, "a", `{"name": "A"}`)
	b, _ := newCard(t, lib, "b", `{"name": "B"}`)
	if err := lib.SetCardTags(ctx, a, []string{"Fantasy", " dwarf ", "fantasy", ""}); err != nil {
		t.Fatal(err)
	}
	// The first spelling of a tag wins.
	if err := lib.SetCardTags(ctx, b, []string{"FANTASY"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := lib.CardTags(ctx, a); !slices.Equal(got, []string{"dwarf", "Fantasy"}) {
		t.Fatalf("a tags = %v", got)
	}
	if got, _ := lib.CardTags(ctx, b); !slices.Equal(got, []string{"Fantasy"}) {
		t.Fatalf("b tags = %v", got)
	}
	if err := lib.SetCardTags(ctx, a, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := lib.CardTags(ctx, a); len(got) != 0 {
		t.Fatalf("a tags after clear = %v", got)
	}
	if err := lib.SetCardTags(ctx, 999, []string{"x"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing card: got %v, want ErrNotFound", err)
	}
}

func TestCollections(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	a, _ := newCard(t, lib, "a", `{"name": "A"}`)
	b, _ := newCard(t, lib, "b", `{"name": "B"}`)
	fav, err := lib.CreateCollection(ctx, "Favorites", "")
	if err != nil {
		t.Fatal(err)
	}
	other, err := lib.CreateCollection(ctx, "Other", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range []struct{ coll, card int64 }{{fav.ID, a}, {fav.ID, b}, {fav.ID, a}, {other.ID, a}} {
		if err := lib.AddToCollection(ctx, step.coll, step.card); err != nil {
			t.Fatal(err)
		}
	}
	if got, _ := lib.CollectionCards(ctx, fav.ID); !slices.Equal(got, []int64{a, b}) {
		t.Fatalf("favorites = %v", got)
	}
	if err := lib.RemoveFromCollection(ctx, fav.ID, a); err != nil {
		t.Fatal(err)
	}
	if got, _ := lib.CollectionCards(ctx, fav.ID); !slices.Equal(got, []int64{b}) {
		t.Fatalf("favorites after remove = %v", got)
	}
	if got, _ := lib.CollectionCards(ctx, other.ID); !slices.Equal(got, []int64{a}) {
		t.Fatalf("other = %v", got)
	}
	if err := lib.AddToCollection(ctx, fav.ID, 999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing card: got %v, want ErrNotFound", err)
	}
}

func TestLorebookLinks(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	card, _ := newCard(t, lib, "a", `{"name": "A"}`)
	book, _, err := lib.Lorebooks.Create(ctx, "lore", "upload", edit(`{"entries": []}`))
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := lib.LinkLorebook(ctx, card, book.ID); err != nil {
			t.Fatal(err)
		}
	}
	if got, _ := lib.CardLorebooks(ctx, card); !slices.Equal(got, []int64{book.ID}) {
		t.Fatalf("lorebooks = %v", got)
	}
	if err := lib.UnlinkLorebook(ctx, card, book.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := lib.CardLorebooks(ctx, card); len(got) != 0 {
		t.Fatalf("lorebooks after unlink = %v", got)
	}
	if err := lib.LinkLorebook(ctx, card, 999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing lorebook: got %v, want ErrNotFound", err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/library/`
Expected: build failure, `lib.SetCardTags undefined`.

- [ ] **Step 3: Write the implementation**

Create `internal/library/organize.go`:

```go
package library

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// SetCardTags replaces a card's tags. Tag names are trimmed and matched
// case-insensitively; new names create tags.
func (l *Library) SetCardTags(ctx context.Context, cardID int64, names []string) error {
	clean := make([]string, 0, len(names))
	for _, n := range names {
		if n = strings.TrimSpace(n); n != "" {
			clean = append(clean, n)
		}
	}
	tx, err := l.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `INSERT INTO tag (name)
		SELECT DISTINCT ON (lower(n)) n FROM unnest($1::text[]) n
		ON CONFLICT (lower(name)) DO NOTHING`, clean); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM card_tag WHERE card_id = $1`, cardID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO card_tag (card_id, tag_id)
		SELECT $1, id FROM tag WHERE lower(name) IN (SELECT lower(n) FROM unnest($2::text[]) n)`,
		cardID, clean); err != nil {
		return mapErr(err)
	}
	return tx.Commit(ctx)
}

// CardTags returns a card's tag names in alphabetical order.
func (l *Library) CardTags(ctx context.Context, cardID int64) ([]string, error) {
	rows, err := l.pool.Query(ctx, `SELECT t.name FROM card_tag ct JOIN tag t ON t.id = ct.tag_id
		WHERE ct.card_id = $1 ORDER BY lower(t.name)`, cardID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// Collection is a named group of cards. A card can be in many.
type Collection struct {
	ID          int64
	Name        string
	Description string
	CreatedAt   time.Time
}

// CreateCollection adds an empty collection.
func (l *Library) CreateCollection(ctx context.Context, name, description string) (Collection, error) {
	var c Collection
	err := l.pool.QueryRow(ctx, `INSERT INTO collection (name, description) VALUES ($1, $2)
		RETURNING id, name, description, created_at`, name, description).
		Scan(&c.ID, &c.Name, &c.Description, &c.CreatedAt)
	return c, err
}

// AddToCollection adds a card to a collection. Adding it twice is a no-op.
func (l *Library) AddToCollection(ctx context.Context, collectionID, cardID int64) error {
	_, err := l.pool.Exec(ctx, `INSERT INTO collection_card (collection_id, card_id) VALUES ($1, $2)
		ON CONFLICT DO NOTHING`, collectionID, cardID)
	return mapErr(err)
}

// RemoveFromCollection removes a card from a collection.
func (l *Library) RemoveFromCollection(ctx context.Context, collectionID, cardID int64) error {
	_, err := l.pool.Exec(ctx, `DELETE FROM collection_card WHERE collection_id = $1 AND card_id = $2`,
		collectionID, cardID)
	return err
}

// CollectionCards returns the IDs of a collection's cards, oldest addition first.
func (l *Library) CollectionCards(ctx context.Context, collectionID int64) ([]int64, error) {
	rows, err := l.pool.Query(ctx, `SELECT card_id FROM collection_card WHERE collection_id = $1
		ORDER BY added_at, card_id`, collectionID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[int64])
}

// LinkLorebook links a card to a lorebook's head revision. Linking twice is a no-op.
func (l *Library) LinkLorebook(ctx context.Context, cardID, lorebookID int64) error {
	_, err := l.pool.Exec(ctx, `INSERT INTO card_lorebook (card_id, lorebook_id) VALUES ($1, $2)
		ON CONFLICT DO NOTHING`, cardID, lorebookID)
	return mapErr(err)
}

// UnlinkLorebook removes a link between a card and a lorebook.
func (l *Library) UnlinkLorebook(ctx context.Context, cardID, lorebookID int64) error {
	_, err := l.pool.Exec(ctx, `DELETE FROM card_lorebook WHERE card_id = $1 AND lorebook_id = $2`,
		cardID, lorebookID)
	return err
}

// CardLorebooks returns the IDs of the lorebooks linked to a card.
func (l *Library) CardLorebooks(ctx context.Context, cardID int64) ([]int64, error) {
	rows, err := l.pool.Query(ctx, `SELECT lorebook_id FROM card_lorebook WHERE card_id = $1
		ORDER BY lorebook_id`, cardID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[int64])
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/library/`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/library/organize.go internal/library/organize_test.go
git commit -m "Add tags, collections, and lorebook links"
```

---

### Task 8: Similar cards

**Files:**
- Create: `internal/library/similar_test.go`
- Create: `internal/library/similar.go`

- [ ] **Step 1: Write the failing test**

Create `internal/library/similar_test.go`:

```go
package library

import (
	"context"
	"slices"
	"testing"
)

func TestSimilarCards(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	const text = "Brakka is a grumpy dwarf blacksmith who runs the forge at the edge of town. " +
		"She hates elves, loves good ale, and never turns away a customer with honest coin."
	orig, _ := newCard(t, lib, "orig", `{"name": "Brakka", "description": "`+text+`"}`)
	dup, _ := newCard(t, lib, "copy", `{"name": "Brakka", "description": " `+text+`\r\n", "creator_notes": ""}`)
	edited, _ := newCard(t, lib, "edited", `{"name": "Brakka 2", "description": "`+text+` She has a cat."}`)
	newCard(t, lib, "elf", `{"name": "Lia", "description": "A cheerful elven bard wanders the coast singing songs of the sea."}`)

	got, err := lib.SimilarCards(ctx, orig, 10)
	if err != nil {
		t.Fatal(err)
	}
	want := []Match{{dup, "content", 0}, {dup, "text", 0}}
	if len(got) != 3 || !slices.Equal(got[:2], want) || got[2].CardID != edited || got[2].Reason != "text" {
		t.Fatalf("matches = %+v", got)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/library/`
Expected: build failure, `lib.SimilarCards undefined`.

- [ ] **Step 3: Write the implementation**

Create `internal/library/similar.go`:

```go
package library

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// Match is another card that looks like a duplicate.
type Match struct {
	CardID int64
	// Reason is content (same content_hash), text (text_simhash), or avatar
	// (avatar_phash).
	Reason string
	// Distance is the Hamming distance between hashes; 0 for content.
	Distance int
}

// SimilarCards compares a card's head revision with every other card's head
// revision and returns matches within maxDistance bits, closest first. The
// spec allows a pairwise scan at library scale.
func (l *Library) SimilarCards(ctx context.Context, cardID int64, maxDistance int) ([]Match, error) {
	rows, err := l.pool.Query(ctx, `
		WITH heads AS (
			SELECT c.id, r.content_hash, r.text_simhash, b.avatar_phash
			FROM card c
			JOIN card_revision r ON r.id = c.head_revision_id
			LEFT JOIN blob b ON b.sha256 = r.avatar_blob
		),
		me AS (SELECT * FROM heads WHERE id = $1),
		other AS (SELECT * FROM heads WHERE id <> $1)
		SELECT * FROM (
			SELECT o.id, 'content', 0 FROM other o, me WHERE o.content_hash = me.content_hash
			UNION ALL
			SELECT o.id, 'text', bit_count((o.text_simhash # me.text_simhash)::bit(64))::int
			FROM other o, me WHERE bit_count((o.text_simhash # me.text_simhash)::bit(64)) <= $2
			UNION ALL
			SELECT o.id, 'avatar', bit_count((o.avatar_phash # me.avatar_phash)::bit(64))::int
			FROM other o, me WHERE bit_count((o.avatar_phash # me.avatar_phash)::bit(64)) <= $2
		) m (card_id, reason, distance)
		ORDER BY distance, card_id, reason`, cardID, maxDistance)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Match])
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/library/`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/library/similar.go internal/library/similar_test.go
git commit -m "Find similar cards by content and Hamming distance"
```

---

### Task 9: `innkeeper migrate`

**Files:**
- Modify: `cmd/innkeeper/main.go`
- Modify: `README.md`

- [ ] **Step 1: Add the command**

Replace `cmd/innkeeper/main.go` with:

```go
// Command innkeeper is the Innkeeper server and CLI.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/inkandale/innkeeper/internal/contract"
	"github.com/inkandale/innkeeper/internal/db"
)

const usage = `usage:
  innkeeper plugin check <plugin-dir>
  innkeeper migrate    (reads INNKEEPER_DATABASE_URL)`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "innkeeper:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 3 && args[0] == "plugin" && args[1] == "check" {
		return pluginCheck(args[2])
	}
	if len(args) == 1 && args[0] == "migrate" {
		return migrate()
	}
	return errors.New(usage)
}

func pluginCheck(dir string) error {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	results, err := contract.Check(context.Background(), dir, log)
	if err != nil {
		return err
	}
	failed := 0
	for _, r := range results {
		if r.Err != nil {
			failed++
			fmt.Printf("FAIL  %s: %v\n", r.Name, r.Err)
		} else {
			fmt.Printf("PASS  %s\n", r.Name)
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d checks failed", failed, len(results))
	}
	return nil
}

func migrate() error {
	url := os.Getenv("INNKEEPER_DATABASE_URL")
	if url == "" {
		return errors.New("INNKEEPER_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return err
	}
	defer pool.Close()
	return db.Migrate(ctx, pool)
}
```

- [ ] **Step 2: Document local testing**

Add a "Development" section to `README.md` with the `docker run` and `export` commands from this plan, and one line on `innkeeper migrate`.

- [ ] **Step 3: Check it end to end**

```bash
go build -o /tmp/innkeeper ./cmd/innkeeper
psql postgres://postgres:postgres@localhost:55432/postgres -c 'CREATE DATABASE innkeeper_dev'
INNKEEPER_DATABASE_URL=postgres://postgres:postgres@localhost:55432/innkeeper_dev /tmp/innkeeper migrate
INNKEEPER_DATABASE_URL=postgres://postgres:postgres@localhost:55432/innkeeper_dev /tmp/innkeeper migrate
go vet ./... && go test -race ./...
```

Expected: both migrations exit 0, and every package passes.

- [ ] **Step 4: Commit**

```bash
git add cmd/innkeeper/main.go README.md
git commit -m "Add innkeeper migrate command"
```
