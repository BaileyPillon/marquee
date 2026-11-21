-- Sources and release picking: the Torznab/Newznab feeds the owner adds, what
-- their feeds showed lately, the quality profiles, the blocklist, and a short
-- record of every search.

CREATE TABLE indexers (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    name           TEXT    NOT NULL,
    kind           TEXT    NOT NULL CHECK (kind IN ('torznab', 'newznab')),
    url            TEXT    NOT NULL,                       -- no query string: the key is kept apart
    api_key_sealed TEXT    NOT NULL DEFAULT '',            -- sealed with the master key
    categories     TEXT    NOT NULL DEFAULT '[]' CHECK (json_valid(categories)),
    enabled        INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    priority       INTEGER NOT NULL DEFAULT 25,            -- lower is asked first and wins ties
    caps           TEXT    CHECK (caps IS NULL OR json_valid(caps)),
    last_ok_at     TEXT,
    last_error     TEXT    NOT NULL DEFAULT '',
    last_error_at  TEXT,
    created_at     TEXT    NOT NULL
);
CREATE UNIQUE INDEX indexers_url ON indexers (url);

-- What the feeds showed in the last 7 days. Links can carry the source's key
-- (and a magnet link can carry a tracker passkey), so both are sealed.
CREATE TABLE releases_seen (
    indexer_id   INTEGER NOT NULL REFERENCES indexers (id) ON DELETE CASCADE,
    guid         TEXT    NOT NULL,
    title        TEXT    NOT NULL,
    size         INTEGER NOT NULL DEFAULT 0,
    published_at TEXT,
    protocol     TEXT    NOT NULL CHECK (protocol IN ('torrent', 'usenet')),
    link_sealed  TEXT,
    magnet       TEXT    NOT NULL DEFAULT '',              -- sealed too, despite the name
    infohash     TEXT    NOT NULL DEFAULT '',
    seeders      INTEGER NOT NULL DEFAULT -1,              -- -1: the feed did not say
    raw_attrs    TEXT    NOT NULL DEFAULT '{}' CHECK (json_valid(raw_attrs)),
    seen_at      TEXT    NOT NULL,
    PRIMARY KEY (indexer_id, guid)
);
CREATE INDEX releases_seen_at ON releases_seen (seen_at);

-- One profile per kind and quality. The names repeat across kinds ("HD" for
-- movies and for shows), so the name is unique per kind.
CREATE TABLE profiles (
    id                   INTEGER PRIMARY KEY AUTOINCREMENT,
    name                 TEXT    NOT NULL,
    kind                 TEXT    NOT NULL CHECK (kind IN ('movie', 'show', 'artist')),
    quality              TEXT    NOT NULL CHECK (quality IN ('hd', 'uhd')),
    allowed_res          TEXT    NOT NULL DEFAULT '[]' CHECK (json_valid(allowed_res)),
    preferred_res        INTEGER NOT NULL DEFAULT 0,       -- 0: none (music)
    allowed_sources      TEXT    NOT NULL DEFAULT '[]' CHECK (json_valid(allowed_sources)),  -- for music: formats, best first
    min_size_mb          INTEGER,
    max_size_mb          INTEGER,
    max_size_per_hour_mb INTEGER,
    prefer_hdr           INTEGER NOT NULL DEFAULT 0 CHECK (prefer_hdr IN (0, 1)),
    preferred_words      TEXT    NOT NULL DEFAULT '[]' CHECK (json_valid(preferred_words)),
    rejected_words       TEXT    NOT NULL DEFAULT '[]' CHECK (json_valid(rejected_words)),
    min_seeders          INTEGER NOT NULL DEFAULT 1,
    upgrade_until        TEXT,                             -- a quality name such as "Bluray-1080p"; NULL: never upgrade
    created_at           TEXT    NOT NULL,
    UNIQUE (kind, name),
    UNIQUE (kind, quality)
);

INSERT INTO profiles (name, kind, quality, allowed_res, preferred_res, allowed_sources,
                      max_size_mb, max_size_per_hour_mb, prefer_hdr, min_seeders, created_at)
VALUES
    ('HD', 'movie', 'hd', '[1080,720]', 1080, '["Remux","BluRay","WEB-DL","WEBRip","HDTV"]',
        20480, NULL, 0, 1, strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    ('4K', 'movie', 'uhd', '[2160]', 2160, '["Remux","BluRay","WEB-DL","WEBRip","HDTV"]',
        61440, NULL, 1, 1, strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    ('HD', 'show', 'hd', '[1080,720]', 1080, '["Remux","BluRay","WEB-DL","WEBRip","HDTV"]',
        NULL, 4096, 0, 1, strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    ('4K', 'show', 'uhd', '[2160]', 2160, '["Remux","BluRay","WEB-DL","WEBRip","HDTV"]',
        NULL, NULL, 1, 1, strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    ('Lossless', 'artist', 'hd', '[]', 0, '["FLAC","MP3-320"]',
        NULL, NULL, 0, 1, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));

-- Releases the owner (or a failed import) turned down. A blocked release is
-- never picked again.
CREATE TABLE blocklist (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    title_id      INTEGER NOT NULL REFERENCES titles (id) ON DELETE CASCADE,
    guid          TEXT    NOT NULL DEFAULT '',
    infohash      TEXT    NOT NULL DEFAULT '',
    release_title TEXT    NOT NULL DEFAULT '',
    reason        TEXT    NOT NULL DEFAULT '',
    created_at    TEXT    NOT NULL
);
CREATE INDEX blocklist_title    ON blocklist (title_id);
CREATE INDEX blocklist_guid     ON blocklist (guid)     WHERE guid <> '';
CREATE INDEX blocklist_infohash ON blocklist (infohash) WHERE infohash <> '';

-- One row per search of a copy (by the 6-hourly job, the feed matcher, a
-- request just approved, or a person pressing a button).
CREATE TABLE search_history (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    copy_id         INTEGER NOT NULL,                      -- no foreign key: the requests module owns copies
    at              TEXT    NOT NULL,
    indexers_ok     INTEGER NOT NULL DEFAULT 0,
    indexers_failed INTEGER NOT NULL DEFAULT 0,
    results         INTEGER NOT NULL DEFAULT 0,
    best_guid       TEXT    NOT NULL DEFAULT '',
    grabbed         INTEGER NOT NULL DEFAULT 0,
    grabbed_for     TEXT    NOT NULL DEFAULT '[]' CHECK (json_valid(grabbed_for)),  -- "movie", "s01e04", "s01": what was sent for download
    note            TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX search_history_copy ON search_history (copy_id, at);
