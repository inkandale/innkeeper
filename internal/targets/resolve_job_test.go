package targets

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/davidgibbons/innkeeper/internal/jobs"
	"github.com/davidgibbons/innkeeper/internal/library"
	"github.com/davidgibbons/innkeeper/internal/merge"
)

// runResolve runs a bulk resolve to the end and returns the parent and the
// children's outcomes.
func (e *env) runResolve(rs []Resolution) (jobs.Job, map[string]int) {
	e.t.Helper()
	q := jobs.New(e.pool)
	wait := e.startWorker(q)
	parent, err := e.s.EnqueueResolve(e.ctx, q, rs)
	if err != nil {
		e.t.Fatal(err)
	}
	j := wait(parent.ID)
	out, err := q.ChildOutcomes(e.ctx, parent.ID)
	if err != nil {
		e.t.Fatal(err)
	}
	return j, out
}

func TestBulkResolveReportsEachItem(t *testing.T) {
	e := setup(t, nil)
	var rs []Resolution
	for _, id := range []string{"a", "b", "c"} {
		card := e.libCard(map[string]any{"name": "Tova " + id, "description": "A smith."})
		e.appCard(id, map[string]any{"name": "Tova " + id, "description": "A smith."}, nil)
		r := e.bindTo(false, id, card)
		r.Default = merge.ChooseLibrary
		rs = append(rs, r)
	}
	e.editRemote("c", "description", "A baker.")
	parent, out := e.runResolve(rs)
	if parent.Status != jobs.StatusFailed || parent.Error == nil || *parent.Error != "1 of 3 jobs failed" ||
		out["bind"] != 2 || out["stale"] != 1 || e.bindingCount() != 2 {
		t.Fatalf("parent %s (%v), outcomes %v, %d bindings", parent.Status, parent.Error, out, e.bindingCount())
	}
	var progress resolveProgress
	if err := e.pool.QueryRow(e.ctx, `SELECT progress FROM job WHERE parent_id = $1 AND status = 'failed'`, parent.ID).
		Scan(&progress); err != nil {
		t.Fatal(err)
	}
	if progress.Stale == nil || progress.Stale.Item != "card:c" || !strings.Contains(mustJSON2(progress.Stale.Comparison), "A baker.") {
		t.Errorf("stale progress = %+v", progress)
	}
}

func mustJSON2(v any) string { return string(mustMarshal(v)) }

// A suggested import sent twice, as ink sync apply run twice does,
// adds one card.
func TestBulkResolveRepeatedImportIsStale(t *testing.T) {
	e := setup(t, nil)
	e.appCard("a", map[string]any{"name": "Pell", "description": "A clockwork owl."}, nil)
	item := e.reconcile(ReconcileParams{InstanceID: e.inst}, "a")["a"]
	rs := []Resolution{{InstanceID: e.inst, RemoteID: "a", AppHash: item.AppHash, BindingID: item.BindingID, Action: "import"}}
	if _, out := e.runResolve(rs); out["import"] != 1 {
		t.Fatalf("first: outcomes %v", out)
	}
	cards := e.cardCount()
	if _, out := e.runResolve(rs); out["stale"] != 1 || e.cardCount() != cards {
		t.Fatalf("again: outcomes %v, %d cards, want %d", out, e.cardCount(), cards)
	}
}

func TestBulkResolveChecksShapeFirst(t *testing.T) {
	e := setup(t, nil)
	q := jobs.New(e.pool)
	rs := []Resolution{{InstanceID: e.inst, RemoteID: "a", Action: "skip"},
		{InstanceID: e.inst, RemoteID: "b", Action: "bind", AppHash: "h"}}
	if _, err := e.s.EnqueueResolve(e.ctx, q, rs); !errors.Is(err, library.ErrInvalid) {
		t.Fatalf("err = %v", err)
	}
	var n int
	if err := e.pool.QueryRow(e.ctx, `SELECT count(*) FROM job`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("%d jobs, %v", n, err)
	}
}

func TestBulkResolveRetriesAPluginError(t *testing.T) {
	e := setup(t, map[string]any{"fail_get": "retryable"})
	q := jobs.New(e.pool)
	wait := e.startWorker(q)
	parent, err := e.s.EnqueueResolve(e.ctx, q, []Resolution{{InstanceID: e.inst, RemoteID: "r1", Action: "bind",
		AppHash: "h", BaseRevisionID: 1, CardID: e.card}})
	if err != nil {
		t.Fatal(err)
	}
	var child int64
	if err := e.pool.QueryRow(e.ctx, `SELECT id FROM job WHERE parent_id = $1`, parent.ID).Scan(&child); err != nil {
		t.Fatal(err)
	}
	if j := wait(child); j.Status != jobs.StatusQueued || j.Attempts != 1 {
		t.Fatalf("child %s after %d attempts (%v)", j.Status, j.Attempts, j.Error)
	}
}

// Children run as separate jobs, so a world book two cards share is bound by
// the first; the second's resolution for it is stale and reported.
func TestBulkResolveSharedWorldBook(t *testing.T) {
	e, tova, forge := worldBooks(t)
	dain := e.libCard(map[string]any{"name": "Dain", "description": "A miner."})
	e.appCard("c1", map[string]any{"name": "Tova", "description": "A smith."},
		map[string]any{"lorebook_remote_ids": []string{"l1"}})
	e.appCard("c2", map[string]any{"name": "Dain", "description": "A miner."},
		map[string]any{"lorebook_remote_ids": []string{"l1"}})
	var rs []Resolution
	for _, c := range []struct {
		id   string
		card int64
	}{{"c1", tova}, {"c2", dain}} {
		r := e.bindTo(false, c.id, c.card)
		r.Lorebooks = []Resolution{e.bindTo(true, "l1", forge)}
		rs = append(rs, r)
	}
	if _, out := e.runResolve(rs); out["bind"] != 1 || out["stale"] != 1 {
		t.Fatalf("outcomes %v", out)
	}
}

func TestReconcileAutoBindsExactMatches(t *testing.T) {
	for _, apply := range []string{ApplyObvious, ApplyNone} {
		e, tova, _ := worldBooks(t)
		dain := e.libCard(map[string]any{"name": "Dain", "description": "A miner."})
		e.appCard("r1", map[string]any{"name": "Tova", "description": "A smith."}, nil)
		// Exact, but its world book is only likely: the app's Forge was rewritten.
		e.writeRemote("l9", map[string]any{"lorebook": map[string]any{"name": "Forge", "description": "Cold lore.",
			"entries": []any{map[string]any{"keys": []any{"forge"}, "content": "The forge is cold."}}}})
		e.appCard("r2", map[string]any{"name": "Dain", "description": "A miner."},
			map[string]any{"lorebook_remote_ids": []string{"l9"}})
		snap, err := e.s.Reconcile(e.ctx, ReconcileParams{InstanceID: e.inst, Apply: apply}, nil)
		if err != nil {
			t.Fatal(err)
		}
		items := map[string]ReconcileItem{}
		for _, it := range snap.Items {
			items[it.RemoteID] = it
		}
		c1, c2 := items["r1"], items["r2"]
		if c2.Verdict != VerdictExact || c2.Lorebooks[0].Verdict != VerdictLikely || c2.AutoBound {
			t.Errorf("%s: c2 = %+v", apply, c2)
		}
		if apply == ApplyNone {
			if c1.AutoBound || e.bindingCount() != 0 {
				t.Errorf("none: c1 = %+v, %d bindings", c1, e.bindingCount())
			}
			continue
		}
		dainBindings, err := e.s.Bindings(e.ctx, dain)
		if !c1.AutoBound || c1.BindingID == 0 || e.status(tova) != "in_sync" || err != nil || len(dainBindings) != 0 {
			t.Errorf("obvious: c1 = %+v, dain bindings %v, %v", c1, dainBindings, err)
		}
		for _, id := range snap.Unpushed {
			if id == tova {
				t.Error("an auto-bound card is listed as unpushed")
			}
		}
		raw, _ := json.Marshal(c1)
		if !strings.Contains(string(raw), `"auto_bound":true`) {
			t.Errorf("c1 JSON = %s", raw)
		}
	}
}

func TestBulkResolveWorldBook(t *testing.T) {
	e, _, forge := worldBooks(t)
	r := e.bindTo(true, "l1", forge)
	r.Kind = "lorebook"
	if parent, out := e.runResolve([]Resolution{r}); parent.Status != jobs.StatusSucceeded || out["bind"] != 1 ||
		e.lorebookStatus(forge) != "in_sync" {
		t.Fatalf("parent %s (%v), outcomes %v", parent.Status, parent.Error, out)
	}
}

// autoReconcile reconciles, running edit once every item is read and before
// anything is applied.
func (e *env) autoReconcile(edit func()) Snapshot {
	e.t.Helper()
	snap, err := e.s.Reconcile(e.ctx, ReconcileParams{InstanceID: e.inst}, func(done, total int) {
		if done == total && edit != nil {
			edit()
			edit = nil
		}
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return snap
}

func item(snap Snapshot, remoteID string) ReconcileItem {
	for _, it := range snap.Items {
		if it.RemoteID == remoteID {
			return it
		}
	}
	return ReconcileItem{}
}

func TestAutoBindLeavesAnItemThatChangedAfterTheRead(t *testing.T) {
	e, tova, _ := worldBooks(t)
	e.appCard("r1", map[string]any{"name": "Tova", "description": "A smith."}, nil)
	snap := e.autoReconcile(func() { e.editRemote("r1", "first_mes", "Go away.") })
	if it := item(snap, "r1"); it.Verdict != VerdictExact || it.AutoBound {
		t.Fatalf("r1 = %+v", it)
	}
	if b, err := e.s.Bindings(e.ctx, tova); err != nil || len(b) != 0 {
		t.Fatalf("bindings = %+v, %v", b, err)
	}
}

func TestAutoBindNeverTakesAppEdits(t *testing.T) {
	e, _, forge := worldBooks(t)
	lb := e.pushLorebook(forge)
	e.editRemote(lb.RemoteID, "entries", []any{map[string]any{"keys": []any{"forge"}, "content": "APP EDIT."}})
	e.appCard("r1", map[string]any{"name": "Tova", "description": "A smith."},
		map[string]any{"lorebook_remote_ids": []string{lb.RemoteID}})
	if it := item(e.autoReconcile(nil), "r1"); it.AutoBound {
		t.Fatalf("r1 = %+v", it)
	}
	if h, err := e.lib.Lorebooks.History(e.ctx, forge); err != nil || len(h) != 1 {
		t.Fatalf("Forge has %d revisions, %v", len(h), err)
	}
}

func TestAutoBindMarksTheCardAndItsWorldBooks(t *testing.T) {
	e, tova, forge := worldBooks(t)
	// Exact by the drift hash, which ignores trailing space, though the text differs.
	e.appCard("r1", map[string]any{"name": "Tova", "description": "A smith. "},
		map[string]any{"lorebook_remote_ids": []string{"l1"}})
	snap := e.autoReconcile(nil)
	it := item(snap, "r1")
	if it.Verdict != VerdictExact || !it.AutoBound || len(it.Lorebooks) != 1 || !it.Lorebooks[0].AutoBound ||
		it.Lorebooks[0].BindingID == 0 || snap.Totals["auto_bound"] != 2 {
		t.Fatalf("r1 = %+v, totals %v", it, snap.Totals)
	}
	if e.revisions(tova) != 1 || field(t, e.head(tova).Data, "description") != "A smith." || e.lorebookStatus(forge) != "in_sync" {
		t.Errorf("tova has %d revisions, head %s", e.revisions(tova), e.head(tova).Data)
	}
	for _, id := range append(snap.Missing, snap.Unpushed...) {
		if id == tova {
			t.Errorf("auto-bound card listed: missing %v, unpushed %v", snap.Missing, snap.Unpushed)
		}
	}
}

func TestAutoBindSkipsLikelyMatches(t *testing.T) {
	e, tova, _ := worldBooks(t)
	e.appCard("r1", map[string]any{"name": "Tova", "description": "A smith of the north."}, nil)
	it := item(e.autoReconcile(nil), "r1")
	if it.Verdict != VerdictLikely || it.Suggested == nil || it.Suggested.Action != "bind" || it.AutoBound {
		t.Fatalf("r1 = %+v", it)
	}
	if b, _ := e.s.Bindings(e.ctx, tova); len(b) != 0 {
		t.Fatalf("bindings = %+v", b)
	}
}

func TestBindOnlyResolveRefusesLibraryWrites(t *testing.T) {
	e, card := resolveEnv(t)
	c := e.compareItem("lv1", card)
	r := Resolution{InstanceID: e.inst, RemoteID: "lv1", AppHash: c.AppHash, BaseRevisionID: c.HeadRevisionID,
		Action: "bind", CardID: card, Default: merge.ChooseApp}
	if _, err := e.s.resolve(e.ctx, cardKind, r, resolveOpts{bindOnly: true}); !errors.Is(err, errWouldRevise) {
		t.Errorf("bind taking the app: %v", err)
	}
	r.Action = "import"
	if _, err := e.s.resolve(e.ctx, cardKind, r, resolveOpts{bindOnly: true}); !errors.Is(err, errWouldRevise) {
		t.Errorf("import: %v", err)
	}
	if e.revisions(card) != 1 || e.bindingCount() != 0 {
		t.Error("a bind-only resolve wrote something")
	}
}

func TestBulkResolveValidatesEveryItemFirst(t *testing.T) {
	e := setup(t, nil)
	q := jobs.New(e.pool)
	ok := Resolution{InstanceID: e.inst, RemoteID: "a", Action: "skip"}
	for name, r := range map[string]Resolution{
		"no app_hash":     {InstanceID: e.inst, RemoteID: "b", Action: "import"},
		"bad kind":        {InstanceID: e.inst, RemoteID: "b", Action: "skip", Kind: "deck"},
		"no instance":     {RemoteID: "b", Action: "skip"},
		"bad nested item": {InstanceID: e.inst, RemoteID: "b", Action: "skip", Lorebooks: []Resolution{{RemoteID: "l1", Action: "merge"}}},
	} {
		if _, err := e.s.EnqueueResolve(e.ctx, q, []Resolution{ok, r}); !errors.Is(err, library.ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	var n int
	if err := e.pool.QueryRow(e.ctx, `SELECT count(*) FROM job`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("%d jobs, %v", n, err)
	}
}

func TestBulkResolveReportsAConflict(t *testing.T) {
	e := setup(t, map[string]any{"lorebooks": "standalone", "fields": []string{"name", "description"}})
	card := e.libCard(map[string]any{"name": "Tova", "description": "A smith."})
	forge := e.newLorebook("Forge")
	book := map[string]any{"lorebook": map[string]any{"name": "Forge", "description": "Forge lore.",
		"entries": []any{map[string]any{"keys": []any{"forge"}, "content": "The forge burns hot."}}}}
	e.writeRemote("l1", book)
	e.writeRemote("l3", book)
	e.appCard("r1", map[string]any{"name": "Tova", "description": "A smith."},
		map[string]any{"lorebook_remote_ids": []string{"l1", "l3"}})
	r := e.bindTo(false, "r1", card)
	r.Lorebooks = []Resolution{e.bindTo(true, "l1", forge)}
	if _, out := e.runResolve([]Resolution{r}); out["conflict"] != 1 {
		t.Fatalf("outcomes %v", out)
	}
}

func TestAutoBindRefusesAMissingNestedBinding(t *testing.T) {
	e, _, forge := worldBooks(t)
	lb := e.pushLorebook(forge)
	if _, err := e.pool.Exec(e.ctx, `UPDATE binding SET remote_state = 'missing' WHERE id = $1`, lb.ID); err != nil {
		t.Fatal(err)
	}
	e.appCard("r1", map[string]any{"name": "Tova", "description": "A smith."},
		map[string]any{"lorebook_remote_ids": []string{lb.RemoteID}})
	if it := item(e.autoReconcile(nil), "r1"); it.AutoBound || len(it.Lorebooks) != 1 || it.Lorebooks[0].Verdict != VerdictBound {
		t.Fatalf("r1 = %+v", it)
	}
}

func TestAutoBindNeedsABindSuggestionForEachExactWorldBook(t *testing.T) {
	e, _, forge := worldBooks(t)
	e.pushLorebook(forge) // Forge is bound to another app world book, so l1 suggests skip.
	e.appCard("r1", map[string]any{"name": "Tova", "description": "A smith."},
		map[string]any{"lorebook_remote_ids": []string{"l1"}})
	it := item(e.autoReconcile(nil), "r1")
	if it.AutoBound || len(it.Lorebooks) != 1 || it.Lorebooks[0].Verdict != VerdictExact ||
		it.Lorebooks[0].Suggested == nil || it.Lorebooks[0].Suggested.Action != "skip" {
		t.Fatalf("r1 = %+v", it)
	}
}

func TestAutoBindSharesAWorldBookAcrossCards(t *testing.T) {
	e, _, forge := worldBooks(t)
	e.libCard(map[string]any{"name": "Dain", "description": "A miner."})
	links := map[string]any{"lorebook_remote_ids": []string{"l1"}}
	e.appCard("r1", map[string]any{"name": "Tova", "description": "A smith."}, links)
	e.appCard("r2", map[string]any{"name": "Dain", "description": "A miner."}, links)
	snap := e.autoReconcile(nil)
	if !item(snap, "r1").AutoBound || !item(snap, "r2").AutoBound || e.lorebookStatus(forge) != "in_sync" {
		t.Fatalf("r1 = %+v, r2 = %+v", item(snap, "r1"), item(snap, "r2"))
	}
}

func TestAutoApplyImportsNewItems(t *testing.T) {
	e, _, _ := worldBooks(t)
	// Listed, unlike worldBooks' c1: r1 (Tova, exact) links l1 (Forge, exact) and l2 (Mines, new).
	e.appCard("r1", map[string]any{"name": "Tova", "description": "A smith."},
		map[string]any{"lorebook_remote_ids": []string{"l1", "l2"}})
	e.writeRemote("l7", map[string]any{"lorebook": map[string]any{"name": "Docks", "description": "The harbor.",
		"entries": []any{map[string]any{"keys": []any{"pier"}, "content": "The pier floods at night."}}}})
	e.appCard("r5", map[string]any{"name": "Wren", "description": "A ferrywoman."},
		map[string]any{"lorebook_remote_ids": []string{"l7"}})
	snap := e.autoReconcile(nil)
	r1, r5 := item(snap, "r1"), item(snap, "r5")
	if !r1.AutoBound || len(r1.Lorebooks) != 2 || !r1.Lorebooks[0].AutoBound || !r1.Lorebooks[1].AutoImported {
		t.Errorf("r1 = %+v", r1)
	}
	if r5.Verdict != VerdictNew || !r5.AutoImported || r5.BindingID == 0 || !r5.Lorebooks[0].AutoImported {
		t.Fatalf("r5 = %+v", r5)
	}
	if snap.Totals["auto_imported"] != 3 || snap.Totals["auto_bound"] != 2 { // r5, l7, l2; r1, l1
		t.Errorf("totals = %v", snap.Totals)
	}
	if e.bindingOf("r5").ID != r5.BindingID {
		t.Errorf("binding = %+v", e.bindingOf("r5"))
	}
}

func TestAutoApplyLeavesANewCardWithALikelyWorldBook(t *testing.T) {
	e, _, _ := worldBooks(t)
	// Named like the library's Forge, with other text: likely, not exact.
	e.writeRemote("l6", map[string]any{"lorebook": map[string]any{"name": "Forge", "description": "Another forge.",
		"entries": []any{map[string]any{"keys": []any{"anvil"}, "content": "The anvil is cracked."}}}})
	e.appCard("r6", map[string]any{"name": "Pell", "description": "A tinker."},
		map[string]any{"lorebook_remote_ids": []string{"l6"}})
	if it := item(e.autoReconcile(nil), "r6"); it.AutoImported || it.BindingID != 0 {
		t.Fatalf("r6 = %+v", it)
	}
}

func TestAutoApplySharesAnImportedWorldBook(t *testing.T) {
	e, _, _ := worldBooks(t)
	e.writeRemote("l8", map[string]any{"lorebook": map[string]any{"name": "Market", "description": "Stalls.",
		"entries": []any{map[string]any{"keys": []any{"stall"}, "content": "Stalls open at dawn."}}}})
	for _, id := range []string{"r7", "r8"} {
		e.appCard(id, map[string]any{"name": "Seller " + id, "description": "Sells things."},
			map[string]any{"lorebook_remote_ids": []string{"l8"}})
	}
	snap := e.autoReconcile(nil)
	// Forge, plus Mines (l2) and Market (l8) imported once each.
	if !item(snap, "r7").AutoImported || !item(snap, "r8").AutoImported || e.lorebookCount() != 3 {
		t.Fatalf("r7 = %+v, r8 = %+v, %d lorebooks", item(snap, "r7"), item(snap, "r8"), e.lorebookCount())
	}
	if snap.Totals["auto_imported"] != 4 || snap.Totals["auto_bound"] != 1 { // r7, r8, l8, l2; l1
		t.Errorf("totals = %v", snap.Totals)
	}
}

func TestApplyNoneWritesNothing(t *testing.T) {
	e, _, _ := worldBooks(t)
	snap, err := e.s.Reconcile(e.ctx, ReconcileParams{InstanceID: e.inst, Apply: ApplyNone}, nil)
	if err != nil || e.bindingCount() != 0 || snap.Totals["auto_bound"]+snap.Totals["auto_imported"] != 0 {
		t.Fatalf("%v, %d bindings, totals %v", err, e.bindingCount(), snap.Totals)
	}
}

func TestAutoApplyImportsOneCopyOfTheSameContent(t *testing.T) {
	e := setup(t, map[string]any{"lorebooks": "standalone", "fields": []string{"name", "description"}})
	cards := e.cardCount()
	for _, id := range []string{"r1", "r2"} {
		e.appCard(id, map[string]any{"name": "Pell", "description": "A tinker."}, nil)
	}
	for _, id := range []string{"l1", "l2"} {
		e.writeRemote(id, map[string]any{"lorebook": map[string]any{"name": "Docks", "description": "The harbor.",
			"entries": []any{map[string]any{"keys": []any{"pier"}, "content": "The pier floods at night."}}}})
	}
	snap := e.autoReconcile(nil)
	if snap.Totals["auto_imported"] != 2 || e.cardCount() != cards+1 || e.lorebookCount() != 1 {
		t.Fatalf("totals %v, %d new cards, %d lorebooks", snap.Totals, e.cardCount()-cards, e.lorebookCount())
	}
	if r1, r2 := item(snap, "r1"), item(snap, "r2"); r1.AutoImported == r2.AutoImported || r2.Verdict != VerdictNew {
		t.Errorf("r1 = %+v, r2 = %+v", r1, r2)
	}
}

func TestAutoApplyImportsOneCopyOfALinkedWorldBook(t *testing.T) {
	e, _, _ := worldBooks(t)
	// Like l2 (Mines), which the run imports as a world book no card links.
	e.writeRemote("l5", map[string]any{"lorebook": map[string]any{"name": "Mines", "description": "Deep tunnels.",
		"entries": []any{map[string]any{"keys": []any{"mine"}, "content": "The mines are dark."}}}})
	e.appCard("r1", map[string]any{"name": "Wren", "description": "A ferrywoman."},
		map[string]any{"lorebook_remote_ids": []string{"l5"}})
	snap := e.autoReconcile(nil)
	if it := item(snap, "r1"); it.AutoImported || e.lorebookCount() != 2 { // Forge, Mines
		t.Fatalf("r1 = %+v, %d lorebooks", it, e.lorebookCount())
	}
}
