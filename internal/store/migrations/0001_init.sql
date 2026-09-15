CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY);

CREATE TABLE IF NOT EXISTS keys (
  id INTEGER PRIMARY KEY,
  key_sha256 TEXT NOT NULL UNIQUE,
  email TEXT NOT NULL,
  created_at TIMESTAMP NOT NULL,
  revoked_at TIMESTAMP
);

CREATE TABLE IF NOT EXISTS artifacts (
  id TEXT PRIMARY KEY,
  key_id INTEGER NOT NULL REFERENCES keys(id),
  title TEXT NOT NULL DEFAULT '',
  current_version INTEGER NOT NULL DEFAULT 0,
  created_at TIMESTAMP NOT NULL,
  updated_at TIMESTAMP NOT NULL,
  deleted_at TIMESTAMP
);
CREATE INDEX IF NOT EXISTS artifacts_key_created ON artifacts(key_id, created_at);

CREATE TABLE IF NOT EXISTS versions (
  artifact_id TEXT NOT NULL REFERENCES artifacts(id),
  version INTEGER NOT NULL,
  created_at TIMESTAMP NOT NULL,
  bytes BIGINT NOT NULL,
  file_count INTEGER NOT NULL,
  PRIMARY KEY (artifact_id, version)
);

CREATE TABLE IF NOT EXISTS files (
  artifact_id TEXT NOT NULL,
  version INTEGER NOT NULL,
  path TEXT NOT NULL,
  sha256 TEXT NOT NULL,
  size BIGINT NOT NULL,
  content_type TEXT NOT NULL,
  PRIMARY KEY (artifact_id, version, path)
);

CREATE TABLE IF NOT EXISTS blobs (
  sha256 TEXT PRIMARY KEY,
  size BIGINT NOT NULL,
  refcount INTEGER NOT NULL DEFAULT 0,
  created_at TIMESTAMP NOT NULL
);

CREATE TABLE IF NOT EXISTS threads (
  id TEXT PRIMARY KEY,
  artifact_id TEXT NOT NULL REFERENCES artifacts(id),
  version INTEGER NOT NULL,
  anchor_json TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'open',
  anchor_status TEXT NOT NULL DEFAULT 'unknown',
  created_at TIMESTAMP NOT NULL,
  resolved_at TIMESTAMP
);
CREATE INDEX IF NOT EXISTS threads_artifact ON threads(artifact_id, status, created_at);

CREATE TABLE IF NOT EXISTS comments (
  id TEXT PRIMARY KEY,
  thread_id TEXT NOT NULL REFERENCES threads(id),
  author_name TEXT NOT NULL,
  author_kind TEXT NOT NULL,
  body TEXT NOT NULL,
  created_at TIMESTAMP NOT NULL
);
CREATE INDEX IF NOT EXISTS comments_thread ON comments(thread_id, created_at);
