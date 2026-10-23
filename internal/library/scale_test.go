package library

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/inkandale/innkeeper/internal/blobstore"
	"github.com/inkandale/innkeeper/internal/testdb"
)

// tracer records every query a pool runs, so the scale check can explain
// the SQL Search really sends.
type tracer struct{ queries []tracedQuery }

type tracedQuery struct {
	sql  string
	args []any
}

func (tr *tracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	tr.queries = append(tr.queries, tracedQuery{d.SQL, d.Args})
	return ctx
}

func (tr *tracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// TestScaleExplain builds a 300k-card library beside a small one and prints
// EXPLAIN ANALYZE for search, facets, and similar lookup. It takes minutes,
// so CI never runs it.
func TestScaleExplain(t *testing.T) {
	if os.Getenv("INNKEEPER_SCALE_TEST") != "1" {
		t.Skip("set INNKEEPER_SCALE_TEST=1 to build a 300k-card library and print query plans")
	}
	pool := testdb.New(t)
	ctx := context.Background()
	generateScaleLibrary(t, pool, 300_000)

	tr := &tracer{}
	cfg := pool.Config()
	cfg.ConnConfig.Tracer = tr
	traced, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer traced.Close()
	lib := New(traced, &blobstore.Store{Name: "library", Root: t.TempDir()})

	explain := func(title string, run func() error) {
		t.Helper()
		tr.queries = nil
		if err := run(); err != nil {
			t.Fatalf("%s: %v", title, err)
		}
		fmt.Printf("\n######## %s\n", title)
		for _, q := range tr.queries {
			fmt.Printf("\n-- %s\n", strings.Join(strings.Fields(q.sql), " "))
			rows, err := pool.Query(ctx, "EXPLAIN (ANALYZE, BUFFERS) "+q.sql, q.args...)
			if err != nil {
				t.Fatalf("%s: explain: %v", title, err)
			}
			lines, err := pgx.CollectRows(rows, pgx.RowTo[string])
			if err != nil {
				t.Fatalf("%s: explain: %v", title, err)
			}
			fmt.Println(strings.Join(lines, "\n"))
		}
	}
	const archive = 2
	search := func(q Query) func() error {
		return func() error {
			q.Library = archive
			_, err := lib.Search(ctx, Card, q)
			return err
		}
	}
	explain("text search, common words", search(Query{Text: "dwarf blacksmith"}))
	explain("text search, rare word", search(Query{Text: "w4321"}))
	explain("sort only, newest first", search(Query{}))
	explain("sort only, by name", search(Query{Sort: "name"}))
	explain("sort only, recently updated", search(Query{Sort: "updated"}))
	first, err := lib.Search(ctx, Card, Query{Library: archive, Page: Page{Size: 50}})
	if err != nil {
		t.Fatal(err)
	}
	explain("next page by cursor, newest first", search(Query{Page: Page{Size: 50, Cursor: first.NextCursor}}))
	explain("tags and sources facets", search(Query{Facets: []string{"tags", "sources"}, Page: Page{Size: 1}}))
	explain("tags facet with text", search(Query{Text: "dwarf", Facets: []string{"tags"}, Page: Page{Size: 1}}))
	explain("family grouping", search(Query{Group: "family"}))
	explain("rare tag filter", search(Query{Filter: &Filter{Tag: ptr("tag450")}}))
	explain("common tag filter, grouped", search(Query{Filter: &Filter{Tag: ptr("tag0")}, Group: "family"}))
	explain("small library beside the big one", func() error {
		_, err := lib.Search(ctx, Card, Query{Text: "dwarf"})
		return err
	})

	var card int64
	var hashes Hashes
	if err := pool.QueryRow(ctx, `SELECT c.id, r.content_hash, r.text_simhash, b.avatar_phash
		FROM card c JOIN card_revision r ON r.id = c.head_revision_id JOIN blob b ON b.sha256 = r.avatar_blob
		WHERE c.library_id = $1 ORDER BY c.id LIMIT 1 OFFSET 1000`, archive).Scan(&card, &hashes.ContentHash, &hashes.TextSimhash, &hashes.ImagePhash); err != nil {
		t.Fatal(err)
	}
	explain("similar cards", func() error {
		_, err := lib.DuplicateCards(ctx, card, NearDistance)
		return err
	})
	explain("match hashes, 20 items", func() error {
		probes := make([]Hashes, 20)
		for i := range probes {
			probes[i] = hashes
		}
		_, err := lib.Cards.MatchHashes(ctx, archive, probes, NearDistance)
		return err
	})
}

// generateScaleLibrary adds library 2 with n cards, each with tags, a
// source, random hashes, and an avatar; one in six has a second revision,
// and one in twenty is a variant of the card before it. Library 1 gets 200 cards.
func generateScaleLibrary(t *testing.T, pool *pgxpool.Pool, n int) {
	t.Helper()
	ctx := context.Background()
	// words is a description of about count words, three in ten from a
	// short list of common ones; g correlates it so each row gets its own.
	words := func(count int) string {
		return fmt.Sprintf(`(SELECT string_agg(CASE WHEN random() < 0.3
			THEN (ARRAY['dwarf','elf','blacksmith','pirate','sailor','forge','tavern','knight','witch','dragon',
				'village','sword','ship','storm','queen','thief','guard','merchant','priest','wolf'])[1 + floor(random() * 20)::int]
			ELSE 'w' || floor(random() * 20000)::int END, ' ') FROM generate_series(1, %d) WHERE g > 0)`, count)
	}
	// random64 is a random hash with all 64 bits set at random; one float
	// has only 53.
	const random64 = `((floor(random() * 4294967296)::bigint << 32) | floor(random() * 4294967296)::bigint)`
	steps := []string{
		`INSERT INTO library (name) VALUES ('Archive')`,
		`INSERT INTO blob (sha256, size, mime, store, avatar_phash)
			SELECT lpad(to_hex(g), 64, '0'), 1, 'image/png', 'library', ` + random64 + `
			FROM generate_series(1, $1) g`,
		`INSERT INTO card (slug, provenance, library_id, archived_at)
			SELECT 'scale-' || g, 'plugin:chub-archive:' || g, CASE WHEN g <= 200 THEN 1 ELSE 2 END,
				CASE WHEN g % 50 = 0 THEN now() END
			FROM generate_series(1, $1 + 200) g`,
		`INSERT INTO card_revision (card_id, data, content_hash, text_simhash, avatar_blob, source)
			SELECT c.id, jsonb_build_object(
					'name', 'Name ' || (g % 9973),
					'description', ` + words(60) + `,
					'first_mes', ` + words(30) + `,
					'tags', (SELECT jsonb_agg(DISTINCT 'tag' || floor(power(random(), 3) * 500)::int) FROM generate_series(1, 3 + g % 6) WHERE g > 0)),
				md5(c.id::text), ` + random64 + `, CASE WHEN g <= $1 THEN lpad(to_hex(g), 64, '0') END, 'ui'
			FROM card c, LATERAL (SELECT substr(c.slug, 7)::int AS g) s`,
		`INSERT INTO card_revision (card_id, parent_id, data, content_hash, text_simhash, avatar_blob, source)
			SELECT card_id, id, data || '{"creator_notes": "v2"}', md5(card_id::text || 'v2'), text_simhash, avatar_blob, 'ui'
			FROM card_revision WHERE card_id % 6 = 0`,
		`UPDATE card c SET head_revision_id = r.id
			FROM (SELECT card_id, max(id) AS id FROM card_revision GROUP BY card_id) r WHERE r.card_id = c.id`,
		`INSERT INTO card_source (card_id, kind, ref, linked_by) SELECT id, 'chub', 'creator/' || id, 'auto' FROM card`,
		`UPDATE card SET variant_of_id = id - 1 WHERE id > 1 AND (id % 20 = 0 OR id % 400 = 1)`,
		`ANALYZE`,
	}
	for _, sql := range steps {
		args := []any{}
		if strings.Contains(sql, "$1") {
			args = append(args, n)
		}
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", strings.Fields(sql)[:3], err)
		}
	}
}
