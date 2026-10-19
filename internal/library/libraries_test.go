package library

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestLibraryCRUD(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	newCard(t, lib, "brakka", `{"name": "Brakka"}`)
	archive, err := lib.CreateLibrary(ctx, "Archive")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := lib.Lorebooks.Create(ctx, archive.ID, "lore", "upload", edit(`{"name": "Lore", "entries": []}`)); err != nil {
		t.Fatal(err)
	}
	list, err := lib.Libraries(ctx)
	if err != nil || len(list) != 2 || list[0].Name != "Library" || list[0].Cards != 1 || list[0].Lorebooks != 0 ||
		list[1].ID != archive.ID || list[1].Cards != 0 || list[1].Lorebooks != 1 {
		t.Fatalf("Libraries = %+v, %v", list, err)
	}
	if _, err := lib.CreateLibrary(ctx, "Archive"); !errors.Is(err, ErrNameTaken) {
		t.Errorf("taken name: %v", err)
	}
	if _, err := lib.CreateLibrary(ctx, ""); !errors.Is(err, ErrInvalid) {
		t.Errorf("empty name: %v", err)
	}
	got, err := lib.RenameLibrary(ctx, archive.ID, "Chub archive")
	if err != nil || got.Name != "Chub archive" || got.Lorebooks != 1 {
		t.Fatalf("RenameLibrary = %+v, %v", got, err)
	}
	if _, err := lib.RenameLibrary(ctx, archive.ID, "Library"); !errors.Is(err, ErrNameTaken) {
		t.Errorf("rename to a taken name: %v", err)
	}
	if _, err := lib.RenameLibrary(ctx, 999, "Gone"); !errors.Is(err, ErrNotFound) {
		t.Errorf("rename missing: %v", err)
	}
	if _, err := lib.GetLibrary(ctx, 999); !errors.Is(err, ErrNotFound) {
		t.Errorf("get missing: %v", err)
	}
}

func TestDeleteDefaultLibraryRefused(t *testing.T) {
	lib := newLibrary(t)
	if err := lib.DeleteLibrary(context.Background(), DefaultLibrary, func(*Kind, int) {}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("got %v, want ErrInvalid", err)
	}
}

func TestDeleteLibraryRemovesItsCardsAndBlobs(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	put := func(s string) string {
		b, err := lib.PutBlob(ctx, strings.NewReader(s))
		if err != nil {
			t.Fatal(err)
		}
		return b.SHA256
	}
	only, shared := put("only in archive"), put("shared")
	archive, err := lib.CreateLibrary(ctx, "Archive")
	if err != nil {
		t.Fatal(err)
	}
	keep, _, err := lib.Cards.Create(ctx, DefaultLibrary, "keep", "upload", Edit{Data: []byte(`{"name": "Keep"}`), AvatarBlob: shared, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	gone, _, err := lib.Cards.Create(ctx, archive.ID, "gone", "upload", Edit{Data: []byte(`{"name": "Gone"}`), AvatarBlob: only, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.Cards.Update(ctx, gone.ID, gone.HeadRevisionID, Edit{Data: []byte(`{"name": "Gone 2"}`), AvatarBlob: shared, Source: "ui"}); err != nil {
		t.Fatal(err)
	}
	variant, err := lib.Cards.Fork(ctx, gone.ID, 0, "ui")
	if err != nil {
		t.Fatal(err)
	}
	book, _, err := lib.Lorebooks.Create(ctx, archive.ID, "gone-lore", "upload", edit(`{"name": "Lore", "entries": []}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, link := range [][2]int64{{gone.ID, book.ID}, {keep.ID, book.ID}} {
		if err := lib.LinkLorebook(ctx, link[0], link[1]); err != nil {
			t.Fatal(err)
		}
	}
	col, err := lib.CreateCollection(ctx, archive.ID, "Crew", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.AddToCollection(ctx, col.ID, gone.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := lib.Cards.AddSource(ctx, gone.ID, Source{Kind: "chub", Ref: "dg/gone"}); err != nil {
		t.Fatal(err)
	}
	var inst int64
	if err := lib.pool.QueryRow(ctx, `INSERT INTO plugin_instance (plugin, name) VALUES ('fake', 'fake') RETURNING id`).Scan(&inst); err != nil {
		t.Fatal(err)
	}
	if _, err := lib.pool.Exec(ctx, `WITH b AS (INSERT INTO binding (instance_id, card_id, remote_id, remote_state, accepted_data, accepted_hash)
			VALUES ($1, $2, 'r1', 'unchanged', '{}', 'h') RETURNING id)
		INSERT INTO push (binding_id, revision_id, pushed_hash, remote_id) SELECT b.id, $3, 'h', 'r1' FROM b`,
		inst, gone.ID, gone.HeadRevisionID); err != nil {
		t.Fatal(err)
	}
	head, err := lib.Cards.Get(ctx, gone.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.AddEnrichments(ctx, inst, []NewEnrichment{{CardID: gone.ID, RevisionID: head.HeadRevisionID, Key: "quality", Value: []byte(`5`)}}); err != nil {
		t.Fatal(err)
	}

	type batch struct {
		kind *Kind
		n    int
	}
	var seen []batch
	if err := lib.DeleteLibrary(ctx, archive.ID, func(k *Kind, n int) { seen = append(seen, batch{k, n}) }); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(seen, []batch{{Card, 2}, {Lorebook, 1}}) {
		t.Errorf("batches = %v, want 2 cards then 1 lorebook", seen)
	}
	if _, err := lib.GetLibrary(ctx, archive.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("library: %v", err)
	}
	for _, id := range []int64{gone.ID, variant.ID} {
		if _, err := lib.Cards.Get(ctx, id); !errors.Is(err, ErrNotFound) {
			t.Errorf("card %d: %v", id, err)
		}
	}
	for table, n := range map[string]int{
		"card_revision":   lib.countRows(t, `SELECT count(*) FROM card_revision WHERE card_id <> $1`, keep.ID),
		"lorebook":        lib.countRows(t, `SELECT count(*) FROM lorebook`),
		"collection":      lib.countRows(t, `SELECT count(*) FROM collection`),
		"binding":         lib.countRows(t, `SELECT count(*) FROM binding`),
		"push":            lib.countRows(t, `SELECT count(*) FROM push`),
		"enrichment":      lib.countRows(t, `SELECT count(*) FROM enrichment`),
		"card_source":     lib.countRows(t, `SELECT count(*) FROM card_source`),
		"card_lorebook":   lib.countRows(t, `SELECT count(*) FROM card_lorebook`),
		"collection_card": lib.countRows(t, `SELECT count(*) FROM collection_card`),
		"source_detect":   lib.countRows(t, `SELECT count(*) FROM source_detect`),
	} {
		if n != 0 {
			t.Errorf("%s has %d rows left", table, n)
		}
	}
	if _, err := lib.Cards.Get(ctx, keep.ID); err != nil {
		t.Errorf("library 1's card: %v", err)
	}

	if _, err := lib.pool.Exec(ctx, `UPDATE blob SET created_at = now() - interval '1 hour'`); err != nil {
		t.Fatal(err)
	}
	blobs, _, err := lib.CollectGarbage(ctx, time.Minute)
	if err != nil || blobs != 1 {
		t.Fatalf("CollectGarbage = %d, %v; want 1", blobs, err)
	}
	if _, err := lib.Blob(ctx, only); !errors.Is(err, ErrNotFound) {
		t.Errorf("blob only the archive used: %v", err)
	}
	if p, _ := lib.blobs.Path(shared); !fileExists(p) {
		t.Error("shared blob file was deleted")
	}
	if p, _ := lib.blobs.Path(only); fileExists(p) {
		t.Error("unused blob file still exists")
	}
}

func TestVariantRefusesOtherLibrary(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	archive, err := lib.CreateLibrary(ctx, "Archive")
	if err != nil {
		t.Fatal(err)
	}
	here, _ := newCard(t, lib, "here", `{"name": "Here"}`)
	there, _, err := lib.Cards.Create(ctx, archive.ID, "there", "upload", edit(`{"name": "There"}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.Cards.SetVariantOf(ctx, there.ID, &here); !errors.Is(err, ErrInvalid) {
		t.Fatalf("got %v, want ErrInvalid", err)
	}
	if p, err := lib.Cards.Parent(ctx, there.ID); err != nil || p != nil {
		t.Fatalf("parent = %+v, %v", p, err)
	}
}

func TestCollectionRefusesOtherLibrary(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	archive, err := lib.CreateLibrary(ctx, "Archive")
	if err != nil {
		t.Fatal(err)
	}
	here, _ := newCard(t, lib, "here", `{"name": "Here", "tags": ["dwarf"]}`)
	there, _, err := lib.Cards.Create(ctx, archive.ID, "there", "upload", edit(`{"name": "There", "tags": ["elf"]}`))
	if err != nil {
		t.Fatal(err)
	}
	col, err := lib.CreateCollection(ctx, archive.ID, "Crew", "")
	if err != nil {
		t.Fatal(err)
	}
	// Names are unique within a library, not across them.
	if _, err := lib.CreateCollection(ctx, DefaultLibrary, "Crew", ""); err != nil {
		t.Fatalf("same name in another library: %v", err)
	}
	if err := lib.AddToCollection(ctx, col.ID, here); !errors.Is(err, ErrInvalid) {
		t.Fatalf("other library's card: %v, want ErrInvalid", err)
	}
	if err := lib.AddToCollection(ctx, col.ID, there.ID); err != nil {
		t.Fatal(err)
	}
	if err := lib.AddToCollection(ctx, 9999, there.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing collection: %v", err)
	}
	cols, err := lib.Collections(ctx, archive.ID)
	if err != nil || len(cols) != 1 || cols[0].ID != col.ID || cols[0].LibraryID != archive.ID || cols[0].Cards != 1 {
		t.Fatalf("Collections = %+v, %v", cols, err)
	}
	if tags, err := lib.Tags(ctx, archive.ID); err != nil || len(tags) != 1 || tags[0].Name != "elf" {
		t.Fatalf("Tags = %+v, %v", tags, err)
	}
}

func TestForkIntoOtherLibrary(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	archive, err := lib.CreateLibrary(ctx, "Archive")
	if err != nil {
		t.Fatal(err)
	}
	id, _ := newCard(t, lib, "brakka", `{"name": "Brakka"}`)
	col, err := lib.CreateCollection(ctx, DefaultLibrary, "Crew", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.AddToCollection(ctx, col.ID, id); err != nil {
		t.Fatal(err)
	}
	f, err := lib.Cards.Fork(ctx, id, archive.ID, "ui")
	if err != nil {
		t.Fatal(err)
	}
	if f.LibraryID != archive.ID {
		t.Fatalf("fork in library %d", f.LibraryID)
	}
	if p, err := lib.Cards.Parent(ctx, f.ID); err != nil || p != nil {
		t.Errorf("parent = %+v, %v", p, err)
	}
	if cols, err := lib.CardCollections(ctx, f.ID); err != nil || len(cols) != 0 {
		t.Errorf("collections = %v, %v", cols, err)
	}
	srcs, err := lib.Cards.Sources(ctx, f.ID)
	if err != nil || len(srcs) != 1 || srcs[0].Kind != ForkSourceKind || srcs[0].Ref != strconv.FormatInt(id, 10) {
		t.Fatalf("sources = %+v, %v", srcs, err)
	}
	if ids, err := lib.Cards.BySource(ctx, ForkSourceKind, strconv.FormatInt(id, 10)); err != nil || !slices.Equal(ids, []int64{f.ID}) {
		t.Fatalf("BySource = %v, %v", ids, err)
	}
}

func TestInOtherLibraries(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	archive, err := lib.CreateLibrary(ctx, "Archive")
	if err != nil {
		t.Fatal(err)
	}
	data := `{"name": "Brakka", "description": "A dwarf smith who forges runes into every blade she makes."}`
	id, _ := newCard(t, lib, "brakka", data)
	newCard(t, lib, "brakka-here", data)
	there, _, err := lib.Cards.Create(ctx, archive.ID, "brakka-there", "upload", edit(data))
	if err != nil {
		t.Fatal(err)
	}
	got, err := lib.Cards.InOtherLibraries(ctx, id, NearDistance)
	if err != nil || len(got) != 1 || got[0] != (HashMatch{ID: there.ID, LibraryID: archive.ID, Reason: "content"}) {
		t.Fatalf("InOtherLibraries = %+v, %v", got, err)
	}
	if _, err := lib.Cards.InOtherLibraries(ctx, id, NearDistance+1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("wide distance: %v", err)
	}
}

func TestDeleteLibraryTakesCardsAddedWhileDeleting(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	archive, err := lib.CreateLibrary(ctx, "Archive")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := lib.Lorebooks.Create(ctx, archive.ID, "lore", "upload", edit(`{"name": "Lore", "entries": []}`)); err != nil {
		t.Fatal(err)
	}
	var late int64
	// The only batch is the lorebook's, so this card arrives after every batch ran.
	err = lib.DeleteLibrary(ctx, archive.ID, func(*Kind, int) {
		if late != 0 {
			return
		}
		c, _, err := lib.Cards.Create(ctx, archive.ID, "late", "upload", edit(`{"name": "Late"}`))
		if err != nil {
			t.Fatal(err)
		}
		late = c.ID
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.GetLibrary(ctx, archive.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("library: %v", err)
	}
	if _, err := lib.Cards.Get(ctx, late); !errors.Is(err, ErrNotFound) {
		t.Errorf("late card: %v", err)
	}
}

func TestForkLinkIsNotASharedSource(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	id, _ := newCard(t, lib, "brakka", `{"name": "Brakka"}`)
	for range 2 {
		if _, err := lib.Cards.Fork(ctx, id, 0, "ui"); err != nil {
			t.Fatal(err)
		}
	}
	shares := true
	res, err := lib.Search(ctx, Card, Query{Filter: &Filter{SharesSource: &shares}, Facets: []string{"sources"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 0 {
		t.Errorf("sibling forks share a source: %+v", res.Hits)
	}
	all, err := lib.Search(ctx, Card, Query{Facets: []string{"sources"}})
	if err != nil {
		t.Fatal(err)
	}
	if all.Facets != nil && len(all.Facets.Sources) != 0 {
		t.Errorf("sources facet = %+v", all.Facets.Sources)
	}
}

func TestInOtherLibrariesStopsAtFifty(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	archive, err := lib.CreateLibrary(ctx, "Archive")
	if err != nil {
		t.Fatal(err)
	}
	id, _ := newCard(t, lib, "brakka", `{"name": "Brakka"}`)
	for i := range 51 {
		if _, _, err := lib.Cards.Create(ctx, archive.ID, "copy-"+strconv.Itoa(i), "upload", edit(`{"name": "Brakka"}`)); err != nil {
			t.Fatal(err)
		}
	}
	got, err := lib.Cards.InOtherLibraries(ctx, id, NearDistance)
	if err != nil || len(got) != 50 {
		t.Fatalf("got %d matches, %v; want 50", len(got), err)
	}
}

func TestLibraryNamesAreTrimmed(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	if _, err := lib.CreateLibrary(ctx, "   "); !errors.Is(err, ErrInvalid) {
		t.Fatalf("blank name: %v, want ErrInvalid", err)
	}
	l, err := lib.CreateLibrary(ctx, "  Archive ")
	if err != nil || l.Name != "Archive" {
		t.Fatalf("CreateLibrary = %+v, %v", l, err)
	}
	if _, err := lib.RenameLibrary(ctx, l.ID, " \t"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("blank rename: %v, want ErrInvalid", err)
	}
	if _, err := lib.pool.Exec(ctx, `UPDATE library SET name = ' x' WHERE id = $1`, l.ID); err == nil {
		t.Fatal("database took an untrimmed name")
	}
}

func TestLibrarySettingOverrides(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	archive, err := lib.CreateLibrary(ctx, "Archive")
	if err != nil {
		t.Fatal(err)
	}
	if v, err := lib.LibrarySetting(ctx, archive.ID, "score_instance"); err != nil || v != "" {
		t.Fatalf("unset = %q, %v", v, err)
	}
	if err := lib.SetLibrarySetting(ctx, archive.ID, "score_instance", "7"); err != nil {
		t.Fatal(err)
	}
	if err := lib.SetLibrarySetting(ctx, archive.ID, "score_instance", "8"); err != nil {
		t.Fatal(err)
	}
	if v, err := lib.LibrarySetting(ctx, archive.ID, "score_instance"); err != nil || v != "8" {
		t.Fatalf("set = %q, %v", v, err)
	}
	if v, _ := lib.LibrarySetting(ctx, DefaultLibrary, "score_instance"); v != "" {
		t.Fatalf("another library's override leaked: %q", v)
	}
	if err := lib.SetLibrarySetting(ctx, archive.ID+100, "score_instance", "7"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing library: %v", err)
	}
	if err := lib.SetLibrarySetting(ctx, archive.ID, "score_instance", ""); err != nil {
		t.Fatal(err)
	}
	if v, _ := lib.LibrarySetting(ctx, archive.ID, "score_instance"); v != "" {
		t.Fatalf("cleared = %q", v)
	}
	if err := lib.SetLibrarySetting(ctx, archive.ID, "score_instance", "9"); err != nil {
		t.Fatal(err)
	}
	if err := lib.DeleteLibrary(ctx, archive.ID, func(*Kind, int) {}); err != nil {
		t.Fatalf("delete a library with an override: %v", err)
	}
}

func TestDeleteCardsRefusesCardsBoundToAnApp(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	plain, _ := newCard(t, lib, "plain", `{"name": "Plain"}`)
	pushed, _ := newCard(t, lib, "pushed", `{"name": "Pushed"}`)
	imported, _ := newCard(t, lib, "imported", `{"name": "Imported"}`)
	lore, _, err := lib.Lorebooks.Create(ctx, DefaultLibrary, "lore", "upload", edit(`{"name": "Lore", "entries": []}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.LinkLorebook(ctx, plain, lore.ID); err != nil {
		t.Fatal(err)
	}
	var inst int64
	if err := lib.pool.QueryRow(ctx, `INSERT INTO plugin_instance (plugin, name) VALUES ('fake', 'fake') RETURNING id`).Scan(&inst); err != nil {
		t.Fatal(err)
	}
	if _, err := lib.pool.Exec(ctx, `INSERT INTO binding (instance_id, card_id, remote_id, remote_state, accepted_data, accepted_hash, imported)
		VALUES ($1, $2, 'r1', 'unchanged', '{}', 'h', false), ($1, $3, 'r2', 'unchanged', '{}', 'h', true)`, inst, pushed, imported); err != nil {
		t.Fatal(err)
	}
	deleted, refused, err := lib.DeleteCards(ctx, []int64{plain, pushed, imported, 999999})
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 2 || !slices.Equal(refused, []int64{pushed}) {
		t.Fatalf("deleted %d, refused %v", deleted, refused)
	}
	for _, id := range []int64{plain, imported} {
		if _, err := lib.Cards.Get(ctx, id); !errors.Is(err, ErrNotFound) {
			t.Errorf("card %d: %v, want deleted", id, err)
		}
	}
	if _, err := lib.Cards.Get(ctx, pushed); err != nil {
		t.Errorf("pushed card: %v", err)
	}
	if _, err := lib.Lorebooks.Get(ctx, lore.ID); err != nil {
		t.Errorf("a deleted card's lorebook: %v", err)
	}
}
