CREATE TABLE blob (
    sha256 text PRIMARY KEY CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    size bigint NOT NULL CHECK (size >= 0),
    mime text NOT NULL,
    store text NOT NULL,
    avatar_phash bigint,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- Revision rows never change. Undo and restore write new revisions.
CREATE FUNCTION forbid_revision_change() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION '% rows are immutable', TG_TABLE_NAME;
END
$$;

CREATE TABLE card (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    slug text NOT NULL UNIQUE CHECK (slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$'),
    -- Null only inside the transaction that creates the card.
    head_revision_id bigint,
    archived_at timestamptz,
    provenance text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE card_revision (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    card_id bigint NOT NULL REFERENCES card,
    parent_id bigint,
    data jsonb NOT NULL CHECK (jsonb_typeof(data) = 'object' AND NOT data ? 'character_book'),
    avatar_blob text REFERENCES blob,
    overrides jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(overrides) = 'object'),
    content_hash text NOT NULL,
    text_simhash bigint,
    message text NOT NULL DEFAULT '',
    source text NOT NULL CHECK (source ~ '^(ui|mcp|adopt|plugin:[a-z][a-z0-9-]*)$'),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (id, card_id),
    FOREIGN KEY (parent_id, card_id) REFERENCES card_revision (id, card_id)
);

ALTER TABLE card ADD FOREIGN KEY (head_revision_id, id) REFERENCES card_revision (id, card_id);
CREATE INDEX ON card_revision (card_id, id);
CREATE INDEX ON card_revision (content_hash);
CREATE TRIGGER card_revision_immutable BEFORE UPDATE OR DELETE ON card_revision
    FOR EACH ROW EXECUTE FUNCTION forbid_revision_change();

CREATE TABLE lorebook (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    slug text NOT NULL UNIQUE CHECK (slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$'),
    head_revision_id bigint,
    archived_at timestamptz,
    provenance text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE lorebook_revision (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    lorebook_id bigint NOT NULL REFERENCES lorebook,
    parent_id bigint,
    data jsonb NOT NULL CHECK (jsonb_typeof(data) = 'object'),
    avatar_blob text REFERENCES blob,
    overrides jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(overrides) = 'object'),
    content_hash text NOT NULL,
    text_simhash bigint,
    message text NOT NULL DEFAULT '',
    source text NOT NULL CHECK (source ~ '^(ui|mcp|adopt|plugin:[a-z][a-z0-9-]*)$'),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (id, lorebook_id),
    FOREIGN KEY (parent_id, lorebook_id) REFERENCES lorebook_revision (id, lorebook_id)
);

ALTER TABLE lorebook ADD FOREIGN KEY (head_revision_id, id) REFERENCES lorebook_revision (id, lorebook_id);
CREATE INDEX ON lorebook_revision (lorebook_id, id);
CREATE INDEX ON lorebook_revision (content_hash);
CREATE TRIGGER lorebook_revision_immutable BEFORE UPDATE OR DELETE ON lorebook_revision
    FOR EACH ROW EXECUTE FUNCTION forbid_revision_change();

CREATE TABLE card_lorebook (
    card_id bigint NOT NULL REFERENCES card,
    lorebook_id bigint NOT NULL REFERENCES lorebook,
    PRIMARY KEY (card_id, lorebook_id)
);
CREATE INDEX ON card_lorebook (lorebook_id);

CREATE TABLE tag (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name text NOT NULL CHECK (name <> '' AND name = btrim(name))
);
CREATE UNIQUE INDEX tag_name_key ON tag (lower(name));

CREATE TABLE card_tag (
    card_id bigint NOT NULL REFERENCES card,
    tag_id bigint NOT NULL REFERENCES tag,
    PRIMARY KEY (card_id, tag_id)
);
CREATE INDEX ON card_tag (tag_id);

CREATE TABLE collection (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name text NOT NULL UNIQUE CHECK (name <> ''),
    description text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE collection_card (
    collection_id bigint NOT NULL REFERENCES collection ON DELETE CASCADE,
    card_id bigint NOT NULL REFERENCES card,
    added_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (collection_id, card_id)
);
CREATE INDEX ON collection_card (card_id);
