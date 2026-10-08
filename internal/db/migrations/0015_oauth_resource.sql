-- Where a grant's access tokens work: /mcp, or the API under /api.
ALTER TABLE oauth_grant ADD COLUMN resource text NOT NULL DEFAULT 'mcp'
    CHECK (resource IN ('mcp', 'api'));
