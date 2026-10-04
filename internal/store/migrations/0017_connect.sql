-- A connect code turns a signed-in person's say-so into an API key for one
-- agent, so the key itself never travels through a chat.
--
-- Two ways in. A code made in the dashboard (/app/connect) is approved when
-- it is made and has no device_sha256: the agent redeems the code itself. A
-- code the CLI asks for (byagent login --browser) carries device_sha256, the
-- secret the CLI polls with, and waits for someone signed in to approve it;
-- the short code alone never redeems it.
--
-- Codes expire after ten minutes and work once. key_id is the key a redeemed
-- code became, kept for the audit trail.
-- dialect:all
CREATE TABLE connect_codes (
 code_sha256 TEXT PRIMARY KEY,
 device_sha256 TEXT UNIQUE,
 name TEXT NOT NULL DEFAULT '',
 tenant_id TEXT REFERENCES tenants(id),
 email TEXT NOT NULL DEFAULT '',
 created_at TIMESTAMP NOT NULL,
 expires_at TIMESTAMP NOT NULL,
 approved_at TIMESTAMP,
 consumed_at TIMESTAMP,
 key_id INTEGER REFERENCES keys(id) ON DELETE SET NULL
);
