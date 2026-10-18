-- A tsvector holds at most 1 MB, and an archive card can carry several MB of
-- text, so search reads only the first 100,000 characters of each field group.
-- PostgreSQL 16 can't change a generated column, so each is rebuilt.
ALTER TABLE card_revision DROP COLUMN IF EXISTS search;
ALTER TABLE card_revision ADD COLUMN search tsvector GENERATED ALWAYS AS (
    setweight(to_tsvector('english', left(coalesce(data->>'name', ''), 100000)), 'A') ||
    setweight(to_tsvector('english', left(
        coalesce(data->>'description', '') || ' ' || coalesce(data->>'personality', '') || ' ' ||
        coalesce(data->>'scenario', '') || ' ' || coalesce(data->>'creator_notes', ''), 100000)), 'B') ||
    setweight(to_tsvector('english', left(coalesce(data->>'first_mes', ''), 100000)), 'C')
) STORED;
CREATE INDEX card_revision_search ON card_revision USING gin (search);

ALTER TABLE lorebook_revision DROP COLUMN IF EXISTS search;
ALTER TABLE lorebook_revision ADD COLUMN search tsvector GENERATED ALWAYS AS (
    setweight(to_tsvector('english', left(coalesce(data->>'name', ''), 100000)), 'A') ||
    setweight(to_tsvector('english', left(
        coalesce(data->>'description', '') || ' ' ||
        coalesce(jsonb_path_query_array(data, '$.entries[*].keys[*]')::text, ''), 100000)), 'B') ||
    setweight(to_tsvector('english', left(coalesce(jsonb_path_query_array(data, '$.entries[*].content')::text, ''), 100000)), 'C')
) STORED;
CREATE INDEX lorebook_revision_search ON lorebook_revision USING gin (search);
