package library

import (
	"context"
	"encoding/json"
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

	got, err := lib.Unembedded(ctx, inst, nil, 0, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != (EmbedText{CardID: brakka, RevisionID: rev, Text: "Brakka\nA dwarf smith."}) || got[1].CardID != lia {
		t.Fatalf("unembedded = %+v", got)
	}

	if err := lib.SaveEmbeddings(ctx, inst, []EmbedText{got[0], got[1]}, [][]float32{{1, 0}, {0, 1}}); err != nil {
		t.Fatal(err)
	}
	if got, err = lib.Unembedded(ctx, inst, nil, 0, 0, 10); err != nil || len(got) != 0 {
		t.Fatalf("after saving: %+v, %v", got, err)
	}

	if _, err := lib.Cards.Update(ctx, lia, liaRev, edit(`{"name": "Lia", "personality": "Grumpy."}`)); err != nil {
		t.Fatal(err)
	}
	if got, err = lib.Unembedded(ctx, inst, nil, 0, 0, 10); err != nil || len(got) != 1 || got[0].CardID != lia || got[0].Text != "Lia\nGrumpy." {
		t.Fatalf("after an edit: %+v, %v", got, err)
	}
}

func TestUnembeddedSkipsCardsWithNoText(t *testing.T) {
	lib := newLibrary(t)
	inst := newInstanceRow(t, lib)
	newCard(t, lib, "blank", `{"first_mes": "Only a greeting."}`)
	if got, err := lib.Unembedded(context.Background(), inst, nil, 0, 0, 10); err != nil || len(got) != 0 {
		t.Fatalf("unembedded = %+v, %v", got, err)
	}
}

func TestClearEmbeddingsDropsEveryInstancesVectors(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	newCard(t, lib, "brakka", `{"name": "Brakka"}`)
	var other int64
	if err := lib.pool.QueryRow(ctx,
		`INSERT INTO plugin_instance (plugin, config) VALUES ('other', '{}') RETURNING id`).Scan(&other); err != nil {
		t.Fatal(err)
	}
	for _, inst := range []int64{newInstanceRow(t, lib), other} {
		todo, err := lib.Unembedded(ctx, inst, nil, 0, 0, 10)
		if err != nil {
			t.Fatal(err)
		}
		if err := lib.SaveEmbeddings(ctx, inst, todo, [][]float32{{1, 2}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := lib.ClearEmbeddings(ctx); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := lib.pool.QueryRow(ctx, `SELECT count(*) FROM card_embedding`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("embeddings left = %d, %v", n, err)
	}
}

func TestDeleteCardsDropsItsEmbeddings(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	inst := newInstanceRow(t, lib)
	card, _ := newCard(t, lib, "brakka", `{"name": "Brakka"}`)
	todo, err := lib.Unembedded(ctx, inst, nil, 0, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.SaveEmbeddings(ctx, inst, todo, [][]float32{{1, 2}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := lib.DeleteCards(ctx, []int64{card}); err != nil {
		t.Fatalf("delete card: %v", err)
	}
	var n int
	if err := lib.pool.QueryRow(ctx, `SELECT count(*) FROM card_embedding`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("embeddings left = %d, %v", n, err)
	}
}

func TestSaveEmbeddingsRejectsMismatchedVectors(t *testing.T) {
	lib := newLibrary(t)
	inst := newInstanceRow(t, lib)
	err := lib.SaveEmbeddings(context.Background(), inst, []EmbedText{{CardID: 1, RevisionID: 1}}, nil)
	if err == nil {
		t.Fatal("want an error for 0 vectors and 1 card")
	}
}

func TestUnembeddedSkipsWhitespaceOnlyText(t *testing.T) {
	lib := newLibrary(t)
	inst := newInstanceRow(t, lib)
	newCard(t, lib, "blank", `{"name": "   "}`)
	if got, err := lib.Unembedded(context.Background(), inst, nil, 0, 0, 10); err != nil || len(got) != 0 {
		t.Fatalf("unembedded = %+v, %v", got, err)
	}
}

func TestUnembeddedIsPerInstance(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	a := newInstanceRow(t, lib)
	var b int64
	if err := lib.pool.QueryRow(ctx,
		`INSERT INTO plugin_instance (plugin, config) VALUES ('other', '{}') RETURNING id`).Scan(&b); err != nil {
		t.Fatal(err)
	}
	newCard(t, lib, "brakka", `{"name": "Brakka"}`)
	todo, err := lib.Unembedded(ctx, a, nil, 0, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.SaveEmbeddings(ctx, a, todo, [][]float32{{1, 2}}); err != nil {
		t.Fatal(err)
	}
	if got, err := lib.Unembedded(ctx, b, nil, 0, 0, 10); err != nil || len(got) != 1 {
		t.Fatalf("instance b: %+v, %v", got, err)
	}
}

func TestUnembeddedLimitKeepsLowestIDs(t *testing.T) {
	lib := newLibrary(t)
	inst := newInstanceRow(t, lib)
	first, _ := newCard(t, lib, "first", `{"name": "First"}`)
	newCard(t, lib, "second", `{"name": "Second"}`)
	if got, err := lib.Unembedded(context.Background(), inst, nil, 0, 0, 1); err != nil || len(got) != 1 || got[0].CardID != first {
		t.Fatalf("unembedded = %+v, %v", got, err)
	}
}

func TestUnembeddedStartsAfterTheGivenCard(t *testing.T) {
	lib := newLibrary(t)
	inst := newInstanceRow(t, lib)
	first, _ := newCard(t, lib, "first", `{"name": "First"}`)
	second, _ := newCard(t, lib, "second", `{"name": "Second"}`)
	if got, err := lib.Unembedded(context.Background(), inst, nil, 0, first, 10); err != nil || len(got) != 1 || got[0].CardID != second {
		t.Fatalf("unembedded after %d = %+v, %v", first, got, err)
	}
}

func TestUnembeddedKeepsToTheGivenLibrary(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	inst := newInstanceRow(t, lib)
	newCard(t, lib, "home", `{"name": "Home"}`)
	archive, err := lib.CreateLibrary(ctx, "Archive")
	if err != nil {
		t.Fatal(err)
	}
	away, _, err := lib.Cards.Create(ctx, archive.ID, "away", "upload", edit(`{"name": "Away"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := lib.Unembedded(ctx, inst, nil, archive.ID, 0, 10); err != nil || len(got) != 1 || got[0].CardID != away.ID {
		t.Fatalf("unembedded in library %d = %+v, %v; want only card %d", archive.ID, got, err, away.ID)
	}
	if got, err := lib.Unembedded(ctx, inst, nil, 0, 0, 10); err != nil || len(got) != 2 {
		t.Fatalf("unembedded in every library = %+v, %v; want both cards", got, err)
	}
}

func TestUnembeddedUsesSourceValuesAndReembedsWhenTheyChange(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	inst := newInstanceRow(t, lib)
	var enricher int64
	if err := lib.pool.QueryRow(ctx,
		`INSERT INTO plugin_instance (plugin, config) VALUES ('summarizer', '{}') RETURNING id`).Scan(&enricher); err != nil {
		t.Fatal(err)
	}
	sources := []EmbedSource{{Instance: enricher, Key: "premise"}, {Instance: enricher, Key: "themes"}}
	card, rev := newCard(t, lib, "brakka", `{"name": "Brakka", "description": "A dwarf smith."}`)

	todo, err := lib.Unembedded(ctx, inst, sources, 0, 0, 10)
	if err != nil || len(todo) != 1 || todo[0].Text != "Brakka\nA dwarf smith." || todo[0].EnrichmentID != nil {
		t.Fatalf("before any value: %+v, %v", todo, err)
	}
	if err := lib.SaveEmbeddings(ctx, inst, todo, [][]float32{{1, 0}}); err != nil {
		t.Fatal(err)
	}

	if err := lib.AddEnrichments(ctx, enricher, []NewEnrichment{
		{CardID: card, RevisionID: rev, Key: "themes", Value: json.RawMessage(`["forge", "grudges"]`)},
		{CardID: card, RevisionID: rev, Key: "premise", Value: json.RawMessage(`"A smith keeps a grudge."`)},
		{CardID: card, RevisionID: rev, Key: "rating", Value: json.RawMessage(`"safe"`)},
	}); err != nil {
		t.Fatal(err)
	}
	todo, err = lib.Unembedded(ctx, inst, sources, 0, 0, 10)
	if err != nil || len(todo) != 1 || todo[0].Text != "A smith keeps a grudge.\nforge, grudges" || todo[0].EnrichmentID == nil {
		t.Fatalf("after values arrive: %+v, %v", todo, err)
	}
	if err := lib.SaveEmbeddings(ctx, inst, todo, [][]float32{{0, 1}}); err != nil {
		t.Fatal(err)
	}
	if todo, err = lib.Unembedded(ctx, inst, sources, 0, 0, 10); err != nil || len(todo) != 0 {
		t.Fatalf("after saving: %+v, %v", todo, err)
	}

	if _, err := lib.Cards.Update(ctx, card, rev, edit(`{"name": "Brakka", "description": "A dwarf smith, retired."}`)); err != nil {
		t.Fatal(err)
	}
	if todo, err = lib.Unembedded(ctx, inst, sources, 0, 0, 10); err != nil || len(todo) != 1 || todo[0].Text != "Brakka\nA dwarf smith, retired." {
		t.Fatalf("values on an older revision: %+v, %v", todo, err)
	}
}
