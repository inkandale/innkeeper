-- No worker runs catalog.ingest any more, so a queued one would wait forever.
DELETE FROM job WHERE kind = 'catalog.ingest';
