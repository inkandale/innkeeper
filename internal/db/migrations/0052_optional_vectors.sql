-- Vectors now come from enrich instances, not an ai instance, so the old
-- rows and setting can't be read. EnableVectors recreates the table.
DROP TABLE IF EXISTS card_embedding;
DELETE FROM setting WHERE key = 'embed_instance';
-- The embed job kind is gone; fail any still waiting so none sits in the queue.
UPDATE job SET status = 'failed', error = 'the embed job was replaced by enrich plugins', finished_at = now(),
    locked_by = NULL, locked_until = NULL
WHERE kind = 'embed' AND status IN ('queued', 'running');
