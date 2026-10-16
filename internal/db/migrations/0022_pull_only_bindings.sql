-- push is false for a binding that follows a site source on a pull-only
-- target: the core only reads it.
ALTER TABLE binding ADD COLUMN push boolean NOT NULL DEFAULT true,
    -- The owner stopped checking it; checks skip it.
    ADD COLUMN paused boolean NOT NULL DEFAULT false,
    -- The site's avatar as last accepted, for a pull-only binding: its
    -- blob's pixel_hash, so a re-encode isn't a change, else its sha256.
    ADD COLUMN avatar_hash text,
    -- Checks of a pull-only binding that failed in a row, and the last error.
    ADD COLUMN check_failures int NOT NULL DEFAULT 0,
    ADD COLUMN check_error text;

-- Cards that share a source each follow it, so only push bindings own their item.
DROP INDEX binding_card_remote;
DROP INDEX binding_lorebook_remote;
CREATE UNIQUE INDEX binding_card_remote ON binding (instance_id, remote_id)
    WHERE card_id IS NOT NULL AND remote_state <> 'missing' AND push;
CREATE UNIQUE INDEX binding_lorebook_remote ON binding (instance_id, remote_id)
    WHERE lorebook_id IS NOT NULL AND remote_state <> 'missing' AND push;

-- As in 0011, except that a pull-only binding is never ahead: nothing pushes it.
DROP VIEW binding_status;
CREATE VIEW binding_status AS
SELECT b.id AS binding_id, b.instance_id, b.card_id, b.lorebook_id, a.ahead,
    CASE
        WHEN b.remote_state = 'missing' THEN 'missing'
        WHEN b.remote_state = 'changed' AND a.ahead THEN 'diverged'
        WHEN b.remote_state = 'changed' THEN 'remote_changed'
        WHEN a.ahead THEN 'ahead'
        WHEN b.remote_state = 'unknown' THEN 'cant_check'
        ELSE 'in_sync'
    END AS status
FROM binding b
JOIN LATERAL (SELECT revision_id, lorebook_revision_ids, lorebook_remote_ids, app_behind FROM push WHERE binding_id = b.id ORDER BY id DESC LIMIT 1) p ON TRUE
CROSS JOIN LATERAL (
    -- A card is ahead when its data, avatar, override for this instance, or
    -- lorebooks changed since the last push.
    SELECT b.push AND (p.app_behind OR hr.content_hash <> pr.content_hash
        OR hr.avatar_blob IS DISTINCT FROM pr.avatar_blob
        OR hr.overrides -> b.instance_id::text IS DISTINCT FROM pr.overrides -> b.instance_id::text
        OR coalesce(p.lorebook_revision_ids <> array(
            SELECT l.head_revision_id FROM card_lorebook cl JOIN lorebook l ON l.id = cl.lorebook_id
            WHERE cl.card_id = b.card_id ORDER BY l.head_revision_id), FALSE)
        -- A linked lorebook unbound here adds a NULL, which no push sent.
        OR coalesce(p.lorebook_remote_ids <> array(
            SELECT lb.remote_id FROM card_lorebook cl
            LEFT JOIN binding lb ON lb.lorebook_id = cl.lorebook_id AND lb.instance_id = b.instance_id
            -- Byte order, as the core sorts them.
            WHERE cl.card_id = b.card_id ORDER BY lb.remote_id COLLATE "C"), FALSE)) AS ahead
    FROM card c JOIN card_revision hr ON hr.id = c.head_revision_id
    JOIN card_revision pr ON pr.id = p.revision_id
    WHERE c.id = b.card_id
    UNION ALL
    -- A lorebook is ahead when its data or override for this instance changed.
    SELECT b.push AND (p.app_behind OR hr.content_hash <> pr.content_hash
        OR hr.overrides -> b.instance_id::text IS DISTINCT FROM pr.overrides -> b.instance_id::text)
    FROM lorebook l JOIN lorebook_revision hr ON hr.id = l.head_revision_id
    JOIN lorebook_revision pr ON pr.id = p.revision_id
    WHERE l.id = b.lorebook_id
) a;
