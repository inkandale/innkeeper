-- A saved search with an action the hide job keeps applied in the libraries
-- it covers. Deleting a saved search, which the API allows only without
-- rules, or its instance takes its rules with it.
CREATE TABLE rule (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name text NOT NULL UNIQUE CHECK (name <> '' AND name = btrim(name)),
    saved_search_id bigint NOT NULL REFERENCES saved_search ON DELETE CASCADE DEFERRABLE INITIALLY IMMEDIATE,
    action text NOT NULL CHECK (action IN ('archive', 'collect')),
    collection_id bigint REFERENCES collection ON DELETE CASCADE DEFERRABLE INITIALLY IMMEDIATE,
    all_libraries boolean NOT NULL DEFAULT true,
    enabled boolean NOT NULL DEFAULT false,
    CHECK ((action = 'collect') = (collection_id IS NOT NULL))
);

-- A rule's libraries when all_libraries is false.
CREATE TABLE rule_library (
    rule_id bigint NOT NULL REFERENCES rule ON DELETE CASCADE DEFERRABLE INITIALLY IMMEDIATE,
    library_id bigint NOT NULL REFERENCES library ON DELETE CASCADE DEFERRABLE INITIALLY IMMEDIATE,
    PRIMARY KEY (rule_id, library_id)
);

-- The collect rule that added a membership; NULL is the owner's.
ALTER TABLE collection_card ADD COLUMN rule_id bigint REFERENCES rule ON DELETE CASCADE DEFERRABLE INITIALLY IMMEDIATE;
CREATE INDEX ON collection_card (rule_id) WHERE rule_id IS NOT NULL;

-- rule:<id> is an archive rule's reason. low_score went with migration 0049.
-- Postgres named the 0040 inline check after no column (it spans two), so find it by definition.
DO $$
DECLARE c text;
BEGIN
    SELECT conname INTO c FROM pg_constraint
    WHERE conrelid = 'card'::regclass AND contype = 'c' AND pg_get_constraintdef(oid) LIKE '%archived_reason%';
    EXECUTE format('ALTER TABLE card DROP CONSTRAINT %I', c);
END $$;
ALTER TABLE card ADD CONSTRAINT card_archived_reason_check CHECK (archived_reason IS NULL OR
    ((archived_reason = 'duplicate' OR archived_reason ~ '^rule:[1-9][0-9]*$') AND archived_at IS NOT NULL));
