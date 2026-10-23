-- hash_bands16 splits a 64-bit hash into 4 bands of 16 bits, each tagged with
-- its band number. Hashes within 3 bits share a band, and a 16-bit band matches
-- far fewer unrelated hashes than hash_bands' 7-bit ones.
CREATE FUNCTION hash_bands16(h bigint) RETURNS int[] LANGUAGE sql IMMUTABLE STRICT PARALLEL SAFE AS $$
    SELECT array_agg(b << 16 | ((h >> (16 * b)) & 65535)::int ORDER BY b) FROM generate_series(0, 3) b
$$;

ALTER TABLE card_revision ADD COLUMN text_bands16 int[] GENERATED ALWAYS AS (hash_bands16(text_simhash)) STORED;
CREATE INDEX ON card_revision USING gin (text_bands16);
