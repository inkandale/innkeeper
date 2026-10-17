-- A disabled instance keeps its settings, but nothing uses it.
ALTER TABLE plugin_instance ADD COLUMN disabled boolean NOT NULL DEFAULT false;
