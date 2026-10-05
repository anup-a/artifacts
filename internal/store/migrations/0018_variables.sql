-- Account variables: secrets a workspace stores for its pages' proxy routes.
-- sealed is the AES-GCM ciphertext (key id, nonce and ciphertext as text),
-- never the value; allowed_upstreams is a JSON array of host names the value
-- may be sent to.
-- dialect:all
CREATE TABLE variables (
 tenant_id TEXT NOT NULL REFERENCES tenants(id),
 name TEXT NOT NULL,
 sealed TEXT NOT NULL,
 allowed_upstreams TEXT NOT NULL,
 created_at TIMESTAMP NOT NULL,
 updated_at TIMESTAMP NOT NULL,
 PRIMARY KEY(tenant_id,name)
);

-- A version whose .byagent/proxy.json passed the publish checks (published
-- by a workspace key, not a guest, and not for an end user unless opted in).
-- Only such a version runs proxy routes, also after a restore.
-- dialect:sqlite
ALTER TABLE versions ADD COLUMN proxy_routes INTEGER NOT NULL DEFAULT 0;

-- dialect:postgres
ALTER TABLE versions ADD COLUMN proxy_routes BOOLEAN NOT NULL DEFAULT FALSE;
