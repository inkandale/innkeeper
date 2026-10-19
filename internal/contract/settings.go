package contract

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/inkandale/innkeeper/internal/pluginhost"
	"github.com/inkandale/innkeeper/protocol"
)

const (
	checkSettingsDefaultsName = "settings.defaults returns defaults or -32601"
	checkSettingsRunName      = "settings.run of each settings action returns a reply or a plugin error"
)

// emptyDraft is a new instance's form before the owner fills it in.
var emptyDraft = protocol.SettingsDraft{Config: map[string]any{}, Secrets: map[string]string{}}

// checkSettings asks for defaults, then runs each settings action on an
// empty draft.
func checkSettings(ctx context.Context, p *pluginhost.Process, m *protocol.Manifest) []Result {
	return runChecks(ctx, p, []check{
		{checkSettingsDefaultsName, func(ctx context.Context) error { return checkSettingsDefaults(ctx, p) }},
		{checkSettingsRunName, func(ctx context.Context) error { return checkSettingsRuns(ctx, p, m) }},
	})
}

// checkSettingsDefaults wants a reply or -32601: defaults don't depend on the
// config, so the core shows any other error to the owner.
func checkSettingsDefaults(ctx context.Context, p *pluginhost.Process) error {
	var res protocol.SettingsDefaultsResult
	err := call(ctx, p, protocol.MethodSettingsDefaults, nil, &res)
	var rpcErr *protocol.Error
	if errors.As(err, &rpcErr) && rpcErr.Code == protocol.CodeMethodNotFound {
		return nil
	}
	return err
}

// checkSettingsRuns runs each action. An empty draft can only fail a check
// that needs the owner's settings, so an error other than -32601 counts as an
// answer.
func checkSettingsRuns(ctx context.Context, p *pluginhost.Process, m *protocol.Manifest) error {
	for _, id := range slices.Sorted(maps.Keys(m.Settings.Actions)) {
		var res protocol.SettingsRunResult
		err := call(ctx, p, protocol.MethodSettingsRun, protocol.SettingsRunParams{SettingsDraft: emptyDraft, Action: id}, &res)
		if answered(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("%s: %w", id, err)
		}
	}
	return nil
}

// driveSettings asks for defaults and runs each settings action.
func driveSettings(m *protocol.Manifest) func(context.Context, caller) []func(context.Context) error {
	return func(_ context.Context, call caller) []func(context.Context) error {
		out := []func(context.Context) error{func(ctx context.Context) error {
			return call(ctx, protocol.MethodSettingsDefaults, nil, nil)
		}}
		for _, id := range slices.Sorted(maps.Keys(m.Settings.Actions)) {
			params := protocol.SettingsRunParams{SettingsDraft: emptyDraft, Action: id}
			out = append(out, func(ctx context.Context) error { return call(ctx, protocol.MethodSettingsRun, params, nil) })
		}
		return out
	}
}
