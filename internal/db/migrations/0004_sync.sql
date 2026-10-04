-- A binding links a library card to its copy in a target app. lorebook_id is
-- for targets that keep lorebooks on their own; nothing sets it yet.
CREATE TABLE binding (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    -- No cascade: an instance can't be deleted while anything is bound to it.
    instance_id bigint NOT NULL REFERENCES plugin_instance,
    card_id bigint REFERENCES card,
    lorebook_id bigint REFERENCES lorebook,
    remote_id text NOT NULL CHECK (remote_id <> ''),
    -- unknown: the target can't pull. unchanged or changed: the last check.
    -- missing: the app no longer has remote_id.
    remote_state text NOT NULL CHECK (remote_state IN ('unknown', 'unchanged', 'changed', 'missing')),
    -- The app's copy, limited to the fields it stores, as of the last push
    -- or adopt. Checks compare against accepted_hash.
    accepted_data jsonb NOT NULL,
    accepted_hash text NOT NULL,
    -- The app's copy at the last check, while it differs from accepted_data.
    seen_data jsonb,
    checked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (num_nonnulls(card_id, lorebook_id) = 1),
    UNIQUE (instance_id, card_id),
    UNIQUE (instance_id, lorebook_id)
);
CREATE INDEX ON binding (card_id);
CREATE INDEX ON binding (lorebook_id);

CREATE TABLE push (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    binding_id bigint NOT NULL REFERENCES binding ON DELETE CASCADE,
    -- A card_revision or lorebook_revision, by the binding's kind.
    revision_id bigint NOT NULL,
    -- Head revisions of the lorebooks embedded in the card, sorted. Null when
    -- the target doesn't embed lorebooks.
    lorebook_revision_ids bigint[],
    pushed_hash text NOT NULL,
    remote_id text NOT NULL CHECK (remote_id <> ''),
    pushed_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON push (binding_id, id);

-- How each card binding stands. The card is ahead when its data, avatar,
-- override for this instance, or embedded lorebooks changed since the last
-- push. Lorebook bindings will need their own branch here.
CREATE VIEW binding_status AS
SELECT b.id AS binding_id, b.instance_id, b.card_id, a.ahead,
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
JOIN card c ON c.id = b.card_id
JOIN card_revision hr ON hr.id = c.head_revision_id
JOIN card_revision pr ON pr.id = p.revision_id
CROSS JOIN LATERAL (SELECT
    hr.content_hash <> pr.content_hash
    OR hr.avatar_blob IS DISTINCT FROM pr.avatar_blob
    OR hr.overrides -> b.instance_id::text IS DISTINCT FROM pr.overrides -> b.instance_id::text
    OR coalesce(p.lorebook_revision_ids <> array(
        SELECT l.head_revision_id FROM card_lorebook cl JOIN lorebook l ON l.id = cl.lorebook_id
        WHERE cl.card_id = b.card_id ORDER BY l.head_revision_id), FALSE) AS ahead) a;
