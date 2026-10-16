-- GET /jobs?card_id finds jobs by what their payload contains.
CREATE INDEX job_payload ON job USING gin (payload jsonb_path_ops);
