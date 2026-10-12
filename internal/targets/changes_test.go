package targets

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/davidgibbons/innkeeper/internal/library"
)

func TestChanges(t *testing.T) {
	e := setup(t, nil)
	// ahead: pushed, then edited in the library only.
	ab := e.push(e.card)
	e.update(e.card, func(ed *library.Edit) { ed.Data = setField(ed.Data, "first_mes", "Edited in the library.") })
	// both_changed: pushed, then edited on both sides.
	both := e.libCard(map[string]any{"name": "Tova", "description": "A smith."})
	bb := e.push(both)
	e.editRemote(bb.RemoteID, "description", "Edited in the app.")
	e.update(both, func(ed *library.Edit) { ed.Data = setField(ed.Data, "first_mes", "Edited in the library.") })
	// missing: pushed, then deleted in the app and checked.
	gone := e.libCard(map[string]any{"name": "Dain", "description": "A miner."})
	gb := e.push(gone)
	if err := os.Remove(filepath.Join(e.dir, gb.RemoteID+".json")); err != nil {
		t.Fatal(err)
	}
	e.check(gb)
	// The fake lists cards by an r prefix, and the pushes took r1 to r3.
	// differs: an app item likely the same as an unbound card.
	lia := e.libCard(map[string]any{"name": "Lia", "description": liaText})
	e.appCard("r7", map[string]any{"name": "Lia", "description": liaText + " She also juggles."}, nil)
	// new: an app item nothing matches.
	e.appCard("r8", map[string]any{"name": "Zed", "description": "Nobody in particular."}, nil)

	res, err := e.s.Sync(e.ctx, SyncParams{Apply: ApplyNone}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// The fixtures give the snapshot the items each state is built from.
	var raw []byte
	if err := e.pool.QueryRow(e.ctx, `SELECT progress FROM job WHERE id = $1`, res.Apps[0].JobID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var snap Snapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		t.Fatal(err)
	}
	items := map[string]ReconcileItem{}
	for _, it := range snap.Items {
		items[it.RemoteID] = it
	}
	if it := items[ab.RemoteID]; it.Verdict != VerdictBound || it.AppChanged {
		t.Errorf("ahead item = %+v", it)
	}
	if it := items[bb.RemoteID]; it.Verdict != VerdictBound || !it.AppChanged || it.AppOnly {
		t.Errorf("both-changed item = %+v", it)
	}
	if it := items["r7"]; it.Verdict != VerdictLikely || unbound(it.Candidates) < 0 || it.Candidates[unbound(it.Candidates)].CardID != lia {
		t.Errorf("likely item = %+v", it)
	}

	ch, err := e.s.Changes(e.ctx, ChangesFilter{})
	if err != nil {
		t.Fatal(err)
	}
	states := map[string]string{}
	for _, r := range ch.Rows {
		if len(r.Apps) != 1 {
			t.Fatalf("row %q has %d apps: %+v", r.Name, len(r.Apps), r.Apps)
		}
		states[r.Name] = r.Apps[0].State
	}
	want := map[string]string{"Zed (Fake app only)": ChangeNew, "Brakka": ChangeAhead, "Tova": ChangeBothChanged,
		"Dain": ChangeMissing, "Lia": ChangeDiffers}
	if len(states) != len(want) {
		t.Errorf("rows = %v, want %v", states, want)
	}
	for name, st := range want {
		if states[name] != st {
			t.Errorf("%s = %q, want %q (rows %v)", name, states[name], st, states)
		}
	}
	if ch.Rows[0].CardID != 0 || ch.Counts[e.inst][ChangeAhead] != 1 {
		t.Errorf("first row %+v, counts %v", ch.Rows[0], ch.Counts)
	}

	// A filter narrows the rows but not the counts.
	ahead, err := e.s.Changes(e.ctx, ChangesFilter{State: ChangeAhead})
	if err != nil || len(ahead.Rows) != 1 || ahead.Rows[0].CardID != e.card || ahead.Counts[e.inst][ChangeNew] != 1 {
		t.Errorf("ahead only = %+v, %v", ahead, err)
	}

	// Binding the likely match takes it off the queue, though the snapshot is older.
	if _, err := e.s.Bind(e.ctx, e.inst, lia, "r7"); err != nil {
		t.Fatal(err)
	}
	ch, err = e.s.Changes(e.ctx, ChangesFilter{State: ChangeDiffers})
	if err != nil || len(ch.Rows) != 0 {
		t.Errorf("after bind = %+v, %v", ch.Rows, err)
	}
}

func TestChangesBoundError(t *testing.T) {
	e := setup(t, nil)
	b := e.push(e.card)
	// The fake still lists a card with no data, but innkeeper can't read it.
	if err := os.WriteFile(filepath.Join(e.dir, b.RemoteID+".json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := e.s.Sync(e.ctx, SyncParams{Apply: ApplyNone}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Apps[0].Error != "" {
		t.Fatal(res.Apps[0].Error)
	}
	ch, err := e.s.Changes(e.ctx, ChangesFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(ch.Rows) != 1 || ch.Rows[0].CardID != e.card || len(ch.Rows[0].Apps) != 1 || ch.Rows[0].Apps[0].State != ChangeError {
		t.Errorf("rows = %+v, want an error on card %d", ch.Rows, e.card)
	}
}

func TestChangesOneItemPerCard(t *testing.T) {
	e := setup(t, nil)
	// Two identical copies of one card; the sync binds one, and the other must not claim the card too.
	r := e.appCopy(e.card)
	e.writeRemote("r7", e.remote(r))
	e.writeRemote("r8", e.remote(r))
	if err := os.Remove(filepath.Join(e.dir, r+".json")); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.Sync(e.ctx, SyncParams{Apply: ApplyObvious}, nil); err != nil {
		t.Fatal(err)
	}
	ch, err := e.s.Changes(e.ctx, ChangesFilter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range ch.Rows {
		if r.CardID == e.card {
			t.Errorf("card %d listed though bound: %+v", e.card, r.Apps)
		}
	}
}
