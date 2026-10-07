package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/davidgibbons/innkeeper/internal/merge"
	"github.com/davidgibbons/innkeeper/internal/targets"
)

func TestPrintSnapshot(t *testing.T) {
	var b strings.Builder
	err := printSnapshot(&b, targets.Snapshot{
		Items: []targets.ReconcileItem{{RemoteID: "Brakka.png", Name: "Brakka", Verdict: "likely",
			Candidates: []targets.Candidate{{CardID: 12, Name: "Brakka"}}, Suggested: &targets.Option{Action: "bind", CardID: 12}}},
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
	printComparison(&b, targets.Comparison{RemoteID: "r1", CardID: 12, Fields: []merge.Field{{Path: "first_mes", Hunks: []merge.Hunk{
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

// stubAPI answers GET with the reply for its path and records every request.
func stubAPI(t *testing.T, replies map[string]string) *[]string {
	var got []string
	srv := httptest.NewServer(http.StripPrefix("/api", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got = append(got, r.Method+" "+r.URL.RequestURI()+" "+string(body))
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"id": 9}`))
			return
		}
		_, _ = w.Write([]byte(replies[r.URL.RequestURI()]))
	})))
	t.Cleanup(srv.Close)
	t.Setenv("INNKEEPER_URL", srv.URL)
	t.Setenv("INNKEEPER_TOKEN", "tok")
	return &got
}

func TestResolveCmd(t *testing.T) {
	got := stubAPI(t, map[string]string{
		"/instances/3/items/a%2Fb.png/compare?card_id=12": `{"remote_id": "a/b.png", "card_id": 12, "head_revision_id": 40, "accepted_hash": "", "app_hash": "h1", "binding_id": 7}`,
	})
	if err := resolveCmd([]string{"3", "a/b.png", "bind", "12", "first_mes#1=app", "--default=keep_in_app"}); err != nil {
		t.Fatal(err)
	}
	if len(*got) != 2 {
		t.Fatalf("requests = %q", *got)
	}
	post := (*got)[1]
	if !strings.HasPrefix(post, "POST /instances/3/items/a%2Fb.png/resolve ") {
		t.Fatalf("post = %q", post)
	}
	var r targets.Resolution
	if err := json.Unmarshal([]byte(strings.SplitN(post, " ", 3)[2]), &r); err != nil {
		t.Fatal(err)
	}
	if r.AppHash != "h1" || r.BaseRevisionID != 40 || r.CardID != 12 || r.Action != "bind" ||
		r.BindingID != 7 || r.Choices["first_mes#1"] != merge.ChooseApp || r.Default != merge.ChooseKeepInApp {
		t.Fatalf("resolution = %+v", r)
	}
	for _, args := range [][]string{{"3", "x"}, {"3", "x", "bind", "y"}, {"3", "x", "bind", "--force"}} {
		if err := resolveCmd(args); err == nil {
			t.Errorf("resolve %q succeeded", args)
		}
	}
}

func TestResolveSuggested(t *testing.T) {
	snap, _ := json.Marshal(targets.Snapshot{Params: targets.ReconcileParams{InstanceID: 3}, Items: []targets.ReconcileItem{
		{RemoteID: "r1", AppHash: "h1", Verdict: "exact", BindingID: 7, Suggested: &targets.Option{Action: "bind", CardID: 12}},
		{RemoteID: "r2", AppHash: "h2", Verdict: "new", Suggested: &targets.Option{Action: "import"}},
	}})
	job, _ := json.Marshal(map[string]any{"kind": targets.ReconcileKind, "status": "succeeded", "progress": json.RawMessage(snap)})
	got := stubAPI(t, map[string]string{
		"/jobs/5": string(job),
		"/instances/3/items/r1/compare?card_id=12": `{"head_revision_id": 40, "accepted_hash": "a1", "app_hash": "h1-newer"}`,
	})
	if err := resolveCmd([]string{"--suggested", "5", "--verdict=exact"}); err != nil {
		t.Fatal(err)
	}
	post := (*got)[len(*got)-1]
	want := `POST /resolutions {"resolutions":[{"instance_id":3,"remote_id":"r1","app_hash":"h1","base_revision_id":40,"accepted_hash":"a1","binding_id":7,"action":"bind","card_id":12,"kind":"card"}]}`
	if post != want {
		t.Fatalf("post = %s\nwant   %s", post, want)
	}
	if err := resolveCmd([]string{"--suggested", "5", "r9"}); err == nil {
		t.Error("resolving a remote ID the snapshot lacks succeeded")
	}
}
