// Package auth issues and checks API tokens and login sessions. Only the
// SHA-256 of each secret is stored.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/inkandale/innkeeper/internal/tier"
)

var (
	ErrNotFound = errors.New("token not found")
	// ErrNotOwner means a login provider verified someone other than the
	// owner it names.
	ErrNotOwner = errors.New("identity is not the owner")
)

// SessionTTL is how long a login lasts.
const SessionTTL = 30 * 24 * time.Hour

// Auth stores tokens and sessions.
type Auth struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Auth { return &Auth{pool: pool} }

// Token is an API token's record. The secret is shown only when created.
type Token struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	Tier       string     `json:"tier" enum:"read,write,destructive"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

// newSecret returns a random secret starting with prefix, and its hash.
func newSecret(prefix string) (string, string) {
	b := make([]byte, 32)
	rand.Read(b)
	s := prefix + base64.RawURLEncoding.EncodeToString(b)
	return s, hash(s)
}

func hash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// CreateToken makes an API token with tier t and returns its record and
// secret.
func (a *Auth) CreateToken(ctx context.Context, name string, t tier.Tier) (Token, string, error) {
	secret, h := newSecret("ink_")
	var tok Token
	err := a.pool.QueryRow(ctx, `INSERT INTO api_token (name, token_hash, tier) VALUES ($1, $2, $3)
		RETURNING id, name, tier, created_at, last_used_at`, name, h, t.String()).
		Scan(&tok.ID, &tok.Name, &tok.Tier, &tok.CreatedAt, &tok.LastUsedAt)
	return tok, secret, err
}

// Tokens returns every API token, oldest first.
func (a *Auth) Tokens(ctx context.Context) ([]Token, error) {
	rows, err := a.pool.Query(ctx, `SELECT id, name, tier, created_at, last_used_at FROM api_token ORDER BY id`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Token])
}

// RevokeToken deletes an API token.
func (a *Auth) RevokeToken(ctx context.Context, id int64) error {
	tag, err := a.pool.Exec(ctx, `DELETE FROM api_token WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// CheckToken returns the tier of the API token secret, or 0 if it isn't one.
// It records the token's use at most once a minute, so reads don't each
// cost a write.
func (a *Auth) CheckToken(ctx context.Context, secret string) (tier.Tier, error) {
	var name *string
	err := a.pool.QueryRow(ctx, `WITH t AS (SELECT id, tier, last_used_at FROM api_token WHERE token_hash = $1),
		used AS (UPDATE api_token SET last_used_at = now() WHERE id IN (
			SELECT id FROM t WHERE last_used_at IS NULL OR last_used_at < now() - interval '1 minute'))
		SELECT (SELECT tier FROM t)`, hash(secret)).Scan(&name)
	if err != nil || name == nil {
		return 0, err
	}
	return tier.Parse(*name)
}

// CreateSession logs in subject, verified by an auth instance, if that
// instance names subject as the owner. It returns the session's secret.
func (a *Auth) CreateSession(ctx context.Context, instanceID int64, subject string) (string, error) {
	if _, err := a.pool.Exec(ctx, `DELETE FROM session WHERE expires_at < now()`); err != nil {
		return "", err
	}
	secret, h := newSecret("")
	tag, err := a.pool.Exec(ctx, `INSERT INTO session (token_hash, instance_id, subject, expires_at)
		SELECT $1, id, $3, now() + make_interval(secs => $4) FROM plugin_instance
		WHERE id = $2 AND owner_subject = $3 AND NOT disabled`, h, instanceID, subject, SessionTTL.Seconds())
	if err != nil {
		return "", err
	}
	if tag.RowsAffected() == 0 {
		return "", ErrNotOwner
	}
	return secret, nil
}

// CheckSession reports whether secret is a live session whose instance still
// names its subject as the owner.
func (a *Auth) CheckSession(ctx context.Context, secret string) (bool, error) {
	var ok bool
	err := a.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM session s JOIN plugin_instance i ON i.id = s.instance_id
		WHERE s.token_hash = $1 AND s.expires_at > now() AND i.owner_subject = s.subject)`, hash(secret)).Scan(&ok)
	return ok, err
}

// DeleteSession logs a session out.
func (a *Auth) DeleteSession(ctx context.Context, secret string) error {
	_, err := a.pool.Exec(ctx, `DELETE FROM session WHERE token_hash = $1`, hash(secret))
	return err
}
