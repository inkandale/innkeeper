ALTER TABLE plugin_instance
    -- Sealed with INNKEEPER_SECRETS_KEY; see internal/secrets.
    ADD COLUMN secrets bytea,
    -- On an auth instance, the subject it verifies as the owner. Null lets no one log in.
    ADD COLUMN owner_subject text CHECK (owner_subject <> ''),
    -- Plugin processes started before the last edit are replaced.
    ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now();

ALTER TABLE job
    DROP CONSTRAINT job_instance_id_fkey,
    ADD FOREIGN KEY (instance_id) REFERENCES plugin_instance ON DELETE SET NULL,
    DROP CONSTRAINT job_parent_id_fkey,
    ADD FOREIGN KEY (parent_id) REFERENCES job ON DELETE CASCADE;
-- Workers look for waiting parents whose children have all finished.
CREATE INDEX job_waiting ON job (id) WHERE status = 'waiting';

-- Tokens and sessions are stored as the hex SHA-256 of the secret.
CREATE TABLE api_token (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name text NOT NULL CHECK (name <> ''),
    token_hash text NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz
);

CREATE TABLE session (
    token_hash text PRIMARY KEY,
    instance_id bigint NOT NULL REFERENCES plugin_instance ON DELETE CASCADE,
    subject text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL
);
CREATE INDEX ON session (expires_at);

-- Search matches the head revision's row. Names rank above descriptions,
-- which rank above the first message.
ALTER TABLE card_revision ADD COLUMN search tsvector GENERATED ALWAYS AS (
    setweight(to_tsvector('english', coalesce(data->>'name', '')), 'A') ||
    setweight(to_tsvector('english',
        coalesce(data->>'description', '') || ' ' || coalesce(data->>'personality', '') || ' ' ||
        coalesce(data->>'scenario', '') || ' ' || coalesce(data->>'creator_notes', '')), 'B') ||
    setweight(to_tsvector('english', coalesce(data->>'first_mes', '')), 'C')
) STORED;
CREATE INDEX card_revision_search ON card_revision USING gin (search);
