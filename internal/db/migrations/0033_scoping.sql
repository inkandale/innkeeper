-- Library names are trimmed, so a blank one can't pass as a name.
ALTER TABLE library DROP CONSTRAINT library_name_check,
    ADD CHECK (name <> '' AND name = btrim(name));

-- A collection holds cards of one library.
ALTER TABLE collection ADD COLUMN library_id bigint NOT NULL DEFAULT 1 REFERENCES library DEFERRABLE INITIALLY IMMEDIATE,
    DROP CONSTRAINT collection_name_key,
    ADD UNIQUE (library_id, name);

-- Deleting a library sets innkeeper.delete for its transactions so it can
-- delete revisions; a restore sets innkeeper.restore. Nothing else may change them.
CREATE OR REPLACE FUNCTION forbid_revision_change() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF current_setting('innkeeper.restore', true) = 'on' THEN
        IF TG_OP = 'DELETE' THEN
            RETURN OLD;
        END IF;
        RETURN NEW;
    END IF;
    IF TG_OP = 'DELETE' AND current_setting('innkeeper.delete', true) = 'on' THEN
        RETURN OLD;
    END IF;
    RAISE EXCEPTION '% rows are immutable', TG_TABLE_NAME;
END
$$;
