ALTER TABLE users ADD COLUMN is_superadmin INTEGER NOT NULL DEFAULT 0;

-- Promote the earliest-created existing user to superadmin (if any users exist).
-- This ensures existing installs don't lose access after the migration.
UPDATE users SET is_superadmin = 1
WHERE id = (SELECT id FROM users ORDER BY created_at ASC LIMIT 1);

CREATE TABLE IF NOT EXISTS permissions (
    id          TEXT PRIMARY KEY,
    key         TEXT NOT NULL UNIQUE,
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS permission_keys (
    permission_id TEXT NOT NULL,
    key TEXT NOT NULL,
    PRIMARY KEY (permission_id, key),
    FOREIGN KEY (permission_id) REFERENCES permissions(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_permission_keys_key ON permission_keys(key);

CREATE TABLE IF NOT EXISTS user_permissions (
    user_id       TEXT NOT NULL,
    permission_id TEXT NOT NULL,
    PRIMARY KEY (user_id, permission_id),
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
    FOREIGN KEY (permission_id) REFERENCES permissions(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_user_permissions_user_id ON user_permissions(user_id);
CREATE INDEX IF NOT EXISTS idx_user_permissions_permission_id ON user_permissions(permission_id);
