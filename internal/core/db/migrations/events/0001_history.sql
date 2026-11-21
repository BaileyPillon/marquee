-- History and logs live in their own file so they can grow, be pruned, or be
-- deleted without touching the main state.

CREATE TABLE history (
    id      INTEGER PRIMARY KEY AUTOINCREMENT,
    at      TEXT NOT NULL,
    kind    TEXT NOT NULL,
    subject TEXT NOT NULL DEFAULT '',
    detail  TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(detail))
);
CREATE INDEX history_at   ON history (at);
CREATE INDEX history_kind ON history (kind, at);
