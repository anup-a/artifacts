-- Links: work an agent published somewhere else (Vercel, Netlify, GitHub
-- Pages, here.now ...) and registered here so the workspace has one list of
-- everything its agents made. A link is only a pointer: no files, no
-- versions, no slug, never served from a page host. One row per URL in a
-- workspace, so registering the same deploy again refreshes it.
-- host is the URL's host name, kept apart for the dashboard's badge; agent,
-- model and key_name say who registered it, as on versions.
-- dialect:all
CREATE TABLE links (
 id TEXT PRIMARY KEY,
 tenant_id TEXT NOT NULL REFERENCES tenants(id),
 url TEXT NOT NULL,
 host TEXT NOT NULL,
 title TEXT NOT NULL DEFAULT '',
 project TEXT,
 agent TEXT NOT NULL DEFAULT '',
 model TEXT NOT NULL DEFAULT '',
 key_name TEXT NOT NULL DEFAULT '',
 created_at TIMESTAMP NOT NULL,
 updated_at TIMESTAMP NOT NULL,
 UNIQUE(tenant_id, url)
);
CREATE INDEX links_tenant_updated ON links(tenant_id, updated_at);
