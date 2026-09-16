-- Gallery metadata. project and tags are labels supplied by the publisher,
-- cover_path names a file inside the current version to use as the cover
-- (NULL = generated), cover_style is the generator name ('' = derive from id).
-- tags holds a JSON array of strings; NULL and '[]' both mean no tags.
-- The same statements work in both dialects.
-- dialect:all
ALTER TABLE artifacts ADD COLUMN project TEXT;
ALTER TABLE artifacts ADD COLUMN tags TEXT;
ALTER TABLE artifacts ADD COLUMN cover_path TEXT;
ALTER TABLE artifacts ADD COLUMN cover_style TEXT NOT NULL DEFAULT '';
CREATE INDEX artifacts_tenant_project ON artifacts(tenant_id,project);
