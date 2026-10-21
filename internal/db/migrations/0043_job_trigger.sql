-- What started the job: a person through the API or MCP, the worker's
-- schedule, or another job as part of its work.
ALTER TABLE job ADD COLUMN trigger text NOT NULL DEFAULT 'manual'
    CHECK (trigger IN ('manual', 'scheduled', 'automatic'));
