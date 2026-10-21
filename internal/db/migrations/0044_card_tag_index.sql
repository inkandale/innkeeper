-- A card's string tags, lower-cased, so a tag filter can use an index
-- instead of unpacking every card's data.tags.
CREATE FUNCTION card_tag_keys(data jsonb) RETURNS text[]
LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT coalesce(array_agg(DISTINCT lower(e #>> '{}')), '{}')
    FROM jsonb_array_elements(CASE jsonb_typeof(data->'tags') WHEN 'array' THEN data->'tags' ELSE '[]' END) e
    WHERE jsonb_typeof(e) = 'string'
$$;
CREATE INDEX card_revision_tag_keys ON card_revision USING gin (card_tag_keys(data));
