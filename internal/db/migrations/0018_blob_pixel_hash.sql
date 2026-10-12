-- The SHA-256 of an image's decoded pixels, so the same picture with
-- different metadata compares equal. NULL until computed.
ALTER TABLE blob ADD COLUMN pixel_hash text;
