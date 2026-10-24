-- A card's string tags in card order, so tag facets and listings read this
-- small column instead of detoasting every card's data.
CREATE FUNCTION card_tags(data jsonb) RETURNS text[]
LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT coalesce(array_agg(e #>> '{}' ORDER BY ord), '{}')
    FROM jsonb_array_elements(CASE jsonb_typeof(data->'tags') WHEN 'array' THEN data->'tags' ELSE '[]' END) WITH ORDINALITY x(e, ord)
    WHERE jsonb_typeof(e) = 'string'
$$;

ALTER TABLE card_revision ADD COLUMN tags text[] GENERATED ALWAYS AS (card_tags(data)) STORED;
