-- Who published each version: the agent and model the publisher reported
-- (free text, empty when unknown) and the name the publishing key had at the
-- time. With several agents publishing into one project, this is how a
-- reader of the history tells their work apart.
-- dialect:all
ALTER TABLE versions ADD COLUMN agent TEXT NOT NULL DEFAULT '';
ALTER TABLE versions ADD COLUMN model TEXT NOT NULL DEFAULT '';
ALTER TABLE versions ADD COLUMN key_name TEXT NOT NULL DEFAULT '';
