-- The newest enrichment row the vector's text came from, or null for the
-- card's own fields. A newer one makes the vector stale.
ALTER TABLE card_embedding ADD COLUMN enrichment_id bigint;
