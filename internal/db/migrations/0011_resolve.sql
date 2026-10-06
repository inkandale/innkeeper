-- A resolve can leave the app behind the library without a new head revision.
ALTER TABLE push ADD COLUMN app_behind boolean NOT NULL DEFAULT false;
ALTER TABLE plugin_instance ADD COLUMN auto_bind_exact boolean NOT NULL DEFAULT false;

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
    SELECT p.app_behind OR hr.content_hash <> pr.content_hash
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
            WHERE cl.card_id = b.card_id ORDER BY lb.remote_id COLLATE "C"), FALSE) AS ahead
    FROM card c JOIN card_revision hr ON hr.id = c.head_revision_id
    JOIN card_revision pr ON pr.id = p.revision_id
    WHERE c.id = b.card_id
    UNION ALL
    -- A lorebook is ahead when its data or override for this instance changed.
    SELECT p.app_behind OR hr.content_hash <> pr.content_hash
        OR hr.overrides -> b.instance_id::text IS DISTINCT FROM pr.overrides -> b.instance_id::text
    FROM lorebook l JOIN lorebook_revision hr ON hr.id = l.head_revision_id
    JOIN lorebook_revision pr ON pr.id = p.revision_id
    WHERE l.id = b.lorebook_id
) a;

-- A resolve writes revisions with source reconcile.
ALTER TABLE card_revision DROP CONSTRAINT card_revision_source_check,
    ADD CONSTRAINT card_revision_source_check CHECK (source ~ '^(ui|mcp|adopt|reconcile|plugin:[a-z][a-z0-9-]*)$');
ALTER TABLE lorebook_revision DROP CONSTRAINT lorebook_revision_source_check,
    ADD CONSTRAINT lorebook_revision_source_check CHECK (source ~ '^(ui|mcp|adopt|reconcile|plugin:[a-z][a-z0-9-]*)$');
