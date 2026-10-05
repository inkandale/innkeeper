package targets

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/davidgibbons/innkeeper/internal/jobs"
	"github.com/davidgibbons/innkeeper/internal/library"
)

func TestJobs(t *testing.T) {
	e := setup(t, nil)
	other := e.newCard("Carrow")
	q := jobs.New(e.pool)
	wait := e.startWorker(q)

	parent, err := e.s.EnqueueBulkPush(e.ctx, q, e.inst, []int64{e.card, other}, false)
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

// startWorker runs a worker for the test and returns a wait that returns once
// a job finishes or is queued to retry.
func (e *env) startWorker(q *jobs.Queue) func(id int64) jobs.Job {
	e.t.Helper()
	w := &jobs.Worker{Queue: q, Log: quiet, Poll: 50 * time.Millisecond,
		Backoff:  func(int) time.Duration { return time.Hour },
		Handlers: map[string]jobs.Handler{PushKind: e.s.RunPush, ScanKind: e.s.RunScan}}
	ctx, cancel := context.WithCancel(e.ctx)
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	e.t.Cleanup(func() { cancel(); <-done })
	return func(id int64) jobs.Job {
		e.t.Helper()
		for deadline := time.Now().Add(20 * time.Second); ; time.Sleep(50 * time.Millisecond) {
			j, err := q.Get(e.ctx, id)
			if err != nil {
				e.t.Fatal(err)
			}
			if j.Status == jobs.StatusSucceeded || j.Status == jobs.StatusFailed || j.Status == jobs.StatusQueued && j.Attempts > 0 {
				return j
			}
			if time.Now().After(deadline) {
				e.t.Fatalf("job %d still %s", id, j.Status)
			}
		}
	}
}

func TestPushJobFailures(t *testing.T) {
	for mode, want := range map[string]string{"retryable": jobs.StatusQueued, "fatal": jobs.StatusFailed} {
		t.Run(mode, func(t *testing.T) {
			e := setup(t, map[string]any{"fail_put": mode})
			q := jobs.New(e.pool)
			j, err := e.s.EnqueuePush(e.ctx, q, e.inst, e.card, false)
			if err != nil {
				t.Fatal(err)
			}
			if got := e.startWorker(q)(j.ID); got.Status != want {
				t.Fatalf("status %s (%v), want %s", got.Status, got.Error, want)
			}
		})
	}
}

func TestPushJobUncertainCreate(t *testing.T) {
	e := setup(t, map[string]any{"fail_put": "crash"})
	q := jobs.New(e.pool)
	j, err := e.s.EnqueuePush(e.ctx, q, e.inst, e.card, false)
	if err != nil {
		t.Fatal(err)
	}
	got := e.startWorker(q)(j.ID)
	if got.Status != jobs.StatusFailed || got.Error == nil || !strings.Contains(*got.Error, "may have the card") {
		t.Fatalf("job %s, error %v", got.Status, got.Error)
	}
}

func TestPushJobSkipsFinishedCopy(t *testing.T) {
	e := setup(t, map[string]any{"update": false})
	q := jobs.New(e.pool)
	e.push(e.card)
	j, err := e.s.EnqueuePush(e.ctx, q, e.inst, e.card, true)
	if err != nil {
		t.Fatal(err)
	}
	// The job ran once already and made its copy, then was run again.
	if _, err := e.s.Push(e.ctx, e.inst, e.card, true); err != nil {
		t.Fatal(err)
	}
	if got := e.startWorker(q)(j.ID); got.Status != jobs.StatusSucceeded || !json.Valid(got.Progress) {
		t.Fatalf("job %s (%v), progress %s", got.Status, got.Error, got.Progress)
	}
	if files, _ := os.ReadDir(e.dir); len(files) != 2 {
		t.Fatalf("the app has %d cards, want 2", len(files))
	}
}

// On a target that updates in place, a queued push still runs after another
// push lands, so an edit made in between reaches the app.
func TestPushJobUpdatesAfterAnotherPush(t *testing.T) {
	e := setup(t, nil)
	q := jobs.New(e.pool)
	e.push(e.card)
	j, err := e.s.EnqueuePush(e.ctx, q, e.inst, e.card, false)
	if err != nil {
		t.Fatal(err)
	}
	e.push(e.card)
	e.update(e.card, func(ed *library.Edit) { ed.Data = setField(ed.Data, "first_mes", "Well met.") })
	if got := e.startWorker(q)(j.ID); got.Status != jobs.StatusSucceeded {
		t.Fatalf("job %s (%v)", got.Status, got.Error)
	}
	if got := e.remoteData("r1")["first_mes"]; got != "Well met." || e.status(e.card) != "in_sync" {
		t.Fatalf("app first_mes = %v, status %s", got, e.status(e.card))
	}
}

func TestBulkPushDedupes(t *testing.T) {
	e := setup(t, nil)
	q := jobs.New(e.pool)
	parent, err := e.s.EnqueueBulkPush(e.ctx, q, e.inst, []int64{e.card, e.card}, false)
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := e.pool.QueryRow(e.ctx, `SELECT count(*) FROM job WHERE parent_id = $1`, parent.ID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("%d children, %v", n, err)
	}
}

func TestLorebookPushJob(t *testing.T) {
	e := setup(t, map[string]any{"lorebooks": "standalone"})
	lb := e.newLorebook("Forge lore")
	q := jobs.New(e.pool)
	j, err := e.s.EnqueueLorebookPush(e.ctx, q, e.inst, lb, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := e.startWorker(q)(j.ID); got.Status != jobs.StatusSucceeded {
		t.Fatalf("job %s (%v)", got.Status, got.Error)
	}
	if s := e.lorebookStatus(lb); s != "in_sync" {
		t.Fatalf("status %s", s)
	}
}
