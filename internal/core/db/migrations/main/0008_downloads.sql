-- Downloads: the download clients Marquee talks to (qBittorrent, Deluge,
-- SABnzbd, ...) and one row for every release sent to one of them.
--
-- Passwords and API keys are sealed with the master key (core/secrets); only
-- the sealed text is ever stored.

CREATE TABLE clients (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    name            TEXT    NOT NULL,
    kind            TEXT    NOT NULL CHECK (kind IN ('qbittorrent', 'deluge', 'sabnzbd', 'transmission', 'nzbget')),
    url             TEXT    NOT NULL,
    username        TEXT    NOT NULL DEFAULT '',
    password_sealed TEXT    NOT NULL DEFAULT '',
    api_key_sealed  TEXT    NOT NULL DEFAULT '',
    category        TEXT    NOT NULL DEFAULT 'marquee',   -- the label every Marquee download carries in the client
    save_path       TEXT,                                 -- where the client should save, NULL = the client's own default
    enabled         INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    priority        INTEGER NOT NULL DEFAULT 1,           -- lowest number is used first
    last_ok_at      TEXT,
    last_error      TEXT    NOT NULL DEFAULT '',
    last_error_at   TEXT,
    created_at      TEXT    NOT NULL
);
CREATE UNIQUE INDEX clients_name ON clients (name COLLATE NOCASE);

CREATE TABLE downloads (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    client_id     INTEGER NOT NULL REFERENCES clients (id) ON DELETE CASCADE,
    client_ref    TEXT    NOT NULL,                       -- the client's own id: info-hash, Deluge id, SABnzbd nzo_id
    protocol      TEXT    NOT NULL CHECK (protocol IN ('torrent', 'usenet')),
    -- No foreign keys on title_id and copy_id: the titles and requests modules
    -- own those tables, and a finished download's history outlives them.
    title_id      INTEGER NOT NULL,
    copy_id       INTEGER,
    kind          TEXT    NOT NULL DEFAULT '',            -- movie | show | artist
    quality       TEXT    NOT NULL DEFAULT '',            -- grab.QualityHD | grab.QualityUHD
    episode_ids   TEXT    NOT NULL DEFAULT '[]' CHECK (json_valid(episode_ids)),
    release_title TEXT    NOT NULL DEFAULT '',
    indexer_id    INTEGER,
    guid          TEXT    NOT NULL DEFAULT '',
    size          INTEGER NOT NULL DEFAULT 0,
    state         TEXT    NOT NULL DEFAULT 'queued'
                  CHECK (state IN ('queued', 'downloading', 'paused', 'stalled', 'completed', 'importing', 'imported', 'failed', 'removed')),
    progress      REAL    NOT NULL DEFAULT 0,             -- 0 to 1
    speed_bps     INTEGER NOT NULL DEFAULT 0,
    eta_seconds   INTEGER NOT NULL DEFAULT -1,            -- -1 = unknown
    save_path     TEXT    NOT NULL DEFAULT '',
    content_path  TEXT    NOT NULL DEFAULT '',            -- the file or folder the client produced (as the client sees it)
    error         TEXT    NOT NULL DEFAULT '',
    created_at    TEXT    NOT NULL,
    updated_at    TEXT    NOT NULL,
    completed_at  TEXT,
    UNIQUE (client_id, client_ref)
);
CREATE INDEX downloads_state ON downloads (state);
CREATE INDEX downloads_title ON downloads (title_id);
