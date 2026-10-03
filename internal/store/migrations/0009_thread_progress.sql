-- What the publisher (usually an agent) is doing about a thread, shown to
-- readers next to it: '' (nothing yet), 'seen', 'working', or 'declined'.
-- It is separate from status: open and resolved still decide which list a
-- thread is in, and a declined thread is also resolved.
-- dialect:all
ALTER TABLE threads ADD COLUMN progress TEXT NOT NULL DEFAULT '';
