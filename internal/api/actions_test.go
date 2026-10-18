package api

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/inkandale/innkeeper/internal/actions"
	"github.com/inkandale/innkeeper/internal/jobs"
	"github.com/inkandale/innkeeper/internal/library"
	"github.com/inkandale/innkeeper/internal/pluginhost"
	"github.com/inkandale/innkeeper/internal/plugins"
	"github.com/inkandale/innkeeper/internal/tier"
	"github.com/inkandale/innkeeper/protocol"
)

// actionEnv adds the fake plugin as an action plugin, forge, with one
// instance, and runs a worker for action jobs.
type actionEnv struct {
	*testEnv
	instID int64
	cardID int64
}

func newActionEnv(t *testing.T) *actionEnv {
	t.Helper()
	e := newEnv(t)
	ctx := context.Background()
	forge := *e.manifests["fake"]
	forge.Name, forge.Capabilities = "forge", []string{"action"}
	forge.Command = []string{forge.Command[0], "-caps=action", "-name=forge"}
	e.manifests["forge"] = &forge
	id, err := e.s.Registry.CreateInstance(ctx, plugins.NewInstance{Plugin: "forge", Name: new("Forge")})
	if err != nil {
		t.Fatal(err)
	}

	runs := pluginhost.NewRuns(quiet)
	mgr := pluginhost.NewManager(runs.Handle, time.Minute, quiet)
	t.Cleanup(mgr.Close)
	rn := &actions.Runner{Lib: e.s.Lib, Registry: e.s.Registry, Plugins: mgr, Runs: runs, API: e.s.APIHandler()}
	w := &jobs.Worker{Queue: e.s.Queue, Log: quiet, Poll: 50 * time.Millisecond, Lease: 600 * time.Millisecond,
		Handlers: map[string]jobs.Handler{actions.Kind: rn.Run}}
	wctx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- w.Run(wctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("worker: %v", err)
		}
	})

	var c entityOut
	e.call("POST", "/cards", map[string]any{"data": map[string]any{"name": "Brakka", "description": "Big."}}, 201, &c)
	return &actionEnv{testEnv: e, instID: id, cardID: c.ID}
}

// start starts a run of action on the test card with body's other fields.
func (e *actionEnv) start(action string, body map[string]any) jobOut {
	e.t.Helper()
	body["instance_id"], body["action"] = e.instID, action
	if body["entity"] == nil {
		body["entity"] = map[string]any{"kind": "card", "id": e.cardID, "data": map[string]any{"name": "Brakka", "description": "Big."}}
	}
	var j jobOut
	e.call("POST", "/actions/runs", body, 202, &j)
	return j
}

// finished waits for the job to finish and returns it with its progress.
func (e *actionEnv) finished(id int64) (jobOut, actions.Progress) {
	e.t.Helper()
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		var j jobOut
		e.call("GET", fmt.Sprintf("/jobs/%d", id), nil, 200, &j)
		if j.Status == jobs.StatusSucceeded || j.Status == jobs.StatusFailed {
			var p actions.Progress
			if err := json.Unmarshal(j.Progress, &p); err != nil {
				e.t.Fatal(err)
			}
			return j, p
		}
	}
	e.t.Fatalf("job %d still running", id)
	return jobOut{}, actions.Progress{}
}

// succeeded is finished for a run that must succeed. It returns the result
// with the proposal decoded.
func (e *actionEnv) succeeded(id int64) (*actions.Result, map[string]any) {
	e.t.Helper()
	j, p := e.finished(id)
	if j.Status != jobs.StatusSucceeded || p.Result == nil {
		e.t.Fatalf("job %d: %s, %v, progress %s", id, j.Status, j.Error, j.Progress)
	}
	var proposal map[string]any
	if len(p.Result.Proposal) > 0 {
		if err := json.Unmarshal(p.Result.Proposal, &proposal); err != nil {
			e.t.Fatal(err)
		}
	}
	return p.Result, proposal
}

func TestListActions(t *testing.T) {
	e := newActionEnv(t)
	var list []PluginAction
	e.call("GET", "/actions?applies=card", nil, 200, &list)
	var ids []string
	for _, a := range list {
		ids = append(ids, a.ID)
		if a.InstanceID != e.instID || a.InstanceName != "Forge" {
			t.Errorf("action %s from %d %q", a.ID, a.InstanceID, a.InstanceName)
		}
	}
	if want := []string{"tighten", "reforge", "evaluate", "sneaky", "lookup"}; !slices.Equal(ids, want) {
		t.Fatalf("card actions = %v, want %v", ids, want)
	}
	e.call("GET", "/actions?applies=new", nil, 200, &list)
	if len(list) != 1 || list[0].ID != "generate" || !list[0].Images {
		t.Fatalf("new actions = %+v", list)
	}
}

func TestStartActionRunRejects(t *testing.T) {
	e := newActionEnv(t)
	run := func(body map[string]any) result {
		body["instance_id"] = e.instID
		return e.send("POST", "/actions/runs", body, "Authorization", "Bearer "+e.token)
	}
	card := map[string]any{"kind": "card", "data": map[string]any{"name": "Brakka"}}
	tests := []struct {
		name string
		body map[string]any
	}{
		{"unknown action", map[string]any{"action": "summon", "entity": card}},
		{"wrong kind", map[string]any{"action": "tighten", "field": "description", "entity": map[string]any{"kind": "lorebook", "data": map[string]any{}}}},
		{"field not offered", map[string]any{"action": "tighten", "field": "scenario", "entity": card}},
		{"images not taken", map[string]any{"action": "tighten", "field": "description", "entity": card, "images": map[string]any{"avatar": "ab"}}},
	}
	for _, tt := range tests {
		if r := run(tt.body); r.Status != 400 {
			t.Errorf("%s: %s, want 400", tt.name, r)
		}
	}
	if r := run(map[string]any{"action": "tighten", "field": "description", "entity": card}); r.Status != 202 {
		t.Fatalf("a good run: %s", r)
	}
	if r := e.send("POST", "/actions/runs", map[string]any{"instance_id": e.instID, "action": "tighten"},
		"Authorization", "Bearer "+e.tokenWith(tier.Read)); r.Status != 403 {
		t.Fatalf("a read token started a run: %s", r)
	}
}

func TestActionRunFieldProposal(t *testing.T) {
	e := newActionEnv(t)
	j := e.start("tighten", map[string]any{"field": "description"})
	res, proposal := e.succeeded(j.ID)
	if proposal["description"] != "Tight: Big." || proposal["name"] != "Brakka" || len(res.Warnings) != 0 {
		t.Fatalf("result %+v, proposal %v", res, proposal)
	}
	_, p := e.finished(j.ID)
	if p.Message != "Running tighten" || p.Done != 1 || p.Total != 2 {
		t.Fatalf("progress %+v", p)
	}

	var list []jobOut
	e.call("GET", fmt.Sprintf("/jobs?kind=action&card_id=%d", e.cardID), nil, 200, &list)
	if len(list) != 1 || list[0].ID != j.ID || strings.Contains(string(list[0].Progress), "result") {
		t.Fatalf("listed %+v", list)
	}
	e.call("GET", fmt.Sprintf("/jobs?kind=action&card_id=%d", e.cardID+1), nil, 200, &list)
	if len(list) != 0 {
		t.Fatalf("another card's runs: %+v", list)
	}
}

func TestActionRunDropsOutOfScope(t *testing.T) {
	e := newActionEnv(t)
	res, proposal := e.succeeded(e.start("sneaky", map[string]any{"field": "description"}).ID)
	if proposal["description"] != "Sneaky" || proposal["name"] != "Brakka" || proposal["character_book"] != nil {
		t.Fatalf("proposal %v", proposal)
	}
	if len(res.Warnings) != 2 || !strings.Contains(res.Warnings[0], "character_book") || !strings.Contains(res.Warnings[1], "name") {
		t.Fatalf("warnings %q", res.Warnings)
	}
}

func TestActionRunReportOnly(t *testing.T) {
	e := newActionEnv(t)
	res, proposal := e.succeeded(e.start("evaluate", map[string]any{}).ID)
	if proposal != nil || res.Report == nil || res.Report.Summary != "Needs work." || len(res.Warnings) != 1 {
		t.Fatalf("result %+v", res)
	}
}

func TestActionRunNewCardGetsImagePaths(t *testing.T) {
	e := newActionEnv(t)
	var b library.Blob
	e.call("POST", "/blobs", append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 32)...), 201, &b)
	res, proposal := e.succeeded(e.start("generate", map[string]any{"instruction": "A seer.",
		"entity": map[string]any{"kind": "card"}, "images": map[string]any{"reference": b.SHA256}}).ID)
	var images struct{ Reference, Avatar string }
	if err := json.Unmarshal([]byte(res.Raw), &images); err != nil {
		t.Fatal(err)
	}
	if proposal["description"] != "A seer." || !strings.HasSuffix(images.Reference, "/"+b.SHA256) || images.Avatar != "" {
		t.Fatalf("proposal %v, images %+v", proposal, images)
	}
}

func TestLibraryCallRefusals(t *testing.T) {
	e := newActionEnv(t)
	getCard := map[string]any{"name": "get_card", "arguments": map[string]any{"id": e.cardID}}
	raw := func(action string, call map[string]any) string {
		res, _ := e.succeeded(e.start(action, map[string]any{"inputs": map[string]any{"call": call}}).ID)
		return res.Raw
	}
	e.call("PATCH", fmt.Sprintf("/instances/%d", e.instID), map[string]any{"library_access": true}, 200, nil)
	if got := raw("reforge", getCard); !strings.HasPrefix(got, "error -32003") {
		t.Errorf("without the declaration: %s", got)
	}
	if got := raw("lookup", getCard); !strings.Contains(got, "Brakka") || strings.Contains(got, "is_error") {
		t.Errorf("with access: %s", got)
	}
	if got := raw("lookup", map[string]any{"name": "create_card", "arguments": map[string]any{"data": map[string]any{"name": "X"}}}); !strings.HasPrefix(got, "error -32003") {
		t.Errorf("a write tool: %s", got)
	}
	e.call("PATCH", fmt.Sprintf("/instances/%d", e.instID), map[string]any{"library_access": false}, 200, nil)
	if got := raw("lookup", getCard); !strings.HasPrefix(got, "error -32003") || !strings.Contains(got, "Library access") {
		t.Errorf("without access: %s", got)
	}
}

func TestCancelledActionRunLeavesNoResult(t *testing.T) {
	e := newActionEnv(t)
	j := e.start("reforge", map[string]any{"instruction": "x", "inputs": map[string]any{"sleep_ms": 1500}})
	for deadline := time.Now().Add(10 * time.Second); j.Status != jobs.StatusRunning; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the run never started")
		}
		e.call("GET", fmt.Sprintf("/jobs/%d", j.ID), nil, 200, &j)
	}
	e.call("POST", fmt.Sprintf("/jobs/%d/cancel", j.ID), nil, 200, nil)
	time.Sleep(2500 * time.Millisecond)
	j, p := e.finished(j.ID)
	if j.Status != jobs.StatusFailed || j.Error == nil || *j.Error != "cancelled" || p.Result != nil {
		t.Fatalf("job %+v, progress %+v", j, p)
	}
}

func TestActionRunFailsWithoutRetry(t *testing.T) {
	e := newActionEnv(t)
	j, p := e.finished(e.start("reforge", map[string]any{"inputs": map[string]any{"fail": true}}).ID)
	if j.Status != jobs.StatusFailed || j.Error == nil || !strings.Contains(*j.Error, "the model refused") || j.Attempts != 1 || p.Result != nil {
		t.Fatalf("job %+v", j)
	}
}

func TestListSkills(t *testing.T) {
	e := newActionEnv(t)
	var list []PluginSkill
	e.call("GET", "/actions/skills", nil, 200, &list)
	want := []PluginSkill{{protocol.Skill{Name: "college", Description: "Campus life."}, e.instID, "Forge"},
		{protocol.Skill{Name: "noir"}, e.instID, "Forge"}}
	if !slices.Equal(list, want) {
		t.Fatalf("skills = %+v, want %+v", list, want)
	}
	// Actions and skills come from one cached action.list reply.
	e.call("GET", "/actions", nil, 200, nil)
	inst, err := e.s.Registry.Instance(context.Background(), e.instID)
	if err != nil {
		t.Fatal(err)
	}
	var calls int
	if err := e.s.Registry.Call(context.Background(), e.s.Plugins, inst, "calls", protocol.MethodActionList, &calls); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("action.list called %d times, want 1", calls)
	}
}

func TestActionRunSendsMentionedSkills(t *testing.T) {
	e := newActionEnv(t)
	j := e.start("reforge", map[string]any{"instruction": "use @skills/noir", "context": map[string]any{"skills": []string{"noir"}}})
	if res, _ := e.succeeded(j.ID); res.Raw != "skills: noir" {
		t.Fatalf("raw = %q", res.Raw)
	}
}

func TestActionRunOnLorebook(t *testing.T) {
	e := newActionEnv(t)
	var list []PluginAction
	e.call("GET", "/actions?applies=lorebook", nil, 200, &list)
	if len(list) != 2 || list[0].ID != "lore" || list[1].ID != "tighten-lore" {
		t.Fatalf("lorebook actions = %+v", list)
	}
	var book entityOut
	data := map[string]any{"name": "Forge", "entries": []any{
		map[string]any{"id": 1, "keys": []any{"anvil"}, "content": "Heavy."},
		map[string]any{"id": 2, "keys": []any{"bellows"}, "content": "Airy."}}}
	e.call("POST", "/lorebooks", map[string]any{"data": data}, 201, &book)
	j := e.start("lore", map[string]any{"instruction": "Hammers ring.", "entity": map[string]any{"kind": "lorebook", "id": book.ID, "data": data}})
	_, proposal := e.succeeded(j.ID)
	entries, _ := proposal["entries"].([]any)
	if len(entries) != 2 || entries[0].(map[string]any)["content"] != "Reworked: Heavy." || entries[1].(map[string]any)["content"] != "Hammers ring." {
		t.Fatalf("entries %v", entries)
	}

	var jobs []jobOut
	e.call("GET", fmt.Sprintf("/jobs?kind=action&lorebook_id=%d", book.ID), nil, 200, &jobs)
	if len(jobs) != 1 || jobs[0].ID != j.ID {
		t.Fatalf("the lorebook's runs: %+v", jobs)
	}
	e.call("GET", fmt.Sprintf("/jobs?kind=action&card_id=%d", book.ID), nil, 200, &jobs)
	if len(jobs) != 0 {
		t.Fatalf("a card with the lorebook's ID has its runs: %+v", jobs)
	}
	if r := e.send("GET", fmt.Sprintf("/jobs?card_id=1&lorebook_id=%d", book.ID), nil, "Authorization", "Bearer "+e.token); r.Status != 400 {
		t.Fatalf("both filters: %s", r)
	}
}
