-- What the worker and a job's plugin said while the job ran.
CREATE TABLE IF NOT EXISTS job_log (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    job_id bigint NOT NULL REFERENCES job ON DELETE CASCADE DEFERRABLE INITIALLY IMMEDIATE,
    at timestamptz NOT NULL DEFAULT clock_timestamp(),
    level text NOT NULL CHECK (level IN ('info', 'warn', 'error')),
    message text NOT NULL
);
CREATE INDEX IF NOT EXISTS job_log_job_id ON job_log (job_id, id);
