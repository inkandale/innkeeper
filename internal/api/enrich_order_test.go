package api

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/inkandale/innkeeper/internal/enrich"
	"github.com/inkandale/innkeeper/internal/jobs"
	"github.com/inkandale/innkeeper/internal/library"
	"github.com/inkandale/innkeeper/internal/libsettings"
	"github.com/inkandale/innkeeper/protocol"
)

// lastEnrichJob is the newest enrich job on inst.
func (e *enrichEnv) lastEnrichJob(inst int64) int64 {
	e.t.Helper()
	var id int64
	if err := e.pool.QueryRow(context.Background(), `SELECT coalesce(max(id), 0) FROM job WHERE kind = $1 AND instance_id = $2`,
		enrich.Kind, inst).Scan(&id); err != nil {
		e.t.Fatal(err)
	}
	return id
}

// lint makes judge cost 200 and adds a copy of it, lint, that costs 100.
func (e *enrichEnv) lint() int64 {
	e.t.Helper()
	e.editManifest("judge", func(m *protocol.Manifest) { m.EnrichCost = 200 })
	return e.addEnricher("lint", func(m *protocol.Manifest) { m.EnrichCost = 100 })
}

func TestEnrichWaitsForACheaperInstanceAnArchiveRuleReads(t *testing.T) {
	e := newEnrichEnv(t)
	ctx := context.Background()
	lintID := e.lint()
	e.call("PATCH", fmt.Sprintf("/instances/%d", e.instID), map[string]any{"enrich_on_change": true}, 200, nil)
	early := e.card("Early", strings.Repeat("z", 90))
	r := e.newRule("Lint junk", map[string]any{"enrichment": "quality", "instance": lintID, "lte": 30},
		map[string]any{"action": "archive", "all_libraries": true, "enabled": false})
	e.putRule(r, enable)
	// waitingRun queues an on-change run of judge alone, as the sweep would, and waits for it.
	waitingRun := func(cards ...int64) enrich.Progress {
		t.Helper()
		j, err := e.s.Queue.Enqueue(ctx, jobs.Spec{Kind: enrich.Kind, InstanceID: &e.instID, Payload: enrich.Payload{CardIDs: cards, Wait: true}})
		if err != nil {
			t.Fatal(err)
		}
		_, p := e.waitRun(j.ID)
		return p
	}

	// Lint doesn't run as cards change, so it gates nothing: a card can't wait for it forever.
	if p := waitingRun(early); p.Waiting != 0 || p.Done != 1 {
		t.Fatalf("judge run while lint is manual only %+v", p)
	}

	// Once lint runs as cards change, judge waits on the cards that change after: lint has answered neither.
	e.call("PATCH", fmt.Sprintf("/instances/%d", lintID), map[string]any{"enrich_on_change": true}, 200, nil)
	short := e.card("Short", "tiny")
	long := e.card("Long", strings.Repeat("x", 90))
	fresh := e.card("Fresh", strings.Repeat("y", 90))
	if p := waitingRun(short, long); p.Waiting != 2 || p.Done != 0 {
		t.Fatalf("judge run before lint %+v", p)
	}
	first := e.lastEnrichJob(e.instID)

	// Lint answers: the rule archives short, and judge gets long alone.
	if _, p := e.evaluate(lintID, short, long); p.Done != 2 {
		t.Fatalf("lint run %+v", p)
	}
	if got := e.archivedReason(short); got != r.Reason() {
		t.Fatalf("short = %q", got)
	}
	second := e.lastEnrichJob(e.instID)
	if second == first {
		t.Fatal("lint's batch queued no judge run")
	}
	if _, p := e.waitRun(second); p.Done != 1 || p.Waiting != 0 {
		t.Fatalf("second judge run %+v", p)
	}
	judged := func(card int64) bool {
		var vals []enrichmentValue
		e.call("GET", fmt.Sprintf("/cards/%d/enrichments", card), nil, 200, &vals)
		return slices.ContainsFunc(vals, func(v enrichmentValue) bool { return v.InstanceID == e.instID })
	}
	if !judged(long) || judged(short) {
		t.Fatalf("judged long %v, short %v", judged(long), judged(short))
	}

	// A run the owner starts doesn't wait.
	if _, p := e.evaluate(e.instID, fresh); p.Done != 1 || p.Waiting != 0 {
		t.Fatalf("manual run %+v", p)
	}
}

func TestAGatesAnswerKeepsADuplicateArchived(t *testing.T) {
	e := newEnrichEnv(t)
	ctx := context.Background()
	lintID := e.lint()
	e.putRule(e.newRule("Lint junk", map[string]any{"enrichment": "quality", "instance": lintID, "lte": 30},
		map[string]any{"action": "archive", "all_libraries": true, "enabled": false}), enable)
	dup := e.card("Dup", "copy")
	if _, _, err := e.s.Lib.SetHidden(ctx, library.DefaultLibrary, map[int64]string{dup: library.ReasonDuplicate}, nil); err != nil {
		t.Fatal(err)
	}
	// The card was archived after the batch read it, so the gate sees it among the answered.
	g := &libsettings.Gate{Settings: e.s.libSettings(), Queue: e.s.Queue}
	if err := g.Answered(ctx, lintID, []int64{dup}); err != nil {
		t.Fatal(err)
	}
	if got := e.archivedReason(dup); got != library.ReasonDuplicate {
		t.Fatalf("dup = %q", got)
	}
}

func TestACardLintsSweepWontReachDoesntWaitForLint(t *testing.T) {
	e := newEnrichEnv(t)
	lintID := e.lint()
	e.call("PATCH", fmt.Sprintf("/instances/%d", e.instID), map[string]any{"enrich_on_change": true}, 200, nil)
	old := e.card("Old", "tiny")
	e.putRule(e.newRule("Lint junk", map[string]any{"enrichment": "quality", "instance": lintID, "lte": 30},
		map[string]any{"action": "archive", "all_libraries": true, "enabled": false}), enable)
	// Lint goes on-change after old changed, so its sweep never sends it old.
	e.call("PATCH", fmt.Sprintf("/instances/%d", lintID), map[string]any{"enrich_on_change": true}, 200, nil)
	sw := &enrich.Sweeper{Lib: e.s.Lib, Registry: e.s.Registry, Queue: e.s.Queue}
	if err := sw.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if e.lastEnrichJob(lintID) != 0 {
		t.Fatal("lint's sweep queued a run")
	}
	if _, p := e.waitRun(e.lastEnrichJob(e.instID)); p.Done != 1 || p.Waiting != 0 {
		t.Fatalf("judge's sweep run %+v", p)
	}
	if e.archivedReason(old) != "-" {
		t.Fatal("old was archived")
	}
}
