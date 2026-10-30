package library

import (
	"context"
	"errors"
	"fmt"
	"maps"
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

func TestRelatedGroups(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	me, _ := newCard(t, lib, "me", `{"name": "Brakka", "creator": "DG "}`)
	byCreator, _ := newCard(t, lib, "by-dg", `{"name": "Dorna", "creator": "dg"}`)
	newCard(t, lib, "by-other", `{"name": "Lia", "creator": "someone"}`)
	sharesBook, _ := newCard(t, lib, "book", `{"name": "Grum"}`)
	inCollection, _ := newCard(t, lib, "coll", `{"name": "Hild"}`)

	book := newLorebook(t, lib, "deeps")
	for _, c := range []int64{me, sharesBook} {
		if err := lib.LinkLorebook(ctx, c, book); err != nil {
			t.Fatal(err)
		}
	}
	coll, err := lib.CreateCollection(ctx, DefaultLibrary, "Dwarves", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []int64{me, inCollection} {
		if err := lib.AddToCollection(ctx, coll.ID, c); err != nil {
			t.Fatal(err)
		}
	}

	for group, want := range map[string][]int64{"creator": {byCreator}, "lorebook": {sharesBook}, "collection": {inCollection}} {
		got, err := lib.RelatedCards(ctx, me, group, Page{})
		if err != nil || !slices.Equal(ids(got.Items), want) || got.Group != group {
			t.Fatalf("%s = %+v, %v; want %v", group, got, err, want)
		}
	}
}

func TestRelatedIgnoresAnEmptyCreator(t *testing.T) {
	lib := newLibrary(t)
	me, _ := newCard(t, lib, "me", `{"name": "Brakka"}`)
	newCard(t, lib, "other", `{"name": "Dorna", "creator": ""}`)
	got, err := lib.RelatedCards(context.Background(), me, "creator", Page{})
	if err != nil || got.Items == nil || len(got.Items) != 0 {
		t.Fatalf("creator = %+v, %v; want an empty non-nil list", got, err)
	}
}

func TestRelatedIgnoresAnAnonymousCreator(t *testing.T) {
	lib := newLibrary(t)
	me, _ := newCard(t, lib, "me", `{"name": "Paige", "creator": "Anonymous"}`)
	newCard(t, lib, "other", `{"name": "Dorna", "creator": "anonymous "}`)
	got, err := lib.RelatedCards(context.Background(), me, "creator", Page{})
	if err != nil || len(got.Items) != 0 {
		t.Fatalf("creator = %+v, %v; want none", got, err)
	}
}

func TestRelatedPagesOneGroup(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	me, _ := newCard(t, lib, "me", `{"name": "Me", "creator": "dg"}`)
	var want []int64
	for i := range 3 {
		id, _ := newCard(t, lib, fmt.Sprintf("c%d", i), `{"name": "C", "creator": "dg"}`)
		want = append(want, id)
	}
	first, err := lib.RelatedCards(ctx, me, "creator", Page{Size: 2})
	if err != nil || len(first.Items) != 2 || first.NextCursor == nil {
		t.Fatalf("first = %+v, %v", first, err)
	}
	second, err := lib.RelatedCards(ctx, me, "creator", Page{Size: 2, Cursor: first.NextCursor})
	if err != nil || second.NextCursor != nil || !slices.Equal(append(ids(first.Items), ids(second.Items)...), want) {
		t.Fatalf("second = %+v, %v", second, err)
	}
}

func TestRelatedRejectsAnUnknownGroup(t *testing.T) {
	lib := newLibrary(t)
	me, _ := newCard(t, lib, "me", `{"name": "Me"}`)
	if _, err := lib.RelatedCards(context.Background(), me, "colour", Page{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

func TestRelatedGroupsMatchTheirConditions(t *testing.T) {
	if !slices.Equal(slices.Sorted(maps.Keys(relatedWhere)), slices.Sorted(slices.Values(RelatedGroups))) {
		t.Fatalf("RelatedGroups %v, conditions for %v", RelatedGroups, slices.Sorted(maps.Keys(relatedWhere)))
	}
}

func TestDiscoveredCardsCarryTheirAvatar(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	me, _ := newCard(t, lib, "me", `{"name": "Brakka", "creator": "Ann", "tags": ["dwarf", "smith"]}`)
	other := avatarCard(t, lib, 0, 1, `{"name": "Dorna", "creator": "Ann", "tags": ["dwarf", "smith"]}`)
	want := fmt.Sprintf("%064x", 1)

	similar, err := lib.SimilarCards(ctx, me, SimilarOptions{})
	if err != nil || !slices.Equal(ids(similar.Items), []int64{other}) || similar.Items[0].Avatar != want {
		t.Fatalf("similar = %+v, %v", similar, err)
	}
	related, err := lib.RelatedCards(ctx, me, "creator", Page{})
	if err != nil || !slices.Equal(ids(related.Items), []int64{other}) || related.Items[0].Avatar != want {
		t.Fatalf("related = %+v, %v", related, err)
	}
}

func TestDiscoveredCardsCarryTheirTags(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	me, _ := newCard(t, lib, "me", `{"name": "Brakka", "creator": "Ann", "tags": ["dwarf", "smith"]}`)
	other, _ := newCard(t, lib, "other", `{"name": "Dorna", "creator": "Ann", "tags": ["dwarf", "smith", "NSFW"]}`)
	want := []string{"dwarf", "smith", "NSFW"}

	similar, err := lib.SimilarCards(ctx, me, SimilarOptions{})
	if err != nil || !slices.Equal(ids(similar.Items), []int64{other}) || !slices.Equal(similar.Items[0].Tags, want) {
		t.Fatalf("similar = %+v, %v", similar, err)
	}
	related, err := lib.RelatedCards(ctx, me, "creator", Page{})
	if err != nil || !slices.Equal(ids(related.Items), []int64{other}) || !slices.Equal(related.Items[0].Tags, want) {
		t.Fatalf("related = %+v, %v", related, err)
	}
}
