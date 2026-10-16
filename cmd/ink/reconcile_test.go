package main

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/inkandale/innkeeper/internal/merge"
	"github.com/inkandale/innkeeper/internal/wire"
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
	if lines[3] != "0 applied, 1 left to review" {
		t.Errorf("summary = %q", lines[3])
	}
}

func TestPrintSnapshotAuto(t *testing.T) {
	var b strings.Builder
	err := printSnapshot(&b, wire.Snapshot{Items: []wire.ReconcileItem{
		{RemoteID: "a", Verdict: "exact", AutoBound: true},
		{RemoteID: "b", Verdict: "new", AutoImported: true, Lorebooks: []wire.ReconcileItem{{RemoteID: "lb", Verdict: "new"}}},
		{RemoteID: "c", Verdict: "bound"},
		{RemoteID: "e", Verdict: "bound", Suggested: &wire.Option{Action: "bind", CardID: 1}},
		{RemoteID: "d", Verdict: "likely"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{"linked (auto)", "imported (auto)", "2 applied, 3 left to review"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
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
		"/instances/3/cards/a%2Fb.png/compare?card_id=12": `{"remote_id": "a/b.png", "card_id": 12, "head_revision_id": 40, "accepted_hash": "", "app_hash": "h1", "binding_id": 7}`,
	})
	if _, err := runInk(t, "sync", "resolve", "3", "a/b.png", "bind", "12", "first_mes#1=app", "--default", "keep_in_app"); err != nil {
		t.Fatal(err)
	}
	if len(*got) != 2 {
		t.Fatalf("requests = %q", *got)
	}
	post := (*got)[1]
	if !strings.HasPrefix(post, "POST /instances/3/cards/a%2Fb.png/resolve ") {
		t.Fatalf("post = %q", post)
	}
	var r wire.Resolution
	if err := json.Unmarshal([]byte(strings.SplitN(post, " ", 3)[2]), &r); err != nil {
		t.Fatal(err)
	}
	if r.AppHash != "h1" || r.BaseRevisionID != 40 || r.CardID != 12 || r.Action != "bind" ||
		r.BindingID != 7 || r.Choices["first_mes#1"] != merge.ChooseApp || r.Default != merge.ChooseKeepInApp {
		t.Fatalf("resolution = %+v", r)
	}
	for _, args := range [][]string{{"3", "x"}, {"3", "x", "bind", "y"}, {"3", "x", "bind", "--force"}} {
		if _, err := runInk(t, append([]string{"sync", "resolve"}, args...)...); err == nil {
			t.Errorf("resolve %q succeeded", args)
		}
	}
}

func TestResolveTwoWay(t *testing.T) {
	got := stubAPI(t, map[string]string{
		"/instances/3/cards/r1/compare?base=none": `{"remote_id": "r1", "card_id": 12, "head_revision_id": 40, "accepted_hash": "acc", "base": "none", "app_hash": "h1"}`,
	})
	if _, err := runInk(t, "sync", "resolve", "3", "r1", "bind", "description#0=app", "--two-way"); err != nil {
		t.Fatal(err)
	}
	var r wire.Resolution
	if err := json.Unmarshal([]byte(strings.SplitN((*got)[1], " ", 3)[2]), &r); err != nil {
		t.Fatal(err)
	}
	if r.Base != "none" || r.AcceptedHash != "acc" {
		t.Fatalf("resolution = %+v", r)
	}
}

func TestResolveRemoteIDWithEquals(t *testing.T) {
	got := stubAPI(t, nil)
	if _, err := runInk(t, "sync", "resolve", "3", "a=b.png", "skip"); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix((*got)[0], "POST /instances/3/cards/a=b.png/resolve ") {
		t.Fatalf("request = %q", (*got)[0])
	}
}

func TestSyncApply(t *testing.T) {
	snap, _ := json.Marshal(wire.Snapshot{Params: wire.ReconcileParams{InstanceID: 3}, Items: []wire.ReconcileItem{
		{RemoteID: "r1", AppHash: "h1", Verdict: "exact", BindingID: 7, Suggested: &wire.Option{Action: "bind", CardID: 12}},
		{RemoteID: "r2", AppHash: "h2", Verdict: "new", Suggested: &wire.Option{Action: "import"}},
	}})
	job, _ := json.Marshal(map[string]any{"kind": wire.ReconcileKind, "status": "succeeded", "progress": json.RawMessage(snap)})
	got := stubAPI(t, map[string]string{
		"/jobs/5": string(job),
		"/instances/3/cards/r1/compare?card_id=12": `{"head_revision_id": 40, "accepted_hash": "a1", "app_hash": "h1-newer"}`,
	})
	if _, err := runInk(t, "sync", "apply", "5", "--verdict", "exact"); err != nil {
		t.Fatal(err)
	}
	post := (*got)[len(*got)-1]
	want := `POST /resolutions {"resolutions":[{"instance_id":3,"remote_id":"r1","app_hash":"h1","base_revision_id":40,"accepted_hash":"a1","binding_id":7,"action":"bind","card_id":12,"kind":"card"}]}`
	if post != want {
		t.Fatalf("post = %s\nwant   %s", post, want)
	}
	if _, err := runInk(t, "sync", "apply", "5", "r9"); err == nil {
		t.Error("applying a remote ID the snapshot lacks succeeded")
	}
}

func TestSyncRun(t *testing.T) {
	snap, _ := json.Marshal(wire.Snapshot{Items: []wire.ReconcileItem{{RemoteID: "r1", Name: "Brakka", Verdict: "new"}}, Totals: map[string]int{"new": 1}})
	job, _ := json.Marshal(map[string]any{"kind": wire.ReconcileKind, "status": "succeeded", "progress": json.RawMessage(snap)})
	got := stubAPI(t, map[string]string{
		"/jobs/9/events": "event: job\ndata: {\"status\": \"succeeded\"}\n\n",
		"/jobs/9":        string(job),
	})
	out, err := runInk(t, "sync", "run", "3")
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
	if _, err := runInk(t, "sync", "run", "3", "null"); err != nil {
		t.Errorf("null params: %v", err)
	}
	if _, err := runInk(t, "sync", "reconcile", "3"); err != nil {
		t.Errorf("reconcile alias: %v", err)
	}
	if _, err := runInk(t, "sync", "run", "3", `{"apply":"obvious"}`, "--dry-run"); err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(*got, func(r string) bool {
		return strings.HasPrefix(r, "POST /reconciles") && strings.Contains(r, `"apply":"none"`)
	}) {
		t.Errorf("no dry-run request in %q", *got)
	}
}

func TestSyncRunEveryApp(t *testing.T) {
	got := stubAPI(t, map[string]string{
		"/jobs/9/events": "event: job\ndata: {\"status\": \"succeeded\"}\n\n",
		"/jobs/9": `{"id": 9, "kind": "target.sync", "status": "succeeded",
			"progress": {"apps": [{"instance_id": 2, "job_id": 11, "totals": {"likely": 1, "auto_imported": 3}},
				{"instance_id": 4, "job_id": 12, "error": "app is down"}]}}`,
	})
	out, err := runInk(t, "sync", "run")
	if err != nil {
		t.Fatal(err)
	}
	if (*got)[0] != `POST /syncs {}` {
		t.Errorf("request = %s", (*got)[0])
	}
	for _, want := range []string{"instance 2: auto_imported=3 likely=1 (review: ink sync apply 11)", "instance 4: error: app is down"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if _, err := runInk(t, "sync", "run", "--dry-run"); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(*got, `POST /syncs {"apply":"none"}`) {
		t.Errorf("no dry-run request in %q", *got)
	}
}

func TestSyncRefreshAndAutosync(t *testing.T) {
	got := stubAPI(t, map[string]string{"PATCH /bindings/5": bindingJSON})
	if _, err := runInk(t, "sync", "refresh"); err != nil {
		t.Fatal(err)
	}
	out, err := runInk(t, "sync", "autosync", "5", "on")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "autosync") {
		t.Errorf("output lacks autosync:\n%s", out)
	}
	if _, err := runInk(t, "sync", "autosync", "5", "maybe"); err == nil {
		t.Error("autosync maybe succeeded")
	}
	want := []string{"POST /scans ", `PATCH /bindings/5 {"autosync":true}`}
	if !slices.Equal(*got, want) {
		t.Errorf("requests = %q, want %q", *got, want)
	}
}

func TestSyncChanges(t *testing.T) {
	got := stubAPI(t, map[string]string{
		"/sync/changes?instance_id=3&state=both_changed": `{"rows": [{"card_id": 12, "name": "Brakka",
			"apps": [{"instance_id": 3, "state": "both_changed"}, {"instance_id": 4, "state": "ahead"}]}], "counts": {"3": {"both_changed": 1}}}`,
	})
	out, err := runInk(t, "sync", "changes", "--app", "3", "--state", "both_changed")
	if err != nil {
		t.Fatal(err)
	}
	if want := "GET /sync/changes?instance_id=3&state=both_changed "; len(*got) != 1 || (*got)[0] != want {
		t.Fatalf("requests = %q", *got)
	}
	if !strings.Contains(out, "Brakka  #3: changed in both, #4: not pushed") {
		t.Errorf("out = %q", out)
	}
}
