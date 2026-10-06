package library

import (
	"context"
	"errors"
	"slices"
	"testing"
)

const chubCard = `{"name": "A", "extensions": {"chub": {"id": 7, "full_path": "u/a"}}}`

func newLorebook(t *testing.T, lib *Library, slug string) int64 {
	t.Helper()
	l, _, err := lib.Lorebooks.Create(context.Background(), slug, "upload", edit(`{"name": "L"}`))
	if err != nil {
		t.Fatal(err)
	}
	return l.ID
}

func TestSourcesLifecycle(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	id, _ := newCard(t, lib, "a", `{"name": "A"}`)
	other, _ := newCard(t, lib, "b", `{"name": "B"}`)
	s := lib.Cards

	for range 2 {
		if err := s.DetectSources(ctx, id, []byte(chubCard)); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Sources(ctx, id)
	if err != nil || len(got) != 1 || got[0].LinkedBy != "auto" || got[0].ExternalID != "7" || got[0].Ref != "u/a" {
		t.Fatalf("after detect twice: %+v, %v", got, err)
	}

	// Remove the auto row: detection must not bring it back.
	if err := s.RemoveSource(ctx, id, got[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DetectSources(ctx, id, []byte(chubCard)); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Sources(ctx, id); len(got) != 0 {
		t.Fatalf("removed source came back: %+v", got)
	}
	if ids, _ := s.BySource(ctx, "chub", "u/a"); len(ids) != 0 {
		t.Fatalf("BySource saw a removed row: %v", ids)
	}
	if err := s.RemoveSource(ctx, id, got0(t, s, id)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("remove twice: %v", err)
	}

	// Adding the removed ref restores it as owner.
	added, err := s.AddSource(ctx, id, Source{Kind: "chub", Ref: "u/a"})
	if err != nil || added.LinkedBy != "owner" {
		t.Fatalf("restore: %+v, %v", added, err)
	}
	if err := s.DetectSources(ctx, id, []byte(chubCard)); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Sources(ctx, id); len(got) != 1 || got[0].LinkedBy != "owner" || got[0].ID != added.ID {
		t.Fatalf("after restore and detect: %+v", got)
	}

	// Two cards share a source; an owner add is listed.
	if _, err := s.AddSource(ctx, other, Source{Kind: "chub", Ref: "u/a"}); err != nil {
		t.Fatal(err)
	}
	if ids, _ := s.BySource(ctx, "chub", "u/a"); !slices.Equal(ids, []int64{id, other}) {
		t.Fatalf("BySource = %v", ids)
	}
	if err := s.RemoveSource(ctx, other, added.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other card's source: %v", err)
	}

	for _, bad := range []Source{{Ref: "x"}, {Kind: "chub"}, {Kind: "Chub", Ref: "x"}} {
		if _, err := s.AddSource(ctx, id, bad); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%+v: got %v, want ErrInvalid", bad, err)
		}
	}
	if _, err := s.AddSource(ctx, 999, Source{Kind: "chub", Ref: "x"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown id: %v", err)
	}
}

func got0(t *testing.T, s *Entities, id int64) int64 {
	t.Helper()
	var sid int64
	if err := s.pool.QueryRow(context.Background(), `SELECT id FROM `+s.k.table+`_source WHERE `+s.k.fk+` = $1`, id).Scan(&sid); err != nil {
		t.Fatal(err)
	}
	return sid
}

func TestLorebookSources(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	a, b := newLorebook(t, lib, "a"), newLorebook(t, lib, "b")
	s := lib.Lorebooks
	if err := s.DetectSources(ctx, a, []byte(chubCard)); err != nil {
		t.Fatal(err)
	}
	got, err := s.Sources(ctx, a)
	if err != nil || len(got) != 1 || got[0].URL != "https://chub.ai/lorebooks/u/a" {
		t.Fatalf("detect: %+v, %v", got, err)
	}
	if err := s.RemoveSource(ctx, b, got[0].ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other lorebook's source: %v", err)
	}
	if err := s.RemoveSource(ctx, a, got[0].ID); err != nil {
		t.Fatal(err)
	}
	if ids, _ := s.BySource(ctx, "chub", "u/a"); len(ids) != 0 {
		t.Fatalf("BySource saw a removed row: %v", ids)
	}
	if err := s.DetectSources(ctx, a, []byte(chubCard)); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Sources(ctx, a); len(got) != 0 {
		t.Fatalf("removed source came back: %+v", got)
	}
	// A card's source of the same ref is separate.
	if ids, _ := lib.Cards.BySource(ctx, "chub", "u/a"); len(ids) != 0 {
		t.Fatalf("cards saw a lorebook source: %v", ids)
	}
}
