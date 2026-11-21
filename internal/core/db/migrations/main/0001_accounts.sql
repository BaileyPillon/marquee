-- Accounts: users, sessions, invites, API keys.

CREATE TABLE users (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    name          TEXT    NOT NULL UNIQUE COLLATE NOCASE,
    role          TEXT    NOT NULL CHECK (role IN ('owner', 'admin', 'member')),
    password_hash TEXT    NOT NULL,
    created_at    TEXT    NOT NULL,
    disabled      INTEGER NOT NULL DEFAULT 0 CHECK (disabled IN (0, 1))
);

-- There is exactly one owner. This is the backstop behind the "first visitor
-- becomes the owner" transaction.
CREATE UNIQUE INDEX users_one_owner ON users (role) WHERE role = 'owner';

CREATE TABLE sessions (
    id           TEXT    PRIMARY KEY,   -- sha256 hex of the cookie token
    user_id      INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at   TEXT    NOT NULL,
    expires_at   TEXT    NOT NULL,
    last_seen_at TEXT    NOT NULL,
    user_agent   TEXT    NOT NULL DEFAULT '',
    ip           TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX sessions_user    ON sessions (user_id);
CREATE INDEX sessions_expires ON sessions (expires_at);

CREATE TABLE invites (
    token_hash TEXT    PRIMARY KEY,     -- sha256 hex of the one-time token
    created_by INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role       TEXT    NOT NULL CHECK (role IN ('admin', 'member')),
    created_at TEXT    NOT NULL,
    expires_at TEXT    NOT NULL,
    used_by    INTEGER REFERENCES users (id) ON DELETE SET NULL,
    used_at    TEXT
);

CREATE TABLE api_keys (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id      INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name         TEXT    NOT NULL,
    prefix       TEXT    NOT NULL UNIQUE,
    hash         TEXT    NOT NULL,      -- sha256 hex of the secret part
    scopes       TEXT    NOT NULL DEFAULT '[]' CHECK (json_valid(scopes)),
    created_at   TEXT    NOT NULL,
    last_used_at TEXT,
    revoked_at   TEXT
);
CREATE INDEX api_keys_user ON api_keys (user_id);
