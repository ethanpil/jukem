-- A rule and a date exception can now play a stream. SQLite cannot change a
-- CHECK, so both tables are built again with the wider one and the rows are
-- copied over.
CREATE TABLE schedules_new (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT NOT NULL,
    enabled     INTEGER NOT NULL DEFAULT 1,
    days        INTEGER NOT NULL,
    start_time  TEXT NOT NULL,
    end_time    TEXT NOT NULL,
    source_type TEXT NOT NULL CHECK (source_type IN ('directory', 'playlist', 'stream')),
    source_ref  TEXT NOT NULL,
    shuffle     INTEGER NOT NULL DEFAULT 0,
    volume      INTEGER,
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);
INSERT INTO schedules_new SELECT id, name, enabled, days, start_time, end_time, source_type, source_ref, shuffle, volume, created_at, updated_at FROM schedules;
DROP TABLE schedules;
ALTER TABLE schedules_new RENAME TO schedules;

CREATE TABLE schedule_exceptions_new (
    date        TEXT PRIMARY KEY,
    kind        TEXT NOT NULL CHECK (kind IN ('silent', 'hours', 'source')),
    note        TEXT NOT NULL DEFAULT '',
    start_time  TEXT,
    end_time    TEXT,
    source_type TEXT CHECK (source_type IN ('directory', 'playlist', 'stream')),
    source_ref  TEXT,
    shuffle     INTEGER,
    volume      INTEGER
);
INSERT INTO schedule_exceptions_new SELECT date, kind, note, start_time, end_time, source_type, source_ref, shuffle, volume FROM schedule_exceptions;
DROP TABLE schedule_exceptions;
ALTER TABLE schedule_exceptions_new RENAME TO schedule_exceptions;
