package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/inkandale/innkeeper/internal/plugins"
	"github.com/inkandale/innkeeper/internal/tier"
	"github.com/inkandale/innkeeper/protocol"
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

// A plugin that hangs listing its tools mustn't hold up everyone else's.
func TestHungPluginTools(t *testing.T) {
	e := newEnv(t)
	if _, err := e.s.Registry.CreateInstance(context.Background(), plugins.NewInstance{Plugin: "fake", Name: "Hung",
		Config: map[string]any{"hang_tools": true}}); err != nil {
		t.Fatal(err)
	}
	for _, limit := range []time.Duration{5 * time.Second, time.Second} {
		start := time.Now()
		var list []PluginTool
		e.call("GET", "/plugin-tools", nil, 200, &list)
		if took := time.Since(start); took > limit {
			t.Fatalf("listing took %s", took)
		}
		for _, tl := range list {
			if tl.InstanceName == "Hung" {
				t.Fatal("listed the hung instance's tools")
			}
		}
		if len(list) == 0 {
			t.Fatal("the working instances' tools are missing")
		}
	}
}
