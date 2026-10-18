-- Tokens from INNKEEPER_API_TOKENS. The API replaces them at each start.
ALTER TABLE api_token ADD COLUMN provisioned boolean NOT NULL DEFAULT false;
CREATE UNIQUE INDEX api_token_provisioned_name ON api_token (name) WHERE provisioned;
