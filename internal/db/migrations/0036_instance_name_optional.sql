-- A name only tells apart instances of one plugin, so it is optional and
-- unique within the plugin, with at most one unnamed instance per plugin.
ALTER TABLE plugin_instance ALTER COLUMN name DROP NOT NULL;
ALTER TABLE plugin_instance DROP CONSTRAINT IF EXISTS plugin_instance_name_key;
UPDATE plugin_instance SET name = NULL WHERE name = plugin;
CREATE UNIQUE INDEX IF NOT EXISTS plugin_instance_plugin_name_key ON plugin_instance (plugin, name);
CREATE UNIQUE INDEX IF NOT EXISTS plugin_instance_unnamed_key ON plugin_instance (plugin) WHERE name IS NULL;
