package library

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/inkandale/innkeeper/internal/blobstore"
	"github.com/inkandale/innkeeper/internal/testdb"
)

func newLibrary(t *testing.T) *Library {
	t.Helper()
	return New(testdb.New(t), &blobstore.Store{Name: "library", Root: t.TempDir()})
}

func edit(data string) Edit {
	return Edit{Data: json.RawMessage(data), Source: "ui"}
}

// newCard creates a card with the given data and returns its ID and head revision ID.
func newCard(t *testing.T, lib *Library, slug, data string) (int64, int64) {
	t.Helper()
	c, rev, err := lib.Cards.Create(context.Background(), slug, "upload", edit(data))
	if err != nil {
		t.Fatal(err)
	}
	return c.ID, rev.ID
}

func TestCreateAndUpdateCard(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	c, first, err := lib.Cards.Create(ctx, "brakka", "upload", edit(`{"name": "Brakka", "description": "A dwarf."}`))
	if err != nil {
		t.Fatal(err)
	}
	if c.HeadRevisionID != first.ID || first.ParentID != nil || first.TextSimhash == nil || first.ContentHash == "" {
		t.Fatalf("unexpected create result: %+v %+v", c, first)
	}
	if string(first.Overrides) != "{}" {
		t.Fatalf("overrides = %s, want {}", first.Overrides)
	}

	second, err := lib.Cards.Update(ctx, c.ID, first.ID, Edit{
		Data:      json.RawMessage(`{"name": "Brakka", "description": "A grumpy dwarf."}`),
		Overrides: json.RawMessage(`{"7": {"name": "Brakka (ST)"}}`),
		Message:   "grumpier", Source: "mcp",
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.ParentID == nil || *second.ParentID != first.ID || second.ContentHash == first.ContentHash {
		t.Fatalf("unexpected second revision: %+v", second)
	}

	got, err := lib.Cards.Get(ctx, c.ID)
	if err != nil || got.HeadRevisionID != second.ID {
		t.Fatalf("head = %+v, %v; want revision %d", got, err, second.ID)
	}
	hist, err := lib.Cards.History(ctx, c.ID)
	if err != nil || len(hist) != 2 || hist[0].ID != second.ID || hist[1].ID != first.ID {
		t.Fatalf("history = %+v, %v", hist, err)
	}
	if hist[0].Message != "grumpier" || hist[0].Source != "mcp" {
		t.Fatalf("history[0] = %+v", hist[0])
	}
}

func TestUpdateFromStaleBaseConflicts(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	id, first := newCard(t, lib, "a", `{"name": "A"}`)
	if _, err := lib.Cards.Update(ctx, id, first, edit(`{"name": "B"}`)); err != nil {
		t.Fatal(err)
	}
	_, err := lib.Cards.Update(ctx, id, first, edit(`{"name": "C"}`))
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("got %v, want ErrConflict", err)
	}
	if _, err := lib.Cards.Update(ctx, 999, first, edit(`{"name": "C"}`)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing card: got %v, want ErrNotFound", err)
	}
}

func TestCreateValidation(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	newCard(t, lib, "taken", `{"name": "A"}`)
	cases := map[string]struct {
		slug string
		e    Edit
		want error
	}{
		"slug taken":     {"taken", edit(`{"name": "B"}`), ErrSlugTaken},
		"bad slug":       {"Not A Slug", edit(`{}`), nil},
		"array data":     {"x", edit(`[]`), nil},
		"embedded book":  {"x", edit(`{"character_book": {}}`), nil},
		"bad source":     {"x", Edit{Data: json.RawMessage(`{}`), Source: "cli"}, nil},
		"missing avatar": {"x", Edit{Data: json.RawMessage(`{}`), Source: "ui", AvatarBlob: "00"}, ErrNotFound},
	}
	for name, c := range cases {
		_, _, err := lib.Cards.Create(ctx, c.slug, "upload", c.e)
		if err == nil || (c.want != nil && !errors.Is(err, c.want)) {
			t.Errorf("%s: got %v, want error %v", name, err, c.want)
		}
	}
}

func TestSetArchived(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	id, _ := newCard(t, lib, "a", `{"name": "A"}`)
	if err := lib.Cards.SetArchived(ctx, id, true); err != nil {
		t.Fatal(err)
	}
	if c, _ := lib.Cards.Get(ctx, id); c.ArchivedAt == nil {
		t.Fatal("not archived")
	}
	if err := lib.Cards.SetArchived(ctx, id, false); err != nil {
		t.Fatal(err)
	}
	if c, _ := lib.Cards.Get(ctx, id); c.ArchivedAt != nil {
		t.Fatal("still archived")
	}
	if err := lib.Cards.SetArchived(ctx, 999, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

func TestLorebooks(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	b, rev, err := lib.Lorebooks.Create(ctx, "dwarf-lore", "upload",
		edit(`{"name": "Dwarf lore", "entries": [{"keys": ["forge"], "content": "The forge never cools."}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if rev.TextSimhash == nil {
		t.Fatal("lorebook has no simhash")
	}
	if _, err := lib.Lorebooks.Update(ctx, b.ID, rev.ID, edit(`{"name": "Dwarf lore", "entries": []}`)); err != nil {
		t.Fatal(err)
	}
	// Cards and lorebooks have separate slug namespaces.
	if _, _, err := lib.Cards.Create(ctx, "dwarf-lore", "upload", edit(`{"name": "x"}`)); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateTxRollbackLeavesHead(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	id, first := newCard(t, lib, "a", `{"name": "A"}`)
	tx, err := lib.Cards.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rev, err := lib.Cards.UpdateTx(ctx, tx, id, first, edit(`{"name": "B"}`))
	if err != nil || rev.ID == first {
		t.Fatalf("UpdateTx = %+v, %v", rev, err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	c, err := lib.Cards.Get(ctx, id)
	if err != nil || c.HeadRevisionID != first {
		t.Fatalf("head = %d, %v; want %d", c.HeadRevisionID, err, first)
	}
}

func TestEntitiesStartInDefaultLibrary(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	c, _, err := lib.Cards.Create(ctx, "brakka", "upload", edit(`{"name": "Brakka"}`))
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := lib.Lorebooks.Create(ctx, "dwarf-lore", "upload", edit(`{"name": "Dwarf lore", "entries": []}`))
	if err != nil {
		t.Fatal(err)
	}
	if c.LibraryID != DefaultLibrary || b.LibraryID != DefaultLibrary {
		t.Fatalf("card in %d, lorebook in %d, want %d", c.LibraryID, b.LibraryID, DefaultLibrary)
	}
	got, err := lib.Cards.Get(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.LibraryID != DefaultLibrary {
		t.Fatalf("Get: library %d, want %d", got.LibraryID, DefaultLibrary)
	}
	var name string
	if err := lib.pool.QueryRow(ctx, `SELECT name FROM library WHERE id = $1`, DefaultLibrary).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "Library" {
		t.Fatalf("default library is named %q", name)
	}
	// The next library gets ID 2, not a conflict with library 1.
	var next int64
	if err := lib.pool.QueryRow(ctx, `INSERT INTO library (name) VALUES ('Archive') RETURNING id`).Scan(&next); err != nil {
		t.Fatal(err)
	}
	if next != 2 {
		t.Fatalf("next library ID = %d, want 2", next)
	}
}
