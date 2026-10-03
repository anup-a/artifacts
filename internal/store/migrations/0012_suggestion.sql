-- A reader may propose replacement text for the passage a thread quotes: an
-- exact edit the author can apply, instead of prose to interpret. Empty means
-- a plain comment.
-- dialect:all
ALTER TABLE threads ADD COLUMN suggestion TEXT NOT NULL DEFAULT '';
