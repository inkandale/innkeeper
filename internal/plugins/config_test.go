package plugins

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/inkandale/innkeeper/internal/blobstore"
	"github.com/inkandale/innkeeper/internal/secrets"
	"github.com/inkandale/innkeeper/internal/testdb"
	"github.com/inkandale/innkeeper/protocol"
)

// configRegistry has one plugin, app, whose config schema requires url and,
// when auth is basic, username.
func configRegistry(t *testing.T) *Registry {
	t.Helper()
	box, err := secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	m := &protocol.Manifest{Name: "app", Version: "1.0.0", MultipleInstances: true, Config: map[string]any{
		"type":     "object",
		"required": []any{"url"},
		"properties": map[string]any{
			"url":      map[string]any{"type": "string"},
			"port":     map[string]any{"type": "integer", "default": 8000},
			"auth":     map[string]any{"type": "string", "enum": []any{"none", "basic"}},
			"username": map[string]any{"type": "string"},
			"provider": map[string]any{"type": "integer", "format": "instance", "x-offers": "ai.chat"},
		},
		"if":   map[string]any{"properties": map[string]any{"auth": map[string]any{"const": "basic"}}},
		"then": map[string]any{"required": []any{"username"}},
	}}
	return New(testdb.New(t), map[string]*protocol.Manifest{"app": m}, &blobstore.Store{Name: "library", Root: t.TempDir()}, box)
}

func TestCreateInstanceRejectsConfigThatBreaksTheSchema(t *testing.T) {
	reg := configRegistry(t)
	for _, config := range []map[string]any{{"port": "eight"}, {"auth": "oauth"}} {
		_, err := reg.CreateInstance(context.Background(), NewInstance{Plugin: "app", Config: config})
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("create with %v: %v, want ErrInvalid", config, err)
		}
	}
	_, err := reg.CreateInstance(context.Background(), NewInstance{Plugin: "app", Config: map[string]any{"port": "eight"}})
	if !strings.Contains(err.Error(), "port") {
		t.Fatalf("error doesn't name the field: %v", err)
	}
}

// A missing required key is needs_setup, not an error.
func TestCreateInstanceAcceptsConfigMissingRequiredKeys(t *testing.T) {
	reg := configRegistry(t)
	for _, config := range []map[string]any{nil, {"auth": "basic"}, {"url": "http://app", "port": float64(8080), "provider": float64(3)}} {
		if _, err := reg.CreateInstance(context.Background(), NewInstance{Plugin: "app", Config: config}); err != nil {
			t.Fatalf("create with %v: %v", config, err)
		}
		if _, err := reg.pool.Exec(context.Background(), `DELETE FROM plugin_instance`); err != nil {
			t.Fatal(err)
		}
	}
}

func TestUpdateInstanceRejectsConfigThatBreaksTheSchema(t *testing.T) {
	ctx := context.Background()
	reg := configRegistry(t)
	id, err := reg.CreateInstance(ctx, NewInstance{Plugin: "app", Config: map[string]any{"url": "http://app"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.UpdateInstance(ctx, id, InstanceEdit{Config: map[string]any{"url": 5}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("update with a number url: %v, want ErrInvalid", err)
	}
	name := "y"
	if err := reg.UpdateInstance(ctx, id, InstanceEdit{Name: &name}); err != nil {
		t.Fatalf("update without config: %v", err)
	}
	if err := reg.UpdateInstance(ctx, id, InstanceEdit{Config: map[string]any{"url": "http://other"}}); err != nil {
		t.Fatalf("update with good config: %v", err)
	}
}
