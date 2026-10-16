CREATE TABLE plugin_instance (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    plugin text NOT NULL CHECK (plugin ~ '^[a-z][a-z0-9]*(-[a-z0-9]+)*$'),
    name text NOT NULL UNIQUE CHECK (name <> ''),
    config jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(config) = 'object'),
    -- Set when the plugin fails to start. While set, nothing starts it.
    broken_error text,
    broken_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE job (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    kind text NOT NULL CHECK (kind ~ '^[a-z][a-z0-9_.-]*$'),
    instance_id bigint REFERENCES plugin_instance,
    payload jsonb NOT NULL DEFAULT '{}',
    -- A bulk action's parent job is 'waiting' until its last child finishes.
    status text NOT NULL DEFAULT 'queued'
        CHECK (status IN ('queued', 'running', 'waiting', 'succeeded', 'failed')),
    attempts int NOT NULL DEFAULT 0,
    max_attempts int NOT NULL DEFAULT 8 CHECK (max_attempts > 0),
    progress jsonb NOT NULL DEFAULT '{}',
    checkpoint jsonb,
    run_after timestamptz NOT NULL DEFAULT now(),
    locked_by text,
    locked_until timestamptz,
    parent_id bigint REFERENCES job,
    error text,
    created_at timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz
);
CREATE INDEX job_claim ON job (run_after, id) WHERE status = 'queued';
CREATE INDEX job_lease ON job (locked_until) WHERE status = 'running';
CREATE INDEX ON job (parent_id);
CREATE INDEX ON job (kind, finished_at);

-- Workers LISTEN on innkeeper_job. Firing from a trigger covers every way a
-- job becomes queued, including a retry and a row inserted with psql.
CREATE FUNCTION notify_job_queued() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    PERFORM pg_notify('innkeeper_job', '');
    RETURN NULL;
END
$$;
CREATE TRIGGER job_queued AFTER INSERT OR UPDATE OF status ON job
    FOR EACH ROW WHEN (NEW.status = 'queued') EXECUTE FUNCTION notify_job_queued();

-- Imports look up earlier imports of the same item by provenance.
CREATE INDEX ON card (provenance);
CREATE INDEX ON lorebook (provenance);

-- Blob garbage collection looks for revisions that still use a blob.
CREATE INDEX ON card_revision (avatar_blob);
CREATE INDEX ON lorebook_revision (avatar_blob);
