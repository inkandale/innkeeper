package targets

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/davidgibbons/innkeeper/internal/jobs"
	"github.com/davidgibbons/innkeeper/internal/library"
	"github.com/davidgibbons/innkeeper/internal/merge"
	"github.com/davidgibbons/innkeeper/internal/plugins"
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
	for _, on := range []bool{true, false} {
		e, tova, _ := worldBooks(t)
		dain := e.libCard(map[string]any{"name": "Dain", "description": "A miner."})
		e.appCard("r1", map[string]any{"name": "Tova", "description": "A smith."}, nil)
		// Exact, but its world book is only likely: the app's Forge was rewritten.
		e.writeRemote("l9", map[string]any{"lorebook": map[string]any{"name": "Forge", "description": "Cold lore.",
			"entries": []any{map[string]any{"keys": []any{"forge"}, "content": "The forge is cold."}}}})
		e.appCard("r2", map[string]any{"name": "Dain", "description": "A miner."},
			map[string]any{"lorebook_remote_ids": []string{"l9"}})
		if err := e.reg.UpdateInstance(e.ctx, e.inst, plugins.InstanceEdit{AutoBindExact: &on}); err != nil {
			t.Fatal(err)
		}
		snap, err := e.s.Reconcile(e.ctx, ReconcileParams{InstanceID: e.inst}, nil)
		if err != nil {
			t.Fatal(err)
		}
		items := map[string]ReconcileItem{}
		for _, it := range snap.Items {
			items[it.RemoteID] = it
		}
		c1, c2 := items["r1"], items["r2"]
		if c2.Verdict != VerdictExact || c2.Lorebooks[0].Verdict != VerdictLikely || c2.AutoBound {
			t.Errorf("on %v: c2 = %+v", on, c2)
		}
		if !on {
			if c1.AutoBound || e.bindingCount() != 0 {
				t.Errorf("off: c1 = %+v, %d bindings", c1, e.bindingCount())
			}
			continue
		}
		dainBindings, err := e.s.Bindings(e.ctx, dain)
		if !c1.AutoBound || c1.BindingID == 0 || e.status(tova) != "in_sync" || err != nil || len(dainBindings) != 0 {
			t.Errorf("on: c1 = %+v, dain bindings %v, %v", c1, dainBindings, err)
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
