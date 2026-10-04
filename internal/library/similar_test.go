package library

import (
	"context"
	"slices"
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
