package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/inkandale/innkeeper/internal/library"
	"github.com/inkandale/innkeeper/internal/plugins"
	"github.com/inkandale/innkeeper/internal/tier"
	"github.com/inkandale/innkeeper/protocol"
)

func TestInstanceRoutes(t *testing.T) {
	e := newEnv(t)
	var installed []pluginOut
	e.call("GET", "/plugins", nil, 200, &installed)
	if len(installed) != 3 || installed[0].Name != "fake" || installed[2].Name != "vault" || installed[2].Title != "Key Vault" || installed[2].Secrets[0] != "api_key" ||
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
	var unnamed plugins.InstanceInfo
	e.call("POST", "/instances", map[string]any{"plugin": "vault"}, 201, &unnamed)
	if unnamed.Name != nil || unnamed.Label != "Key Vault" {
		t.Fatalf("unnamed instance = %+v", unnamed)
	}
	e.call("POST", "/instances", map[string]any{"plugin": "vault"}, 409, nil)
	e.call("DELETE", fmt.Sprintf("/instances/%d", unnamed.ID), nil, 204, nil)
	e.call("POST", "/instances", map[string]any{"plugin": "missing", "name": "X"}, 400, nil)
	e.call("POST", "/instances", map[string]any{"plugin": "vault", "name": "Y", "secrets": map[string]string{"password": "x"}}, 400, nil)
	e.call("POST", "/instances", map[string]any{"plugin": "vault", "name": "Z", "owner_subject": ""}, 400, nil)

	path := fmt.Sprintf("/instances/%d", inst.ID)
	e.markBroken(inst.ID, "bad key")
	r = e.call("PATCH", path, map[string]any{"name": "Vault 2", "secrets": map[string]any{"api_key": nil}}, 200, &inst)
	if !strings.Contains(string(r.Body), `"secrets":[]`) {
		t.Fatalf("patched %s", r)
	}
	if inst.Label != "Vault 2" || len(inst.Secrets) != 0 || inst.BrokenError != nil || inst.Config["url"] != "http://example.com" {
		t.Fatalf("patched %+v", inst)
	}

	var raw map[string]any
	e.call("PATCH", path, map[string]any{"name": "Vault 2"}, 200, &raw)
	if _, ok := raw["auto_bind_exact"]; ok {
		t.Fatalf("instance still has auto_bind_exact: %v", raw)
	}
	e.call("PATCH", path, map[string]any{"auto_bind_exact": true}, 422, nil)

	var list []plugins.InstanceInfo
	e.call("GET", "/instances", nil, 200, &list)
	if len(list) != 3 || list[2].Label != "Vault 2" {
		t.Fatalf("instances = %+v", list)
	}
	e.call("DELETE", path, nil, 204, nil)
	e.call("GET", path, nil, 404, nil)
	e.call("PATCH", path, map[string]any{"name": "X"}, 404, nil)
}

func TestInstanceCheck(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	create := func(plugin, name string, config map[string]any) int64 {
		id, err := e.s.Registry.CreateInstance(ctx, plugins.NewInstance{Plugin: plugin, Name: new(name), Config: config})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	check := func(id int64) (out checkResult) {
		t.Helper()
		e.call("POST", fmt.Sprintf("/instances/%d/check", id), nil, 200, &out)
		return out
	}
	broken := func(id int64) *string {
		t.Helper()
		i, err := e.s.Registry.GetInstance(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return i.BrokenError
	}

	good := create("target", "Good", map[string]any{"dir": t.TempDir()})
	if c := check(good); !c.OK || c.Error != "" {
		t.Errorf("good: %+v", c)
	}
	e.markBroken(good, "passing trouble")
	if c := check(good); !c.OK || broken(good) != nil {
		t.Errorf("a good start leaves %+v, broken %v", c, broken(good))
	}

	// Without a dir, the fake target's app refuses target.list.
	noApp := create("target", "No app", nil)
	e.markBroken(noApp, "passing trouble")
	if c := check(noApp); c.OK || !strings.Contains(c.Error, "config.dir is not set") || broken(noApp) != nil {
		t.Errorf("app error: %+v, broken %v", c, broken(noApp))
	}

	// The vault manifest names no binary, so its plugin can't start.
	vault := create("vault", "Vault", nil)
	if c := check(vault); c.OK || broken(vault) == nil || c.Error != *broken(vault) {
		t.Errorf("failed start: %+v, broken %v", c, broken(vault))
	}

	e.call("POST", "/instances/9999/check", nil, 404, nil)
}

// An OAuth client could make itself the owner through a login provider, so
// even a destructive one for the API can't touch them; an API token can.
func TestOAuthClientsCantChangeLogins(t *testing.T) {
	e := newEnv(t)
	oauth, token := "Bearer "+e.oauthToken(tier.Destructive), "Bearer "+e.tokenWith(tier.Destructive)
	login := map[string]any{"plugin": "fake", "name": "Login 2", "config": map[string]any{}}
	owned := map[string]any{"plugin": "vault", "name": "Owned", "owner_subject": "me"}
	for _, body := range []map[string]any{login, owned} {
		if r := e.send("POST", "/instances", body, "Authorization", oauth); r.Status != http.StatusForbidden {
			t.Fatalf("OAuth client creating %v: %s", body["name"], r)
		}
	}
	var inst plugins.InstanceInfo
	if r := e.send("POST", "/instances", login, "Authorization", token); r.Status != http.StatusCreated {
		t.Fatalf("API token creating a login provider: %s", r)
	} else if err := json.Unmarshal(r.Body, &inst); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/instances/%d", inst.ID)
	if r := e.send("PATCH", path, map[string]any{"name": "Renamed"}, "Authorization", oauth); r.Status != http.StatusForbidden {
		t.Fatalf("OAuth client changing a login provider: %s", r)
	}
	if r := e.send("PATCH", path, map[string]any{"owner_subject": "me"}, "Authorization", token); r.Status != http.StatusOK {
		t.Fatalf("API token setting an owner: %s", r)
	}
}

func TestInstanceLibraryAccess(t *testing.T) {
	e := newEnv(t)
	var inst plugins.InstanceInfo
	e.call("POST", "/instances", map[string]any{"plugin": "vault", "name": "Vault", "config": map[string]any{"url": "http://v"}}, 201, &inst)
	if inst.LibraryAccess {
		t.Fatal("a new instance has library access")
	}
	path := fmt.Sprintf("/instances/%d", inst.ID)
	e.call("PATCH", path, map[string]any{"library_access": true}, 200, &inst)
	if !inst.LibraryAccess || inst.Config["url"] != "http://v" {
		t.Fatalf("after turning it on: %+v", inst)
	}
	e.call("PATCH", path, map[string]any{"name": "Vault 2"}, 200, &inst)
	if !inst.LibraryAccess {
		t.Fatal("renaming turned library access off")
	}
	e.call("POST", "/instances", map[string]any{"plugin": "vault", "name": "Open", "library_access": true}, 201, &inst)
	if !inst.LibraryAccess {
		t.Fatal("created with library_access true, got false")
	}
}

func TestDisableRefusesLastLogin(t *testing.T) {
	e := newEnv(t)
	path := fmt.Sprintf("/instances/%d", e.loginID)
	e.call("PATCH", path, map[string]any{"disabled": true}, 409, nil)

	owner := "second"
	if _, err := e.s.Registry.CreateInstance(context.Background(), plugins.NewInstance{Plugin: "fake", Name: new("Login 2"), OwnerSubject: &owner}); err != nil {
		t.Fatal(err)
	}
	var inst plugins.InstanceInfo
	e.call("PATCH", path, map[string]any{"disabled": true}, 200, &inst)
	if !inst.Disabled || inst.Status != "disabled" {
		t.Fatalf("disabled: %+v", inst)
	}
	e.call("PATCH", path, map[string]any{"disabled": false}, 200, &inst)
	if inst.Disabled || inst.Status != "ready" {
		t.Fatalf("enabled: %+v", inst)
	}
}

func TestDisableAllowsABrokenLogin(t *testing.T) {
	e := newEnv(t)
	e.markBroken(e.loginID, "bad key")
	e.call("PATCH", fmt.Sprintf("/instances/%d", e.loginID), map[string]any{"disabled": true}, 200, nil)
}

func TestInstanceSyncLibrary(t *testing.T) {
	e := newEnv(t)
	app, _ := e.target()
	archive := e.createLibrary("Archive")
	path := fmt.Sprintf("/instances/%d", app)

	var got plugins.InstanceInfo
	e.call("GET", path, nil, 200, &got)
	if got.SyncLibrary != library.DefaultLibrary {
		t.Fatalf("default sync library = %d", got.SyncLibrary)
	}
	e.call("PATCH", path, map[string]any{"sync_library": archive.ID}, 200, nil)
	e.call("GET", path, nil, 200, &got)
	if got.SyncLibrary != archive.ID {
		t.Fatalf("sync library = %d, want %d", got.SyncLibrary, archive.ID)
	}
	e.call("PATCH", path, map[string]any{"sync_library": 999999}, 400, nil)

	// A plugin that doesn't sync has no sync library.
	scorer := &protocol.Manifest{Name: "scorer", Version: "1.0.0", Protocol: protocol.Version, Command: []string{"./scorer"},
		Capabilities: []string{"enrich"}, Enrichments: map[string]protocol.Enrichment{"quality": {Type: "score", Range: []float64{0, 100}, Label: "Quality"}}}
	e.manifests["scorer"] = scorer
	id, err := e.s.Registry.CreateInstance(context.Background(), plugins.NewInstance{Plugin: "scorer"})
	if err != nil {
		t.Fatal(err)
	}
	e.call("PATCH", fmt.Sprintf("/instances/%d", id), map[string]any{"sync_library": archive.ID}, 400, nil)
}
