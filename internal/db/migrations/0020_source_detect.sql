-- Entities waiting for source detection. Imports add rows; the
-- detect-sources job records each one's sources and deletes the row.
CREATE TABLE source_detect (
    kind text NOT NULL,
    entity_id bigint NOT NULL,
    PRIMARY KEY (kind, entity_id)
);
