-- Search matches the head revision's row. Names rank above descriptions and
-- entry keys, which rank above entry content.
ALTER TABLE lorebook_revision ADD COLUMN search tsvector GENERATED ALWAYS AS (
    setweight(to_tsvector('english', coalesce(data->>'name', '')), 'A') ||
    setweight(to_tsvector('english',
        coalesce(data->>'description', '') || ' ' ||
        coalesce(jsonb_path_query_array(data, '$.entries[*].keys[*]')::text, '')), 'B') ||
    setweight(to_tsvector('english', coalesce(jsonb_path_query_array(data, '$.entries[*].content')::text, '')), 'C')
) STORED;
CREATE INDEX lorebook_revision_search ON lorebook_revision USING gin (search);
