package targets

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/davidgibbons/innkeeper/internal/jobs"
)

func TestJobs(t *testing.T) {
	e := setup(t, nil)
	other := e.newCard("Carrow")
	q := jobs.New(e.pool)
	w := &jobs.Worker{Queue: q, Log: quiet, Poll: 50 * time.Millisecond,
		Handlers: map[string]jobs.Handler{PushKind: e.s.RunPush, ScanKind: e.s.RunScan}}
	ctx, cancel := context.WithCancel(e.ctx)
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	defer func() { cancel(); <-done }()
	wait := func(id int64) jobs.Job {
		t.Helper()
		for deadline := time.Now().Add(20 * time.Second); ; time.Sleep(50 * time.Millisecond) {
			j, err := q.Get(e.ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if j.Status == jobs.StatusSucceeded || j.Status == jobs.StatusFailed {
				return j
			}
			if time.Now().After(deadline) {
				t.Fatalf("job %d still %s", id, j.Status)
			}
		}
	}

	parent, err := EnqueueBulkPush(e.ctx, q, e.inst, []int64{e.card, other}, false)
	if err != nil {
		t.Fatal(err)
	}
	if j := wait(parent.ID); j.Status != jobs.StatusSucceeded {
		t.Fatalf("bulk push %s: %v", j.Status, j.Error)
	}
	if e.status(e.card) != "in_sync" || e.status(other) != "in_sync" {
		t.Fatal("bulk push didn't push both cards")
	}

	e.editRemote("r1", "first_mes", "Well met.")
	scan, err := q.Enqueue(e.ctx, jobs.Spec{Kind: ScanKind})
	if err != nil {
		t.Fatal(err)
	}
	j := wait(scan.ID)
	var p ScanProgress
	_ = json.Unmarshal(j.Progress, &p)
	if j.Status != jobs.StatusSucceeded || p.Checked != 2 || p.Changed != 1 {
		t.Fatalf("scan %s, progress %s", j.Status, j.Progress)
	}
}
