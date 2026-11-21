-- Import and media servers: what happens once a download finishes.
--
-- clients.path_map says how a download client's folders look from Marquee's
-- side ("/downloads" inside a container is "D:\dl" on the host). media_servers
-- are the Jellyfin, Emby and Plex servers told about new files. imports is the
-- importer's own log, one row per try, shown on the Downloads screen.
-- history is what happened to each title (grabbed, imported, upgraded, ...). It
-- lives in this database, not the events one, so a title's page can read it
-- with a plain join.

ALTER TABLE clients ADD COLUMN path_map TEXT NOT NULL DEFAULT '[]';   -- JSON [{"from": "...", "to": "..."}]

CREATE TABLE media_servers (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    name         TEXT    NOT NULL,
    kind         TEXT    NOT NULL CHECK (kind IN ('jellyfin', 'emby', 'plex')),
    url          TEXT    NOT NULL,
    token_sealed TEXT    NOT NULL DEFAULT '',                 -- the API key or Plex token, sealed with the master key
    enabled      INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    path_map     TEXT    NOT NULL DEFAULT '[]' CHECK (json_valid(path_map)),  -- Marquee's folders as the server sees them
    last_ok_at   TEXT,
    last_error   TEXT    NOT NULL DEFAULT '',
    created_at   TEXT    NOT NULL
);
CREATE UNIQUE INDEX media_servers_name ON media_servers (name COLLATE NOCASE);

CREATE TABLE history (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    at          TEXT    NOT NULL,
    -- No foreign keys: a title's history outlives the title, and the copies and
    -- downloads belong to other modules.
    title_id    INTEGER NOT NULL,
    copy_id     INTEGER,
    download_id INTEGER,
    event       TEXT    NOT NULL CHECK (event IN
                ('grabbed', 'imported', 'upgraded', 'failed', 'deleted', 'renamed', 'requested', 'approved', 'declined')),
    detail      TEXT    NOT NULL DEFAULT '{}' CHECK (json_valid(detail))
);
CREATE INDEX history_title ON history (title_id, at);
CREATE INDEX history_at    ON history (at);

CREATE TABLE imports (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    download_id    INTEGER NOT NULL,
    title_id       INTEGER NOT NULL DEFAULT 0,
    copy_id        INTEGER,
    release_title  TEXT    NOT NULL DEFAULT '',
    manual         INTEGER NOT NULL DEFAULT 0 CHECK (manual IN (0, 1)),
    -- A failure that is not the release's fault (a missing folder, a full disk)
    -- is worth one more try later; a bad release is blocklisted instead.
    retryable      INTEGER NOT NULL DEFAULT 0 CHECK (retryable IN (0, 1)),
    started_at     TEXT    NOT NULL,
    finished_at    TEXT,
    outcome        TEXT    NOT NULL DEFAULT 'running' CHECK (outcome IN ('running', 'imported', 'skipped', 'failed')),
    files_imported INTEGER NOT NULL DEFAULT 0,
    files_skipped  INTEGER NOT NULL DEFAULT 0,
    reason         TEXT    NOT NULL DEFAULT '',
    detail         TEXT    NOT NULL DEFAULT '{}' CHECK (json_valid(detail))
);
CREATE INDEX imports_download ON imports (download_id);
CREATE INDEX imports_started  ON imports (started_at);
