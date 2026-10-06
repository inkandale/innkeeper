-- Sites an item was published on. Cards and lorebooks are never hard-deleted, so no cascade.
CREATE TABLE card_source (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    card_id bigint NOT NULL REFERENCES card,
    kind text NOT NULL CHECK (kind ~ '^[a-z][a-z0-9-]*$'),
    ref text NOT NULL CHECK (ref <> ''),
    external_id text,
    url text,
    linked_by text NOT NULL CHECK (linked_by IN ('auto', 'owner')),
    -- Set when the owner removes it, so detection doesn't add it back.
    removed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (card_id, kind, ref)
);
CREATE INDEX ON card_source (kind, ref) WHERE removed_at IS NULL;

CREATE TABLE lorebook_source (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    lorebook_id bigint NOT NULL REFERENCES lorebook,
    kind text NOT NULL CHECK (kind ~ '^[a-z][a-z0-9-]*$'),
    ref text NOT NULL CHECK (ref <> ''),
    external_id text,
    url text,
    linked_by text NOT NULL CHECK (linked_by IN ('auto', 'owner')),
    -- Set when the owner removes it, so detection doesn't add it back.
    removed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (lorebook_id, kind, ref)
);
CREATE INDEX ON lorebook_source (kind, ref) WHERE removed_at IS NULL;
