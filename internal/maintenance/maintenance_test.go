package maintenance_test

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/inkandale/innkeeper/internal/blobstore"
	"github.com/inkandale/innkeeper/internal/jobs"
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
	if rows := enrichmentRows(t, pool); rows != 2 {
		t.Fatalf("%d rows left, want 2", rows)
	}
}

func TestTrimHistoryKeepsTheHeadRevisionsValue(t *testing.T) {
	m, lib, pool := newMaintenance(t)
	ctx := context.Background()
	inst := instanceRow(t, pool)
	card, old := cardRow(t, lib)
	head, err := lib.Cards.Update(ctx, card, old, library.Edit{Data: json.RawMessage(`{"name": "Card 2"}`), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range []library.NewEnrichment{{CardID: card, RevisionID: head.ID, Key: "q", Value: json.RawMessage(`"head"`)},
		{CardID: card, RevisionID: old, Key: "q", Value: json.RawMessage(`"old"`)}} {
		if err := lib.AddEnrichments(ctx, inst, []library.NewEnrichment{e}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `UPDATE enrichment SET created_at = now() + interval '1 hour' WHERE revision_id = $1`, old); err != nil {
		t.Fatal(err)
	}
	if n, err := m.TrimHistory(ctx, 1); err != nil || n != 1 {
		t.Fatalf("trimmed %d, %v", n, err)
	}
	vals, err := lib.Enrichments(ctx, card, false)
	if err != nil || len(vals) != 1 || string(vals[0].Value) != `"head"` {
		t.Fatalf("current values = %+v, %v", vals, err)
	}
}

func TestTrimHistoryRefusesKeepOutsideOneToAHundred(t *testing.T) {
	m, lib, pool := newMaintenance(t)
	ctx := context.Background()
	inst := instanceRow(t, pool)
	card, rev := cardRow(t, lib)
	for i := range 2 {
		if err := lib.AddEnrichments(ctx, inst, []library.NewEnrichment{{CardID: card, RevisionID: rev, Key: "q",
			Value: json.RawMessage(strconv.Itoa(i))}}); err != nil {
			t.Fatal(err)
		}
	}
	for _, keep := range []int{-1, 0, 101} {
		if _, err := m.TrimHistory(ctx, keep); !errors.Is(err, library.ErrInvalid) {
			t.Errorf("keep %d: %v", keep, err)
		}
	}
	if rows := enrichmentRows(t, pool); rows != 2 {
		t.Fatalf("%d rows left, want 2", rows)
	}
}

func TestRunnerFailsAPayloadWithoutKeep(t *testing.T) {
	m, _, _ := newMaintenance(t)
	rn := &maintenance.Runner{M: m}
	err := rn.Run(context.Background(), &jobs.Run{Job: jobs.Job{Payload: json.RawMessage(`{"action": "trim-history"}`)}})
	if !errors.Is(err, library.ErrInvalid) {
		t.Fatalf("run = %v", err)
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

func TestRemoveStaleVectorsWithoutAVectorInstanceRemovesNothing(t *testing.T) {
	m, lib, pool := newMaintenance(t)
	ctx := context.Background()
	card, rev := cardRow(t, lib)
	if err := lib.SaveVectors(ctx, instanceRow(t, pool), []library.NewVector{{CardID: card, RevisionID: rev, Space: "s", Vector: []float32{1, 0}}}); err != nil {
		t.Fatal(err)
	}
	if n, err := m.RemoveStaleVectors(ctx, 0, ""); err != nil || n != 0 {
		t.Fatalf("removed %d, %v", n, err)
	}
	var left int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM card_embedding`).Scan(&left); err != nil || left != 1 {
		t.Fatalf("%d vectors left, %v", left, err)
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
	if err := m.Purge(ctx, 30, func(*library.Kind, int) {}); err != nil {
		t.Fatal(err)
	}
	if _, err := lib.Cards.Get(ctx, old); !errors.Is(err, library.ErrNotFound) {
		t.Fatalf("old card: %v", err)
	}
	if _, err := lib.Cards.Get(ctx, recent); err != nil {
		t.Fatalf("recent card: %v", err)
	}
}

func TestPurgeKeepsACardUnarchivedAfterThePreview(t *testing.T) {
	m, lib, pool := newMaintenance(t)
	ctx := context.Background()
	card, _ := cardRow(t, lib)
	if _, err := pool.Exec(ctx, `UPDATE card SET archived_at = now() - interval '40 days' WHERE id = $1`, card); err != nil {
		t.Fatal(err)
	}
	if p, _ := m.Preview(ctx, 3, 30); p.Purge.Cards != 1 {
		t.Fatalf("preview %+v", p.Purge)
	}
	if err := lib.Cards.SetArchived(ctx, card, false); err != nil {
		t.Fatal(err)
	}
	if err := m.Purge(ctx, 30, func(*library.Kind, int) {}); err != nil {
		t.Fatal(err)
	}
	if _, err := lib.Cards.Get(ctx, card); err != nil {
		t.Fatalf("unarchived card: %v", err)
	}
}

func TestPurgeRefusesDaysBelowOne(t *testing.T) {
	m, lib, pool := newMaintenance(t)
	ctx := context.Background()
	card, _ := cardRow(t, lib)
	if _, err := pool.Exec(ctx, `UPDATE card SET archived_at = now() - interval '40 days' WHERE id = $1`, card); err != nil {
		t.Fatal(err)
	}
	for _, days := range []int{-1, 0} {
		if err := m.Purge(ctx, days, func(*library.Kind, int) {}); !errors.Is(err, library.ErrInvalid) {
			t.Errorf("days %d: %v", days, err)
		}
	}
	if _, err := lib.Cards.Get(ctx, card); err != nil {
		t.Fatalf("archived card: %v", err)
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
	if err := m.Purge(ctx, 30, func(*library.Kind, int) {}); err != nil {
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

func TestVacuumHintsAtOwnershipOnlyForPermissionErrors(t *testing.T) {
	m, _, _ := newMaintenance(t)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := m.Vacuum(cancelled); err == nil || strings.Contains(err.Error(), "must own") {
		t.Fatalf("vacuum with a cancelled context = %v, want an error without the ownership hint", err)
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

func enrichmentRows(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM enrichment`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
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
