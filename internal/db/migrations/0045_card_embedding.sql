-- One vector per card per embed instance, from the revision named. No
-- fixed dimension, so switching to a model of another size needs no migration.
CREATE EXTENSION IF NOT EXISTS vector;
CREATE TABLE card_embedding (
    card_id bigint NOT NULL REFERENCES card (id) ON DELETE CASCADE DEFERRABLE INITIALLY IMMEDIATE,
    instance_id bigint NOT NULL REFERENCES plugin_instance ON DELETE CASCADE DEFERRABLE INITIALLY IMMEDIATE,
    revision_id bigint NOT NULL,
    embedding vector NOT NULL,
    PRIMARY KEY (instance_id, card_id),
    FOREIGN KEY (revision_id, card_id) REFERENCES card_revision (id, card_id) ON DELETE CASCADE DEFERRABLE INITIALLY IMMEDIATE
);
