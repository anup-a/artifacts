-- A private artifact may also carry a share code, which lets anyone holding it
-- read the artifact without signing in. Empty means no code, so existing rows
-- keep exactly the access they had. This is a separate column rather than a
-- third visibility value on purpose: the visibility CHECK constraint cannot be
-- widened in one statement that both SQLite and Postgres accept, and the code
-- is orthogonal anyway — rotating or clearing it never changes who owns what.
ALTER TABLE artifacts ADD COLUMN share_code TEXT NOT NULL DEFAULT '';
