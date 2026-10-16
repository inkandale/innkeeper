package main

import (
	"encoding/json"

	"github.com/davidgibbons/innkeeper/protocol"
)

// settingsCall answers settings.*. The "echo" field and action send the
// draft's api_key back, as a careless plugin might.
func settingsCall(method string, params json.RawMessage, mode string) (any, error) {
	switch method {
	case protocol.MethodSettingsDefaults:
		if mode == "settings-no-defaults" {
			return nil, protocol.MethodNotFound(method)
		}
		return protocol.SettingsDefaultsResult{Defaults: map[string]any{"/prompts/greet": "Hello, {{user}}."}}, nil
	case protocol.MethodSettingsOptions:
		var p protocol.SettingsOptionsParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, protocol.NewError(protocol.CodeInvalidParams, err.Error(), false)
		}
		if key := p.Secrets["api_key"]; p.Field == "/echo" && key != "" {
			return protocol.SettingsOptionsResult{Options: []protocol.SettingsOption{{Value: "v-" + key, Label: "l-" + key}}}, nil
		}
		if p.Field == "/echo-error" {
			return nil, protocol.NewError(-32000, "bad key "+p.Secrets["api_key"], false)
		}
		if p.Field != "/model" {
			return nil, protocol.NewError(protocol.CodeInvalidParams, "no choices for "+p.Field, false)
		}
		url, _ := p.Config["url"].(string)
		if url == "" {
			return nil, protocol.NewError(-32000, "set the url first", false)
		}
		return protocol.SettingsOptionsResult{Options: []protocol.SettingsOption{{Value: url + "/m1", Label: "Model one"}, {Value: url + "/m2"}}}, nil
	case protocol.MethodSettingsRun:
		var p protocol.SettingsRunParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, protocol.NewError(protocol.CodeInvalidParams, err.Error(), false)
		}
		if key := p.Secrets["api_key"]; p.Action == "echo" {
			return protocol.SettingsRunResult{Message: "sent " + key, Checks: []protocol.SettingsCheck{{Name: "echo " + key, Message: "got " + key}}}, nil
		}
		if p.Action != "test" {
			return nil, protocol.NewError(protocol.CodeInvalidParams, "unknown action", false)
		}
		ok := p.Secrets["api_key"] == "hunter2"
		msg := "Works."
		if !ok {
			msg = "The key was refused."
		}
		return protocol.SettingsRunResult{OK: ok, Message: msg, Checks: []protocol.SettingsCheck{{Name: "chat", OK: ok, LatencyMS: 1}}}, nil
	}
	return nil, protocol.MethodNotFound(method)
}
