package library

import (
	"context"
	"errors"
	"slices"
	"testing"
)

func TestCardTags(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	a, _ := newCard(t, lib, "a", `{"name": "A"}`)
	b, _ := newCard(t, lib, "b", `{"name": "B"}`)
	if err := lib.SetCardTags(ctx, a, []string{"Fantasy", " dwarf ", "fantasy", ""}); err != nil {
		t.Fatal(err)
	}
	// The first spelling of a tag wins.
	if err := lib.SetCardTags(ctx, b, []string{"FANTASY"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := lib.CardTags(ctx, a); !slices.Equal(got, []string{"dwarf", "Fantasy"}) {
		t.Fatalf("a tags = %v", got)
	}
	if got, _ := lib.CardTags(ctx, b); !slices.Equal(got, []string{"Fantasy"}) {
		t.Fatalf("b tags = %v", got)
	}
	if err := lib.SetCardTags(ctx, a, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := lib.CardTags(ctx, a); len(got) != 0 {
		t.Fatalf("a tags after clear = %v", got)
	}
	if err := lib.SetCardTags(ctx, 999, []string{"x"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing card: got %v, want ErrNotFound", err)
	}
}

func TestCollections(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	a, _ := newCard(t, lib, "a", `{"name": "A"}`)
	b, _ := newCard(t, lib, "b", `{"name": "B"}`)
	fav, err := lib.CreateCollection(ctx, "Favorites", "")
	if err != nil {
		t.Fatal(err)
	}
	other, err := lib.CreateCollection(ctx, "Other", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range []struct{ coll, card int64 }{{fav.ID, a}, {fav.ID, b}, {fav.ID, a}, {other.ID, a}} {
		if err := lib.AddToCollection(ctx, step.coll, step.card); err != nil {
			t.Fatal(err)
		}
	}
	if got, _ := lib.CollectionCards(ctx, fav.ID); !slices.Equal(got, []int64{a, b}) {
		t.Fatalf("favorites = %v", got)
	}
	if err := lib.RemoveFromCollection(ctx, fav.ID, a); err != nil {
		t.Fatal(err)
	}
	if got, _ := lib.CollectionCards(ctx, fav.ID); !slices.Equal(got, []int64{b}) {
		t.Fatalf("favorites after remove = %v", got)
	}
	if got, _ := lib.CollectionCards(ctx, other.ID); !slices.Equal(got, []int64{a}) {
		t.Fatalf("other = %v", got)
	}
	if err := lib.AddToCollection(ctx, fav.ID, 999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing card: got %v, want ErrNotFound", err)
	}
}

func TestLorebookLinks(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	card, _ := newCard(t, lib, "a", `{"name": "A"}`)
	book, _, err := lib.Lorebooks.Create(ctx, "lore", "upload", edit(`{"entries": []}`))
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := lib.LinkLorebook(ctx, card, book.ID); err != nil {
			t.Fatal(err)
		}
	}
	if got, _ := lib.CardLorebooks(ctx, card); !slices.Equal(got, []int64{book.ID}) {
		t.Fatalf("lorebooks = %v", got)
	}
	if err := lib.UnlinkLorebook(ctx, card, book.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := lib.CardLorebooks(ctx, card); len(got) != 0 {
		t.Fatalf("lorebooks after unlink = %v", got)
	}
	if err := lib.LinkLorebook(ctx, card, 999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing lorebook: got %v, want ErrNotFound", err)
	}
}
