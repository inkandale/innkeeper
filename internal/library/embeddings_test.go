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

func TestDeleteCardsDropsItsEmbeddings(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	inst := newInstanceRow(t, lib)
	card, _ := newCard(t, lib, "brakka", `{"name": "Brakka"}`)
	embedCards(t, lib, inst, map[int64][]float32{card: {1, 2}})
	if _, _, err := lib.DeleteCards(ctx, []int64{card}); err != nil {
		t.Fatalf("delete card: %v", err)
	}
	var n int
	if err := lib.pool.QueryRow(ctx, `SELECT count(*) FROM card_embedding`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("embeddings left = %d, %v", n, err)
	}
}

func TestHasVectorsReadsOnceOnSuccess(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	if ok, err := lib.HasVectors(ctx); err != nil || !ok {
		t.Fatalf("HasVectors = %v, %v", ok, err)
	}
	if _, err := lib.pool.Exec(ctx, `DROP TABLE card_embedding CASCADE`); err != nil {
		t.Fatal(err)
	}
	if ok, err := lib.HasVectors(ctx); err != nil || !ok {
		t.Fatalf("cached HasVectors = %v, %v", ok, err)
	}
	fresh := New(lib.pool, lib.blobs)
	if ok, err := fresh.HasVectors(ctx); err != nil || ok {
		t.Fatalf("HasVectors without the table = %v, %v", ok, err)
	}
}

func TestHasVectorsDoesNotCacheAnError(t *testing.T) {
	lib := newLibrary(t)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := lib.HasVectors(cancelled); err == nil {
		t.Fatal("cancelled context: want an error")
	}
	if ok, err := lib.HasVectors(context.Background()); err != nil || !ok {
		t.Fatalf("retry = %v, %v", ok, err)
	}
}
