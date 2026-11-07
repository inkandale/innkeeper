package api

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/inkandale/innkeeper/internal/bulk"
	"github.com/inkandale/innkeeper/internal/jobs"
	"github.com/inkandale/innkeeper/internal/library"
)

// startHide runs the hide job until the test ends.
func startHide(e *testEnv) {
	w := &jobs.Worker{Queue: e.s.Queue, Log: quiet, Poll: 50 * time.Millisecond,
		Handlers: map[string]jobs.Handler{bulk.HideKind: bulk.Hide(e.s.Lib, nil)}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	e.t.Cleanup(func() { cancel(); <-done })
}

// eventually fails the test unless ok turns true within 10 seconds.
func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); !ok(); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("never %s", what)
		}
	}
}

// archivedReason is card id's archived_reason, or "-" when it isn't archived.
func (e *testEnv) archivedReason(id int64) string {
	e.t.Helper()
	var c entityOut
	e.call("GET", fmt.Sprintf("/cards/%d", id), nil, 200, &c)
	if c.ArchivedAt == nil {
		return "-"
	}
	return c.ArchivedReason
}

// newRule saves a search named name for filter and a rule on it, disabled.
func (e *testEnv) newRule(name string, filter, rule map[string]any) ruleItem {
	e.t.Helper()
	var ss savedSearchItem
	e.call("POST", "/saved-searches", map[string]any{"name": name, "filter": filter}, 201, &ss)
	rule["name"], rule["saved_search_id"] = name, ss.ID
	var r ruleItem
	e.call("POST", "/rules", rule, 201, &r)
	return r
}

// putRule saves r with change made to its spec.
func (e *testEnv) putRule(r ruleItem, change func(*library.RuleSpec)) ruleItem {
	e.t.Helper()
	spec := r.RuleSpec
	change(&spec)
	var out ruleItem
	e.call("PUT", fmt.Sprintf("/rules/%d", r.ID), spec, 200, &out)
	return out
}

func enable(s *library.RuleSpec) { s.Enabled = true }

func TestNarrowingARuleGivesItsCardsBackInTheLibraryItLeft(t *testing.T) {
	e := newEnv(t)
	startHide(e)
	var two library.LibrarySummary
	e.call("POST", "/libraries", map[string]any{"name": "Two"}, 201, &two)
	a := e.createCard(`{"name": "Junk a"}`).ID
	b, _, err := e.s.Lib.Cards.Create(context.Background(), two.ID, "junk-b", "upload", library.Edit{Data: []byte(`{"name": "Junk b"}`), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	r := e.newRule("Junk", map[string]any{"name": "junk"}, map[string]any{"action": "archive", "all_libraries": true, "enabled": true})
	if r.Enabled {
		t.Fatal("a new rule started enabled")
	}
	r = e.putRule(r, enable)
	eventually(t, "archived both", func() bool { return e.archivedReason(a) == r.Reason() && e.archivedReason(b.ID) == r.Reason() })

	e.putRule(r, func(s *library.RuleSpec) { s.AllLibraries, s.Libraries = false, []int64{library.DefaultLibrary} })
	eventually(t, "gave b back", func() bool { return e.archivedReason(b.ID) == "-" })
	if e.archivedReason(a) != r.Reason() {
		t.Fatal("a came back in the library the rule still covers")
	}

	var list []ruleItem
	e.call("GET", fmt.Sprintf("/rules?library=%d", two.ID), nil, 200, &list)
	if len(list) != 0 {
		t.Fatalf("rules covering Two = %+v", list)
	}
	e.call("GET", "/rules?library=1", nil, 200, &list)
	if len(list) != 1 || !slices.Equal(list[0].Affected, []library.RuleCount{{Library: 1, Cards: 1}}) || list[0].SavedSearch != "Junk" {
		t.Fatalf("rules covering 1 = %+v", list)
	}
}

func TestRulesRefuseAWrongScopeOrActionAndGuardTheirSearch(t *testing.T) {
	e := newEnv(t)
	var col library.Collection
	e.call("POST", "/collections", map[string]any{"name": "Elves"}, 201, &col)
	var ss savedSearchItem
	e.call("POST", "/saved-searches", map[string]any{"name": "Elves", "filter": map[string]any{"tag": "elf"}}, 201, &ss)
	collect := map[string]any{"name": "Collect elves", "saved_search_id": ss.ID, "action": "collect", "collection_id": col.ID, "all_libraries": true}
	e.call("POST", "/rules", collect, 400, nil)
	collect["all_libraries"], collect["libraries"] = false, []int64{1}
	var r ruleItem
	e.call("POST", "/rules", collect, 201, &r)

	spec := r.RuleSpec
	spec.Action, spec.CollectionID, spec.AllLibraries = "archive", nil, true
	e.call("PUT", fmt.Sprintf("/rules/%d", r.ID), spec, 400, nil)

	res := e.call("DELETE", fmt.Sprintf("/saved-searches/%d", ss.ID), nil, 409, nil)
	if !strings.Contains(string(res.Body), "Collect elves") {
		t.Fatalf("409 = %s", res)
	}
	var list []savedSearchItem
	e.call("GET", "/saved-searches", nil, 200, &list)
	if len(list) != 1 || len(list[0].Rules) != 1 || list[0].Rules[0].Name != "Collect elves" {
		t.Fatalf("searches = %+v", list)
	}
}

func TestDeletingARuleGivesBackWhatItDid(t *testing.T) {
	e := newEnv(t)
	startHide(e)
	junk := e.createCard(`{"name": "Junk", "tags": ["junk"]}`).ID
	r := e.putRule(e.newRule("Tagged junk", map[string]any{"tag": "junk"}, map[string]any{"action": "archive", "all_libraries": true}), enable)
	eventually(t, "archived the junk", func() bool { return e.archivedReason(junk) == r.Reason() })
	e.call("DELETE", fmt.Sprintf("/rules/%d", r.ID), nil, 204, nil)
	eventually(t, "gave the junk back", func() bool { return e.archivedReason(junk) == "-" })
}
