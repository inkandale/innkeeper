-- The grid score now comes from every scorer by priority, and hiding by
-- score is gone, so cards it hid come back.
DELETE FROM setting WHERE key IN ('score_instance', 'hide_below');
DELETE FROM library_setting WHERE key IN ('score_instance', 'hide_below');
UPDATE card SET archived_at = NULL, archived_reason = NULL WHERE archived_reason = 'low_score';
UPDATE lorebook SET archived_at = NULL, archived_reason = NULL WHERE archived_reason = 'low_score';
