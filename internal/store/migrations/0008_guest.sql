-- A page published by a guest workspace (POST /api/guest) carries the moment
-- it stops being served. NULL means it never expires: every page published
-- with a real key, and every guest page once its workspace is claimed. The
-- store reads a page past this time as deleted; the hourly GC then marks it
-- deleted for good.
-- dialect:all
ALTER TABLE artifacts ADD COLUMN expires_at TIMESTAMP;
