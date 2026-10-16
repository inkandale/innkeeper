package plugins

import (
	"context"
	"errors"
	"slices"
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
// api_key secret, a url default, and a Test button.
func settingsRegistry(t *testing.T) (*Registry, *pluginhost.Manager) {
	t.Helper()
	m, err := protocol.LoadManifest(testutil.FakePluginWith(t, []string{"settings"}))
	if err != nil {
		t.Fatal(err)
	}
	m.Name, m.Secrets = "conf", []string{"api_key"}
	m.Command = append(m.Command, "-name=conf")
	m.Config = map[string]any{"type": "object", "properties": map[string]any{
		"url":     map[string]any{"type": "string", "default": "http://localhost"},
		"prompts": map[string]any{"type": "object", "properties": map[string]any{"greet": map[string]any{"type": "string"}}},
	}}
	m.Settings.Actions = map[string]protocol.SettingsAction{"test": {Label: "Test connection"}}
	other := &protocol.Manifest{Name: "other", Version: "1.0.0", Secrets: []string{"api_key"}}
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
	if _, err := reg.Defaults(ctx, mgr, "conf", &id); !errors.Is(err, ErrInvalid) {
		t.Fatalf("defaults: %v, want ErrInvalid", err)
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
