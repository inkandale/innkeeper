package plugins

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/davidgibbons/innkeeper/internal/blobstore"
	"github.com/davidgibbons/innkeeper/internal/pluginhost"
	"github.com/davidgibbons/innkeeper/internal/secrets"
	"github.com/davidgibbons/innkeeper/internal/testdb"
	"github.com/davidgibbons/innkeeper/internal/testutil"
	"github.com/davidgibbons/innkeeper/protocol"
)

// settingsRegistry has "conf", the fake plugin as a settings plugin with an
// api_key secret, a url default, a Test button, and an Echo button. "other"
// has no settings calls and no command.
func settingsRegistry(t *testing.T, args ...string) (*Registry, *pluginhost.Manager) {
	t.Helper()
	m, err := protocol.LoadManifest(testutil.FakePluginWith(t, []string{"settings"}, args...))
	if err != nil {
		t.Fatal(err)
	}
	m.Name, m.Secrets = "conf", []string{"api_key"}
	m.Command = append(m.Command, "-name=conf")
	m.Config = map[string]any{"type": "object", "properties": map[string]any{
		"url":     map[string]any{"type": "string", "default": "http://localhost"},
		"prompts": map[string]any{"type": "object", "properties": map[string]any{"greet": map[string]any{"type": "string"}}},
	}}
	m.Settings.Actions = map[string]protocol.SettingsAction{"test": {Label: "Test connection"}, "echo": {Label: "Echo"}}
	other := &protocol.Manifest{Name: "other", Version: "1.0.0", Secrets: []string{"api_key"},
		Config: map[string]any{"type": "object", "properties": map[string]any{"url": map[string]any{"type": "string", "default": "http://other"}}}}
	box, err := secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	store := &blobstore.Store{Name: "library", Root: t.TempDir()}
	reg := New(testdb.New(t), map[string]*protocol.Manifest{"conf": m, "other": other}, store, box)
	mgr := pluginhost.NewManager(nil, time.Minute, quiet)
	t.Cleanup(mgr.Close)
	return reg, mgr
}

func TestDefaultsJoinsSchemaAndPluginAndMarksUpdated(t *testing.T) {
	ctx := context.Background()
	reg, mgr := settingsRegistry(t)
	id, err := reg.CreateInstance(ctx, NewInstance{Plugin: "conf", Name: "C",
		Config:       map[string]any{"url": "http://mine", "prompts": map[string]any{"greet": "Yo"}},
		DefaultBases: map[string]any{"/url": "http://localhost", "/prompts/greet": "an older default"}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := reg.Defaults(ctx, mgr, "conf", &id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Defaults["/url"] != "http://localhost" || got.Defaults["/prompts/greet"] != "Hello, {{user}}." {
		t.Fatalf("defaults = %v", got.Defaults)
	}
	if !slices.Equal(got.Updated, []string{"/prompts/greet"}) {
		t.Fatalf("updated = %v, want only /prompts/greet", got.Updated)
	}
}

func TestSettingsCallsLayerDraftSecretsOnSaved(t *testing.T) {
	ctx := context.Background()
	reg, mgr := settingsRegistry(t)
	id, err := reg.CreateInstance(ctx, NewInstance{Plugin: "conf", Name: "C", Secrets: map[string]string{"api_key": "hunter2"}})
	if err != nil {
		t.Fatal(err)
	}
	res, err := reg.RunSetting(ctx, mgr, "conf", "test", Draft{Instance: &id})
	if err != nil || !res.OK {
		t.Fatalf("saved key: %+v, %v", res, err)
	}
	wrong := "nope"
	if res, _ := reg.RunSetting(ctx, mgr, "conf", "test", Draft{Instance: &id, Secrets: map[string]*string{"api_key": &wrong}}); res.OK {
		t.Fatal("a typed key didn't replace the saved one")
	}
	if res, _ := reg.RunSetting(ctx, mgr, "conf", "test", Draft{Instance: &id, Secrets: map[string]*string{"api_key": nil}}); res.OK {
		t.Fatal("a removed key was still sent")
	}
	opts, err := reg.Options(ctx, mgr, "conf", "/model", Draft{Config: map[string]any{"url": "http://draft"}})
	if err != nil || len(opts.Options) != 2 || opts.Options[0].Value != "http://draft/m1" {
		t.Fatalf("options = %+v, %v", opts, err)
	}
}

func TestSettingsCallsRefuseAnotherPluginsInstance(t *testing.T) {
	ctx := context.Background()
	reg, mgr := settingsRegistry(t)
	id, err := reg.CreateInstance(ctx, NewInstance{Plugin: "other", Name: "O", Secrets: map[string]string{"api_key": "hunter2"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reg.RunSetting(ctx, mgr, "conf", "test", Draft{Instance: &id}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
	// A nil manager panics if Defaults calls the plugin before checking.
	if _, err := reg.Defaults(ctx, nil, "conf", &id); !errors.Is(err, ErrInvalid) {
		t.Fatalf("defaults: %v, want ErrInvalid", err)
	}
	missing := id + 1000
	if _, err := reg.Defaults(ctx, nil, "conf", &missing); !errors.Is(err, ErrNotFound) {
		t.Fatalf("defaults for a missing instance: %v, want ErrNotFound", err)
	}
	_, err = reg.RunSetting(ctx, mgr, "conf", "test", Draft{Instance: &missing})
	if !errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), fmt.Sprintf("instance %d", missing)) {
		t.Fatalf("run for a missing instance: %v, want ErrNotFound naming it", err)
	}
	if _, err := reg.RunSetting(ctx, mgr, "conf", "deploy", Draft{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("undeclared action: %v, want ErrInvalid", err)
	}
	if _, err := reg.RunSetting(ctx, mgr, "conf", "test", Draft{Secrets: map[string]*string{"password": new(string)}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown secret: %v, want ErrInvalid", err)
	}
	if _, err := reg.RunSetting(ctx, mgr, "other", "test", Draft{}); !errors.Is(err, ErrIncapable) {
		t.Fatalf("plugin without settings: %v, want ErrIncapable", err)
	}
}

// A nil manager panics if Defaults starts a process.
func TestDefaultsWithoutSettingsCapabilityStartsNoProcess(t *testing.T) {
	reg, _ := settingsRegistry(t)
	got, err := reg.Defaults(context.Background(), nil, "other", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Defaults) != 1 || got.Defaults["/url"] != "http://other" {
		t.Fatalf("defaults = %v, want only the schema's", got.Defaults)
	}
}

func TestDefaultsFallsBackToSchemaWhenPluginHasNone(t *testing.T) {
	reg, mgr := settingsRegistry(t, "-mode=settings-no-defaults")
	got, err := reg.Defaults(context.Background(), mgr, "conf", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Defaults) != 1 || got.Defaults["/url"] != "http://localhost" {
		t.Fatalf("defaults = %v, want only the schema's", got.Defaults)
	}
}

func TestSettingsCallsRedactSecretsThePluginEchoes(t *testing.T) {
	ctx := context.Background()
	reg, mgr := settingsRegistry(t)
	id, err := reg.CreateInstance(ctx, NewInstance{Plugin: "conf", Name: "C", Secrets: map[string]string{"api_key": "hunter2"}})
	if err != nil {
		t.Fatal(err)
	}
	typed := "typed-key"
	for name, d := range map[string]Draft{"saved": {Instance: &id}, "typed": {Instance: &id, Secrets: map[string]*string{"api_key": &typed}}} {
		key := "hunter2"
		if name == "typed" {
			key = typed
		}
		res, err := reg.RunSetting(ctx, mgr, "conf", "echo", d)
		if err != nil {
			t.Fatal(err)
		}
		if res.Message != "sent [redacted]" || res.Checks[0].Message != "got [redacted]" {
			t.Errorf("%s: run = %+v", name, res)
		}
		opts, err := reg.Options(ctx, mgr, "conf", "/echo", d)
		if err != nil {
			t.Fatal(err)
		}
		if opts.Options[0] != (protocol.SettingsOption{Value: "v-[redacted]", Label: "l-[redacted]"}) {
			t.Errorf("%s: options = %+v", name, opts)
		}
		_, err = reg.Options(ctx, mgr, "conf", "/echo-error", d)
		var rpcErr *protocol.Error
		if !errors.As(err, &rpcErr) || rpcErr.Message != "bad key [redacted]" || strings.Contains(err.Error(), key) {
			t.Errorf("%s: options error = %v", name, err)
		}
	}
}
