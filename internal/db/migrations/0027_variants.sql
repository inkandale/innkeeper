-- A variant records the card or lorebook it came from. Entities are never
-- hard-deleted, so no cascade; restore defers foreign key checks.
ALTER TABLE card ADD COLUMN variant_of_id bigint REFERENCES card (id) DEFERRABLE INITIALLY IMMEDIATE;
ALTER TABLE lorebook ADD COLUMN variant_of_id bigint REFERENCES lorebook (id) DEFERRABLE INITIALLY IMMEDIATE;
CREATE INDEX ON card (variant_of_id) WHERE variant_of_id IS NOT NULL;
CREATE INDEX ON lorebook (variant_of_id) WHERE variant_of_id IS NOT NULL;
