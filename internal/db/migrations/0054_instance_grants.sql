-- What the owner has granted an instance, from its plugin's permissions.
ALTER TABLE plugin_instance ADD COLUMN grants text[] NOT NULL DEFAULT '{}';
UPDATE plugin_instance SET grants = '{library.read}' WHERE library_access;
ALTER TABLE plugin_instance DROP COLUMN library_access;
