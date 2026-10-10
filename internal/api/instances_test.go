package api

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/davidgibbons/innkeeper/internal/auth"
	"github.com/davidgibbons/innkeeper/internal/plugins"
	"github.com/davidgibbons/innkeeper/internal/tier"
)

func TestInstanceRoutes(t *testing.T) {
	e := newEnv(t)
	var installed []pluginOut
	e.call("GET", "/plugins", nil, 200, &installed)
	if len(installed) != 3 || installed[0].Name != "fake" || installed[2].Name != "vault" || installed[2].Secrets[0] != "api_key" ||
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

	var raw map[string]any
	e.call("PATCH", path, map[string]any{"name": "Vault 2"}, 200, &raw)
	if _, ok := raw["auto_bind_exact"]; ok {
		t.Fatalf("instance still has auto_bind_exact: %v", raw)
	}
	e.call("PATCH", path, map[string]any{"auto_bind_exact": true}, 422, nil)

	var list []plugins.InstanceInfo
	e.call("GET", "/instances", nil, 200, &list)
	if len(list) != 3 || list[2].Name != "Vault 2" {
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
		id, err := e.s.Registry.CreateInstance(ctx, plugins.NewInstance{Plugin: plugin, Name: name, Config: config})
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
	ctx := context.Background()
	c, err := e.s.Auth.RegisterClient(ctx, "ink", []string{redirect})
	if err != nil {
		t.Fatal(err)
	}
	verifier := strings.Repeat("x", 50)
	sum := sha256.Sum256([]byte(verifier))
	code, err := e.s.Auth.Approve(ctx, c.ID, tier.Destructive, auth.ResourceAPI, redirect, base64.RawURLEncoding.EncodeToString(sum[:]))
	if err != nil {
		t.Fatal(err)
	}
	toks, err := e.s.Auth.RedeemCode(ctx, c.ID, code, redirect, verifier)
	if err != nil {
		t.Fatal(err)
	}
	oauth, token := "Bearer "+toks.Access, "Bearer "+e.tokenWith(tier.Destructive)
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
