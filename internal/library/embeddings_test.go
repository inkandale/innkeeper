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
