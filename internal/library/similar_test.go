package library

import (
	"context"
	"slices"
	"strings"
	"testing"
)

func TestSimilarCards(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	const text = "Brakka is a grumpy dwarf blacksmith who runs the forge at the edge of town. " +
		"She hates elves, loves good ale, and never turns away a customer with honest coin."
	orig, _ := newCard(t, lib, "orig", `{"name": "Brakka", "description": "`+text+`"}`)
	dup, _ := newCard(t, lib, "copy", `{"name": "Brakka", "description": " `+text+`\r\n", "creator_notes": ""}`)
	edited, _ := newCard(t, lib, "edited", `{"name": "Brakka 2", "description": "`+text+` She has a cat."}`)
	newCard(t, lib, "elf", `{"name": "Lia", "description": "A cheerful elven bard wanders the coast singing songs of the sea."}`)

	got, err := lib.SimilarCards(ctx, orig, 10)
	if err != nil {
		t.Fatal(err)
	}
	want := []Match{{dup, "content", 0}, {dup, "text", 0}}
	if len(got) != 3 || !slices.Equal(got[:2], want) || got[2].CardID != edited || got[2].Reason != "text" {
		t.Fatalf("matches = %+v", got)
	}
}

func TestMatchHashes(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	const text = "Brakka is a grumpy dwarf blacksmith who runs the forge at the edge of town. " +
		"She hates elves, loves good ale, and never turns away a customer with honest coin."
	orig, rev := newCard(t, lib, "orig", `{"name": "Brakka", "description": "`+text+`"}`)
	edited, _ := newCard(t, lib, "edited", `{"name": "Brakka 2", "description": "`+text+` She has a cat."}`)
	newCard(t, lib, "elf", `{"name": "Lia", "description": "A cheerful elven bard wanders the coast singing songs of the sea."}`)
	r, err := lib.Cards.Revision(ctx, rev)
	if err != nil {
		t.Fatal(err)
	}

	got, err := lib.Cards.MatchHashes(ctx, []Hashes{
		{ContentHash: r.ContentHash, TextSimhash: r.TextSimhash},
		{ContentHash: strings.Repeat("0", 64)},
	}, 10)
	if err != nil {
		t.Fatal(err)
	}
	// orig matches by content and by text, and is listed once, by content.
	if len(got) != 2 || len(got[0]) != 2 || got[0][0] != (HashMatch{orig, "content", 0}) ||
		got[0][1].ID != edited || got[0][1].Reason != "text" || len(got[1]) != 0 {
		t.Fatalf("matches = %+v", got)
	}
	if got, err := lib.Lorebooks.MatchHashes(ctx, []Hashes{{ContentHash: r.ContentHash}}, 10); err != nil || len(got[0]) != 0 {
		t.Fatalf("lorebook matches = %+v, %v", got, err)
	}
	if got, err := lib.Cards.MatchHashes(ctx, nil, 10); err != nil || len(got) != 0 {
		t.Fatalf("no hashes = %+v, %v", got, err)
	}
}
