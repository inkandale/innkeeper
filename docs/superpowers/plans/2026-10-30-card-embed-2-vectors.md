# Card embeddings 2: vectors as enrichments, pgvector optional

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Enrich plugins produce card vectors; core stores them and ranks Similar and semantic search by them; Innkeeper starts and works without pgvector.

**Architecture:** A new enrichment type, `vector`, carries `{space, vector}`. The enrich job writes such values to `card_embedding`, which core creates at startup only when pgvector is available. `vector.query` becomes a service method that enrich instances can offer, so the settings picker, validation and calls reuse the `ai.embed` service path (`InstanceOffering`, `/instances?offers=`). `internal/embed` and the `embed = true` flag go away.

**Tech stack:** Go, pgx, PostgreSQL with optional pgvector, huma, React and TanStack Query.

**Spec:** [2026-10-30-card-embed-design.md](../specs/2026-10-30-card-embed-design.md). Plan 1 must be merged first. Plan 3 (updating as cards change) and plan 4 (the `card-embed` plugin) build on this one.

---

## Conventions

- Work in `~/git/innkeeper-card-embed` (branch `card-embed-spec`).
- Database tests need
  `INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres`
  (pgvector image, see `CONTRIBUTING.md`). Without it they skip, and a skip
  is not a pass. Below, `$DB` stands for that assignment.
- After any API change, run `cd web && npm run gen`. Never hand-edit
  `web/openapi.json` or `web/src/api/schema.d.ts`.
- Comments say why, in one line. Follow `AGENTS.md`.

## Names

One name per concept, used everywhere below:

| Name | Meaning |
|---|---|
| vector instance | The enrich instance whose vectors Similar and search read. Setting key `vector_instance`. |
| `space` | A string the plugin derives from its models and prompt. Vectors compare only within one space. |
| `protocol.VectorValue` | `{"space": "...", "vector": [...]}`, the value of a `vector` enrichment and the result of `vector.query`. |
| vectors available | pgvector is installed and `card_embedding` exists. |

## Files

| File | Change |
|---|---|
| `protocol/enrich.go` | `vector` type check, `VectorValue`, `MethodVectorQuery`, `VectorQueryParams` |
| `protocol/manifest.go` | `vector` in `enrichmentTypes`; at most one vector key; `embed` ignored |
| `protocol/ai.go` | `vector.query` in `Services` and `ServiceSamples` |
| `protocol/messages.go` | `InitializeParams.Features`, `FeatureVectors` |
| `protocol/*_test.go` | Tests for the above |
| `internal/db/migrations/0045_card_embedding.sql`, `0051_embedding_enrichment.sql` | Emptied |
| `internal/db/migrations/0052_optional_vectors.sql` | Drops the old table and setting |
| `internal/db/vectors.go` | `EnableVectors`, `VectorsAvailable`, `ErrNoVectors` |
| `internal/db/vectors_test.go` | Tests |
| `internal/testdb/testdb.go` | Enable vectors in test databases |
| `cmd/innkeeper/services.go` | Enable vectors at startup |
| `internal/library/embeddings.go` | Rewritten: `NewVector`, `SaveVectors`, `HasVectors` |
| `internal/library/discover.go`, `search.go`, `library.go` | Compare by space; vectors-off fallbacks; errors |
| `internal/library/*_test.go` | Follow the changes |
| `internal/plugins/plugins.go` | `Features` in initialize; `VectorQuery`; `VectorInstance`; drop `Embed` |
| `internal/enrich/enrich.go` | Store vector values; drop the embed queue |
| `internal/embed/` | Deleted |
| `internal/api/settings.go`, `search.go`, `discover.go`, `jobs.go`, `server.go` | `vector_instance`, `vectors_available`, query via the plugin |
| `internal/importer/importer.go`, `cmd/innkeeper/worker.go` | Drop the embed job |
| `internal/fakeplugin/` , `internal/testutil/fakeplugin.go` | Vector mode for tests |
| `internal/mcp/tools.go` | Doc string |
| `web/src/settings/EmbedFrom.tsx` → `VectorsFrom.tsx` | Picker; pgvector note; no Embed now |
| `web/src/settings/General.tsx`, `Libraries.tsx`, `web/src/cards/Cards.tsx` | Follow the rename |
| `docs/running.md`, `docs/protocol.md` | pgvector optional; vector type; features; `vector.query` |

---

### Task 1: Protocol: the `vector` type, `vector.query`, and features

**Files:** `protocol/enrich.go`, `protocol/manifest.go`, `protocol/ai.go`,
`protocol/messages.go`, `protocol/enrich_test.go`, `protocol/manifest_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `protocol/enrich_test.go` (create the file with `package protocol`
and imports `encoding/json`, `strings`, `testing` if it doesn't exist):

```go
func TestVectorEnrichmentCheck(t *testing.T) {
	d := Enrichment{Type: "vector", Label: "Embedding"}
	for value, ok := range map[string]bool{
		`{"space": "m1", "vector": [0.5, -1]}`: true,
		`{"space": "", "vector": [1]}`:         false,
		`{"space": "m1", "vector": []}`:        false,
		`{"space": "m1"}`:                      false,
		`[1, 2]`:                               false,
		`"text"`:                               false,
	} {
		if err := d.Check(json.RawMessage(value)); (err == nil) != ok {
			t.Errorf("Check(%s) = %v, want ok %v", value, err, ok)
		}
	}
	long := `{"space": "` + strings.Repeat("x", 201) + `", "vector": [1]}`
	if d.Check(json.RawMessage(long)) == nil {
		t.Error("a 201-character space passed")
	}
}
```

Append to `protocol/manifest_test.go`, following how the file's other
validation tests build a manifest (find the helper that returns a valid
enrich manifest and reuse it; the test below calls it `enrichManifest`):

```go
func TestManifestAllowsOneVectorKey(t *testing.T) {
	m := enrichManifest()
	m.Enrichments = map[string]Enrichment{"embedding": {Type: "vector", Label: "Embedding"}}
	if err := m.Validate(); err != nil {
		t.Fatalf("one vector key: %v", err)
	}
	m.Enrichments["other"] = Enrichment{Type: "vector", Label: "Other"}
	if err := m.Validate(); err == nil || !strings.Contains(err.Error(), "at most one vector") {
		t.Fatalf("two vector keys: %v", err)
	}
}

func TestManifestStillLoadsTheRetiredEmbedFlag(t *testing.T) {
	m := enrichManifest()
	m.Enrichments = map[string]Enrichment{"premise": {Type: "text", Label: "Premise", Embed: true}}
	if err := m.Validate(); err != nil {
		t.Fatalf("embed on a text key: %v", err)
	}
}
```

If the file names its helper and validate method differently, use those
names; don't add a second helper.

- [ ] **Step 2: Run them and see them fail**

```bash
go test ./protocol -run 'TestVectorEnrichmentCheck|TestManifestAllowsOneVectorKey|TestManifestStillLoadsTheRetiredEmbedFlag'
```

Expected: FAIL: `unknown type "vector"`.

- [ ] **Step 3: Add the type and its value**

In `protocol/enrich.go`, next to `MaxEnrichValue`:

```go
// MaxSpace is the longest vector space name.
const MaxSpace = 200

// VectorValue is a vector enrichment's value and vector.query's result.
// Vectors compare only within one Space, which the plugin derives from
// the models and prompt that made them.
type VectorValue struct {
	Space  string    `json:"space"`
	Vector []float32 `json:"vector"`
}

// Validate reports a value core can't store or rank.
func (v VectorValue) Validate() error {
	switch {
	case v.Space == "" || len(v.Space) > MaxSpace:
		return fmt.Errorf("space must be 1 to %d characters", MaxSpace)
	case len(v.Vector) == 0:
		return errors.New("vector is empty")
	}
	return nil
}

// MethodVectorQuery asks a vector instance for a search query's vector, in
// the space of the vectors it gives cards.
const MethodVectorQuery = "vector.query"

// VectorQueryParams is vector.query's input.
type VectorQueryParams struct {
	ServiceCall
	Text string `json:"text"`
}
```

In `Enrichment.Check`, add a case before `default`:

```go
	case "vector":
		var vv VectorValue
		if err := json.Unmarshal(v, &vv); err != nil {
			return errors.New("it must be an object with space and vector")
		}
		return vv.Validate()
```

- [ ] **Step 4: Accept it in manifests**

In `protocol/manifest.go`:

```go
var enrichmentTypes = []string{"text", "score", "list", "vector"}
```

Replace the `Embed` field's comment:

```go
	// Embed is retired and ignored; kept so manifests that still set it load.
	Embed bool `toml:"embed"`
```

Delete the `if e.Embed && e.Type != "text" ...` check in validation. After
the `grids` loop, add:

```go
	vectors := 0
	for _, e := range m.Enrichments {
		if e.Type == "vector" {
			vectors++
		}
	}
	// One vector key per plugin, so vector.query has one space to answer in.
	if vectors > 1 {
		errs = append(errs, errors.New("at most one vector enrichment"))
	}
```

- [ ] **Step 5: Make `vector.query` a service and add features**

In `protocol/ai.go`:

```go
var Services = map[string]string{MethodAIChat: CapabilityAI, MethodAIEmbed: CapabilityAI, MethodAIDecide: CapabilityAI,
	MethodVectorQuery: CapabilityEnrich}
```

and in `ServiceSamples`:

```go
	MethodVectorQuery: {VectorQueryParams{Text: "OK"}, decoded(func(r VectorValue) error { return r.Validate() })},
```

In `protocol/messages.go`, add to `InitializeParams`:

```go
	// Features are the optional core features this install has, such as
	// FeatureVectors. A plugin skips work whose results core would drop.
	Features []string `json:"features,omitempty"`
```

and below the struct:

```go
// FeatureVectors means core stores vector enrichments: pgvector is installed.
const FeatureVectors = "vectors"
```

- [ ] **Step 6: Run the protocol tests**

```bash
go test ./protocol/...
```

Expected: PASS. If an existing test asserted that `embed` on a score key is
rejected, delete that test: the flag is retired.

- [ ] **Step 7: Commit**

```bash
git add protocol
git commit -m "feat(protocol): add vector enrichments, vector.query, and init features"
```

---

### Task 2: Optional pgvector

**Files:** `internal/db/migrations/0045_card_embedding.sql`,
`internal/db/migrations/0051_embedding_enrichment.sql`, create
`internal/db/migrations/0052_optional_vectors.sql`, `internal/db/vectors.go`,
`internal/db/vectors_test.go`, `internal/testdb/testdb.go`,
`cmd/innkeeper/services.go`

- [ ] **Step 1: Write the failing tests**

`internal/db/vectors_test.go`:

```go
package db_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/inkandale/innkeeper/internal/db"
	"github.com/inkandale/innkeeper/internal/testdb"
)

func TestEnableVectorsCreatesTheTable(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	if err := db.EnableVectors(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := db.EnableVectors(ctx, pool); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if ok, err := db.VectorsAvailable(ctx, pool); err != nil || !ok {
		t.Fatalf("available = %v, %v", ok, err)
	}
}

// A role that may not create extensions stands in for PostgreSQL without
// pgvector: both make CREATE EXTENSION fail.
func TestEnableVectorsWithoutPgvectorTurnsThemOff(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `DROP TABLE IF EXISTS card_embedding; DROP EXTENSION IF EXISTS vector`); err != nil {
		t.Fatal(err)
	}
	role := fmt.Sprintf("novec%d", time.Now().UnixNano())
	if _, err := pool.Exec(ctx, fmt.Sprintf(`CREATE ROLE %s LOGIN PASSWORD 'x'; GRANT ALL ON SCHEMA public TO %s`, role, role)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Exec(context.Background(), "DROP OWNED BY "+role+"; DROP ROLE "+role) })
	cfg, err := pgxpool.ParseConfig(os.Getenv("INNKEEPER_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.User, cfg.ConnConfig.Password = role, "x"
	cfg.ConnConfig.Database = pool.Config().ConnConfig.Database
	plain, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(plain.Close)

	if err := db.EnableVectors(ctx, plain); !errors.Is(err, db.ErrNoVectors) {
		t.Fatalf("EnableVectors = %v, want ErrNoVectors", err)
	}
	if ok, err := db.VectorsAvailable(ctx, plain); err != nil || ok {
		t.Fatalf("available = %v, %v", ok, err)
	}
}
```

`testdb.New` skips without `INNKEEPER_TEST_DATABASE_URL`, so these do too.

- [ ] **Step 2: Run them and see them fail**

```bash
$DB go test ./internal/db -run TestEnableVectors -count=1
```

Expected: FAIL: `undefined: db.EnableVectors`.

- [ ] **Step 3: Take vectors out of the numbered migrations**

Replace the whole of `internal/db/migrations/0045_card_embedding.sql` with:

```sql
-- card_embedding is created at startup by db.EnableVectors, only when
-- pgvector is available. Installs that ran this file keep its name applied.
SELECT 1;
```

Replace the whole of `internal/db/migrations/0051_embedding_enrichment.sql`
with:

```sql
-- Superseded by db.EnableVectors; see 0045.
SELECT 1;
```

Create `internal/db/migrations/0052_optional_vectors.sql`:

```sql
-- Vectors now come from enrich instances, not an ai instance, so the old
-- rows and setting can't be read. EnableVectors recreates the table.
DROP TABLE IF EXISTS card_embedding;
DELETE FROM setting WHERE key = 'embed_instance';
```

Check the settings table is named `setting`:
`grep -rn "INSERT INTO setting" internal/plugins` should hit.

- [ ] **Step 4: Write `EnableVectors`**

`internal/db/vectors.go`:

```go
package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNoVectors means pgvector isn't available, so vector features are off.
var ErrNoVectors = errors.New("pgvector is not available; similar cards rank by tags and semantic search is off")

// EnableVectors creates the pgvector extension, if it can, and the table
// card vectors live in. It returns ErrNoVectors, wrapping the reason, when
// the extension can't be created; the caller logs it and carries on.
func EnableVectors(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, `CREATE EXTENSION IF NOT EXISTS vector`); err != nil {
		return fmt.Errorf("%w: %v", ErrNoVectors, err)
	}
	// No fixed dimension: a vector's space says which model made it.
	_, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS card_embedding (
		card_id bigint NOT NULL REFERENCES card (id) ON DELETE CASCADE DEFERRABLE INITIALLY IMMEDIATE,
		instance_id bigint NOT NULL REFERENCES plugin_instance ON DELETE CASCADE DEFERRABLE INITIALLY IMMEDIATE,
		revision_id bigint NOT NULL,
		space text NOT NULL,
		embedding vector NOT NULL,
		PRIMARY KEY (instance_id, card_id),
		FOREIGN KEY (revision_id, card_id) REFERENCES card_revision (id, card_id) ON DELETE CASCADE DEFERRABLE INITIALLY IMMEDIATE
	)`)
	if err != nil {
		return fmt.Errorf("create card_embedding: %w", err)
	}
	return nil
}

// VectorsAvailable reports whether EnableVectors has succeeded on this
// database.
func VectorsAvailable(ctx context.Context, pool *pgxpool.Pool) (bool, error) {
	var ok bool
	err := pool.QueryRow(ctx, `SELECT to_regclass('card_embedding') IS NOT NULL`).Scan(&ok)
	return ok, err
}
```

- [ ] **Step 5: Call it at startup and in test databases**

In `cmd/innkeeper/services.go`, `openDB` takes the logger. Change its
signature to `openDB(ctx context.Context, log *slog.Logger, maxConnsAtLeast int)`,
update its one caller in `openServices` to pass `log`, and after
`db.Migrate` succeeds:

```go
	if err := db.EnableVectors(ctx, pool); errors.Is(err, db.ErrNoVectors) {
		log.Warn("vector features are off; install pgvector, or have an admin run CREATE EXTENSION vector, then restart", "err", err)
	} else if err != nil {
		pool.Close()
		return nil, err
	}
```

In `internal/testdb/testdb.go`, after `db.Migrate(ctx, pool)` succeeds:

```go
	// The test image has pgvector, as the old migration assumed.
	if err := db.EnableVectors(ctx, pool); err != nil {
		t.Fatal(err)
	}
```

- [ ] **Step 6: Run the tests**

```bash
$DB go test ./internal/db -count=1
go build ./...
```

Expected: PASS, and the build succeeds.

- [ ] **Step 7: Commit**

```bash
git add internal/db internal/testdb cmd/innkeeper/services.go
git commit -m "feat(db): create card vectors at startup only when pgvector is available"
```

---

### Task 3: Library: store vectors and compare by space

**Files:** `internal/library/embeddings.go`, `internal/library/library.go`,
`internal/library/discover.go`, `internal/library/search.go`,
`internal/library/embeddings_test.go`, `internal/library/discover_test.go`,
`internal/library/search_test.go`

- [ ] **Step 1: Rewrite the test helper and add space tests**

In `internal/library/discover_test.go`, replace `embedCards` with:

```go
// embedCards saves vecs[i] as card i's vector from instance in space "s1",
// from each card's head.
func embedCards(t *testing.T, lib *Library, instance int64, vecs map[int64][]float32) {
	t.Helper()
	embedCardsIn(t, lib, instance, "s1", vecs)
}

func embedCardsIn(t *testing.T, lib *Library, instance int64, space string, vecs map[int64][]float32) {
	t.Helper()
	ctx := context.Background()
	var vals []NewVector
	for id, v := range vecs {
		c, err := lib.Cards.Get(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		vals = append(vals, NewVector{CardID: id, RevisionID: c.HeadRevisionID, Space: space, Vector: v})
	}
	if err := lib.SaveVectors(ctx, instance, vals); err != nil {
		t.Fatal(err)
	}
}
```

Add:

```go
func TestSimilarComparesOnlyWithinASpace(t *testing.T) {
	lib := newLibrary(t)
	inst := newInstanceRow(t, lib)
	me, _ := newCard(t, lib, "me", `{"name": "Me"}`)
	same, _ := newCard(t, lib, "same", `{"name": "Same"}`)
	other, _ := newCard(t, lib, "other", `{"name": "Other"}`)
	embedCardsIn(t, lib, inst, "s1", map[int64][]float32{me: {1, 0}, same: {0, 1}})
	embedCardsIn(t, lib, inst, "s2", map[int64][]float32{other: {1, 0, 0}})

	got, err := lib.SimilarCards(context.Background(), me, SimilarOptions{Instance: inst})
	if err != nil {
		t.Fatal(err)
	}
	if got.Basis != "embedding" || len(got.Items) != 1 || got.Items[0].CardID != same {
		t.Fatalf("similar = %+v", got)
	}
}
```

`newInstanceRow` and `newCard` already exist in this package's tests; if
`newInstanceRow` lives in `embeddings_test.go`, keep it there.

Replace `internal/library/embeddings_test.go`'s tests of `Unembedded`,
`SaveEmbeddings` and `ClearEmbeddings` with one test of `SaveVectors`:

```go
func TestSaveVectorsReplacesACardsVector(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	inst := newInstanceRow(t, lib)
	id, rev := newCard(t, lib, "brakka", `{"name": "Brakka"}`)
	for _, v := range [][]float32{{1, 0}, {0, 1}} {
		if err := lib.SaveVectors(ctx, inst, []NewVector{{CardID: id, RevisionID: rev, Space: "s1", Vector: v}}); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	var text string
	if err := lib.pool.QueryRow(ctx, `SELECT count(*), max(embedding::text) FROM card_embedding WHERE card_id = $1`, id).Scan(&n, &text); err != nil {
		t.Fatal(err)
	}
	if n != 1 || text != "[0,1]" {
		t.Fatalf("rows = %d, vector = %s", n, text)
	}
}
```

In `internal/library/search_test.go`, every literal
`Embedding: ..., EmbedInstance: ...` also gets `EmbedSpace: "s1"`, and
`TestSemanticSearchSkipsVectorsOfAnotherLength` becomes
`TestSemanticSearchSkipsVectorsOfAnotherSpace`: seed one card with
`embedCardsIn(t, lib, inst, "s2", ...)` and assert a query with
`EmbedSpace: "s1"` doesn't return it. Keep its other assertions.

- [ ] **Step 2: Run them and see them fail**

```bash
$DB go test ./internal/library -run 'TestSimilar|TestSaveVectors|TestSemantic' -count=1
```

Expected: compile errors: `undefined: NewVector`.

- [ ] **Step 3: Rewrite `embeddings.go`**

Replace the whole of `internal/library/embeddings.go`:

```go
package library

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/inkandale/innkeeper/internal/db"
)

// NewVector is one card's vector from an enrich instance, from the
// revision named.
type NewVector struct {
	CardID     int64
	RevisionID int64
	Space      string
	Vector     []float32
}

// SaveVectors stores each card's vector from instance, replacing the one it had.
func (l *Library) SaveVectors(ctx context.Context, instance int64, vals []NewVector) error {
	ids, revs, spaces, vecs := make([]int64, len(vals)), make([]int64, len(vals)), make([]string, len(vals)), make([]string, len(vals))
	for i, v := range vals {
		ids[i], revs[i], spaces[i], vecs[i] = v.CardID, v.RevisionID, v.Space, vectorText(v.Vector)
	}
	_, err := l.pool.Exec(ctx, `
		INSERT INTO card_embedding (instance_id, card_id, revision_id, space, embedding)
		SELECT $1, v.card_id, v.revision_id, v.space, v.embedding::vector
		FROM unnest($2::bigint[], $3::bigint[], $4::text[], $5::text[]) v (card_id, revision_id, space, embedding)
		ON CONFLICT (instance_id, card_id) DO UPDATE
		SET revision_id = excluded.revision_id, space = excluded.space, embedding = excluded.embedding`,
		instance, ids, revs, spaces, vecs)
	if err != nil {
		return fmt.Errorf("save %d vectors for instance %d: %w", len(vals), instance, err)
	}
	return nil
}

// HasVectors reports whether this install stores vectors. It is read once:
// enabling pgvector takes a restart.
func (l *Library) HasVectors(ctx context.Context) (bool, error) {
	l.vectorsOnce.Do(func() { l.vectors, l.vectorsErr = db.VectorsAvailable(ctx, l.pool) })
	return l.vectors, l.vectorsErr
}

// vectorText is v in pgvector's text form.
func vectorText(v []float32) string {
	parts := make([]string, len(v))
	for i, x := range v {
		parts[i] = strconv.FormatFloat(float64(x), 'g', -1, 32)
	}
	return "[" + strings.Join(parts, ",") + "]"
}
```

If `vectorText` is already defined elsewhere in the package (search for
`func vectorText`), delete this copy and keep the existing one. Remove the
`"sync"` import if the struct fields below end up in another file.

In `internal/library/library.go`, add to `Library`:

```go
	vectorsOnce sync.Once
	vectors     bool
	vectorsErr  error
```

(import `sync`), and replace `ErrNoEmbedInstance` with:

```go
	// ErrNoVectorInstance means vector_instance is unset, or names an instance that is gone or disabled.
	ErrNoVectorInstance = errors.New("semantic search needs a vector source; pick one in Settings under Plugins, Vectors from")
	// ErrNoVectors means pgvector isn't installed, so semantic search is off.
	ErrNoVectors = errors.New("semantic search needs pgvector; see docs/running.md")
```

- [ ] **Step 4: Compare by space in Similar**

In `internal/library/discover.go`, in `SimilarCards`'s embedding query,
replace the `q AS` CTE and the join:

```go
	sql := discoverWith + `, q AS (SELECT embedding, space FROM card_embedding WHERE instance_id = $2 AND card_id = $1)
		SELECT c.id, c.library_id, coalesce(r.data->>'name', ''), coalesce(r.avatar_blob, ''), coalesce(r.tags, '{}'), greatest(0, 1 - (e.embedding <=> q.embedding))
		FROM self CROSS JOIN q
		JOIN card_embedding e ON e.instance_id = $2 AND e.space = q.space
```

(the rest of the query is unchanged). In `similarBasis`, before the
`EXISTS` query:

```go
	if ok, err := l.HasVectors(ctx); err != nil || !ok {
		return "tags", err
	}
```

Update `SimilarPage.Basis`'s doc to: "embedding ranks by meaning; tags, by
shared tags, when there is no vector source or pgvector, or the card has no
vector yet." and `SimilarOptions.Instance`'s comment to "the vector
instance, or 0 to rank by tags".

- [ ] **Step 5: Compare by space in semantic search**

In `internal/library/search.go`, add to `Query` beside `EmbedInstance`:

```go
	// EmbedSpace is Embedding's space; only vectors in it are compared.
	EmbedSpace string `json:"-"`
```

In `semantic`, replace the returned condition:

```go
	return `EXISTS (SELECT 1 FROM card_embedding e WHERE e.card_id = c.id AND e.instance_id = ` + w.arg(q.EmbedInstance) +
		` AND e.space = ` + w.arg(q.EmbedSpace) + `)`, nil
```

and `similarity`:

```go
// similarity is a card's cosine similarity to q's vector, as a real so a
// cursor holds it exactly, or null for a vector in another space.
func (w *where) similarity(q Query) string {
	return `(SELECT (1 - (e.embedding <=> ` + w.arg(vectorText(q.Embedding)) + `::vector))::real
		FROM card_embedding e WHERE e.card_id = c.id AND e.instance_id = ` + w.arg(q.EmbedInstance) +
		` AND e.space = ` + w.arg(q.EmbedSpace) + `)`
}
```

Update `Query.Semantic`'s doc to say it "needs text and a vector source
(Settings, Plugins, Vectors from)".

- [ ] **Step 6: Run the library tests**

```bash
$DB go test ./internal/library -count=1
```

Expected: PASS. The build of other packages fails until Task 5; that's
expected.

- [ ] **Step 7: Commit**

```bash
git add internal/library
git commit -m "feat(library): store vectors by space and rank only within one"
```

---

### Task 4: Plugins: features, `vector.query`, and the vector instance

**Files:** `internal/plugins/plugins.go`, `internal/plugins/services_test.go`

- [ ] **Step 1: Write the failing tests**

In `internal/plugins/services_test.go`, replace the test of `Registry.Embed`
with tests of `VectorQuery` and `VectorInstance`. Follow the file's existing
setup for starting a fake instance that offers a service (it configures
`"services"` in the instance config); use `protocol.MethodVectorQuery` and
the `enrich` capability:

```go
func TestVectorQueryReturnsTheInstancesVector(t *testing.T) {
	r, mgr, id := newServiceInstance(t, []string{"enrich"}, protocol.MethodVectorQuery)
	got, err := r.VectorQuery(context.Background(), mgr, id, "abc")
	if err != nil || got.Space != "fake" || len(got.Vector) != 2 || got.Vector[0] != 3 {
		t.Fatalf("VectorQuery = %+v, %v", got, err)
	}
}

func TestVectorInstanceIgnoresAMissingOrDisabledInstance(t *testing.T) {
	r, _, id := newServiceInstance(t, []string{"enrich"}, protocol.MethodVectorQuery)
	ctx := context.Background()
	if err := r.SetSetting(ctx, plugins.VectorInstanceKey, strconv.FormatInt(id, 10)); err != nil {
		t.Fatal(err)
	}
	if got, err := r.VectorInstance(ctx); err != nil || got != id {
		t.Fatalf("set: %d, %v", got, err)
	}
	if err := r.SetDisabled(ctx, id, true); err != nil {
		t.Fatal(err)
	}
	if got, err := r.VectorInstance(ctx); err != nil || got != 0 {
		t.Fatalf("disabled: %d, %v", got, err)
	}
}
```

`newServiceInstance` stands for whatever helper the file uses to create a
fake instance with services; if none takes capabilities, extend the
existing one with a capabilities parameter rather than writing a second.
Use the registry's real method for disabling an instance (search
`plugins.go` for `Disabled`).

- [ ] **Step 2: Teach the fake plugin `vector.query`**

In `internal/fakeplugin/main.go`'s method switch, add:

```go
		case protocol.MethodVectorQuery:
			var p protocol.VectorQueryParams
			if err := json.Unmarshal(params, &p); err != nil {
				return nil, protocol.InvalidParams(err)
			}
			return protocol.VectorValue{Space: "fake", Vector: []float32{float32(len(p.Text)), 1}}, nil
```

Check `aiServices(init.Config)` returns `config["services"]` whatever the
capability; if it only does for `ai`, make it read the config for any
capability.

- [ ] **Step 3: Run them and see them fail**

```bash
$DB go test ./internal/plugins -run 'TestVectorQuery|TestVectorInstance' -count=1
```

Expected: FAIL: `r.VectorQuery undefined`.

- [ ] **Step 4: Implement**

In `internal/plugins/plugins.go`, replace `Embed` with:

```go
// VectorInstanceKey is the setting naming the enrich instance whose vectors
// Similar and semantic search read. One for the install, since vectors from
// different models can't be compared.
const VectorInstanceKey = "vector_instance"

// VectorInstance returns the vector instance, or 0 when none is set or it
// is gone or disabled.
func (r *Registry) VectorInstance(ctx context.Context) (int64, error) {
	v, err := r.Setting(ctx, VectorInstanceKey)
	if err != nil || v == "" {
		return 0, err
	}
	id, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("setting %s = %q: %w", VectorInstanceKey, v, err)
	}
	info, err := r.GetInstance(ctx, id)
	if errors.Is(err, ErrNotFound) || (err == nil && info.Disabled) {
		return 0, nil
	}
	return id, err
}

// VectorQuery returns instance id's vector for a search query. The
// instance must offer vector.query.
func (r *Registry) VectorQuery(ctx context.Context, mgr *pluginhost.Manager, id int64, text string) (protocol.VectorValue, error) {
	var res protocol.VectorValue
	if err := r.callService(ctx, mgr, id, protocol.MethodVectorQuery, protocol.VectorQueryParams{Text: text}, &res); err != nil {
		return res, err
	}
	if err := res.Validate(); err != nil {
		return res, fmt.Errorf("vector.query from instance %d: %w", id, err)
	}
	return res, nil
}
```

Then give every
plugin the install's features. In the function that builds
`pluginhost.Instance` (around line 715), add to `InitializeParams`:

```go
			Features:    r.features(ctx),
```

and add:

```go
// features are the optional core features plugins are told about.
func (r *Registry) features(ctx context.Context) []string {
	// ponytail: asks once per instance load; cache on the Registry if loads get hot.
	if ok, err := db.VectorsAvailable(ctx, r.pool); err == nil && ok {
		return []string{protocol.FeatureVectors}
	}
	return nil
}
```

importing `internal/db`. If `internal/db` importing creates a cycle, pass
the result in from `plugins.Open` instead.

- [ ] **Step 5: Run the tests**

```bash
$DB go test ./internal/plugins -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/plugins internal/fakeplugin
git commit -m "feat(plugins): call vector.query and tell plugins about vectors"
```

---

### Task 5: Enrich stores vectors; the embed job goes

**Files:** `internal/enrich/enrich.go`, `internal/embed/` (delete),
`internal/importer/importer.go`, `cmd/innkeeper/worker.go`,
`internal/api/jobs.go`, `internal/testutil/fakeplugin.go`,
`internal/fakeplugin/enrich.go`, `internal/api/enrichments_test.go`

- [ ] **Step 1: Give the fake enrich plugin a vector key**

In `internal/testutil/fakeplugin.go`, below `FakeEnrichments`:

```go
// FakeVectorEnrichment is the vector key the fake plugin writes when its
// config sets vectors.
const FakeVectorEnrichment = `embedding = { type = "vector", label = "Embedding" }
`
```

In `internal/fakeplugin/enrich.go`, `enrichRun` needs the instance config;
pass `config` from `main.go`'s call site (`enrichRun(ctx, conn, params,
*mode, config)`), and after building `r.Values`:

```go
		if config["vectors"] == true {
			r.Values["embedding"] = json.RawMessage(fmt.Sprintf(`{"space": "fake", "vector": [%d, 1]}`, len(data.Description)))
		}
```

Extend the function's doc comment with: "With config vectors, embedding is
[description length, 1] in space fake."

- [ ] **Step 2: Write the failing tests**

In `internal/api/enrichments_test.go`, change `newEnrichEnv` to decode
`testutil.FakeEnrichments + testutil.FakeVectorEnrichment` into the judge
manifest, and take an optional config:

```go
func newEnrichEnv(t *testing.T, config ...map[string]any) *enrichEnv {
```

passing `Config: config[0]` to `plugins.NewInstance` when given (check the
field name in `plugins.NewInstance`). Delete
`TestEnrichRunQueuesEmbedOnlyForEmbedKeys`, and the `Queue`/`Log` fields
in the `enrich.Runner` literal. Add:

```go
func TestEnrichRunStoresVectors(t *testing.T) {
	e := newEnrichEnv(t, map[string]any{"vectors": true})
	id := e.card("Brakka", "abc")
	j, p := e.evaluate(e.instID, id)
	if j.Status != jobs.StatusSucceeded || p.Done != 1 {
		t.Fatalf("job %s, progress %+v", j.Status, p)
	}
	var space, vec string
	if err := e.pool.QueryRow(context.Background(), `SELECT space, embedding::text FROM card_embedding WHERE card_id = $1 AND instance_id = $2`,
		id, e.instID).Scan(&space, &vec); err != nil {
		t.Fatal(err)
	}
	if space != "fake" || vec != "[3,1]" {
		t.Fatalf("stored %s %s", space, vec)
	}
	var vals []enrichmentValue
	e.call("GET", fmt.Sprintf("/cards/%d/enrichments", id), nil, 200, &vals)
	for _, v := range vals {
		if v.Key == "embedding" {
			t.Fatal("a vector was stored as an enrichment value too")
		}
	}
}

func TestEnrichRunDropsVectorsWithoutPgvector(t *testing.T) {
	e := newEnrichEnv(t, map[string]any{"vectors": true})
	if _, err := e.pool.Exec(context.Background(), `DROP TABLE card_embedding`); err != nil {
		t.Fatal(err)
	}
	e.s.Lib = library.New(e.pool, e.s.Lib.Blobs())
	id := e.card("Brakka", "abc")
	j, p := e.evaluate(e.instID, id)
	if j.Status != jobs.StatusSucceeded || p.Done != 1 || !slices.ContainsFunc(p.Warnings, func(w string) bool { return strings.Contains(w, "pgvector") }) {
		t.Fatalf("job %s, progress %+v", j.Status, p)
	}
}
```

The second test needs a fresh `Library` so `HasVectors` reads the dropped
table; build it the way `newEnv` does (if there's no `Blobs()` accessor,
reuse the store `newEnv` created, from the `testEnv` struct). Make sure the
`enrich.Runner` in `newEnrichEnv` reads `e.s.Lib` at run time, or create the
env after dropping the table, whichever the helper allows without a new
abstraction.

- [ ] **Step 3: Run them and see them fail**

```bash
$DB go test ./internal/api -run 'TestEnrichRunStoresVectors|TestEnrichRunDropsVectorsWithoutPgvector' -count=1
```

Expected: FAIL: the vector is stored in `enrichment` and not in
`card_embedding`.

- [ ] **Step 4: Split vector values off in `batch`**

In `internal/enrich/enrich.go`, remove `Queue` and `Log` from `Runner`, the
`embed.Enqueue` block in `run`, and the `embed` import. In `batch`, before
the per-revision loop:

```go
	vectors, err := rn.vectors(ctx, inst.Manifest.Enrichments, &checked)
	if err != nil {
		return Checked{}, err
	}
	if len(vectors) > 0 {
		if err := rn.Lib.SaveVectors(ctx, instanceID, vectors); err != nil {
			return Checked{}, err
		}
	}
```

and add:

```go
// vectors takes checked's vector values out of its Values, as NewVectors to
// store, or drops them with a warning when this install has no pgvector.
func (rn *Runner) vectors(ctx context.Context, decl map[string]protocol.Enrichment, checked *Checked) ([]library.NewVector, error) {
	var out []library.NewVector
	kept := checked.Values[:0]
	for _, v := range checked.Values {
		if decl[v.Key].Type != "vector" {
			kept = append(kept, v)
			continue
		}
		var vv protocol.VectorValue
		if err := json.Unmarshal(v.Value, &vv); err != nil {
			return nil, fmt.Errorf("read checked vector: %w", err)
		}
		out = append(out, library.NewVector{CardID: v.CardID, RevisionID: v.RevisionID, Space: vv.Space, Vector: vv.Vector})
	}
	checked.Values = kept
	if len(out) == 0 {
		return nil, nil
	}
	ok, err := rn.Lib.HasVectors(ctx)
	if err != nil {
		return nil, err
	}
	if !ok {
		checked.Warnings = append(checked.Warnings, fmt.Sprintf("dropped %d vectors: this install has no pgvector", len(out)))
		return nil, nil
	}
	return out, nil
}
```

A revision whose only value was a vector now has no values left in the
per-revision loop; it was already counted done by `Check`, which is right:
its vector was stored.

- [ ] **Step 4b: Count a stored vector as enriched**

Without this, a plugin that writes only a vector is re-run on every card
each time, since `unscored` finds no `enrichment` row. Add to
`TestEnrichRunStoresVectors`, at its end:

```go
	j, p = e.evaluate(e.instID, id)
	if j.Status != jobs.StatusSucceeded || p.Skipped != 1 {
		t.Fatalf("second run: job %s, progress %+v", j.Status, p)
	}
```

That passes today only because the fake also writes score values, so also
add a vector-only case: give `enrichRun` a config `vectors_only` that sets
`r.Values` to just the embedding, and repeat the two runs with
`newEnrichEnv(t, map[string]any{"vectors": true, "vectors_only": true})`,
expecting `Skipped == 1` on the second.

In `internal/library/enrichment.go`, replace `EnrichedRevisions`:

```go
// EnrichedRevisions returns which of revIDs hold a value or a vector from
// instanceID.
func (l *Library) EnrichedRevisions(ctx context.Context, instanceID int64, cardIDs, revIDs []int64) ([]int64, error) {
	vectors, err := l.HasVectors(ctx)
	if err != nil {
		return nil, err
	}
	// card_id leads the index; revision_id alone would scan the table.
	sql := `SELECT revision_id FROM enrichment WHERE card_id = ANY($2) AND instance_id = $1 AND revision_id = ANY($3)`
	if vectors {
		sql += ` UNION SELECT revision_id FROM card_embedding WHERE card_id = ANY($2) AND instance_id = $1 AND revision_id = ANY($3)`
	}
	rows, err := l.pool.Query(ctx, sql, instanceID, cardIDs, revIDs)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[int64])
}
```

`UNION` already removes duplicates, so `DISTINCT` goes.

- [ ] **Step 5: Delete the embed job**

```bash
git rm -r internal/embed
```

Then remove every use the compiler reports:

- `cmd/innkeeper/worker.go`: the `embedder` variable, its handler entry,
  its `{Kind: embed.Kind, Every: time.Hour}` schedule, and the `Queue`/`Log`
  fields in the `enrich.Runner` literal.
- `internal/importer/importer.go`: the `embed.Enqueue` line.
- `internal/api/jobs.go`: the `embed.Kind` label.

```bash
go build ./... 2>&1 | head -30
```

Fix what's left in `internal/api` in Task 6; everything else must build.

- [ ] **Step 6: Run the tests**

```bash
$DB go test ./internal/enrich/... ./internal/importer/... -count=1
```

Expected: PASS. Delete importer test assertions that an import queues an
embed job; keep the rest of those tests.

- [ ] **Step 7: Commit**

```bash
git add -A internal cmd
git commit -m "feat(enrich): store vector enrichments as card vectors and drop the embed job"
```

---

### Task 6: API: `vector_instance`, `vectors_available`, and search via the plugin

**Files:** `internal/api/settings.go`, `internal/api/search.go`,
`internal/api/discover.go`, `internal/api/server.go`, `internal/mcp/tools.go`,
`internal/api/settings_test.go`, `internal/api/search_test.go`

- [ ] **Step 1: Write the failing tests**

In `internal/api/settings_test.go`, delete the `EmbedInstance` tests and
`TestEmbedLibraryQueuesOneJobForThatLibrary`, and add:

```go
func TestSettingVectorInstance(t *testing.T) {
	e := newEnrichEnv(t, map[string]any{"vectors": true, "services": []string{protocol.MethodVectorQuery}})
	var s settings
	e.call("PATCH", "/settings", map[string]any{"vector_instance": e.instID}, 200, &s)
	if s.VectorInstance == nil || *s.VectorInstance != e.instID || !s.VectorsAvailable {
		t.Fatalf("settings = %+v", s)
	}
	e.call("PATCH", "/settings", map[string]any{"vector_instance": nil}, 200, &s)
	if s.VectorInstance != nil {
		t.Fatalf("cleared: %+v", s)
	}
}

func TestSettingVectorInstanceRejectsAnInstanceWithoutVectorQuery(t *testing.T) {
	e := newEnrichEnv(t)
	e.call("PATCH", "/settings", map[string]any{"vector_instance": e.instID}, 409, nil)
}
```

Use the status the old `TestSettingEmbedInstanceRejectsAnAIInstanceWithoutEmbed`
expected for an incapable instance if it isn't 409.

In `internal/api/search_test.go`, replace the two semantic tests:

```go
func TestSemanticSearchRanksByMeaning(t *testing.T) {
	e := newEnrichEnv(t, map[string]any{"vectors": true, "services": []string{protocol.MethodVectorQuery}})
	e.call("PATCH", "/settings", map[string]any{"vector_instance": e.instID}, 200, nil)
	near, far := e.card("Brakka", "abcdefg"), e.card("Lia", "a")
	e.evaluate(e.instID, near, far)

	// The fake embeds the query "abcdef" as [6, 1]: nearer [7, 1] than [1, 1].
	var res library.Results
	e.call("POST", "/cards/search", map[string]any{"text": "abcdef", "semantic": true}, 200, &res)
	if len(res.Hits) != 2 || res.Hits[0].ID != near || res.Hits[1].ID != far {
		t.Fatalf("hits = %+v", res.Hits)
	}
	e.call("POST", "/cards/search", map[string]any{"text": "abc", "semantic": true, "sort": "name"}, 400, nil)
	e.call("POST", "/lorebooks/search", map[string]any{"text": "abc", "semantic": true}, 400, nil)
}

func TestSemanticSearchWithoutAVectorInstanceIs409(t *testing.T) {
	e := newEnv(t)
	e.call("POST", "/cards/search", map[string]any{"text": "abc", "semantic": true}, 409, nil)
}
```

Check the ranking claim against cosine similarity before relying on it:
cos([6,1],[7,1]) ≈ 0.9997 and cos([6,1],[1,1]) ≈ 0.81, so `near` ranks first.

- [ ] **Step 2: Run them and see them fail**

```bash
$DB go test ./internal/api -run 'TestSettingVectorInstance|TestSemanticSearch' -count=1
```

Expected: compile errors in `internal/api` from Task 5's deletions, then
the new tests fail.

- [ ] **Step 3: Settings**

In `internal/api/settings.go`, replace the `EmbedInstance` fields:

```go
	VectorInstance   *int64 `json:"vector_instance" doc:"The enrich instance whose card vectors similar cards and semantic search read, or null for none."`
	VectorsAvailable bool   `json:"vectors_available" readOnly:"true" doc:"Whether this install has pgvector. Without it, similar cards rank by tags and semantic search is off."`
```

and in `patchSettingsIn`:

```go
		VectorInstance optional[int64] `json:"vector_instance,omitempty" doc:"An enrich instance that offers vector.query, or null to clear it."`
```

In `settings()`, replace the `embed.Instance` block:

```go
	if id, err := s.Registry.VectorInstance(ctx); err != nil {
		return nil, s.fail(err)
	} else if id != 0 {
		out.Body.VectorInstance = &id
	}
	if out.Body.VectorsAvailable, err = s.Lib.HasVectors(ctx); err != nil {
		return nil, s.fail(err)
	}
```

In the PATCH handler, `in.Body.EmbedInstance` becomes
`in.Body.VectorInstance` calling `s.setVectorInstance`, which replaces
`setEmbedInstance`:

```go
// setVectorInstance stores the vector instance, or clears it for nil.
// Vectors stay: each instance's are kept apart, and spaces keep models apart.
func (s *Server) setVectorInstance(ctx context.Context, id *int64) error {
	if id == nil {
		return s.fail(s.Registry.SetSetting(ctx, plugins.VectorInstanceKey, ""))
	}
	if _, err := s.Registry.InstanceOffering(ctx, s.Plugins, *id, protocol.MethodVectorQuery); err != nil {
		return s.fail(err)
	}
	return s.fail(s.Registry.SetSetting(ctx, plugins.VectorInstanceKey, strconv.FormatInt(*id, 10)))
}
```

- [ ] **Step 4: Search, Similar, and errors**

In `internal/api/search.go`, replace `withEmbedding`:

```go
// withEmbedding asks the vector instance for q's vector when q is a
// semantic card search, for Search to rank by. Anything else is left for
// Search to refuse with a 400.
func (s *Server) withEmbedding(ctx context.Context, k *library.Kind, q *library.Query) error {
	if !q.Semantic || q.Text == "" || k != library.Card {
		return nil
	}
	if ok, err := s.Lib.HasVectors(ctx); err != nil || !ok {
		return cmp.Or(err, library.ErrNoVectors)
	}
	inst, err := s.Registry.VectorInstance(ctx)
	if err != nil {
		return err
	}
	if inst == 0 {
		return library.ErrNoVectorInstance
	}
	v, err := s.Registry.VectorQuery(ctx, s.Plugins, inst, q.Text)
	if err != nil {
		return err
	}
	q.Embedding, q.EmbedInstance, q.EmbedSpace = v.Vector, inst, v.Space
	return nil
}
```

In `internal/api/discover.go`, delete the `embed-library` route and its
input type, and in the similar route replace `embed.Instance(ctx, s.Registry)`
with `s.Registry.VectorInstance(ctx)`. Update its description to "by
meaning when a vector source is set, else by shared tags".

In `internal/api/server.go`, replace `errors.Is(err, library.ErrNoEmbedInstance)`
with `errors.Is(err, library.ErrNoVectorInstance), errors.Is(err, library.ErrNoVectors)`.

In `internal/mcp/tools.go`, change "needs an embed instance" to "needs a
vector source".

- [ ] **Step 5: Regenerate and test**

```bash
go build ./... && (cd web && npm run gen)
$DB go test ./internal/api ./internal/mcp -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal web/openapi.json web/src/api/schema.d.ts
git commit -m "feat(api): read vectors from the vector instance and report pgvector"
```

---

### Task 7: Web: Vectors from

**Files:** rename `web/src/settings/EmbedFrom.tsx` → `VectorsFrom.tsx` and
`EmbedFrom.test.tsx` → `VectorsFrom.test.tsx`, `web/src/settings/General.tsx`,
`web/src/settings/Libraries.tsx`, `web/src/cards/Cards.tsx`,
`web/src/cards/Cards.test.tsx`

- [ ] **Step 1: Rename and rewrite the test first**

```bash
cd web && git mv src/settings/EmbedFrom.tsx src/settings/VectorsFrom.tsx && git mv src/settings/EmbedFrom.test.tsx src/settings/VectorsFrom.test.tsx
```

In `VectorsFrom.test.tsx`, keep the file's mocking setup and change it to:

- mock `GET /instances` with query `offers: "vector.query"`;
- read and patch `vector_instance`;
- add a case where settings has `vectors_available: false`, asserting the
  text "Similar cards by meaning and semantic search need pgvector" shows
  and no select renders;
- delete the `EmbedNow` tests.

- [ ] **Step 2: Run it and see it fail**

```bash
cd web && npx vitest run src/settings/VectorsFrom.test.tsx
```

- [ ] **Step 3: Write the component**

Replace `web/src/settings/VectorsFrom.tsx`:

```tsx
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, must } from "../api/client";
import { settingsQuery } from "../api/queries";
import { SaveStatus } from "./SaveStatus";

/** VectorsFrom picks the enrich instance whose card vectors similar cards and semantic search read. */
export function VectorsFrom() {
  const qc = useQueryClient();
  const sources = useQuery({
    queryKey: ["instances", "offers", "vector.query"],
    queryFn: async () => must(await api.GET("/instances", { params: { query: { offers: "vector.query" } } })),
  });
  const settings = useQuery(settingsQuery);
  const save = useMutation({
    mutationFn: async (vector_instance: number | null) => {
      qc.setQueryData(settingsQuery.queryKey, must(await api.PATCH("/settings", { body: { vector_instance } })));
    },
    meta: { toast: false },
  });
  if (settings.data && !settings.data.vectors_available) {
    return (
      <p className="muted">
        Similar cards by meaning and semantic search need pgvector.{" "}
        <a href="https://github.com/inkandale/innkeeper/blob/main/docs/running.md">How to enable it</a>
      </p>
    );
  }
  if (!sources.data?.length) return null;
  const saved = settings.data?.vector_instance;
  return (
    <section className="scores-from">
      <label title="The plugin instance whose card vectors find similar cards and rank semantic search.">
        Vectors from{" "}
        <select
          className="input"
          value={sources.data.some((i) => i.id === saved) ? saved! : ""}
          disabled={!settings.data || save.isPending}
          onChange={(e) => save.mutate(e.target.value === "" ? null : Number(e.target.value))}
        >
          <option value="">None</option>
          {sources.data.map((i) => (
            <option key={i.id} value={i.id}>
              {i.label}
            </option>
          ))}
        </select>
      </label>{" "}
      <SaveStatus save={save} />
    </section>
  );
}
```

Check the repo link against the `README.md`'s repository URL and use that.

- [ ] **Step 4: Follow the rename**

- `General.tsx`: import and render `VectorsFrom` in place of `EmbedFrom`.
- `Libraries.tsx`: remove the `EmbedNow` import and its use. A library's
  vectors come from a bulk enrich of the vector instance, which the
  existing enrich run UI starts.
- `Cards.tsx` line 72: `settings.data?.embed_instance` →
  `settings.data?.vector_instance`, and its comment's "embed instance" →
  "vector source". Follow it in `Cards.test.tsx`.

- [ ] **Step 5: Run the web checks**

```bash
cd web && npm run typecheck && npm test
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add -A web/src
git commit -m "feat(web): pick the vector source and say when pgvector is missing"
```

---

### Task 8: Docs

**Files:** `docs/running.md`, `docs/protocol.md`

- [ ] **Step 1: `running.md`**

Replace the three lines starting "Innkeeper needs PostgreSQL 15 or later
with the pgvector extension." with:

```markdown
Innkeeper needs PostgreSQL 15 or later. pgvector is optional: with it,
similar cards rank by meaning and semantic search works; without it,
similar cards rank by shared tags. Innkeeper creates the extension at
startup, which pgvector allows only a superuser, so start it as one once or
have an admin run `CREATE EXTENSION vector`, then restart.
```

Check `chart/values.yaml` line 9–10 says the same and fix it to match.

- [ ] **Step 2: `protocol.md`**

Find each passage with `grep -n "embed\b\|embed =\|enrichment types\|\"list\"" docs/protocol.md`
and:

- In the enrichment types table, add a row: `vector` | An object
  `{"space": "...", "vector": [numbers]}`. Core stores it as the card's
  vector, not as a value. `space` (1–200 characters) names what made the
  vector; core compares only vectors in one space. At most one vector key
  per plugin, and its plugin must offer `vector.query`.
- Remove the `embed` key's description, and add one line: "`embed` is
  retired and ignored."
- In `initialize`'s params, add `features`: the optional core features the
  install has. `vectors` means core stores vector values; without it, skip
  computing them.
- Add a `vector.query` section beside the `ai.*` services: params
  `{"text": "..."}`, result a vector value in the same space as the
  plugin's card vectors. An enrich plugin offers it by listing it in
  `initialize`'s `services`.

- [ ] **Step 3: Commit**

```bash
git add docs/running.md docs/protocol.md chart/values.yaml
git commit -m "docs: pgvector is optional; document vector enrichments and vector.query"
```

---

### Task 9: Full check

- [ ] **Step 1: Run what CI runs**

```bash
go vet ./...
$DB go test -race ./...
cd web && npm run typecheck && npm test
```

Expected: PASS.

- [ ] **Step 2: Grep for stragglers**

```bash
grep -rn "embed_instance\|EmbedInstance\b\|ErrNoEmbedInstance\|internal/embed\|SaveEmbeddings\|Unembedded\|EmbedFrom\|EmbedNow\|Registry.Embed\b" \
  --include='*.go' --include='*.ts' --include='*.tsx' --include='*.md' . | grep -v node_modules | grep -v docs/superpowers
```

Expected: no hits. Migration `0052` names `embed_instance` in SQL, which
this grep doesn't search.

- [ ] **Step 3: Read the diff once more** for dead code, stale names and
anything the plan didn't ask for.
