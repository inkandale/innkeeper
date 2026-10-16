package plugins

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/davidgibbons/innkeeper/internal/pluginhost"
	"github.com/davidgibbons/innkeeper/protocol"
)

// Draft is the instance form's unsaved state. Secrets go on top of the saved
// instance's, if Instance is set; a nil value removes one.
type Draft struct {
	Instance *int64
	Config   map[string]any
	Secrets  map[string]*string
}

// Defaults is a plugin's config defaults by JSON pointer, and which of an
// instance's overrides replaced a default that has since changed.
type Defaults struct {
	Defaults map[string]any `json:"defaults"`
	Updated  []string       `json:"updated" doc:"JSON pointers of overrides whose default changed after they were saved."`
}

// settingsInstance is the plugin's process for settings calls: started with
// an empty config, and replaced when the plugin's install changes.
func (r *Registry) settingsInstance(ctx context.Context, plugin string) (pluginhost.Instance, error) {
	m, ok := r.manifest(plugin)
	if !ok {
		return pluginhost.Instance{}, fmt.Errorf("%w: %s", ErrUnknownPlugin, plugin)
	}
	if !m.Has("settings") {
		return pluginhost.Instance{}, fmt.Errorf("%w: plugin %s has no settings calls", ErrIncapable, plugin)
	}
	var changed time.Time
	err := r.pool.QueryRow(ctx, `SELECT changed_at FROM plugin_install WHERE name = $1`, plugin).Scan(&changed)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return pluginhost.Instance{}, err
	}
	return pluginhost.Instance{ID: "settings-" + plugin, Version: strconv.FormatInt(changed.UnixMicro(), 10), Manifest: m}, nil
}

// draft resolves d into what a settings call sends.
func (r *Registry) draft(ctx context.Context, m *protocol.Manifest, d Draft) (protocol.SettingsDraft, error) {
	out := protocol.SettingsDraft{Config: d.Config, Secrets: map[string]string{}}
	if out.Config == nil {
		out.Config = map[string]any{}
	}
	if d.Instance != nil {
		var plugin string
		var sealed []byte
		err := r.pool.QueryRow(ctx, `SELECT plugin, secrets FROM plugin_instance WHERE id = $1`, *d.Instance).Scan(&plugin, &sealed)
		if errors.Is(err, pgx.ErrNoRows) {
			return out, fmt.Errorf("%w: instance %d", ErrNotFound, *d.Instance)
		}
		if err != nil {
			return out, err
		}
		// Another plugin's saved secrets must never reach this one.
		if plugin != m.Name {
			return out, fmt.Errorf("%w: instance %d belongs to %s, not %s", ErrInvalid, *d.Instance, plugin, m.Name)
		}
		if out.Secrets, err = r.box.Open(sealed); err != nil {
			return out, err
		}
	}
	for name, v := range d.Secrets {
		if err := checkSecret(m, name); err != nil {
			return out, err
		}
		if v == nil {
			delete(out.Secrets, name)
		} else {
			out.Secrets[name] = *v
		}
	}
	return out, nil
}

// Defaults joins the config schema's defaults with the plugin's. With an
// instance, it also lists the overrides whose default has changed.
func (r *Registry) Defaults(ctx context.Context, mgr *pluginhost.Manager, plugin string, instance *int64) (Defaults, error) {
	m, ok := r.manifest(plugin)
	if !ok {
		return Defaults{}, fmt.Errorf("%w: %s", ErrUnknownPlugin, plugin)
	}
	var hashes map[string]string
	if instance != nil {
		var owner string
		err := r.pool.QueryRow(ctx, `SELECT plugin, default_hashes FROM plugin_instance WHERE id = $1`, *instance).Scan(&owner, &hashes)
		if errors.Is(err, pgx.ErrNoRows) {
			return Defaults{}, fmt.Errorf("%w: instance %d", ErrNotFound, *instance)
		}
		if err != nil {
			return Defaults{}, err
		}
		if owner != plugin {
			return Defaults{}, fmt.Errorf("%w: instance %d belongs to %s, not %s", ErrInvalid, *instance, owner, plugin)
		}
	}
	out := Defaults{Defaults: schemaDefaults(m.Config), Updated: []string{}}
	if m.Has("settings") {
		inst, err := r.settingsInstance(ctx, plugin)
		if err != nil {
			return out, err
		}
		var res protocol.SettingsDefaultsResult
		err = mgr.Call(ctx, inst, protocol.MethodSettingsDefaults, nil, &res)
		var rpcErr *protocol.Error
		if err != nil && !(errors.As(err, &rpcErr) && rpcErr.Code == protocol.CodeMethodNotFound) {
			return out, err
		}
		maps.Copy(out.Defaults, res.Defaults)
	}
	for p, h := range hashes {
		if d, ok := out.Defaults[p]; ok && hashDefault(d) != h {
			out.Updated = append(out.Updated, p)
		}
	}
	slices.Sort(out.Updated)
	return out, nil
}

// Options asks the plugin for the choices of the config field at pointer.
func (r *Registry) Options(ctx context.Context, mgr *pluginhost.Manager, plugin, pointer string, d Draft) (protocol.SettingsOptionsResult, error) {
	var res protocol.SettingsOptionsResult
	inst, err := r.settingsInstance(ctx, plugin)
	if err != nil {
		return res, err
	}
	draft, err := r.draft(ctx, inst.Manifest, d)
	if err != nil {
		return res, err
	}
	err = mgr.Call(ctx, inst, protocol.MethodSettingsOptions, protocol.SettingsOptionsParams{SettingsDraft: draft, Field: pointer}, &res)
	redact := redactor(draft.Secrets)
	for i, o := range res.Options {
		res.Options[i] = protocol.SettingsOption{Value: redact(o.Value), Label: redact(o.Label)}
	}
	return res, redactError(err, redact)
}

// RunSetting runs one of the plugin's settings.actions against the draft.
func (r *Registry) RunSetting(ctx context.Context, mgr *pluginhost.Manager, plugin, action string, d Draft) (protocol.SettingsRunResult, error) {
	var res protocol.SettingsRunResult
	inst, err := r.settingsInstance(ctx, plugin)
	if err != nil {
		return res, err
	}
	if _, ok := inst.Manifest.Settings.Actions[action]; !ok {
		return res, fmt.Errorf("%w: plugin %s has no settings action %q", ErrInvalid, plugin, action)
	}
	draft, err := r.draft(ctx, inst.Manifest, d)
	if err != nil {
		return res, err
	}
	err = mgr.Call(ctx, inst, protocol.MethodSettingsRun, protocol.SettingsRunParams{SettingsDraft: draft, Action: action}, &res)
	redact := redactor(draft.Secrets)
	res.Message = redact(res.Message)
	for i := range res.Checks {
		res.Checks[i].Message = redact(res.Checks[i].Message)
	}
	return res, redactError(err, redact)
}

// redactor replaces each secret in a plugin's reply, which goes to the
// browser, with "[redacted]".
func redactor(secrets map[string]string) func(string) string {
	var values []string
	for _, v := range secrets {
		if v != "" {
			values = append(values, v)
		}
	}
	// Longest first, so a secret inside another doesn't leave the rest showing.
	slices.SortFunc(values, func(a, b string) int { return len(b) - len(a) })
	var pairs []string
	for _, v := range values {
		pairs = append(pairs, v, "[redacted]")
	}
	return strings.NewReplacer(pairs...).Replace
}

// redactError redacts the message of a plugin's RPC error in place.
func redactError(err error, redact func(string) string) error {
	var rpcErr *protocol.Error
	if errors.As(err, &rpcErr) {
		rpcErr.Message = redact(rpcErr.Message)
	}
	return err
}
