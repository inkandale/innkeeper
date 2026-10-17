-- An import binding records what an import plugin last sent for an item:
-- accepted_data and avatar_hash are the copy last taken into the library;
-- seen_data and seen_avatar a newer one waiting because the owner edited it.
ALTER TABLE binding ADD COLUMN imported boolean NOT NULL DEFAULT false,
    ADD COLUMN seen_avatar text;
CREATE INDEX ON binding (seen_avatar) WHERE seen_avatar IS NOT NULL;

-- An instance that both imports and follows sources can hold both for one entity.
ALTER TABLE binding DROP CONSTRAINT binding_instance_id_card_id_key,
    DROP CONSTRAINT binding_instance_id_lorebook_id_key,
    ADD UNIQUE (instance_id, card_id, imported),
    ADD UNIQUE (instance_id, lorebook_id, imported);
