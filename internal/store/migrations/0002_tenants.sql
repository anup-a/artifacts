CREATE TABLE tenants (
 id TEXT PRIMARY KEY,
 kind TEXT NOT NULL CHECK (kind IN ('personal','integrator')),
 name TEXT NOT NULL,
 plan TEXT NOT NULL DEFAULT 'free',
 created_at TIMESTAMP NOT NULL
);
CREATE TABLE users (
 id TEXT PRIMARY KEY, email TEXT NOT NULL UNIQUE, name TEXT NOT NULL DEFAULT '', created_at TIMESTAMP NOT NULL
);
CREATE TABLE identities (
 provider TEXT NOT NULL, subject TEXT NOT NULL, user_id TEXT NOT NULL REFERENCES users(id), created_at TIMESTAMP NOT NULL,
 PRIMARY KEY(provider,subject)
);
CREATE TABLE memberships (
 tenant_id TEXT NOT NULL REFERENCES tenants(id), user_id TEXT NOT NULL REFERENCES users(id),
 role TEXT NOT NULL CHECK (role='owner'), created_at TIMESTAMP NOT NULL,
 PRIMARY KEY(tenant_id,user_id)
);
CREATE TABLE sessions (
 id TEXT PRIMARY KEY, user_id TEXT NOT NULL REFERENCES users(id), tenant_id TEXT NOT NULL REFERENCES tenants(id),
 created_at TIMESTAMP NOT NULL, expires_at TIMESTAMP NOT NULL, last_seen_at TIMESTAMP NOT NULL
);
CREATE TABLE magic_links (
 token_sha256 TEXT PRIMARY KEY, email TEXT NOT NULL, created_at TIMESTAMP NOT NULL,
 expires_at TIMESTAMP NOT NULL, consumed_at TIMESTAMP
);
CREATE TABLE oauth_states (
 state TEXT PRIMARY KEY, created_at TIMESTAMP NOT NULL, expires_at TIMESTAMP NOT NULL
);
CREATE TABLE end_users (
 id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL REFERENCES tenants(id), external_id TEXT NOT NULL,
 first_seen_at TIMESTAMP NOT NULL, last_seen_at TIMESTAMP NOT NULL,
 UNIQUE(tenant_id,external_id)
);
ALTER TABLE keys ADD COLUMN tenant_id TEXT REFERENCES tenants(id);
ALTER TABLE keys ADD COLUMN name TEXT NOT NULL DEFAULT '';
ALTER TABLE keys ADD COLUMN prefix TEXT NOT NULL DEFAULT '';
ALTER TABLE keys ADD COLUMN last_used_at TIMESTAMP;

-- dialect:sqlite
-- key_id is provenance only. Deleting a revoked credential preserves artifacts.
CREATE TABLE artifacts_v2 (
 id TEXT PRIMARY KEY, key_id INTEGER REFERENCES keys(id) ON DELETE SET NULL,
 title TEXT NOT NULL DEFAULT '', current_version INTEGER NOT NULL DEFAULT 0,
 created_at TIMESTAMP NOT NULL, updated_at TIMESTAMP NOT NULL, deleted_at TIMESTAMP,
 tenant_id TEXT REFERENCES tenants(id), end_user_id TEXT REFERENCES end_users(id)
);
INSERT INTO artifacts_v2(id,key_id,title,current_version,created_at,updated_at,deleted_at)
 SELECT id,key_id,title,current_version,created_at,updated_at,deleted_at FROM artifacts;
DROP TABLE artifacts;
ALTER TABLE artifacts_v2 RENAME TO artifacts;

-- dialect:postgres
ALTER TABLE artifacts ADD COLUMN tenant_id TEXT REFERENCES tenants(id);
ALTER TABLE artifacts ADD COLUMN end_user_id TEXT REFERENCES end_users(id);
ALTER TABLE artifacts ALTER COLUMN key_id DROP NOT NULL;
ALTER TABLE artifacts DROP CONSTRAINT artifacts_key_id_fkey;
ALTER TABLE artifacts ADD CONSTRAINT artifacts_key_id_fkey FOREIGN KEY(key_id) REFERENCES keys(id) ON DELETE SET NULL;

-- dialect:all
CREATE INDEX artifacts_tenant_created ON artifacts(tenant_id,created_at);
CREATE INDEX artifacts_tenant_updated ON artifacts(tenant_id,updated_at,id);
CREATE INDEX artifacts_end_user_created ON artifacts(end_user_id,created_at);
CREATE INDEX keys_tenant ON keys(tenant_id);
CREATE TABLE artifact_views (
 artifact_id TEXT NOT NULL REFERENCES artifacts(id), day TEXT NOT NULL, count BIGINT NOT NULL,
 PRIMARY KEY(artifact_id,day)
);
