-- Stalled downloads: when a download last made progress.
--
-- Some torrents never finish: an Internet Archive torrent whose files changed
-- after it was made sits at 99.97% for ever. The poll compares the time since
-- progress_changed_at with the "downloads" setting stallMinutes and gives up on a
-- download that has not gained progress for that long.
--
-- It is set when the download is added, whenever its progress grows, and when it
-- leaves queued or paused (waiting is not stalling). Rows that were there before
-- count from their last update.

ALTER TABLE downloads ADD COLUMN progress_changed_at TEXT NOT NULL DEFAULT '';
UPDATE downloads SET progress_changed_at = updated_at;
