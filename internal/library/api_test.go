package library

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
)

func TestCreateFromName(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	a, _, err := lib.Cards.CreateFromName(ctx, "Brakka Ironfist", "api", edit(`{"name": "Brakka Ironfist"}`))
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := lib.Cards.CreateFromName(ctx, "Brakka Ironfist", "api", edit(`{"name": "Brakka Ironfist"}`))
	if err != nil {
		t.Fatal(err)
	}
	if a.Slug != "brakka-ironfist" || b.Slug != "brakka-ironfist-2" {
		t.Fatalf("slugs = %q, %q", a.Slug, b.Slug)
	}
}

func TestOverridesMustBeAnObject(t *testing.T) {
	lib := newLibrary(t)
	e := edit(`{"name": "A"}`)
	e.Overrides = json.RawMessage(`[]`)
	if _, _, err := lib.Cards.Create(context.Background(), "a", "api", e); !errors.Is(err, ErrInvalid) {
		t.Fatalf("got %v, want ErrInvalid", err)
	}
}

func TestTags(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	newCard(t, lib, "a", `{"name": "A", "tags": ["fantasy", "Dwarf"]}`)
	newCard(t, lib, "b", `{"name": "B", "tags": ["fantasy"]}`)
	got, err := lib.Tags(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []TagCount{{"Dwarf", 1}, {"fantasy", 2}}
	if !slices.Equal(got, want) {
		t.Fatalf("tags = %v, want %v", got, want)
	}
}

func TestCollectionManagement(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	card, _ := newCard(t, lib, "a", `{"name": "A"}`)
	c, err := lib.CreateCollection(ctx, "Favorites", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.CreateCollection(ctx, "Favorites", ""); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("duplicate name: %v", err)
	}
	if err := lib.AddToCollection(ctx, c.ID, card); err != nil {
		t.Fatal(err)
	}
	other, err := lib.CreateCollection(ctx, "Other", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.UpdateCollection(ctx, other.ID, &c.Name, nil); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("rename to taken name: %v", err)
	}
	if _, err := lib.CreateCollection(ctx, "", ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty name: %v", err)
	}
	if err := lib.DeleteCollection(ctx, other.ID); err != nil {
		t.Fatal(err)
	}
	name, desc := "Best", "The best ones"
	if _, err := lib.UpdateCollection(ctx, c.ID, &name, &desc); err != nil {
		t.Fatal(err)
	}
	list, err := lib.Collections(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Name != "Best" || list[0].Description != "The best ones" || list[0].Cards != 1 {
		t.Fatalf("collections = %+v", list)
	}
	if err := lib.DeleteCollection(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if err := lib.DeleteCollection(ctx, c.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
	if _, err := lib.UpdateCollection(ctx, c.ID, &name, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update deleted: %v", err)
	}
}

func TestOpenBlob(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	b, err := lib.PutBlob(ctx, strings.NewReader("hello"))
	if err != nil {
		t.Fatal(err)
	}
	got, f, err := lib.OpenBlob(ctx, b.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	body, _ := io.ReadAll(f)
	if string(body) != "hello" || got.Size != 5 {
		t.Fatalf("blob = %+v, %q", got, body)
	}
	if _, _, err := lib.OpenBlob(ctx, strings.Repeat("0", 64)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing blob: %v", err)
	}
	if _, _, err := lib.OpenBlob(ctx, "../../etc/passwd"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("traversal: %v", err)
	}
}

func TestExportCardEmbedsLorebooks(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	card, _ := newCard(t, lib, "brakka", `{"name": "Brakka"}`)
	for i, data := range []string{
		`{"name": "Forge", "entries": [{"keys": ["anvil"], "content": "Heavy."}]}`,
		`{"name": "Clan", "entries": [{"keys": ["clan"], "content": "Ironfist."}]}`,
	} {
		lb, _, err := lib.Lorebooks.Create(ctx, []string{"forge", "clan"}[i], "api", edit(data))
		if err != nil {
			t.Fatal(err)
		}
		if err := lib.LinkLorebook(ctx, card, lb.ID); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := lib.ExportCard(ctx, card)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Spec string `json:"spec"`
		Data struct {
			Name string `json:"name"`
			Book struct {
				Name    string `json:"name"`
				Entries []struct {
					Content string `json:"content"`
				} `json:"entries"`
			} `json:"character_book"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out.Spec != "chara_card_v3" || out.Data.Name != "Brakka" || out.Data.Book.Name != "Forge" ||
		len(out.Data.Book.Entries) != 2 || out.Data.Book.Entries[1].Content != "Ironfist." {
		t.Fatalf("export = %s", raw)
	}

	plain, _ := newCard(t, lib, "plain", `{"name": "Plain"}`)
	raw, err = lib.ExportCard(ctx, plain)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "character_book") {
		t.Fatalf("card without lorebooks exported a book: %s", raw)
	}
}
