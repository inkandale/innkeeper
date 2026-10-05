package api

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/davidgibbons/innkeeper/internal/tier"
	"github.com/davidgibbons/innkeeper/protocol"
)

func TestPluginTools(t *testing.T) {
	e := newEnv(t)
	var list []PluginTool
	e.call("GET", "/plugin-tools", nil, 200, &list)
	tiers := map[string]string{}
	for _, tl := range list {
		if tl.InstanceID == e.importID {
			tiers[tl.Name] = tl.Tier
		}
	}
	if tiers["echo"] != "read" || tiers["note"] != "write" || tiers["wipe"] != "destructive" || tiers["untiered"] != "destructive" {
		t.Fatalf("tools = %v", tiers)
	}

	read := e.tokenWith(tier.Read)
	path := func(name string) string { return fmt.Sprintf("/instances/%d/tools/%s", e.importID, name) }
	body := map[string]any{"arguments": map[string]any{"text": "hi"}}
	r := e.send("POST", path("echo"), body, "Authorization", "Bearer "+read)
	if r.Status != 200 || !strings.Contains(string(r.Body), "echo: hi") {
		t.Fatalf("echo: %s", r)
	}
	for _, name := range []string{"note", "untiered"} {
		if r := e.send("POST", path(name), body, "Authorization", "Bearer "+read); r.Status != http.StatusForbidden {
			t.Fatalf("read token calling %s: %s", name, r)
		}
	}
	e.call("POST", path("missing"), body, 404, nil)
	var res protocol.MCPCallResult
	e.call("POST", path("wipe"), body, 200, &res)
	if res.IsError || res.Content[0].Text != "wipe: hi" {
		t.Fatalf("wipe = %+v", res)
	}
	if r := e.send("POST", path("crash"), body, "Authorization", "Bearer "+e.token); r.Status < 500 {
		t.Fatalf("crash: %s", r)
	}
}
