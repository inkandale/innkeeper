-- With autosync on, each sync pushes the library's copy when the card is
-- ahead and the app's copy hasn't changed.
ALTER TABLE binding ADD COLUMN autosync boolean NOT NULL DEFAULT false;
