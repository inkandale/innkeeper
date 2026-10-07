package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

func TestImport(t *testing.T) {
	got := stubAPI(t, nil)
	out, err := runInk(t, "import", "4", `{"path": "fantasy"}`)
	if err != nil || out != "started job 9; follow it with: ink jobs watch 9\n" {
		t.Fatalf("out = %q, err = %v", out, err)
	}
	if want := `POST /imports {"instance_id":4,"params":{"path":"fantasy"}}`; (*got)[0] != want {
		t.Fatalf("request = %s, want %s", (*got)[0], want)
	}
	if out, err := runInk(t, "import", "4", "--json"); err != nil || out != `{"id": 9}`+"\n" {
		t.Fatalf("--json out = %q, err = %v", out, err)
	}
}

func TestJobs(t *testing.T) {
	row := `{"id": 9, "kind": "import", "status": "failed", "created_at": "2026-10-07T10:00:00Z", "error": "boom"}`
	got := stubAPI(t, map[string]string{"/jobs": "[" + row + "]", "/jobs/9": row})
	out, err := runInk(t, "jobs")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(out, "\n")
	if f := strings.Fields(lines[0]); !slices.Equal(f, []string{"ID", "KIND", "STATUS", "CREATED", "ERROR"}) {
		t.Errorf("header = %q", lines[0])
	}
	if f := strings.Fields(lines[1]); len(f) != 6 || f[0] != "9" || f[2] != "failed" || f[5] != "boom" {
		t.Errorf("row = %q", lines[1])
	}
	if out, _ := runInk(t, "jobs", "show", "9"); !strings.Contains(out, "boom") {
		t.Errorf("show = %q", out)
	}
	if out, _ := runInk(t, "jobs", "--json"); out != "["+row+"]\n" {
		t.Errorf("--json = %q", out)
	}
	if _, err := runInk(t, "jobs", "cancel", "9"); err != nil {
		t.Fatal(err)
	}
	if last := (*got)[len(*got)-1]; last != "POST /jobs/9/cancel " {
		t.Errorf("cancel sent %q", last)
	}
	for _, args := range [][]string{{"jobs", "bogus", "1"}, {"jobs", "show"}, {"jobs", "show", "x"}} {
		if _, err := runInk(t, args...); err == nil {
			t.Errorf("%q succeeded", args)
		}
	}
}

func TestWatchJob(t *testing.T) {
	for _, tc := range []struct{ stream, want string }{
		{"event: job\ndata: {\"status\": \"running\", \"progress\": {}}\n\n: ping\n\n", "running"},
		{"event: job\ndata: {\"status\": \"failed\"}\n\n", "job failed"},
		{"event: job\ndata: {\"status\": \"succeeded\"}\n\n", ""},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, tc.stream)
		}))
		err := watchJob(context.Background(), &client{base: srv.URL, token: "tok"}, "1", io.Discard)
		srv.Close()
		if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
			t.Fatalf("stream %q: err = %v, want %q", tc.stream, err, tc.want)
		}
	}
}
