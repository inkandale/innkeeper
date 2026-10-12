package targets

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/davidgibbons/innkeeper/cardhash"
	"github.com/davidgibbons/innkeeper/internal/library"
	"github.com/davidgibbons/innkeeper/internal/plugins"
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
	p, err := withDefaults(p)
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
		if out[id], _, err = e.s.reconcileItem(e.ctx, cardKind, inst, info, idx, p, id); err != nil {
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
	p, err := withDefaults(ReconcileParams{})
	if err != nil || !*p.Match.Name || *p.Match.TextDistance != 8 || *p.Match.AvatarDistance != 8 || *p.SplitFields != 3 ||
		p.Apply != ApplyObvious {
		t.Fatalf("defaults = %+v, %v", p, err)
	}
	big, neg, five := 33, -1, 5
	for _, bad := range []ReconcileParams{
		{Match: ReconcileMatch{TextDistance: &big}},
		{Match: ReconcileMatch{AvatarDistance: &neg}},
		{SplitFields: &five},
		{Apply: "all"},
	} {
		if _, err := withDefaults(bad); !errors.Is(err, library.ErrInvalid) {
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

// breakBlobStore puts a file where the blob store stages uploads, so every
// ingest fails.
func (e *env) breakBlobStore() {
	e.t.Helper()
	shaPath, err := e.lib.BlobPath(strings.Repeat("0", 64))
	if err != nil {
		e.t.Fatal(err)
	}
	root := filepath.Dir(filepath.Dir(filepath.Dir(shaPath)))
	if err := os.RemoveAll(filepath.Join(root, "staging")); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "staging"), nil, 0o644); err != nil {
		e.t.Fatal(err)
	}
}

func TestReconcileAvatarIngestFailure(t *testing.T) {
	e := setup(t, nil)
	stripes := patternPNG(t, func(x, _ int) uint8 { return uint8(x / 8 % 2 * 255) })
	e.setAvatar(e.card, stripes)
	e.appCard("pic", map[string]any{"name": "Brakka", "description": "A pirate queen who never smiles."},
		map[string]any{"avatar": stripes})
	e.breakBlobStore()
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
	e.appCard("greeting", edited("description", "personality", "first_mes"), nil)
	// mes_example isn't a core field, so this is 2 of 4.
	e.appCard("two", edited("description", "personality", "mes_example"), nil)
	e.appCard("exact", coreCard, nil)
	e.appCard("new", map[string]any{"name": "Pell", "description": "A clockwork owl that reads the stars."}, nil)
	// A second app copy of e.card, which is already bound.
	e.writeRemote("dupe", e.remote(bound.RemoteID))
	quill := e.libCard(map[string]any{"name": "Quill", "description": "A scribe."})
	quillBinding := e.push(quill)
	e.appCard("quill", map[string]any{"name": "Quill", "description": "A sword swallower."}, nil)
	ids := []string{bound.RemoteID, "one", "three", "greeting", "two", "exact", "new", "dupe", "quill"}

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
	suggested("greeting", "import", 0)
	// bind to each candidate in rank order, then import, then skip.
	want := []Option{{Action: "bind", CardID: tamsin}, {Action: "bind", CardID: twin}, {Action: "bind", CardID: other},
		{Action: "import"}, {Action: "skip"}}
	if !slices.Equal(got["three"].Options, want) {
		t.Errorf("three options = %+v", got["three"].Options)
	}
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

func TestReconcileInstance(t *testing.T) {
	e := setup(t, map[string]any{"lorebooks": "standalone"})
	gone := e.push(e.card)
	kept := e.push(e.newCard("Oskar"))
	if err := os.Remove(filepath.Join(e.dir, gone.RemoteID+".json")); err != nil {
		t.Fatal(err)
	}
	iko := e.newCard("Iko")
	forge := e.newLorebook("Forge lore")
	sea, _, err := e.lib.Lorebooks.CreateFromName(e.ctx, "Sea lore", "upload", library.Edit{Source: "ui",
		Data: json.RawMessage(`{"name": "Sea lore", "entries": [{"keys": ["tide"], "content": "Tides rise twice a day."}]}`)})
	if err != nil {
		t.Fatal(err)
	}

	var forgeData map[string]any
	_ = json.Unmarshal(e.lorebookHead(forge).Data, &forgeData)
	e.writeRemote("l1", map[string]any{"lorebook": forgeData})
	e.writeRemote("l2", map[string]any{"lorebook": map[string]any{"name": "Star charts",
		"entries": []any{map[string]any{"keys": []any{"star"}, "content": "The north star never moves."}}}})
	e.writeRemote("l3", map[string]any{"lorebook": map[string]any{"name": "Cave maps",
		"entries": []any{map[string]any{"keys": []any{"cave"}, "content": "Bats nest in the deep tunnels."}}}})
	e.writeRemote("l4", map[string]any{"lorebook": map[string]any{"name": "sea lore",
		"entries": []any{map[string]any{"keys": []any{"tide"}, "content": "A kraken sleeps below the reef."}}}})
	e.appCard("r10", map[string]any{"name": "Pell", "description": "A clockwork owl that reads the stars."},
		map[string]any{"lorebook_remote_ids": []string{"l1", "l2"}})
	e.appCard("r11", map[string]any{"name": "Wren", "description": "A navigator who never looks down."},
		map[string]any{"lorebook_remote_ids": []string{"l2"}})

	var progress [][2]int
	snap, err := e.s.Reconcile(e.ctx, ReconcileParams{InstanceID: e.inst, Apply: ApplyNone}, func(done, total int) {
		progress = append(progress, [2]int{done, total})
	})
	if err != nil {
		t.Fatal(err)
	}
	items := map[string]ReconcileItem{}
	for _, it := range snap.Items {
		items[it.RemoteID] = it
	}
	verdicts := func(its []ReconcileItem) map[string]string {
		out := map[string]string{}
		for _, it := range its {
			out[it.RemoteID] = it.Verdict
		}
		return out
	}
	if items[kept.RemoteID].Verdict != VerdictBound {
		t.Errorf("kept = %+v", items[kept.RemoteID])
	}
	if got := verdicts(items["r10"].Lorebooks); len(got) != 2 || got["l1"] != VerdictExact || got["l2"] != VerdictNew {
		t.Errorf("r10 lorebooks = %v", got)
	}
	if got := verdicts(items["r11"].Lorebooks); len(got) != 1 || got["l2"] != VerdictNew {
		t.Errorf("r11 lorebooks = %v", got)
	}
	if got := verdicts(snap.Lorebooks); len(got) != 2 || got["l3"] != VerdictNew || got["l4"] != VerdictLikely {
		t.Errorf("top-level lorebooks = %v", got)
	}
	for _, lb := range snap.Lorebooks {
		if lb.RemoteID == "l4" && (lb.Suggested == nil || lb.Suggested.Action != "bind" || lb.Suggested.CardID != sea.ID) {
			t.Errorf("l4 suggested = %+v", lb.Suggested)
		}
	}
	log, _ := os.ReadFile(filepath.Join(e.dir, "get_lorebook.log"))
	if n := strings.Count(string(log), "l2\n"); n != 1 {
		t.Errorf("l2 read %d times", n)
	}
	if !slices.Equal(snap.Missing, []int64{e.card}) {
		t.Errorf("missing = %v", snap.Missing)
	}
	if !slices.Equal(snap.Unpushed, []int64{iko}) {
		t.Errorf("unpushed = %v", snap.Unpushed)
	}
	// Totals count each character and each world book once.
	sum := 0
	for _, n := range snap.Totals {
		sum += n
	}
	if sum != 7 || snap.Totals[VerdictBound] != 1 || snap.Totals[VerdictNew] != 4 || snap.Totals[VerdictExact] != 1 || snap.Totals[VerdictLikely] != 1 {
		t.Errorf("totals = %v", snap.Totals)
	}
	for i, p := range progress {
		if p != [2]int{i + 1, 7} || len(progress) != 7 {
			t.Fatalf("progress = %v", progress)
		}
	}
}

func TestReconcileBoundArchived(t *testing.T) {
	e := setup(t, nil)
	bound := e.push(e.card)
	if err := e.lib.Cards.SetArchived(e.ctx, e.card, true); err != nil {
		t.Fatal(err)
	}
	it := e.reconcile(ReconcileParams{}, bound.RemoteID)[bound.RemoteID]
	if it.Verdict != VerdictBound || it.BindingID != bound.ID || len(it.Candidates) != 0 || it.Suggested != nil ||
		!slices.Equal(it.Options, []Option{{Action: "import"}, {Action: "skip"}}) {
		t.Errorf("item = %+v", it)
	}
}

func TestReconcileReusedRemoteID(t *testing.T) {
	e := setup(t, nil)
	// The dead binding is older, so it sorts first by ID; the live one must still win.
	deadCard := e.newCard("Iko")
	dead := e.push(deadCard)
	live := e.push(e.card)
	// The app lost the dead card's item and gave its ID to the live one.
	if _, err := e.pool.Exec(e.ctx, `UPDATE binding SET remote_state = 'missing', remote_id = $1 WHERE id = $2`,
		live.RemoteID, dead.ID); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(e.dir, dead.RemoteID+".json")); err != nil {
		t.Fatal(err)
	}

	snap, err := e.s.Reconcile(e.ctx, ReconcileParams{InstanceID: e.inst}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Items) != 1 || snap.Items[0].BindingID != live.ID || snap.Items[0].Candidates[0].CardID != e.card {
		t.Errorf("items = %+v", snap.Items)
	}
	if !slices.Equal(snap.Missing, []int64{deadCard}) {
		t.Errorf("missing = %v", snap.Missing)
	}
	c, err := e.s.CompareItem(e.ctx, e.inst, live.RemoteID, 0)
	if err != nil || c.CardID != e.card || c.Base != "accepted" {
		t.Errorf("compare = %+v, %v", c, err)
	}
}

// An item whose only binding is missing has no binding to send back, so an
// import of it names none.
func TestReconcileOnlyAMissingBinding(t *testing.T) {
	e := setup(t, nil)
	b := e.push(e.card)
	if _, err := e.pool.Exec(e.ctx, `UPDATE binding SET remote_state = 'missing' WHERE id = $1`, b.ID); err != nil {
		t.Fatal(err)
	}
	it := e.reconcile(ReconcileParams{}, b.RemoteID)[b.RemoteID]
	if it.Verdict != VerdictBound || it.BindingID != 0 {
		t.Fatalf("item = %+v", it)
	}
	if c := e.compareItem(b.RemoteID, 0); c.BindingID != 0 {
		t.Errorf("comparison binding = %d", c.BindingID)
	}
	res, err := e.s.Resolve(e.ctx, Resolution{InstanceID: e.inst, RemoteID: b.RemoteID, AppHash: it.AppHash, Action: "import"})
	if err != nil || res.CardID == e.card {
		t.Fatalf("import: %+v, %v", res, err)
	}
}

func TestReconcileCapsCandidates(t *testing.T) {
	e := setup(t, nil)
	var want []int64
	for i := range 11 {
		want = append(want, e.libCard(map[string]any{"name": "Guard", "description": fmt.Sprintf("Guard number %d.", i)}))
	}
	e.appCard("guard", map[string]any{"name": "Guard", "description": "A tired guard at the gate."}, nil)
	it := e.reconcile(ReconcileParams{}, "guard")["guard"]
	if !slices.Equal(ids(it.Candidates), want[:10]) {
		t.Errorf("candidates = %v", ids(it.Candidates))
	}
}

// The cap keeps an unbound match ranked past it, so the suggestion can bind
// it rather than call the item a duplicate.
func TestReconcileCapKeepsUnboundCandidate(t *testing.T) {
	e := setup(t, nil)
	guard := map[string]any{"name": "Guard", "description": "A tired guard at the gate."}
	var cards []int64
	for range 11 {
		cards = append(cards, e.libCard(guard))
	}
	for _, c := range cards[:10] {
		e.push(c)
	}
	e.appCard("guard", guard, nil)
	it := e.reconcile(ReconcileParams{}, "guard")["guard"]
	if it.Verdict != VerdictExact || !slices.Equal(ids(it.Candidates), append(cards[:9:9], cards[10])) {
		t.Fatalf("verdict %s, candidates = %v", it.Verdict, ids(it.Candidates))
	}
	if it.Suggested == nil || it.Suggested.Action != "bind" || it.Suggested.CardID != cards[10] {
		t.Errorf("suggested = %+v", it.Suggested)
	}
}

// An embedded target's world books live inside its cards, so a card's links
// aren't reconciled as standalone lorebooks.
func TestReconcileEmbeddedIgnoresLinks(t *testing.T) {
	e := setup(t, nil)
	e.writeRemote("l1", map[string]any{"lorebook": map[string]any{"name": "Sea lore",
		"entries": []any{map[string]any{"keys": []any{"tide"}, "content": "Tides rise twice a day."}}}})
	e.appCard("r5", map[string]any{"name": "Pell", "description": "An owl."},
		map[string]any{"lorebook_remote_ids": []string{"l1"}})
	snap, err := e.s.Reconcile(e.ctx, ReconcileParams{InstanceID: e.inst}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range snap.Items {
		if len(it.Lorebooks) != 0 {
			t.Errorf("%s lorebooks = %+v", it.RemoteID, it.Lorebooks)
		}
	}
	if len(snap.Lorebooks) != 0 {
		t.Errorf("top-level lorebooks = %+v", snap.Lorebooks)
	}
}

func TestReconcileUnlistedLinkGrowsTotal(t *testing.T) {
	e := setup(t, map[string]any{"lorebooks": "standalone"})
	// The fake lists only l*.json, so x1 is linked but never listed.
	e.writeRemote("x1", map[string]any{"lorebook": map[string]any{"name": "Hidden",
		"entries": []any{map[string]any{"keys": []any{"k"}, "content": "Unlisted."}}}})
	e.appCard("r5", map[string]any{"name": "Pell", "description": "An owl."},
		map[string]any{"lorebook_remote_ids": []string{"x1"}})
	var progress [][2]int
	snap, err := e.s.Reconcile(e.ctx, ReconcileParams{InstanceID: e.inst}, func(done, total int) {
		progress = append(progress, [2]int{done, total})
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(progress, [][2]int{{1, 1}, {2, 2}}) || len(snap.Items) != 1 || len(snap.Items[0].Lorebooks) != 1 {
		t.Errorf("progress = %v, snapshot = %+v", progress, snap)
	}
}

func TestReconcileLorebookIgnoresSplit(t *testing.T) {
	e := setup(t, map[string]any{"lorebooks": "standalone"})
	sea, _, err := e.lib.Lorebooks.CreateFromName(e.ctx, "Sea lore", "upload", library.Edit{Source: "ui",
		Data: json.RawMessage(`{"name": "Sea lore", "description": "Old tales.",
			"entries": [{"keys": ["tide"], "content": "Tides rise twice a day."}]}`)})
	if err != nil {
		t.Fatal(err)
	}
	// name, description, and entries all differ; a card would be imported.
	e.writeRemote("l1", map[string]any{"lorebook": map[string]any{"name": "SEA LORE", "description": "New tales.",
		"entries": []any{map[string]any{"keys": []any{"tide"}, "content": "A kraken sleeps below the reef."}}}})
	one := 1
	snap, err := e.s.Reconcile(e.ctx, ReconcileParams{InstanceID: e.inst, SplitFields: &one}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Lorebooks) != 1 {
		t.Fatalf("lorebooks = %+v", snap.Lorebooks)
	}
	lb := snap.Lorebooks[0]
	if lb.Verdict != VerdictLikely || lb.Candidates[0].DifferingFields != 3 || lb.Suggested == nil ||
		lb.Suggested.Action != "bind" || lb.Suggested.CardID != sea.ID {
		t.Errorf("l1 = %+v, suggested %+v", lb, lb.Suggested)
	}
}

func TestReconcileSplitUsesUnboundCandidate(t *testing.T) {
	e := setup(t, map[string]any{"fields": []string{"name", "description", "personality", "scenario", "first_mes",
		"mes_example"}})
	taken := e.libCard(coreCard)
	if _, err := e.lib.Cards.AddSource(e.ctx, taken, library.Source{Kind: "chub", Ref: "someone/tamsin"}); err != nil {
		t.Fatal(err)
	}
	e.push(taken)
	free := e.libCard(edited("description", "personality", "scenario"))
	chub := map[string]any{"chub": map[string]any{"full_path": "someone/tamsin"}}
	// Both rank the bound card first by source; the free card decides.
	near := edited("mes_example")
	near["extensions"] = chub
	far := edited("description", "personality", "scenario", "mes_example")
	far["extensions"] = chub
	e.appCard("like-taken", near, nil)
	e.appCard("like-free", far, nil)

	got := e.reconcile(ReconcileParams{}, "like-taken", "like-free")
	for id, want := range map[string]Option{"like-taken": {Action: "import"}, "like-free": {Action: "bind", CardID: free}} {
		it := got[id]
		if ids(it.Candidates)[0] != taken || it.Suggested == nil || it.Suggested.Action != want.Action ||
			it.Suggested.CardID != want.CardID {
			t.Errorf("%s = %+v, suggested %+v", id, ids(it.Candidates), it.Suggested)
		}
	}
}

// Two apps reconciled side by side both see a card as new. The second import
// binds to the card the first one made rather than adding a copy.
func TestReconcileConcurrentImportsBindOneCard(t *testing.T) {
	e := setup(t, nil)
	dir2 := t.TempDir()
	inst2, err := e.reg.CreateInstance(e.ctx, plugins.NewInstance{Plugin: "fake", Name: "Other app",
		Config: map[string]any{"dir": dir2}})
	if err != nil {
		t.Fatal(err)
	}
	e.appCard("r1", map[string]any{"name": "Lia", "description": liaText}, nil)
	raw, err := os.ReadFile(filepath.Join(e.dir, "r1.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir2, "r1.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	// The other app's run reads the library before this one imports.
	stale, err := e.s.Reconcile(e.ctx, ReconcileParams{InstanceID: inst2, Apply: ApplyNone}, nil)
	if err != nil {
		t.Fatal(err)
	}
	it := stale.Items[0]
	if it.Verdict != VerdictNew {
		t.Fatalf("other app item = %+v", it)
	}
	snap, err := e.s.Reconcile(e.ctx, ReconcileParams{InstanceID: e.inst, Apply: ApplyObvious}, nil)
	if err != nil || !snap.Items[0].AutoImported {
		t.Fatalf("first import = %+v, %v", snap.Items, err)
	}
	lia := e.bindingOf("r1").CardID
	res, err := e.s.Resolve(e.ctx, Resolution{InstanceID: inst2, RemoteID: "r1", AppHash: it.AppHash, Action: "import"})
	if err != nil {
		t.Fatal(err)
	}
	var cards int
	if err := e.pool.QueryRow(e.ctx, `SELECT count(*) FROM card WHERE archived_at IS NULL`).Scan(&cards); err != nil {
		t.Fatal(err)
	}
	// Brakka from setup and Lia.
	if res.Action != "bind" || res.CardID != lia || cards != 2 {
		t.Errorf("second import = %+v, want bind to #%d; %d cards", res, lia, cards)
	}
}

// tagPNG copies the PNG at path with a tEXt chunk before IEND, as
// SillyTavern embeds the card in its avatars.
func tagPNG(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("chara\x00card json")
	body := append([]byte("tEXt"), data...)
	chunk := binary.BigEndian.AppendUint32(nil, uint32(len(data)))
	chunk = append(chunk, body...)
	chunk = binary.BigEndian.AppendUint32(chunk, crc32.ChecksumIEEE(body))
	iend := len(b) - 12
	out := append(append(append([]byte{}, b[:iend]...), chunk...), b[iend:]...)
	tagged := filepath.Join(t.TempDir(), "tagged.png")
	if err := os.WriteFile(tagged, out, 0o644); err != nil {
		t.Fatal(err)
	}
	return tagged
}

// An app avatar with the library's pixels but other metadata binds in sync;
// one changed pixel leaves the card ahead.
func TestReconcileBindIgnoresAvatarMetadata(t *testing.T) {
	for _, change := range []bool{false, true} {
		e := setup(t, nil)
		img := patternPNG(t, func(x, y int) uint8 { return uint8(x * y) })
		lia := e.libCard(map[string]any{"name": "Lia", "description": liaText})
		e.setAvatar(lia, img)
		appImg := tagPNG(t, img)
		if change {
			appImg = patternPNG(t, func(x, y int) uint8 { return uint8(x*y) + 1 })
		}
		e.appCard("r1", map[string]any{"name": "Lia", "description": liaText}, map[string]any{"avatar": appImg})
		snap, err := e.s.Reconcile(e.ctx, ReconcileParams{InstanceID: e.inst, Apply: ApplyObvious}, nil)
		if err != nil || !snap.Items[0].AutoBound {
			t.Fatalf("change=%v: items %+v, %v", change, snap.Items, err)
		}
		want := "in_sync"
		if change {
			want = "ahead"
		}
		if got := e.status(lia); got != want {
			t.Errorf("change=%v: status %s, want %s", change, got, want)
		}
	}
}

// An edit made only in the app becomes a library revision.
func TestReconcilePullsAppOnlyEdits(t *testing.T) {
	e := setup(t, nil)
	b := e.push(e.card)
	e.editRemote(b.RemoteID, "description", "Edited in the app.")
	snap, err := e.s.Reconcile(e.ctx, ReconcileParams{InstanceID: e.inst, Apply: ApplyObvious}, nil)
	if err != nil {
		t.Fatal(err)
	}
	it := snap.Items[0]
	if !it.AppOnly || !it.AutoPulled || it.AutoBound || snap.Totals["auto_pulled"] != 1 {
		t.Fatalf("item = %+v, totals %v", it, snap.Totals)
	}
	var data map[string]any
	if err := json.Unmarshal(e.head(e.card).Data, &data); err != nil || data["description"] != "Edited in the app." {
		t.Errorf("library = %v, %v", data, err)
	}
	if got := e.status(e.card); got != "in_sync" {
		t.Errorf("status %s", got)
	}
}

// Edits on both sides stay for the owner, even when they don't conflict.
func TestReconcileLeavesBothSidesEdited(t *testing.T) {
	e := setup(t, nil)
	b := e.push(e.card)
	e.editRemote(b.RemoteID, "description", "Edited in the app.")
	e.update(e.card, func(ed *library.Edit) { ed.Data = setField(ed.Data, "first_mes", "Edited in the library.") })
	before := e.head(e.card).ID
	snap, err := e.s.Reconcile(e.ctx, ReconcileParams{InstanceID: e.inst, Apply: ApplyObvious}, nil)
	if err != nil {
		t.Fatal(err)
	}
	it := snap.Items[0]
	if it.AppOnly || it.AutoPulled || e.head(e.card).ID != before {
		t.Errorf("item = %+v; head moved: %v", it, e.head(e.card).ID != before)
	}
}

// Pulling a card leaves the world books it links as they are.
func TestReconcilePullsCardWithWorldBooks(t *testing.T) {
	e, card, _ := worldBooks(t)
	// The fake app lists only cards whose IDs start with r.
	e.appCard("r1", map[string]any{"name": "Tova", "description": "A smith."},
		map[string]any{"lorebook_remote_ids": []string{"l1", "l2"}})
	if _, err := e.s.Bind(e.ctx, e.inst, card, "r1"); err != nil {
		t.Fatal(err)
	}
	e.editRemote("r1", "description", "A smith, edited in the app.")
	snap, err := e.s.Reconcile(e.ctx, ReconcileParams{InstanceID: e.inst, Apply: ApplyObvious}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var it ReconcileItem
	for _, i := range snap.Items {
		if i.RemoteID == "r1" {
			it = i
		}
	}
	if !it.AutoPulled || len(it.Lorebooks) != 2 {
		t.Fatalf("r1 = %+v", it)
	}
	for _, lb := range it.Lorebooks {
		if lb.AutoBound || lb.AutoImported {
			t.Errorf("world book %s was applied: %+v", lb.RemoteID, lb)
		}
	}
}
