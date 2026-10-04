package api

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/davidgibbons/innkeeper/internal/plugins"
)

func TestInstanceRoutes(t *testing.T) {
	e := newEnv(t)
	var installed []pluginOut
	e.call("GET", "/plugins", nil, 200, &installed)
	if len(installed) != 2 || installed[0].Name != "fake" || installed[1].Name != "vault" || installed[1].Secrets[0] != "api_key" ||
		!slices.Equal(installed[0].Formats, []string{"fake"}) {
		t.Fatalf("plugins = %+v", installed)
	}

	var inst plugins.InstanceInfo
	r := e.call("POST", "/instances", map[string]any{"plugin": "vault", "name": "Vault",
		"config": map[string]any{"url": "http://example.com"}, "secrets": map[string]string{"api_key": "hunter2"}}, 201, &inst)
	if strings.Contains(string(r.Body), "hunter2") || len(inst.Secrets) != 1 || inst.Config["url"] != "http://example.com" {
		t.Fatalf("created %s", r)
	}
	e.call("POST", "/instances", map[string]any{"plugin": "vault", "name": "Vault"}, 409, nil)
	e.call("POST", "/instances", map[string]any{"plugin": "missing", "name": "X"}, 400, nil)
	e.call("POST", "/instances", map[string]any{"plugin": "vault", "name": "Y", "secrets": map[string]string{"password": "x"}}, 400, nil)
	e.call("POST", "/instances", map[string]any{"plugin": "vault", "name": "Z", "owner_subject": ""}, 400, nil)

	path := fmt.Sprintf("/instances/%d", inst.ID)
	e.markBroken(inst.ID, "bad key")
	r = e.call("PATCH", path, map[string]any{"name": "Vault 2", "secrets": map[string]any{"api_key": nil}}, 200, &inst)
	if !strings.Contains(string(r.Body), `"secrets":[]`) {
		t.Fatalf("patched %s", r)
	}
	if inst.Name != "Vault 2" || len(inst.Secrets) != 0 || inst.BrokenError != nil || inst.Config["url"] != "http://example.com" {
		t.Fatalf("patched %+v", inst)
	}

	var list []plugins.InstanceInfo
	e.call("GET", "/instances", nil, 200, &list)
	if len(list) != 3 || list[2].Name != "Vault 2" {
		t.Fatalf("instances = %+v", list)
	}
	e.call("DELETE", path, nil, 204, nil)
	e.call("GET", path, nil, 404, nil)
	e.call("PATCH", path, map[string]any{"name": "X"}, 404, nil)
}
