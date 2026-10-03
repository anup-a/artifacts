-- A share code may have an end date. Past it the store reads the artifact as
-- having no code, so the gate, the one-click link and any unlocked browser
-- all stop working together. NULL means the code lasts until rotated.
-- dialect:all
ALTER TABLE artifacts ADD COLUMN share_code_expires_at TIMESTAMP;
