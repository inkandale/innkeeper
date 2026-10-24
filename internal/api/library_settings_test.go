package api

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/inkandale/innkeeper/internal/tier"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestLibrarySettingsNeedTheLibrary(t *testing.T) {
	e := newEnv(t)
	e.call("GET", "/libraries/999999/settings", nil, 404, nil)
	e.call("PATCH", "/libraries/999999/settings", map[string]any{"duplicates": "exact"}, 404, nil)
}

func TestLibrarySettingsOverMCP(t *testing.T) {
	e := newEnrichEnv(t)
	s := e.mcpClient(tier.Write)
	tool(t, s, "update_library_settings", map[string]any{"library": 1, "settings": map[string]any{"duplicates": "exact"}}, false)
	got := tool(t, s, "get_library_settings", map[string]any{"library": 1}, false)
	if !strings.Contains(got, `"value":"exact"`) || !strings.Contains(got, `"overridden":true`) {
		t.Fatalf("get_library_settings = %s", got)
	}
	tool(t, s, "update_library_settings", map[string]any{"library": 1, "settings": map[string]any{"duplicates": nil}}, false)
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
