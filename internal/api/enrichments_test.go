package api

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/inkandale/innkeeper/internal/enrich"
	"github.com/inkandale/innkeeper/internal/jobs"
	"github.com/inkandale/innkeeper/internal/library"
	"github.com/inkandale/innkeeper/internal/pluginhost"
	"github.com/inkandale/innkeeper/internal/plugins"
	"github.com/inkandale/innkeeper/internal/testutil"
	"github.com/inkandale/innkeeper/internal/tier"
)

// enrichEnv adds the fake plugin as an enrich plugin, judge, with one
// instance, and runs a worker for enrich jobs.
type enrichEnv struct {
	*testEnv
	instID int64
}

func newEnrichEnv(t *testing.T) *enrichEnv {
	t.Helper()
	e := newEnv(t)
	judge := *e.manifests["fake"]
	judge.Name, judge.Capabilities = "judge", []string{"enrich"}
	judge.Command = []string{judge.Command[0], "-caps=enrich", "-name=judge"}
	if _, err := toml.Decode(testutil.FakeEnrichments, &judge); err != nil {
		t.Fatal(err)
	}
	e.manifests["judge"] = &judge
	id, err := e.s.Registry.CreateInstance(context.Background(), plugins.NewInstance{Plugin: "judge", Name: new("Judge")})
	if err != nil {
		t.Fatal(err)
	}
	runs := pluginhost.NewRuns(quiet)
	mgr := pluginhost.NewManager(runs.Handle, time.Minute, quiet)
	t.Cleanup(mgr.Close)
	rn := &enrich.Runner{Lib: e.s.Lib, Registry: e.s.Registry, Plugins: mgr, Runs: runs}
	w := &jobs.Worker{Queue: e.s.Queue, Log: quiet, Poll: 50 * time.Millisecond, Lease: 600 * time.Millisecond,
		Handlers: map[string]jobs.Handler{enrich.Kind: rn.Run}}
	wctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(wctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("worker: %v", err)
		}
	})
	return &enrichEnv{testEnv: e, instID: id}
}

// card creates a card and returns its ID.
func (e *enrichEnv) card(name, description string) int64 {
	e.t.Helper()
	data, _ := json.Marshal(map[string]string{"name": name, "description": description})
	return e.createCard(string(data)).ID
}

// evaluate runs the instance on cards and waits for the job to end.
func (e *enrichEnv) evaluate(inst int64, cards ...int64) (jobOut, enrich.Progress) {
	e.t.Helper()
	return e.startRun(map[string]any{"instance_id": inst, "card_ids": cards})
}

// startRun starts a run with body and waits for the job to end.
func (e *enrichEnv) startRun(body map[string]any) (jobOut, enrich.Progress) {
	e.t.Helper()
	var j jobOut
	e.call("POST", "/enrichments/runs", body, 202, &j)
	return e.waitRun(j.ID)
}

// waitRun waits for the enrich job id to end.
func (e *enrichEnv) waitRun(id int64) (jobOut, enrich.Progress) {
	e.t.Helper()
	var j jobOut
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		e.call("GET", fmt.Sprintf("/jobs/%d", id), nil, 200, &j)
		if j.Status == jobs.StatusSucceeded || j.Status == jobs.StatusFailed {
			var p enrich.Progress
			if err := json.Unmarshal(j.Progress, &p); err != nil {
				e.t.Fatal(err)
			}
			return j, p
		}
	}
	e.t.Fatalf("job %d still running", id)
	return j, enrich.Progress{}
}

func (e *enrichEnv) search(q map[string]any, status int) library.Results {
	e.t.Helper()
	var res library.Results
	e.call("POST", "/cards/search", q, status, &res)
	return res
}

func TestEnrichRunStoresCheckedValues(t *testing.T) {
	e := newEnrichEnv(t)
	good := e.card("Brakka", strings.Repeat("x", 78))
	bad := e.card("Bad Brakka", "x")
	fail := e.card("Fail", "x")
	skip := e.card("Skip", "x")
	j, p := e.evaluate(e.instID, good, bad, fail, skip, 999999)
	if j.Status != jobs.StatusSucceeded || p.Total != 5 || p.Done != 2 || p.Failed != 3 || j.CardsChanged != 2 {
		t.Fatalf("job %s, %d cards changed, progress %+v", j.Status, j.CardsChanged, p)
	}
	for _, w := range []string{"dropped mood", "dropped voice", "dropped quality", "Fail: the model returned no JSON",
		"Skip: the plugin sent no result", "card 999999"} {
		if !slices.ContainsFunc(p.Warnings, func(s string) bool { return strings.Contains(s, w) }) {
			t.Errorf("no warning with %q in %q", w, p.Warnings)
		}
	}

	var vals []enrichmentValue
	e.call("GET", fmt.Sprintf("/cards/%d/enrichments", good), nil, 200, &vals)
	if len(vals) != 3 || vals[0].Key != "quality" || string(vals[0].Value) != "78" || vals[0].Label != "Quality" ||
		!vals[0].Grid || vals[0].InstanceName != "Judge" || vals[0].Stale || string(vals[0].Meta) != `{"model":"fake-1"}` {
		t.Fatalf("values = %+v", vals)
	}
	e.call("GET", fmt.Sprintf("/cards/%d/enrichments", bad), nil, 200, &vals)
	if len(vals) != 1 || vals[0].Key != "summary" {
		t.Fatalf("bad card's values = %+v", vals)
	}

	// An edit makes the values stale; a second run replaces them, and history keeps the first.
	var c entityOut
	e.call("GET", fmt.Sprintf("/cards/%d", good), nil, 200, &c)
	e.call("PUT", fmt.Sprintf("/cards/%d", good), map[string]any{"base_revision_id": c.Revision.ID,
		"data": map[string]any{"name": "Brakka", "description": strings.Repeat("x", 90)}}, 200, nil)
	e.call("GET", fmt.Sprintf("/cards/%d/enrichments", good), nil, 200, &vals)
	if !vals[0].Stale {
		t.Fatalf("after an edit: %+v", vals[0])
	}
	e.evaluate(e.instID, good)
	e.call("GET", fmt.Sprintf("/cards/%d/enrichments?history=true", good), nil, 200, &vals)
	if len(vals) != 6 || string(vals[0].Value) != "90" || vals[0].Stale || !vals[0].Current ||
		string(vals[1].Value) != "78" || vals[1].Current || !vals[1].Stale {
		t.Fatalf("history = %+v", vals)
	}
	e.call("GET", "/cards/999999/enrichments", nil, 404, nil)
}

func TestEnrichRunSkipsCardsScoredOnTheirHead(t *testing.T) {
	e := newEnrichEnv(t)
	scored, stale, fresh := e.card("Scored", "xx"), e.card("Stale", "xx"), e.card("Fresh", "xx")
	e.evaluate(e.instID, scored, stale)
	var c entityOut
	e.call("GET", fmt.Sprintf("/cards/%d", stale), nil, 200, &c)
	e.call("PUT", fmt.Sprintf("/cards/%d", stale), map[string]any{"base_revision_id": c.Revision.ID,
		"data": map[string]any{"name": "Stale", "description": "xxxx"}}, 200, nil)

	j, p := e.evaluate(e.instID, scored, stale, fresh, 999999)
	if j.Status != jobs.StatusSucceeded || p.Total != 4 || p.Done != 2 || p.Failed != 1 || p.Skipped != 1 {
		t.Fatalf("job %s, progress %+v", j.Status, p)
	}
	var vals []enrichmentValue
	e.call("GET", fmt.Sprintf("/cards/%d/enrichments", stale), nil, 200, &vals)
	if len(vals) != 3 || string(vals[0].Value) != "4" || vals[0].Stale {
		t.Fatalf("stale card's values = %+v", vals)
	}

	j, p = e.startRun(map[string]any{"instance_id": e.instID, "card_ids": []int64{scored, fresh}, "rescore": true})
	if j.Status != jobs.StatusSucceeded || p.Total != 2 || p.Done != 2 || p.Skipped != 0 {
		t.Fatalf("rescore: job %s, progress %+v", j.Status, p)
	}
	e.call("GET", fmt.Sprintf("/cards/%d/enrichments?history=true", scored), nil, 200, &vals)
	if len(vals) != 6 {
		t.Fatalf("scored card's history = %+v", vals)
	}

	// A run whose every card is skipped still succeeds.
	j, p = e.evaluate(e.instID, scored, fresh)
	if j.Status != jobs.StatusSucceeded || p.Done != 0 || p.Skipped != 2 {
		t.Fatalf("all skipped: job %s, progress %+v", j.Status, p)
	}
}

func TestEnrichRunKeepsABatchWithAValueTheDatabaseRefuses(t *testing.T) {
	e := newEnrichEnv(t)
	nul, good := e.card("Nul", "xx"), e.card("Good", "xxx")
	j, p := e.evaluate(e.instID, nul, good)
	if j.Status != jobs.StatusSucceeded || p.Done != 2 || p.Failed != 0 {
		t.Fatalf("job %s, progress %+v", j.Status, p)
	}
	var vals []enrichmentValue
	e.call("GET", fmt.Sprintf("/cards/%d/enrichments", nul), nil, 200, &vals)
	if len(vals) != 2 || vals[0].Key != "quality" || string(vals[0].Meta) != "{}" {
		t.Fatalf("nul card's values = %+v", vals)
	}
	e.call("GET", fmt.Sprintf("/cards/%d/enrichments", good), nil, 200, &vals)
	if len(vals) != 3 {
		t.Fatalf("good card's values = %+v", vals)
	}
}

func TestEnrichRunOverAQuery(t *testing.T) {
	e := newEnrichEnv(t)
	a := e.card("Brakka", "x")
	archive := e.createLibrary("Archive")
	var b, c entityResult
	e.call("POST", "/cards", map[string]any{"data": map[string]any{"name": "Carrow", "description": "x"}, "library": archive.ID}, 201, &b)
	e.call("POST", "/cards", map[string]any{"data": map[string]any{"name": "Dellin", "description": "x"}, "library": archive.ID}, 201, &c)
	j, p := e.startRun(map[string]any{"instance_id": e.instID, "query": map[string]any{"library": archive.ID}})
	var payload enrich.Payload
	if err := json.Unmarshal(j.Payload, &payload); err != nil || payload.CardIDs != nil || payload.Query == nil || payload.Query.Library != archive.ID {
		t.Fatalf("payload = %s, %v", j.Payload, err)
	}
	if j.Status != "succeeded" || p.Total != 2 || p.Done != 2 {
		t.Fatalf("job = %s, progress = %+v", j.Status, p)
	}
	for card, want := range map[int64]bool{a: false, b.ID: true, c.ID: true} {
		vals, err := e.s.Lib.Enrichments(context.Background(), card, false)
		if err != nil || (len(vals) > 0) != want {
			t.Errorf("card %d values = %+v, %v; want some: %v", card, vals, err, want)
		}
	}
	e.call("POST", "/enrichments/runs", map[string]any{"instance_id": e.instID, "card_ids": []int64{b.ID, a, b.ID}}, 202, &j)
	if err := json.Unmarshal(j.Payload, &payload); err != nil || !slices.Equal(payload.CardIDs, []int64{a, b.ID}) {
		t.Fatalf("payload with repeats = %s, %v", j.Payload, err)
	}
}

func TestSettingsForgetADeletedScoreInstance(t *testing.T) {
	e := newEnrichEnv(t)
	second, err := e.s.Registry.CreateInstance(context.Background(), plugins.NewInstance{Plugin: "judge", Name: new("Second")})
	if err != nil {
		t.Fatal(err)
	}
	e.call("PATCH", "/settings", map[string]any{"score_instance": second}, 200, nil)
	e.call("DELETE", fmt.Sprintf("/instances/%d", second), nil, 204, nil)
	var s settings
	e.call("GET", "/settings", nil, 200, &s)
	if s.ScoreInstance != nil {
		t.Fatalf("settings = %+v", s)
	}
}

func TestSettingsIgnoreADisabledScoreInstance(t *testing.T) {
	e := newEnrichEnv(t)
	second, err := e.s.Registry.CreateInstance(context.Background(), plugins.NewInstance{Plugin: "judge", Name: new("Second")})
	if err != nil {
		t.Fatal(err)
	}
	e.call("PATCH", "/settings", map[string]any{"score_instance": second}, 200, nil)
	e.call("PATCH", fmt.Sprintf("/instances/%d", second), map[string]any{"disabled": true}, 200, nil)
	var s settings
	e.call("GET", "/settings", nil, 200, &s)
	if s.ScoreInstance != nil {
		t.Fatalf("settings = %+v", s)
	}
}

func TestEnrichRunFailsWhenEveryBatchFails(t *testing.T) {
	e := newEnrichEnv(t)
	j, p := e.evaluate(e.instID, 999998, 999999)
	if j.Status != jobs.StatusFailed || p.Failed != 2 || j.Error == nil || !strings.Contains(*j.Error, "every batch failed") {
		t.Fatalf("job %s %v, progress %+v", j.Status, j.Error, p)
	}
}

func TestStartEnrichRunRejects(t *testing.T) {
	e := newEnrichEnv(t)
	id := e.card("Brakka", "x")
	auth := []string{"Authorization", "Bearer " + e.token}
	for name, body := range map[string]map[string]any{
		"no cards":                 {"instance_id": e.instID, "card_ids": []int64{}},
		"ids and a query":          {"instance_id": e.instID, "card_ids": []int64{id}, "query": map[string]any{}},
		"a query matching nothing": {"instance_id": e.instID, "query": map[string]any{"filter": map[string]any{"name": "nobody"}}},
		"too many cards":           {"instance_id": e.instID, "card_ids": make([]int64, enrich.DefaultMaxCards+1)},
		"not an enricher":          {"instance_id": e.importID, "card_ids": []int64{id}},
	} {
		if r := e.send("POST", "/enrichments/runs", body, auth...); r.Status != 400 && r.Status != 422 {
			t.Errorf("%s: %s", name, r)
		}
	}
	if r := e.send("POST", "/enrichments/runs", map[string]any{"instance_id": e.instID, "card_ids": []int64{id}},
		"Authorization", "Bearer "+e.tokenWith(tier.Read)); r.Status != 403 {
		t.Errorf("read tier: %s", r)
	}
}

func TestStartEnrichRunNeedsConfirmPastThePluginsCardCap(t *testing.T) {
	e := newEnrichEnv(t)
	e.manifests["judge"].EnrichMaxCards = 1
	a, b := e.card("Brakka", "x"), e.card("Carrow", "x")
	auth := []string{"Authorization", "Bearer " + e.token}
	query := map[string]any{"filter": map[string]any{"ids": []int64{a, b}}}
	if r := e.send("POST", "/enrichments/runs", map[string]any{"instance_id": e.instID, "query": query}, auth...); r.Status != 400 {
		t.Errorf("two cards past a cap of one: %s", r)
	}
	if r := e.send("POST", "/enrichments/runs", map[string]any{"instance_id": e.instID, "card_ids": []int64{a}}, auth...); r.Status != 202 {
		t.Errorf("one card within a cap of one: %s", r)
	}
	j, p := e.startRun(map[string]any{"instance_id": e.instID, "query": query, "confirm": true, "rescore": true})
	if j.Status != jobs.StatusSucceeded || p.Total != 2 || p.Done != 2 {
		t.Fatalf("confirmed run past the cap: job %s, progress %+v", j.Status, p)
	}
	type listed struct {
		Name           string `json:"name"`
		EnrichMaxCards int    `json:"enrich_max_cards"`
	}
	var list []listed
	e.call("GET", "/plugins", nil, 200, &list)
	if i := slices.IndexFunc(list, func(pl listed) bool { return pl.Name == "judge" }); i < 0 || list[i].EnrichMaxCards != 1 {
		t.Errorf("plugins = %+v, want judge with enrich_max_cards 1", list)
	}
}

func TestEnrichmentSearchUsesTheScoreInstance(t *testing.T) {
	e := newEnrichEnv(t)
	high := e.card("High", strings.Repeat("x", 90))
	low := e.card("Low", strings.Repeat("x", 20))
	none := e.card("None", "x")
	e.evaluate(e.instID, high, low)

	// The only enrich instance is the score instance.
	var keys enrichmentKeysOut
	e.call("GET", "/enrichments/keys", nil, 200, &keys.Body)
	if keys.Body.InstanceID == nil || *keys.Body.InstanceID != e.instID || len(keys.Body.Instances) != 1 {
		t.Fatalf("keys = %+v", keys.Body)
	}
	if k := keys.Body.Instances[0].Keys; len(k) != 3 || k[0].Key != "quality" || !k[0].Grid || k[1].Key != "summary" {
		t.Fatalf("instance keys = %+v", k)
	}
	ids := func(r library.Results) []int64 {
		out := []int64{}
		for _, h := range r.Hits {
			out = append(out, h.ID)
		}
		return out
	}
	quality := map[string]any{"enrichment": "quality", "gte": 50}
	if got := ids(e.search(map[string]any{"filter": quality}, 200)); !slices.Equal(got, []int64{high}) {
		t.Fatalf("gte 50 = %v", got)
	}
	if got := ids(e.search(map[string]any{"filter": map[string]any{"enrichment": "quality", "missing": true}}, 200)); !slices.Equal(got, []int64{none}) {
		t.Fatalf("missing = %v", got)
	}
	res := e.search(map[string]any{"sort": "enrichment:quality", "include": []string{"enrichments"}}, 200)
	if got := ids(res); !slices.Equal(got, []int64{high, low, none}) {
		t.Fatalf("sorted = %v", got)
	}
	if v := res.Hits[0].Enrichments["quality"]; string(v.Value) != "90" || v.Stale || len(res.Hits[0].Enrichments) != 2 {
		t.Fatalf("included = %+v, want the score keys only", res.Hits[0].Enrichments)
	}

	// A second instance with no setting leaves no score instance.
	second, err := e.s.Registry.CreateInstance(context.Background(), plugins.NewInstance{Plugin: "judge", Name: new("Second")})
	if err != nil {
		t.Fatal(err)
	}
	e.search(map[string]any{"filter": quality}, 400)
	e.search(map[string]any{"sort": "enrichment:quality"}, 400)
	e.call("GET", "/enrichments/keys", nil, 200, &keys.Body)
	if keys.Body.InstanceID != nil || len(keys.Body.Instances) != 2 || keys.Body.Instances[1].ID != second || len(keys.Body.Instances[1].Keys) != 3 {
		t.Fatalf("keys without a score instance = %+v", keys.Body)
	}
	// A filter or sort that names its instance needs no score instance.
	named := map[string]any{"enrichment": "quality", "gte": 50, "instance": e.instID}
	if got := ids(e.search(map[string]any{"filter": named, "sort": fmt.Sprintf("enrichment:%d:quality", e.instID)}, 200)); !slices.Equal(got, []int64{high}) {
		t.Fatalf("named instance without a score instance = %v", got)
	}
	// Bulk selections use the same rule.
	e.call("POST", "/bulk", map[string]any{"action": "tag", "tags": []string{"good"}, "query": map[string]any{"filter": quality}}, 400, nil)

	// The second instance has no values, so its filter matches nothing.
	var s settings
	e.call("PATCH", "/settings", map[string]any{"score_instance": second}, 200, &s)
	if s.ScoreInstance == nil || *s.ScoreInstance != second {
		t.Fatalf("settings = %+v", s)
	}
	if got := ids(e.search(map[string]any{"filter": quality}, 200)); len(got) != 0 {
		t.Fatalf("second instance's gte 50 = %v", got)
	}
	e.call("PATCH", "/settings", map[string]any{"score_instance": e.instID}, 200, &s)
	if got := ids(e.search(map[string]any{"filter": quality}, 200)); !slices.Equal(got, []int64{high}) {
		t.Fatalf("first instance's gte 50 = %v", got)
	}
	e.call("PATCH", "/settings", map[string]any{"score_instance": e.importID}, 400, nil)
	e.markBroken(e.importID, "boom")
	e.call("PATCH", "/settings", map[string]any{"score_instance": e.importID}, 400, nil)
	e.call("PATCH", "/settings", map[string]any{"score_instance": 999999}, 404, nil)
	e.call("PATCH", "/settings", map[string]any{"theme": ""}, 200, &s)
	if s.ScoreInstance == nil || *s.ScoreInstance != e.instID {
		t.Fatalf("a patch without score_instance changed it: %+v", s)
	}
	e.call("PATCH", "/settings", map[string]any{"score_instance": nil}, 200, &s)
	if s.ScoreInstance != nil {
		t.Fatalf("after null: %+v", s)
	}

	// search_cards over MCP takes the same filters.
	e.call("PATCH", "/settings", map[string]any{"score_instance": e.instID}, 200, nil)
	text := tool(t, e.mcpClient(tier.Read), "search_cards", map[string]any{"query": map[string]any{"filter": quality}}, false)
	var mres library.Results
	if err := json.Unmarshal([]byte(text), &mres); err != nil || !slices.Equal(ids(mres), []int64{high}) {
		t.Fatalf("search_cards = %s, %v", text, err)
	}
}

func TestMCPStartJobEvaluates(t *testing.T) {
	e := newEnrichEnv(t)
	brakka := e.card("Brakka", strings.Repeat("x", 78))
	e.card("Other", strings.Repeat("x", 78))
	s := e.mcpClient(tier.Write)
	run := func(body map[string]any) enrich.Progress {
		t.Helper()
		var j jobOut
		_ = json.Unmarshal([]byte(tool(t, s, "start_job", map[string]any{"operation": "start-enrich-run", "body": body}, false)), &j)
		_, p := e.waitRun(j.ID)
		return p
	}
	if p := run(map[string]any{"instance_id": e.instID, "query": map[string]any{"text": "Brakka"}}); p.Total != 1 || p.Done != 1 {
		t.Fatalf("query run: %+v", p)
	}
	if p := run(map[string]any{"instance_id": e.instID, "card_ids": []int64{brakka}, "rescore": true}); p.Done != 1 {
		t.Fatalf("rescore run: %+v", p)
	}
}
