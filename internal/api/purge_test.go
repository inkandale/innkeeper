package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/inkandale/innkeeper/internal/bulk"
	"github.com/inkandale/innkeeper/internal/jobs"
	"github.com/inkandale/innkeeper/internal/tier"
)

func TestPurgeCards(t *testing.T) {
	e := newEnv(t)
	w := &jobs.Worker{Queue: e.s.Queue, Log: quiet, Poll: 50 * time.Millisecond,
		Handlers: map[string]jobs.Handler{bulk.PurgeKind: bulk.Purge(e.s.Lib)}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })

	gone1 := e.createCard(`{"name": "Gone one"}`)
	gone2 := e.createCard(`{"name": "Gone two"}`)
	kept := e.createCard(`{"name": "Kept"}`)
	query := map[string]any{"filter": map[string]any{"ids": []int64{gone1.ID, gone2.ID}}}

	r := e.call("POST", "/cards/purge", map[string]any{"query": query, "confirm": "3"}, 422, nil)
	if !strings.Contains(string(r.Body), "2") {
		t.Fatalf("a wrong count should say the right one: %s", r.Body)
	}
	e.call("POST", "/cards/purge", map[string]any{"query": map[string]any{}, "confirm": "3"}, 400, nil)
	if r := e.send("POST", "/cards/purge", map[string]any{"query": query, "confirm": "2"}, "Authorization", "Bearer "+e.tokenWith(tier.Write)); r.Status != 403 {
		t.Fatalf("a write token: %s", r)
	}

	var j jobOut
	e.call("POST", "/cards/purge", map[string]any{"query": query, "confirm": "2"}, 202, &j)
	last, resp := e.watchJob(j.ID)
	if last.Status != "succeeded" || last.CardsChanged != 2 {
		t.Fatalf("purge = %+v\n%s", last, resp.Body)
	}
	var p bulk.PurgeProgress
	if err := json.Unmarshal(last.Progress, &p); err != nil || p.Done != 2 || p.Total != 2 || len(p.Refused) != 0 {
		t.Fatalf("progress %s: %v", last.Progress, err)
	}
	for _, id := range []int64{gone1.ID, gone2.ID} {
		e.call("GET", fmt.Sprintf("/cards/%d", id), nil, 404, nil)
	}
	e.call("GET", fmt.Sprintf("/cards/%d", kept.ID), nil, 200, nil)
}
