package library

import (
	"context"
	"errors"
	"slices"
	"testing"
)

// bindCard binds card to a new plugin instance, as an import binding when
// imported, and returns the instance's ID.
func bindCard(t *testing.T, lib *Library, card int64, imported bool) int64 {
	t.Helper()
	var instance int64
	if err := lib.pool.QueryRow(context.Background(), `WITH i AS (INSERT INTO plugin_instance (plugin, name) VALUES ('fake', 'fake ' || $2::boolean) RETURNING id)
		INSERT INTO binding (instance_id, card_id, remote_id, remote_state, accepted_data, accepted_hash, imported)
		SELECT i.id, $1, 'r1', 'unchanged', '{}', 'h', $2::boolean FROM i RETURNING instance_id`, card, imported).Scan(&instance); err != nil {
		t.Fatal(err)
	}
	return instance
}

func libraryOf(t *testing.T, s *Entities, id int64) int64 {
	t.Helper()
	e, err := s.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return e.LibraryID
}

func TestMoveKeepsSlugAndRevisions(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	dest := newLibraryRow(t, lib, "Other")
	id, first := newCard(t, lib, "brakka", `{"name": "Brakka"}`)
	second, err := lib.Cards.Update(ctx, id, first, edit(`{"name": "Brakka II"}`))
	if err != nil {
		t.Fatal(err)
	}

	res, err := lib.Cards.Move(ctx, []int64{id}, dest)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(res.Moved, []int64{id}) || !slices.Equal(res.FromLibraries, []int64{DefaultLibrary}) {
		t.Fatalf("result = %+v", res)
	}
	e, err := lib.Cards.Get(ctx, id)
	if err != nil || e.LibraryID != dest || e.Slug != "brakka" || e.HeadRevisionID != second.ID {
		t.Fatalf("card = %+v, %v", e, err)
	}
	if revs, err := lib.Cards.History(ctx, id); err != nil || len(revs) != 2 {
		t.Fatalf("history = %d revisions, %v", len(revs), err)
	}
}

func TestMoveDropsCollectionMemberships(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	dest := newLibraryRow(t, lib, "Other")
	id, _ := newCard(t, lib, "a", `{"name": "A"}`)
	for _, name := range []string{"Smiths", "Dwarves"} {
		col, err := lib.CreateCollection(ctx, DefaultLibrary, name, "")
		if err != nil {
			t.Fatal(err)
		}
		if err := lib.AddToCollection(ctx, col.ID, id); err != nil {
			t.Fatal(err)
		}
	}

	res, err := lib.Cards.Move(ctx, []int64{id}, dest)
	if err != nil || res.CollectionMembershipsDropped != 2 {
		t.Fatalf("result = %+v, %v", res, err)
	}
	if cols, err := lib.CardCollections(ctx, id); err != nil || len(cols) != 0 {
		t.Fatalf("collections = %v, %v", cols, err)
	}
}

func TestMoveTakesDescendantsIncludingArchived(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	dest := newLibraryRow(t, lib, "Other")
	root, _ := newCard(t, lib, "root", `{"name": "R"}`)
	child, _ := newCard(t, lib, "child", `{"name": "C"}`)
	grandchild, _ := newCard(t, lib, "grandchild", `{"name": "G"}`)
	bystander, _ := newCard(t, lib, "bystander", `{"name": "B"}`)
	setVariantOf(t, lib.Cards, child, root)
	setVariantOf(t, lib.Cards, grandchild, child)
	if err := lib.Cards.SetArchived(ctx, child, true); err != nil {
		t.Fatal(err)
	}

	res, err := lib.Cards.Move(ctx, []int64{root}, dest)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(res.Moved, []int64{root, child, grandchild}) || res.Archived != 1 || len(res.VariantLinksCleared) != 0 {
		t.Fatalf("result = %+v", res)
	}
	for _, id := range []int64{root, child, grandchild} {
		if got := libraryOf(t, lib.Cards, id); got != dest {
			t.Errorf("card %d in library %d, want %d", id, got, dest)
		}
	}
	if got := libraryOf(t, lib.Cards, bystander); got != DefaultLibrary {
		t.Errorf("bystander in library %d", got)
	}
	if e, err := lib.Cards.Get(ctx, child); err != nil || e.ArchivedAt == nil {
		t.Errorf("child = %+v, %v; want still archived", e, err)
	}
	if p, err := lib.Cards.Parent(ctx, grandchild); err != nil || p == nil || p.ID != child {
		t.Errorf("parent of grandchild = %+v, %v", p, err)
	}
}

func TestMoveClearsLinkToParentLeftBehind(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	dest := newLibraryRow(t, lib, "Other")
	parent, _ := newCard(t, lib, "parent", `{"name": "P"}`)
	child, _ := newCard(t, lib, "child", `{"name": "C"}`)
	grandchild, _ := newCard(t, lib, "grandchild", `{"name": "G"}`)
	setVariantOf(t, lib.Cards, child, parent)
	setVariantOf(t, lib.Cards, grandchild, child)

	res, err := lib.Cards.Move(ctx, []int64{child}, dest)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(res.Moved, []int64{child, grandchild}) || !slices.Equal(res.VariantLinksCleared, []int64{child}) {
		t.Fatalf("result = %+v", res)
	}
	if p, err := lib.Cards.Parent(ctx, child); err != nil || p != nil {
		t.Errorf("parent of child = %+v, %v; want none", p, err)
	}
	if p, err := lib.Cards.Parent(ctx, grandchild); err != nil || p == nil || p.ID != child {
		t.Errorf("parent of grandchild = %+v, %v", p, err)
	}
}

func TestMoveKeepsLorebookLinksAndBindings(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	dest := newLibraryRow(t, lib, "Other")
	id, _ := newCard(t, lib, "a", `{"name": "A"}`)
	book := newLorebook(t, lib, "forge")
	if err := lib.LinkLorebook(ctx, id, book); err != nil {
		t.Fatal(err)
	}
	instance := bindCard(t, lib, id, false)
	bindCard(t, lib, id, true)

	res, err := lib.Cards.Move(ctx, []int64{id}, dest)
	if err != nil || !slices.Equal(res.BoundInstances, []int64{instance}) {
		t.Fatalf("result = %+v, %v", res, err)
	}
	if books, err := lib.CardLorebooks(ctx, id); err != nil || !slices.Equal(books, []int64{book}) {
		t.Errorf("lorebooks = %v, %v", books, err)
	}
	var bindings int
	if err := lib.pool.QueryRow(ctx, `SELECT count(*) FROM binding WHERE card_id = $1`, id).Scan(&bindings); err != nil || bindings != 2 {
		t.Errorf("bindings = %d, %v", bindings, err)
	}
}

func TestMoveToOwnLibraryChangesNothing(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	parent, _ := newCard(t, lib, "parent", `{"name": "P"}`)
	child, _ := newCard(t, lib, "child", `{"name": "C"}`)
	setVariantOf(t, lib.Cards, child, parent)
	col, err := lib.CreateCollection(ctx, DefaultLibrary, "Smiths", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.AddToCollection(ctx, col.ID, child); err != nil {
		t.Fatal(err)
	}

	res, err := lib.Cards.Move(ctx, []int64{child}, DefaultLibrary)
	if err != nil || len(res.Moved) != 0 || len(res.VariantLinksCleared) != 0 || res.CollectionMembershipsDropped != 0 {
		t.Fatalf("result = %+v, %v", res, err)
	}
	if p, err := lib.Cards.Parent(ctx, child); err != nil || p == nil || p.ID != parent {
		t.Errorf("parent = %+v, %v", p, err)
	}
	if cols, err := lib.CardCollections(ctx, child); err != nil || !slices.Equal(cols, []int64{col.ID}) {
		t.Errorf("collections = %v, %v", cols, err)
	}
}

func TestMoveToMissingLibraryChangesNothing(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	id, _ := newCard(t, lib, "a", `{"name": "A"}`)

	if _, err := lib.Cards.Move(ctx, []int64{id}, 999999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if got := libraryOf(t, lib.Cards, id); got != DefaultLibrary {
		t.Errorf("card in library %d", got)
	}
}

func TestMoveRefusesArchivedSelection(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	dest := newLibraryRow(t, lib, "Other")
	live, _ := newCard(t, lib, "live", `{"name": "L"}`)
	hidden, _ := newCard(t, lib, "hidden", `{"name": "H"}`)
	if _, _, err := lib.SetHidden(ctx, DefaultLibrary, map[int64]string{hidden: ReasonDuplicate}); err != nil {
		t.Fatal(err)
	}

	if _, err := lib.Cards.Move(ctx, []int64{live, hidden}, dest); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if got := libraryOf(t, lib.Cards, live); got != DefaultLibrary {
		t.Errorf("live card in library %d", got)
	}
}

func TestMoveRefusesUnknownID(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	dest := newLibraryRow(t, lib, "Other")
	id, _ := newCard(t, lib, "a", `{"name": "A"}`)

	if _, err := lib.Cards.Move(ctx, []int64{id, 999999}, dest); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if got := libraryOf(t, lib.Cards, id); got != DefaultLibrary {
		t.Errorf("card in library %d", got)
	}
}

func TestMoveTakesLorebookVariants(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	dest := newLibraryRow(t, lib, "Other")
	book := newLorebook(t, lib, "forge")
	variant := newLorebook(t, lib, "forge-two")
	setVariantOf(t, lib.Lorebooks, variant, book)

	res, err := lib.Lorebooks.Move(ctx, []int64{book}, dest)
	if err != nil || !slices.Equal(res.Moved, []int64{book, variant}) || res.CollectionMembershipsDropped != 0 {
		t.Fatalf("result = %+v, %v", res, err)
	}
	if got := libraryOf(t, lib.Lorebooks, variant); got != dest {
		t.Errorf("variant in library %d", got)
	}
}

func TestMoveDedupesOverlappingSelection(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	src := newLibraryRow(t, lib, "Source")
	dest := newLibraryRow(t, lib, "Other")
	root, _ := newCard(t, lib, "root", `{"name": "R"}`)
	child, _ := newCard(t, lib, "child", `{"name": "C"}`)
	setVariantOf(t, lib.Cards, child, root)
	elsewhere, _, err := lib.Cards.Create(ctx, src, "elsewhere", "upload", edit(`{"name": "E"}`))
	if err != nil {
		t.Fatal(err)
	}

	res, err := lib.Cards.Move(ctx, []int64{elsewhere.ID, child, root, root}, dest)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(res.Moved, []int64{root, child, elsewhere.ID}) || !slices.Equal(res.FromLibraries, []int64{DefaultLibrary, src}) {
		t.Fatalf("result = %+v", res)
	}
}

func TestDeletingTheSourceLibraryDuringAMoveKeepsTheMovedCard(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	src := newLibraryRow(t, lib, "Source")
	dest := newLibraryRow(t, lib, "Other")
	card, _, err := lib.Cards.Create(ctx, src, "a", "upload", edit(`{"name": "A"}`))
	if err != nil {
		t.Fatal(err)
	}

	tx, err := lib.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := lib.Cards.MoveTx(ctx, tx, []int64{card.ID}, dest); err != nil {
		t.Fatal(err)
	}
	deleted := make(chan error, 1)
	go func() { deleted <- lib.DeleteLibrary(ctx, src, func(*Kind, int) {}) }()
	// The delete must be waiting on the move's row lock before the move commits.
	for waiting := false; !waiting; {
		if err := lib.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock')`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-deleted; err != nil {
		t.Fatal(err)
	}
	if got := libraryOf(t, lib.Cards, card.ID); got != dest {
		t.Fatalf("card in library %d, want %d", got, dest)
	}
}
