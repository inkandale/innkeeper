package targets

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/davidgibbons/innkeeper/internal/cardhash"
	"github.com/davidgibbons/innkeeper/internal/library"
)

const (
	liaText = "Lia is a cheerful elven bard who wanders the northern coast, singing old songs of the sea " +
		"to fishermen and trading stories for bread, ale, and a dry place to sleep."
	miraText = "Mira keeps the lighthouse on the black cliffs. She counts the ships that pass each night, " +
		"writes their names in a leather ledger, and has not spoken to another person in nine years."
)

// libCard adds a library card with data.
func (e *env) libCard(data map[string]any) int64 {
	e.t.Helper()
	raw, _ := json.Marshal(data)
	c, _, err := e.lib.Cards.CreateFromName(e.ctx, data["name"].(string), "upload", library.Edit{Data: raw, Source: "ui"})
	if err != nil {
		e.t.Fatal(err)
	}
	return c.ID
}

// reconcile reconciles the app's cards in remoteIDs with params p.
func (e *env) reconcile(p ReconcileParams, remoteIDs ...string) map[string]ReconcileItem {
	e.t.Helper()
	p, err := p.withDefaults()
	if err != nil {
		e.t.Fatal(err)
	}
	inst, info, err := e.s.describe(e.ctx, e.inst)
	if err != nil {
		e.t.Fatal(err)
	}
	info = reconcileInfo(info)
	idx, err := e.s.loadIndex(e.ctx, cardKind, inst, info)
	if err != nil {
		e.t.Fatal(err)
	}
	out := map[string]ReconcileItem{}
	for _, id := range remoteIDs {
		out[id] = e.s.reconcileItem(e.ctx, cardKind, inst, info, idx, p, id)
	}
	return out
}

func ids(cs []Candidate) []int64 {
	var out []int64
	for _, c := range cs {
		out = append(out, c.CardID)
	}
	return out
}

func TestReconcileVerdicts(t *testing.T) {
	e := setup(t, nil)
	bound := e.push(e.card)
	lia := e.libCard(map[string]any{"name": "Lia", "description": liaText, "first_mes": "Sit, friend."})
	mira := e.libCard(map[string]any{"name": "Mira", "description": miraText, "first_mes": "Who goes there?"})
	oskar := e.libCard(map[string]any{"name": "Oskar", "description": "A retired sea captain with a wooden leg.",
		"first_mes": "Ahoy."})
	dragon := e.libCard(map[string]any{"name": "Vyr", "description": "An ancient red dragon asleep on gold.",
		"first_mes": "Who wakes me?"})
	if _, err := e.lib.Cards.AddSource(e.ctx, dragon, library.Source{Kind: "chub", Ref: "someone/vyr"}); err != nil {
		t.Fatal(err)
	}

	// The app's copy embeds lorebooks; a character_book still matches exactly.
	e.appCard("exact", map[string]any{"name": "Lia", "description": liaText, "first_mes": "Sit, friend.",
		"character_book": map[string]any{"entries": []any{}}}, nil)
	e.appCard("name", map[string]any{"name": " oskar ", "description": "A young baker who sells warm bread at dawn.",
		"first_mes": "Ahoy."}, nil)
	renamed := strings.Replace(miraText, "leather", "red", 1)
	e.appCard("text", map[string]any{"name": "The Keeper", "description": renamed, "first_mes": "Who goes there?"}, nil)
	e.appCard("source", map[string]any{"name": "Lia", "description": "A tavern cat that steals fish from the kitchen.",
		"extensions": map[string]any{"chub": map[string]any{"full_path": "someone/vyr"}}}, nil)
	e.appCard("new", map[string]any{"name": "Pell", "description": "A clockwork owl that reads the stars."}, nil)
	e.appCard("dup", map[string]any{"name": "Brakka", "description": "A dwarf smith.", "first_mes": "Hail."}, nil)
	if err := os.WriteFile(filepath.Join(e.dir, "bad.json"), []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	// The text-only case needs a small but nonzero distance.
	a, _, _ := cardhash.CardSimhash([]byte(`{"description": ` + mustJSON(miraText) + `, "first_mes": "Who goes there?"}`))
	b, _, _ := cardhash.CardSimhash([]byte(`{"description": ` + mustJSON(renamed) + `, "first_mes": "Who goes there?"}`))
	if d := cardhash.Distance(a, b); d < 1 || d > 8 {
		t.Fatalf("text distance %d; pick another edit", d)
	}

	got := e.reconcile(ReconcileParams{}, bound.RemoteID, "exact", "name", "text", "source", "new", "dup", "bad")
	check := func(id, verdict string, cands ...int64) ReconcileItem {
		t.Helper()
		it := got[id]
		if it.Verdict != verdict || len(it.Candidates) != len(cands) {
			t.Fatalf("%s = %+v, want %s %v", id, it, verdict, cands)
		}
		for i, c := range cands {
			if it.Candidates[i].CardID != c {
				t.Fatalf("%s candidates = %v, want %v", id, ids(it.Candidates), cands)
			}
		}
		return it
	}
	if it := check(bound.RemoteID, VerdictBound, e.card); it.BindingID != bound.ID || it.Candidates[0].BoundTo != "" {
		t.Errorf("bound = %+v", it)
	}
	if it := check("exact", VerdictExact, lia); it.Candidates[0].Reasons["content"] != 0 || it.AppHash == "" {
		t.Errorf("exact = %+v", it)
	}
	if _, ok := check("name", VerdictLikely, oskar).Candidates[0].Reasons["name"]; !ok {
		t.Error("name match has no name reason")
	}
	if d, ok := check("text", VerdictLikely, mira).Candidates[0].Reasons["text"]; !ok || d == 0 {
		t.Errorf("text reason = %d, %v", d, ok)
	}
	// Source ranks above a name match with a lower card ID.
	if _, ok := check("source", VerdictLikely, dragon, lia).Candidates[0].Reasons["source"]; !ok {
		t.Error("source match has no source reason")
	}
	check("new", VerdictNew)
	if it := check("dup", VerdictExact, e.card); it.Candidates[0].BoundTo != bound.RemoteID {
		t.Errorf("dup candidate bound_to = %q", it.Candidates[0].BoundTo)
	}
	if it := check("bad", VerdictError); it.Error == "" {
		t.Error("error item has no message")
	}

	off, zero := false, 0
	got = e.reconcile(ReconcileParams{Match: ReconcileMatch{Name: &off, TextDistance: &zero}}, "name", "text")
	check("name", VerdictNew)
	check("text", VerdictNew)
}

func TestReconcileParams(t *testing.T) {
	p, err := ReconcileParams{}.withDefaults()
	if err != nil || !*p.Match.Name || *p.Match.TextDistance != 8 || *p.Match.AvatarDistance != 8 || *p.SplitFields != 3 {
		t.Fatalf("defaults = %+v, %v", p, err)
	}
	big, neg, five := 33, -1, 5
	for _, bad := range []ReconcileParams{
		{Match: ReconcileMatch{TextDistance: &big}},
		{Match: ReconcileMatch{AvatarDistance: &neg}},
		{SplitFields: &five},
	} {
		if _, err := bad.withDefaults(); !errors.Is(err, library.ErrInvalid) {
			t.Errorf("%+v: err = %v", bad, err)
		}
	}
}

func mustJSON(s string) string {
	raw, _ := json.Marshal(s)
	return string(raw)
}
