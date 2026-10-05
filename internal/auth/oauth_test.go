package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/davidgibbons/innkeeper/internal/testdb"
	"github.com/davidgibbons/innkeeper/internal/tier"
)

var verifier = strings.Repeat("v", 43)

func challenge(v string) string {
	sum := sha256.Sum256([]byte(v))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func TestOAuthGrant(t *testing.T) {
	pool := testdb.New(t)
	a := New(pool)
	ctx := context.Background()
	c, err := a.RegisterClient(ctx, "Claude", []string{"https://claude.example/cb"})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := a.Client(ctx, c.ID); err != nil || got.Name != "Claude" {
		t.Fatalf("Client = %+v, %v", got, err)
	}
	if _, err := a.Client(ctx, "nope"); !errors.Is(err, ErrNoClient) {
		t.Fatalf("unknown client: %v", err)
	}
	approve := func() string {
		code, err := a.Approve(ctx, c.ID, tier.Write, "https://claude.example/cb", challenge(verifier))
		if err != nil {
			t.Fatal(err)
		}
		return code
	}

	// Refused: wrong verifier, wrong redirect, wrong client.
	code := approve()
	for _, bad := range [][3]string{{c.ID, "https://claude.example/cb", strings.Repeat("w", 43)},
		{c.ID, "https://evil.example/cb", verifier}, {"other", "https://claude.example/cb", verifier}} {
		if _, err := a.RedeemCode(ctx, bad[0], code, bad[1], bad[2]); !errors.Is(err, ErrBadGrant) {
			t.Fatalf("redeem %v: %v", bad, err)
		}
	}
	toks, err := a.RedeemCode(ctx, c.ID, code, "https://claude.example/cb", verifier)
	if err != nil || toks.Tier != tier.Write {
		t.Fatalf("redeem = %+v, %v", toks, err)
	}
	if got, _, _ := a.CheckAccessToken(ctx, toks.Access); got != tier.Write {
		t.Fatalf("access token tier = %v", got)
	}
	if got, _, _ := a.CheckAccessToken(ctx, toks.Refresh); got != 0 {
		t.Fatal("a refresh token works as an access token")
	}
	if got, _ := a.CheckToken(ctx, toks.Access); got != 0 {
		t.Fatal("an access token works as an API token")
	}

	// Refresh rotates. A quick reuse, like a client's retry, is refused but
	// keeps the grant; a later one revokes it.
	next, err := a.Refresh(ctx, c.ID, toks.Refresh)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Refresh(ctx, c.ID, toks.Refresh); !errors.Is(err, ErrBadGrant) {
		t.Fatalf("quick refresh reuse: %v", err)
	}
	if got, _, _ := a.CheckAccessToken(ctx, next.Access); got == 0 {
		t.Fatal("a retried refresh revoked the grant")
	}
	if _, err := pool.Exec(ctx, `UPDATE oauth_token SET used_at = now() - interval '1 minute' WHERE used_at IS NOT NULL`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Refresh(ctx, c.ID, toks.Refresh); !errors.Is(err, ErrBadGrant) {
		t.Fatalf("refresh reuse: %v", err)
	}
	if got, _, _ := a.CheckAccessToken(ctx, next.Access); got != 0 {
		t.Fatal("tokens survived a refresh token's reuse")
	}

	// A code used twice revokes its grant.
	code = approve()
	toks, err = a.RedeemCode(ctx, c.ID, code, "https://claude.example/cb", verifier)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.RedeemCode(ctx, c.ID, code, "https://claude.example/cb", verifier); !errors.Is(err, ErrBadGrant) {
		t.Fatalf("code reuse: %v", err)
	}
	if got, _, _ := a.CheckAccessToken(ctx, toks.Access); got != 0 {
		t.Fatal("tokens survived a code's reuse")
	}

	// An expired code is refused; a revoked grant's tokens stop working.
	code = approve()
	if _, err := pool.Exec(ctx, `UPDATE oauth_code SET expires_at = now() - interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.RedeemCode(ctx, c.ID, code, "https://claude.example/cb", verifier); !errors.Is(err, ErrBadGrant) {
		t.Fatalf("expired code: %v", err)
	}
	toks, _ = a.RedeemCode(ctx, c.ID, approve(), "https://claude.example/cb", verifier)
	grants, err := a.Grants(ctx)
	if err != nil || len(grants) != 1 || grants[0].ClientName != "Claude" {
		t.Fatalf("grants = %+v, %v", grants, err)
	}
	if err := a.RevokeGrant(ctx, grants[0].ID); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := a.CheckAccessToken(ctx, toks.Access); got != 0 {
		t.Fatal("a revoked grant's token works")
	}
	if err := a.PruneOAuth(ctx); err != nil {
		t.Fatal(err)
	}
}
