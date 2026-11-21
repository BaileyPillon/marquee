-- Metadata cache: the raw answer of each provider call, so a lookup repeated
-- inside its TTL never leaves the machine, and an outage can fall back to the
-- last good copy (stale-while-error).

CREATE TABLE metadata_cache (
    provider   TEXT NOT NULL,
    key        TEXT NOT NULL,
    body       BLOB NOT NULL,
    fetched_at TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    PRIMARY KEY (provider, key)
);
CREATE INDEX metadata_cache_expires ON metadata_cache (expires_at);
