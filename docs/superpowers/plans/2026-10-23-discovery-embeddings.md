# Discovery slice 1: embeddings

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Embed every card through an install-wide `ai.embed` instance and keep the vectors current, so slices 2 and 3 can rank by them.

**Architecture:** A `card_embedding` table (pgvector) holds one vector per card per instance. A new `internal/embed` job asks `library` for cards whose vector is missing or stale, calls `plugins.Registry.Embed`, and saves the results. The importer, `PATCH /settings`, and an hourly schedule queue the job. The old duplicate finder moves from `/cards/{id}/similar` to `/cards/{id}/duplicates`.

**Tech stack:** Go, pgx, PostgreSQL with pgvector, huma, React and TanStack Query.

**Spec:** [2026-10-23-discovery-design.md](../specs/2026-10-23-discovery-design.md)

---

## Conventions

- Work in `~/git/innkeeper-discovery` (branch `discovery`). Never run git
  in `~/git/innkeeper`.
- Database tests need
  `INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres`
  (pgvector image, see README). Without it they skip, which is not a pass.
- After any API change, run `cd web && npm run gen`. Never hand-edit
  `web/openapi.json` or `web/src/api/schema.d.ts`.
- Comments say why, in one line. Follow the surrounding code's style.

## Files

| File | Change |
|---|---|
| `internal/library/similar.go` | Rename `SimilarCards` to `DuplicateCards` |
| `internal/library/similar_test.go` | Follow the rename |
| `internal/api/entities.go` | Route `/cards/{id}/duplicates`, operation `list-duplicate-cards` |
| `internal/api/entities_test.go` | Follow the rename |
| `internal/db/migrations/0045_card_embedding.sql` | New table |
| `internal/library/embeddings.go` | `Unembedded`, `SaveEmbeddings`, `DeleteEmbeddings` |
| `internal/library/embeddings_test.go` | Tests for the above |
| `internal/plugins/plugins.go` | `Registry.Embed`, sharing `Decide`'s service check |
| `internal/plugins/services_test.go` | Test for `Embed` |
| `internal/embed/embed.go` | `Kind`, `InstanceKey`, `Runner`, `Enqueue` |
| `internal/embed/embed_test.go` | Job tests |
| `cmd/innkeeper/worker.go` | Register handler and hourly schedule |
| `internal/importer/importer.go` | Queue the embed job after an import |
| `internal/api/settings.go` | `embed_instance` get and patch |
| `internal/api/settings_test.go` | Settings tests (create if missing) |
| `web/src/settings/EmbedFrom.tsx` | Embed instance picker |
| `web/src/settings/EmbedFrom.test.tsx` | Picker test |
| `web/src/settings/Plugins.tsx` | Render `EmbedFrom` beside `ScoresFrom` |

---

### Task 1: Rename the duplicate finder

"Similar" is about to mean something else, so the hash-based duplicate
finder takes the name it describes.

**Files:** `internal/library/similar.go`, `internal/library/similar_test.go`,
`internal/api/entities.go`, `internal/api/entities_test.go`

- [ ] **Step 1: Rename in the tests first**

In `internal/api/entities_test.go`, change both `/similar` paths to
`/duplicates` (lines 89 and 209) and the failure message `similar =` to
`duplicates =`. In `internal/library/similar_test.go`, replace every
`lib.SimilarCards(` with `lib.DuplicateCards(`.

- [ ] **Step 2: Run them and see them fail**

```bash
go test ./internal/library -run 'TestSimilarCards' ./internal/api -run 'TestCard'
```

Expected: a compile error, `lib.DuplicateCards undefined`.

- [ ] **Step 3: Rename the code**

In `internal/library/similar.go`:

```go
// DuplicateCards returns the other cards in cardID's library whose head
// revision is like cardID's, within maxDistance bits, closest first.
func (l *Library) DuplicateCards(ctx context.Context, cardID int64, maxDistance int) ([]Match, error) {
```

Rename `TestSimilarCards` to `TestDuplicateCards`. Change `errWideDistance`'s
text from `similar lookup` to `duplicate lookup`, and the `NearDistance`
comment's route to `GET /cards/{id}/duplicates`.

In `internal/api/entities.go` `cardRoutes`:

```go
	huma.Register(api, huma.Operation{OperationID: "list-duplicate-cards", Method: http.MethodGet, Path: "/cards/{id}/duplicates",
		Summary: "Find likely duplicates", Description: "Same content, near-identical text, or a similar avatar; closest first.", Tags: tags},
		func(ctx context.Context, in *similarIn) (*matchesOut, error) {
			if _, err := s.Lib.Cards.Get(ctx, in.ID); err != nil {
				return nil, s.fail(err)
			}
			m, err := s.Lib.DuplicateCards(ctx, in.ID, in.MaxDistance)
```

Leave `similarIn` alone: `in-other-libraries` shares it.

- [ ] **Step 4: Grep for stragglers**

```bash
grep -rn "SimilarCards\|cards/{id}/similar\|list-similar-cards" --include='*.go' --include='*.ts' --include='*.tsx' --include='*.md' . | grep -v node_modules
```

Expected: hits only in `web/src/api/schema.d.ts` and `web/openapi.json`, and
in dated specs and plans under `docs/superpowers`, which record history and
stay as they are.

- [ ] **Step 5: Regenerate and test**

```bash
(cd web && npm run gen) && go test ./internal/library -run TestDuplicateCards && go test ./internal/api -run TestCard
```

Expected: PASS, and the generated schema has `/cards/{id}/duplicates`.

- [ ] **Step 6: Commit**

```bash
git add -A internal/library internal/api web/openapi.json web/src/api/schema.d.ts
git commit -m "refactor(api): serve the duplicate finder at /cards/{id}/duplicates"
```

---

### Task 2: Store embeddings

**Files:** create `internal/db/migrations/0045_card_embedding.sql`,
`internal/library/embeddings.go`, `internal/library/embeddings_test.go`

- [ ] **Step 1: Write the migration**

```sql
-- One vector per card per embed instance, from the revision named. No
-- fixed dimension, so switching to a model of another size needs no migration.
CREATE EXTENSION IF NOT EXISTS vector;
CREATE TABLE card_embedding (
    card_id bigint NOT NULL REFERENCES card (id),
    instance_id bigint NOT NULL REFERENCES plugin_instance ON DELETE CASCADE,
    revision_id bigint NOT NULL REFERENCES card_revision (id),
    embedding vector NOT NULL,
    PRIMARY KEY (instance_id, card_id)
);
```

- [ ] **Step 2: Write the failing tests**

`internal/library/embeddings_test.go`. Instances come from a raw insert,
since `library` can't import `plugins`:

```go
package library

import (
	"context"
	"testing"
)

// newInstanceRow inserts a plugin instance row and returns its ID.
func newInstanceRow(t *testing.T, lib *Library) int64 {
	t.Helper()
	var id int64
	if err := lib.pool.QueryRow(context.Background(),
		`INSERT INTO plugin_instance (plugin, config) VALUES ('fake', '{}') RETURNING id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestUnembeddedListsMissingAndStaleCards(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	inst := newInstanceRow(t, lib)
	brakka, rev := newCard(t, lib, "brakka", `{"name": "Brakka", "description": "A dwarf smith.", "first_mes": "Hello."}`)
	lia, liaRev := newCard(t, lib, "lia", `{"name": "Lia", "personality": "Cheerful."}`)

	got, err := lib.Unembedded(ctx, inst, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != (EmbedText{CardID: brakka, RevisionID: rev, Text: "Brakka\nA dwarf smith."}) || got[1].CardID != lia {
		t.Fatalf("unembedded = %+v", got)
	}

	if err := lib.SaveEmbeddings(ctx, inst, []EmbedText{got[0], got[1]}, [][]float32{{1, 0}, {0, 1}}); err != nil {
		t.Fatal(err)
	}
	if got, err = lib.Unembedded(ctx, inst, 10); err != nil || len(got) != 0 {
		t.Fatalf("after saving: %+v, %v", got, err)
	}

	if _, err := lib.Cards.Update(ctx, lia, liaRev, edit(`{"name": "Lia", "personality": "Grumpy."}`)); err != nil {
		t.Fatal(err)
	}
	if got, err = lib.Unembedded(ctx, inst, 10); err != nil || len(got) != 1 || got[0].CardID != lia || got[0].Text != "Lia\nGrumpy." {
		t.Fatalf("after an edit: %+v, %v", got, err)
	}
}

func TestUnembeddedSkipsCardsWithNoText(t *testing.T) {
	lib := newLibrary(t)
	inst := newInstanceRow(t, lib)
	newCard(t, lib, "blank", `{"first_mes": "Only a greeting."}`)
	if got, err := lib.Unembedded(context.Background(), inst, 10); err != nil || len(got) != 0 {
		t.Fatalf("unembedded = %+v, %v", got, err)
	}
}

func TestDeleteEmbeddingsQueuesEveryCardAgain(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	inst := newInstanceRow(t, lib)
	newCard(t, lib, "brakka", `{"name": "Brakka"}`)
	todo, err := lib.Unembedded(ctx, inst, 10)
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.SaveEmbeddings(ctx, inst, todo, [][]float32{{1, 2}}); err != nil {
		t.Fatal(err)
	}
	if err := lib.DeleteEmbeddings(ctx, inst); err != nil {
		t.Fatal(err)
	}
	if got, err := lib.Unembedded(ctx, inst, 10); err != nil || len(got) != 1 {
		t.Fatalf("after delete: %+v, %v", got, err)
	}
}
```

Before running, check the `plugin_instance` columns in
`internal/db/migrations/0002_jobs.sql` and later migrations. If the insert
needs more `NOT NULL` columns, add them to `newInstanceRow`.

- [ ] **Step 3: Run them and see them fail**

```bash
go test ./internal/library -run 'Embed'
```

Expected: a compile error, `lib.Unembedded undefined`.

- [ ] **Step 4: Implement**

`internal/library/embeddings.go`:

```go
package library

import (
	"context"
	"strconv"
	"strings"
)

// EmbedText is a card's text to embed, from its head revision.
type EmbedText struct {
	CardID     int64
	RevisionID int64
	Text       string
}

// embedChars caps the text sent to a model; most embedding models read
// fewer than 8,192 tokens.
const embedChars = 8000

// Unembedded returns up to limit cards with text whose head revision has no
// vector from instance, lowest ID first. The text is the fields that say
// who the character is, leaving out greetings and example dialogue.
func (l *Library) Unembedded(ctx context.Context, instance int64, limit int) ([]EmbedText, error) {
	rows, err := l.pool.Query(ctx, `
		SELECT c.id, r.id, left(concat_ws(E'\n', nullif(r.data->>'name', ''), nullif(r.data->>'description', ''),
			nullif(r.data->>'personality', ''), nullif(r.data->>'scenario', ''), nullif(r.data->>'creator_notes', '')), $3) AS text
		FROM card c
		JOIN card_revision r ON r.id = c.head_revision_id
		LEFT JOIN card_embedding e ON e.instance_id = $1 AND e.card_id = c.id
		WHERE e.revision_id IS DISTINCT FROM c.head_revision_id
			AND concat(r.data->>'name', r.data->>'description', r.data->>'personality', r.data->>'scenario', r.data->>'creator_notes') <> ''
		ORDER BY c.id
		LIMIT $2`, instance, limit, embedChars)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[EmbedText])
}

// SaveEmbeddings stores vectors[i] as cards[i]'s vector from instance,
// replacing an older one.
func (l *Library) SaveEmbeddings(ctx context.Context, instance int64, cards []EmbedText, vectors [][]float32) error {
	ids := make([]int64, len(cards))
	revs := make([]int64, len(cards))
	vecs := make([]string, len(cards))
	for i, c := range cards {
		ids[i], revs[i], vecs[i] = c.CardID, c.RevisionID, vectorText(vectors[i])
	}
	_, err := l.pool.Exec(ctx, `
		INSERT INTO card_embedding (instance_id, card_id, revision_id, embedding)
		SELECT $1, u.card_id, u.revision_id, u.embedding::vector
		FROM unnest($2::bigint[], $3::bigint[], $4::text[]) AS u (card_id, revision_id, embedding)
		ON CONFLICT (instance_id, card_id) DO UPDATE SET revision_id = EXCLUDED.revision_id, embedding = EXCLUDED.embedding`,
		instance, ids, revs, vecs)
	return err
}

// DeleteEmbeddings drops instance's vectors, so every card is embedded again.
func (l *Library) DeleteEmbeddings(ctx context.Context, instance int64) error {
	_, err := l.pool.Exec(ctx, `DELETE FROM card_embedding WHERE instance_id = $1`, instance)
	return err
}

// vectorText writes v in pgvector's text form, so no pgvector Go package is needed.
func vectorText(v []float32) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, f := range v {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(float64(f), 'g', -1, 32))
	}
	b.WriteByte(']')
	return b.String()
}
```

Add `"github.com/jackc/pgx/v5"` to the imports.

- [ ] **Step 5: Run the tests**

```bash
go test ./internal/library -run 'Embed'
```

Expected: PASS. If the migration fails with `permission denied to create
extension "vector"`, stop and report. The production database may need the
extension created by a superuser (see Task 8).

- [ ] **Step 6: Commit**

```bash
git add internal/db/migrations/0045_card_embedding.sql internal/library/embeddings.go internal/library/embeddings_test.go
git commit -m "feat(library): store a vector per card per embed instance"
```

---

### Task 3: `Registry.Embed`

**Files:** `internal/plugins/plugins.go`, `internal/plugins/services_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/plugins/services_test.go`. It uses the same setup as
`TestDecideRefusesAnAIInstanceWithoutAIDecide`. The fake plugin answers
each text with `[len(text), 1]`.

```go
func TestEmbedRefusesAnAIInstanceWithoutAIEmbed(t *testing.T) {
	ctx := context.Background()
	m, err := protocol.LoadManifest(testutil.FakePluginWith(t, []string{"ai"}))
	if err != nil {
		t.Fatal(err)
	}
	box, err := secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	reg := New(testdb.New(t), map[string]*protocol.Manifest{"fake": m},
		&blobstore.Store{Name: "library", Root: t.TempDir()}, box)
	mgr := pluginhost.NewManager(nil, time.Minute, quiet)
	t.Cleanup(mgr.Close)
	create := func(name string, services ...string) int64 {
		id, err := reg.CreateInstance(ctx, NewInstance{Plugin: "fake", Name: new(name),
			Config: map[string]any{"services": services}})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}

	if _, err := reg.Embed(ctx, mgr, create("Chat", protocol.MethodAIChat), []string{"hi"}); !errors.Is(err, ErrIncapable) ||
		!strings.Contains(err.Error(), "doesn't offer ai.embed") {
		t.Fatalf("Embed on an instance without ai.embed: %v, want ErrIncapable naming ai.embed", err)
	}
	got, err := reg.Embed(ctx, mgr, create("Embed", protocol.MethodAIEmbed), []string{"hi", "hello"})
	if err != nil || len(got) != 2 || got[0][0] != 2 || got[1][0] != 5 {
		t.Fatalf("Embed = %v, %v; want one vector per text", got, err)
	}
}
```

- [ ] **Step 2: Run it and see it fail**

```bash
go test ./internal/plugins -run TestEmbed
```

Expected: a compile error, `reg.Embed undefined`.

- [ ] **Step 3: Implement, sharing Decide's check**

In `internal/plugins/plugins.go`, replace `Decide` with a shared helper,
`Decide`, and `Embed`:

```go
// callService sends p to ai instance id's method, which the instance must offer.
func (r *Registry) callService(ctx context.Context, mgr *pluginhost.Manager, id int64, method string, p, res any) error {
	inst, err := r.InstanceWith(ctx, id, protocol.Services[method])
	if err != nil {
		return err
	}
	services, err := r.Services(ctx, mgr, inst)
	if err != nil {
		return err
	}
	if !slices.Contains(services, method) {
		return fmt.Errorf("%w: instance %d doesn't offer %s", ErrIncapable, id, method)
	}
	return r.Call(ctx, mgr, inst, method, p, res)
}

// Decide sends p to ai instance id's ai.decide, which the instance must
// offer.
func (r *Registry) Decide(ctx context.Context, mgr *pluginhost.Manager, id int64, p protocol.AIDecideParams) (protocol.AIDecideResult, error) {
	var res protocol.AIDecideResult
	return res, r.callService(ctx, mgr, id, protocol.MethodAIDecide, p, &res)
}

// Embed returns ai instance id's vector for each of texts, in order. The
// instance must offer ai.embed.
func (r *Registry) Embed(ctx context.Context, mgr *pluginhost.Manager, id int64, texts []string) ([][]float32, error) {
	var res protocol.AIEmbedResult
	if err := r.callService(ctx, mgr, id, protocol.MethodAIEmbed, protocol.AIEmbedParams{Texts: texts}, &res); err != nil {
		return nil, err
	}
	if err := res.Validate(len(texts)); err != nil {
		return nil, fmt.Errorf("ai.embed from instance %d: %w", id, err)
	}
	return res.Vectors, nil
}
```

- [ ] **Step 4: Run the plugins tests**

```bash
go test ./internal/plugins -run 'TestEmbed|TestDecide'
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/plugins/plugins.go internal/plugins/services_test.go
git commit -m "feat(plugins): let core call an instance's ai.embed"
```

---

### Task 4: The embed job

**Files:** create `internal/embed/embed.go`, `internal/embed/embed_test.go`

- [ ] **Step 1: Write the failing tests**

`internal/embed/embed_test.go`:

```go
package embed

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strconv"
	"testing"
	"time"

	"github.com/inkandale/innkeeper/internal/blobstore"
	"github.com/inkandale/innkeeper/internal/jobs"
	"github.com/inkandale/innkeeper/internal/library"
	"github.com/inkandale/innkeeper/internal/pluginhost"
	"github.com/inkandale/innkeeper/internal/plugins"
	"github.com/inkandale/innkeeper/internal/secrets"
	"github.com/inkandale/innkeeper/internal/testdb"
	"github.com/inkandale/innkeeper/internal/testutil"
	"github.com/inkandale/innkeeper/protocol"
)

type env struct {
	lib  *library.Library
	reg  *plugins.Registry
	run  *Runner
	inst int64
}

// newEnv has one fake ai instance offering ai.embed, not yet the embed instance.
func newEnv(t *testing.T) env {
	t.Helper()
	ctx := context.Background()
	pool := testdb.New(t)
	store := &blobstore.Store{Name: "library", Root: t.TempDir()}
	m, err := protocol.LoadManifest(testutil.FakePluginWith(t, []string{"ai"}))
	if err != nil {
		t.Fatal(err)
	}
	box, err := secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	reg := plugins.New(pool, map[string]*protocol.Manifest{"fake": m}, store, box)
	mgr := pluginhost.NewManager(nil, time.Minute, slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(mgr.Close)
	inst, err := reg.CreateInstance(ctx, plugins.NewInstance{Plugin: "fake", Name: new("Embed"),
		Config: map[string]any{"services": []string{protocol.MethodAIEmbed}}})
	if err != nil {
		t.Fatal(err)
	}
	lib := library.New(pool, store)
	return env{lib: lib, reg: reg, run: &Runner{Lib: lib, Registry: reg, Plugins: mgr}, inst: inst}
}

func (e env) card(t *testing.T, slug, data string) (int64, int64) {
	t.Helper()
	c, rev, err := e.lib.Cards.Create(context.Background(), library.DefaultLibrary, slug, "upload",
		library.Edit{Data: json.RawMessage(data), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	return c.ID, rev.ID
}

func TestEmbedJobDoesNothingWithoutEmbedInstance(t *testing.T) {
	e := newEnv(t)
	e.card(t, "brakka", `{"name": "Brakka"}`)
	if err := e.run.Run(context.Background(), &jobs.Run{}); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.lib.Unembedded(context.Background(), e.inst, 10); len(got) != 1 {
		t.Fatalf("a run without an embed instance embedded the card: %+v", got)
	}
}

func TestEmbedJobEmbedsEveryCardInBatches(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for i := range batchSize + 3 {
		e.card(t, "card-"+strconv.Itoa(i), `{"name": "Card `+strconv.Itoa(i)+`"}`)
	}
	if err := e.reg.SetSetting(ctx, InstanceKey, strconv.FormatInt(e.inst, 10)); err != nil {
		t.Fatal(err)
	}
	r := &jobs.Run{}
	if err := e.run.Run(ctx, r); err != nil {
		t.Fatal(err)
	}
	if got, err := e.lib.Unembedded(ctx, e.inst, 100); err != nil || len(got) != 0 {
		t.Fatalf("left unembedded: %d, %v", len(got), err)
	}
}

func TestEmbedJobReembedsEditedCard(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	id, rev := e.card(t, "brakka", `{"name": "Brakka"}`)
	if err := e.reg.SetSetting(ctx, InstanceKey, strconv.FormatInt(e.inst, 10)); err != nil {
		t.Fatal(err)
	}
	if err := e.run.Run(ctx, &jobs.Run{}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.lib.Cards.Update(ctx, id, rev, library.Edit{Data: json.RawMessage(`{"name": "Brakka the Bold"}`), Source: "ui"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.lib.Unembedded(ctx, e.inst, 10); len(got) != 1 {
		t.Fatalf("an edited card isn't due: %+v", got)
	}
	if err := e.run.Run(ctx, &jobs.Run{}); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.lib.Unembedded(ctx, e.inst, 10); len(got) != 0 {
		t.Fatalf("still due after a run: %+v", got)
	}
}

func TestEmbedJobIgnoresADeletedEmbedInstance(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.card(t, "brakka", `{"name": "Brakka"}`)
	if err := e.reg.SetSetting(ctx, InstanceKey, "999999"); err != nil {
		t.Fatal(err)
	}
	if err := e.run.Run(ctx, &jobs.Run{}); err != nil {
		t.Fatalf("a run naming a missing instance: %v, want nil", err)
	}
}
```

- [ ] **Step 2: Run them and see them fail**

```bash
go test ./internal/embed
```

Expected: a compile error, the package has no non-test files.

- [ ] **Step 3: Implement**

`internal/embed/embed.go`:

```go
// Package embed keeps every card's vector from the embed instance current,
// so cards can be ranked by meaning.
package embed

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"

	"github.com/inkandale/innkeeper/internal/jobs"
	"github.com/inkandale/innkeeper/internal/library"
	"github.com/inkandale/innkeeper/internal/pluginhost"
	"github.com/inkandale/innkeeper/internal/plugins"
)

// Kind is the job kind that embeds cards whose vector is missing or stale.
const Kind = "embed"

// InstanceKey is the setting naming the ai instance whose ai.embed every
// library's vectors come from. One instance for all libraries, since
// vectors from different models can't be compared.
const InstanceKey = "embed_instance"

// batchSize is how many texts go in one ai.embed call.
const batchSize = 32

// Runner is the jobs.Handler for Kind.
type Runner struct {
	Lib      *library.Library
	Registry *plugins.Registry
	Plugins  *pluginhost.Manager
}

// Run embeds batches until no card is due. With no usable embed instance it
// does nothing; the job runs again when one is set.
func (rn *Runner) Run(ctx context.Context, r *jobs.Run) error {
	inst, err := rn.instance(ctx)
	if err != nil || inst == 0 {
		return err
	}
	done := 0
	for {
		todo, err := rn.Lib.Unembedded(ctx, inst, batchSize)
		if err != nil || len(todo) == 0 {
			return err
		}
		texts := make([]string, len(todo))
		for i, c := range todo {
			texts[i] = c.Text
		}
		vectors, err := rn.Registry.Embed(ctx, rn.Plugins, inst, texts)
		if err != nil {
			return fmt.Errorf("embed %d cards: %w", len(todo), err)
		}
		if err := rn.Lib.SaveEmbeddings(ctx, inst, todo, vectors); err != nil {
			return err
		}
		done += len(todo)
		r.SetProgress(fmt.Appendf(nil, `{"embedded": %d}`, done))
	}
}

// instance is the embed instance's ID, or 0 when the setting is empty or
// names an instance that is gone or disabled.
func (rn *Runner) instance(ctx context.Context) (int64, error) {
	v, err := rn.Registry.Setting(ctx, InstanceKey)
	if err != nil || v == "" {
		return 0, err
	}
	id, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("setting %s = %q: %w", InstanceKey, v, err)
	}
	info, err := rn.Registry.GetInstance(ctx, id)
	if errors.Is(err, plugins.ErrNotFound) || (err == nil && info.Disabled) {
		return 0, nil
	}
	return id, err
}

// Enqueue makes sure an embed job will run. A failure is only logged; the
// hourly run catches up.
func Enqueue(ctx context.Context, q *jobs.Queue, log *slog.Logger) {
	if _, err := q.EnqueueOnce(context.WithoutCancel(ctx), jobs.Spec{Kind: Kind}); err != nil {
		log.Error("enqueue embedding", "err", err)
	}
}
```

Check that `InstanceInfo` has a `Disabled` field (`libsettings.Enrichers`
uses `i.Disabled`, so it does).

- [ ] **Step 4: Run the tests**

```bash
go test -race ./internal/embed
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/embed
git commit -m "feat(embed): embed cards whose vector is missing or stale"
```

---

### Task 5: Run the job

**Files:** `cmd/innkeeper/worker.go`, `internal/importer/importer.go`

- [ ] **Step 1: Register the handler and schedule**

In `cmd/innkeeper/worker.go`, import `"github.com/inkandale/innkeeper/internal/embed"`,
then after `enricher := ...`:

```go
	embedder := &embed.Runner{Lib: svc.lib, Registry: svc.registry, Plugins: mgr}
```

Add to `Handlers`:

```go
			embed.Kind:               embedder.Run,
```

Add to `Schedules`:

```go
			// Catches edits, restores, and pulls; imports and the setting queue it at once.
			{Kind: embed.Kind, Every: time.Hour},
```

- [ ] **Step 2: Queue it after each import**

In `internal/importer/importer.go`, where the import queues detection:

```go
	if !s.bulk {
		EnqueueDetect(ctx, im.Queue, im.Log)
		embed.Enqueue(ctx, im.Queue, im.Log)
	}
```

Import `"github.com/inkandale/innkeeper/internal/embed"`. If that creates an
import cycle (`embed` imports nothing from `importer`, so it shouldn't),
stop and report.

A bulk import's children skip this, as they skip detection. Check how the
bulk parent queues detection when it finishes
(`grep -n "EnqueueDetect\|bulk" internal/importer/importer.go`) and queue
`embed.Enqueue` in the same place.

- [ ] **Step 3: Build and run the importer tests**

```bash
go build ./... && go test ./internal/importer
```

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add cmd/innkeeper/worker.go internal/importer/importer.go
git commit -m "feat(embed): run the embed job after imports and hourly"
```

---

### Task 6: The `embed_instance` setting

**Files:** `internal/api/settings.go`, `internal/api/settings_test.go`

- [ ] **Step 1: Write the failing tests**

If `internal/api/settings_test.go` doesn't exist, create it in package `api`.
The test env's `fake` plugin lacks the `ai` capability, so add one, as
`newEnrichEnv` adds `judge` (read `internal/api/enrichments_test.go` for how
it builds its manifest and copy that approach):

```go
func TestSettingEmbedInstanceQueuesEmbedJob(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	ai, err := protocol.LoadManifest(testutil.FakePluginWith(t, []string{"ai"}))
	if err != nil {
		t.Fatal(err)
	}
	ai.Name = "brain"
	e.manifests["brain"] = ai
	inst, err := e.s.Registry.CreateInstance(ctx, plugins.NewInstance{Plugin: "brain", Name: new("Brain"),
		Config: map[string]any{"services": []string{protocol.MethodAIEmbed}}})
	if err != nil {
		t.Fatal(err)
	}

	var s settings
	e.call("PATCH", "/settings", map[string]any{"embed_instance": inst}, 200, &s)
	if s.EmbedInstance == nil || *s.EmbedInstance != inst {
		t.Fatalf("settings = %+v", s)
	}
	var queued int
	if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM job WHERE kind = $1 AND status = 'queued'`, embed.Kind).Scan(&queued); err != nil {
		t.Fatal(err)
	}
	if queued != 1 {
		t.Fatalf("embed jobs queued = %d, want 1", queued)
	}

	e.call("PATCH", "/settings", map[string]any{"embed_instance": e.importID}, 400, nil)
	e.call("PATCH", "/settings", map[string]any{"embed_instance": nil}, 200, &s)
	if s.EmbedInstance != nil {
		t.Fatalf("after clearing: %+v", s)
	}
}

func TestSettingEmbedInstanceDropsItsVectors(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	ai, err := protocol.LoadManifest(testutil.FakePluginWith(t, []string{"ai"}))
	if err != nil {
		t.Fatal(err)
	}
	ai.Name = "brain"
	e.manifests["brain"] = ai
	inst, err := e.s.Registry.CreateInstance(ctx, plugins.NewInstance{Plugin: "brain", Name: new("Brain")})
	if err != nil {
		t.Fatal(err)
	}
	e.createCard(`{"name": "Brakka"}`)
	todo, err := e.s.Lib.Unembedded(ctx, inst, 10)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.s.Lib.SaveEmbeddings(ctx, inst, todo, [][]float32{{1, 2}}); err != nil {
		t.Fatal(err)
	}
	e.call("PATCH", "/settings", map[string]any{"embed_instance": inst}, 200, nil)
	if got, err := e.s.Lib.Unembedded(ctx, inst, 10); err != nil || len(got) != 1 {
		t.Fatalf("after setting: %+v, %v; want the card due again", got, err)
	}
}
```

If `FakePluginWith` names its manifest `fake` and the command depends on
the name, copy how `newEnv` builds `target` (`-name=` flag) instead of only
setting `ai.Name`.

- [ ] **Step 2: Run them and see them fail**

```bash
go test ./internal/api -run TestSettingEmbedInstance
```

Expected: a compile error, `s.EmbedInstance undefined`.

- [ ] **Step 3: Implement**

In `internal/api/settings.go`, add to `settings`:

```go
	EmbedInstance *int64 `json:"embed_instance" doc:"The ai instance whose ai.embed every library's card vectors come from, or null for none."`
```

Add to `patchSettingsIn.Body`:

```go
		EmbedInstance optional[int64] `json:"embed_instance,omitempty" doc:"An ai instance's ID, or null to clear it. Setting it, even to its current value, embeds every card again."`
```

In `s.settings`, after the score instance:

```go
	if v, err := s.Registry.Setting(ctx, embed.InstanceKey); err != nil {
		return nil, s.fail(err)
	} else if id, err := strconv.ParseInt(v, 10, 64); err == nil {
		out.Body.EmbedInstance = &id
	}
```

In the PATCH handler, after the score instance:

```go
			if in.Body.EmbedInstance.Sent {
				if err := s.setEmbedInstance(ctx, in.Body.EmbedInstance.Value); err != nil {
					return nil, err
				}
			}
```

And the method, beside `setScoreInstance` in shape:

```go
// setEmbedInstance stores the embed instance, or clears it for nil. Setting
// one drops its vectors, so a model changed in its config takes effect.
func (s *Server) setEmbedInstance(ctx context.Context, id *int64) error {
	if id == nil {
		return s.fail(s.Registry.SetSetting(ctx, embed.InstanceKey, ""))
	}
	info, err := s.Registry.GetInstance(ctx, *id)
	if err != nil {
		return s.fail(err)
	}
	if !s.Registry.HasCapability(info.Plugin, protocol.CapabilityAI) {
		return huma.Error400BadRequest(fmt.Sprintf("plugin %s offers no AI services", info.Plugin))
	}
	if err := s.Lib.DeleteEmbeddings(ctx, *id); err != nil {
		return s.fail(err)
	}
	if err := s.Registry.SetSetting(ctx, embed.InstanceKey, strconv.FormatInt(*id, 10)); err != nil {
		return s.fail(err)
	}
	embed.Enqueue(ctx, s.Queue, s.Log)
	return nil
}
```

Imports: `fmt`, `strconv`, `internal/embed`, `protocol`. Check that
`protocol.CapabilityAI` is the constant's name (`grep -n CapabilityAI protocol/*.go`).

A deleted instance's ID stays in the setting. `GET /settings` then shows
an ID that `GET /instances` lacks. The picker in Task 7 shows "None" for
it, and the job ignores it. That's acceptable; don't add cleanup code.

- [ ] **Step 4: Run the tests and regenerate**

```bash
go test ./internal/api -run 'TestSetting' && (cd web && npm run gen)
```

Expected: PASS, and `schema.d.ts` gains `embed_instance`.

- [ ] **Step 5: Commit**

```bash
git add internal/api/settings.go internal/api/settings_test.go web/openapi.json web/src/api/schema.d.ts
git commit -m "feat(api): pick the embed instance in settings"
```

---

### Task 7: The embed instance picker

**Files:** create `web/src/settings/EmbedFrom.tsx`,
`web/src/settings/EmbedFrom.test.tsx`; modify `web/src/settings/Plugins.tsx`

- [ ] **Step 1: Write the failing test**

Read `web/src/settings/*.test.tsx` for one that renders a settings
component with a mocked `fetch` (search for `json(` helpers like the one in
`web/src/card/Card.test.tsx`) and copy its render and mock setup. The test:

- mocks `GET /api/instances?offers=ai.embed` to return
  `[{ id: 5, label: "Embedder", plugin: "brain" }]`, and `GET /api/settings`
  to return `{ embed_instance: null, ... }`;
- selects "Embedder" in the select labelled "Embeddings from";
- asserts a `PATCH /api/settings` with body `{ embed_instance: 5 }`.

Also add a case where the instances list is empty and assert nothing
renders.

- [ ] **Step 2: Run it and see it fail**

```bash
cd web && npx vitest run src/settings/EmbedFrom.test.tsx
```

Expected: FAIL, module not found.

- [ ] **Step 3: Implement**

`web/src/settings/EmbedFrom.tsx`:

```tsx
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, must } from "../api/client";
import { settingsQuery } from "../api/queries";
import { SaveStatus } from "./SaveStatus";

/** EmbedFrom picks the ai instance whose ai.embed every library's card vectors come from. */
export function EmbedFrom() {
  const qc = useQueryClient();
  const embedders = useQuery({
    queryKey: ["instances", "offers", "ai.embed"],
    queryFn: async () => must(await api.GET("/instances", { params: { query: { offers: "ai.embed" } } })),
  });
  const settings = useQuery(settingsQuery);
  const save = useMutation({
    mutationFn: async (embed_instance: number | null) => {
      qc.setQueryData(settingsQuery.queryKey, must(await api.PATCH("/settings", { body: { embed_instance } })));
    },
    meta: { toast: false },
  });
  if (!embedders.data?.length) return null;
  const saved = settings.data?.embed_instance;
  return (
    <section className="scores-from">
      <label title="The AI instance that turns every card into a vector, for similar cards and semantic search.">
        Embeddings from{" "}
        <select
          className="input"
          value={embedders.data.some((i) => i.id === saved) ? saved! : ""}
          disabled={!settings.data || save.isPending}
          onChange={(e) => save.mutate(e.target.value === "" ? null : Number(e.target.value))}
        >
          <option value="">None</option>
          {embedders.data.map((i) => (
            <option key={i.id} value={i.id}>
              {i.label}
            </option>
          ))}
        </select>
      </label>{" "}
      <SaveStatus save={save} />
      <p className="muted">
        Every card in every library is sent to this instance to find similar cards. Picking it again embeds every
        card again.
      </p>
    </section>
  );
}
```

In `web/src/settings/Plugins.tsx`, import it and render `<EmbedFrom />`
right after `<ScoresFrom />` (line 235).

- [ ] **Step 4: Run the web checks**

```bash
cd web && npm run typecheck && npx vitest run src/settings
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/settings/EmbedFrom.tsx web/src/settings/EmbedFrom.test.tsx web/src/settings/Plugins.tsx
git commit -m "feat(web): pick the embed instance under Plugins"
```

---

### Task 8: Verify and check production

- [ ] **Step 1: Run what CI runs**

```bash
go vet ./...
go test -race ./...
cd web && npm run typecheck && npm test
```

Expected: every Go package `ok` (none skipped for want of a database), and
the web checks pass.

- [ ] **Step 2: Check that production can create the extension**

Production runs Crunchy PostgreSQL 16 (`~/git/homelab/svc/innkeeper/values.yaml`).
Don't connect to it or change anything there. Report to the owner:

- Crunchy's images ship pgvector. pgvector's control file marks it
  `trusted`, so the database owner can run `CREATE EXTENSION vector`
  without superuser.
- Before deploying, ask the owner to confirm with
  `SELECT * FROM pg_available_extensions WHERE name = 'vector';` on the
  production database. If no row comes back, the migration will fail on
  startup and the deploy must wait.

- [ ] **Step 3: Read the whole diff once more**

```bash
git diff origin/develop --stat && git diff origin/develop
```

Look for dead code, stale names (`SimilarCards`, `/similar`), and anything
the plan didn't ask for.
