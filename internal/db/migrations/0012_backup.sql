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
