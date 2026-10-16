-- Tokens made before tiers keep full access, so existing clients work.
-- New tokens are read-only unless they ask for more.
ALTER TABLE api_token ADD COLUMN tier text NOT NULL DEFAULT 'destructive'
    CHECK (tier IN ('read', 'write', 'destructive'));
ALTER TABLE api_token ALTER COLUMN tier SET DEFAULT 'read';
