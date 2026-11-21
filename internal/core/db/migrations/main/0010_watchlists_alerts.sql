-- Watchlists, alerts, and the catalog columns the calendar and the artist page
-- need.
--
-- Catalog: a movie's release date (for the calendar), an artist's life span,
-- and a flag on each album that says whether it is a studio album (MusicBrainz
-- primary type Album with no secondary types). Only studio albums are
-- monitored by default.

ALTER TABLE titles ADD COLUMN release_date TEXT NOT NULL DEFAULT '';   -- YYYY-MM-DD (or YYYY), empty if unknown
ALTER TABLE titles ADD COLUMN life_begin   TEXT NOT NULL DEFAULT '';   -- an artist's life-span begin, as MusicBrainz writes it
ALTER TABLE titles ADD COLUMN life_end     TEXT NOT NULL DEFAULT '';   -- and end; empty while it goes on
CREATE INDEX titles_release ON titles (release_date) WHERE release_date <> '';

ALTER TABLE albums ADD COLUMN studio INTEGER NOT NULL DEFAULT 1 CHECK (studio IN (0, 1));
UPDATE albums SET studio = 0 WHERE type <> 'album';

-- Plex watchlists. There is one linked Plex account: the owner's. The token
-- is sealed with the master key.
CREATE TABLE plex_account (
    id              INTEGER PRIMARY KEY CHECK (id = 1),
    user_id         INTEGER REFERENCES users (id) ON DELETE SET NULL,   -- the Marquee person whose asks the owner's own watchlist makes
    plex_user_id    TEXT    NOT NULL,
    plex_username   TEXT    NOT NULL DEFAULT '',
    token_sealed    TEXT    NOT NULL,
    linked_at       TEXT    NOT NULL,
    last_checked_at TEXT,
    last_error      TEXT    NOT NULL DEFAULT '',
    last_result     TEXT    NOT NULL DEFAULT '{}' CHECK (json_valid(last_result))
);

-- The owner's Plex friends as of the last check, so the owner can link each to
-- a Marquee person.
CREATE TABLE plex_friends (
    plex_user_id TEXT PRIMARY KEY,
    username     TEXT NOT NULL DEFAULT '',
    display_name TEXT NOT NULL DEFAULT '',
    seen_at      TEXT NOT NULL
);

-- A friend's items become asks as the Marquee person they are linked to.
CREATE TABLE plex_links (
    plex_user_id  TEXT    PRIMARY KEY,
    user_id       INTEGER NOT NULL UNIQUE REFERENCES users (id) ON DELETE CASCADE,
    plex_username TEXT    NOT NULL DEFAULT '',
    linked_at     TEXT    NOT NULL
);

-- Every watchlist item Marquee has looked at. An item that became an ask, or
-- that cannot become one (skipped_reason names why), is not looked at again;
-- the reasons that can pass (a used-up weekly limit, a missing TMDB key, a
-- friend who is not linked yet) are tried again at the next check.
CREATE TABLE watchlist_items (
    source         TEXT    NOT NULL,                          -- plex
    plex_user_id   TEXT    NOT NULL,
    item_key       TEXT    NOT NULL,                          -- the item's id at the source
    title          TEXT    NOT NULL DEFAULT '',
    year           INTEGER NOT NULL DEFAULT 0,
    type           TEXT    NOT NULL DEFAULT '',               -- movie | show
    guids          TEXT    NOT NULL DEFAULT '[]' CHECK (json_valid(guids)),
    first_seen_at  TEXT    NOT NULL,
    -- No foreign key: the requests module owns that table, and an item that was
    -- asked for stays dealt with even if the ask is deleted later.
    request_id     INTEGER,
    skipped_reason TEXT    NOT NULL DEFAULT '',
    PRIMARY KEY (source, plex_user_id, item_key)
);
CREATE INDEX watchlist_items_request ON watchlist_items (request_id) WHERE request_id IS NOT NULL;

-- Alert channels. Secret fields inside config_sealed are sealed with the master
-- key; the other fields are plain, so a list can show where an alert goes.
CREATE TABLE alert_channels (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    name          TEXT    NOT NULL,
    kind          TEXT    NOT NULL CHECK (kind IN ('discord', 'ntfy', 'gotify', 'telegram', 'pushover', 'email', 'webhook')),
    config_sealed TEXT    NOT NULL DEFAULT '{}' CHECK (json_valid(config_sealed)),
    events        TEXT    NOT NULL DEFAULT '[]' CHECK (json_valid(events)),
    enabled       INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    last_ok_at    TEXT,
    last_error    TEXT    NOT NULL DEFAULT '',
    created_at    TEXT    NOT NULL
);
CREATE UNIQUE INDEX alert_channels_name ON alert_channels (name COLLATE NOCASE);
