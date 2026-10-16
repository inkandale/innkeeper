package api

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/davidgibbons/innkeeper/internal/plugins"
	"github.com/davidgibbons/innkeeper/internal/tier"
	"github.com/davidgibbons/innkeeper/protocol"
)

// addConf adds "conf", the fake plugin as a settings plugin with a Test button.
func (e *testEnv) addConf() {
	conf := *e.manifests["fake"]
	conf.Name, conf.Capabilities, conf.Secrets = "conf", []string{"settings"}, []string{"api_key"}
	conf.Command = []string{conf.Command[0], "-caps=settings", "-name=conf"}
	conf.Config = map[string]any{"type": "object", "properties": map[string]any{
		"url":     map[string]any{"type": "string", "default": "http://localhost"},
		"prompts": map[string]any{"type": "object", "properties": map[string]any{"greet": map[string]any{"type": "string"}}},
	}}
	conf.Settings.Actions = map[string]protocol.SettingsAction{"test": {Label: "Test connection"}}
	e.manifests["conf"] = &conf
}

func TestPluginSettingsRoutes(t *testing.T) {
	e := newEnv(t)
	e.addConf()

	var listed []pluginOut
	e.call("GET", "/plugins", nil, 200, &listed)
	var conf pluginOut
	for _, p := range listed {
		if p.Name == "conf" {
			conf = p
		}
	}
	if conf.SettingsActions["test"] != "Test connection" {
		t.Fatalf("settings_actions = %v", conf.SettingsActions)
	}

	var inst plugins.InstanceInfo
	e.call("POST", "/instances", map[string]any{"plugin": "conf", "name": "C",
		"config":        map[string]any{"prompts": map[string]any{"greet": "Yo"}},
		"default_bases": map[string]any{"/prompts/greet": "an older default"},
		"secrets":       map[string]string{"api_key": "hunter2"}}, 201, &inst)

	var defs plugins.Defaults
	e.call("GET", fmt.Sprintf("/plugins/conf/settings/defaults?instance=%d", inst.ID), nil, 200, &defs)
	if defs.Defaults["/prompts/greet"] != "Hello, {{user}}." || len(defs.Updated) != 1 {
		t.Fatalf("defaults = %+v", defs)
	}

	var run protocol.SettingsRunResult
	r := e.call("POST", "/plugins/conf/settings/run", map[string]any{"action": "test", "instance": inst.ID}, 200, &run)
	if !run.OK || strings.Contains(string(r.Body), "hunter2") {
		t.Fatalf("run = %s", r)
	}
	var opts protocol.SettingsOptionsResult
	e.call("POST", "/plugins/conf/settings/options", map[string]any{"field": "/model", "config": map[string]any{"url": "http://draft"}}, 200, &opts)
	if len(opts.Options) != 2 {
		t.Fatalf("options = %+v", opts)
	}
	// The plugin's own message reaches the owner.
	r = e.call("POST", "/plugins/conf/settings/options", map[string]any{"field": "/model"}, 502, nil)
	if !strings.Contains(string(r.Body), "set the url first") {
		t.Fatalf("options error = %s", r)
	}
	e.call("POST", "/plugins/conf/settings/run", map[string]any{"action": "deploy"}, 400, nil)
	e.call("POST", "/plugins/vault/settings/run", map[string]any{"action": "test"}, 400, nil)
}

// A draft can point the plugin anywhere and send the saved key there, so
// options and run need the tier that can change the instance.
func TestPluginSettingsCallsNeedDestructive(t *testing.T) {
	e := newEnv(t)
	e.addConf()
	read := "Bearer " + e.tokenWith(tier.Read)
	for _, path := range []string{"/plugins/conf/settings/run", "/plugins/conf/settings/options"} {
		if r := e.send("POST", path, map[string]any{"action": "test", "field": "/model"}, "Authorization", read); r.Status != 403 {
			t.Errorf("%s with read tier: %s", path, r)
		}
	}
	if r := e.send("GET", "/plugins/conf/settings/defaults", nil, "Authorization", read); r.Status != 200 {
		t.Errorf("defaults with read tier: %s", r)
	}
}

// A login provider's saved secrets decide who logs in, so an OAuth client
// can't send them anywhere through a draft.
func TestPluginSettingsCallsKeepOAuthClientsFromLoginSecrets(t *testing.T) {
	e := newEnv(t)
	e.addConf()
	e.manifests["conf"].Capabilities = []string{"settings", "auth"}
	var inst plugins.InstanceInfo
	e.call("POST", "/instances", map[string]any{"plugin": "conf", "name": "C", "secrets": map[string]string{"api_key": "hunter2"}}, 201, &inst)
	oauth := "Bearer " + e.oauthToken(tier.Destructive)
	for _, path := range []string{"/plugins/conf/settings/run", "/plugins/conf/settings/options"} {
		body := map[string]any{"action": "test", "instance": inst.ID}
		if strings.HasSuffix(path, "options") {
			body = map[string]any{"field": "/model", "instance": inst.ID}
		}
		if r := e.send("POST", path, body, "Authorization", oauth); r.Status != http.StatusForbidden {
			t.Errorf("%s from an OAuth client: %s", path, r)
		}
	}
}

func TestInstanceDefaultBasesNeedPointers(t *testing.T) {
	e := newEnv(t)
	e.addConf()
	body := map[string]any{"plugin": "conf", "name": "C",
		"config": map[string]any{"url": "http://mine"}, "default_bases": map[string]any{"url": "http://localhost"}}
	e.call("POST", "/instances", body, 400, nil)
	body["default_bases"] = map[string]any{"/url": "http://localhost"}
	var inst plugins.InstanceInfo
	e.call("POST", "/instances", body, 201, &inst)
	e.call("PATCH", fmt.Sprintf("/instances/%d", inst.ID), map[string]any{
		"config": map[string]any{"url": "http://other"}, "default_bases": map[string]any{"url": "x"}}, 400, nil)
}

func TestListInstancesOffersRejectsUnknownService(t *testing.T) {
	e := newEnv(t)
	var list []plugins.InstanceInfo
	e.call("GET", "/instances?offers=ai.chat", nil, 200, &list)
	if len(list) != 0 {
		t.Fatalf("instances offering ai.chat = %+v", list)
	}
	e.call("GET", "/instances?offers=cards.list", nil, 400, nil)
}
