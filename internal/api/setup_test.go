package api

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/inkandale/innkeeper/internal/plugins"
)

// validHash is a well-formed bcrypt hash; Bootstrap only checks its shape.
const validHash = "$2a$12$R9h/cIPz0gi.URNNX3kh2OPST9/PgBkqquzi.Ss7KIUgO2t0jWMUW"

// unclaimedEnv is newEnv without a login provider, plus "password-login":
// the fake plugin with a password_hash secret.
func unclaimedEnv(t *testing.T) *testEnv {
	t.Helper()
	e := newEnv(t)
	pw := *e.manifests["fake"]
	pw.Name, pw.Capabilities, pw.Secrets = "password-login", []string{"auth"}, []string{"password_hash"}
	pw.Command = []string{e.manifests["fake"].Command[0], "-caps=auth", "-name=password-login"}
	e.manifests["password-login"] = &pw
	if err := e.s.Registry.DeleteInstance(context.Background(), e.loginID); err != nil {
		t.Fatal(err)
	}
	return e
}

// loginInstance returns the password-login instance, or fails.
func loginInstance(t *testing.T, e *testEnv) plugins.InstanceInfo {
	t.Helper()
	insts, err := e.s.Registry.ListInstances(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range insts {
		if i.Plugin == "password-login" && i.OwnerSubject != nil {
			return i
		}
	}
	t.Fatal("no owner password-login instance")
	return plugins.InstanceInfo{}
}

func TestBootstrapLogsASetupCode(t *testing.T) {
	e := unclaimedEnv(t)
	var buf bytes.Buffer
	e.s.Log = slog.New(slog.NewTextHandler(&buf, nil))
	if err := e.s.Bootstrap(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	code := e.s.currentSetupCode()
	if len(normalizeCode(code)) != 26 || !strings.Contains(buf.String(), code) {
		t.Fatalf("code %q, log %q", code, buf.String())
	}
}

func TestBootstrapWithHashCreatesTheLogin(t *testing.T) {
	e := unclaimedEnv(t)
	if err := e.s.Bootstrap(context.Background(), validHash); err != nil {
		t.Fatal(err)
	}
	inst := loginInstance(t, e)
	if inst.OwnerSubject == nil || *inst.OwnerSubject != "owner" || len(inst.Secrets) != 1 || inst.Secrets[0] != "password_hash" {
		t.Fatalf("instance %+v", inst)
	}
	if code := e.s.currentSetupCode(); code != "" {
		t.Fatalf("setup code %q after a hash claimed the server", code)
	}
}

func TestBootstrapReplacesBrokenSingletonLogin(t *testing.T) {
	e := unclaimedEnv(t)
	e.manifests["password-login"].MultipleInstances = false
	ctx := context.Background()
	if _, err := e.s.Registry.CreateInstance(ctx, plugins.NewInstance{Plugin: "password-login", Name: new("Old login")}); err != nil {
		t.Fatal(err)
	}
	if err := e.s.Bootstrap(ctx, validHash); err != nil {
		t.Fatal(err)
	}
	insts, err := e.s.Registry.ListInstances(ctx)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, i := range insts {
		if i.Plugin == "password-login" {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("%d login instances, want 2", n)
	}
}

func TestBootstrapRejectsABadHash(t *testing.T) {
	e := unclaimedEnv(t)
	err := e.s.Bootstrap(context.Background(), "hunter2")
	if err == nil || !strings.Contains(err.Error(), "password-login hash") {
		t.Fatalf("got %v", err)
	}
}

func TestBootstrapIgnoresAHashOnceClaimed(t *testing.T) {
	e := newEnv(t)
	e.manifests["password-login"] = e.manifests["fake"]
	var buf bytes.Buffer
	e.s.Log = slog.New(slog.NewTextHandler(&buf, nil))
	if err := e.s.Bootstrap(context.Background(), validHash); err != nil {
		t.Fatal(err)
	}
	insts, err := e.s.Registry.ListInstances(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(insts) != 2 || !strings.Contains(buf.String(), "ignored") || e.s.currentSetupCode() != "" {
		t.Fatalf("instances %d, code %q, log %q", len(insts), e.s.currentSetupCode(), buf.String())
	}
}

func claimBody(code, password, confirm string) map[string]any {
	return map[string]any{"code": code, "plugin": "password-login",
		"params": map[string]string{"password": password, "confirm": confirm}}
}

func TestSetupClaimsTheServer(t *testing.T) {
	e := unclaimedEnv(t)
	if err := e.s.Bootstrap(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	var list []setupPlugin
	e.call("GET", "/setup", nil, http.StatusOK, &list)
	if !slices.ContainsFunc(list, func(p setupPlugin) bool { return p.Plugin == "password-login" && len(p.Form) > 0 }) {
		t.Fatalf("setup plugins %+v", list)
	}

	// Lower case and no dashes still match.
	code := strings.ToLower(strings.ReplaceAll(e.s.currentSetupCode(), "-", ""))
	r := e.send("POST", "/setup", claimBody(code, "correct horse", "correct horse"))
	if r.Status != http.StatusNoContent {
		t.Fatalf("claim: %s", r)
	}
	session := r.cookie(t, sessionCookie)
	if me := e.send("GET", "/auth/me", nil, "Cookie", session.Name+"="+session.Value); me.Status != http.StatusOK {
		t.Fatalf("me with the claim's session: %s", me)
	}
	inst := loginInstance(t, e)
	if inst.Label != "Login" || inst.OwnerSubject == nil || *inst.OwnerSubject != "owner" {
		t.Fatalf("instance %+v", inst)
	}

	if r := e.send("GET", "/setup", nil); r.Status != http.StatusNotFound {
		t.Fatalf("setup after claim: %s", r)
	}
	if r := e.send("POST", "/setup", claimBody(code, "x", "x")); r.Status != http.StatusForbidden {
		t.Fatalf("second claim: %s", r)
	}
}

func TestSetupRejectsAWrongCode(t *testing.T) {
	e := unclaimedEnv(t)
	if err := e.s.Bootstrap(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if r := e.send("POST", "/setup", claimBody("AAAA-BBBB", "pw", "pw")); r.Status != http.StatusForbidden {
		t.Fatalf("wrong code: %s", r)
	}
	if claimed, _ := e.s.claimed(context.Background()); claimed {
		t.Fatal("a wrong code claimed the server")
	}
}

func TestSetupShowsThePluginsRejection(t *testing.T) {
	e := unclaimedEnv(t)
	if err := e.s.Bootstrap(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	r := e.send("POST", "/setup", claimBody(e.s.currentSetupCode(), "one", "two"))
	if r.Status != http.StatusBadRequest || !strings.Contains(string(r.Body), "don't match") {
		t.Fatalf("mismatch: %s", r)
	}
}

func TestSetupIsGoneOnAClaimedServer(t *testing.T) {
	e := newEnv(t)
	if r := e.send("GET", "/setup", nil); r.Status != http.StatusNotFound {
		t.Fatalf("got %s", r)
	}
}

// A server that loses its last login provider makes a code on demand.
func TestSetupMakesACodeWhenNeeded(t *testing.T) {
	e := unclaimedEnv(t)
	e.call("GET", "/setup", nil, http.StatusOK, nil)
	if e.s.currentSetupCode() == "" {
		t.Fatal("no setup code")
	}
}

// Opening /setup logs the code again, so it is near the end of the log when
// the owner goes looking, but not on every reload.
func TestSetupLogsTheCodeAgainWhenOpened(t *testing.T) {
	e := unclaimedEnv(t)
	if err := e.s.Bootstrap(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	e.s.Log = slog.New(slog.NewTextHandler(&buf, nil))
	e.s.setupLogged = time.Now().Add(-setupLogEvery)
	e.call("GET", "/setup", nil, http.StatusOK, nil)
	e.call("GET", "/setup", nil, http.StatusOK, nil)
	if n := strings.Count(buf.String(), e.s.currentSetupCode()); n != 1 {
		t.Fatalf("logged the code %d times: %q", n, buf.String())
	}
}

// takeLoginName leaves an ownerless login instance holding the name "Login",
// as an old or broken one would.
func takeLoginName(t *testing.T, e *testEnv) {
	t.Helper()
	if _, err := e.s.Registry.CreateInstance(context.Background(), plugins.NewInstance{Plugin: "password-login", Name: new("Login")}); err != nil {
		t.Fatal(err)
	}
}

func TestBootstrapWhenLoginNameIsTaken(t *testing.T) {
	e := unclaimedEnv(t)
	takeLoginName(t, e)
	if err := e.s.Bootstrap(context.Background(), validHash); err != nil {
		t.Fatal(err)
	}
	if inst := loginInstance(t, e); inst.Label != "Login 2" {
		t.Fatalf("instance %+v", inst)
	}
}

func TestSetupWhenLoginNameIsTaken(t *testing.T) {
	e := unclaimedEnv(t)
	takeLoginName(t, e)
	if err := e.s.Bootstrap(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if r := e.send("POST", "/setup", claimBody(e.s.currentSetupCode(), "pw", "pw")); r.Status != http.StatusNoContent {
		t.Fatalf("claim: %s", r)
	}
	if inst := loginInstance(t, e); inst.Label != "Login 2" {
		t.Fatalf("instance %+v", inst)
	}
}

// A code left over from before the server was claimed claims nothing.
func TestSetupRefusesAClaimedServerWithAValidCode(t *testing.T) {
	e := newEnv(t)
	pw := *e.manifests["fake"]
	pw.Name, pw.Capabilities, pw.Secrets = "password-login", []string{"auth"}, []string{"password_hash"}
	pw.Command = []string{e.manifests["fake"].Command[0], "-caps=auth", "-name=password-login"}
	e.manifests["password-login"] = &pw
	code := e.s.ensureSetupCode()
	if r := e.send("POST", "/setup", claimBody(code, "pw", "pw")); r.Status != http.StatusForbidden {
		t.Fatalf("claim: %s", r)
	}
	insts, err := e.s.Registry.ListInstances(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(insts) != 2 {
		t.Fatalf("%d instances, want 2", len(insts))
	}
}
