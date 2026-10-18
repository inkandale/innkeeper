package api

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/inkandale/innkeeper/internal/bulk"
	"github.com/inkandale/innkeeper/internal/jobs"
	"github.com/inkandale/innkeeper/internal/library"
	"github.com/inkandale/innkeeper/internal/plugins"
	"github.com/inkandale/innkeeper/internal/targets"
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
	var retried jobOut
	e.call("POST", path+"/retry", nil, 200, &retried)
	if retried.Status != "queued" || retried.Attempts != 0 || retried.Error != nil || retried.FinishedAt != nil {
		t.Fatalf("retried = %+v", retried)
	}
	e.call("POST", path+"/retry", nil, 409, nil)
	e.call("POST", "/jobs/999/retry", nil, 404, nil)
	e.call("GET", "/jobs/999", nil, 404, nil)
	e.call("GET", "/jobs/999/events", nil, 404, nil)

	vault, err := e.s.Registry.CreateInstance(context.Background(), plugins.NewInstance{Plugin: "vault", Name: new("Vault")})
	if err != nil {
		t.Fatal(err)
	}
	e.call("POST", "/imports", map[string]any{"instance_id": vault}, 400, nil)
	e.call("POST", "/imports", map[string]any{"instance_id": 999}, 404, nil)
}

func TestBulkArchiveAndWatch(t *testing.T) {
	e := newEnv(t)
	for _, name := range []string{"A", "B", "C"} {
		tags := `[]`
		if name != "C" {
			tags = `["old"]`
		}
		e.createCard(fmt.Sprintf(`{"name": %q, "tags": %s}`, name, tags))
	}
	w := &jobs.Worker{Queue: e.s.Queue, Log: quiet, Poll: 50 * time.Millisecond,
		Handlers: map[string]jobs.Handler{bulk.ArchiveKind: bulk.Archive(e.s.Lib, e.s.Targets)}}
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

	last, r := e.watchJob(j.ID)
	if r.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("events content type %q", r.Header.Get("Content-Type"))
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
	var list []jobOut
	e.call("GET", "/jobs", nil, 200, &list)
	if len(list) != 1 || list[0].Children["succeeded"] != 2 {
		t.Fatalf("listed bulk job = %+v", list)
	}

	// Unarchive finds the archived cards without the filter naming archived.
	j = jobOut{}
	e.call("POST", "/bulk", map[string]any{"query": map[string]any{"filter": map[string]any{"tag": "old"}}, "action": "unarchive"}, 202, &j)
	if last, r := e.watchJob(j.ID); last.Status != "succeeded" || last.Children["succeeded"] != 2 {
		t.Fatalf("unarchive last event = %+v\n%s", last, r.Body)
	}
	res = library.Results{}
	e.call("POST", "/search", map[string]any{}, 200, &res)
	if len(res.Hits) != 3 {
		t.Fatalf("cards after unarchive = %+v", res.Hits)
	}

	e.call("POST", "/bulk", map[string]any{"query": map[string]any{}, "action": "archive"}, 400, nil)
}

func TestBulkUnarchiveLorebooks(t *testing.T) {
	e := newEnv(t)
	var lb entityOut
	e.call("POST", "/lorebooks", map[string]any{"data": map[string]any{"name": "Old lore", "entries": []any{}}}, 201, &lb)
	path := fmt.Sprintf("/lorebooks/%d", lb.ID)
	e.call("PUT", path+"/archived", map[string]any{"archived": true}, 204, nil)

	w := &jobs.Worker{Queue: e.s.Queue, Log: quiet, Poll: 50 * time.Millisecond,
		Handlers: map[string]jobs.Handler{bulk.LorebookArchiveKind: bulk.ArchiveLorebook(e.s.Lib, e.s.Targets)}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })

	var j jobOut
	e.call("POST", "/bulk", map[string]any{"kind": "lorebook", "query": map[string]any{"text": "old"}, "action": "unarchive"}, 202, &j)
	if last, r := e.watchJob(j.ID); last.Status != "succeeded" || last.Children["succeeded"] != 1 {
		t.Fatalf("unarchive last event = %+v\n%s", last, r.Body)
	}
	var got entityOut
	e.call("GET", path, nil, 200, &got)
	if got.ArchivedAt != nil {
		t.Fatalf("lorebook still archived: %+v", got)
	}
}

// watchJob reads a job's event stream, which ends when the job finishes, and
// returns the last event.
func (e *testEnv) watchJob(id int64) (jobOut, result) {
	e.t.Helper()
	r := e.call("GET", fmt.Sprintf("/jobs/%d/events", id), nil, 200, nil)
	var last jobOut
	for _, line := range strings.Split(string(r.Body), "\n") {
		if data, ok := strings.CutPrefix(line, "data: "); ok {
			last = jobOut{}
			if err := json.Unmarshal([]byte(data), &last); err != nil {
				e.t.Fatal(err)
			}
		}
	}
	return last, r
}

func TestWatchPings(t *testing.T) {
	// Restored after newEnv's server closes, so no stream still reads it.
	old := pingInterval
	pingInterval = 50 * time.Millisecond
	t.Cleanup(func() { pingInterval = old })
	e := newEnv(t)
	var j jobOut
	e.call("POST", "/imports", map[string]any{"instance_id": e.importID}, 202, &j)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("%s/api/jobs/%d/events", e.srv.URL, j.ID), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+e.token)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.Header.Get("X-Accel-Buffering") != "no" {
		t.Errorf("X-Accel-Buffering = %q", resp.Header.Get("X-Accel-Buffering"))
	}
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		if sc.Text() == ": ping" {
			return
		}
	}
	t.Fatalf("no ping before the stream ended: %v", sc.Err())
}

func TestImportBrokenInstance(t *testing.T) {
	e := newEnv(t)
	e.markBroken(e.importID, "secret detail")
	r := e.call("POST", "/imports", map[string]any{"instance_id": e.importID}, 409, nil)
	if strings.Contains(string(r.Body), "secret detail") {
		t.Fatalf("leaked the stored error: %s", r.Body)
	}
}

func TestWatchEndsWhenStopping(t *testing.T) {
	e := newEnv(t)
	stopping := make(chan struct{})
	e.s.Stopping = stopping
	var j jobOut
	e.call("POST", "/imports", map[string]any{"instance_id": e.importID}, 202, &j)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("%s/api/jobs/%d/events", e.srv.URL, j.ID), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+e.token)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	sc := bufio.NewScanner(resp.Body)
	if !sc.Scan() {
		t.Fatalf("no first event: %v", sc.Err())
	}
	close(stopping)
	for sc.Scan() {
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("stream didn't end when stopping: %v", err)
	}
}

func TestReconcileJobSummary(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	j, err := e.s.Queue.Enqueue(ctx, jobs.Spec{Kind: targets.ReconcileKind})
	if err != nil {
		t.Fatal(err)
	}
	snap := `{"items": [{"remote_id": "r1", "verdict": "new"}], "totals": {"new": 1}}`
	if _, err := e.pool.Exec(ctx, `UPDATE job SET status = 'succeeded', finished_at = now(), progress = $1 WHERE id = $2`,
		snap, j.ID); err != nil {
		t.Fatal(err)
	}
	var list []jobOut
	e.call("GET", "/jobs", nil, 200, &list)
	if len(list) != 1 || string(list[0].Progress) != `{"totals":{"new":1}}` {
		t.Errorf("listed progress = %s", list[0].Progress)
	}
	if last, _ := e.watchJob(j.ID); string(last.Progress) != `{"totals":{"new":1}}` {
		t.Errorf("watched progress = %s", last.Progress)
	}
	var full jobOut
	e.call("GET", fmt.Sprintf("/jobs/%d", j.ID), nil, 200, &full)
	if !strings.Contains(string(full.Progress), `"items"`) {
		t.Errorf("full progress = %s", full.Progress)
	}
}

func TestBulkTagCollectExport(t *testing.T) {
	e := newEnv(t)
	a := e.createCard(`{"name": "A"}`)
	b := e.createCard(`{"name": "B"}`)
	e.createCard(`{"name": "C"}`)
	w := &jobs.Worker{Queue: e.s.Queue, Log: quiet, Poll: 50 * time.Millisecond,
		Handlers: map[string]jobs.Handler{bulk.TagKind: bulk.Tag(e.s.Lib), bulk.CollectKind: bulk.Collect(e.s.Lib),
			bulk.ExportKind: bulk.Export(e.s.exporter())}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	picked := map[string]any{"filter": map[string]any{"ids": []int64{a.ID, b.ID}}}

	var j jobOut
	e.call("POST", "/bulk", map[string]any{"query": picked, "action": "tag", "tags": []string{"elf"}}, 202, &j)
	if last, r := e.watchJob(j.ID); last.Status != "succeeded" || last.Children["succeeded"] != 2 {
		t.Fatalf("tag job = %+v\n%s", last, r.Body)
	}
	var res library.Results
	e.call("POST", "/search", map[string]any{"filter": map[string]any{"tag": "elf"}}, 200, &res)
	if res.Total != 2 {
		t.Fatalf("tagged = %+v", res.Hits)
	}

	var col struct{ ID int64 }
	e.call("POST", "/collections", map[string]any{"name": "Elves"}, 201, &col)
	j = jobOut{}
	e.call("POST", "/bulk", map[string]any{"query": picked, "action": "collect", "collection": col.ID}, 202, &j)
	if last, _ := e.watchJob(j.ID); last.Status != "succeeded" {
		t.Fatalf("collect job = %+v", last)
	}
	e.call("POST", "/search", map[string]any{"filter": map[string]any{"collection": col.ID}}, 200, &res)
	if res.Total != 2 {
		t.Fatalf("collected = %+v", res.Hits)
	}

	j = jobOut{}
	e.call("POST", "/bulk", map[string]any{"query": picked, "action": "export"}, 202, &j)
	last, _ := e.watchJob(j.ID)
	var p bulk.ExportProgress
	if last.Kind != bulk.ExportKind || last.Status != "succeeded" || json.Unmarshal(last.Progress, &p) != nil || p.Blob == "" {
		t.Fatalf("export job = %+v", last)
	}
	e.call("GET", "/blobs/"+p.Blob, nil, 200, nil)
	if string(last.Payload) != `{"cards":2,"format":"json"}` {
		t.Errorf("watched export payload = %s", last.Payload)
	}
	var list []jobOut
	e.call("GET", "/jobs?kind="+bulk.ExportKind, nil, 200, &list)
	if len(list) != 1 || string(list[0].Payload) != `{"cards":2,"format":"json"}` {
		t.Errorf("listed export = %+v", list)
	}
	var full jobOut
	e.call("GET", fmt.Sprintf("/jobs/%d", j.ID), nil, 200, &full)
	if !strings.Contains(string(full.Payload), `"card_ids"`) {
		t.Errorf("full export payload = %s", full.Payload)
	}

	e.call("POST", "/bulk", map[string]any{"query": picked, "action": "tag"}, 400, nil)
	e.call("POST", "/bulk", map[string]any{"query": picked, "action": "tag", "tags": []string{"elf", "  "}}, 400, nil)
	e.call("POST", "/bulk", map[string]any{"query": picked, "action": "tag", "tags": []string{strings.Repeat("x", 101)}}, 422, nil)
	e.call("POST", "/bulk", map[string]any{"query": picked, "action": "tag", "tags": make([]string, 101)}, 422, nil)
	e.call("POST", "/bulk", map[string]any{"query": picked, "action": "collect", "collection": 99999}, 404, nil)
	e.call("POST", "/bulk", map[string]any{"query": picked, "action": "export", "format": "nope"}, 400, nil)
}

func TestBulkLorebooks(t *testing.T) {
	e := newEnv(t)
	w := &jobs.Worker{Queue: e.s.Queue, Log: quiet, Poll: 50 * time.Millisecond,
		Handlers: map[string]jobs.Handler{bulk.LorebookArchiveKind: bulk.ArchiveLorebook(e.s.Lib, e.s.Targets)}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })

	var forge entityResult
	e.call("POST", "/lorebooks", map[string]any{"data": json.RawMessage(`{"name": "Forge", "entries": []}`)}, 201, &forge)
	pick := map[string]any{"filter": map[string]any{"ids": []int64{forge.ID}}}

	var j jobOut
	e.call("POST", "/bulk", map[string]any{"kind": "lorebook", "query": pick, "action": "archive"}, 202, &j)
	if last, _ := e.watchJob(j.ID); last.Status != "succeeded" {
		t.Fatalf("bulk archive = %+v", last)
	}
	var got entityResult
	e.call("GET", fmt.Sprintf("/lorebooks/%d", forge.ID), nil, 200, &got)
	if got.ArchivedAt == nil {
		t.Fatal("lorebook not archived")
	}

	e.call("POST", "/bulk", map[string]any{"kind": "lorebook", "query": pick, "action": "tag", "tags": []string{"x"}}, 400, nil)
	e.call("POST", "/bulk", map[string]any{"kind": "spell", "query": pick, "action": "archive"}, 422, nil)
}
