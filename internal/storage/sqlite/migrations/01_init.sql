CREATE TABLE IF NOT EXISTS users (
    id TEXT PRIMARY KEY,
    username TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    disabled INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS tokens (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    hashed_key TEXT NOT NULL UNIQUE,
    scopes TEXT NOT NULL DEFAULT '[]',
    created_at TEXT NOT NULL,
    created_by TEXT NOT NULL,
    expires_at TEXT,
    active INTEGER NOT NULL DEFAULT 1
);

CREATE TABLE IF NOT EXISTS folders (
    path TEXT PRIMARY KEY,
    folders TEXT NOT NULL DEFAULT '[]',
    files TEXT NOT NULL DEFAULT '[]',
    variants TEXT
);

CREATE TABLE IF NOT EXISTS file_versions (
    path TEXT PRIMARY KEY,
    versions TEXT NOT NULL DEFAULT '[]'
);

CREATE TABLE IF NOT EXISTS files (
    path TEXT NOT NULL,
    version INTEGER NOT NULL,
    meta TEXT NOT NULL DEFAULT '{}',
    data BLOB NOT NULL,
    PRIMARY KEY (path, version)
);

CREATE TABLE IF NOT EXISTS settings (
    id TEXT PRIMARY KEY DEFAULT 'default',
    data TEXT NOT NULL DEFAULT '{}'
);
