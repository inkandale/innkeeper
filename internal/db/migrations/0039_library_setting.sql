-- A library's override of a core setting. A setting a library doesn't
-- override follows the core's value.
CREATE TABLE library_setting (
    library_id bigint NOT NULL REFERENCES library ON DELETE CASCADE DEFERRABLE INITIALLY IMMEDIATE,
    key text NOT NULL,
    value text NOT NULL CHECK (value <> ''),
    PRIMARY KEY (library_id, key)
);
