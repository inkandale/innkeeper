package targets

import (
	"bytes"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"slices"
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
	out, err := e.tryReconcile(p, remoteIDs...)
	if err != nil {
		e.t.Fatal(err)
	}
	return out
}

func (e *env) tryReconcile(p ReconcileParams, remoteIDs ...string) (map[string]ReconcileItem, error) {
	e.t.Helper()
	p, err := p.withDefaults()
	if err != nil {
		return nil, err
	}
	inst, info, err := e.s.describeReconcile(e.ctx, cardKind, e.inst)
	if err != nil {
		return nil, err
	}
	idx, err := e.s.loadIndex(e.ctx, cardKind, inst, info)
	if err != nil {
		return nil, err
	}
	out := map[string]ReconcileItem{}
	for _, id := range remoteIDs {
		if out[id], err = e.s.reconcileItem(e.ctx, cardKind, inst, info, idx, p, id); err != nil {
			return nil, err
		}
	}
	return out, nil
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
	if it := check("bad", VerdictError); it.Error == "" || !slices.Equal(it.Options, []Option{{Action: "skip"}}) {
		t.Errorf("error item = %+v", it)
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

// patternPNG writes a 64×64 grayscale image whose pixels come from f.
func patternPNG(t *testing.T, f func(x, y int) uint8) string {
	t.Helper()
	img := image.NewGray(image.Rect(0, 0, 64, 64))
	for y := range 64 {
		for x := range 64 {
			img.SetGray(x, y, color.Gray{Y: f(x, y)})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "avatar.png")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func (e *env) setAvatar(card int64, path string) *int64 {
	e.t.Helper()
	f, err := os.Open(path)
	if err != nil {
		e.t.Fatal(err)
	}
	defer f.Close()
	b, err := e.lib.PutBlob(e.ctx, f)
	if err != nil {
		e.t.Fatal(err)
	}
	e.update(card, func(ed *library.Edit) { ed.AvatarBlob = b.SHA256 })
	return b.AvatarPhash
}

func TestReconcileAvatarSignal(t *testing.T) {
	e := setup(t, nil)
	stripes := patternPNG(t, func(x, _ int) uint8 { return uint8(x / 8 % 2 * 255) })
	rings := patternPNG(t, func(x, y int) uint8 { return uint8((x-32)*(x-32)+(y-32)*(y-32)) / 128 * 255 })
	pa := e.setAvatar(e.card, stripes)
	pb, err := cardhash.ImagePhash(rings)
	if err != nil || pa == nil || cardhash.Distance(*pa, pb) <= 8 {
		t.Fatalf("pick images further apart: %v, %v", pa, err)
	}
	// Only the avatar matches: a new name and new text. The fake sends the
	// avatar only when target.get asks for it.
	e.appCard("same", map[string]any{"name": "Grumbleforge", "description": "A pirate queen who never smiles."},
		map[string]any{"avatar": stripes})
	e.appCard("far", map[string]any{"name": "Grumbleforge", "description": "A pirate queen who never smiles."},
		map[string]any{"avatar": rings})

	got := e.reconcile(ReconcileParams{}, "same", "far")
	it := got["same"]
	if it.Verdict != VerdictLikely || len(it.Candidates) != 1 || it.Candidates[0].CardID != e.card {
		t.Fatalf("same = %+v", it)
	}
	if r := it.Candidates[0].Reasons; len(r) != 1 || r["avatar"] != 0 {
		t.Errorf("reasons = %v", r)
	}
	if it := got["far"]; it.Verdict != VerdictNew {
		t.Errorf("far = %+v", it)
	}
}

// miraEdit returns miraText with one word replaced, and that text's simhash
// distance from the app item below.
func miraEdit(from, to string) (string, int) {
	text := strings.Replace(miraText, from, to, 1)
	h := func(s string) int64 {
		x, _, _ := cardhash.CardSimhash([]byte(`{"description": ` + mustJSON(s) + `, "first_mes": "Who goes there?"}`))
		return x
	}
	return text, cardhash.Distance(h(miraText), h(text))
}

func TestReconcileRanking(t *testing.T) {
	e := setup(t, nil)
	near, dNear := miraEdit("leather", "red")
	far, dFar := miraEdit("black", "grey")
	if dNear == 0 || dNear >= dFar || dFar > 8 {
		t.Fatalf("distances %d, %d; pick other edits", dNear, dFar)
	}
	// Created in the order that ranking must undo.
	nameOnly := e.libCard(map[string]any{"name": "Ranko", "description": "A goatherd on a windy hill."})
	nameText := e.libCard(map[string]any{"name": "Ranko", "description": far, "first_mes": "Who goes there?"})
	textFar := e.libCard(map[string]any{"name": "Fen", "description": far, "first_mes": "Who goes there?"})
	textNear := e.libCard(map[string]any{"name": "Moss", "description": near, "first_mes": "Who goes there?"})
	twinA := e.libCard(map[string]any{"name": "Twin", "description": "A juggler with seven knives."})
	twinB := e.libCard(map[string]any{"name": "Twin", "description": "A potter who sings to clay."})
	e.appCard("counts", map[string]any{"name": "Ranko", "description": miraText, "first_mes": "Who goes there?"}, nil)
	e.appCard("dist", map[string]any{"name": "Keeper", "description": miraText, "first_mes": "Who goes there?"}, nil)
	e.appCard("tie", map[string]any{"name": "twin", "description": "A dancer made of smoke."}, nil)

	got := e.reconcile(ReconcileParams{}, "counts", "dist", "tie")
	want := map[string][]int64{
		// Two signals beat one; then the smaller total distance.
		"counts": {nameText, nameOnly, textNear, textFar},
		// Equal counts: the nearer text first; equal distances by ID.
		"dist": {textNear, nameText, textFar},
		"tie":  {twinA, twinB},
	}
	for id, w := range want {
		if c := ids(got[id].Candidates); !slices.Equal(c, w) {
			t.Errorf("%s candidates = %v, want %v", id, c, w)
		}
	}
}

func TestReconcileEmptyNames(t *testing.T) {
	e := setup(t, nil)
	c, _, err := e.lib.Cards.CreateFromName(e.ctx, "Nameless", "upload",
		library.Edit{Data: json.RawMessage(`{"name": "", "description": "A shadow at the edge of the map."}`), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	e.appCard("blank", map[string]any{"name": "   ", "description": "A brass band that marches in the rain."}, nil)
	e.appCard("empty", map[string]any{"name": "", "description": "A brass band that marches in the rain."}, nil)
	for id, it := range e.reconcile(ReconcileParams{}, "blank", "empty") {
		if it.Verdict != VerdictNew {
			t.Errorf("%s = %+v; card %d has an empty name too", id, it, c.ID)
		}
	}
}

func (e *env) blobCount() int {
	e.t.Helper()
	var n int
	if err := e.pool.QueryRow(e.ctx, `SELECT count(*) FROM blob`).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func TestReconcileBoundSkipsAvatar(t *testing.T) {
	e := setup(t, nil)
	bound := e.push(e.card)
	app := e.remote(bound.RemoteID)
	app["avatar"] = patternPNG(t, func(x, y int) uint8 { return uint8(x * y) })
	e.writeRemote(bound.RemoteID, app)
	before := e.blobCount()
	if it := e.reconcile(ReconcileParams{}, bound.RemoteID)[bound.RemoteID]; it.Verdict != VerdictBound {
		t.Fatalf("item = %+v", it)
	}
	if n := e.blobCount(); n != before {
		t.Errorf("blobs %d -> %d: a bound item's avatar was fetched", before, n)
	}
}

func TestReconcileAvatarIngestFailure(t *testing.T) {
	e := setup(t, nil)
	stripes := patternPNG(t, func(x, _ int) uint8 { return uint8(x / 8 % 2 * 255) })
	e.setAvatar(e.card, stripes)
	e.appCard("pic", map[string]any{"name": "Brakka", "description": "A pirate queen who never smiles."},
		map[string]any{"avatar": stripes})
	// A file where the blob store stages uploads makes every ingest fail.
	shaPath, err := e.lib.BlobPath(strings.Repeat("0", 64))
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Dir(filepath.Dir(filepath.Dir(shaPath)))
	if err := os.RemoveAll(filepath.Join(root, "staging")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "staging"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	it := e.reconcile(ReconcileParams{}, "pic")["pic"]
	if it.Verdict != VerdictLikely || len(it.Candidates) != 1 {
		t.Fatalf("item = %+v", it)
	}
	if r := it.Candidates[0].Reasons; len(r) != 1 || r["name"] != 0 {
		t.Errorf("reasons = %v, want name only", r)
	}
}

func TestReconcileLibraryErrorFailsRun(t *testing.T) {
	e := setup(t, nil)
	e.appCard("src", map[string]any{"name": "Vyr", "description": "A dragon.",
		"extensions": map[string]any{"chub": map[string]any{"full_path": "someone/vyr"}}}, nil)
	if _, err := e.pool.Exec(e.ctx, `ALTER TABLE card_source RENAME TO card_source_gone`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.tryReconcile(ReconcileParams{}, "src"); err == nil {
		t.Fatal("a failed source lookup didn't fail the run")
	}
}

// coreCard is a library card's data with all four core fields.
var coreCard = map[string]any{"name": "Tamsin", "description": "A hedge witch in the fens.", "personality": "Wry.",
	"scenario": "A rainy market.", "first_mes": "Buying or browsing?", "mes_example": "<START>"}

// edited returns coreCard with fields changed.
func edited(fields ...string) map[string]any {
	out := map[string]any{}
	for k, v := range coreCard {
		out[k] = v
	}
	for _, f := range fields {
		out[f] = "Something else entirely, " + f + "."
	}
	return out
}

func hasOption(it ReconcileItem, o Option) bool {
	for _, got := range it.Options {
		if got.Action == o.Action && got.CardID == o.CardID {
			return true
		}
	}
	return false
}

func TestReconcileSuggestions(t *testing.T) {
	e := setup(t, map[string]any{"fields": []string{"name", "description", "personality", "scenario", "first_mes",
		"mes_example"}})
	tamsin := e.libCard(coreCard)
	twin := e.libCard(coreCard)
	other := e.libCard(map[string]any{"name": "Tamsin", "description": "A sailor."})
	bound := e.push(e.card)
	taken := e.push(tamsin)
	e.appCard("one", edited("description"), nil)
	e.appCard("three", edited("description", "personality", "scenario"), nil)
	// mes_example isn't a core field, so this is 2 of 4.
	e.appCard("two", edited("description", "personality", "mes_example"), nil)
	e.appCard("exact", coreCard, nil)
	e.appCard("new", map[string]any{"name": "Pell", "description": "A clockwork owl that reads the stars."}, nil)
	// A second app copy of e.card, which is already bound.
	e.writeRemote("dupe", e.remote(bound.RemoteID))
	quill := e.libCard(map[string]any{"name": "Quill", "description": "A scribe."})
	quillBinding := e.push(quill)
	e.appCard("quill", map[string]any{"name": "Quill", "description": "A sword swallower."}, nil)
	ids := []string{bound.RemoteID, "one", "three", "two", "exact", "new", "dupe", "quill"}

	got := e.reconcile(ReconcileParams{}, ids...)
	for _, id := range ids {
		if !hasOption(got[id], Option{Action: "skip"}) {
			t.Errorf("%s options = %+v", id, got[id].Options)
		}
	}
	suggested := func(id, action string, card int64) ReconcileItem {
		t.Helper()
		it := got[id]
		if it.Suggested == nil || it.Suggested.Action != action || it.Suggested.CardID != card || it.Suggested.Why == "" {
			t.Fatalf("%s suggested = %+v", id, it.Suggested)
		}
		return it
	}
	// tamsin ties with twin and ranks first, but it's bound to another app item.
	if it := suggested("one", "bind", twin); it.Candidates[0].CardID != tamsin || it.Candidates[0].DifferingFields != 1 {
		t.Errorf("one candidates = %+v", it.Candidates)
	}
	it := suggested("three", "import", 0)
	if it.Candidates[0].CardID != tamsin || it.Candidates[0].DifferingFields != 3 {
		t.Errorf("three candidates = %+v", it.Candidates)
	}
	for _, c := range []int64{tamsin, other} {
		if !hasOption(it, Option{Action: "bind", CardID: c}) || !hasOption(it, Option{Action: "import"}) {
			t.Errorf("three options = %+v", it.Options)
		}
	}
	suggested("two", "bind", twin)
	// tamsin is bound to its own app copy, so its exact twin is suggested.
	if it := suggested("exact", "bind", twin); it.Candidates[0].BoundTo != taken.RemoteID {
		t.Errorf("exact candidates = %+v", it.Candidates)
	}
	suggested("new", "import", 0)
	if it := suggested("dupe", "skip", 0); !strings.Contains(it.Suggested.Why, bound.RemoteID) {
		t.Errorf("dupe why = %q", it.Suggested.Why)
	}
	if it := suggested("quill", "import", 0); !hasOption(it, Option{Action: "bind", CardID: quill}) ||
		it.Candidates[0].BoundTo != quillBinding.RemoteID {
		t.Errorf("quill = %+v", it)
	}
	if got[bound.RemoteID].Suggested != nil {
		t.Errorf("in-sync bound item suggests %+v", got[bound.RemoteID].Suggested)
	}

	four := 4
	got = e.reconcile(ReconcileParams{SplitFields: &four}, "three")
	suggested("three", "bind", twin)

	// The app's edit to a bound card is drift to resolve.
	e.editRemote(bound.RemoteID, "description", "A dwarf smith who sings.")
	got = e.reconcile(ReconcileParams{}, bound.RemoteID)
	if it := suggested(bound.RemoteID, "bind", e.card); it.Candidates[0].DifferingFields != 1 || it.Candidates[0].Conflicts != 0 {
		t.Errorf("drifted candidates = %+v", it.Candidates)
	}

	// The library changed the same sentence too: a conflict.
	e.update(e.card, func(ed *library.Edit) { ed.Data = setField(ed.Data, "description", "A dwarf smith who drinks.") })
	got = e.reconcile(ReconcileParams{}, bound.RemoteID)
	if it := suggested(bound.RemoteID, "bind", e.card); it.Candidates[0].Conflicts != 1 {
		t.Errorf("conflicting candidates = %+v", it.Candidates)
	}
}
