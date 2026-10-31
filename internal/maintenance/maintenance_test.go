package maintenance_test

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/inkandale/innkeeper/internal/blobstore"
	"github.com/inkandale/innkeeper/internal/library"
	"github.com/inkandale/innkeeper/internal/maintenance"
	"github.com/inkandale/innkeeper/internal/testdb"
)

func TestTrimHistoryKeepsTheNewestValues(t *testing.T) {
	m, lib, pool := newMaintenance(t)
	ctx := context.Background()
	inst := instanceRow(t, pool)
	card, rev := cardRow(t, lib)
	for i := range 5 {
		if err := lib.AddEnrichments(ctx, inst, []library.NewEnrichment{{CardID: card, RevisionID: rev, Key: "q",
			Value: json.RawMessage(strconv.Itoa(i))}}); err != nil {
			t.Fatal(err)
		}
	}
	p, err := m.Preview(ctx, 2, 30)
	if err != nil || p.History.Trimmable != 3 {
		t.Fatalf("preview %+v, %v", p.History, err)
	}
	n, err := m.TrimHistory(ctx, 2)
	if err != nil || n != 3 {
		t.Fatalf("trimmed %d, %v", n, err)
	}
	vals, _ := lib.Enrichments(ctx, card, false)
	if len(vals) != 1 || string(vals[0].Value) != "4" {
		t.Fatalf("current values = %+v", vals)
	}
}

func TestRemoveStaleVectorsKeepsOnlyTheCurrentSpace(t *testing.T) {
	m, lib, pool := newMaintenance(t)
	ctx := context.Background()
	cur, other := instanceRow(t, pool), instanceRow(t, pool)
	a, ra := cardRow(t, lib)
	b, rb := cardRow(t, lib)
	c, rc := cardRow(t, lib)
	save := func(inst, card, rev int64, space string) {
		if err := lib.SaveVectors(ctx, inst, []library.NewVector{{CardID: card, RevisionID: rev, Space: space, Vector: []float32{1, 0}}}); err != nil {
			t.Fatal(err)
		}
	}
	save(cur, a, ra, "new")
	save(cur, b, rb, "old")
	save(other, c, rc, "new")
	n, err := m.RemoveStaleVectors(ctx, cur, "new")
	if err != nil || n != 2 {
		t.Fatalf("removed %d, %v", n, err)
	}
}

func TestPurgeDeletesOnlyCardsArchivedLongEnoughAgo(t *testing.T) {
	m, lib, pool := newMaintenance(t)
	ctx := context.Background()
	old, _ := cardRow(t, lib)
	recent, _ := cardRow(t, lib)
	for id, ago := range map[int64]string{old: "31 days", recent: "29 days"} {
		if _, err := pool.Exec(ctx, `UPDATE card SET archived_at = now() - $2::interval WHERE id = $1`, id, ago); err != nil {
			t.Fatal(err)
		}
	}
	p, _ := m.Preview(ctx, 3, 30)
	if p.Purge.Cards != 1 {
		t.Fatalf("preview %+v", p.Purge)
	}
	if _, err := m.Purge(ctx, 30, func(int) {}); err != nil {
		t.Fatal(err)
	}
	if _, err := lib.Cards.Get(ctx, old); !errors.Is(err, library.ErrNotFound) {
		t.Fatalf("old card: %v", err)
	}
	if _, err := lib.Cards.Get(ctx, recent); err != nil {
		t.Fatalf("recent card: %v", err)
	}
}

func TestPurgeKeepsALorebookALiveCardLinks(t *testing.T) {
	m, lib, pool := newMaintenance(t)
	ctx := context.Background()
	card, _ := cardRow(t, lib)
	linked := lorebookRow(t, lib)
	alone := lorebookRow(t, lib)
	if err := lib.LinkLorebook(ctx, card, linked); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE lorebook SET archived_at = now() - interval '40 days'`); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Purge(ctx, 30, func(int) {}); err != nil {
		t.Fatal(err)
	}
	if _, err := lib.Lorebooks.Get(ctx, linked); err != nil {
		t.Fatalf("linked lorebook: %v", err)
	}
	if _, err := lib.Lorebooks.Get(ctx, alone); !errors.Is(err, library.ErrNotFound) {
		t.Fatalf("lone lorebook: %v", err)
	}
}

func TestVacuumRunsOutsideATransaction(t *testing.T) {
	m, _, _ := newMaintenance(t)
	if size, err := m.Vacuum(context.Background()); err != nil || size <= 0 {
		t.Fatalf("vacuum = %d, %v", size, err)
	}
}

func TestRevisionsStayImmutableOutsideADelete(t *testing.T) {
	_, lib, pool := newMaintenance(t)
	card, _ := cardRow(t, lib)
	if _, err := pool.Exec(context.Background(), `DELETE FROM card_revision WHERE card_id = $1`, card); err == nil {
		t.Fatal("deleted a revision without innkeeper.delete")
	}
}

func newMaintenance(t *testing.T) (*maintenance.Maintenance, *library.Library, *pgxpool.Pool) {
	t.Helper()
	pool := testdb.New(t)
	lib := library.New(pool, &blobstore.Store{Name: "library", Root: t.TempDir()})
	return maintenance.New(pool, lib), lib, pool
}

func instanceRow(t *testing.T, pool *pgxpool.Pool) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO plugin_instance (plugin, name, config) VALUES ('fake', 'i' || nextval('plugin_instance_id_seq'), '{}') RETURNING id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

var slugs int

// cardRow creates a card and returns its ID and head revision ID.
func cardRow(t *testing.T, lib *library.Library) (int64, int64) {
	t.Helper()
	slugs++
	c, rev, err := lib.Cards.Create(context.Background(), library.DefaultLibrary, "card-"+strconv.Itoa(slugs), "upload",
		library.Edit{Data: json.RawMessage(`{"name": "Card"}`), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	return c.ID, rev.ID
}

func lorebookRow(t *testing.T, lib *library.Library) int64 {
	t.Helper()
	slugs++
	l, _, err := lib.Lorebooks.Create(context.Background(), library.DefaultLibrary, "lore-"+strconv.Itoa(slugs), "upload",
		library.Edit{Data: json.RawMessage(`{"name": "Lore", "entries": []}`), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	return l.ID
}
