package targets

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/davidgibbons/innkeeper/internal/jobs"
	"github.com/davidgibbons/innkeeper/internal/library"
)

func TestReconcileJob(t *testing.T) {
	e := setup(t, nil)
	e.push(e.card)
	e.appCard("r5", map[string]any{"name": "Pell", "description": "A clockwork owl that reads the stars."}, nil)
	q := jobs.New(e.pool)
	wait := e.startWorker(q)
	j, err := e.s.EnqueueReconcile(e.ctx, q, ReconcileParams{InstanceID: e.inst})
	if err != nil {
		t.Fatal(err)
	}
	if j = wait(j.ID); j.Status != jobs.StatusSucceeded {
		t.Fatalf("job %s: %v", j.Status, j.Error)
	}
	var snap Snapshot
	if err := json.Unmarshal(j.Progress, &snap); err != nil {
		t.Fatal(err)
	}
	sum := 0
	for _, n := range snap.Totals {
		sum += n
	}
	if len(snap.Items) != 2 || sum != 2 || snap.Totals[VerdictBound] != 1 || snap.Totals[VerdictNew] != 1 ||
		*snap.Params.SplitFields != 3 {
		t.Errorf("snapshot = %+v", snap)
	}
}

func TestReconcileJobFailureKeepsProgress(t *testing.T) {
	e := setup(t, nil)
	e.appCard("r1", map[string]any{"name": "Pell", "description": "A clockwork owl."}, nil)
	// The second item's source lookup fails, after the first item's progress.
	e.appCard("r2", map[string]any{"name": "Vyr", "description": "A dragon.",
		"extensions": map[string]any{"chub": map[string]any{"full_path": "someone/vyr"}}}, nil)
	if _, err := e.pool.Exec(e.ctx, `ALTER TABLE card_source RENAME TO card_source_gone`); err != nil {
		t.Fatal(err)
	}
	q := jobs.New(e.pool)
	wait := e.startWorker(q)
	j, err := e.s.EnqueueReconcile(e.ctx, q, ReconcileParams{InstanceID: e.inst})
	if err != nil {
		t.Fatal(err)
	}
	if j = wait(j.ID); j.Status != jobs.StatusFailed {
		t.Fatalf("job %s", j.Status)
	}
	var p map[string]any
	if err := json.Unmarshal(j.Progress, &p); err != nil || len(p) != 2 || p["done"] != 1.0 || p["total"] != 2.0 {
		t.Errorf("progress = %s, %v", j.Progress, err)
	}
}

func TestEnqueueReconcileRefuses(t *testing.T) {
	e := setup(t, map[string]any{"pull": false})
	q := jobs.New(e.pool)
	if _, err := e.s.EnqueueReconcile(e.ctx, q, ReconcileParams{InstanceID: e.inst}); !errors.Is(err, ErrCannot) {
		t.Errorf("push-only target: err = %v", err)
	}
	big := 40
	if _, err := e.s.EnqueueReconcile(e.ctx, q, ReconcileParams{InstanceID: e.inst, SplitFields: &big}); !errors.Is(err, library.ErrInvalid) {
		t.Errorf("bad params: err = %v", err)
	}
	var n int
	if err := e.pool.QueryRow(e.ctx, `SELECT count(*) FROM job`).Scan(&n); err != nil || n != 0 {
		t.Errorf("%d jobs queued, %v", n, err)
	}
}
