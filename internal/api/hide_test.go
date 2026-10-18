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
	"github.com/inkandale/innkeeper/internal/plugins"
	"github.com/inkandale/innkeeper/protocol"
)

func TestHideArchivesLowScoresAndDuplicates(t *testing.T) {
	e := newEnrichEnv(t)
	w := &jobs.Worker{Queue: e.s.Queue, Log: quiet, Poll: 50 * time.Millisecond,
		Handlers: map[string]jobs.Handler{bulk.HideKind: bulk.Hide(e.s.Lib, nil)}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })

	long := strings.Repeat("x", 90)
	first := e.card("Brakka", long)
	copied := e.card("Brakka", long)
	low := e.card("Lia", strings.Repeat("y", 20))
	owned := e.card("Owned", strings.Repeat("z", 10))
	e.evaluate(e.instID, first, copied, low, owned)
	e.call("PUT", fmt.Sprintf("/cards/%d/archived", owned), map[string]any{"archived": true}, 204, nil)

	e.call("POST", "/libraries/1/hide", nil, 400, nil) // nothing set
	var set librarySettings
	e.call("PATCH", "/libraries/1/settings", map[string]any{"hide_below": 50, "duplicates": "exact", "duplicate_keep": "oldest"}, 200, &set)
	if *set.HideBelow.Value != 50 || *set.Duplicates.Value != "exact" || *set.DuplicateDistance.Value != 3 || !set.Duplicates.Overridden {
		t.Fatalf("settings %+v", set)
	}
	e.call("PATCH", "/libraries/1/settings", map[string]any{"duplicates": "fuzzy"}, 422, nil)

	hide := func() bulk.HideProgress {
		t.Helper()
		var j jobOut
		e.call("POST", "/libraries/1/hide", nil, 202, &j)
		last, r := e.watchJob(j.ID)
		if last.Status != "succeeded" {
			t.Fatalf("hide = %+v\n%s", last, r.Body)
		}
		var p bulk.HideProgress
		if err := json.Unmarshal(last.Progress, &p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	reason := func(id int64) (bool, string) {
		t.Helper()
		var c entityOut
		e.call("GET", fmt.Sprintf("/cards/%d", id), nil, 200, &c)
		return c.ArchivedAt != nil, c.ArchivedReason
	}

	if p := hide(); p.Groups != 1 || p.LowScore != 1 || p.Duplicate != 1 || p.Archived != 2 {
		t.Fatalf("progress %+v", p)
	}
	for id, want := range map[int64]string{copied: "duplicate", low: "low_score"} {
		if archived, r := reason(id); !archived || r != want {
			t.Errorf("card %d = %v %q, want %q", id, archived, r, want)
		}
	}
	if archived, _ := reason(first); archived {
		t.Error("the kept copy was archived")
	}
	if archived, r := reason(owned); !archived || r != "" {
		t.Errorf("the owner's card = %v %q", archived, r)
	}

	// Without a threshold, the low card comes back.
	e.call("PATCH", "/libraries/1/settings", map[string]any{"hide_below": nil}, 200, nil)
	if p := hide(); p.Unarchived != 1 {
		t.Fatalf("second progress %+v", p)
	}
	if archived, _ := reason(low); archived {
		t.Error("the low card is still archived")
	}
}

func TestHideDefaultsFollowTheCore(t *testing.T) {
	e := newEnv(t)
	var s settings
	e.call("PATCH", "/settings", map[string]any{"duplicates": "near", "duplicate_distance": 5}, 200, &s)
	if s.Duplicates != "near" || s.DuplicateDistance != 5 || s.DuplicateKeep != "best" || s.HideBelow != nil {
		t.Fatalf("settings %+v", s)
	}
	var set librarySettings
	e.call("GET", "/libraries/1/settings", nil, 200, &set)
	if *set.Duplicates.Value != "near" || set.Duplicates.Overridden || *set.DuplicateDistance.Default != 5 {
		t.Fatalf("library settings %+v", set)
	}
	e.call("PATCH", "/settings", map[string]any{"duplicate_distance": 9}, 422, nil)
}

func TestHideAsksTheDecisionModelAboutNearPairs(t *testing.T) {
	e := newEnv(t)
	decider := *e.manifests["fake"]
	decider.Name, decider.Capabilities = "decider", []string{"ai"}
	e.manifests["decider"] = &decider
	id, err := e.s.Registry.CreateInstance(context.Background(), plugins.NewInstance{Plugin: "decider", Name: new("d3")})
	if err != nil {
		t.Fatal(err)
	}
	var asked []string
	decide := func(_ context.Context, inst int64, p protocol.AIDecideParams) (protocol.AIDecideResult, error) {
		asked = append(asked, string(p.State))
		same := 0.9
		if inst != id {
			return protocol.AIDecideResult{}, fmt.Errorf("asked instance %d", inst)
		}
		if strings.Contains(string(p.State), "cat") {
			same = 0.1
		}
		return protocol.AIDecideResult{Answers: map[string]protocol.DecideAnswer{"same": {Type: "noul", Noul: &same}}}, nil
	}
	w := &jobs.Worker{Queue: e.s.Queue, Log: quiet, Poll: 50 * time.Millisecond,
		Handlers: map[string]jobs.Handler{bulk.HideKind: bulk.Hide(e.s.Lib, decide)}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })

	const text = "Brakka is a grumpy dwarf blacksmith who runs the forge at the edge of town. " +
		"She hates elves, loves good ale, and never turns away a customer with honest coin."
	orig := e.createCard(`{"name": "Brakka", "description": "` + text + `"}`)
	copied := e.createCard(`{"name": "Brakka", "description": "` + text + `"}`)
	edited := e.createCard(`{"name": "Brakka 2", "description": "` + text + ` She has a cat."}`)
	e.call("PATCH", "/libraries/1/settings", map[string]any{"duplicates": "near", "duplicate_distance": 8, "duplicate_decide": id}, 200, nil)

	var j jobOut
	e.call("POST", "/libraries/1/hide", nil, 202, &j)
	last, r := e.watchJob(j.ID)
	if last.Status != "succeeded" {
		t.Fatalf("hide = %+v\n%s", last, r.Body)
	}
	var p bulk.HideProgress
	if err := json.Unmarshal(last.Progress, &p); err != nil {
		t.Fatal(err)
	}
	// The exact copies need no asking; both near pairs involve the edited card, which the model rejects.
	if p.Checked != 2 || p.Rejected != 2 || p.Duplicate != 1 || len(asked) != 2 {
		t.Fatalf("progress %+v, asked %d", p, len(asked))
	}
	for id, want := range map[int64]bool{orig.ID: false, copied.ID: true, edited.ID: false} {
		var c entityOut
		e.call("GET", fmt.Sprintf("/cards/%d", id), nil, 200, &c)
		if (c.ArchivedAt != nil) != want {
			t.Errorf("card %d archived %v, want %v", id, c.ArchivedAt != nil, want)
		}
	}
	e.call("PATCH", "/libraries/1/settings", map[string]any{"duplicate_decide": 999999}, 200, nil)
	e.call("POST", "/libraries/1/hide", nil, 404, nil)
}
