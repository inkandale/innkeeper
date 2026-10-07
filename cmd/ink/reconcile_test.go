package main

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/davidgibbons/innkeeper/internal/merge"
	"github.com/davidgibbons/innkeeper/internal/wire"
)

func TestPrintSnapshot(t *testing.T) {
	var b strings.Builder
	err := printSnapshot(&b, wire.Snapshot{
		Items: []wire.ReconcileItem{{RemoteID: "Brakka.png", Name: "Brakka", Verdict: "likely",
			Candidates: []wire.Candidate{{CardID: 12, Name: "Brakka"}}, Suggested: &wire.Option{Action: "bind", CardID: 12}}},
		Totals: map[string]int{"likely": 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(b.String(), "\n")
	if got := strings.Fields(lines[0]); !slices.Equal(got, []string{"KIND", "VERDICT", "REMOTE", "ID", "NAME", "TOP", "CANDIDATE", "SUGGESTED"}) {
		t.Errorf("header = %q", lines[0])
	}
	if got := strings.Fields(lines[1]); !slices.Equal(got, []string{"card", "likely", "Brakka.png", "Brakka", "Brakka", "(12)", "bind", "12"}) {
		t.Errorf("row = %q", lines[1])
	}
	if lines[2] != "totals: likely 1" {
		t.Errorf("totals = %q", lines[2])
	}
}

func TestPrintComparison(t *testing.T) {
	var b strings.Builder
	printComparison(&b, wire.Comparison{RemoteID: "r1", CardID: 12, Fields: []merge.Field{{Path: "first_mes", Hunks: []merge.Hunk{
		{ID: "first_mes#0", Status: merge.Same, Library: json.RawMessage(`"Hi."`), App: json.RawMessage(`"Hi."`)},
		{ID: "first_mes#1", Status: merge.Conflict, Library: json.RawMessage(`"Hail, traveler."`), App: json.RawMessage(`"Well met, traveler."`),
			Words: []merge.Span{{Side: "library", Start: 0, End: 4}, {Side: "app", Start: 0, End: 8}}},
	}}}})
	out := b.String()
	for _, want := range []string{"first_mes#1  conflict", "library: [-Hail-], traveler.", "app:     {+Well met+}, traveler."} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "first_mes#0") {
		t.Errorf("same hunk printed:\n%s", out)
	}
}

func TestResolveCmd(t *testing.T) {
	got := stubAPI(t, map[string]string{
		"/instances/3/items/a%2Fb.png/compare?card_id=12": `{"remote_id": "a/b.png", "card_id": 12, "head_revision_id": 40, "accepted_hash": "", "app_hash": "h1"}`,
	})
	if _, err := runInk(t, "sync", "resolve", "3", "a/b.png", "bind", "12", "first_mes#1=app", "--default", "keep_in_app"); err != nil {
		t.Fatal(err)
	}
	if len(*got) != 2 {
		t.Fatalf("requests = %q", *got)
	}
	post := (*got)[1]
	if !strings.HasPrefix(post, "POST /instances/3/items/a%2Fb.png/resolve ") {
		t.Fatalf("post = %q", post)
	}
	var r wire.Resolution
	if err := json.Unmarshal([]byte(strings.SplitN(post, " ", 3)[2]), &r); err != nil {
		t.Fatal(err)
	}
	if r.AppHash != "h1" || r.BaseRevisionID != 40 || r.CardID != 12 || r.Action != "bind" ||
		r.Choices["first_mes#1"] != merge.ChooseApp || r.Default != merge.ChooseKeepInApp {
		t.Fatalf("resolution = %+v", r)
	}
	for _, args := range [][]string{{"3", "x"}, {"3", "x", "bind", "y"}, {"3", "x", "bind", "--force"}} {
		if _, err := runInk(t, append([]string{"sync", "resolve"}, args...)...); err == nil {
			t.Errorf("resolve %q succeeded", args)
		}
	}
}

func TestSyncApply(t *testing.T) {
	snap, _ := json.Marshal(wire.Snapshot{Params: wire.ReconcileParams{InstanceID: 3}, Items: []wire.ReconcileItem{
		{RemoteID: "r1", AppHash: "h1", Verdict: "exact", Suggested: &wire.Option{Action: "bind", CardID: 12}},
		{RemoteID: "r2", AppHash: "h2", Verdict: "new", Suggested: &wire.Option{Action: "import"}},
	}})
	job, _ := json.Marshal(map[string]any{"kind": wire.ReconcileKind, "status": "succeeded", "progress": json.RawMessage(snap)})
	got := stubAPI(t, map[string]string{
		"/jobs/5": string(job),
		"/instances/3/items/r1/compare?card_id=12": `{"head_revision_id": 40, "accepted_hash": "a1", "app_hash": "h1-newer"}`,
	})
	if _, err := runInk(t, "sync", "apply", "5", "--verdict", "exact"); err != nil {
		t.Fatal(err)
	}
	post := (*got)[len(*got)-1]
	want := `POST /resolutions {"resolutions":[{"instance_id":3,"remote_id":"r1","app_hash":"h1","base_revision_id":40,"accepted_hash":"a1","action":"bind","card_id":12,"kind":"card"}]}`
	if post != want {
		t.Fatalf("post = %s\nwant   %s", post, want)
	}
	if _, err := runInk(t, "sync", "apply", "5", "r9"); err == nil {
		t.Error("applying a remote ID the snapshot lacks succeeded")
	}
}

func TestSyncReconcile(t *testing.T) {
	snap, _ := json.Marshal(wire.Snapshot{Items: []wire.ReconcileItem{{RemoteID: "r1", Name: "Brakka", Verdict: "new"}}, Totals: map[string]int{"new": 1}})
	job, _ := json.Marshal(map[string]any{"kind": wire.ReconcileKind, "status": "succeeded", "progress": json.RawMessage(snap)})
	got := stubAPI(t, map[string]string{
		"/jobs/9/events": "event: job\ndata: {\"status\": \"succeeded\"}\n\n",
		"/jobs/9":        string(job),
	})
	out, err := runInk(t, "sync", "reconcile", "3")
	if err != nil {
		t.Fatal(err)
	}
	if (*got)[0] != `POST /reconciles {"instance_id":3}` {
		t.Errorf("request = %s", (*got)[0])
	}
	for _, want := range []string{"ink sync apply 9", "card  new", "totals: new 1"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if _, err := runInk(t, "sync", "reconcile", "3", "null"); err != nil {
		t.Errorf("null params: %v", err)
	}
}
