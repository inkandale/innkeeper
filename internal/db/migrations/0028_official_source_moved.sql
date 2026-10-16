-- The plugins repo moved to the inkandale org.
UPDATE plugin_source
SET url = 'https://github.com/inkandale/innkeeper-plugins/releases/download/index/index.toml'
WHERE id = 'official'
  AND url = 'https://github.com/davidgibbons/innkeeper-plugins/releases/download/index/index.toml';
