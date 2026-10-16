package targets

import (
	"errors"
	"strings"
	"testing"

	"github.com/inkandale/innkeeper/internal/jobs"
	"github.com/inkandale/innkeeper/internal/library"
)

// bulkEnv has two cards pushed as r1 and r2, and a queue.
func bulkEnv(t *testing.T) (*env, int64, *jobs.Queue) {
	e := setup(t, nil)
	other := e.newCard("Carrow")
	e.push(e.card)
	e.push(other)
	q := jobs.New(e.pool)
	return e, other, q
}

func (e *env) binding(card int64) Binding {
	e.t.Helper()
	list, err := e.s.Bindings(e.ctx, library.Card, card)
	if err != nil || len(list) != 1 {
		e.t.Fatalf("%d bindings, %v", len(list), err)
	}
	return list[0]
}

// childOf returns the bulk job's child that pushed card.
func (e *env) childOf(q *jobs.Queue, parent, card int64) jobs.Job {
	e.t.Helper()
	var id int64
	if err := e.pool.QueryRow(e.ctx, `SELECT id FROM job WHERE parent_id = $1 AND (payload->>'card_id')::bigint = $2`,
		parent, card).Scan(&id); err != nil {
		e.t.Fatal(err)
	}
	j, err := q.Get(e.ctx, id)
	if err != nil {
		e.t.Fatal(err)
	}
	return j
}

func TestBulkPushRefusesChangedCopy(t *testing.T) {
	e, other, q := bulkEnv(t)
	// No check sees this edit before the push.
	e.editRemote("r1", "first_mes", "Well met.")
	e.update(other, func(ed *library.Edit) { ed.Data = setField(ed.Data, "description", "A sailor.") })

	parent, err := e.s.EnqueueBulkPush(e.ctx, q, library.Card, e.inst, []int64{e.card, other}, false, "")
	if err != nil {
		t.Fatal(err)
	}
	if j := e.startWorker(q)(parent.ID); j.Status != jobs.StatusFailed {
		t.Fatalf("bulk push %s, want failed", j.Status)
	}
	if c := e.childOf(q, parent.ID, e.card); c.Status != jobs.StatusFailed || c.Error == nil || !strings.Contains(*c.Error, "changed") {
		t.Fatalf("card's push %s, error %v", c.Status, c.Error)
	}
	if got := e.remoteData("r1")["first_mes"]; got != "Well met." {
		t.Fatalf("the app's edit was overwritten: first_mes = %v", got)
	}
	if got := e.remoteData("r2")["description"]; got != "A sailor." {
		t.Fatalf("the unchanged copy wasn't pushed: description = %v", got)
	}
}

func TestBulkPreview(t *testing.T) {
	e, other, q := bulkEnv(t)
	unbound := e.newCard("Dunmore")
	e.editRemote("r2", "first_mes", "Ahoy.")
	e.check(e.binding(other))
	ids := []int64{e.card, other, unbound}

	p, err := e.s.PreviewBulkPush(e.ctx, library.Card, e.inst, ids)
	if err != nil {
		t.Fatal(err)
	}
	if p.Total != 3 || p.New != 1 || p.InSync != 1 || p.Drifted != 1 || p.Confirm == "" {
		t.Fatalf("preview %+v", p)
	}

	// A check records another edit after the preview.
	e.editRemote("r2", "first_mes", "Avast.")
	e.check(e.binding(other))
	var stale *StalePreviewError
	if _, err := e.s.EnqueueBulkPush(e.ctx, q, library.Card, e.inst, ids, false, p.Confirm); !errors.As(err, &stale) {
		t.Fatalf("push with a stale confirm: %v", err)
	}
	if stale.Preview.Confirm == p.Confirm || stale.Preview.Drifted != 1 {
		t.Fatalf("the refusal's preview %+v", stale.Preview)
	}

	// The new confirm replaces the drifted copy it showed.
	parent, err := e.s.EnqueueBulkPush(e.ctx, q, library.Card, e.inst, ids, false, stale.Preview.Confirm)
	if err != nil {
		t.Fatal(err)
	}
	if j := e.startWorker(q)(parent.ID); j.Status != jobs.StatusSucceeded {
		t.Fatalf("bulk push %s: %v", j.Status, j.Error)
	}
	if got := e.remoteData("r2")["first_mes"]; got != "Hail." {
		t.Fatalf("drifted copy not replaced: first_mes = %v", got)
	}
}

func TestBulkPushRefusesCopyChangedAfterPreview(t *testing.T) {
	e, other, q := bulkEnv(t)
	e.editRemote("r2", "first_mes", "Ahoy.")
	e.check(e.binding(other))
	p, err := e.s.PreviewBulkPush(e.ctx, library.Card, e.inst, []int64{other})
	if err != nil {
		t.Fatal(err)
	}
	// Edited again, with no check to record it.
	e.editRemote("r2", "first_mes", "Avast.")
	parent, err := e.s.EnqueueBulkPush(e.ctx, q, library.Card, e.inst, []int64{other}, false, p.Confirm)
	if err != nil {
		t.Fatal(err)
	}
	if j := e.startWorker(q)(parent.ID); j.Status != jobs.StatusFailed {
		t.Fatalf("bulk push %s, want failed", j.Status)
	}
	if got := e.remoteData("r2")["first_mes"]; got != "Avast." {
		t.Fatalf("the app's edit was overwritten: first_mes = %v", got)
	}
}

// A card unbound when the bulk push is queued may only be created, so a copy
// bound to it before the job runs is left alone.
func TestBulkPushLeavesCopyBoundAfterQueue(t *testing.T) {
	e := setup(t, nil)
	q := jobs.New(e.pool)
	parent, err := e.s.EnqueueBulkPush(e.ctx, q, library.Card, e.inst, []int64{e.card}, false, "")
	if err != nil {
		t.Fatal(err)
	}
	e.push(e.card)
	e.editRemote("r1", "first_mes", "Well met.")
	if j := e.startWorker(q)(parent.ID); j.Status != jobs.StatusSucceeded {
		t.Fatalf("bulk push %s: %v", j.Status, j.Error)
	}
	if got := e.remoteData("r1")["first_mes"]; got != "Well met." {
		t.Fatalf("the app's edit was overwritten: first_mes = %v", got)
	}
}

// A push that lands after the bulk push is queued is what the app's copy is
// checked against, so the queued push still sends a later edit.
func TestBulkPushAfterAnotherPush(t *testing.T) {
	e, _, q := bulkEnv(t)
	parent, err := e.s.EnqueueBulkPush(e.ctx, q, library.Card, e.inst, []int64{e.card}, false, "")
	if err != nil {
		t.Fatal(err)
	}
	e.update(e.card, func(ed *library.Edit) { ed.Data = setField(ed.Data, "description", "A sailor.") })
	e.push(e.card)
	e.update(e.card, func(ed *library.Edit) { ed.Data = setField(ed.Data, "first_mes", "Well met.") })
	if j := e.startWorker(q)(parent.ID); j.Status != jobs.StatusSucceeded {
		t.Fatalf("bulk push %s: %v", j.Status, j.Error)
	}
	if got := e.remoteData("r1")["first_mes"]; got != "Well met." {
		t.Fatalf("app first_mes = %v", got)
	}
}
