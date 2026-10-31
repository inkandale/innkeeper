-- Vectors now come from enrich instances, not an ai instance, so the old
-- rows and setting can't be read. EnableVectors recreates the table.
DROP TABLE IF EXISTS card_embedding;
DELETE FROM setting WHERE key = 'embed_instance';
