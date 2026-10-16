-- For each config override, a hash of the default it replaced, so the form can mark one whose default has since changed.
ALTER TABLE plugin_instance ADD COLUMN default_hashes jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(default_hashes) = 'object');
