-- Answers "has this instance scored anything" without scanning every value.
CREATE INDEX enrichment_instance ON enrichment (instance_id);
