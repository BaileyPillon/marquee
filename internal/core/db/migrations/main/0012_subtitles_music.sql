-- Subtitles and music search.
--
-- subtitles: one row for every subtitle file Marquee itself wrote next to a
-- video. Marquee never overwrites a subtitle file it did not write, and this
-- table is how it knows which ones it did write. A file row going away (the
-- library folder was removed) takes its subtitle rows with it; the subtitle
-- files on disk stay where they are.
--
-- subtitle_searches: a short log of looks for a subtitle, one row per file,
-- language and try. "result" is found, none (the providers had nothing good
-- enough) or error (a provider did not answer). The scheduled job reads the run
-- of "none" rows after the last "found" to wait 1, then 3, then 7 days before
-- looking again.
--
-- album_searches: which albums of an artist's copy were looked for, and when,
-- so a search that stops after its per-run limit carries on with the albums it
-- has not tried (or tried longest ago) the next time.

CREATE TABLE subtitles (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    file_id     INTEGER NOT NULL REFERENCES files (id) ON DELETE CASCADE,
    language    TEXT    NOT NULL,                       -- "en", "pt-br": lower case, as the settings write it
    hi          INTEGER NOT NULL DEFAULT 0 CHECK (hi IN (0, 1)),   -- hearing impaired
    provider    TEXT    NOT NULL,                       -- opensubtitles | podnapisi
    provider_id TEXT    NOT NULL DEFAULT '',            -- the provider's own id for the subtitle
    path        TEXT    NOT NULL,                       -- the .srt Marquee wrote (full path)
    score       INTEGER NOT NULL DEFAULT 0,
    created_at  TEXT    NOT NULL,
    UNIQUE (file_id, language, hi)
);
CREATE INDEX subtitles_file ON subtitles (file_id);

CREATE TABLE subtitle_searches (
    file_id  INTEGER NOT NULL REFERENCES files (id) ON DELETE CASCADE,
    language TEXT    NOT NULL,
    at       TEXT    NOT NULL,
    result   TEXT    NOT NULL CHECK (result IN ('found', 'none', 'error'))
);
CREATE INDEX subtitle_searches_file ON subtitle_searches (file_id, language, at);

CREATE TABLE album_searches (
    copy_id  INTEGER NOT NULL,                          -- no foreign key: the requests module owns copies
    album_id INTEGER NOT NULL REFERENCES albums (id) ON DELETE CASCADE,
    at       TEXT    NOT NULL,
    grabbed  INTEGER NOT NULL DEFAULT 0 CHECK (grabbed IN (0, 1)),
    PRIMARY KEY (copy_id, album_id)
);
