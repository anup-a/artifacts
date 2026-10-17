-- Page state: a small JSON key-value store each page's own script reads and
-- writes through window.byagent.state. It belongs to the artifact, not to a
-- version, and a write never makes a version. state_revision counts writes,
-- so a reader can ask "anything new since n?" without fetching every value.
-- state_enabled is the owner's switch (publish --no-state); on by default.
-- versions.summary is the publisher's short note on what changed.
-- dialect:all
CREATE TABLE page_state (
 artifact_id TEXT NOT NULL REFERENCES artifacts(id),
 name TEXT NOT NULL,
 value TEXT NOT NULL,
 updated_at TIMESTAMP NOT NULL,
 PRIMARY KEY(artifact_id, name)
);
ALTER TABLE artifacts ADD COLUMN state_revision BIGINT NOT NULL DEFAULT 0;
ALTER TABLE versions ADD COLUMN summary TEXT NOT NULL DEFAULT '';

-- dialect:sqlite
ALTER TABLE artifacts ADD COLUMN state_enabled INTEGER NOT NULL DEFAULT 1;

-- dialect:postgres
ALTER TABLE artifacts ADD COLUMN state_enabled BOOLEAN NOT NULL DEFAULT TRUE;
