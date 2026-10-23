package library

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"

	"github.com/inkandale/innkeeper/cardhash"
)

func TestDuplicateCards(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	const text = "Brakka is a grumpy dwarf blacksmith who runs the forge at the edge of town. " +
		"She hates elves, loves good ale, and never turns away a customer with honest coin."
	orig, _ := newCard(t, lib, "orig", `{"name": "Brakka", "description": "`+text+`"}`)
	dup, _ := newCard(t, lib, "copy", `{"name": "Brakka", "description": " `+text+`\r\n", "creator_notes": ""}`)
	edited, _ := newCard(t, lib, "edited", `{"name": "Brakka 2", "description": "`+text+` She has a cat."}`)
	newCard(t, lib, "elf", `{"name": "Lia", "description": "A cheerful elven bard wanders the coast singing songs of the sea."}`)

	got, err := lib.DuplicateCards(ctx, orig, NearDistance)
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

	got, err := lib.Cards.MatchHashes(ctx, DefaultLibrary, []Hashes{
		{ContentHash: r.ContentHash, TextSimhash: r.TextSimhash},
		{ContentHash: strings.Repeat("0", 64)},
	}, NearDistance)
	if err != nil {
		t.Fatal(err)
	}
	// orig matches by content and by text, and is listed once, by content.
	if len(got) != 2 || len(got[0]) != 2 || got[0][0] != (HashMatch{orig, DefaultLibrary, "content", 0}) ||
		got[0][1].ID != edited || got[0][1].Reason != "text" || len(got[1]) != 0 {
		t.Fatalf("matches = %+v", got)
	}
	if got, err := lib.Lorebooks.MatchHashes(ctx, DefaultLibrary, []Hashes{{ContentHash: r.ContentHash}}, NearDistance); err != nil || len(got[0]) != 0 {
		t.Fatalf("lorebook matches = %+v, %v", got, err)
	}
	if got, err := lib.Cards.MatchHashes(ctx, DefaultLibrary, nil, NearDistance); err != nil || len(got) != 0 {
		t.Fatalf("no hashes = %+v, %v", got, err)
	}
}

// newLibraryRow adds a library and returns its ID; there is no API for it yet.
func newLibraryRow(t *testing.T, lib *Library, name string) int64 {
	t.Helper()
	var id int64
	if err := lib.pool.QueryRow(context.Background(), `INSERT INTO library (name) VALUES ($1) RETURNING id`, name).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func moveCard(t *testing.T, lib *Library, card, library int64) {
	t.Helper()
	if _, err := lib.pool.Exec(context.Background(), `UPDATE card SET library_id = $1 WHERE id = $2`, library, card); err != nil {
		t.Fatal(err)
	}
}

// avatarCard creates a card with data whose avatar blob has the given phash.
func avatarCard(t *testing.T, lib *Library, i int, phash int64, data string) int64 {
	t.Helper()
	ctx := context.Background()
	sha := fmt.Sprintf("%064x", i+1)
	if _, err := lib.pool.Exec(ctx, `INSERT INTO blob (sha256, size, mime, store, avatar_phash) VALUES ($1, 1, 'image/png', 'library', $2)`, sha, phash); err != nil {
		t.Fatal(err)
	}
	c, _, err := lib.Cards.Create(ctx, DefaultLibrary, fmt.Sprintf("avatar-%d", i), "upload", Edit{Data: json.RawMessage(data), AvatarBlob: sha, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	return c.ID
}

// flip returns h with the given bits inverted.
func flip(h int64, bits ...int) int64 {
	for _, b := range bits {
		h ^= int64(uint64(1) << b)
	}
	return h
}

func onlyReason(ms []Match, reason string) []int64 {
	out := []int64{}
	for _, m := range ms {
		if m.Reason == reason {
			out = append(out, m.CardID)
		}
	}
	slices.Sort(out)
	return out
}

func TestSimilarAvatarsMatchExactScan(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	const base = int64(0x5a3c_9e01_77d2_4b68)
	// Bands are 7 bits wide from bit 0; bit 63 is in none.
	hashes := []int64{
		base,
		flip(base, 63),
		flip(base, 0, 7, 14, 21, 28, 35, 42, 49), // 8 bits, one per band but the last
		flip(base, 0, 7, 14, 21, 28, 35, 42, 49, 56), // 9 bits, one per band
		flip(base, 0, 7, 14, 21, 28, 35, 42, 63),     // 8 bits, two bands untouched
		flip(base, 1, 2, 3, 4, 5, 6, 8, 9),           // 8 bits in two bands
		flip(base, 1, 2, 3, 4, 5, 6, 8, 9, 10, 11),   // 10 bits
		flip(base, 60, 61, 62, 63),
		^base,
	}
	for k := 1; k <= 12; k++ {
		bits := make([]int, k)
		for j := range bits {
			bits[j] = (j*11 + k) % 64
		}
		hashes = append(hashes, flip(base, bits...))
	}
	ids := make([]int64, len(hashes))
	for i, h := range hashes {
		ids[i] = avatarCard(t, lib, i, h, fmt.Sprintf(`{"name": "Avatar %d"}`, i))
	}
	near := func(h int64, skip int) []int64 {
		out := []int64{}
		for j, o := range hashes {
			if j != skip && cardhash.Distance(h, o) <= NearDistance {
				out = append(out, ids[j])
			}
		}
		slices.Sort(out)
		return out
	}
	probes := make([]Hashes, len(hashes))
	for i, h := range hashes {
		got, err := lib.DuplicateCards(ctx, ids[i], NearDistance)
		if err != nil {
			t.Fatal(err)
		}
		if want := near(h, i); !slices.Equal(onlyReason(got, "avatar"), want) {
			t.Errorf("card %d: avatar matches %v, exact scan %v", i, onlyReason(got, "avatar"), want)
		}
		probes[i] = Hashes{ContentHash: strings.Repeat("0", 64), ImagePhash: &hashes[i]}
	}
	got, err := lib.Cards.MatchHashes(ctx, DefaultLibrary, probes, NearDistance)
	if err != nil {
		t.Fatal(err)
	}
	for i, ms := range got {
		ids := []int64{}
		for _, m := range ms {
			ids = append(ids, m.ID)
		}
		slices.Sort(ids)
		if want := near(hashes[i], -1); !slices.Equal(ids, want) {
			t.Errorf("probe %d: matches %v, exact scan %v", i, ids, want)
		}
	}
}

func TestSimilarTextMatchesExactScan(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	words := strings.Fields("Brakka is a grumpy dwarf blacksmith who runs the forge at the edge of town. She hates elves, " +
		"loves good ale, and never turns away a customer with honest coin. Her hammer was her father's, and his father's before " +
		"him, and she swears it hums when trouble is near. Travelers who cross her learn quickly that she forgives nothing.")
	spare := strings.Fields("lantern river crow bramble copper winter thistle harbor ember velvet orchard granite")
	rng := rand.New(rand.NewPCG(1, 2))
	ids := make([]int64, 40)
	sims := make([]int64, 40)
	for i := range ids {
		w := slices.Clone(words)
		for range i % 8 {
			w[rng.IntN(len(w))] = spare[rng.IntN(len(spare))]
		}
		var rev int64
		ids[i], rev = newCard(t, lib, fmt.Sprintf("text-%d", i), fmt.Sprintf(`{"name": "Text %d", "description": %q}`, i, strings.Join(w, " ")))
		r, err := lib.Cards.Revision(ctx, rev)
		if err != nil || r.TextSimhash == nil {
			t.Fatalf("revision %d: %+v, %v", i, r, err)
		}
		sims[i] = *r.TextSimhash
	}
	pairs := 0
	for i := range ids {
		got, err := lib.DuplicateCards(ctx, ids[i], NearDistance)
		if err != nil {
			t.Fatal(err)
		}
		want := []int64{}
		for j := range ids {
			if j != i && cardhash.Distance(sims[i], sims[j]) <= NearDistance {
				want = append(want, ids[j])
			}
		}
		slices.Sort(want)
		pairs += len(want)
		if !slices.Equal(onlyReason(got, "text"), want) {
			t.Errorf("card %d: text matches %v, exact scan %v", i, onlyReason(got, "text"), want)
		}
	}
	if pairs == 0 || pairs == len(ids)*(len(ids)-1) {
		t.Fatalf("%d near pairs: the edits don't exercise the threshold", pairs)
	}
}

func TestSimilarStaysInLibrary(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	const data = `{"name": "Brakka", "description": "Brakka is a grumpy dwarf blacksmith who runs the forge at the edge of town."}`
	orig, rev := newCard(t, lib, "orig", data)
	moved, _ := newCard(t, lib, "moved", data)
	archive := newLibraryRow(t, lib, "Archive")
	moveCard(t, lib, moved, archive)
	r, err := lib.Cards.Revision(ctx, rev)
	if err != nil {
		t.Fatal(err)
	}

	if got, err := lib.DuplicateCards(ctx, orig, NearDistance); err != nil || len(got) != 0 {
		t.Fatalf("library 1 similar = %+v, %v", got, err)
	}
	if got, err := lib.DuplicateCards(ctx, moved, NearDistance); err != nil || len(got) != 0 {
		t.Fatalf("archive similar = %+v, %v", got, err)
	}
	probe := []Hashes{{ContentHash: r.ContentHash, TextSimhash: r.TextSimhash}}
	got, err := lib.Cards.MatchHashes(ctx, DefaultLibrary, probe, NearDistance)
	if err != nil || len(got[0]) != 1 || got[0][0].ID != orig {
		t.Fatalf("library 1 matches = %+v, %v", got, err)
	}
	got, err = lib.Cards.MatchHashes(ctx, archive, probe, NearDistance)
	if err != nil || len(got[0]) != 1 || got[0][0].ID != moved {
		t.Fatalf("archive matches = %+v, %v", got, err)
	}
}

func TestSimilarRefusesWideDistance(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	c, _ := newCard(t, lib, "brakka", `{"name": "Brakka"}`)
	if _, err := lib.DuplicateCards(ctx, c, NearDistance+1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("DuplicateCards: %v", err)
	}
	if _, err := lib.Cards.MatchHashes(ctx, DefaultLibrary, []Hashes{{ContentHash: "x"}}, NearDistance+1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("MatchHashes: %v", err)
	}
}
