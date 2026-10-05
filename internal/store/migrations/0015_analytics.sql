-- Page analytics. A day row gains its unique visitors, counted in memory with
-- a salt that changes every UTC day and is never written down, so the number
-- is all that is stored. Hour rows feed the 24 hour chart and are pruned after
-- two days. Facet rows count views per day by referrer host, country, path
-- within the page and device class, at most a few dozen values each plus an
-- "(other)" row, so a hostile visitor cannot grow them without bound.
-- dialect:all
ALTER TABLE artifact_views ADD COLUMN visitors BIGINT NOT NULL DEFAULT 0;
CREATE TABLE artifact_view_hours (
 artifact_id TEXT NOT NULL REFERENCES artifacts(id), hour TEXT NOT NULL,
 views BIGINT NOT NULL, visitors BIGINT NOT NULL,
 PRIMARY KEY(artifact_id,hour)
);
CREATE INDEX artifact_view_hours_hour ON artifact_view_hours(hour);
CREATE TABLE artifact_view_facets (
 artifact_id TEXT NOT NULL REFERENCES artifacts(id), day TEXT NOT NULL,
 kind TEXT NOT NULL, value TEXT NOT NULL, views BIGINT NOT NULL,
 PRIMARY KEY(artifact_id,day,kind,value)
);
