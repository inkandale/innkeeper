-- started_at is when the first attempt was claimed; a manual retry clears
-- it. cards_changed counts the cards the job changed, across every attempt.
ALTER TABLE job
    ADD COLUMN started_at timestamptz,
    ADD COLUMN cards_changed int NOT NULL DEFAULT 0 CHECK (cards_changed >= 0);
