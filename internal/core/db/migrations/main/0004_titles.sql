-- Titles: the catalog of movies, shows and artists, with the state Marquee
-- keeps about them (monitored, root folder) and the metadata copied from the
-- providers. Image columns hold artwork-proxy refs such as "tmdb/w500/abc.jpg",
-- never full third-party URLs.

CREATE TABLE titles (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    kind          TEXT    NOT NULL CHECK (kind IN ('movie', 'show', 'artist')),
    tmdb_id       INTEGER,
    tvmaze_id     INTEGER,
    imdb_id       TEXT,
    mbid          TEXT,
    name          TEXT    NOT NULL,
    sort_name     TEXT    NOT NULL,
    alt_names     TEXT    NOT NULL DEFAULT '[]' CHECK (json_valid(alt_names)),
    year          INTEGER NOT NULL DEFAULT 0,
    overview      TEXT    NOT NULL DEFAULT '',
    tagline       TEXT    NOT NULL DEFAULT '',
    certification TEXT    NOT NULL DEFAULT '',
    trailer_key   TEXT    NOT NULL DEFAULT '',   -- YouTube video key
    runtime_min   INTEGER NOT NULL DEFAULT 0,
    genres        TEXT    NOT NULL DEFAULT '[]' CHECK (json_valid(genres)),
    poster_ref    TEXT    NOT NULL DEFAULT '',
    backdrop_ref  TEXT    NOT NULL DEFAULT '',
    status        TEXT    NOT NULL DEFAULT '',
    monitored     INTEGER NOT NULL DEFAULT 1 CHECK (monitored IN (0, 1)),
    root_folder   TEXT    NOT NULL DEFAULT '',
    added_at      TEXT    NOT NULL,
    updated_at    TEXT    NOT NULL,
    refreshed_at  TEXT    NOT NULL
);

-- One catalog row per provider id. TMDB numbers movies and shows separately,
-- so its id is unique per kind.
CREATE UNIQUE INDEX titles_tmdb   ON titles (kind, tmdb_id) WHERE tmdb_id IS NOT NULL;
CREATE UNIQUE INDEX titles_tvmaze ON titles (tvmaze_id)     WHERE tvmaze_id IS NOT NULL;
CREATE UNIQUE INDEX titles_imdb   ON titles (kind, imdb_id) WHERE imdb_id IS NOT NULL;
CREATE UNIQUE INDEX titles_mbid   ON titles (mbid)          WHERE mbid IS NOT NULL;
CREATE INDEX titles_kind_sort ON titles (kind, sort_name);
CREATE INDEX titles_added     ON titles (added_at);

CREATE TABLE seasons (
    title_id  INTEGER NOT NULL REFERENCES titles (id) ON DELETE CASCADE,
    number    INTEGER NOT NULL,
    monitored INTEGER NOT NULL DEFAULT 1 CHECK (monitored IN (0, 1)),
    PRIMARY KEY (title_id, number)
);

CREATE TABLE episodes (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    title_id    INTEGER NOT NULL REFERENCES titles (id) ON DELETE CASCADE,
    season      INTEGER NOT NULL,
    number      INTEGER NOT NULL,
    name        TEXT    NOT NULL DEFAULT '',
    air_date    TEXT    NOT NULL DEFAULT '',      -- YYYY-MM-DD, empty if unknown
    overview    TEXT    NOT NULL DEFAULT '',
    runtime_min INTEGER NOT NULL DEFAULT 0,
    monitored   INTEGER NOT NULL DEFAULT 1 CHECK (monitored IN (0, 1)),
    UNIQUE (title_id, season, number)
);
CREATE INDEX episodes_air ON episodes (air_date);

CREATE TABLE albums (
    id        INTEGER PRIMARY KEY AUTOINCREMENT,
    title_id  INTEGER NOT NULL REFERENCES titles (id) ON DELETE CASCADE,
    mbid      TEXT    NOT NULL UNIQUE,            -- MusicBrainz release group
    name      TEXT    NOT NULL,
    year      INTEGER NOT NULL DEFAULT 0,
    type      TEXT    NOT NULL DEFAULT 'album',   -- album | ep
    monitored INTEGER NOT NULL DEFAULT 1 CHECK (monitored IN (0, 1)),
    cover_ref TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX albums_title ON albums (title_id);

CREATE TABLE tracks (
    id        INTEGER PRIMARY KEY AUTOINCREMENT,
    album_id  INTEGER NOT NULL REFERENCES albums (id) ON DELETE CASCADE,
    disc      INTEGER NOT NULL DEFAULT 1,
    position  INTEGER NOT NULL,
    name      TEXT    NOT NULL,
    length_ms INTEGER NOT NULL DEFAULT 0,
    mbid      TEXT    NOT NULL DEFAULT '',        -- MusicBrainz recording
    UNIQUE (album_id, disc, position)
);

CREATE TABLE people (
    title_id  INTEGER NOT NULL REFERENCES titles (id) ON DELETE CASCADE,
    name      TEXT    NOT NULL,
    role      TEXT    NOT NULL,                   -- actor | director | writer | creator
    job       TEXT    NOT NULL DEFAULT '',        -- the character, or the crew job
    ord       INTEGER NOT NULL,
    image_ref TEXT    NOT NULL DEFAULT '',
    PRIMARY KEY (title_id, ord)
);
