package api

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/inkandale/innkeeper/internal/embed"
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
	rn := &enrich.Runner{Lib: e.s.Lib, Registry: e.s.Registry, Plugins: mgr, Runs: runs, Queue: e.s.Queue, Log: quiet}
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

func TestEnrichRunQueuesEmbedOnlyForEmbedKeys(t *testing.T) {
	e := newEnrichEnv(t)
	queued := func() int {
		var n int
		if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM job WHERE kind = $1 AND status = 'queued'`, embed.Kind).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	card := e.card("Brakka", "A dwarf smith.")
	if e.evaluate(e.instID, card); queued() != 0 {
		t.Fatal("a run with no embed key queued an embed job")
	}
	summary := e.manifests["judge"].Enrichments["summary"]
	summary.Embed = true
	e.manifests["judge"].Enrichments["summary"] = summary
	if e.startRun(map[string]any{"instance_id": e.instID, "card_ids": []int64{card}, "rescore": true}); queued() != 1 {
		t.Fatal("a run that stored an embed key's value queued no embed job")
	}
}

func TestEnrichRunPassesThoroughToThePlugin(t *testing.T) {
	e := newEnrichEnv(t)
	id := e.card("Brakka", "x")
	if j, _ := e.startRun(map[string]any{"instance_id": e.instID, "card_ids": []int64{id}, "thorough": true}); j.Status != jobs.StatusSucceeded {
		t.Fatalf("job %s", j.Status)
	}
	var vals []enrichmentValue
	e.call("GET", fmt.Sprintf("/cards/%d/enrichments", id), nil, 200, &vals)
	if len(vals) == 0 || string(vals[0].Meta) != `{"model":"fake-thorough"}` {
		t.Fatalf("values = %+v", vals)
	}
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
	j, _ = e.evaluate(e.instID, good)
	var lines []jobs.LogLine
	e.call("GET", fmt.Sprintf("/jobs/%d/logs", j.ID), nil, 200, &lines)
	for _, want := range []string{"Scoring Brakka", "Scored Brakka: Quality 90 (was 78)"} {
		if !slices.ContainsFunc(lines, func(l jobs.LogLine) bool { return l.Level == "info" && l.Message == want }) {
			t.Errorf("no log line %q in %+v", want, lines)
		}
	}
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

func TestEnrichRunSkipsArchivedCards(t *testing.T) {
	e := newEnrichEnv(t)
	live, archived := e.card("Live", "xx"), e.card("Archived", "xx")
	e.call("PUT", fmt.Sprintf("/cards/%d/archived", archived), map[string]any{"archived": true}, 204, nil)

	j, p := e.evaluate(e.instID, live, archived)
	if j.Status != jobs.StatusSucceeded || p.Done != 1 || p.Skipped != 1 || p.Failed != 0 {
		t.Fatalf("job %s, progress %+v", j.Status, p)
	}
	var vals []enrichmentValue
	e.call("GET", fmt.Sprintf("/cards/%d/enrichments", archived), nil, 200, &vals)
	if len(vals) != 0 {
		t.Fatalf("archived card got values %+v", vals)
	}

	// A run of only archived cards succeeds with nothing sent, and so does a
	// query that asks for archived cards.
	j, p = e.evaluate(e.instID, archived)
	if j.Status != jobs.StatusSucceeded || p.Done != 0 || p.Skipped != 1 {
		t.Fatalf("only archived: job %s, progress %+v", j.Status, p)
	}
	j, p = e.startRun(map[string]any{"instance_id": e.instID, "query": map[string]any{"filter": map[string]any{"archived": true}}})
	if j.Status != jobs.StatusSucceeded || p.Done != 0 || p.Total != 1 || p.Skipped != 1 {
		t.Fatalf("archived query: job %s, progress %+v", j.Status, p)
	}
	j, p = e.startRun(map[string]any{"instance_id": e.instID, "card_ids": []int64{archived}, "rescore": true})
	if j.Status != jobs.StatusSucceeded || p.Done != 0 || p.Skipped != 1 {
		t.Fatalf("archived rescore: job %s, progress %+v", j.Status, p)
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

func TestEnrichmentKeysSayWhetherAnInstanceScoredAnything(t *testing.T) {
	e := newEnrichEnv(t)
	card := e.card("Card", "x")
	var keys enrichmentKeysOut
	e.call("GET", "/enrichments/keys", nil, 200, &keys.Body)
	if len(keys.Body.Instances) != 1 || keys.Body.Instances[0].Scored {
		t.Fatalf("before a run: %+v", keys.Body.Instances)
	}
	e.evaluate(e.instID, card)
	e.call("GET", "/enrichments/keys", nil, 200, &keys.Body)
	if !keys.Body.Instances[0].Scored {
		t.Fatalf("after a run: %+v", keys.Body.Instances)
	}
}

func TestGridScoreReadsTheHighestPriorityScorer(t *testing.T) {
	e := newEnrichEnv(t)
	high := e.card("High", strings.Repeat("x", 90))
	low := e.card("Low", strings.Repeat("x", 20))
	none := e.card("None", "x")
	e.evaluate(e.instID, high, low)

	var keys enrichmentKeysOut
	e.call("GET", "/enrichments/keys", nil, 200, &keys.Body)
	if !slices.Equal(keys.Body.Scorers, []library.Scorer{{Instance: e.instID, Key: "quality"}}) || len(keys.Body.Instances) != 1 {
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
	res := e.search(map[string]any{"sort": "enrichment:quality", "include": []string{"score"}}, 200)
	if got := ids(res); !slices.Equal(got, []int64{high, low, none}) {
		t.Fatalf("sorted = %v", got)
	}
	if v := res.Hits[0].Score; v == nil || v.Instance != e.instID || v.Key != "quality" || string(v.Value) != "90" || v.Stale {
		t.Fatalf("included = %+v", v)
	}
	// A key naming no instance must be a grid key.
	e.search(map[string]any{"filter": map[string]any{"enrichment": "voice", "gte": 1}}, 400)

	// A higher-priority scorer goes first, and wins for the cards it scored.
	critic := *e.manifests["judge"]
	critic.Name, critic.Command = "critic", []string{critic.Command[0], "-caps=enrich", "-name=critic"}
	critic.Enrichments = maps.Clone(critic.Enrichments)
	grid := critic.Enrichments["quality"]
	grid.Priority = 200
	critic.Enrichments["quality"] = grid
	e.manifests["critic"] = &critic
	second, err := e.s.Registry.CreateInstance(context.Background(), plugins.NewInstance{Plugin: "critic", Name: new("Critic")})
	if err != nil {
		t.Fatal(err)
	}
	e.evaluate(second, low)
	e.call("GET", "/enrichments/keys", nil, 200, &keys.Body)
	if !slices.Equal(keys.Body.Scorers, []library.Scorer{{Instance: second, Key: "quality"}, {Instance: e.instID, Key: "quality"}}) {
		t.Fatalf("scorers = %+v", keys.Body.Scorers)
	}
	res = e.search(map[string]any{"sort": "enrichment:quality", "include": []string{"score"}}, 200)
	if got := ids(res); !slices.Equal(got, []int64{high, low, none}) || res.Hits[0].Score.Instance != e.instID || res.Hits[1].Score.Instance != second {
		t.Fatalf("ranked = %v, %+v, %+v", got, res.Hits[0].Score, res.Hits[1].Score)
	}
	// A filter or sort that names its instance reads only that instance.
	named := map[string]any{"enrichment": "quality", "gte": 1, "instance": second}
	if got := ids(e.search(map[string]any{"filter": named, "sort": fmt.Sprintf("enrichment:%d:quality", second)}, 200)); !slices.Equal(got, []int64{low}) {
		t.Fatalf("named instance = %v", got)
	}
	// A disabled instance scores nothing.
	e.call("PATCH", fmt.Sprintf("/instances/%d", second), map[string]any{"disabled": true}, 200, nil)
	e.call("GET", "/enrichments/keys", nil, 200, &keys.Body)
	if !slices.Equal(keys.Body.Scorers, []library.Scorer{{Instance: e.instID, Key: "quality"}}) {
		t.Fatalf("scorers with one disabled = %+v", keys.Body.Scorers)
	}

	// search_cards over MCP takes the same filters.
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
