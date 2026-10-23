package library

import (
	"context"
	"fmt"
	"slices"
	"testing"
)

// embedCards saves vec[i] as cards[i]'s vector from instance, from each card's head.
func embedCards(t *testing.T, lib *Library, instance int64, vecs map[int64][]float32) {
	t.Helper()
	ctx := context.Background()
	for id, v := range vecs {
		c, err := lib.Cards.Get(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if err := lib.SaveEmbeddings(ctx, instance, []EmbedText{{CardID: id, RevisionID: c.HeadRevisionID}}, [][]float32{v}); err != nil {
			t.Fatal(err)
		}
	}
}

func ids(cards []DiscoveredCard) []int64 {
	out := make([]int64, len(cards))
	for i, c := range cards {
		out[i] = c.CardID
	}
	return out
}

func TestSimilarRanksByEmbeddingDistance(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	inst := newInstanceRow(t, lib)
	me, _ := newCard(t, lib, "me", `{"name": "Brakka"}`)
	near, _ := newCard(t, lib, "near", `{"name": "Dorna"}`)
	far, _ := newCard(t, lib, "far", `{"name": "Lia"}`)
	embedCards(t, lib, inst, map[int64][]float32{me: {1, 0}, near: {0.9, 0.1}, far: {0, 1}})

	got, err := lib.SimilarCards(ctx, me, SimilarOptions{Instance: inst})
	if err != nil {
		t.Fatal(err)
	}
	if got.Basis != "embedding" || len(got.Items) != 2 || got.Items[0].CardID != near || got.Items[1].CardID != far ||
		got.Items[0].Name != "Dorna" || got.Items[0].Score <= got.Items[1].Score {
		t.Fatalf("similar = %+v", got)
	}
}

func TestSimilarFallsBackToTagsWithoutEmbedInstance(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	me, _ := newCard(t, lib, "me", `{"name": "Brakka", "tags": ["Dwarf", "smith", "grumpy"]}`)
	two, _ := newCard(t, lib, "two", `{"name": "Dorna", "tags": ["dwarf", "smith"]}`)
	three, _ := newCard(t, lib, "three", `{"name": "Grum", "tags": ["dwarf", "smith", "grumpy", "old"]}`)
	newCard(t, lib, "one", `{"name": "Lia", "tags": ["dwarf", "elf"]}`)

	got, err := lib.SimilarCards(ctx, me, SimilarOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// Jaccard: three shares 3 of 4 tags, two shares 2 of 3; one shares only 1.
	if got.Basis != "tags" || !slices.Equal(ids(got.Items), []int64{three, two}) || got.Items[0].Score != 0.75 {
		t.Fatalf("similar = %+v", got)
	}
}

func TestSimilarFallsBackToTagsForACardWithoutAVector(t *testing.T) {
	lib := newLibrary(t)
	inst := newInstanceRow(t, lib)
	me, _ := newCard(t, lib, "me", `{"name": "Brakka", "tags": ["dwarf", "smith"]}`)
	other, _ := newCard(t, lib, "other", `{"name": "Dorna", "tags": ["dwarf", "smith"]}`)
	embedCards(t, lib, inst, map[int64][]float32{other: {1, 0}})
	got, err := lib.SimilarCards(context.Background(), me, SimilarOptions{Instance: inst})
	if err != nil || got.Basis != "tags" || !slices.Equal(ids(got.Items), []int64{other}) {
		t.Fatalf("similar = %+v, %v", got, err)
	}
}

func TestSimilarExcludesVariantFamilyDuplicatesAndArchived(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	inst := newInstanceRow(t, lib)
	root, _ := newCard(t, lib, "root", `{"name": "Root"}`)
	me, _ := newCard(t, lib, "me", `{"name": "Brakka"}`)
	sibling, _ := newCard(t, lib, "sibling", `{"name": "Sibling"}`)
	dup, _ := newCard(t, lib, "dup", `{"name": "Brakka"}`)
	archived, _ := newCard(t, lib, "archived", `{"name": "Old"}`)
	kept, _ := newCard(t, lib, "kept", `{"name": "Kept"}`)
	for _, v := range []int64{me, sibling} {
		if err := lib.Cards.SetVariantOf(ctx, v, &root); err != nil {
			t.Fatal(err)
		}
	}
	if err := lib.Cards.SetArchived(ctx, archived, true); err != nil {
		t.Fatal(err)
	}
	embedCards(t, lib, inst, map[int64][]float32{root: {1, 0}, me: {1, 0}, sibling: {1, 0}, dup: {1, 0}, archived: {1, 0}, kept: {0, 1}})

	got, err := lib.SimilarCards(ctx, me, SimilarOptions{Instance: inst})
	if err != nil || !slices.Equal(ids(got.Items), []int64{kept}) {
		t.Fatalf("similar = %+v, %v; want only kept", got, err)
	}
}

func TestSimilarAllScopeFindsOtherLibraries(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	inst := newInstanceRow(t, lib)
	me, _ := newCard(t, lib, "me", `{"name": "Brakka"}`)
	away, _ := newCard(t, lib, "away", `{"name": "Dorna"}`)
	archive := newLibraryRow(t, lib, "Archive")
	moveCard(t, lib, away, archive)
	embedCards(t, lib, inst, map[int64][]float32{me: {1, 0}, away: {1, 0.1}})

	here, err := lib.SimilarCards(ctx, me, SimilarOptions{Instance: inst})
	if err != nil || len(here.Items) != 0 {
		t.Fatalf("library scope = %+v, %v", here, err)
	}
	all, err := lib.SimilarCards(ctx, me, SimilarOptions{Instance: inst, AllLibraries: true})
	if err != nil || len(all.Items) != 1 || all.Items[0].CardID != away || all.Items[0].LibraryID != archive {
		t.Fatalf("all scope = %+v, %v", all, err)
	}
}

func TestSimilarPagesByCursor(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	inst := newInstanceRow(t, lib)
	me, _ := newCard(t, lib, "me", `{"name": "Me"}`)
	vecs := map[int64][]float32{me: {1, 0}}
	var want []int64
	for i := range 5 {
		id, _ := newCard(t, lib, fmt.Sprintf("c%d", i), fmt.Sprintf(`{"name": "C%d"}`, i))
		vecs[id] = []float32{1, float32(i + 1)}
		want = append(want, id)
	}
	embedCards(t, lib, inst, vecs)

	var got []int64
	page := Page{Size: 2}
	for range 3 {
		res, err := lib.SimilarCards(ctx, me, SimilarOptions{Instance: inst, Page: page})
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, ids(res.Items)...)
		if res.NextCursor == nil {
			break
		}
		page.Cursor = res.NextCursor
	}
	if !slices.Equal(got, want) {
		t.Fatalf("paged = %v, want %v", got, want)
	}
}
