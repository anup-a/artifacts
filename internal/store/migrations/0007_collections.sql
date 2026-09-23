-- A collection is every live artifact of a tenant that shares one project
-- label. The label stays the join key, so publishing with --project files a
-- page into its collection with no extra step and no backfill. This table only
-- holds what a label cannot: a description, a course order and a public share
-- slug. A row exists once one of those is saved; a label without a row is a
-- collection with empty metadata.
-- positions is a JSON array of artifact ids ('' = order by publish time).
-- share_slug NULL means not shared; both dialects allow many NULLs in a
-- unique index.
-- dialect:all
CREATE TABLE collections (
 tenant_id TEXT NOT NULL REFERENCES tenants(id),
 name TEXT NOT NULL,
 description TEXT NOT NULL DEFAULT '',
 positions TEXT NOT NULL DEFAULT '',
 share_slug TEXT,
 updated_at TIMESTAMP NOT NULL,
 PRIMARY KEY (tenant_id, name)
);
CREATE UNIQUE INDEX collections_share_slug ON collections(share_slug);
