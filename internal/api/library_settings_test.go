package api

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/inkandale/innkeeper/internal/library"
	"github.com/inkandale/innkeeper/internal/plugins"
	"github.com/inkandale/innkeeper/internal/tier"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestLibraryScoreInstanceOverride(t *testing.T) {
	e := newEnrichEnv(t)
	second, err := e.s.Registry.CreateInstance(context.Background(), plugins.NewInstance{Plugin: "judge", Name: new("Second")})
	if err != nil {
		t.Fatal(err)
	}
	var archive library.LibrarySummary
	e.call("POST", "/libraries", map[string]any{"name": "Archive"}, 201, &archive)
	var archived entityOut
	e.call("POST", "/cards", map[string]any{"library": archive.ID, "data": map[string]any{"name": "High", "description": strings.Repeat("x", 90)}}, 201, &archived)
	curated := e.card("High", strings.Repeat("x", 90))
	e.evaluate(e.instID, curated, archived.ID)
	e.call("PATCH", "/settings", map[string]any{"score_instance": e.instID}, 200, nil)

	path := fmt.Sprintf("/libraries/%d/settings", archive.ID)
	var got librarySettings
	e.call("GET", path, nil, 200, &got)
	if s := got.ScoreInstance; s.Value == nil || *s.Value != e.instID || s.Default == nil || *s.Default != e.instID || s.Overridden {
		t.Fatalf("inherited = %+v", s)
	}

	e.call("PATCH", path, map[string]any{"score_instance": second}, 200, &got)
	if s := got.ScoreInstance; *s.Value != second || *s.Default != e.instID || !s.Overridden {
		t.Fatalf("overridden = %+v", s)
	}
	// The archive scores from the second instance, which has no values; library 1 still uses the first.
	quality := map[string]any{"enrichment": "quality", "gte": 50}
	if r := e.search(map[string]any{"library": archive.ID, "filter": quality}, 200); len(r.Hits) != 0 {
		t.Fatalf("archive hits %+v", r.Hits)
	}
	if r := e.search(map[string]any{"filter": quality}, 200); len(r.Hits) != 1 || r.Hits[0].ID != curated {
		t.Fatalf("library 1 hits %+v", r.Hits)
	}
	var keys enrichmentKeysOut
	e.call("GET", fmt.Sprintf("/enrichments/keys?library=%d", archive.ID), nil, 200, &keys.Body)
	if keys.Body.InstanceID == nil || *keys.Body.InstanceID != second {
		t.Fatalf("archive keys = %+v", keys.Body)
	}

	// Clearing the override follows the default again, including a changed one.
	e.call("PATCH", path, map[string]any{"score_instance": nil}, 200, &got)
	e.call("PATCH", "/settings", map[string]any{"score_instance": second}, 200, nil)
	e.call("GET", path, nil, 200, &got)
	if s := got.ScoreInstance; *s.Value != second || s.Overridden {
		t.Fatalf("after reset = %+v", s)
	}

	e.call("PATCH", path, map[string]any{"score_instance": e.importID}, 400, nil)
	e.call("PATCH", path, map[string]any{"score_instance": 999999}, 404, nil)
	e.call("GET", "/libraries/999999/settings", nil, 404, nil)
	e.call("PATCH", "/libraries/999999/settings", map[string]any{"score_instance": second}, 404, nil)
}

func TestLibrarySettingsOverMCP(t *testing.T) {
	e := newEnrichEnv(t)
	s := e.mcpClient(tier.Write)
	tool(t, s, "update_library_settings", map[string]any{"library": 1, "score_instance": e.instID}, false)
	got := tool(t, s, "get_library_settings", map[string]any{"library": 1}, false)
	if !strings.Contains(got, fmt.Sprintf(`"value":%d`, e.instID)) || !strings.Contains(got, `"overridden":true`) {
		t.Fatalf("get_library_settings = %s", got)
	}
	tool(t, s, "update_library_settings", map[string]any{"library": 1, "score_instance": nil}, false)
	if got := tool(t, s, "get_library_settings", map[string]any{"library": 1}, false); !strings.Contains(got, `"overridden":false`) {
		t.Fatalf("after reset = %s", got)
	}
	read, err := e.mcpClient(tier.Read).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(read.Tools, func(tl *sdk.Tool) bool { return tl.Name == "update_library_settings" }) {
		t.Fatal("a read token sees update_library_settings")
	}
}
