package api

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/inkandale/innkeeper/internal/library"
	"github.com/inkandale/innkeeper/protocol"
)

func TestSearchRoute(t *testing.T) {
	e := newEnv(t)
	a := e.createCard(`{"name": "Brakka", "description": "A dwarf blacksmith.", "tags": ["fantasy"]}`)
	e.createCard(`{"name": "Carrow", "description": "A sailor."}`)

	var res library.Results
	e.call("POST", "/search", map[string]any{"text": "blacksmith", "filter": map[string]any{"tag": "fantasy"},
		"include": []string{"tags"}}, 200, &res)
	if len(res.Hits) != 1 || res.Hits[0].ID != a.ID || res.Hits[0].Fields["name"] != "Brakka" || res.Hits[0].Tags[0] != "fantasy" {
		t.Fatalf("results = %+v", res)
	}

	res = library.Results{}
	e.call("POST", "/cards/search", map[string]any{"text": "blacksmith", "filter": map[string]any{"tag": "fantasy"}}, 200, &res)
	if len(res.Hits) != 1 || res.Hits[0].ID != a.ID {
		t.Fatalf("/cards/search = %+v", res)
	}

	res = library.Results{}
	e.call("POST", "/search", map[string]any{"page": map[string]any{"size": 1, "cursor": nil}}, 200, &res)
	if len(res.Hits) != 1 || res.NextCursor == nil {
		t.Fatalf("first page = %+v", res)
	}

	res = library.Results{}
	e.call("POST", "/search", map[string]any{"sort": "name", "page": map[string]any{"size": 1, "number": 2},
		"facets": []string{"tags"}}, 200, &res)
	if len(res.Hits) != 1 || res.Hits[0].Fields["name"] != "Carrow" || res.Total != 2 ||
		res.Facets == nil || len(res.Facets.Tags) != 1 {
		t.Fatalf("numbered page = %+v", res)
	}
	e.call("POST", "/search", map[string]any{"sort": "size"}, 422, nil)

	e.call("POST", "/search", map[string]any{"semantic": true}, 400, nil)
	e.call("POST", "/search", map[string]any{"filter": map[string]any{"tag": "a", "archived": true}}, 400, nil)
	e.call("POST", "/search", map[string]any{"filter": map[string]any{"bogus": 1}}, 422, nil)
}

func TestFacetSearchAnswersFromASampleThenFromExactCounts(t *testing.T) {
	e := newEnv(t)
	for range 30 {
		e.createCard(`{"name": "C", "tags": ["all"]}`)
	}
	e.s.facets.sampleOver = 5
	body := map[string]any{"facets": []string{"tags"}, "facets_only": true}
	var res library.Results
	e.call("POST", "/cards/search", body, 200, &res)
	if res.Facets == nil || !res.Facets.Approximate {
		t.Fatalf("first answer = %+v, want sampled counts", res.Facets)
	}
	deadline := time.Now().Add(5 * time.Second)
	for res.Facets.Approximate && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		res = library.Results{}
		e.call("POST", "/cards/search", body, 200, &res)
	}
	if res.Facets.Approximate || len(res.Facets.Tags) != 1 || res.Facets.Tags[0].Cards != 30 {
		t.Fatalf("answer after the recount = %+v, want exact counts", res.Facets)
	}
}

func TestSearchLorebooksRoute(t *testing.T) {
	e := newEnv(t)
	var forge, sea entityResult
	e.call("POST", "/lorebooks", map[string]any{"data": json.RawMessage(`{"name": "Forge", "entries": [{"keys": ["anvil"], "content": "Heavy."}]}`)}, 201, &forge)
	e.call("POST", "/lorebooks", map[string]any{"data": json.RawMessage(`{"name": "Sea", "entries": []}`)}, 201, &sea)

	var res struct {
		Total int `json:"total"`
		Hits  []struct {
			ID      int64 `json:"id"`
			Entries int   `json:"entries"`
		} `json:"hits"`
	}
	e.call("POST", "/lorebooks/search", map[string]any{"text": "anvil"}, 200, &res)
	if res.Total != 1 || res.Hits[0].ID != forge.ID || res.Hits[0].Entries != 1 {
		t.Fatalf("search = %+v", res)
	}
	e.call("POST", "/lorebooks/search", map[string]any{}, 200, &res)
	if res.Total != 2 {
		t.Fatalf("all = %+v", res)
	}
	e.call("POST", "/lorebooks/search", map[string]any{"facets": []string{"tags"}}, 400, nil)
}

func TestSemanticSearchRanksByMeaning(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	inst := e.addBrain(map[string]any{"services": []string{protocol.MethodAIEmbed}})
	e.call("PATCH", "/settings", map[string]any{"embed_instance": inst}, 200, nil)
	near := e.createCard(`{"name": "Brakka"}`)
	far := e.createCard(`{"name": "Lia"}`)
	e.createCard(`{"name": "Grum"}`)
	for id, v := range map[int64][]float32{near.ID: {3, 1}, far.ID: {1, 3}} {
		c, err := e.s.Lib.Cards.Get(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if err := e.s.Lib.SaveEmbeddings(ctx, inst, []library.EmbedText{{CardID: id, RevisionID: c.HeadRevisionID}}, [][]float32{v}); err != nil {
			t.Fatal(err)
		}
	}

	var res library.Results
	e.call("POST", "/cards/search", map[string]any{"text": "abc", "semantic": true}, 200, &res)
	if len(res.Hits) != 2 || res.Hits[0].ID != near.ID || res.Hits[1].ID != far.ID {
		t.Fatalf("hits = %+v", res.Hits)
	}
	e.call("POST", "/cards/search", map[string]any{"text": "abc", "semantic": true, "sort": "name"}, 400, nil)
	e.call("POST", "/lorebooks/search", map[string]any{"text": "abc", "semantic": true}, 400, nil)
}

func TestSemanticSearchWithoutEmbedInstanceIs409(t *testing.T) {
	e := newEnv(t)
	e.call("POST", "/cards/search", map[string]any{"text": "abc", "semantic": true}, 409, nil)
}
