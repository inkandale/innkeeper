-- bind: the owner linked an item the app already had; nothing was sent.
ALTER TABLE push ADD COLUMN kind text NOT NULL DEFAULT 'push' CHECK (kind IN ('push', 'bind'));

-- Only a create-only target that reused an ID could have left two bindings
-- on one app item, so stop rather than pick one.
DO $$
DECLARE dupes text;
BEGIN
    SELECT string_agg(ids, '; ') INTO dupes FROM (
        SELECT string_agg(id::text, ', ' ORDER BY id) AS ids FROM binding WHERE remote_state <> 'missing'
        GROUP BY instance_id, remote_id, card_id IS NULL HAVING count(*) > 1) d;
    IF dupes IS NOT NULL THEN
        RAISE EXCEPTION 'bindings share an app item; unbind all but one of each group: %', dupes;
    END IF;
END $$;

-- One binding per app item. Cards and lorebooks get separate indexes, since
-- an app may number its characters and world books the same way. A missing
-- binding lets go of its ID, since an app may give it to a new item.
CREATE UNIQUE INDEX binding_card_remote ON binding (instance_id, remote_id)
    WHERE card_id IS NOT NULL AND remote_state <> 'missing';
CREATE UNIQUE INDEX binding_lorebook_remote ON binding (instance_id, remote_id)
    WHERE lorebook_id IS NOT NULL AND remote_state <> 'missing';
