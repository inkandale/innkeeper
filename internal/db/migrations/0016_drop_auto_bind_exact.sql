-- Every sync binds exact matches now; the per-instance switch is gone.
ALTER TABLE plugin_instance DROP COLUMN auto_bind_exact;
