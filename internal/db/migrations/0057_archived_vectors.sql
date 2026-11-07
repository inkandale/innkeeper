-- Archived cards keep no vectors; db.EnableVectors' trigger keeps it so from
-- here on. card_embedding exists only once EnableVectors has run with
-- pgvector, so an install without it has nothing to delete.
DO $$
BEGIN
    IF to_regclass('card_embedding') IS NOT NULL THEN
        DELETE FROM card_embedding e USING card c WHERE c.id = e.card_id AND c.archived_at IS NOT NULL;
    END IF;
END $$;
