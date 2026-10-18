package api

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/inkandale/innkeeper/internal/auth"
	"github.com/inkandale/innkeeper/internal/tier"
)

// Operations whose tier isn't what their method suggests.
var tierExceptions = map[string]tier.Tier{
	"search-cards":        tier.Read,
	"search-lorebooks":    tier.Read,
	"search-cards-legacy": tier.Read,
	"check-binding":       tier.Read,
	"start-scan":          tier.Read, // Reads the apps; changes nothing.
	"check-instance":      tier.Read,
	"push-card":           tier.Destructive,
	"push-lorebook":       tier.Destructive,
	"push-cards":          tier.Destructive,
	"unbind":              tier.Destructive,
	// Autosync pushes unattended, replacing the app's copy.
	"update-binding":    tier.Destructive,
	"delete-instance":   tier.Destructive,
	"create-token":      tier.Destructive,
	"revoke-token":      tier.Destructive,
	"delete-collection": tier.Destructive,
	"delete-library":    tier.Destructive,
	"purge-cards":       tier.Destructive,
	// Plugins run as code inside Innkeeper.
	"add-plugin-source":    tier.Destructive,
	"delete-plugin-source": tier.Destructive,
	"install-plugin":       tier.Destructive,
	"uninstall-plugin":     tier.Destructive,
	"remove-plugin-data":   tier.Destructive,
	// Instance settings decide what a plugin reaches and who can log in.
	"create-instance": tier.Destructive,
	"update-instance": tier.Destructive,
	// A draft can send an instance's saved secrets to any address.
	"get-plugin-options": tier.Destructive,
	"run-plugin-setting": tier.Destructive,
	// The old record is gone; an import may replace records.
	"replace-record":     tier.Destructive,
	"delete-record":      tier.Destructive,
	"import-record-file": tier.Destructive,
	"import-record-url":  tier.Destructive,
	// Checks the tool's own tier itself.
	"call-plugin-tool": tier.Read,
	"revoke-grant":     tier.Destructive,
	// Backups hold every card and the encrypted secrets.
	"list-backups":    tier.Destructive,
	"start-backup":    tier.Destructive,
	"download-backup": tier.Destructive,
	"start-restore":   tier.Destructive,
}

func (e *testEnv) tokenWith(t tier.Tier) string {
	e.t.Helper()
	_, secret, err := e.s.Auth.CreateToken(context.Background(), t.String()+" token", t)
	if err != nil {
		e.t.Fatal(err)
	}
	return secret
}

// oauthToken is an OAuth client's access token for the API with tier t.
func (e *testEnv) oauthToken(t tier.Tier) string {
	e.t.Helper()
	ctx := context.Background()
	c, err := e.s.Auth.RegisterClient(ctx, "ink", []string{redirect})
	if err != nil {
		e.t.Fatal(err)
	}
	verifier := strings.Repeat("x", 50)
	sum := sha256.Sum256([]byte(verifier))
	code, err := e.s.Auth.Approve(ctx, c.ID, t, auth.ResourceAPI, redirect, base64.RawURLEncoding.EncodeToString(sum[:]))
	if err != nil {
		e.t.Fatal(err)
	}
	toks, err := e.s.Auth.RedeemCode(ctx, c.ID, code, redirect, verifier)
	if err != nil {
		e.t.Fatal(err)
	}
	return toks.Access
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
				// An ID nothing has: a job another call queued would keep its event stream open.
				r := e.send(method, param.ReplaceAllString(path, "999999"), nil, "Authorization", "Bearer "+token)
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
