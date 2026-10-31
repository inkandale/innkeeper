-- Null follows the plugin's enrich_on_change. since bounds the hourly
-- sweep, so turning updates on never backfills older cards.
ALTER TABLE plugin_instance ADD COLUMN enrich_on_change boolean;
ALTER TABLE plugin_instance ADD COLUMN enrich_on_change_since timestamptz NOT NULL DEFAULT now();

-- When an archived card or lorebook was last restored: the sweep counts it
-- as changed.
ALTER TABLE card ADD COLUMN restored_at timestamptz;
ALTER TABLE lorebook ADD COLUMN restored_at timestamptz;

-- Revisions an instance's plugin failed on. The sweep doesn't send them
-- again; a bulk run the owner starts does.
CREATE TABLE enrich_failure (
    instance_id bigint NOT NULL REFERENCES plugin_instance ON DELETE CASCADE DEFERRABLE INITIALLY IMMEDIATE,
    card_id bigint NOT NULL,
    revision_id bigint NOT NULL,
    failed_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (instance_id, revision_id),
    FOREIGN KEY (revision_id, card_id) REFERENCES card_revision (id, card_id) ON DELETE CASCADE DEFERRABLE INITIALLY IMMEDIATE
);
