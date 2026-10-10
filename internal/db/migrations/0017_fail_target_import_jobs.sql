-- No handler runs target.import any more; fail the leftovers so they don't sit queued forever.
UPDATE job SET status = 'failed', error = 'importing from an app was removed; sync the app instead', finished_at = now()
WHERE kind = 'target.import' AND status IN ('queued', 'running', 'waiting');
