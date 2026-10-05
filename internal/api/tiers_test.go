package api

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/davidgibbons/innkeeper/internal/tier"
)

// Operations whose tier isn't what their method suggests.
var tierExceptions = map[string]tier.Tier{
	"search-cards":    tier.Read,
	"check-binding":   tier.Read,
	"push-card":       tier.Destructive,
	"push-lorebook":   tier.Destructive,
	"push-cards":      tier.Destructive,
	"unbind":          tier.Destructive,
	"delete-instance": tier.Destructive,
	"create-token":    tier.Destructive,
	"revoke-token":    tier.Destructive,
	// Instance settings decide what a plugin reaches and who can log in.
	"create-instance":   tier.Destructive,
	"update-instance":   tier.Destructive,
	"delete-collection": tier.Destructive,
}

func (e *testEnv) tokenWith(t tier.Tier) string {
	e.t.Helper()
	_, secret, err := e.s.Auth.CreateToken(context.Background(), t.String()+" token", t)
	if err != nil {
		e.t.Fatal(err)
	}
	return secret
}

// TestOperationTiers reads the OpenAPI spec, so it covers operations added
// later: each is refused exactly to tokens below its tier.
func TestOperationTiers(t *testing.T) {
	e := newEnv(t)
	var spec struct {
		Paths map[string]map[string]struct {
			OperationID string                 `json:"operationId"`
			Security    *[]map[string][]string `json:"security"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(e.send("GET", "/openapi.json", nil).Body, &spec); err != nil {
		t.Fatal(err)
	}
	tokens := map[tier.Tier]string{tier.Read: e.tokenWith(tier.Read), tier.Write: e.tokenWith(tier.Write)}
	param := regexp.MustCompile(`\{[^}]+\}`)
	for path, ops := range spec.Paths {
		for method, op := range ops {
			if op.Security != nil && len(*op.Security) == 0 {
				continue
			}
			method = strings.ToUpper(method)
			need, ok := tierExceptions[op.OperationID]
			if !ok {
				need = tier.Write
				if method == http.MethodGet || method == http.MethodHead {
					need = tier.Read
				}
			}
			for have, token := range tokens {
				r := e.send(method, param.ReplaceAllString(path, "1"), nil, "Authorization", "Bearer "+token)
				if refused := r.Status == http.StatusForbidden; refused != !have.Allows(need) {
					t.Errorf("%s (%s %s) with a %s token: got %d; it needs %s", op.OperationID, method, path, have, r.Status, need)
				}
			}
		}
	}
}

func TestTokenTiers(t *testing.T) {
	e := newEnv(t)
	write := e.tokenWith(tier.Write)
	// A token can't make one stronger than itself.
	r := e.send("POST", "/tokens", map[string]any{"name": "x", "tier": "write"}, "Authorization", "Bearer "+e.tokenWith(tier.Read))
	if r.Status != http.StatusForbidden {
		t.Fatalf("read token creating a token: %s", r)
	}
	var made createdToken
	e.call("POST", "/tokens", map[string]any{"name": "default"}, 201, &made)
	if made.Tier != "read" {
		t.Fatalf("default tier = %q", made.Tier)
	}
	e.call("POST", "/tokens", map[string]any{"name": "full", "tier": "destructive"}, 201, &made)

	// Instance settings decide what a plugin reaches with its secrets, and
	// who can log in, so they need destructive.
	body := map[string]any{"plugin": "fake", "name": "T", "config": map[string]any{}}
	if r := e.send("POST", "/instances", body, "Authorization", "Bearer "+write); r.Status != http.StatusForbidden {
		t.Fatalf("write token creating an instance: %s", r)
	}
}

// A browser session is the owner, with every tier.
func TestSessionIsDestructive(t *testing.T) {
	e := newEnv(t)
	cookie := sessionCookie + "=" + e.login("letmein").cookie(t, sessionCookie).Value
	if r := e.send("POST", "/tokens", map[string]any{"name": "x", "tier": "destructive"}, "Cookie", cookie); r.Status != 201 {
		t.Fatalf("session creating a destructive token: %s", r)
	}
}
