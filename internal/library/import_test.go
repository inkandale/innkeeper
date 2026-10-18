package library

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/inkandale/innkeeper/cardhash"
	"github.com/jackc/pgx/v5"
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

// importer makes a plugin instance and returns a function that imports a
// card with data from it as item, with each change applied to the item.
func importer(t *testing.T, lib *Library) (int64, func(item, data string, change ...func(*PluginItem)) Imported) {
	t.Helper()
	var inst int64
	if err := lib.pool.QueryRow(context.Background(), `INSERT INTO plugin_instance (plugin, name)
		VALUES ('folder', 'Folder ' || (SELECT count(*) FROM plugin_instance)) RETURNING id`).Scan(&inst); err != nil {
		t.Fatal(err)
	}
	return inst, func(item, data string, change ...func(*PluginItem)) Imported {
		t.Helper()
		it := PluginItem{Kind: Card, InstanceID: inst, Provenance: "folder:" + item, Source: "plugin:folder",
			Data: json.RawMessage(`{"spec":"chara_card_v3","spec_version":"3.0","data":` + data + `}`)}
		for _, c := range change {
			c(&it)
		}
		got, err := lib.ImportItem(context.Background(), it)
		if err != nil {
			t.Fatalf("import %s: %v", item, err)
		}
		return got
	}
}

func (l *Library) countRows(t *testing.T, query string, args ...any) (n int) {
	t.Helper()
	if err := l.pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestImportSameItemTwiceAddsNothing(t *testing.T) {
	lib := newLibrary(t)
	_, imp := importer(t, lib)
	first := imp("brakka.png", `{"name": "Brakka"}`)
	again := imp("brakka.png", `{"name": "Brakka"}`)
	if first.Outcome != Created || again.Outcome != Unchanged || again.ID != first.ID {
		t.Fatalf("imports = %+v, %+v", first, again)
	}
	if n := lib.countRows(t, `SELECT count(*) FROM card_revision`); n != 1 {
		t.Fatalf("%d revisions, want 1", n)
	}

	// The same content from another item is a different card, with a free slug.
	other := imp("copy.png", `{"name": "Brakka"}`)
	third := imp("third.png", `{"name": "Brakka"}`)
	for id, want := range map[int64]string{other.ID: "brakka-2", third.ID: "brakka-3"} {
		if c, _ := lib.Cards.Get(context.Background(), id); c.Slug != want {
			t.Fatalf("slug = %q, want %q", c.Slug, want)
		}
	}
}

func TestImportRejectsDataThatIsNotAnObject(t *testing.T) {
	lib := newLibrary(t)
	inst, _ := importer(t, lib)
	_, err := lib.ImportItem(context.Background(), PluginItem{Kind: Lorebook, InstanceID: inst, Provenance: "folder:list.json",
		Source: "plugin:folder", Data: json.RawMessage(`[]`)})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("import of a JSON array: %v, want ErrInvalid", err)
	}
}

func TestImportChangedItemAddsRevision(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	_, imp := importer(t, lib)
	first := imp("brakka.png", `{"name": "Brakka"}`)
	when := time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC)
	second := imp("brakka.png", `{"name": "Brakka", "description": "An orc."}`, func(it *PluginItem) {
		it.Version, it.Created = "v2", when
	})
	if second.Outcome != Revised || second.ID != first.ID {
		t.Fatalf("second import = %+v, want a revision of card %d", second, first.ID)
	}
	c, _ := lib.Cards.Get(ctx, first.ID)
	head, err := lib.Cards.Revision(ctx, c.HeadRevisionID)
	if err != nil || head.ID != second.RevisionID || head.Message != "v2" || !head.CreatedAt.Equal(when) {
		t.Fatalf("head = %+v, %v", head, err)
	}
	if *head.ParentID != first.RevisionID {
		t.Fatalf("parent = %d, want %d", *head.ParentID, first.RevisionID)
	}
	if n := lib.countRows(t, `SELECT count(*) FROM card`); n != 1 {
		t.Fatalf("%d cards, want 1", n)
	}
	// Re-reading the first version after a restart adds nothing.
	if again := imp("brakka.png", `{"name": "Brakka"}`); again.Outcome != Unchanged {
		t.Fatalf("re-read first version = %+v", again)
	}
}

func TestImportOnEditedCardRecordsSourceUpdate(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	_, imp := importer(t, lib)
	first := imp("brakka.png", `{"name": "Brakka"}`)
	edited, err := lib.Cards.Update(ctx, first.ID, first.RevisionID, edit(`{"name": "Brakka the Bold"}`))
	if err != nil {
		t.Fatal(err)
	}
	update := `{"name": "Brakka", "description": "An orc."}`
	got := imp("brakka.png", update)
	if got.Outcome != Pending || got.RevisionID != edited.ID {
		t.Fatalf("import on edited card = %+v", got)
	}
	var state string
	var seen json.RawMessage
	if err := lib.pool.QueryRow(ctx, `SELECT remote_state, seen_data FROM binding WHERE card_id = $1 AND imported`, first.ID).
		Scan(&state, &seen); err != nil {
		t.Fatal(err)
	}
	if state != "changed" || DataName(seen) != "Brakka" || !strings.Contains(string(seen), "An orc.") {
		t.Fatalf("binding = %s, %s", state, seen)
	}
	if c, _ := lib.Cards.Get(ctx, first.ID); c.HeadRevisionID != edited.ID {
		t.Fatal("the import moved the head of an edited card")
	}
	if again := imp("brakka.png", update); again.Outcome != Unchanged {
		t.Fatalf("same update again = %+v", again)
	}
}

func TestImportPendingUpdateOffersAutosync(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	_, imp := importer(t, lib)
	first := imp("brakka.png", `{"name": "Brakka"}`)
	if _, err := lib.pool.Exec(ctx, `UPDATE binding SET autosync = true WHERE card_id = $1`, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := lib.Cards.Update(ctx, first.ID, first.RevisionID, edit(`{"name": "Brakka the Bold"}`)); err != nil {
		t.Fatal(err)
	}
	got := imp("brakka.png", `{"name": "Brakka", "description": "An orc."}`)
	if len(got.Autosync) != 1 {
		t.Fatalf("autosync = %v, want the card's binding", got.Autosync)
	}
}

func TestImportTargetsLibrary(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	var archive int64
	if err := lib.pool.QueryRow(ctx, `INSERT INTO library (name) VALUES ('Archive') RETURNING id`).Scan(&archive); err != nil {
		t.Fatal(err)
	}
	_, imp := importer(t, lib)
	there := imp("brakka.png", `{"name": "Brakka"}`, func(it *PluginItem) { it.LibraryID = archive })
	here := imp("brakka.png", `{"name": "Brakka"}`)
	if there.Outcome != Created || here.Outcome != Created || there.ID == here.ID {
		t.Fatalf("imports = %+v, %+v; want a card in each library", there, here)
	}
	c, _ := lib.Cards.Get(ctx, there.ID)
	d, _ := lib.Cards.Get(ctx, here.ID)
	if c.LibraryID != archive || d.LibraryID != DefaultLibrary {
		t.Fatalf("libraries = %d, %d", c.LibraryID, d.LibraryID)
	}
}

func TestImportValuesBecomeEnrichments(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	inst, imp := importer(t, lib)
	rating := func(it *PluginItem) { it.Values = map[string]json.RawMessage{"rating": json.RawMessage(`4.5`)} }
	got := imp("brakka.png", `{"name": "Brakka"}`, rating)
	vals, err := lib.Enrichments(ctx, got.ID, false)
	if err != nil || len(vals) != 1 || vals[0].InstanceID != inst || vals[0].RevisionID != got.RevisionID || string(vals[0].Value) != `4.5` {
		t.Fatalf("enrichments = %+v, %v", vals, err)
	}
	imp("brakka.png", `{"name": "Brakka"}`, rating)
	if n := lib.countRows(t, `SELECT count(*) FROM enrichment`); n != 1 {
		t.Fatalf("%d enrichment rows after an unchanged import, want 1", n)
	}
}

func TestImportAdoptsCardImportedBeforeBindings(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	_, imp := importer(t, lib)
	c, rev, err := lib.Cards.Create(ctx, DefaultLibrary, "brakka", "folder:brakka.png", Edit{Data: json.RawMessage(`{"name": "Brakka"}`),
		Source: "plugin:folder", Message: "Imported"})
	if err != nil {
		t.Fatal(err)
	}
	got := imp("brakka.png", `{"name": "Brakka", "description": "An orc."}`)
	if got.Outcome != Revised || got.ID != c.ID {
		t.Fatalf("import = %+v, want a revision of card %d (head %d)", got, c.ID, rev.ID)
	}
}

func TestImportItemNamesALorebookWithoutOne(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	inst, imp := importer(t, lib)
	got, err := lib.ImportItem(ctx, PluginItem{Kind: Lorebook, InstanceID: inst, Provenance: "x:1", Source: "plugin:x",
		Data: json.RawMessage(`{"entries": []}`), Name: "Fallback"})
	if err != nil || got.Outcome != Created {
		t.Fatalf("import = %+v, %v", got, err)
	}
	if lb, _ := lib.Lorebooks.Get(ctx, got.ID); lb.Slug != "fallback" {
		t.Fatalf("slug = %q, want fallback", lb.Slug)
	}
	card := imp("2", `{"name":"Brakka"}`, func(it *PluginItem) { it.Name = "Fallback" })
	if c, _ := lib.Cards.Get(ctx, card.ID); c.Slug != "brakka" {
		t.Fatalf("card slug = %q, want brakka", c.Slug)
	}
}

func TestConcurrentImportsOfOneItemMakeOneCard(t *testing.T) {
	lib := newLibrary(t)
	inst, _ := importer(t, lib)
	ids := make(chan int64, 8)
	var wg sync.WaitGroup
	for range cap(ids) {
		wg.Go(func() {
			got, err := lib.ImportItem(context.Background(), PluginItem{Kind: Lorebook, InstanceID: inst,
				Provenance: "folder:lore.json", Source: "plugin:folder", Data: json.RawMessage(`{"name": "Lore", "entries": []}`)})
			if err != nil {
				t.Error(err)
			}
			ids <- got.ID
		})
	}
	wg.Wait()
	close(ids)
	first := <-ids
	for id := range ids {
		if id != first {
			t.Fatalf("imports made lorebooks %d and %d", first, id)
		}
	}
}

func TestImportCardQueuesCardAndLorebook(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	inst, imp := importer(t, lib)
	var id int64
	for range 2 {
		id = imp("1", `{"name":"A","character_book":{"name":"L","entries":[]}}`).ID
	}
	books, err := lib.CardLorebooks(ctx, id)
	if err != nil || len(books) != 1 {
		t.Fatalf("lorebooks = %v, %v", books, err)
	}
	// A standalone lorebook import queues too; a bulk one doesn't.
	lb, err := lib.ImportItem(ctx, PluginItem{Kind: Lorebook, InstanceID: inst, Provenance: "x:2", Source: "plugin:x",
		Data: []byte(`{"name":"M","entries":[]}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.ImportItem(ctx, PluginItem{Kind: Lorebook, InstanceID: inst, Provenance: "x:3", Source: "plugin:x",
		Data: []byte(`{"name":"N","entries":[]}`), SkipDetect: true}); err != nil {
		t.Fatal(err)
	}
	q, err := lib.DetectQueue(ctx, DetectItem{}, 10)
	want := []DetectItem{{Card, id}, {Lorebook, books[0]}, {Lorebook, lb.ID}}
	if err != nil || !slices.Equal(q, want) {
		t.Fatalf("queue = %+v, %v; want %+v", q, err, want)
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

func TestImportOfTheAcceptedVersionWithdrawsAPendingUpdate(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	_, imp := importer(t, lib)
	first := imp("brakka.png", `{"name": "Brakka"}`)
	if _, err := lib.Cards.Update(ctx, first.ID, first.RevisionID, edit(`{"name": "Brakka the Bold"}`)); err != nil {
		t.Fatal(err)
	}
	if got := imp("brakka.png", `{"name": "Brakka", "description": "An orc."}`); got.Outcome != Pending {
		t.Fatalf("update = %+v", got)
	}
	// The source took its change back.
	imp("brakka.png", `{"name": "Brakka"}`)
	var state string
	var seen *string
	if err := lib.pool.QueryRow(ctx, `SELECT remote_state, seen_data::text FROM binding WHERE card_id = $1 AND imported`, first.ID).
		Scan(&state, &seen); err != nil {
		t.Fatal(err)
	}
	if state != "unchanged" || seen != nil {
		t.Fatalf("binding = %s, %v; want the update withdrawn", state, seen)
	}
}

func TestImportOfAnOlderVersionAfterANewerOneIsSkipped(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	_, imp := importer(t, lib)
	at := func(month time.Month) func(*PluginItem) {
		return func(it *PluginItem) { it.Created = time.Date(2024, month, 1, 0, 0, 0, 0, time.UTC) }
	}
	v2 := imp("brakka.png", `{"name": "Brakka", "description": "v2"}`, at(3))
	if got := imp("brakka.png", `{"name": "Brakka", "description": "v1"}`, at(1)); got.Outcome != Unchanged {
		t.Fatalf("older version = %+v, want it skipped", got)
	}
	if c, _ := lib.Cards.Get(ctx, v2.ID); c.HeadRevisionID != v2.RevisionID {
		t.Fatal("an older version became the head")
	}
	// Nor does it wait as an update on an edited card, or replace a newer waiting one.
	if _, err := lib.Cards.Update(ctx, v2.ID, v2.RevisionID, edit(`{"name": "Brakka the Bold", "description": "v2"}`)); err != nil {
		t.Fatal(err)
	}
	if got := imp("brakka.png", `{"name": "Brakka", "description": "v1"}`, at(1)); got.Outcome != Unchanged {
		t.Fatalf("older version on an edited card = %+v", got)
	}
	if got := imp("brakka.png", `{"name": "Brakka", "description": "v4"}`, at(5)); got.Outcome != Pending {
		t.Fatalf("newer version = %+v", got)
	}
	if got := imp("brakka.png", `{"name": "Brakka", "description": "v3"}`, at(4)); got.Outcome != Unchanged {
		t.Fatalf("version older than the waiting one = %+v", got)
	}
	var seen string
	if err := lib.pool.QueryRow(ctx, `SELECT seen_data->>'description' FROM binding WHERE card_id = $1 AND imported`, v2.ID).
		Scan(&seen); err != nil || seen != "v4" {
		t.Fatalf("waiting update = %q, %v", seen, err)
	}
	// Without a date, order is the plugin's.
	if got := imp("brakka.png", `{"name": "Brakka", "description": "v0"}`); got.Outcome != Pending {
		t.Fatalf("undated version = %+v", got)
	}
}

// A card PostgreSQL can't store is refused on its own, and the rest of its
// batch is stored.
func TestImportBatchRefusesOnlyTheItemPostgresCantStore(t *testing.T) {
	lib := newLibrary(t)
	inst, _ := importer(t, lib)
	card := func(item, name string) PluginItem {
		return PluginItem{Kind: Card, InstanceID: inst, Provenance: "folder:" + item, Source: "plugin:folder",
			Data: json.RawMessage(`{"spec":"chara_card_v3","spec_version":"3.0","data":{"name":"` + name + `"}}`)}
	}
	items := []PluginItem{card("a", "Ada"), card("nul", `Bad\u0000Name`), card("b", "Bea")}
	var got []Imported
	var errs []error
	err := lib.ImportBatch(context.Background(), items, func(_ pgx.Tx, g []Imported, e []error) error {
		got, errs = g, e
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if errs[0] != nil || errs[2] != nil || !errors.Is(errs[1], ErrInvalid) ||
		got[0].Outcome != Created || got[2].Outcome != Created {
		t.Fatalf("got %+v, errs %v; want Ada and Bea created, the NUL card refused", got, errs)
	}
}
