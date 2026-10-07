-- Where the owner installs plugins from. "builtin" names the image's plugins.
CREATE TABLE plugin_source (
    id text PRIMARY KEY CHECK (id ~ '^[a-z0-9][a-z0-9-]{0,31}$' AND id <> 'builtin'),
    url text NOT NULL UNIQUE CHECK (url ~ '^(https|file)://'),
    created_at timestamptz NOT NULL DEFAULT now()
);

INSERT INTO plugin_source (id, url)
VALUES ('official', 'https://github.com/davidgibbons/innkeeper-plugins/releases/download/index/index.toml');

-- One row per plugin the owner has installed. Uninstalling nulls the version
-- but keeps the row, so changed_at still gives the plugin's instances a new
-- process version.
CREATE TABLE plugin_install (
    name text PRIMARY KEY,
    source text REFERENCES plugin_source DEFERRABLE INITIALLY IMMEDIATE,
    version text,
    sha256 text,
    previous_source text REFERENCES plugin_source ON DELETE SET NULL DEFERRABLE INITIALLY IMMEDIATE,
    previous_version text,
    previous_sha256 text,
    changed_at timestamptz NOT NULL DEFAULT now(),
    CHECK ((source IS NULL) = (version IS NULL) AND (version IS NULL) = (sha256 IS NULL))
);

-- Every install and uninstall takes a value, so other processes know to reload.
CREATE SEQUENCE plugin_generation;
