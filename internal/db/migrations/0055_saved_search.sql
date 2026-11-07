-- A named filter the owner opens to browse, and rules act on. A plugin's
-- searches name the instance that shipped them. No foreign key: saved
-- searches back up with the library, instances with config, and deleting an
-- instance deletes its searches through the API.
CREATE TABLE saved_search (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name text NOT NULL UNIQUE CHECK (name <> '' AND name = btrim(name)),
    filter jsonb NOT NULL,
    sort text NOT NULL DEFAULT '',
    instance_id bigint,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON saved_search (instance_id) WHERE instance_id IS NOT NULL;
