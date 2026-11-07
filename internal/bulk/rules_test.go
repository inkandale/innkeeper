package bulk

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/inkandale/innkeeper/internal/jobs"
	"github.com/inkandale/innkeeper/internal/library"
)

// ruleEnv is a library and a queue whose worker runs the hide job.
type ruleEnv struct {
	t   *testing.T
	ctx context.Context
	lib *library.Library
	q   *jobs.Queue
}

func newRuleEnv(t *testing.T) *ruleEnv {
	lib, q, _ := setup(t, func(lib *library.Library) map[string]jobs.Handler {
		return map[string]jobs.Handler{HideKind: Hide(lib, nil)}
	})
	return &ruleEnv{t, context.Background(), lib, q}
}

func (e *ruleEnv) card(slug, data string) int64 {
	e.t.Helper()
	c, _, err := e.lib.Cards.Create(e.ctx, library.DefaultLibrary, slug, "upload", library.Edit{Data: []byte(data), Source: "ui"})
	if err != nil {
		e.t.Fatal(err)
	}
	return c.ID
}

// rule saves a search for f and an enabled rule on it.
func (e *ruleEnv) rule(name string, f library.Filter, spec library.RuleSpec) library.Rule {
	e.t.Helper()
	s, err := e.lib.CreateSavedSearch(e.ctx, name, f, "")
	if err != nil {
		e.t.Fatal(err)
	}
	spec.Name, spec.SavedSearchID = name, s.ID
	r, err := e.lib.CreateRule(e.ctx, spec)
	if err != nil {
		e.t.Fatal(err)
	}
	return e.setEnabled(r, true)
}

func (e *ruleEnv) setEnabled(r library.Rule, on bool) library.Rule {
	e.t.Helper()
	spec := r.RuleSpec
	spec.Enabled = on
	_, after, err := e.lib.UpdateRule(e.ctx, r.ID, spec)
	if err != nil {
		e.t.Fatal(err)
	}
	return after
}

func (e *ruleEnv) hide(duplicates string) HideProgress {
	return e.hideWith(HidePayload{Library: library.DefaultLibrary, Duplicates: duplicates, Keep: "oldest"})
}

func (e *ruleEnv) hideWith(p HidePayload) HideProgress {
	e.t.Helper()
	j, err := e.q.Enqueue(e.ctx, jobs.Spec{Kind: HideKind, Payload: p})
	if err != nil {
		e.t.Fatal(err)
	}
	var prog HideProgress
	if err := json.Unmarshal(wait(e.t, e.q, j.ID).Progress, &prog); err != nil {
		e.t.Fatal(err)
	}
	return prog
}

// reason is card id's archived reason, or "-" when it isn't archived.
func (e *ruleEnv) reason(id int64) string {
	e.t.Helper()
	c, err := e.lib.Cards.Get(e.ctx, id)
	if err != nil {
		e.t.Fatal(err)
	}
	if c.ArchivedAt == nil {
		return "-"
	}
	return c.ArchivedReason
}

func TestDuplicatesAndArchiveRulesShareOneArchivedList(t *testing.T) {
	e := newRuleEnv(t)
	orig := e.card("orig", `{"name": "Brakka", "description": "A dwarf smith."}`)
	copied := e.card("copy", `{"name": "Brakka", "description": "A dwarf smith."}`)
	junk := e.card("junk", `{"name": "Junk"}`)
	r := e.rule("Junk", library.Filter{Name: new("junk")}, library.RuleSpec{Action: library.RuleArchive, AllLibraries: true})
	// Matches the copy alone, so the original stays out of the counts.
	copyRule := e.rule("Copy", library.Filter{IDs: []int64{copied}}, library.RuleSpec{Action: library.RuleArchive, AllLibraries: true})

	if p := e.hide("exact"); p.Archived != 2 || p.Duplicate != 1 {
		t.Fatalf("first run %+v", p)
	}
	if e.reason(copied) != library.ReasonDuplicate || e.reason(junk) != r.Reason() || e.reason(orig) != "-" {
		t.Fatalf("reasons: copy %q, junk %q, orig %q", e.reason(copied), e.reason(junk), e.reason(orig))
	}
	e.setEnabled(copyRule, false)

	// Disabling the rule gives junk back and leaves the duplicate.
	r = e.setEnabled(r, false)
	if p := e.hide("exact"); p.Unarchived != 1 {
		t.Fatalf("rule off %+v", p)
	}
	if e.reason(junk) != "-" || e.reason(copied) != library.ReasonDuplicate {
		t.Fatalf("rule off: junk %q, copy %q", e.reason(junk), e.reason(copied))
	}

	// Duplicates off gives the copy back and leaves the rule's card.
	e.setEnabled(r, true)
	e.hide("off")
	if e.reason(copied) != "-" || e.reason(junk) != r.Reason() {
		t.Fatalf("duplicates off: copy %q, junk %q", e.reason(copied), e.reason(junk))
	}
}

func TestEveryArchiveRuleKeepsItsCardsAndTheLowestIDWins(t *testing.T) {
	e := newRuleEnv(t)
	old := e.card("old", `{"name": "Old draft"}`)
	draft := e.card("draft", `{"name": "Draft"}`)
	first := e.rule("Old", library.Filter{Name: new("old")}, library.RuleSpec{Action: library.RuleArchive, AllLibraries: true})
	second := e.rule("Drafts", library.Filter{Name: new("draft")}, library.RuleSpec{Action: library.RuleArchive, AllLibraries: true})

	for run := range 2 {
		p := e.hide("off")
		if want := 2 * (1 - run); p.Archived != want || p.Unarchived != 0 {
			t.Fatalf("run %d %+v", run, p)
		}
		if e.reason(old) != first.Reason() || e.reason(draft) != second.Reason() {
			t.Fatalf("run %d: old %q, draft %q", run, e.reason(old), e.reason(draft))
		}
	}
}

func TestFailingRuleLeavesItsCardsArchived(t *testing.T) {
	e := newRuleEnv(t)
	junk := e.card("junk", `{"name": "Junk"}`)
	r := e.rule("Junk", library.Filter{Name: new("junk")}, library.RuleSpec{Action: library.RuleArchive, AllLibraries: true})
	e.hide("off")

	// An enrichment term that names no instance fails without Browsable.
	if _, err := e.lib.UpdateSavedSearch(e.ctx, r.SavedSearchID, nil, &library.Filter{Enrichment: new("rating"), Missing: new(true)}, nil); err != nil {
		t.Fatal(err)
	}
	var prog HideProgress
	if err := ApplyRules(e.ctx, e.lib, library.DefaultLibrary, nil, nil, nil, &prog); err == nil {
		t.Fatal("a failing rule applied")
	}
	if e.reason(junk) != r.Reason() {
		t.Fatalf("junk %q after a failed run", e.reason(junk))
	}
}

func TestHideJobReadsBrowsableKeysFromItsPayload(t *testing.T) {
	e := newRuleEnv(t)
	unrated := e.card("unrated", `{"name": "Unrated"}`)
	r := e.rule("Unrated", library.Filter{Enrichment: new("rating"), Missing: new(true)},
		library.RuleSpec{Action: library.RuleArchive, AllLibraries: true})
	p := e.hideWith(HidePayload{Library: library.DefaultLibrary, Duplicates: "off", Keep: "oldest",
		Browsable: map[string][]library.Scorer{"rating": {{Instance: 1, Key: "rating"}}}})
	if p.Archived != 1 || e.reason(unrated) != r.Reason() {
		t.Fatalf("%+v, unrated %q", p, e.reason(unrated))
	}
}

func TestCollectRuleOnATagFollowsItsSearch(t *testing.T) {
	e := newRuleEnv(t)
	draft := e.card("draft", `{"name": "Draft", "tags": ["wip"]}`)
	owned := e.card("owned", `{"name": "Owned", "tags": ["WIP"]}`)
	col, err := e.lib.CreateCollection(e.ctx, library.DefaultLibrary, "Work in progress", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.lib.AddToCollection(e.ctx, col.ID, owned); err != nil {
		t.Fatal(err)
	}
	r := e.rule("WIP", library.Filter{Tag: new("wip")},
		library.RuleSpec{Action: library.RuleCollect, CollectionID: &col.ID, Libraries: []int64{library.DefaultLibrary}})

	if p := e.hide("off"); p.Collected != 1 {
		t.Fatalf("first run %+v", p)
	}

	// Both lose the tag; only the rule's membership goes.
	for _, id := range []int64{draft, owned} {
		c, _ := e.lib.Cards.Get(e.ctx, id)
		if _, err := e.lib.Cards.Update(e.ctx, id, c.HeadRevisionID, library.Edit{Data: []byte(`{"name": "Done"}`), Source: "ui"}); err != nil {
			t.Fatal(err)
		}
	}
	if p := e.hide("off"); p.Uncollected != 1 {
		t.Fatalf("second run %+v", p)
	}
	if in, _ := e.lib.CardCollections(e.ctx, draft); len(in) != 0 {
		t.Fatalf("draft still collected: %v", in)
	}
	if in, _ := e.lib.CardCollections(e.ctx, owned); !slices.Equal(in, []int64{col.ID}) {
		t.Fatalf("the owner's membership = %v", in)
	}

	// A disabled rule gives back what it collected, and still not the owner's.
	c, _ := e.lib.Cards.Get(e.ctx, draft)
	if _, err := e.lib.Cards.Update(e.ctx, draft, c.HeadRevisionID, library.Edit{Data: []byte(`{"name": "Draft", "tags": ["wip"]}`), Source: "ui"}); err != nil {
		t.Fatal(err)
	}
	if p := e.hide("off"); p.Collected != 1 {
		t.Fatalf("third run %+v", p)
	}
	e.setEnabled(r, false)
	if p := e.hide("off"); p.Uncollected != 1 {
		t.Fatalf("rule off %+v", p)
	}
	if in, _ := e.lib.CardCollections(e.ctx, draft); len(in) != 0 {
		t.Fatalf("draft still collected by a disabled rule: %v", in)
	}
	if in, _ := e.lib.CardCollections(e.ctx, owned); !slices.Equal(in, []int64{col.ID}) {
		t.Fatalf("the owner's membership after rule off = %v", in)
	}
}
