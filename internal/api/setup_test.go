package api

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/davidgibbons/innkeeper/internal/plugins"
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
		if i.Plugin == "password-login" {
			return i
		}
	}
	t.Fatal("no password-login instance")
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
