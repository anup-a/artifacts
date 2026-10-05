-- Who may open a page besides its owner. access_mode '' keeps today's rules:
-- public pages are open, private ones open for the workspace and, when a share
-- code is set, for anyone holding it. 'password' asks visitors for a password
-- (access_password holds its argon2id hash, never the password) and
-- 'restricted' lets in only visitors who prove by email that they are on
-- access_allow, a JSON array of addresses and "@domain" entries. Both imply a
-- private page with no share code, so existing rows keep exactly the access
-- they had. access_version signs every visitor grant: bumping it on a mode or
-- password change signs every visitor out of that page at once.
--
-- access_codes are the one-time codes and links mailed to a restricted page's
-- visitors. Only hashes are stored; a row is good for one page, one address
-- and one access_version.
--
-- comments.author_verified marks a comment made through a restricted page's
-- email gate, whose author name is then the address the reader proved. It is
-- set by the server, never by what a reader typed.
-- dialect:sqlite
ALTER TABLE comments ADD COLUMN author_verified INTEGER NOT NULL DEFAULT 0;
-- dialect:postgres
ALTER TABLE comments ADD COLUMN author_verified BOOLEAN NOT NULL DEFAULT FALSE;
-- dialect:all
ALTER TABLE artifacts ADD COLUMN access_mode TEXT NOT NULL DEFAULT '';
ALTER TABLE artifacts ADD COLUMN access_password TEXT NOT NULL DEFAULT '';
ALTER TABLE artifacts ADD COLUMN access_allow TEXT NOT NULL DEFAULT '';
ALTER TABLE artifacts ADD COLUMN access_version INTEGER NOT NULL DEFAULT 0;
CREATE TABLE access_codes (
 token_sha256 TEXT PRIMARY KEY,
 artifact_id TEXT NOT NULL REFERENCES artifacts(id),
 email TEXT NOT NULL,
 code_sha256 TEXT NOT NULL,
 access_version INTEGER NOT NULL,
 attempts INTEGER NOT NULL DEFAULT 0,
 created_at TIMESTAMP NOT NULL,
 expires_at TIMESTAMP NOT NULL,
 consumed_at TIMESTAMP
);
CREATE INDEX access_codes_artifact_email ON access_codes(artifact_id, email);
