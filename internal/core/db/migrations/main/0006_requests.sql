-- Requests: what people ask for, the owner's yes or no, and the wanted copies
-- (an HD copy and a 4K copy of one title are two rows with their own folders).

-- A copy is one wanted version of a title. It exists once a request is
-- approved, or the owner adds the title directly. folder_id is where the
-- finished files go; it is NULL when the folder was removed from the library.
CREATE TABLE copies (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    title_id   INTEGER NOT NULL REFERENCES titles (id) ON DELETE CASCADE,
    quality    TEXT    NOT NULL CHECK (quality IN ('hd', 'uhd')),
    folder_id  INTEGER REFERENCES library_folders (id) ON DELETE SET NULL,
    monitored  INTEGER NOT NULL DEFAULT 1 CHECK (monitored IN (0, 1)),
    created_at TEXT    NOT NULL,
    UNIQUE (title_id, quality)
);
CREATE INDEX copies_monitored ON copies (monitored) WHERE monitored = 1;

-- scope says how much of a title was asked for: {} for a movie,
-- {"seasons":[1,2]} or {"all":true} for a show, {"albums":[ids]} or
-- {"all":true} for an artist.
CREATE TABLE requests (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id        INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    title_id       INTEGER NOT NULL REFERENCES titles (id) ON DELETE CASCADE,
    kind           TEXT    NOT NULL CHECK (kind IN ('movie', 'show', 'artist')),
    quality        TEXT    NOT NULL CHECK (quality IN ('hd', 'uhd')),
    scope          TEXT    NOT NULL DEFAULT '{}' CHECK (json_valid(scope)),
    note           TEXT    NOT NULL DEFAULT '',
    state          TEXT    NOT NULL DEFAULT 'pending'
                   CHECK (state IN ('pending', 'approved', 'declined', 'available', 'partial', 'failed', 'cancelled')),
    decided_by     INTEGER REFERENCES users (id) ON DELETE SET NULL,  -- NULL when approved by the rules
    decided_at     TEXT,
    decline_reason TEXT    NOT NULL DEFAULT '',
    source         TEXT    NOT NULL DEFAULT 'manual' CHECK (source IN ('manual', 'watchlist', 'mcp', 'list')),
    source_ref     TEXT    NOT NULL DEFAULT '',
    created_at     TEXT    NOT NULL,
    updated_at     TEXT    NOT NULL
);
CREATE INDEX requests_user  ON requests (user_id, created_at);
CREATE INDEX requests_state ON requests (state, created_at);
CREATE INDEX requests_title ON requests (title_id, quality);

-- Backstop behind the "already asked for" check: one open request per title
-- and quality, even if two asks arrive at the same moment.
CREATE UNIQUE INDEX requests_open ON requests (title_id, quality)
    WHERE state IN ('pending', 'approved', 'available');

-- Per-person overrides of the defaults in the "requests" settings section.
-- NULL means use the default.
CREATE TABLE user_limits (
    user_id          INTEGER PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    hd_per_week      INTEGER CHECK (hd_per_week IS NULL OR hd_per_week >= 0),
    uhd_per_week     INTEGER CHECK (uhd_per_week IS NULL OR uhd_per_week >= 0),
    auto_approve_hd  INTEGER CHECK (auto_approve_hd IS NULL OR auto_approve_hd IN (0, 1)),
    auto_approve_uhd INTEGER CHECK (auto_approve_uhd IS NULL OR auto_approve_uhd IN (0, 1))
);
