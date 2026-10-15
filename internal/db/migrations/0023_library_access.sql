-- The owner lets an action plugin's instance read the library with library.call.
ALTER TABLE plugin_instance ADD COLUMN library_access boolean NOT NULL DEFAULT false;
