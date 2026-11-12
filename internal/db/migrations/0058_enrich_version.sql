-- The manifest's enrich_version each value was scored under, so a plugin that
-- changes what its scores mean can have them rescored. Rows before it are 0.
ALTER TABLE enrichment ADD COLUMN enrich_version int NOT NULL DEFAULT 0;

-- card_embedding exists only once db.EnableVectors has run with pgvector;
-- EnableVectors creates it with the column.
DO $$
BEGIN
    IF to_regclass('card_embedding') IS NOT NULL THEN
        ALTER TABLE card_embedding ADD COLUMN enrich_version int NOT NULL DEFAULT 0;
    END IF;
END $$;
