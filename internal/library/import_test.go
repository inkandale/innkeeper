package library

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestSlugify(t *testing.T) {
	for name, want := range map[string]string{
		"Brakka":                    "brakka",
		"  Sir Reginald, the 3rd! ": "sir-reginald-the-3rd",
		"Zoë--Ann":                  "zo-ann",
		"花子":                        "untitled",
		"":                          "untitled",
	} {
		if got := Slugify(name); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", name, got, want)
		}
	}
	long := Slugify("a234567890 b234567890 c234567890 d234567890 e234567890 f2345678 gg")
	if len(long) > maxSlugLen || long[len(long)-1] == '-' {
		t.Errorf("long slug %q", long)
	}
}

func TestImportSkipsContentSeenBefore(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	e := Edit{Data: json.RawMessage(`{"name": "Brakka"}`), Source: "plugin:folder"}

	id, created, err := lib.Cards.Import(ctx, "folder:brakka.png", "Brakka", e)
	if err != nil || !created {
		t.Fatalf("first import = %d, %v, %v", id, created, err)
	}
	again, created, err := lib.Cards.Import(ctx, "folder:brakka.png", "Brakka", e)
	if err != nil || created || again != id {
		t.Fatalf("second import = %d, %v, %v; want existing %d", again, created, err, id)
	}

	// Still skipped after the owner edits the card.
	c, _ := lib.Cards.Get(ctx, id)
	if _, err := lib.Cards.Update(ctx, id, c.HeadRevisionID, Edit{Data: json.RawMessage(`{"name": "Brakka!"}`), Source: "ui"}); err != nil {
		t.Fatal(err)
	}
	if _, created, _ := lib.Cards.Import(ctx, "folder:brakka.png", "Brakka", e); created {
		t.Fatal("re-import after an edit created a card")
	}

	// The same content from another source is a different card, with a free slug.
	other, created, err := lib.Cards.Import(ctx, "folder:copy.png", "Brakka", e)
	if err != nil || !created {
		t.Fatalf("import from another source = %v, %v", created, err)
	}
	if c, _ := lib.Cards.Get(ctx, other); c.Slug != "brakka-2" {
		t.Fatalf("slug = %q, want brakka-2", c.Slug)
	}
	third, _, _ := lib.Cards.Import(ctx, "folder:third.png", "Brakka", e)
	if c, _ := lib.Cards.Get(ctx, third); c.Slug != "brakka-3" {
		t.Fatalf("slug = %q, want brakka-3", c.Slug)
	}

	if _, _, err := lib.Cards.Import(ctx, "folder:list.json", "List", Edit{Data: json.RawMessage(`[]`), Source: "plugin:folder"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("import of a JSON array: %v, want ErrInvalid", err)
	}
}
