-- Initial schema.

CREATE TABLE state (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

CREATE TABLE login (
    id            INTEGER PRIMARY KEY CHECK (id = 1),
    password_hash TEXT NOT NULL,
    updated_at    TEXT NOT NULL
);

CREATE TABLE sessions (
    id           TEXT PRIMARY KEY,
    csrf_token   TEXT NOT NULL,
    created_at   TEXT NOT NULL,
    last_seen_at TEXT NOT NULL,
    expires_at   TEXT NOT NULL,
    ip           TEXT NOT NULL DEFAULT '',
    user_agent   TEXT NOT NULL DEFAULT ''
);

CREATE TABLE api_keys (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    name         TEXT NOT NULL,
    key_hash     TEXT NOT NULL UNIQUE,
    scopes       TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL,
    last_used_at TEXT,
    last_used_ip TEXT NOT NULL DEFAULT '',
    expires_at   TEXT
);

CREATE TABLE playlists (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT NOT NULL UNIQUE,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE schedules (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT NOT NULL,
    enabled     INTEGER NOT NULL DEFAULT 1,
    days        INTEGER NOT NULL,
    start_time  TEXT NOT NULL,
    end_time    TEXT NOT NULL,
    source_type TEXT NOT NULL CHECK (source_type IN ('directory', 'playlist')),
    source_ref  TEXT NOT NULL,
    shuffle     INTEGER NOT NULL DEFAULT 0,
    volume      INTEGER,
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);

CREATE TABLE schedule_exceptions (
    date        TEXT PRIMARY KEY,
    kind        TEXT NOT NULL CHECK (kind IN ('silent', 'hours', 'source')),
    note        TEXT NOT NULL DEFAULT '',
    start_time  TEXT,
    end_time    TEXT,
    source_type TEXT CHECK (source_type IN ('directory', 'playlist')),
    source_ref  TEXT,
    shuffle     INTEGER,
    volume      INTEGER
);

CREATE TABLE overrides (
    id               INTEGER PRIMARY KEY CHECK (id = 1),
    mode             TEXT NOT NULL CHECK (mode IN ('until_next', 'timed', 'play_now')),
    intent           TEXT NOT NULL CHECK (intent IN ('play', 'pause', 'stop')),
    ends_at          TEXT,
    source           TEXT NOT NULL,
    queue_generation INTEGER NOT NULL DEFAULT 0,
    created_at       TEXT NOT NULL
);

CREATE TABLE history (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    started_at TEXT NOT NULL,
    file       TEXT NOT NULL,
    title      TEXT NOT NULL DEFAULT '',
    artist     TEXT NOT NULL DEFAULT '',
    album      TEXT NOT NULL DEFAULT '',
    source     TEXT NOT NULL DEFAULT ''
);
CREATE INDEX history_started_at ON history (started_at);

CREATE TABLE alerts (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    kind         TEXT NOT NULL,
    message      TEXT NOT NULL,
    fix          TEXT NOT NULL DEFAULT '',
    raised_at    TEXT NOT NULL,
    updated_at   TEXT NOT NULL,
    dismissed_at TEXT,
    count        INTEGER NOT NULL DEFAULT 1
);
CREATE UNIQUE INDEX alerts_active_kind ON alerts (kind) WHERE dismissed_at IS NULL;

CREATE TABLE do_not_play (
    file     TEXT PRIMARY KEY,
    title    TEXT NOT NULL DEFAULT '',
    added_at TEXT NOT NULL
);

CREATE TABLE device_mixer (
    device_key TEXT PRIMARY KEY,
    control    TEXT NOT NULL,
    level      INTEGER NOT NULL,
    reapply    INTEGER NOT NULL DEFAULT 0
);
