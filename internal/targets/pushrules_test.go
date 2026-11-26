package targets

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/inkandale/innkeeper/internal/library"
)

// pushRule adds a push rule to e's app for the cards tagged tag, enabled
// unless off.
func (e *env) pushRule(tag string, off bool) library.Rule {
	e.t.Helper()
	search, err := e.lib.CreateSavedSearch(e.ctx, "Tagged "+tag, library.Filter{Tag: &tag}, "")
	if err != nil {
		e.t.Fatal(err)
	}
	r, err := e.lib.CreateRule(e.ctx, library.RuleSpec{Name: "Push " + tag, SavedSearchID: search.ID,
		Action: library.RulePush, InstanceID: &e.inst, AllLibraries: true})
	if err != nil {
		e.t.Fatal(err)
	}
	if off {
		return r
	}
	r.Enabled = true
	if _, r, err = e.lib.UpdateRule(e.ctx, r.ID, r.RuleSpec); err != nil {
		e.t.Fatal(err)
	}
	return r
}

func (e *env) tag(card int64, add, remove []string) {
	e.t.Helper()
	if err := e.lib.EditCardTags(e.ctx, card, add, remove); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) syncApp() SyncApp {
	e.t.Helper()
	res, err := e.s.Sync(e.ctx, SyncParams{InstanceID: e.inst}, nil)
	if err != nil {
		e.t.Fatal(err)
	}
	if len(res.Apps) != 1 || res.Apps[0].Error != "" {
		e.t.Fatalf("sync = %+v", res.Apps)
	}
	return res.Apps[0]
}

func (e *env) bindings(card int64) []Binding {
	e.t.Helper()
	list, err := e.s.Bindings(e.ctx, library.Card, card)
	if err != nil {
		e.t.Fatal(err)
	}
	return list
}

func TestPushRuleCreatesUnboundMatch(t *testing.T) {
	e := setup(t, nil)
	other := e.newCard("Carrow")
	e.tag(e.card, []string{"wholesome"}, nil)
	e.pushRule("wholesome", false)
	if app := e.syncApp(); app.Totals["rule_pushed"] != 1 {
		t.Fatalf("totals = %v", app.Totals)
	}
	if s := e.status(e.card); s != "in_sync" {
		t.Fatalf("matching card is %s", s)
	}
	if b := e.bindings(other); len(b) != 0 {
		t.Fatalf("card the rule doesn't match was pushed: %+v", b)
	}
	if app := e.syncApp(); app.Totals["rule_pushed"] != 0 || app.Totals["autosync_pushed"] != 0 {
		t.Fatalf("second sync pushed again: %v", app.Totals)
	}
}

func TestPushRuleUpdatesAheadMatch(t *testing.T) {
	e := setup(t, nil)
	e.tag(e.card, []string{"wholesome"}, nil)
	e.pushRule("wholesome", false)
	e.syncApp()
	e.update(e.card, func(ed *library.Edit) { ed.Data = setField(ed.Data, "first_mes", "Well met.") })
	if app := e.syncApp(); app.Totals["autosync_pushed"] != 1 {
		t.Fatalf("totals = %v", app.Totals)
	}
	if got := e.remoteData(e.bindingOfCard(e.inst).RemoteID)["first_mes"]; got != "Well met." {
		t.Fatalf("app has first_mes %v", got)
	}
}

func TestPushRuleLeavesCardThatStopsMatching(t *testing.T) {
	e := setup(t, nil)
	e.tag(e.card, []string{"wholesome"}, nil)
	e.pushRule("wholesome", false)
	e.syncApp()
	e.tag(e.card, nil, []string{"wholesome"})
	if app := e.syncApp(); app.Totals["autosync_pushed"] != 0 {
		t.Fatalf("pushed a card the rule no longer matches: %v", app.Totals)
	}
	if s := e.status(e.card); s != "ahead" {
		t.Fatalf("card is %s, want its copy left as it was", s)
	}
}

func TestPushRuleSkipsMissingBinding(t *testing.T) {
	e := setup(t, nil)
	e.tag(e.card, []string{"wholesome"}, nil)
	e.pushRule("wholesome", false)
	e.syncApp()
	b := e.bindingOfCard(e.inst)
	if err := os.Remove(filepath.Join(e.dir, b.RemoteID+".json")); err != nil {
		t.Fatal(err)
	}
	e.check(b)
	if app := e.syncApp(); app.Totals["rule_pushed"] != 0 {
		t.Fatalf("recreated a copy deleted in the app: %v", app.Totals)
	}
	if s := e.status(e.card); s != "missing" {
		t.Fatalf("card is %s", s)
	}
}

func TestPushRuleIgnoresDisabledRule(t *testing.T) {
	e := setup(t, nil)
	e.tag(e.card, []string{"wholesome"}, nil)
	e.pushRule("wholesome", true)
	e.syncApp()
	if b := e.bindings(e.card); len(b) != 0 {
		t.Fatalf("a disabled rule pushed: %+v", b)
	}
}

func TestPushRuleWaitsForAnAppCopyInReview(t *testing.T) {
	e := setup(t, nil)
	e.appCard("r9", map[string]any{"name": "Brakka", "description": "A dwarf smith.", "first_mes": "Hail, traveller.", "scenario": "A forge."}, nil)
	e.tag(e.card, []string{"wholesome"}, nil)
	e.pushRule("wholesome", false)
	app := e.syncApp()
	if app.Totals["rule_pushed"] != 0 || app.Totals["rule_review"] != 1 {
		t.Fatalf("totals = %v", app.Totals)
	}
	if b := e.bindings(e.card); len(b) != 0 {
		t.Fatalf("pushed a second copy beside one in review: %+v", b)
	}
}
