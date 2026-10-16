package library

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// setVariantOf makes parent the parent of id, failing the test on an error.
func setVariantOf(t *testing.T, s *Entities, id, parent int64) {
	t.Helper()
	if err := s.SetVariantOf(context.Background(), id, &parent); err != nil {
		t.Fatal(err)
	}
}

func refIDs(refs []EntityRef) []int64 {
	out := []int64{}
	for _, r := range refs {
		out = append(out, r.ID)
	}
	return out
}

func TestSetVariantOfRefusesACycle(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	a, _ := newCard(t, lib, "a", `{"name": "A"}`)
	b, _ := newCard(t, lib, "b", `{"name": "B"}`)
	c, _ := newCard(t, lib, "c", `{"name": "C"}`)
	setVariantOf(t, lib.Cards, b, a)
	setVariantOf(t, lib.Cards, c, b)
	if err := lib.Cards.SetVariantOf(ctx, a, &c); !errors.Is(err, ErrVariantCycle) {
		t.Fatalf("a under its own grandchild: %v, want ErrVariantCycle", err)
	}
	if err := lib.Cards.SetVariantOf(ctx, a, &a); !errors.Is(err, ErrVariantCycle) {
		t.Fatalf("self-link: %v, want ErrVariantCycle", err)
	}
	if err := lib.Cards.SetVariantOf(ctx, a, ptr(int64(999999))); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing parent: %v, want ErrNotFound", err)
	}
	if err := lib.Cards.SetVariantOf(ctx, 999999, &a); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing entity: %v, want ErrNotFound", err)
	}
	if p, err := lib.Cards.Parent(ctx, c); err != nil || p == nil || p.ID != b || p.Slug != "b" || p.Name != "B" {
		t.Fatalf("parent of c = %+v, %v", p, err)
	}
	if kids, err := lib.Cards.Variants(ctx, a); err != nil || !slices.Equal(refIDs(kids), []int64{b}) {
		t.Fatalf("variants of a = %+v, %v", kids, err)
	}
}

func TestClearingAVariantLinkMakesNoRevision(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	a, _ := newCard(t, lib, "a", `{"name": "A"}`)
	b, head := newCard(t, lib, "b", `{"name": "B"}`)
	setVariantOf(t, lib.Cards, b, a)
	if err := lib.Cards.SetVariantOf(ctx, b, nil); err != nil {
		t.Fatal(err)
	}
	if p, err := lib.Cards.Parent(ctx, b); err != nil || p != nil {
		t.Fatalf("parent after clearing = %+v, %v", p, err)
	}
	if e, err := lib.Cards.Get(ctx, b); err != nil || e.HeadRevisionID != head {
		t.Fatalf("head = %+v, %v; want revision %d", e, err, head)
	}
}

func TestForkCopiesTagsAndLinksButNotSources(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	avatar, err := lib.PutBlob(ctx, strings.NewReader("avatar bytes"))
	if err != nil {
		t.Fatal(err)
	}
	src, srcRev, err := lib.Cards.Create(ctx, "brakka", "upload", Edit{Data: json.RawMessage(`{"name": "Brakka", "tags": ["dwarf"]}`),
		AvatarBlob: avatar.SHA256, Overrides: json.RawMessage(`{"7": {"name": "Brak"}}`), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	id, head := src.ID, srcRev.ID
	col, err := lib.CreateCollection(ctx, "Smiths", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.AddToCollection(ctx, col.ID, id); err != nil {
		t.Fatal(err)
	}
	book := newLorebook(t, lib, "forge")
	if err := lib.LinkLorebook(ctx, id, book); err != nil {
		t.Fatal(err)
	}
	if _, err := lib.Cards.AddSource(ctx, id, Source{Kind: "chub", Ref: "dg/brakka"}); err != nil {
		t.Fatal(err)
	}

	if _, err := lib.pool.Exec(ctx, `WITH i AS (INSERT INTO plugin_instance (plugin, name) VALUES ('fake', 'fake') RETURNING id)
		INSERT INTO binding (instance_id, card_id, remote_id, remote_state, accepted_data, accepted_hash)
		SELECT i.id, $1, 'r1', 'unchanged', '{}', 'h' FROM i`, id); err != nil {
		t.Fatal(err)
	}

	v, err := lib.Cards.Fork(ctx, id, "ui")
	if err != nil {
		t.Fatal(err)
	}
	if v.Slug != "brakka-variant" || v.Provenance != "variant" {
		t.Fatalf("fork = %+v", v)
	}
	rev, err := lib.Cards.Revision(ctx, v.HeadRevisionID)
	if err != nil || rev.Message != fmt.Sprintf("Variant of brakka at r%d", head) || rev.Source != "ui" {
		t.Fatalf("revision = %+v, %v", rev, err)
	}
	if rev.AvatarBlob == nil || *rev.AvatarBlob != avatar.SHA256 || string(rev.Overrides) != string(srcRev.Overrides) {
		t.Errorf("avatar = %v, overrides = %s", rev.AvatarBlob, rev.Overrides)
	}
	if tags := headTags(t, lib, v.ID); !slices.Equal(tags, []string{"dwarf"}) {
		t.Errorf("tags = %v", tags)
	}
	if cols, err := lib.CardCollections(ctx, v.ID); err != nil || !slices.Equal(cols, []int64{col.ID}) {
		t.Errorf("collections = %v, %v", cols, err)
	}
	if books, err := lib.CardLorebooks(ctx, v.ID); err != nil || !slices.Equal(books, []int64{book}) {
		t.Errorf("lorebooks = %v, %v", books, err)
	}
	if srcs, err := lib.Cards.Sources(ctx, v.ID); err != nil || len(srcs) != 0 {
		t.Errorf("sources = %v, %v", srcs, err)
	}
	var bindings int
	if err := lib.pool.QueryRow(ctx, `SELECT count(*) FROM binding WHERE card_id = $1`, v.ID).Scan(&bindings); err != nil || bindings != 0 {
		t.Errorf("bindings = %d, %v", bindings, err)
	}
	if p, err := lib.Cards.Parent(ctx, v.ID); err != nil || p == nil || p.ID != id {
		t.Errorf("parent = %+v, %v", p, err)
	}

	again, err := lib.Cards.Fork(ctx, id, "ui")
	if err != nil || again.Slug != "brakka-variant-2" {
		t.Fatalf("second fork = %+v, %v", again, err)
	}
	lv, err := lib.Lorebooks.Fork(ctx, book, "mcp")
	if err != nil || lv.Slug != "forge-variant" || lv.Provenance != "variant" {
		t.Fatalf("lorebook fork = %+v, %v", lv, err)
	}
	if p, err := lib.Lorebooks.Parent(ctx, lv.ID); err != nil || p == nil || p.ID != book {
		t.Errorf("lorebook parent = %+v, %v", p, err)
	}
}

func TestForkOfAMissingEntityIsNotFound(t *testing.T) {
	lib := newLibrary(t)
	if _, err := lib.Cards.Fork(context.Background(), 999999, "ui"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}
