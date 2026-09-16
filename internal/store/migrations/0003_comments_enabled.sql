-- Per-artifact switch for the viewer comment runtime. Existing artifacts keep
-- comments on, so the column defaults to true in both dialects.
-- dialect:sqlite
ALTER TABLE artifacts ADD COLUMN comments_enabled INTEGER NOT NULL DEFAULT 1;

-- dialect:postgres
ALTER TABLE artifacts ADD COLUMN comments_enabled BOOLEAN NOT NULL DEFAULT TRUE;
