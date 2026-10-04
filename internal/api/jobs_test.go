package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/davidgibbons/innkeeper/internal/bulk"
	"github.com/davidgibbons/innkeeper/internal/jobs"
	"github.com/davidgibbons/innkeeper/internal/library"
	"github.com/davidgibbons/innkeeper/internal/plugins"
)

func TestImportAndCancel(t *testing.T) {
	e := newEnv(t)
	var j jobOut
	e.call("POST", "/imports", map[string]any{"instance_id": e.importID, "params": map[string]int{"cards": 2}}, 202, &j)
	if j.Kind != "import" || j.Status != "queued" || *j.InstanceID != e.importID || !strings.Contains(string(j.Payload), `"cards"`) {
		t.Fatalf("import job = %+v", j)
	}
	var list []jobOut
	e.call("GET", "/jobs", nil, 200, &list)
	if len(list) != 1 || list[0].ID != j.ID {
		t.Fatalf("jobs = %+v", list)
	}
	e.call("GET", "/jobs?status=failed", nil, 200, &list)
	if len(list) != 0 {
		t.Fatalf("failed jobs = %+v", list)
	}

	path := fmt.Sprintf("/jobs/%d", j.ID)
	e.call("POST", path+"/cancel", nil, 200, &j)
	if j.Status != "failed" || *j.Error != "cancelled" {
		t.Fatalf("cancelled = %+v", j)
	}
	e.call("POST", path+"/cancel", nil, 409, nil)
	e.call("GET", "/jobs/999", nil, 404, nil)
	e.call("GET", "/jobs/999/events", nil, 404, nil)

	vault, err := e.s.Registry.CreateInstance(context.Background(), plugins.NewInstance{Plugin: "vault", Name: "Vault"})
	if err != nil {
		t.Fatal(err)
	}
	e.call("POST", "/imports", map[string]any{"instance_id": vault}, 400, nil)
	e.call("POST", "/imports", map[string]any{"instance_id": 999}, 404, nil)
}

func TestBulkArchiveAndWatch(t *testing.T) {
	e := newEnv(t)
	for _, name := range []string{"A", "B", "C"} {
		c := e.createCard(fmt.Sprintf(`{"name": %q}`, name))
		if name != "C" {
			e.call("PUT", fmt.Sprintf("/cards/%d/tags", c.ID), map[string]any{"tags": []string{"old"}}, 200, nil)
		}
	}
	w := &jobs.Worker{Queue: e.s.Queue, Log: quiet, Poll: 50 * time.Millisecond,
		Handlers: map[string]jobs.Handler{bulk.ArchiveKind: bulk.Archive(e.s.Lib)}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })

	var j jobOut
	e.call("POST", "/bulk", map[string]any{"query": map[string]any{"filter": map[string]any{"tag": "old"}}, "action": "archive"}, 202, &j)
	total := 0
	for _, n := range j.Children {
		total += n
	}
	if j.Kind != bulk.Kind || total != 2 {
		t.Fatalf("bulk job = %+v", j)
	}

	// The stream ends when the job finishes; the last event shows how.
	r := e.call("GET", fmt.Sprintf("/jobs/%d/events", j.ID), nil, 200, nil)
	if r.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("events content type %q", r.Header.Get("Content-Type"))
	}
	var last jobOut
	for _, line := range strings.Split(string(r.Body), "\n") {
		if data, ok := strings.CutPrefix(line, "data: "); ok {
			if err := json.Unmarshal([]byte(data), &last); err != nil {
				t.Fatal(err)
			}
		}
	}
	if last.Status != "succeeded" || last.Children["succeeded"] != 2 {
		t.Fatalf("last event = %+v\n%s", last, r.Body)
	}

	var res library.Results
	e.call("POST", "/search", map[string]any{}, 200, &res)
	if len(res.Hits) != 1 || res.Hits[0].Fields["name"] != "C" {
		t.Fatalf("cards left = %+v", res.Hits)
	}
	var children []jobOut
	e.call("GET", fmt.Sprintf("/jobs?parent=%d", j.ID), nil, 200, &children)
	if len(children) != 2 {
		t.Fatalf("children = %+v", children)
	}
}
