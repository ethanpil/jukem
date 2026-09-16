-- Announcements are single files that play instead of the music at their
-- own times: a message for customers, an offer, an advertisement.
CREATE TABLE announcements (
	id            INTEGER PRIMARY KEY,
	name          TEXT    NOT NULL,
	enabled       INTEGER NOT NULL DEFAULT 1,
	days          INTEGER NOT NULL,
	-- 'at' plays once at at_time. 'every' plays every every_minutes from
	-- start_time to end_time.
	mode          TEXT    NOT NULL CHECK (mode IN ('at', 'every')),
	at_time       TEXT,
	start_time    TEXT,
	end_time      TEXT,
	every_minutes INTEGER,
	-- 'file' plays source_ref. 'random' plays one file from the folder
	-- source_ref. 'cycle' plays the next file of that folder each time.
	source_kind   TEXT    NOT NULL CHECK (source_kind IN ('file', 'random', 'cycle')),
	source_ref    TEXT    NOT NULL,
	cycle_index   INTEGER NOT NULL DEFAULT 0,
	volume        INTEGER,
	last_played   TEXT,
	created_at    TEXT    NOT NULL
);

CREATE INDEX announcements_enabled ON announcements (enabled);
