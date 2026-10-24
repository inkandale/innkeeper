-- A revision's tags lower-cased and deduplicated, read from the tags column
-- so a tag filter never detoasts a card's data.
CREATE FUNCTION tag_keys(tags text[]) RETURNS text[]
LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT coalesce(array_agg(DISTINCT lower(t)), '{}') FROM unnest(tags) t
$$;
CREATE INDEX card_revision_tag_keys_of_tags ON card_revision USING gin (tag_keys(tags));
