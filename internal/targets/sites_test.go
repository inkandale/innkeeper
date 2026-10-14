package targets

import (
	"testing"

	"github.com/davidgibbons/innkeeper/internal/library"
	"github.com/davidgibbons/innkeeper/internal/plugins"
)

// addSites creates an instance of the fake sites plugin, which reads https://fake.example/{a}/{b} in data.source.
func (e *env) addSites() {
	e.t.Helper()
	if _, err := e.reg.CreateInstance(e.ctx, plugins.NewInstance{Plugin: "sites", Name: "Sites"}); err != nil {
		e.t.Fatal(err)
	}
}

func TestReconcileMatchesASitesPluginSource(t *testing.T) {
	e := setup(t, nil)
	e.addSites()
	vyr := e.libCard(map[string]any{"name": "Vyr", "description": "An ancient red dragon asleep on gold."})
	if _, err := e.lib.Cards.AddSource(e.ctx, vyr, library.Source{Kind: "fake", Ref: "u/vyr"}); err != nil {
		t.Fatal(err)
	}
	e.appCard("src", map[string]any{"name": "Lia", "description": "A tavern cat that steals fish.",
		"source": []string{"https://fake.example/u/vyr"}}, nil)
	it := e.reconcile(ReconcileParams{}, "src")["src"]
	if len(it.Candidates) != 1 || it.Candidates[0].CardID != vyr {
		t.Fatalf("item = %+v, want vyr as a candidate by source", it)
	}
	if _, ok := it.Candidates[0].Reasons["source"]; !ok {
		t.Fatalf("reasons = %v, want source", it.Candidates[0].Reasons)
	}
}

func TestResolveRecordsSitesPluginSources(t *testing.T) {
	e, card := resolveEnv(t)
	e.addSites()
	e.appCard("src", map[string]any{"name": "Tova", "description": "A smith.", "first_mes": libGreeting,
		"source": []string{"https://fake.example/u/tova"}}, nil)
	e.resolve(e.compareItem("src", card), nil, "")
	if srcs, err := e.lib.Cards.Sources(e.ctx, card); err != nil || len(srcs) != 1 || srcs[0].Kind != "fake" || srcs[0].LinkedBy != "auto" {
		t.Fatalf("sources = %+v, %v", srcs, err)
	}
}

func TestResolveImportRecordsTheSplitLorebooksSources(t *testing.T) {
	e := setup(t, nil)
	e.addSites()
	e.appCard("owl", map[string]any{"name": "Pell", "description": "A clockwork owl.", "character_book": map[string]any{
		"name": "Owl lore", "entries": []any{}, "source": []string{"https://fake.example/u/owl-lore"}}}, nil)
	c, err := e.s.CompareItem(e.ctx, library.Card, e.inst, "owl", e.card)
	if err != nil {
		t.Fatal(err)
	}
	res, err := e.s.Resolve(e.ctx, library.Card, Resolution{InstanceID: e.inst, RemoteID: "owl", AppHash: c.AppHash, Action: "import"})
	if err != nil {
		t.Fatal(err)
	}
	links := e.links(res.CardID)
	if len(links) != 1 {
		t.Fatalf("links = %v", links)
	}
	if srcs, err := e.lib.Lorebooks.Sources(e.ctx, links[0]); err != nil || len(srcs) != 1 || srcs[0].Ref != "u/owl-lore" {
		t.Fatalf("lorebook sources = %+v, %v", srcs, err)
	}
}
