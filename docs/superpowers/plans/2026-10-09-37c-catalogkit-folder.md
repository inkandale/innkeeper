# 37c: `catalogkit` and the `folder` plugin — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A `folder` catalog plugin indexes a directory of cards and lorebooks into its own schema and answers `catalog.search`, `get`, `similar`, and `ingest`, built on a shared `catalogkit` package that `chub-archive` (37d) reuses.

**Architecture:** `catalogkit` is a Go package in `innkeeper-plugins`. It owns the `item` and `item_version` tables in the plugin's `plugin_<name>` schema, derives names, hashes, thumbnails, and LSH bands from each entry's CCv3 data, and answers search, get, and similar. The `folder` plugin walks its directory, decodes files with the `card` package, and calls `Store.Put` for each changed file. Two small core changes come first: `cardhash` hashes an in-memory image, and `innkeeper plugin check` runs `catalog.ingest` before the catalog checks when it has a config.

**Tech Stack:** Go, PostgreSQL 18 (pgx v5), `golang.org/x/image/draw`.

Spec: `docs/superpowers/specs/2026-10-09-archive-catalog-design.md`, build step 3. Plans 37a and 37b added the protocol, the contract checks, and the core routes.

**Changes from the spec.** Task 12 updates the spec to match:

- LSH lives in an `lsh integer[]` column on `item` with one GIN index, not an `lsh_bucket` table. Nothing has to keep a second table in step.
- Each 64-bit hash splits into 9 bands of 7 bits, so two hashes within 8 bits share a band. `catalog.similar` refuses a `max_distance` over 8 with -32602. The core asks for 8 (`library.NearDistance`).
- The folder plugin has no `kind` filter. `catalog.search` already takes `kind`.
- Tags are stored and filtered in lowercase.
- `folder` returns an avatar only for the latest version, and only while the file is unchanged since ingest. Older versions have none.
- `folder` numbers versions 1, 2, 3, and gives each the file's modification time as its creation time.
- `innkeeper plugin check` runs `catalog.ingest` first when the catalog ingests and a config is set.

**Branches and worktrees.** **Do not** `git checkout` other branches or move HEAD in either main checkout.

| Part | Repo | Branch | Worktree |
|---|---|---|---|
| A (Tasks 1–3) | `innkeeper` | `inn-37c-core` | `../innkeeper-inn-37c` |
| B (Tasks 4–12) | `innkeeper-plugins` | `inn-37c-folder` | `../innkeeper-plugins-inn-37c` |

Part B needs Part A merged to `innkeeper` main and pushed, because the plugins repo fetches the core by version.

**Database tests** skip without `INNKEEPER_TEST_DATABASE_URL`, and a skipped run looks green. Run every test command in this plan with `INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres` set.

## Files

| File | Change |
|---|---|
| **innkeeper** | |
| `cardhash/phash.go`, `phash_test.go` | `DecodeImageBytes`, `Phash` |
| `internal/contract/catalog.go`, `contract_test.go` | The `catalog.ingest` check |
| **innkeeper-plugins** | |
| `go.mod`, `go.sum` | Core bump; pgx becomes direct |
| `internal/pgtest/pgtest.go` (new) | A schema per test |
| `internal/pluginio/pluginio.go` | `ReadIn`, moved from `card-import` |
| `card-import/import.go` | Uses `pluginio.ReadIn` |
| `catalogkit/catalogkit.go` (new) | `Config`, `Filter`, `Describe`, `Open`, migrations |
| `catalogkit/put.go` (new) | `Entry`, `Put`, `MarkRemovedExcept`, thumbnails, LSH bands |
| `catalogkit/read.go` (new) | `Get`, `Search`, `Similar` |
| `catalogkit/*_test.go` (new) | Tests |
| `folder/main.go`, `ingest.go`, `ingest_test.go`, `main_test.go` (new) | The plugin |
| `folder/plugin.toml`, `README.md`, `check-config.json`, `testdata/library/` (new) | Manifest, docs, contract-check data |
| `index.toml` | The `folder` entry |
| `Makefile` | `check` reads each plugin's `check-config.json` |
| `.github/workflows/ci.yml` | A PostgreSQL service |
| **innkeeper** (Task 12) | |
| `docs/superpowers/specs/2026-10-09-archive-catalog-design.md` | The changes above |

---

## Part A: core

Set up the worktree:

```bash
cd /Users/dgibbons/git/innkeeper
git worktree add -b inn-37c-core ../innkeeper-inn-37c main
cd ../innkeeper-inn-37c
```

### Task 1: `cardhash` hashes an image in memory

`folder` decodes the avatar once from card bytes, for both the thumbnail and the phash. CharX avatars have no file of their own, so `ImagePhash(path)` doesn't fit.

**Files:**
- Modify: `cardhash/phash.go`
- Test: `cardhash/phash_test.go`

- [ ] **Step 1: Write the failing test**

Append to `cardhash/phash_test.go`:

```go
func TestPhashMatchesImagePhash(t *testing.T) {
	path := filepath.Join("testdata", "avatar.png")
	want, err := ImagePhash(path)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	img, err := DecodeImageBytes(b)
	if err != nil {
		t.Fatal(err)
	}
	if got := Phash(img); got != want {
		t.Fatalf("Phash = %x, ImagePhash = %x", got, want)
	}
}

func TestDecodeImageBytesRefusesHuge(t *testing.T) {
	// A PNG header claiming 100000×100000 pixels.
	var buf bytes.Buffer
	buf.WriteString("\x89PNG\r\n\x1a\n")
	ihdr := []byte{0, 1, 0x86, 0xa0, 0, 1, 0x86, 0xa0, 8, 2, 0, 0, 0}
	binary.Write(&buf, binary.BigEndian, uint32(len(ihdr)))
	chunk := append([]byte("IHDR"), ihdr...)
	buf.Write(chunk)
	binary.Write(&buf, binary.BigEndian, crc32.ChecksumIEEE(chunk))
	if _, err := DecodeImageBytes(buf.Bytes()); err == nil || !strings.Contains(err.Error(), "pixel limit") {
		t.Fatalf("err = %v, want the pixel limit", err)
	}
}
```

Add `bytes`, `encoding/binary`, `hash/crc32`, `os`, `path/filepath`, and `strings` to the imports if missing.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./cardhash/ -run 'TestPhashMatchesImagePhash|TestDecodeImageBytesRefusesHuge'`
Expected: FAIL to compile: `undefined: DecodeImageBytes`, `undefined: Phash`.

- [ ] **Step 3: Implement**

In `cardhash/phash.go`, replace `DecodeImage` with:

```go
// DecodeImage decodes the image at path, refusing one whose decoded size
// could exhaust memory.
func DecodeImage(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return decodeImage(f)
}

// DecodeImageBytes decodes b with DecodeImage's pixel limit.
func DecodeImageBytes(b []byte) (image.Image, error) {
	return decodeImage(bytes.NewReader(b))
}

func decodeImage(r io.ReadSeeker) (image.Image, error) {
	cfg, _, err := image.DecodeConfig(r)
	if err != nil {
		return nil, err
	}
	if cfg.Width*cfg.Height > maxPixels {
		return nil, fmt.Errorf("image is %d×%d, over the %d pixel limit", cfg.Width, cfg.Height, maxPixels)
	}
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	img, _, err := image.Decode(r)
	return img, err
}
```

Then split `ImagePhash`. Keep its doc comment on `Phash`, and move every line after the decode into `Phash` unchanged, returning the hash without an error:

```go
// ImagePhash returns Phash of the image at path.
func ImagePhash(path string) (int64, error) {
	img, err := DecodeImage(path)
	if err != nil {
		return 0, err
	}
	return Phash(img), nil
}

// Phash returns the 64-bit DCT perceptual hash of img. It follows
// imagehash.phash from Python: grayscale, resize to 32×32, 2D DCT-II, keep
// the top-left 8×8 block, and set a bit (row-major, first is the high bit)
// for each value above the block's median.
//
// ponytail: resampling differs from PIL's, so on some images a Python port
// lands a few bits away; the Hamming threshold absorbs it.
func Phash(img image.Image) int64 {
	const n = 32
	gray := image.NewGray(image.Rect(0, 0, n, n))
	draw.CatmullRom.Scale(gray, gray.Bounds(), img, img.Bounds(), draw.Src, nil)
	// ...the rest of the old ImagePhash body, unchanged, ending in
	// `return <hash>` instead of `return <hash>, nil`.
}
```

Add `bytes` and `io` to the imports.

- [ ] **Step 4: Run the package's tests**

Run: `go test ./cardhash/`
Expected: PASS, including the existing test vectors.

- [ ] **Step 5: Commit**

```bash
git add cardhash/phash.go cardhash/phash_test.go
git commit -m "feat(cardhash): hash an image already in memory"
```

### Task 2: `plugin check` ingests a catalog before checking it

Without this, a catalog that indexes its source into a database has nothing to search during `plugin check`, and the paging, get, and similar checks pass on an empty index.

**Files:**
- Modify: `internal/contract/catalog.go`
- Test: `internal/contract/contract_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/contract/contract_test.go`:

```go
// With a config, a catalog that ingests runs catalog.ingest before the
// other catalog checks.
func TestCatalogIngestCheck(t *testing.T) {
	dir := testutil.FakePluginWith(t, []string{"catalog"})
	t.Cleanup(func() { Config = nil })

	Config = map[string]any{"source": "testdata"}
	results, err := Check(context.Background(), dir, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if f := failed(results); len(f) > 0 {
		t.Fatalf("checks failed: %v", f)
	}
	if !slices.ContainsFunc(results, func(r Result) bool { return r.Name == catalogIngestName }) {
		t.Fatal("the ingest check didn't run")
	}

	Config = map[string]any{"crash_ingest_at": 0.0}
	results, err = Check(context.Background(), dir, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if failed(results)[catalogIngestName] == nil {
		t.Fatal("an ingest that crashed passed")
	}
}
```

Add `slices` to the imports if missing.

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/contract/ -run TestCatalogIngestCheck`
Expected: FAIL to compile: `undefined: catalogIngestName`.

- [ ] **Step 3: Implement**

In `internal/contract/catalog.go`, add to the name constants:

```go
	catalogIngestName   = "catalog.ingest completes"
```

In `checkCatalog`, after the `if err != nil { return results }` that follows the describe check, insert:

```go
	// An ingested catalog is empty until it ingests its source.
	if info.Ingest && len(Config) > 0 {
		params := protocol.CatalogIngestParams{Run: "contract-check"}
		err := call(ctx, p, protocol.MethodCatalogIngest, params, &protocol.CatalogIngestResult{})
		results = append(results, Result{catalogIngestName, err})
		if err != nil {
			return results
		}
	}
```

- [ ] **Step 4: Run the package's tests**

Run: `go test ./internal/contract/`
Expected: PASS. `TestCatalogChecks` still counts six catalog checks, since it sets no config.

- [ ] **Step 5: Commit**

```bash
git add internal/contract/catalog.go internal/contract/contract_test.go
git commit -m "feat(contract): ingest a catalog before checking it"
```

### Task 3: Merge and push the core

- [ ] **Step 1: Run the whole suite**

Run: `INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres go test ./...`
Expected: PASS.

- [ ] **Step 2: Hand off to the owner**

Ask the owner to review, merge `inn-37c-core` into `innkeeper` main, and push. Part B fetches the pushed commit. Don't merge or push yourself.

---

## Part B: plugins

Set up the worktree once Part A is on `origin/main`:

```bash
cd /Users/dgibbons/git/innkeeper-plugins
git worktree add -b inn-37c-folder ../innkeeper-plugins-inn-37c main
cd ../innkeeper-plugins-inn-37c
```

### Task 4: Bump the core, add pgx and a test-schema helper

**Files:**
- Modify: `go.mod`, `go.sum`
- Create: `internal/pgtest/pgtest.go`, `internal/pgtest/pgtest_test.go`

- [ ] **Step 1: Bump the core and add pgx**

```bash
GOPRIVATE=github.com/inkandale/innkeeper go get github.com/inkandale/innkeeper@main
go get github.com/jackc/pgx/v5@v5.11.0
```

Expected: `go.mod` pins a core pseudo-version from the Part A merge, and `github.com/jackc/pgx/v5` moves to the direct `require` block once Task 4 uses it.

- [ ] **Step 2: Write the failing test**

Create `internal/pgtest/pgtest_test.go`:

```go
package pgtest

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestURLGivesItsOwnSchema(t *testing.T) {
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, URL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	var schema string
	if err := conn.QueryRow(ctx, "SELECT current_schema()").Scan(&schema); err != nil {
		t.Fatal(err)
	}
	if len(schema) < 6 || schema[:5] != "test_" {
		t.Fatalf("current_schema() = %q, want test_…", schema)
	}
}
```

- [ ] **Step 3: Run it to verify it fails**

Run: `go test ./internal/pgtest/`
Expected: FAIL to compile: `undefined: URL`.

- [ ] **Step 4: Implement**

Create `internal/pgtest/pgtest.go`:

```go
// Package pgtest gives each test a PostgreSQL schema of its own.
package pgtest

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net/url"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
)

// URL returns INNKEEPER_TEST_DATABASE_URL with search_path set to a new
// schema, dropped when the test ends. It skips the test without the variable.
func URL(t testing.TB) string {
	t.Helper()
	base := os.Getenv("INNKEEPER_TEST_DATABASE_URL")
	if base == "" {
		t.Skip("INNKEEPER_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("test_%x", rand.Uint64())
	if _, err := conn.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		if _, err := conn.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Errorf("dropping %s: %v", schema, err)
		}
		conn.Close(ctx)
	})
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	// pgx sends unknown URL parameters as session settings.
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	return u.String()
}
```

- [ ] **Step 5: Run it**

Run: `go test ./internal/pgtest/`
Expected: PASS (not `ok … [no tests to run]`, and not skipped: add `-v` and look for `--- PASS`).

- [ ] **Step 6: Commit**

```bash
go mod tidy
git add go.mod go.sum internal/pgtest
git commit -m "chore: bump the core, and give database tests a schema each"
```

### Task 5: `catalogkit` config, schema, and migrations

**Files:**
- Create: `catalogkit/catalogkit.go`, `catalogkit/catalogkit_test.go`

- [ ] **Step 1: Write the failing tests**

Create `catalogkit/catalogkit_test.go`:

```go
package catalogkit

import (
	"context"
	"strings"
	"testing"

	"github.com/inkandale/innkeeper-plugins/internal/pgtest"
	"github.com/inkandale/innkeeper/protocol"
)

var testConfig = Config{
	Kinds: []string{protocol.KindCard, protocol.KindLorebook},
	Filters: []Filter{
		{CatalogFilter: protocol.CatalogFilter{Name: "tag", Label: "Tag", Type: protocol.FilterText}, Field: "tag"},
		{CatalogFilter: protocol.CatalogFilter{Name: "dir", Label: "Folder", Type: protocol.FilterText}, Field: "attrs.dir"},
		{CatalogFilter: protocol.CatalogFilter{Name: "changed", Label: "Changed", Type: protocol.FilterDateRange}, Field: "updated"},
	},
	Ingest: true,
}

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), pgtest.URL(t), testConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

func TestDescribeIsValid(t *testing.T) {
	if err := Describe(testConfig).Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestOpenRefusesUnsupportedFilter(t *testing.T) {
	cfg := testConfig
	cfg.Filters = []Filter{{CatalogFilter: protocol.CatalogFilter{Name: "rating", Label: "Rating",
		Type: protocol.FilterNumberRange}, Field: "attrs.rating"}}
	_, err := Open(context.Background(), "postgres://unused", cfg)
	if err == nil || !strings.Contains(err.Error(), `filter "rating"`) {
		t.Fatalf("err = %v, want the rating filter refused", err)
	}
}

// Opening twice applies each migration once, and runs the plugin's own.
func TestOpenMigratesOnce(t *testing.T) {
	ctx := context.Background()
	url := pgtest.URL(t)
	cfg := testConfig
	cfg.Migrations = []Migration{{Name: "test/1", SQL: "CREATE TABLE extra (x int)"}}
	for range 2 {
		s, err := Open(ctx, url, cfg)
		if err != nil {
			t.Fatal(err)
		}
		s.Close()
	}
	s, err := Open(ctx, url, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var n int
	if err := s.Pool().QueryRow(ctx, "SELECT count(*) FROM migration").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if want := len(migrations) + 1; n != want {
		t.Fatalf("%d migrations recorded, want %d", n, want)
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./catalogkit/`
Expected: FAIL to compile: `undefined: Config`.

- [ ] **Step 3: Implement**

Create `catalogkit/catalogkit.go`:

```go
// Package catalogkit is the index a catalog plugin keeps in its own schema:
// items and their versions, full-text search, declared filters, and near
// matches by hash.
package catalogkit

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/inkandale/innkeeper/protocol"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Config describes one catalog.
type Config struct {
	Kinds   []string
	Filters []Filter
	Ingest  bool
	// Migrations are the plugin's own, applied after catalogkit's, each once
	// by name.
	Migrations []Migration
}

// Migration is one schema change.
type Migration struct {
	Name string
	SQL  string
}

// Filter is a declared filter and what it reads. Field is "tag" or
// "attrs.<key>" for a text filter, and "added" or "updated" for a date
// range.
type Filter struct {
	protocol.CatalogFilter
	Field string
}

// ponytail: only the filters folder declares; chub-archive (37d) adds number ranges.
func (f Filter) check() error {
	ok := false
	switch f.Type {
	case protocol.FilterText:
		ok = f.Field == "tag" || strings.HasPrefix(f.Field, "attrs.") && len(f.Field) > len("attrs.")
	case protocol.FilterDateRange:
		ok = f.Field == "added" || f.Field == "updated"
	}
	if !ok {
		return fmt.Errorf("filter %q: catalogkit can't serve a %s filter on %q", f.Name, f.Type, f.Field)
	}
	return nil
}

// Sorts are the orders every catalogkit catalog offers. Relevance falls back
// to newest when the query is empty.
var sorts = []protocol.CatalogOption{
	{Value: "relevance", Label: "Best match"},
	{Value: "name", Label: "Name"},
	{Value: "newest", Label: "Recently changed"},
}

// Describe returns the catalog.describe reply for cfg.
func Describe(cfg Config) protocol.CatalogInfo {
	info := protocol.CatalogInfo{Kinds: cfg.Kinds, Filters: []protocol.CatalogFilter{}, Sorts: sorts,
		Ingest: cfg.Ingest, Similar: true}
	for _, f := range cfg.Filters {
		info.Filters = append(info.Filters, f.CatalogFilter)
	}
	return info
}

// Store is an open catalog index.
type Store struct {
	pool *pgxpool.Pool
	cfg  Config
}

// Open checks cfg, connects to the plugin's database, and migrates it.
func Open(ctx context.Context, dsn string, cfg Config) (*Store, error) {
	errs := []error{Describe(cfg).Validate()}
	for _, f := range cfg.Filters {
		errs = append(errs, f.check())
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	if err := migrate(ctx, pool, append(slices.Clone(migrations), cfg.Migrations...)); err != nil {
		pool.Close()
		return nil, err
	}
	return &Store{pool: pool, cfg: cfg}, nil
}

// Close closes the database connections.
func (s *Store) Close() { s.pool.Close() }

// Pool is the database, for the plugin's own tables.
func (s *Store) Pool() *pgxpool.Pool { return s.pool }

// migrate applies each migration not yet recorded. The lock keeps the API
// and worker, which start plugin instances together, from racing.
func migrate(ctx context.Context, pool *pgxpool.Pool, ms []Migration) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtext('catalogkit:' || current_schema()))"); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "CREATE TABLE IF NOT EXISTS migration (name text PRIMARY KEY, applied timestamptz NOT NULL DEFAULT now())"); err != nil {
		return err
	}
	for _, m := range ms {
		tag, err := tx.Exec(ctx, "INSERT INTO migration (name) VALUES ($1) ON CONFLICT DO NOTHING", m.Name)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			continue
		}
		if _, err := tx.Exec(ctx, m.SQL); err != nil {
			return fmt.Errorf("migration %s: %w", m.Name, err)
		}
	}
	return tx.Commit(ctx)
}

var migrations = []Migration{{Name: "catalogkit/1-items", SQL: `
-- array_to_string isn't immutable, so a generated column can't call it directly.
CREATE FUNCTION tags_text(text[]) RETURNS text
	LANGUAGE sql IMMUTABLE PARALLEL SAFE RETURN array_to_string($1, ' ');

CREATE TABLE item (
	id text PRIMARY KEY,
	kind text NOT NULL,
	name text NOT NULL,
	author text NOT NULL DEFAULT '',
	tags text[] NOT NULL DEFAULT '{}',
	summary text NOT NULL DEFAULT '',
	description text NOT NULL DEFAULT '',
	attrs jsonb NOT NULL DEFAULT '{}',
	added timestamptz NOT NULL DEFAULT now(),
	updated timestamptz NOT NULL,
	removed boolean NOT NULL DEFAULT false,
	latest integer NOT NULL,
	latest_version text NOT NULL,
	content_hash text NOT NULL,
	text_simhash bigint,
	image_phash bigint,
	lsh integer[] NOT NULL DEFAULT '{}',
	thumbnail bytea,
	search tsvector GENERATED ALWAYS AS (
		setweight(to_tsvector('english', name), 'A') ||
		setweight(to_tsvector('english', author || ' ' || tags_text(tags)), 'B') ||
		setweight(to_tsvector('english', summary || ' ' || description), 'C')) STORED
);
CREATE INDEX item_search ON item USING gin (search);
CREATE INDEX item_tags ON item USING gin (tags);
CREATE INDEX item_attrs ON item USING gin (attrs jsonb_path_ops);
CREATE INDEX item_lsh ON item USING gin (lsh);
CREATE INDEX item_content_hash ON item (content_hash);
CREATE INDEX item_name ON item (lower(name), id);
CREATE INDEX item_updated ON item (updated, id);

-- data is json, not jsonb, so get returns the bytes content_hash was taken from.
CREATE TABLE item_version (
	item_id text NOT NULL REFERENCES item ON DELETE CASCADE,
	seq integer NOT NULL,
	version text NOT NULL,
	created timestamptz,
	data json NOT NULL,
	PRIMARY KEY (item_id, seq),
	UNIQUE (item_id, version)
);
`}}
```

- [ ] **Step 4: Run the tests**

Run: `go test -v ./catalogkit/`
Expected: PASS for all three, with `TestOpenMigratesOnce` not skipped.

- [ ] **Step 5: Commit**

```bash
git add catalogkit
git commit -m "feat(catalogkit): catalog config, schema, and migrations"
```

### Task 6: `Put` and `MarkRemovedExcept`

`Put` derives every stored field from the entry's data, so plugins can't disagree with the core on names or hashes.

**Files:**
- Create: `catalogkit/put.go`, `catalogkit/put_test.go`

- [ ] **Step 1: Write the failing tests**

Create `catalogkit/put_test.go`:

```go
package catalogkit

import (
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"testing"
	"time"

	"github.com/inkandale/innkeeper/protocol"
)

func testCard(name, description string) json.RawMessage {
	b, _ := json.Marshal(map[string]any{"spec": "chara_card_v3", "spec_version": "3.0", "data": map[string]any{
		"name": name, "creator": "ann", "description": description, "tags": []string{"Fantasy", " elf "},
		"creator_notes": "A test card."}})
	return b
}

func testImage() image.Image {
	img := image.NewRGBA(image.Rect(0, 0, 400, 600))
	for y := range 600 {
		for x := range 400 {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 128, 255})
		}
	}
	return img
}

var t0 = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

func TestPutVersions(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	e := Entry{ID: "a.json", Kind: protocol.KindCard, Data: testCard("Ann", "One."), Image: testImage(), Updated: t0}

	steps := []struct {
		name string
		edit func()
		want Change
	}{
		{"new", func() {}, Added},
		{"same data", func() { e.Attrs = map[string][]string{"dir": {"x"}} }, Unchanged},
		{"changed data", func() { e.Data = testCard("Ann", "Two."); e.Updated = t0.Add(time.Hour) }, Updated},
	}
	for _, st := range steps {
		st.edit()
		got, err := s.Put(ctx, e)
		if err != nil {
			t.Fatalf("%s: %v", st.name, err)
		}
		if got != st.want {
			t.Fatalf("%s: change = %v, want %v", st.name, got, st.want)
		}
	}

	var name, author, latest string
	var tags []string
	var thumb []byte
	var phash *int64
	err := s.pool.QueryRow(ctx, `SELECT name, author, tags, latest_version, thumbnail, image_phash FROM item WHERE id = 'a.json'`).
		Scan(&name, &author, &tags, &latest, &thumb, &phash)
	if err != nil {
		t.Fatal(err)
	}
	if name != "Ann" || author != "ann" || latest != "2" || len(tags) != 2 || tags[0] != "fantasy" || tags[1] != "elf" {
		t.Fatalf("item = %q %q %v %q", name, author, tags, latest)
	}
	if len(thumb) == 0 || len(thumb) > protocol.MaxThumbnail || phash == nil {
		t.Fatalf("thumbnail %d bytes, phash %v", len(thumb), phash)
	}
	cfg, _, err := image.DecodeConfig(bytesReader(thumb))
	if err != nil || cfg.Width != 160 {
		t.Fatalf("thumbnail is %d wide, err %v", cfg.Width, err)
	}
}

func TestPutRefusesBadData(t *testing.T) {
	s := openTest(t)
	_, err := s.Put(context.Background(), Entry{ID: "x", Kind: protocol.KindCard, Data: json.RawMessage(`{"spec":"chara_card_v3"}`), Updated: t0})
	if !errors.Is(err, ErrBadData) {
		t.Fatalf("err = %v, want ErrBadData", err)
	}
}

func TestMarkRemovedExcept(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	for _, id := range []string{"a", "b"} {
		if _, err := s.Put(ctx, Entry{ID: id, Kind: protocol.KindCard, Data: testCard(id, id), Updated: t0}); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := s.MarkRemovedExcept(ctx, []string{"a"}); err != nil || n != 1 {
		t.Fatalf("marked %d, err %v; want 1", n, err)
	}
	// Putting a removed item back is a change even with the same data.
	if c, err := s.Put(ctx, Entry{ID: "b", Kind: protocol.KindCard, Data: testCard("b", "b"), Updated: t0}); err != nil || c != Updated {
		t.Fatalf("change = %v, err %v; want Updated", c, err)
	}
	// An empty keep list removes everything.
	if n, err := s.MarkRemovedExcept(ctx, nil); err != nil || n != 2 {
		t.Fatalf("marked %d, err %v; want 2", n, err)
	}
}

// Hashes within MaxDistance bits share a band; one further apart may not.
func TestBands(t *testing.T) {
	h := int64(0x0123456789abcdef)
	var near int64 = h
	for b := range MaxDistance {
		near ^= 1 << (7 * b) // one bit in each of the first 8 bands
	}
	if !shareBand(lsh(&h, nil), lsh(&near, nil)) {
		t.Fatal("hashes 8 bits apart share no band")
	}
	far := near ^ 1<<(7*MaxDistance) // and one in the last band
	if shareBand(lsh(&h, nil), lsh(&far, nil)) {
		t.Fatal("hashes with a bit off in every band share a band")
	}
	if shareBand(lsh(&h, nil), lsh(nil, &h)) {
		t.Fatal("a simhash and a phash share a band")
	}
}

func shareBand(a, b []int32) bool {
	for _, x := range a {
		for _, y := range b {
			if x == y {
				return true
			}
		}
	}
	return false
}
```

Add a helper at the bottom of the same file:

```go
func bytesReader(b []byte) *bytes.Reader { return bytes.NewReader(b) }
```

and `bytes` to its imports.

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./catalogkit/`
Expected: FAIL to compile: `undefined: Entry`.

- [ ] **Step 3: Implement**

Create `catalogkit/put.go`:

```go
package catalogkit

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"strconv"
	"strings"
	"time"

	"github.com/inkandale/innkeeper/cardhash"
	"github.com/inkandale/innkeeper/protocol"
	"github.com/jackc/pgx/v5"
	"golang.org/x/image/draw"
)

// ErrBadData wraps Put's errors about the entry itself, which an ingest skips.
var ErrBadData = errors.New("catalogkit: bad data")

// Entry is one item as a plugin read it from its source.
type Entry struct {
	ID   string
	Kind string // protocol.KindCard or protocol.KindLorebook
	// Data is a CCv3 card, or a CCv3 character_book for a lorebook.
	Data json.RawMessage
	// Image is the card's avatar, or nil. It gives the thumbnail and phash.
	Image image.Image
	// Attrs are the values attrs.<key> filters match.
	Attrs map[string][]string
	// Updated is when the source last changed. It dates a new version.
	Updated time.Time
}

// Change is what Put did.
type Change int

const (
	Unchanged Change = iota
	Added
	Updated
)

// Put stores e. Data that hashes differently from the latest version adds a
// version; the same data only refreshes the item's other fields.
func (s *Store) Put(ctx context.Context, e Entry) (Change, error) {
	d, err := derive(e)
	if err != nil {
		return 0, fmt.Errorf("%w: %s: %w", ErrBadData, e.ID, err)
	}
	if e.Attrs == nil {
		e.Attrs = map[string][]string{}
	}
	attrs, err := json.Marshal(e.Attrs)
	if err != nil {
		return 0, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	var hash string
	var latest int
	var removed bool
	err = tx.QueryRow(ctx, `SELECT content_hash, latest, removed FROM item WHERE id = $1 FOR UPDATE`, e.ID).
		Scan(&hash, &latest, &removed)
	change, newVersion := Unchanged, true
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		change, latest = Added, 1
	case err != nil:
		return 0, err
	case hash != d.contentHash:
		change, latest = Updated, latest+1
	default:
		newVersion = false
		if removed {
			change = Updated
		}
	}
	version := strconv.Itoa(latest)
	_, err = tx.Exec(ctx, `
		INSERT INTO item (id, kind, name, author, tags, summary, description, attrs, updated, latest,
			latest_version, content_hash, text_simhash, image_phash, lsh, thumbnail)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
		ON CONFLICT (id) DO UPDATE SET kind = excluded.kind, name = excluded.name, author = excluded.author,
			tags = excluded.tags, summary = excluded.summary, description = excluded.description,
			attrs = excluded.attrs, updated = excluded.updated, latest = excluded.latest,
			latest_version = excluded.latest_version, content_hash = excluded.content_hash,
			text_simhash = excluded.text_simhash, image_phash = excluded.image_phash, lsh = excluded.lsh,
			thumbnail = excluded.thumbnail, removed = false`,
		e.ID, e.Kind, d.name, d.author, d.tags, d.summary, d.description, string(attrs), e.Updated, latest,
		version, d.contentHash, d.simhash, d.phash, lsh(d.simhash, d.phash), d.thumbnail)
	if err != nil {
		return 0, err
	}
	if newVersion {
		_, err = tx.Exec(ctx, `INSERT INTO item_version (item_id, seq, version, created, data) VALUES ($1, $2, $3, $4, $5)`,
			e.ID, latest, version, e.Updated, string(e.Data))
		if err != nil {
			return 0, err
		}
	}
	return change, tx.Commit(ctx)
}

// MarkRemovedExcept marks every item not in keep removed and returns how
// many it marked. Removed items leave search and similar, but get still
// answers for them, so adopted cards keep a working source.
func (s *Store) MarkRemovedExcept(ctx context.Context, keep []string) (int64, error) {
	if keep == nil {
		keep = []string{} // NULL would match nothing
	}
	tag, err := s.pool.Exec(ctx, `UPDATE item SET removed = true WHERE NOT removed AND id <> ALL($1)`, keep)
	return tag.RowsAffected(), err
}

// derived is what Put stores beside the entry's own fields.
type derived struct {
	name, author, summary, description string
	tags                               []string
	contentHash                        string
	simhash, phash                     *int64
	thumbnail                          []byte
}

// derive hashes e as the library does: a card over cardhash.CardData, a
// lorebook whole.
func derive(e Entry) (derived, error) {
	d := derived{tags: []string{}}
	hashed := []byte(e.Data)
	var sim int64
	var ok bool
	var err error
	switch e.Kind {
	case protocol.KindCard:
		if hashed, _, err = cardhash.CardData(e.Data); err == nil && hashed == nil {
			err = errors.New("the card has no data")
		}
		if err == nil {
			sim, ok, err = cardhash.CardSimhash(hashed)
		}
	case protocol.KindLorebook:
		sim, ok, err = cardhash.LorebookSimhash(hashed)
	default:
		err = fmt.Errorf("kind %q: catalogkit stores cards and lorebooks", e.Kind)
	}
	if err != nil {
		return d, err
	}
	if ok {
		d.simhash = &sim
	}
	if d.contentHash, err = cardhash.ContentHash(hashed); err != nil {
		return d, err
	}
	var m map[string]any
	if err := json.Unmarshal(hashed, &m); err != nil {
		return d, err
	}
	str := func(k string) string { s, _ := m[k].(string); return strings.TrimSpace(s) }
	d.name, d.author, d.description = cmp.Or(str("name"), e.ID), str("creator"), str("description")
	d.summary = truncate(cmp.Or(str("creator_notes"), d.description), 300)
	tags, _ := m["tags"].([]any)
	for _, t := range tags {
		if s, _ := t.(string); strings.TrimSpace(s) != "" {
			d.tags = append(d.tags, strings.ToLower(strings.TrimSpace(s)))
		}
	}
	if e.Image != nil {
		p := cardhash.Phash(e.Image)
		d.phash = &p
		d.thumbnail = thumbnail(e.Image)
	}
	return d, nil
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return strings.TrimSpace(string(r[:n])) + "…"
	}
	return s
}

// thumbnail scales img to 160 pixels wide on white, as a JPEG of at most
// protocol.MaxThumbnail bytes, or nil if no quality fits.
func thumbnail(img image.Image) []byte {
	b := img.Bounds()
	if b.Dx() == 0 || b.Dy() == 0 {
		return nil
	}
	w := min(160, b.Dx())
	h := max(1, b.Dy()*w/b.Dx())
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(dst, dst.Bounds(), image.White, image.Point{}, draw.Src)
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, b, draw.Over, nil)
	for _, q := range []int{80, 60, 40} {
		var buf bytes.Buffer
		if jpeg.Encode(&buf, dst, &jpeg.Options{Quality: q}) == nil && buf.Len() <= protocol.MaxThumbnail {
			return buf.Bytes()
		}
	}
	return nil
}

// bands splits a 64-bit hash into 9 bands of 7 bits, ignoring the top bit.
// Hashes within MaxDistance bits differ in at most 8 bands, so they share
// at least one.
const bands = 9

// MaxDistance is the largest max_distance Similar serves without missing
// matches.
const MaxDistance = bands - 1

// lsh returns the bands of each hash, tagged with the hash and band number
// so a simhash band never equals a phash band.
func lsh(simhash, phash *int64) []int32 {
	out := []int32{}
	for which, h := range []*int64{simhash, phash} {
		if h == nil {
			continue
		}
		for b := range bands {
			bucket := int32(uint64(*h)>>(7*b)) & 0x7f
			out = append(out, int32(which)<<12|int32(b)<<8|bucket)
		}
	}
	return out
}
```

- [ ] **Step 4: Run the tests**

Run: `go test -v ./catalogkit/`
Expected: PASS, none skipped.

- [ ] **Step 5: Commit**

```bash
git add catalogkit
git commit -m "feat(catalogkit): store items and versions with their hashes"
```

### Task 7: `Get`

**Files:**
- Create: `catalogkit/read.go`, `catalogkit/read_test.go`

- [ ] **Step 1: Write the failing test**

Create `catalogkit/read_test.go`:

```go
package catalogkit

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/inkandale/innkeeper/cardhash"
	"github.com/inkandale/innkeeper/protocol"
)

func put(t *testing.T, s *Store, e Entry) {
	t.Helper()
	if _, err := s.Put(context.Background(), e); err != nil {
		t.Fatal(err)
	}
}

func wantCode(t *testing.T, err error, code int) {
	t.Helper()
	var rpc *protocol.Error
	if !errors.As(err, &rpc) || rpc.Code != code {
		t.Fatalf("err = %v, want code %d", err, code)
	}
}

func TestGet(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	put(t, s, Entry{ID: "a", Kind: protocol.KindCard, Data: testCard("Ann", "One."), Updated: t0})
	put(t, s, Entry{ID: "a", Kind: protocol.KindCard, Data: testCard("Ann", "Two."), Updated: t0.Add(time.Hour)})

	res, err := s.Get(ctx, "a", "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Version != "2" || len(res.Versions) != 2 || res.Versions[0].Version != "2" ||
		res.Versions[1].Created != "2026-01-02T03:04:05Z" {
		t.Fatalf("got version %q of %+v", res.Version, res.Versions)
	}
	data, _, _ := cardhash.CardData(res.Data)
	if hash, _ := cardhash.ContentHash(data); hash != res.Item.Hashes.ContentHash {
		t.Fatal("latest data doesn't hash to the item's content_hash")
	}

	old, err := s.Get(ctx, "a", "1")
	if err != nil || old.Version != "1" {
		t.Fatalf("version 1: %v, %v", old.Version, err)
	}
	_, err = s.Get(ctx, "a", "9")
	wantCode(t, err, protocol.CodeRemoteNotFound)
	_, err = s.Get(ctx, "missing", "")
	wantCode(t, err, protocol.CodeRemoteNotFound)
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./catalogkit/ -run TestGet`
Expected: FAIL to compile: `s.Get undefined`.

- [ ] **Step 3: Implement**

Create `catalogkit/read.go`:

```go
package catalogkit

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/inkandale/innkeeper/protocol"
	"github.com/jackc/pgx/v5"
)

// itemColumns are the columns itemDest scans, in order.
const itemColumns = `id, kind, name, author, tags, summary, latest_version, content_hash, text_simhash, image_phash, thumbnail`

func itemDest(it *protocol.CatalogItem) []any {
	return []any{&it.ID, &it.Kind, &it.Name, &it.Author, &it.Tags, &it.Summary, &it.LatestVersion,
		&it.Hashes.ContentHash, &it.Hashes.TextSimhash, &it.Hashes.ImagePhash, &it.Thumbnail}
}

func invalid(err error) error {
	return protocol.NewError(protocol.CodeInvalidParams, err.Error(), false)
}

func notFound(format string, args ...any) error {
	return protocol.NewError(protocol.CodeRemoteNotFound, fmt.Sprintf(format, args...), false)
}

// Get returns version of item id, or its latest when version is empty.
// Removed items still answer.
func (s *Store) Get(ctx context.Context, id, version string) (protocol.CatalogGetResult, error) {
	var res protocol.CatalogGetResult
	err := s.pool.QueryRow(ctx, `SELECT `+itemColumns+` FROM item WHERE id = $1`, id).Scan(itemDest(&res.Item)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return res, notFound("no item %q", id)
	}
	if err != nil {
		return res, err
	}
	rows, _ := s.pool.Query(ctx, `SELECT version, created FROM item_version WHERE item_id = $1 ORDER BY seq DESC`, id)
	res.Versions, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (protocol.CatalogVersion, error) {
		var v protocol.CatalogVersion
		var created *time.Time
		err := r.Scan(&v.Version, &created)
		if created != nil {
			v.Created = created.UTC().Format(time.RFC3339)
		}
		return v, err
	})
	if err != nil {
		return res, err
	}
	res.Version = cmp.Or(version, res.Item.LatestVersion)
	var data string
	err = s.pool.QueryRow(ctx, `SELECT data FROM item_version WHERE item_id = $1 AND version = $2`, id, res.Version).Scan(&data)
	if errors.Is(err, pgx.ErrNoRows) {
		return res, notFound("item %q has no version %q", id, res.Version)
	}
	res.Data = json.RawMessage(data)
	return res, err
}
```

- [ ] **Step 4: Run the tests**

Run: `go test -v ./catalogkit/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add catalogkit
git commit -m "feat(catalogkit): get an item's version"
```

### Task 8: `Search`

Paging is keyset: the cursor holds the last row's sort key and ID, so a page never repeats a row while the index is unchanged.

**Files:**
- Modify: `catalogkit/read.go`, `catalogkit/read_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `catalogkit/read_test.go`:

```go
func searchIDs(t *testing.T, s *Store, p protocol.CatalogSearchParams) []string {
	t.Helper()
	var ids []string
	for {
		res, err := s.Search(context.Background(), p)
		if err != nil {
			t.Fatal(err)
		}
		for _, it := range res.Items {
			ids = append(ids, it.ID)
		}
		if res.Next == "" {
			return ids
		}
		p.Page.Cursor = res.Next
	}
}

func seed(t *testing.T) *Store {
	s := openTest(t)
	put(t, s, Entry{ID: "c1", Kind: protocol.KindCard, Data: testCard("Bram the Smith", "A dwarf blacksmith."),
		Attrs: map[string][]string{"dir": {"dwarves"}}, Updated: t0})
	put(t, s, Entry{ID: "c2", Kind: protocol.KindCard, Data: testCard("Aria", "An elf ranger who hunts with a smith's bow."),
		Attrs: map[string][]string{"dir": {"elves"}}, Updated: t0.Add(48 * time.Hour)})
	put(t, s, Entry{ID: "c3", Kind: protocol.KindCard, Data: testCard("Cole", "A sailor."), Updated: t0.Add(24 * time.Hour)})
	put(t, s, Entry{ID: "l1", Kind: protocol.KindLorebook,
		Data: json.RawMessage(`{"name":"Forge lore","description":"Anvils.","entries":[]}`), Updated: t0})
	return s
}

func TestSearch(t *testing.T) {
	s := seed(t)
	tests := []struct {
		name string
		p    protocol.CatalogSearchParams
		want []string
	}{
		{"all, newest first", protocol.CatalogSearchParams{}, []string{"c2", "c3", "l1", "c1"}},
		{"name", protocol.CatalogSearchParams{Sort: "name"}, []string{"c2", "c1", "c3", "l1"}},
		{"relevance ranks a name match first", protocol.CatalogSearchParams{Q: "smith"}, []string{"c1", "c2"}},
		{"kind", protocol.CatalogSearchParams{Kind: protocol.KindLorebook}, []string{"l1"}},
		{"tag", protocol.CatalogSearchParams{Filters: map[string]json.RawMessage{"tag": json.RawMessage(`"FANTASY"`)}}, []string{"c2", "c3", "c1"}},
		{"attrs", protocol.CatalogSearchParams{Filters: map[string]json.RawMessage{"dir": json.RawMessage(`"elves"`)}}, []string{"c2"}},
		{"date range", protocol.CatalogSearchParams{Filters: map[string]json.RawMessage{
			"changed": json.RawMessage(`{"from":"2026-01-03","to":"2026-01-03"}`)}}, []string{"c3"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, size := range []int{0, 1} { // one page, then pages of one
				tt.p.Page.Size = size
				if got := searchIDs(t, s, tt.p); !slices.Equal(got, tt.want) {
					t.Fatalf("size %d: got %v, want %v", size, got, tt.want)
				}
			}
		})
	}
}

func TestSearchRefuses(t *testing.T) {
	s := seed(t)
	ctx := context.Background()
	_, err := s.Search(ctx, protocol.CatalogSearchParams{Filters: map[string]json.RawMessage{"nope": json.RawMessage(`"x"`)}})
	wantCode(t, err, protocol.CodeInvalidParams)

	res, err := s.Search(ctx, protocol.CatalogSearchParams{Sort: "name", Page: protocol.CatalogPage{Size: 1}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Search(ctx, protocol.CatalogSearchParams{Sort: "newest", Page: protocol.CatalogPage{Size: 1, Cursor: res.Next}})
	wantCode(t, err, protocol.CodeInvalidParams)
	_, err = s.Search(ctx, protocol.CatalogSearchParams{Page: protocol.CatalogPage{Cursor: "garbage"}})
	wantCode(t, err, protocol.CodeInvalidParams)
}

func TestSearchSkipsRemoved(t *testing.T) {
	s := seed(t)
	if _, err := s.MarkRemovedExcept(context.Background(), []string{"c1"}); err != nil {
		t.Fatal(err)
	}
	if got := searchIDs(t, s, protocol.CatalogSearchParams{}); !slices.Equal(got, []string{"c1"}) {
		t.Fatalf("got %v", got)
	}
}
```

Add `encoding/json` and `slices` to the file's imports.

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./catalogkit/ -run TestSearch`
Expected: FAIL to compile: `s.Search undefined`.

- [ ] **Step 3: Implement**

Append to `catalogkit/read.go`:

```go
const defaultPage = 50

// cursor is the last row of a page: its sort key as Postgres prints it,
// and its ID.
type cursor struct {
	Sort string `json:"s"`
	Key  string `json:"k"`
	ID   string `json:"id"`
}

// query collects a WHERE clause and its arguments.
type query struct {
	conds []string
	args  []any
}

func (b *query) arg(v any) string {
	b.args = append(b.args, v)
	return fmt.Sprintf("$%d", len(b.args))
}

func (b *query) where(cond string) { b.conds = append(b.conds, cond) }

// Search answers catalog.search over items not removed.
func (s *Store) Search(ctx context.Context, p protocol.CatalogSearchParams) (protocol.CatalogSearchResult, error) {
	res := protocol.CatalogSearchResult{Items: []protocol.CatalogItem{}}
	if err := Describe(s.cfg).CheckSearch(p); err != nil {
		return res, invalid(err)
	}
	b := &query{}
	b.where("NOT removed")
	text := strings.TrimSpace(p.Q)
	var tsq string
	if text != "" {
		tsq = "websearch_to_tsquery('english', " + b.arg(text) + ")"
		b.where("search @@ " + tsq)
	}
	if p.Kind != "" {
		b.where("kind = " + b.arg(p.Kind))
	}
	for name, raw := range p.Filters {
		s.filter(b, name, raw)
	}

	sort := cmp.Or(p.Sort, sorts[0].Value)
	if sort == "relevance" && text == "" {
		sort = "newest"
	}
	// Each sort orders by its key, then id in the same direction, so one
	// row comparison continues from the cursor.
	var key, cast, dir, after string
	switch sort {
	case "relevance":
		key, cast, dir, after = "ts_rank(search, "+tsq+")::float8", "float8", "DESC", "<"
	case "name":
		key, cast, dir, after = "lower(name)", "text", "ASC", ">"
	default:
		key, cast, dir, after = "updated", "timestamptz", "DESC", "<"
	}
	if p.Page.Cursor != "" {
		var c cursor
		raw, err := base64.RawURLEncoding.DecodeString(p.Page.Cursor)
		if err == nil {
			err = json.Unmarshal(raw, &c)
		}
		if err != nil || c.Sort != sort {
			return res, invalid(errors.New("page.cursor didn't come from this search"))
		}
		b.where(fmt.Sprintf("(%s, id) %s (%s::%s, %s)", key, after, b.arg(c.Key), cast, b.arg(c.ID)))
	}
	size := cmp.Or(p.Page.Size, defaultPage)
	sql := fmt.Sprintf(`SELECT %s, (%s)::text FROM item WHERE %s ORDER BY %s %s, id %s LIMIT %d`,
		itemColumns, key, strings.Join(b.conds, " AND "), key, dir, dir, size+1)
	rows, err := s.pool.Query(ctx, sql, b.args...)
	if err != nil {
		return res, err
	}
	defer rows.Close()
	var lastKey string
	for rows.Next() {
		if len(res.Items) == size {
			next, _ := json.Marshal(cursor{Sort: sort, Key: lastKey, ID: res.Items[size-1].ID})
			res.Next = base64.RawURLEncoding.EncodeToString(next)
			break
		}
		var it protocol.CatalogItem
		if err := rows.Scan(append(itemDest(&it), &lastKey)...); err != nil {
			return res, err
		}
		res.Items = append(res.Items, it)
	}
	return res, rows.Err()
}

// filter adds one declared filter. CheckSearch has already checked the
// value's shape, and Open the filter's field.
func (s *Store) filter(b *query, name string, raw json.RawMessage) {
	f := s.cfg.Filters[slices.IndexFunc(s.cfg.Filters, func(f Filter) bool { return f.Name == name })]
	switch f.Type {
	case protocol.FilterText:
		var v string
		_ = json.Unmarshal(raw, &v)
		if f.Field == "tag" {
			b.where("tags @> ARRAY[" + b.arg(strings.ToLower(v)) + "::text]")
			return
		}
		match, _ := json.Marshal(map[string][]string{strings.TrimPrefix(f.Field, "attrs."): {v}})
		b.where("attrs @> " + b.arg(string(match)) + "::jsonb")
	case protocol.FilterDateRange:
		var r protocol.DateRange
		_ = json.Unmarshal(raw, &r)
		if r.From != "" {
			b.where(f.Field + " >= " + b.arg(r.From) + "::date")
		}
		if r.To != "" {
			b.where(f.Field + " < " + b.arg(r.To) + "::date + 1")
		}
	}
}
```

Add `encoding/base64`, `slices`, and `strings` to the imports.

- [ ] **Step 4: Run the tests**

Run: `go test -v ./catalogkit/`
Expected: PASS. `c1` matches "smith" in its name (weight `A`) and `c2` only in its description (weight `C`), so `c1` ranks first.

- [ ] **Step 5: Commit**

```bash
git add catalogkit
git commit -m "feat(catalogkit): full-text search with filters and keyset paging"
```

### Task 9: `Similar`

**Files:**
- Modify: `catalogkit/read.go`, `catalogkit/read_test.go`

- [ ] **Step 1: Write the failing test**

Append to `catalogkit/read_test.go`:

```go
// setSimhash overwrites an item's simhash and its bands, to test distances
// exactly.
func setSimhash(t *testing.T, s *Store, id string, h int64) {
	t.Helper()
	_, err := s.pool.Exec(context.Background(), `UPDATE item SET text_simhash = $2, image_phash = NULL, lsh = $3 WHERE id = $1`,
		id, h, lsh(&h, nil))
	if err != nil {
		t.Fatal(err)
	}
}

func TestSimilar(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	h := int64(0x0123456789abcdef)
	near := h
	for b := range MaxDistance {
		near ^= 1 << (7 * b)
	}
	for id, sim := range map[string]int64{"same": h, "near": near, "far": near ^ 1<<(7*MaxDistance)} {
		put(t, s, Entry{ID: id, Kind: protocol.KindCard, Data: testCard(id, "Text for "+id+"."), Updated: t0})
		setSimhash(t, s, id, sim)
	}
	exact, err := s.Get(ctx, "far", "")
	if err != nil {
		t.Fatal(err)
	}

	res, err := s.Similar(ctx, protocol.CatalogSimilarParams{Kind: protocol.KindCard,
		Hashes: protocol.CatalogHashes{ContentHash: exact.Item.Hashes.ContentHash, TextSimhash: &h},
		MaxDistance: MaxDistance, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, it := range res.Items {
		ids = append(ids, it.ID)
	}
	// far matches on content_hash at distance 0; same is 0 bits away, near 8.
	if want := []string{"far", "same", "near"}; !slices.Equal(ids, want) {
		t.Fatalf("got %v, want %v", ids, want)
	}

	_, err = s.Similar(ctx, protocol.CatalogSimilarParams{Kind: protocol.KindCard, MaxDistance: MaxDistance + 1, Limit: 10})
	wantCode(t, err, protocol.CodeInvalidParams)
	_, err = s.Similar(ctx, protocol.CatalogSimilarParams{Kind: "preset", Limit: 10})
	wantCode(t, err, protocol.CodeInvalidParams)
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./catalogkit/ -run TestSimilar`
Expected: FAIL to compile: `s.Similar undefined`.

- [ ] **Step 3: Implement**

Append to `catalogkit/read.go`:

```go
// Similar answers catalog.similar: items of the kind with the same
// content_hash, or within MaxDistance bits by simhash or phash, closest first.
func (s *Store) Similar(ctx context.Context, p protocol.CatalogSimilarParams) (protocol.CatalogSimilarResult, error) {
	res := protocol.CatalogSimilarResult{Items: []protocol.CatalogItem{}}
	switch {
	case !slices.Contains(s.cfg.Kinds, p.Kind):
		return res, invalid(fmt.Errorf("kind %q is not declared", p.Kind))
	case p.Limit < 1 || p.Limit > protocol.MaxCatalogPage:
		return res, invalid(fmt.Errorf("limit %d must be 1 to %d", p.Limit, protocol.MaxCatalogPage))
	case p.MaxDistance < 0 || p.MaxDistance > MaxDistance:
		return res, invalid(fmt.Errorf("max_distance %d must be 0 to %d", p.MaxDistance, MaxDistance))
	}
	// least() skips the NULL a missing hash gives.
	rows, _ := s.pool.Query(ctx, `
		SELECT `+itemColumns+` FROM (
			SELECT *, least(
				CASE WHEN content_hash = $2 THEN 0 END,
				bit_count((text_simhash # $3)::bit(64)),
				bit_count((image_phash # $4)::bit(64))) AS distance
			FROM item
			WHERE kind = $1 AND NOT removed AND (content_hash = $2 OR lsh && $5::int4[])) m
		WHERE distance <= $6
		ORDER BY distance, id
		LIMIT $7`,
		p.Kind, p.Hashes.ContentHash, p.Hashes.TextSimhash, p.Hashes.ImagePhash,
		lsh(p.Hashes.TextSimhash, p.Hashes.ImagePhash), p.MaxDistance, p.Limit)
	items, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (protocol.CatalogItem, error) {
		var it protocol.CatalogItem
		err := r.Scan(itemDest(&it)...)
		return it, err
	})
	if err != nil {
		return res, err
	}
	res.Items = append(res.Items, items...)
	return res, nil
}
```

- [ ] **Step 4: Run the tests**

Run: `go test -race -v ./catalogkit/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add catalogkit
git commit -m "feat(catalogkit): near matches by content hash and LSH bands"
```

### Task 10: `folder` ingest

**Files:**
- Modify: `internal/pluginio/pluginio.go`, `card-import/import.go`
- Create: `folder/ingest.go`, `folder/ingest_test.go`, `folder/main.go` (the catalog config only, for now)

- [ ] **Step 1: Move `readIn` to `pluginio`**

Append to `internal/pluginio/pluginio.go`:

```go
// ReadIn reads the file name inside root, failing if it is over MaxInput.
func ReadIn(root *os.Root, name string) ([]byte, error) {
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return ReadAll(f)
}
```

In `card-import/import.go`, delete `readIn` and its comment, and change its one call to `pluginio.ReadIn(root, name)`.

Run: `go test ./internal/pluginio/ ./card-import/`
Expected: PASS.

- [ ] **Step 2: Write the failing tests**

Create `folder/ingest_test.go`:

```go
package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/inkandale/innkeeper-plugins/catalogkit"
	"github.com/inkandale/innkeeper-plugins/internal/pgtest"
	"github.com/inkandale/innkeeper/protocol"
)

// testEnv is a folder holding a PNG card, a JSON card, a lorebook, and a
// file that's neither, in subfolders.
func testEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	copyFile(t, "../card/testdata/v2-seraphina.png", filepath.Join(dir, "fantasy/elves/seraphina.png"))
	copyFile(t, "../card/testdata/v3-mirelle.json", filepath.Join(dir, "fantasy/mirelle.json"))
	copyFile(t, "../card/testdata/eldoria.lorebook.json", filepath.Join(dir, "eldoria.json"))
	writeFile(t, filepath.Join(dir, "notes.json"), `{"todo": true}`)
	store, err := catalogkit.Open(context.Background(), pgtest.URL(t), catalogConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	return &env{store: store, folder: dir, blobTmp: t.TempDir()}
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	b, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, to, string(b))
}

func writeFile(t *testing.T, path, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

// runIngest ingests from checkpoint and returns the counts and the last
// checkpoint sent.
func runIngest(t *testing.T, e *env, checkpoint string) (protocol.CatalogIngestResult, string) {
	t.Helper()
	var last string
	notify := func(method string, params any) error {
		if cp, ok := params.(protocol.Checkpoint); ok {
			last = string(cp.Checkpoint)
		}
		return nil
	}
	params, _ := json.Marshal(protocol.CatalogIngestParams{Run: "r", Checkpoint: json.RawMessage(checkpoint)})
	res, err := ingest(context.Background(), e, notify, params)
	if err != nil {
		t.Fatal(err)
	}
	return res, last
}

func ids(t *testing.T, e *env, p protocol.CatalogSearchParams) []string {
	t.Helper()
	p.Sort = "name"
	res, err := e.store.Search(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, it := range res.Items {
		out = append(out, it.ID)
	}
	return out
}

func TestIngest(t *testing.T) {
	e := testEnv(t)
	ctx := context.Background()

	res, last := runIngest(t, e, "")
	if want := (protocol.CatalogIngestResult{Added: 3, Skipped: 1}); res != want {
		t.Fatalf("first ingest = %+v, want %+v", res, want)
	}
	if last != `{"last":"notes.json"}` {
		t.Fatalf("last checkpoint = %s", last)
	}
	got := ids(t, e, protocol.CatalogSearchParams{Filters: map[string]json.RawMessage{"subdirectory": json.RawMessage(`"fantasy"`)}})
	if slices.Sort(got); !slices.Equal(got, []string{"fantasy/elves/seraphina.png", "fantasy/mirelle.json"}) {
		t.Fatalf("fantasy holds %v", got)
	}

	if res, _ := runIngest(t, e, ""); res != (protocol.CatalogIngestResult{}) {
		t.Fatalf("unchanged re-ingest = %+v, want nothing", res)
	}

	// Change a card, delete the lorebook.
	path := filepath.Join(e.folder, "fantasy/mirelle.json")
	b, _ := os.ReadFile(path)
	var card map[string]any
	_ = json.Unmarshal(b, &card)
	card["data"].(map[string]any)["description"] = "Changed."
	b, _ = json.Marshal(card)
	writeFile(t, path, string(b))
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
	eldoria, _ := os.ReadFile(filepath.Join(e.folder, "eldoria.json"))
	if err := os.Remove(filepath.Join(e.folder, "eldoria.json")); err != nil {
		t.Fatal(err)
	}
	if res, _ := runIngest(t, e, ""); res != (protocol.CatalogIngestResult{Updated: 1, Removed: 1}) {
		t.Fatalf("after a change and a delete = %+v", res)
	}
	changed, err := e.store.Get(ctx, "fantasy/mirelle.json", "")
	if err != nil || changed.Version != "2" || len(changed.Versions) != 2 {
		t.Fatalf("changed card: version %q of %v, err %v", changed.Version, changed.Versions, err)
	}
	if slices.Contains(ids(t, e, protocol.CatalogSearchParams{}), "eldoria.json") {
		t.Fatal("search still lists the deleted lorebook")
	}
	if _, err := e.store.Get(ctx, "eldoria.json", ""); err != nil {
		t.Fatalf("get of a removed item: %v", err)
	}

	// The same file coming back returns the item.
	writeFile(t, filepath.Join(e.folder, "eldoria.json"), string(eldoria))
	if res, _ := runIngest(t, e, ""); res != (protocol.CatalogIngestResult{Updated: 1}) {
		t.Fatalf("after restoring the lorebook = %+v", res)
	}
}

func TestIngestResumes(t *testing.T) {
	e := testEnv(t)
	// Files sort as eldoria.json, fantasy/elves/seraphina.png, fantasy/mirelle.json, notes.json.
	res, _ := runIngest(t, e, `{"last":"fantasy/elves/seraphina.png"}`)
	if want := (protocol.CatalogIngestResult{Added: 1, Skipped: 1}); res != want {
		t.Fatalf("resumed ingest = %+v, want %+v", res, want)
	}
}

func TestIngestNeedsAFolder(t *testing.T) {
	e := testEnv(t)
	e.folder = filepath.Join(e.folder, "missing")
	params, _ := json.Marshal(protocol.CatalogIngestParams{Run: "r"})
	_, err := ingest(context.Background(), e, func(string, any) error { return nil }, params)
	var rpc *protocol.Error
	if !errors.As(err, &rpc) || rpc.Code != protocol.CodeInvalidParams {
		t.Fatalf("err = %v, want -32602", err)
	}
}
```

Add `errors` to the file's imports.

- [ ] **Step 3: Run them to verify they fail**

Run: `go test ./folder/`
Expected: FAIL to compile: `undefined: env`, `undefined: catalogConfig`, `undefined: ingest`.

- [ ] **Step 4: Implement**

Create `folder/main.go` with the catalog config, `env`, and an empty `main`, which a `main` package needs to compile. Task 11 replaces `main`:

```go
// Command folder is a catalog plugin over a folder of character cards and
// lorebooks.
package main

import (
	"github.com/inkandale/innkeeper-plugins/catalogkit"
	"github.com/inkandale/innkeeper/protocol"
)

const version = "0.1.0"

var catalogConfig = catalogkit.Config{
	Kinds: []string{protocol.KindCard, protocol.KindLorebook},
	Filters: []catalogkit.Filter{
		{CatalogFilter: protocol.CatalogFilter{Name: "subdirectory", Label: "Subfolder", Type: protocol.FilterText}, Field: "attrs.dir"},
		{CatalogFilter: protocol.CatalogFilter{Name: "tag", Label: "Tag", Type: protocol.FilterText}, Field: "tag"},
		{CatalogFilter: protocol.CatalogFilter{Name: "modified", Label: "Date modified", Type: protocol.FilterDateRange}, Field: "updated"},
	},
	Ingest: true,
	Migrations: []catalogkit.Migration{{Name: "folder/1-file",
		SQL: `CREATE TABLE file (path text PRIMARY KEY, mtime timestamptz NOT NULL, size bigint NOT NULL)`}},
}

// env is what initialize set up.
type env struct {
	store   *catalogkit.Store // nil without a database_url
	folder  string            // config.folder
	blobTmp string
}

func main() {}
```

Create `folder/ingest.go`:

```go
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/inkandale/innkeeper-plugins/card"
	"github.com/inkandale/innkeeper-plugins/catalogkit"
	"github.com/inkandale/innkeeper-plugins/internal/pluginio"
	"github.com/inkandale/innkeeper/cardhash"
	"github.com/inkandale/innkeeper/protocol"
)

// checkpointEvery is how many files ingest reads between checkpoints.
const checkpointEvery = 100

type checkpoint struct {
	Last string `json:"last"`
}

func invalid(err error) error {
	return protocol.NewError(protocol.CodeInvalidParams, err.Error(), false)
}

// ingest answers catalog.ingest: it indexes every changed card and lorebook
// file, then marks items whose file is gone removed.
func ingest(ctx context.Context, e *env, notify func(string, any) error, raw json.RawMessage) (protocol.CatalogIngestResult, error) {
	var res protocol.CatalogIngestResult
	var p protocol.CatalogIngestParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return res, invalid(err)
	}
	var cp checkpoint
	if len(p.Checkpoint) > 0 && string(p.Checkpoint) != "null" {
		if err := json.Unmarshal(p.Checkpoint, &cp); err != nil {
			return res, invalid(fmt.Errorf("checkpoint: %w", err))
		}
	}
	root, err := os.OpenRoot(e.folder)
	if err != nil {
		return res, invalid(fmt.Errorf("config folder: %w", err))
	}
	defer root.Close()
	files, err := walk(root)
	if err != nil {
		return res, invalid(err)
	}
	// ponytail: if last is gone, the ingest starts over; unchanged files cost a stat each.
	start := slices.Index(files, cp.Last) + 1
	for i := start; i < len(files); i++ {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		change, skip, err := ingestFile(ctx, e.store, root, files[i])
		if err != nil {
			return res, err
		}
		switch {
		case skip != nil:
			res.Skipped++
			log.Printf("skipped %s: %v", files[i], skip)
		case change == catalogkit.Added:
			res.Added++
		case change == catalogkit.Updated:
			res.Updated++
		}
		if n := i + 1; n%checkpointEvery == 0 || n == len(files) {
			if err := notify(protocol.NotifyProgress, protocol.Progress{Run: p.Run, Done: int64(n), Total: int64(len(files))}); err != nil {
				return res, err
			}
			next, _ := json.Marshal(checkpoint{Last: files[i]})
			if err := notify(protocol.NotifyCheckpoint, protocol.Checkpoint{Run: p.Run, Checkpoint: next}); err != nil {
				return res, err
			}
		}
	}
	if res.Removed, err = e.store.MarkRemovedExcept(ctx, files); err != nil {
		return res, err
	}
	// A file that comes back unchanged must be read again to restore its item.
	_, err = e.store.Pool().Exec(ctx, `DELETE FROM file WHERE path <> ALL($1)`, files)
	return res, err
}

// walk lists the card and lorebook files under root in lexical order,
// skipping hidden folders and logging unreadable ones.
func walk(root *os.Root) ([]string, error) {
	files := []string{}
	err := fs.WalkDir(root.FS(), ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			if name == "." {
				return err
			}
			log.Printf("skipped %s: %v", name, err)
			return nil
		}
		if d.IsDir() && name != "." && strings.HasPrefix(d.Name(), ".") {
			return fs.SkipDir
		}
		ext := strings.ToLower(path.Ext(name))
		if d.Type().IsRegular() && (ext == ".png" || ext == ".json" || ext == ".charx") {
			files = append(files, name)
		}
		return nil
	})
	return files, err
}

// ingestFile indexes one file unless its mtime and size match the last
// ingest. skip is why a file that isn't a card or lorebook was passed over;
// err is a database error, which fails the ingest.
func ingestFile(ctx context.Context, store *catalogkit.Store, root *os.Root, name string) (change catalogkit.Change, skip, err error) {
	info, err := root.Stat(name)
	if err != nil {
		return 0, err, nil
	}
	mtime := modTime(info)
	var oldTime time.Time
	var oldSize int64
	err = store.Pool().QueryRow(ctx, `SELECT mtime, size FROM file WHERE path = $1`, name).Scan(&oldTime, &oldSize)
	if err == nil && oldTime.Equal(mtime) && oldSize == info.Size() {
		return catalogkit.Unchanged, nil, nil
	}
	b, err := pluginio.ReadIn(root, name)
	if err != nil {
		return 0, err, nil
	}
	e := catalogkit.Entry{ID: name, Updated: mtime, Attrs: map[string][]string{"dir": dirs(name)}}
	stem := strings.TrimSuffix(path.Base(name), path.Ext(name))
	// Lorebooks first: one with a name and description would pass as a V1 card.
	if book, berr := card.DecodeLorebook(b, stem); berr == nil {
		e.Kind, e.Data = protocol.KindLorebook, book
	} else if c, img, cerr := card.Decode(b); cerr != nil {
		skip = cerr
	} else {
		e.Kind, e.Data = protocol.KindCard, c
		if img != nil {
			if e.Image, err = cardhash.DecodeImageBytes(img); err != nil {
				log.Printf("%s: avatar: %v", name, err)
			}
		}
	}
	if skip == nil {
		change, err = store.Put(ctx, e)
		if errors.Is(err, catalogkit.ErrBadData) {
			skip = err
		} else if err != nil {
			return 0, nil, err
		}
	}
	_, err = store.Pool().Exec(ctx, `INSERT INTO file (path, mtime, size) VALUES ($1, $2, $3)
		ON CONFLICT (path) DO UPDATE SET mtime = excluded.mtime, size = excluded.size`, name, mtime, info.Size())
	return change, skip, err
}

// modTime is info's mtime as Postgres stores it, to the microsecond.
func modTime(info fs.FileInfo) time.Time {
	return info.ModTime().UTC().Truncate(time.Microsecond)
}

// dirs lists the folders above name, outermost first: a/b/c.png gives a and a/b.
func dirs(name string) []string {
	out := []string{}
	for d := path.Dir(name); d != "."; d = path.Dir(d) {
		out = append([]string{d}, out...)
	}
	return out
}
```

- [ ] **Step 5: Run the tests**

Run: `go test -race -v ./folder/`
Expected: PASS, none skipped.

- [ ] **Step 6: Commit**

```bash
git add internal/pluginio card-import folder
git commit -m "feat(folder): ingest a folder of cards and lorebooks into a catalog"
```

### Task 11: `folder` plugin process, manifest, and contract check

**Files:**
- Modify: `folder/main.go`, `index.toml`, `Makefile`, `.github/workflows/ci.yml`, `.gitignore` if it lists plugin binaries
- Create: `folder/main_test.go`, `folder/plugin.toml`, `folder/README.md`, `folder/check-config.json`, `folder/testdata/library/`

- [ ] **Step 1: Write the failing test**

Create `folder/main_test.go`:

```go
package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/inkandale/innkeeper/protocol"
)

func getParams(id, version string) json.RawMessage {
	b, _ := json.Marshal(protocol.CatalogGetParams{ID: id, Version: version, Avatar: true})
	return b
}

func TestGetAvatar(t *testing.T) {
	e := testEnv(t)
	runIngest(t, e, "")
	const id = "fantasy/elves/seraphina.png"

	res, err := get(context.Background(), e, getParams(id, ""))
	if err != nil {
		t.Fatal(err)
	}
	if res.Avatar == "" || filepath.Dir(res.Avatar) != e.blobTmp {
		t.Fatalf("avatar = %q, want a file in %s", res.Avatar, e.blobTmp)
	}

	// After the file changes, the stored version's avatar is gone.
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(filepath.Join(e.folder, id), later, later); err != nil {
		t.Fatal(err)
	}
	if res, err = get(context.Background(), e, getParams(id, "")); err != nil || res.Avatar != "" {
		t.Fatalf("avatar = %q, err %v; want none", res.Avatar, err)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./folder/ -run TestGetAvatar`
Expected: FAIL to compile: `undefined: get`.

- [ ] **Step 3: Implement the process**

Replace the imports in `folder/main.go`, replace the empty `main`, and append `get`, `currentAvatar`, and `errNoDatabase`:

```go
import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"sync/atomic"
	"time"

	"github.com/inkandale/innkeeper-plugins/card"
	"github.com/inkandale/innkeeper-plugins/catalogkit"
	"github.com/inkandale/innkeeper-plugins/internal/pluginio"
	"github.com/inkandale/innkeeper/protocol"
)
```

```go
func main() {
	// Set by initialize; requests run on other goroutines.
	var cur atomic.Pointer[env]
	var conn *protocol.Conn
	handler := func(ctx context.Context, method string, params json.RawMessage) (any, error) {
		switch method {
		case protocol.MethodInitialize:
			var p protocol.InitializeParams
			if err := json.Unmarshal(params, &p); err != nil {
				return nil, invalid(err)
			}
			e := &env{blobTmp: p.BlobTmp}
			e.folder, _ = p.Config["folder"].(string)
			if p.DatabaseURL != "" {
				var err error
				if e.store, err = catalogkit.Open(ctx, p.DatabaseURL, catalogConfig); err != nil {
					return nil, err
				}
			}
			cur.Store(e)
			return protocol.InitializeResult{Protocol: protocol.Version, Name: "folder", Version: version,
				Capabilities: []string{"catalog"}}, nil
		case protocol.MethodShutdown:
			if e := cur.Load(); e != nil && e.store != nil {
				e.store.Close()
			}
			return nil, nil
		case protocol.MethodCatalogDescribe:
			return catalogkit.Describe(catalogConfig), nil
		case protocol.MethodCatalogSearch, protocol.MethodCatalogGet, protocol.MethodCatalogSimilar, protocol.MethodCatalogIngest:
		default:
			return nil, protocol.MethodNotFound(method)
		}
		e := cur.Load()
		if e == nil {
			return nil, fmt.Errorf("%s before initialize", method)
		}
		// Search checks its params first, so plugin check's filter check
		// passes without a database too.
		if method == protocol.MethodCatalogSearch {
			var p protocol.CatalogSearchParams
			if err := json.Unmarshal(params, &p); err != nil {
				return nil, invalid(err)
			}
			if err := catalogkit.Describe(catalogConfig).CheckSearch(p); err != nil {
				return nil, invalid(err)
			}
			if e.store == nil {
				return nil, errNoDatabase
			}
			return e.store.Search(ctx, p)
		}
		if e.store == nil {
			return nil, errNoDatabase
		}
		switch method {
		case protocol.MethodCatalogGet:
			return get(ctx, e, params)
		case protocol.MethodCatalogSimilar:
			var p protocol.CatalogSimilarParams
			if err := json.Unmarshal(params, &p); err != nil {
				return nil, invalid(err)
			}
			return e.store.Similar(ctx, p)
		}
		if e.folder == "" {
			return nil, invalid(errors.New("set folder in this instance's config before ingesting"))
		}
		return ingest(ctx, e, conn.Notify, params)
	}
	conn = protocol.NewConn(os.Stdin, os.Stdout, handler)
	if err := conn.Run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

var errNoDatabase = errors.New("no database: the core sends database_url to a plugin whose manifest sets database = true")

// get answers catalog.get. The avatar comes from the file, so only the
// latest version of an unchanged file has one.
func get(ctx context.Context, e *env, raw json.RawMessage) (protocol.CatalogGetResult, error) {
	var p protocol.CatalogGetParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return protocol.CatalogGetResult{}, invalid(err)
	}
	res, err := e.store.Get(ctx, p.ID, p.Version)
	if err != nil || !p.Avatar || res.Item.Kind != protocol.KindCard || res.Version != res.Item.LatestVersion || e.folder == "" {
		return res, err
	}
	img, err := currentAvatar(ctx, e, p.ID)
	if err != nil {
		log.Printf("%s: avatar: %v", p.ID, err)
		return res, nil
	}
	if img != nil {
		res.Avatar, err = pluginio.WriteTmp(e.blobTmp, "avatar-*.png", img)
	}
	return res, err
}

// currentAvatar reads id's avatar if the file is unchanged since ingest, or
// returns nil.
func currentAvatar(ctx context.Context, e *env, id string) ([]byte, error) {
	if !fs.ValidPath(id) {
		return nil, nil
	}
	root, err := os.OpenRoot(e.folder)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	info, err := root.Stat(id)
	if err != nil {
		return nil, err
	}
	var mtime time.Time
	var size int64
	if err := e.store.Pool().QueryRow(ctx, `SELECT mtime, size FROM file WHERE path = $1`, id).Scan(&mtime, &size); err != nil {
		return nil, err
	}
	if !mtime.Equal(modTime(info)) || size != info.Size() {
		return nil, nil
	}
	b, err := pluginio.ReadIn(root, id)
	if err != nil {
		return nil, err
	}
	_, img, err := card.Decode(b)
	return img, err
}
```

Run: `go test -race ./folder/`
Expected: PASS.

- [ ] **Step 4: Add the manifest, index entry, and contract-check data**

Create `folder/plugin.toml`:

```toml
name = "folder"
version = "0.1.0"
protocol = 1
command = ["./folder"]
capabilities = ["catalog"]
database = true

[config]
type = "object"

[config.properties.folder]
type = "string"
title = "Folder"
description = "Absolute path of the folder to index, as the core sees it, such as /archive."
```

Append to `index.toml`:

```toml

[[plugin]]
name = "folder"
category = "Archives"
path = "folder"
version = "0.1.0"
protocol = 1
capabilities = ["catalog"]
description = "Search and adopt cards and lorebooks from a folder, without importing them all."
```

Create the contract-check library and its config. The plugin runs in its own folder, so the relative path resolves there:

```bash
mkdir -p folder/testdata/library
cp card/testdata/v2-seraphina.png card/testdata/v3-mirelle.json card/testdata/eldoria.lorebook.json folder/testdata/library/
printf '{"folder": "testdata/library"}\n' > folder/check-config.json
```

If `.gitignore` lists each plugin binary, add `folder/folder` the same way.

- [ ] **Step 5: Give each plugin its own check config**

In `Makefile`, replace the `check` recipe:

```make
# Runs the contract kit from the core version go.mod pins. A plugin's
# check-config.json, if present, is its instance config for the check.
check: build
	for p in $(PLUGINS); do INNKEEPER_PLUGIN_CONFIG="$$(cat $$p/check-config.json 2>/dev/null)" go tool innkeeper plugin check $$p || exit 1; done
```

- [ ] **Step 6: Run the contract check with and without a database**

Run: `make check`
Expected: every plugin passes. For `folder`, the search, get, and similar checks pass by refusing without a database, and no ingest check runs.

Run: `INNKEEPER_PLUGIN_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres make check`
Expected: every plugin passes, and `folder` lists `catalog.ingest completes` among its passing checks. This leaves `item`, `item_version`, `file`, `migration`, and `tags_text` in that database's `public` schema; drop them when done:

```bash
psql postgres://postgres:postgres@localhost:55432/postgres -c 'DROP TABLE IF EXISTS item_version, item, file, migration; DROP FUNCTION IF EXISTS tags_text'
```

- [ ] **Step 7: Give CI a database**

In `.github/workflows/ci.yml`, add to the `test` job, beside `runs-on`:

```yaml
    services:
      postgres:
        image: postgres:18
        env:
          POSTGRES_PASSWORD: postgres
        ports:
          - 5432:5432
        options: >-
          --health-cmd pg_isready --health-interval 5s --health-timeout 5s --health-retries 10
    env:
      INNKEEPER_TEST_DATABASE_URL: postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable
      INNKEEPER_PLUGIN_DATABASE_URL: postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable
```

`publish.yml` keeps running `make check` without a database, which passes the same way the first run in Step 6 did.

- [ ] **Step 8: Write the README**

Create `folder/README.md`:

```markdown
# folder

A catalog over a folder of character cards and lorebooks: PNG, JSON, and
CharX cards, and lorebook JSON. Innkeeper searches it and adopts single
items into the library without importing the whole folder.

## Setup

Set **Folder** to the folder's path as the core sees it, such as `/archive`,
then start an ingest from the Archive page. Run another ingest after the
folder changes.

## Behavior

- An item's ID is its path inside the folder.
- A changed file adds a version. A deleted file marks its item removed:
  search stops listing it, but cards adopted from it keep their source.
- Only the latest version of an unchanged file has an avatar. After a file
  changes, ingest again to adopt it with its image.
- Hidden folders, and files that aren't cards or lorebooks, are skipped.
```

- [ ] **Step 9: Run everything CI runs**

```bash
go vet ./...
go test -race ./...
go run ./internal/indexcheck
make check
```

Expected: all pass, with the database tests run, not skipped.

- [ ] **Step 10: Commit**

```bash
git add folder index.toml Makefile .github/workflows/ci.yml .gitignore
git commit -m "feat(folder): the folder catalog plugin"
```

### Task 12: Update the spec

**Files:**
- Modify (in `innkeeper`): `docs/superpowers/specs/2026-10-09-archive-catalog-design.md`

Do this in the `innkeeper` worktree `../innkeeper-inn-37c` if Part A hasn't merged, or a new `inn-37c-spec` worktree off main if it has.

- [ ] **Step 1: Edit the spec**

Make these edits with the Edit tool, keeping the spec's style:

- `catalogkit` list: replace "The `item`, `item_version`, and `lsh_bucket` tables" with "The `item` and `item_version` tables", and replace the LSH bullet with: "LSH bands over `text_simhash` and `image_phash` in an `lsh` array column with a GIN index. Each hash splits into 9 bands of 7 bits, so hashes within 8 bits share a band. `catalog.similar` refuses a `max_distance` over 8."
- `catalogkit` list: add "Tags stored and filtered in lowercase."
- `folder` section: replace "**Filters:** kind, subdirectory, tag, and date modified." with "**Filters:** subdirectory, tag, and date modified. `catalog.search` takes `kind` already."
- `folder` section: after the Item ID bullet, add "**Versions:** numbered 1, 2, 3, each dated by the file's modification time. Only the latest version of a file unchanged since ingest has an avatar, read from the file."
- Testing, Contract kit bullet: add "When a catalog ingests and the check has a config, the kit runs `catalog.ingest` first."

- [ ] **Step 2: Commit**

```bash
git add docs/superpowers/specs/2026-10-09-archive-catalog-design.md
git commit -m "docs: describe catalogkit and folder as built"
```

## Leaves for later plans

- **37d (`chub-archive`)** extends `catalogkit`: number-range and enum filters, `author` text filters, plugin-given versions (the dump's `increment`) and author and tag overrides on `Entry`, and its own avatar storage.
- Ingest counts cover only the files read since the last checkpoint, so a resumed ingest reports less than the whole run did.
