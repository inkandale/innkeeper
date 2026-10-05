package auth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/davidgibbons/innkeeper/internal/tier"
)

// OAuth token lifetimes.
const (
	CodeTTL    = time.Minute
	AccessTTL  = time.Hour
	RefreshTTL = 30 * 24 * time.Hour
)

var (
	// ErrBadGrant means a code or refresh token is unknown, expired, used, or
	// doesn't match the request. OAuth calls this invalid_grant.
	ErrBadGrant = errors.New("invalid grant")
	// ErrNoClient means the client ID isn't registered.
	ErrNoClient = errors.New("unknown client")
)

// Client is a registered OAuth client.
type Client struct {
	ID           string
	Name         string
	RedirectURIs []string
}

// RegisterClient records a client and returns it with its new ID.
func (a *Auth) RegisterClient(ctx context.Context, name string, redirectURIs []string) (Client, error) {
	id, _ := newSecret("inkc_")
	c := Client{ID: id, Name: name, RedirectURIs: redirectURIs}
	_, err := a.pool.Exec(ctx, `INSERT INTO oauth_client (id, name, redirect_uris) VALUES ($1, $2, $3)`, id, name, redirectURIs)
	return c, err
}

// Client returns a registered client.
func (a *Auth) Client(ctx context.Context, id string) (Client, error) {
	c := Client{ID: id}
	err := a.pool.QueryRow(ctx, `SELECT name, redirect_uris FROM oauth_client WHERE id = $1`, id).Scan(&c.Name, &c.RedirectURIs)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, ErrNoClient
	}
	return c, err
}

// Approve records the owner's approval of client at tier t, and returns an
// authorization code for redirectURI, bound to the PKCE S256 challenge.
func (a *Auth) Approve(ctx context.Context, clientID string, t tier.Tier, redirectURI, challenge string) (string, error) {
	code, h := newSecret("inkx_")
	_, err := a.pool.Exec(ctx, `WITH g AS (INSERT INTO oauth_grant (client_id, tier) VALUES ($1, $2) RETURNING id)
		INSERT INTO oauth_code (hash, grant_id, redirect_uri, challenge, expires_at)
		SELECT $3, id, $4, $5, now() + make_interval(secs => $6) FROM g`,
		clientID, t.String(), h, redirectURI, challenge, CodeTTL.Seconds())
	return code, err
}

// Tokens is what a token request returns.
type Tokens struct {
	Access, Refresh string
	Tier            tier.Tier
}

// RedeemCode exchanges a code for tokens. The code must be unused,
// unexpired, and for this client and redirect URI, and verifier must match
// its challenge. A code used twice revokes the grant it made.
func (a *Auth) RedeemCode(ctx context.Context, clientID, code, redirectURI, verifier string) (Tokens, error) {
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		return Tokens{}, err
	}
	defer tx.Rollback(ctx)
	var grant int64
	var client, uri, challenge, tierName string
	var expired, used bool
	err = tx.QueryRow(ctx, `SELECT c.grant_id, g.client_id, c.redirect_uri, c.challenge, g.tier,
			c.expires_at < now(), c.used_at IS NOT NULL
		FROM oauth_code c JOIN oauth_grant g ON g.id = c.grant_id WHERE c.hash = $1 FOR UPDATE OF c`, hash(code)).
		Scan(&grant, &client, &uri, &challenge, &tierName, &expired, &used)
	if errors.Is(err, pgx.ErrNoRows) {
		return Tokens{}, ErrBadGrant
	}
	if err != nil {
		return Tokens{}, err
	}
	if used {
		// Someone else may hold the code; end everything it gave.
		if _, err := tx.Exec(ctx, `DELETE FROM oauth_grant WHERE id = $1`, grant); err != nil {
			return Tokens{}, err
		}
		return Tokens{}, errors.Join(ErrBadGrant, tx.Commit(ctx))
	}
	if expired || client != clientID || uri != redirectURI || !pkceMatches(challenge, verifier) {
		return Tokens{}, ErrBadGrant
	}
	if _, err := tx.Exec(ctx, `UPDATE oauth_code SET used_at = now() WHERE hash = $1`, hash(code)); err != nil {
		return Tokens{}, err
	}
	t, err := tier.Parse(tierName)
	if err != nil {
		return Tokens{}, err
	}
	toks, err := issue(ctx, tx, grant, t)
	if err != nil {
		return Tokens{}, err
	}
	return toks, tx.Commit(ctx)
}

// pkceMatches checks an S256 PKCE verifier against its challenge.
func pkceMatches(challenge, verifier string) bool {
	if len(verifier) < 43 || len(verifier) > 128 {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	want := base64.RawURLEncoding.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(want), []byte(challenge)) == 1
}

func issue(ctx context.Context, tx pgx.Tx, grant int64, t tier.Tier) (Tokens, error) {
	access, ah := newSecret("inka_")
	refresh, rh := newSecret("inkr_")
	_, err := tx.Exec(ctx, `INSERT INTO oauth_token (hash, grant_id, kind, expires_at) VALUES
		($1, $3, 'access', now() + make_interval(secs => $4)), ($2, $3, 'refresh', now() + make_interval(secs => $5))`,
		ah, rh, grant, AccessTTL.Seconds(), RefreshTTL.Seconds())
	return Tokens{Access: access, Refresh: refresh, Tier: t}, err
}

// Refresh exchanges a refresh token for new tokens. A refresh token used
// twice revokes the grant, since one copy is in the wrong hands.
func (a *Auth) Refresh(ctx context.Context, clientID, refresh string) (Tokens, error) {
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		return Tokens{}, err
	}
	defer tx.Rollback(ctx)
	var grant int64
	var client, tierName string
	var expired, used bool
	err = tx.QueryRow(ctx, `SELECT t.grant_id, g.client_id, g.tier, t.expires_at < now(), t.used_at IS NOT NULL
		FROM oauth_token t JOIN oauth_grant g ON g.id = t.grant_id
		WHERE t.hash = $1 AND t.kind = 'refresh' FOR UPDATE OF t`, hash(refresh)).
		Scan(&grant, &client, &tierName, &expired, &used)
	if errors.Is(err, pgx.ErrNoRows) {
		return Tokens{}, ErrBadGrant
	}
	if err != nil {
		return Tokens{}, err
	}
	if used {
		if _, err := tx.Exec(ctx, `DELETE FROM oauth_grant WHERE id = $1`, grant); err != nil {
			return Tokens{}, err
		}
		return Tokens{}, errors.Join(ErrBadGrant, tx.Commit(ctx))
	}
	if expired || client != clientID {
		return Tokens{}, ErrBadGrant
	}
	if _, err := tx.Exec(ctx, `UPDATE oauth_token SET used_at = now() WHERE hash = $1`, hash(refresh)); err != nil {
		return Tokens{}, err
	}
	t, err := tier.Parse(tierName)
	if err != nil {
		return Tokens{}, err
	}
	toks, err := issue(ctx, tx, grant, t)
	if err != nil {
		return Tokens{}, err
	}
	return toks, tx.Commit(ctx)
}

// CheckAccessToken returns the tier and expiry of an OAuth access token, or
// a zero tier if it isn't a live one. It records the grant's use at most
// once a minute.
func (a *Auth) CheckAccessToken(ctx context.Context, secret string) (tier.Tier, time.Time, error) {
	var name *string
	var expires time.Time
	err := a.pool.QueryRow(ctx, `WITH t AS (SELECT g.id, g.tier, g.last_used_at, t.expires_at
			FROM oauth_token t JOIN oauth_grant g ON g.id = t.grant_id
			WHERE t.hash = $1 AND t.kind = 'access' AND t.expires_at > now()),
		used AS (UPDATE oauth_grant SET last_used_at = now() WHERE id IN (
			SELECT id FROM t WHERE last_used_at IS NULL OR last_used_at < now() - interval '1 minute'))
		SELECT tier, expires_at FROM t`, hash(secret)).Scan(&name, &expires)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && name == nil {
		return 0, time.Time{}, nil
	}
	if err != nil {
		return 0, time.Time{}, err
	}
	t, err := tier.Parse(*name)
	return t, expires, err
}

// Grant is an approved OAuth client.
type Grant struct {
	ID         int64      `json:"id"`
	ClientName string     `json:"client_name"`
	Tier       string     `json:"tier" enum:"read,write,destructive"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

// Grants returns every grant that has tokens, oldest first.
func (a *Auth) Grants(ctx context.Context) ([]Grant, error) {
	rows, err := a.pool.Query(ctx, `SELECT g.id, c.name, g.tier, g.created_at, g.last_used_at
		FROM oauth_grant g JOIN oauth_client c ON c.id = g.client_id
		WHERE EXISTS (SELECT 1 FROM oauth_token t WHERE t.grant_id = g.id) ORDER BY g.id`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Grant])
}

// RevokeGrant ends a grant and its tokens.
func (a *Auth) RevokeGrant(ctx context.Context, id int64) error {
	tag, err := a.pool.Exec(ctx, `DELETE FROM oauth_grant WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// PruneOAuth deletes expired tokens and codes, grants left with no tokens,
// and clients a day old with no grants. Registration is open, so this keeps
// unused clients from piling up.
func (a *Auth) PruneOAuth(ctx context.Context) error {
	_, err := a.pool.Exec(ctx, `
		DELETE FROM oauth_token WHERE expires_at < now() - interval '1 day';
		DELETE FROM oauth_code WHERE expires_at < now() - interval '1 day';
		DELETE FROM oauth_grant g WHERE created_at < now() - interval '1 day'
			AND NOT EXISTS (SELECT 1 FROM oauth_token t WHERE t.grant_id = g.id);
		DELETE FROM oauth_client c WHERE created_at < now() - interval '1 day'
			AND NOT EXISTS (SELECT 1 FROM oauth_grant g WHERE g.client_id = c.id)`)
	return err
}
