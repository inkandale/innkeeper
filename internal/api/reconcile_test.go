package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/davidgibbons/innkeeper/internal/bulk"
	"github.com/davidgibbons/innkeeper/internal/jobs"
	"github.com/davidgibbons/innkeeper/internal/targets"
)

// appCard writes a character into a fake target's dir, as if made in the app.
func (e *testEnv) appCard(dir, remoteID, name, greeting string) {
	e.t.Helper()
	path := filepath.Join(dir, remoteID+".json")
	raw, _ := json.Marshal(map[string]any{"spec": "chara_card_v3", "data": map[string]any{"name": name, "first_mes": greeting}})
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		e.t.Fatal(err)
	}
}

// work runs reconcile and resolve jobs until the test ends.
func (e *testEnv) work() {
	w := &jobs.Worker{Queue: e.s.Queue, Log: quiet, Poll: 50 * time.Millisecond,
		Handlers: map[string]jobs.Handler{targets.ReconcileKind: e.s.Targets.RunReconcile, targets.ResolveKind: e.s.Targets.RunResolve,
			targets.SyncKind: e.s.Targets.RunSync}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	e.t.Cleanup(func() { cancel(); <-done })
}

func TestReconcileRoutes(t *testing.T) {
	e := newEnv(t)
	e.work()
	inst, dir := e.target()
	var c entityOut
	e.call("POST", "/cards", map[string]any{"data": map[string]any{"name": "Brakka", "first_mes": "Hail, traveler."}}, 201, &c)
	e.appCard(dir, "rBrakka.png", "Brakka", "Well met, traveler.")
	e.appCard(dir, "rCarrow", "Carrow", "Hello.")
	e.appCard(dir, "rsub/Brakka.png", "Brakka", "Well met, traveler.")

	var j jobOut
	e.call("POST", "/reconciles", map[string]any{"instance_id": inst, "apply": "none"}, 202, &j)
	if last, r := e.watchJob(j.ID); last.Status != "succeeded" {
		t.Fatalf("reconcile = %+v\n%s", last, r.Body)
	}
	e.call("GET", fmt.Sprintf("/jobs/%d", j.ID), nil, 200, &j)
	var snap targets.Snapshot
	if err := json.Unmarshal(j.Progress, &snap); err != nil {
		t.Fatal(err)
	}
	verdicts, hashes := map[string]string{}, map[string]string{}
	for _, it := range snap.Items {
		verdicts[it.RemoteID], hashes[it.RemoteID] = it.Verdict, it.AppHash
	}
	if verdicts["rBrakka.png"] != "likely" || verdicts["rCarrow"] != "new" {
		t.Fatalf("verdicts = %v", verdicts)
	}
	e.call("POST", "/reconciles", map[string]any{"instance_id": 999}, 404, nil)

	item := func(id string) string { return fmt.Sprintf("/instances/%d/items/%s", inst, url.PathEscape(id)) }
	var cmp targets.Comparison
	e.call("GET", fmt.Sprintf("%s/compare?card_id=%d", item("rsub/Brakka.png"), c.ID), nil, 200, &cmp)
	if cmp.RemoteID != "rsub/Brakka.png" {
		t.Fatalf("compare remote_id = %q", cmp.RemoteID)
	}
	e.call("GET", fmt.Sprintf("%s/compare?card_id=%d", item("rBrakka.png"), c.ID), nil, 200, &cmp)
	if cmp.RemoteID != "rBrakka.png" || cmp.AppHash == "" {
		t.Fatalf("compare = %+v", cmp)
	}

	body := map[string]any{"action": "bind", "card_id": c.ID, "app_hash": "stale", "base_revision_id": cmp.HeadRevisionID,
		"accepted_hash": cmp.AcceptedHash, "default": "app"}
	r := e.call("POST", item("rBrakka.png")+"/resolve", body, 409, nil)
	var conflict struct {
		Errors []struct{ Value targets.StaleError }
	}
	if err := json.Unmarshal(r.Body, &conflict); err != nil || len(conflict.Errors) != 1 || conflict.Errors[0].Value.Comparison.AppHash != cmp.AppHash {
		t.Fatalf("stale resolve = %s", r)
	}
	body["app_hash"] = cmp.AppHash
	body["choices"] = map[string]string{"nope": "app"}
	e.call("POST", item("rBrakka.png")+"/resolve", body, 422, nil)
	delete(body, "choices")
	var res targets.ResolveResult
	e.call("POST", item("rBrakka.png")+"/resolve", body, 200, &res)
	if res.Action != "bind" || res.CardID != c.ID || res.BindingID == 0 || res.RevisionID == 0 {
		t.Fatalf("resolve = %+v", res)
	}

	// An unbound item with no card has nothing to compare with.
	e.call("GET", item("rCarrow")+"/compare", nil, 404, nil)
	j = jobOut{}
	e.call("POST", "/resolutions", map[string]any{"resolutions": []map[string]any{
		{"instance_id": inst, "remote_id": "rCarrow", "action": "import", "app_hash": hashes["rCarrow"]}}}, 202, &j)
	if j.Kind != bulk.Kind {
		t.Fatalf("bulk resolve = %+v", j)
	}
	if last, r := e.watchJob(j.ID); last.Status != "succeeded" || last.Children["succeeded"] != 1 {
		t.Fatalf("bulk resolve = %+v\n%s", last, r.Body)
	}
	var children []jobOut
	e.call("GET", fmt.Sprintf("/jobs?parent=%d", j.ID), nil, 200, &children)
	var p struct{ Outcome string }
	if len(children) != 1 || json.Unmarshal(children[0].Progress, &p) != nil || p.Outcome != "import" {
		t.Fatalf("children = %+v", children)
	}
	e.call("POST", "/resolutions", map[string]any{"resolutions": []map[string]any{{"remote_id": "rCarrow", "action": "skip"}}}, 400, nil)
}

func TestSyncRoute(t *testing.T) {
	e := newEnv(t)
	e.work()
	inst, dir := e.target()
	e.appCard(dir, "rCarrow", "Carrow", "Hello.")
	var j jobOut
	e.call("POST", "/syncs", map[string]any{}, 202, &j)
	if last, r := e.watchJob(j.ID); last.Status != "succeeded" {
		t.Fatalf("sync = %+v\n%s", last, r.Body)
	}
	e.call("GET", fmt.Sprintf("/jobs/%d", j.ID), nil, 200, &j)
	var res targets.SyncResult
	if err := json.Unmarshal(j.Progress, &res); err != nil {
		t.Fatalf("result = %s, %v", j.Progress, err)
	}
	// The env's other fake instances can pull too, and fail without a dir; the sync goes on.
	var mine targets.SyncApp
	for _, a := range res.Apps {
		if a.InstanceID == inst {
			mine = a
		}
	}
	if mine.JobID == 0 || mine.Error != "" {
		t.Fatalf("app for instance %d = %+v in %s", inst, mine, j.Progress)
	}
	var app jobOut
	e.call("GET", fmt.Sprintf("/jobs/%d", mine.JobID), nil, 200, &app)
	if app.Kind != targets.ReconcileKind || app.Status != "succeeded" {
		t.Errorf("app job = %+v", app)
	}
	e.call("POST", "/syncs", map[string]any{"instance_id": 999}, 404, nil)
}

func TestChangesRoute(t *testing.T) {
	e := newEnv(t)
	e.work()
	inst, dir := e.target()
	e.appCard(dir, "rCarrow", "Carrow", "Hello.")
	var j jobOut
	e.call("POST", "/syncs", map[string]any{"apply": "none"}, 202, &j)
	if last, r := e.watchJob(j.ID); last.Status != "succeeded" {
		t.Fatalf("sync = %+v\n%s", last, r.Body)
	}
	var ch targets.Changes
	e.call("GET", "/sync/changes", nil, 200, &ch)
	if len(ch.Rows) != 1 || len(ch.Rows[0].Apps) != 1 || ch.Rows[0].Apps[0].State != targets.ChangeNew ||
		ch.Rows[0].Apps[0].InstanceID != inst || ch.Counts[inst][targets.ChangeNew] != 1 {
		t.Fatalf("changes = %+v", ch)
	}
	var ahead targets.Changes
	e.call("GET", "/sync/changes?state=ahead", nil, 200, &ahead)
	if len(ahead.Rows) != 0 || ahead.Counts[inst][targets.ChangeNew] != 1 {
		t.Fatalf("ahead = %+v", ahead)
	}
	e.call("GET", "/sync/changes?state=bogus", nil, 422, nil)
}
