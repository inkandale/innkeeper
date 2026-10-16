-- OAuth for MCP clients. Secrets are stored as their hex SHA-256.
CREATE TABLE oauth_client (
    id text PRIMARY KEY,
    name text NOT NULL,
    redirect_uris text[] NOT NULL CHECK (cardinality(redirect_uris) > 0),
    created_at timestamptz NOT NULL DEFAULT now()
);

-- A grant is the owner's approval of one client at one tier. Deleting it
-- ends its codes and tokens.
CREATE TABLE oauth_grant (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    client_id text NOT NULL REFERENCES oauth_client ON DELETE CASCADE,
    tier text NOT NULL CHECK (tier IN ('read', 'write', 'destructive')),
    created_at timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz
);

CREATE TABLE oauth_code (
    hash text PRIMARY KEY,
    grant_id bigint NOT NULL REFERENCES oauth_grant ON DELETE CASCADE,
    redirect_uri text NOT NULL,
    challenge text NOT NULL,
    expires_at timestamptz NOT NULL,
    used_at timestamptz
);

CREATE TABLE oauth_token (
    hash text PRIMARY KEY,
    grant_id bigint NOT NULL REFERENCES oauth_grant ON DELETE CASCADE,
    kind text NOT NULL CHECK (kind IN ('access', 'refresh')),
    expires_at timestamptz NOT NULL,
    -- A refresh token is used once; a second use revokes the grant.
    used_at timestamptz
);
CREATE INDEX ON oauth_token (grant_id);
