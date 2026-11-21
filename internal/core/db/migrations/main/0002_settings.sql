-- Settings: one JSON object per section. Secret-looking fields inside the
-- object are sealed with the master key before they get here.

CREATE TABLE settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL CHECK (json_valid(value)),
    updated_at TEXT NOT NULL
);
