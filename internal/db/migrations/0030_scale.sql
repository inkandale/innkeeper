-- hash_bands splits a 64-bit hash into 9 bands of 7 bits, ignoring the top
-- bit, each tagged with its band number. Hashes within 8 bits differ in at
-- most 8 bands, so they share one, and a GIN index finds them.
CREATE FUNCTION hash_bands(h bigint) RETURNS int[] LANGUAGE sql IMMUTABLE STRICT PARALLEL SAFE AS $$
    SELECT array_agg(b << 7 | ((h >> (7 * b)) & 127)::int ORDER BY b) FROM generate_series(0, 8) b
$$;

-- Adding a stored generated column computes it for existing rows without
-- firing the revision immutability triggers.
ALTER TABLE card_revision ADD COLUMN text_bands int[] GENERATED ALWAYS AS (hash_bands(text_simhash)) STORED;
ALTER TABLE lorebook_revision ADD COLUMN text_bands int[] GENERATED ALWAYS AS (hash_bands(text_simhash)) STORED;
ALTER TABLE blob ADD COLUMN phash_bands int[] GENERATED ALWAYS AS (hash_bands(avatar_phash)) STORED;
CREATE INDEX ON card_revision USING gin (text_bands);
CREATE INDEX ON lorebook_revision USING gin (text_bands);
CREATE INDEX ON blob USING gin (phash_bands);

-- Search filters on library first, then sorts newest or last updated first.
DROP INDEX card_library_id_idx;
DROP INDEX lorebook_library_id_idx;
CREATE INDEX ON card (library_id, id);
CREATE INDEX ON card (library_id, head_revision_id, id);
CREATE INDEX ON lorebook (library_id, id);
CREATE INDEX ON lorebook (library_id, head_revision_id, id);
