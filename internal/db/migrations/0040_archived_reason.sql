-- Why the hide job archived a card: low_score or duplicate. NULL is the
-- owner's own archiving, which the hide job never undoes.
ALTER TABLE card ADD COLUMN archived_reason text
    CHECK (archived_reason IS NULL OR (archived_reason IN ('low_score', 'duplicate') AND archived_at IS NOT NULL));
ALTER TABLE lorebook ADD COLUMN archived_reason text
    CHECK (archived_reason IS NULL OR (archived_reason IN ('low_score', 'duplicate') AND archived_at IS NOT NULL));
