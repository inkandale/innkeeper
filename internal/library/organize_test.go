package library

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	if got, _ := lib.collectionCards(ctx, fav.ID); !slices.Equal(got, []int64{a, b}) {
		t.Fatalf("favorites = %v", got)
	}
	if err := lib.RemoveFromCollection(ctx, fav.ID, a); err != nil {
		t.Fatal(err)
	}
	if got, _ := lib.collectionCards(ctx, fav.ID); !slices.Equal(got, []int64{b}) {
		t.Fatalf("favorites after remove = %v", got)
	}
	if got, _ := lib.collectionCards(ctx, other.ID); !slices.Equal(got, []int64{a}) {
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

func TestCollectionAvatars(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	png, err := os.Open(filepath.Join("..", "..", "cardhash", "testdata", "avatar.png"))
	if err != nil {
		t.Fatal(err)
	}
	defer png.Close()
	img, err := lib.PutBlob(ctx, png)
	if err != nil {
		t.Fatal(err)
	}
	c, err := lib.CreateCollection(ctx, "Crew", "")
	if err != nil {
		t.Fatal(err)
	}
	var archived int64
	for i := range 6 {
		ed := edit(`{"name": "Sailor"}`)
		if i > 0 {
			ed.AvatarBlob = img.SHA256
		}
		card, _, err := lib.Cards.Create(ctx, fmt.Sprintf("sailor-%d", i), "upload", ed)
		if err != nil {
			t.Fatal(err)
		}
		if err := lib.AddToCollection(ctx, c.ID, card.ID); err != nil {
			t.Fatal(err)
		}
		if i == 5 {
			archived = card.ID
		}
	}
	list, err := lib.Collections(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// The card without an avatar is skipped, and the mosaic stops at four.
	want := slices.Repeat([]string{img.SHA256}, 4)
	if len(list) != 1 || list[0].Cards != 6 || !slices.Equal(list[0].Avatars, want) {
		t.Fatalf("collections = %+v", list)
	}
	// The Cards page hides archived cards, so the count must too.
	if err := lib.Cards.SetArchived(ctx, archived, true); err != nil {
		t.Fatal(err)
	}
	list, err = lib.Collections(ctx)
	if err != nil || list[0].Cards != 5 {
		t.Fatalf("after archive = %+v, %v", list, err)
	}
}

func TestEditCardTags(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	id, _ := newCard(t, lib, "brakka", `{"name": "Brakka"}`)
	if err := lib.SetCardTags(ctx, id, []string{"Dwarf", "smith"}); err != nil {
		t.Fatal(err)
	}
	if err := lib.EditCardTags(ctx, id, []string{" grumpy ", "dwarf"}, []string{"SMITH", "absent"}); err != nil {
		t.Fatal(err)
	}
	got, err := lib.CardTags(ctx, id)
	if err != nil || !slices.Equal(got, []string{"Dwarf", "grumpy"}) {
		t.Fatalf("tags = %v, %v", got, err)
	}
	if err := lib.EditCardTags(ctx, 99999, []string{"x"}, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing card: %v", err)
	}
}

func TestCardCollections(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	a, _ := newCard(t, lib, "a", `{"name": "A"}`)
	fav, err := lib.CreateCollection(ctx, "Favorites", "")
	if err != nil {
		t.Fatal(err)
	}
	other, err := lib.CreateCollection(ctx, "Other", "")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := lib.CardCollections(ctx, a); err != nil || len(got) != 0 {
		t.Fatalf("before = %v, %v", got, err)
	}
	for _, c := range []int64{other.ID, fav.ID} {
		if err := lib.AddToCollection(ctx, c, a); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := lib.CardCollections(ctx, a); err != nil || !slices.Equal(got, []int64{fav.ID, other.ID}) {
		t.Fatalf("after = %v, %v", got, err)
	}
}

func TestLorebookCards(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	a, _ := newCard(t, lib, "a", `{"name": "A"}`)
	b, _ := newCard(t, lib, "b", `{"name": "B"}`)
	lb := newLorebook(t, lib, "forge")
	if got, err := lib.LorebookCards(ctx, lb); err != nil || len(got) != 0 {
		t.Fatalf("before = %v, %v", got, err)
	}
	for _, c := range []int64{b, a} {
		if err := lib.LinkLorebook(ctx, c, lb); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := lib.LorebookCards(ctx, lb); err != nil || !slices.Equal(got, []int64{a, b}) {
		t.Fatalf("after = %v, %v", got, err)
	}
}
