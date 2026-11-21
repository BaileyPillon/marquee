-- Library: the folders Marquee watches and every media file found in them.
-- Files are rows only; nothing here ever touches the files on disk.

CREATE TABLE library_folders (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    kind       TEXT    NOT NULL CHECK (kind IN ('movie', 'show', 'music')),
    path       TEXT    NOT NULL UNIQUE,   -- absolute, cleaned
    label      TEXT    NOT NULL DEFAULT '',
    created_at TEXT    NOT NULL
);

CREATE TABLE files (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    folder_id     INTEGER NOT NULL REFERENCES library_folders (id) ON DELETE CASCADE,
    rel_path      TEXT    NOT NULL,                 -- under the folder, always with "/"
    size          INTEGER NOT NULL DEFAULT 0,       -- bytes
    mtime         INTEGER NOT NULL DEFAULT 0,       -- unix nanoseconds
    kind          TEXT    NOT NULL CHECK (kind IN ('movie', 'show', 'music')),  -- the folder's kind
    parsed        TEXT    NOT NULL DEFAULT '{}' CHECK (json_valid(parsed)),
    quality       TEXT    NOT NULL DEFAULT '',
    -- No foreign keys on the three ids below: the titles module owns those
    -- tables and decides what a title, an episode and a track are.
    title_id      INTEGER,
    episode_ids   TEXT    CHECK (episode_ids IS NULL OR json_valid(episode_ids)),
    track_id      INTEGER,
    match_state   TEXT    NOT NULL DEFAULT 'unmatched' CHECK (match_state IN ('matched', 'unmatched', 'ignored')),
    added_at      TEXT    NOT NULL,
    missing_since TEXT,                             -- set when the file vanished from disk
    UNIQUE (folder_id, rel_path)
);
CREATE INDEX files_state   ON files (match_state);
CREATE INDEX files_title   ON files (title_id) WHERE title_id IS NOT NULL;
CREATE INDEX files_missing ON files (missing_since) WHERE missing_since IS NOT NULL;
