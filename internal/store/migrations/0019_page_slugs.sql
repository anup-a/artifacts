-- Every page gets a host of its own, {slug}.{CONTENT_DOMAIN}, so that one
-- page's script cannot read another page or act with its visitors' unlocks.
-- The id cannot be that label: ids are case-sensitive base62 and DNS names
-- are not. slug is lower-case base32, random and unrelated to the id, and
-- unique (NULLs aside, which both SQLite and Postgres let repeat).
--
-- The column is added empty here; runMigrations then gives every existing
-- row, deleted ones included (their hosts answer 410), a slug in the same
-- transaction, so no row is ever left without one once 0019 is recorded.
-- dialect:all
ALTER TABLE artifacts ADD COLUMN slug TEXT;
CREATE UNIQUE INDEX artifacts_slug ON artifacts(slug);
