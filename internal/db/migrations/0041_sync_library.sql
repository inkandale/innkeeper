-- The library a target app's syncs match against and import into. NULL is
-- library 1, which is also what a deleted library falls back to.
ALTER TABLE plugin_instance ADD COLUMN sync_library bigint
    REFERENCES library ON DELETE SET NULL DEFERRABLE INITIALLY IMMEDIATE;
