package auth

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/davidgibbons/innkeeper/internal/testdb"
)

func TestTokens(t *testing.T) {
	pool := testdb.New(t)
	a := New(pool)
	ctx := context.Background()
	tok, secret, err := a.CreateToken(ctx, "cli")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(secret, "ink_") || tok.Name != "cli" {
		t.Fatalf("token = %+v, %q", tok, secret)
	}
	var stored string
	if err := pool.QueryRow(ctx, `SELECT token_hash FROM api_token`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored, secret) {
		t.Fatal("database holds the token itself")
	}

	for _, s := range []string{secret, "ink_wrong", ""} {
		ok, err := a.CheckToken(ctx, s)
		if err != nil || ok != (s == secret) {
			t.Fatalf("CheckToken(%q) = %v, %v", s, ok, err)
		}
	}
	list, err := a.Tokens(ctx)
	if err != nil || len(list) != 1 || list[0].LastUsedAt == nil {
		t.Fatalf("tokens = %+v, %v", list, err)
	}

	if err := a.RevokeToken(ctx, tok.ID); err != nil {
		t.Fatal(err)
	}
	if ok, _ := a.CheckToken(ctx, secret); ok {
		t.Fatal("revoked token still works")
	}
	if err := a.RevokeToken(ctx, tok.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second revoke: %v", err)
	}
}

func loginInstance(t *testing.T, pool *pgxpool.Pool) int64 {
	t.Helper()
	var id int64
	err := pool.QueryRow(context.Background(), `INSERT INTO plugin_instance (plugin, name, owner_subject)
		VALUES ('fake', 'Login', 'owner') RETURNING id`).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestSessions(t *testing.T) {
	pool := testdb.New(t)
	a := New(pool)
	ctx := context.Background()
	inst := loginInstance(t, pool)

	if _, err := a.CreateSession(ctx, inst, "someone-else"); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("non-owner login: %v", err)
	}
	if _, err := a.CreateSession(ctx, inst+1, "owner"); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("login through missing instance: %v", err)
	}
	secret, err := a.CreateSession(ctx, inst, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := a.CheckSession(ctx, secret); !ok || err != nil {
		t.Fatalf("CheckSession = %v, %v", ok, err)
	}

	// Changing the owner ends the old owner's sessions.
	if _, err := pool.Exec(ctx, `UPDATE plugin_instance SET owner_subject = 'new-owner'`); err != nil {
		t.Fatal(err)
	}
	if ok, _ := a.CheckSession(ctx, secret); ok {
		t.Fatal("session survived an owner change")
	}
	if _, err := pool.Exec(ctx, `UPDATE plugin_instance SET owner_subject = 'owner'`); err != nil {
		t.Fatal(err)
	}

	if _, err := pool.Exec(ctx, `UPDATE session SET expires_at = now() - interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	if ok, _ := a.CheckSession(ctx, secret); ok {
		t.Fatal("expired session still works")
	}

	secret, _ = a.CreateSession(ctx, inst, "owner")
	if err := a.DeleteSession(ctx, secret); err != nil {
		t.Fatal(err)
	}
	if ok, _ := a.CheckSession(ctx, secret); ok {
		t.Fatal("deleted session still works")
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM session`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("%d sessions left; the expired one should be cleaned up at login", n)
	}
}
