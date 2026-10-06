-- Restore defers foreign key checks: card and card_revision reference each
-- other, and replacing assets briefly removes cards that bindings point to.
-- New foreign keys must be DEFERRABLE too.
DO $$
DECLARE r record;
BEGIN
    FOR r IN SELECT conrelid::regclass AS tbl, conname FROM pg_constraint
        WHERE contype = 'f' AND connamespace = 'public'::regnamespace AND NOT condeferrable
    LOOP
        EXECUTE format('ALTER TABLE %s ALTER CONSTRAINT %I DEFERRABLE INITIALLY IMMEDIATE', r.tbl, r.conname);
    END LOOP;
END $$;

-- A restore replacing the library sets innkeeper.restore for its transaction
-- so it can delete revisions; nothing else may change them.
CREATE OR REPLACE FUNCTION forbid_revision_change() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF current_setting('innkeeper.restore', true) = 'on' THEN
        IF TG_OP = 'DELETE' THEN
            RETURN OLD;
        END IF;
        RETURN NEW;
    END IF;
    RAISE EXCEPTION '% rows are immutable', TG_TABLE_NAME;
END
$$;
