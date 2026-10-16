-- Values enrich plugins computed about card revisions. Every result is kept;
-- a key's current value for a card and instance is its newest row on the newest revision.
CREATE TABLE enrichment (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    card_id bigint NOT NULL REFERENCES card ON DELETE CASCADE DEFERRABLE INITIALLY IMMEDIATE,
    revision_id bigint NOT NULL,
    -- No foreign key: deleting an instance keeps its results.
    instance_id bigint NOT NULL,
    key text NOT NULL,
    value jsonb NOT NULL,
    meta jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(meta) = 'object'),
    created_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (revision_id, card_id) REFERENCES card_revision (id, card_id) DEFERRABLE INITIALLY IMMEDIATE
);
CREATE INDEX enrichment_current ON enrichment (card_id, key, instance_id, revision_id DESC, created_at DESC);
