package library

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/davidgibbons/innkeeper/cardhash"
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

func TestDataNameIsEmptyWithoutAName(t *testing.T) {
	for data, want := range map[string]string{
		`{"name": "Brakka", "description": "orc"}`: "Brakka",
		`{"description": "orc"}`:                   "",
		`["Brakka"]`:                               "",
		`not json`:                                 "",
	} {
		if got := DataName(json.RawMessage(data)); got != want {
			t.Errorf("DataName(%s) = %q, want %q", data, got, want)
		}
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

func TestConcurrentImportsOfOneItemMakeOneCard(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	e := Edit{Data: json.RawMessage(`{"name": "Brakka"}`), Source: "plugin:folder"}
	ids := make(chan int64, 8)
	var wg sync.WaitGroup
	for range cap(ids) {
		wg.Go(func() {
			id, _, err := lib.Cards.Import(ctx, "folder:brakka.png", "Brakka", e)
			if err != nil {
				t.Error(err)
			}
			ids <- id
		})
	}
	wg.Wait()
	close(ids)
	first := <-ids
	for id := range ids {
		if id != first {
			t.Fatalf("imports made cards %d and %d", first, id)
		}
	}
}

func TestImportCardRecordsSources(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	card := `{"spec":"chara_card_v3","spec_version":"3.0","data":{"name":"A","extensions":{"chub":{"id":7,"full_path":"u/a"}},
		"character_book":{"name":"L","entries":[],"extensions":{"chub":{"id":8,"full_path":"u/l"}}}}}`
	for range 2 {
		id, _, err := lib.ImportCard(ctx, "plugin:x:1", "plugin:x", []byte(card), "")
		if err != nil {
			t.Fatal(err)
		}
		got, err := lib.Cards.Sources(ctx, id)
		if err != nil || len(got) != 1 || got[0].Ref != "u/a" || got[0].LinkedBy != "auto" {
			t.Fatalf("card sources: %+v, %v", got, err)
		}
	}
	ids, err := lib.Lorebooks.BySource(ctx, "chub", "u/l")
	if err != nil || len(ids) != 1 {
		t.Fatalf("lorebook sources: %v, %v", ids, err)
	}
	// A standalone lorebook import records its source too.
	id, _, err := lib.Lorebooks.Import(ctx, "plugin:x:2", "M", Edit{Data: []byte(`{"name":"M","entries":[],"extensions":{"chub":{"full_path":"u/m"}}}`), Source: "plugin:x"})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := lib.Lorebooks.Sources(ctx, id); len(got) != 1 || got[0].URL != "https://chub.ai/lorebooks/u/m" {
		t.Fatalf("standalone lorebook sources: %+v", got)
	}
}

func TestSplitCardDropsAppLocalExtensions(t *testing.T) {
	split := func(ext string) map[string]any {
		t.Helper()
		_, data, _, err := SplitCard(json.RawMessage(`{"spec":"chara_card_v3","data":{"name":"Piko","extensions":` + ext + `}}`))
		if err != nil {
			t.Fatal(err)
		}
		var d map[string]any
		_ = json.Unmarshal(data, &d)
		return d
	}
	d := split(`{"fav":true,"world":"Dwarves","depth_prompt":{"depth":4},"talkativeness":"0.5"}`)
	if ext, _ := json.Marshal(d["extensions"]); string(ext) != `{"depth_prompt":{"depth":4},"talkativeness":"0.5"}` {
		t.Fatalf("extensions = %s", ext)
	}
	// Only app-local keys leave {}, which hashes like no extensions at all.
	only, _ := json.Marshal(split(`{"fav":false,"world":""}`))
	none, _ := json.Marshal(map[string]any{"name": "Piko"})
	h1, err1 := cardhash.ContentHash(only)
	h2, err2 := cardhash.ContentHash(none)
	if err := errors.Join(err1, err2); err != nil || h1 != h2 {
		t.Fatalf("%s hashes %s, want %s (%v)", only, h1, h2, err)
	}
}
