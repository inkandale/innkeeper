-- Core settings the owner changes at runtime, one row per key.
CREATE TABLE setting (
    key text PRIMARY KEY,
    value text NOT NULL
);
