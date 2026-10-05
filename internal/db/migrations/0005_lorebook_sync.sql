-- Remote IDs of the card's lorebooks the push sent, sorted. Null unless the
-- target keeps lorebooks on their own.
ALTER TABLE push ADD COLUMN lorebook_remote_ids text[];

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
JOIN LATERAL (SELECT * FROM push WHERE binding_id = b.id ORDER BY id DESC LIMIT 1) p ON TRUE
CROSS JOIN LATERAL (
    -- A card is ahead when its data, avatar, override for this instance, or
    -- lorebooks changed since the last push.
    SELECT hr.content_hash <> pr.content_hash
        OR hr.avatar_blob IS DISTINCT FROM pr.avatar_blob
        OR hr.overrides -> b.instance_id::text IS DISTINCT FROM pr.overrides -> b.instance_id::text
        OR coalesce(p.lorebook_revision_ids <> array(
            SELECT l.head_revision_id FROM card_lorebook cl JOIN lorebook l ON l.id = cl.lorebook_id
            WHERE cl.card_id = b.card_id ORDER BY l.head_revision_id), FALSE)
        OR coalesce(p.lorebook_remote_ids <> array(
            SELECT lb.remote_id FROM card_lorebook cl
            JOIN binding lb ON lb.lorebook_id = cl.lorebook_id AND lb.instance_id = b.instance_id
            WHERE cl.card_id = b.card_id ORDER BY lb.remote_id), FALSE) AS ahead
    FROM card c JOIN card_revision hr ON hr.id = c.head_revision_id
    JOIN card_revision pr ON pr.id = p.revision_id
    WHERE c.id = b.card_id
    UNION ALL
    -- A lorebook is ahead when its data or override for this instance changed.
    SELECT hr.content_hash <> pr.content_hash
        OR hr.overrides -> b.instance_id::text IS DISTINCT FROM pr.overrides -> b.instance_id::text
    FROM lorebook l JOIN lorebook_revision hr ON hr.id = l.head_revision_id
    JOIN lorebook_revision pr ON pr.id = p.revision_id
    WHERE l.id = b.lorebook_id
) a;
