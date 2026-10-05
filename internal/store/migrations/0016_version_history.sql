-- Restoring a version is a pointer flip: current_version moves back to an
-- older version and nothing is copied. The live version is then no longer
-- the newest, and the newest may later be deleted, so the next publish
-- numbers itself from last_version, the highest number the artifact has ever
-- used. A number is never reused: a pinned /v/n/ link or a comment made on
-- version n can never come to point at different content.
--
-- restored_at records when a version was last made live again by a restore,
-- NULL when it never was, so the history shows that it happened.
-- dialect:all
ALTER TABLE artifacts ADD COLUMN last_version INTEGER NOT NULL DEFAULT 0;
UPDATE artifacts SET last_version = current_version;
ALTER TABLE versions ADD COLUMN restored_at TIMESTAMP;
