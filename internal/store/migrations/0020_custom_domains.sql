-- Custom domains. A workspace connects a hostname it owns (docs.example.com)
-- and mounts its pages on it: the root of the domain, or a path under it.
--
-- token is the value the owner publishes in a TXT record at
-- _byagent.<hostname>; verified_at is set once it has been seen, and nothing
-- is served on the hostname before then. provider and provider_ref name the
-- edge that terminates TLS for it (Cloudflare for SaaS, Fly, or an operator
-- by hand) and that edge's own id for the hostname. status is pending until
-- the hostname is verified and its certificate is live, then active; error
-- when the edge gave up on it. message is the last check's explanation.
-- dialect:all
CREATE TABLE domains (
 hostname TEXT PRIMARY KEY,
 tenant_id TEXT NOT NULL REFERENCES tenants(id),
 token TEXT NOT NULL,
 status TEXT NOT NULL DEFAULT 'pending',
 ssl_status TEXT NOT NULL DEFAULT '',
 provider TEXT NOT NULL DEFAULT '',
 provider_ref TEXT NOT NULL DEFAULT '',
 message TEXT NOT NULL DEFAULT '',
 verified_at TIMESTAMP,
 checked_at TIMESTAMP,
 created_at TIMESTAMP NOT NULL
);
CREATE INDEX domains_tenant ON domains(tenant_id);
-- A mount serves one page at a path on a domain. path is '' for the root,
-- else segments without leading or trailing slashes ('docs', 'docs/api').
-- is_primary marks the one mount a page's byagent.dev address redirects to.
CREATE TABLE domain_mounts (
 hostname TEXT NOT NULL REFERENCES domains(hostname),
 path TEXT NOT NULL,
 artifact_id TEXT NOT NULL REFERENCES artifacts(id),
 is_primary INTEGER NOT NULL DEFAULT 0,
 created_at TIMESTAMP NOT NULL,
 PRIMARY KEY (hostname, path)
);
CREATE INDEX domain_mounts_artifact ON domain_mounts(artifact_id);
