CREATE TABLE sessions_new (
    id TEXT PRIMARY KEY,
    user_id TEXT,
    username TEXT NOT NULL,
    payload BLOB,
    refresh_id TEXT,
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL
);
INSERT INTO sessions_new (id, user_id, username, created_at, expires_at)
    SELECT id, user_id, username, created_at, expires_at FROM sessions;
DROP TABLE sessions;
ALTER TABLE sessions_new RENAME TO sessions;
CREATE INDEX idx_sessions_user_id ON sessions(user_id);
CREATE INDEX idx_sessions_expires_at ON sessions(expires_at);
