-- Polish: where an import is up to, an index for the Downloads history, and
-- "unlimited" per-person limits.
--
-- downloads.import_step says which step of its import a download is on (so the
-- Downloads screen can show it), and import_target is the file or folder the
-- import is putting the files into. Both are empty when no import is running.
--
-- user_limits stores -1 for "unlimited" (a person the owner lets ask for as much
-- as they like, whatever the default is); NULL still means "use the default".
-- SQLite cannot change a CHECK, so the table is rebuilt.

ALTER TABLE downloads ADD COLUMN import_step   TEXT NOT NULL DEFAULT ''
    CHECK (import_step IN ('', 'checking', 'renaming', 'moving', 'rescanning', 'done'));
ALTER TABLE downloads ADD COLUMN import_target TEXT NOT NULL DEFAULT '';

-- The Downloads history marks a download whose import replaced a worse file as
-- "upgraded", which it reads from the title's history by download id.
CREATE INDEX history_download ON history (download_id) WHERE download_id IS NOT NULL;

CREATE TABLE user_limits_new (
    user_id          INTEGER PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    hd_per_week      INTEGER CHECK (hd_per_week IS NULL OR hd_per_week >= -1),
    uhd_per_week     INTEGER CHECK (uhd_per_week IS NULL OR uhd_per_week >= -1),
    auto_approve_hd  INTEGER CHECK (auto_approve_hd IS NULL OR auto_approve_hd IN (0, 1)),
    auto_approve_uhd INTEGER CHECK (auto_approve_uhd IS NULL OR auto_approve_uhd IN (0, 1))
);
INSERT INTO user_limits_new (user_id, hd_per_week, uhd_per_week, auto_approve_hd, auto_approve_uhd)
    SELECT user_id, hd_per_week, uhd_per_week, auto_approve_hd, auto_approve_uhd FROM user_limits;
DROP TABLE user_limits;
ALTER TABLE user_limits_new RENAME TO user_limits;
