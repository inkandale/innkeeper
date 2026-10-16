package library

import (
	"context"
	"errors"
	"slices"
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
