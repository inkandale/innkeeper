-- The source's dates for an import binding's accepted and waiting copies, so
-- a version older than either, sent out of order, is skipped.
ALTER TABLE binding ADD COLUMN accepted_created timestamptz,
    ADD COLUMN seen_created timestamptz;
