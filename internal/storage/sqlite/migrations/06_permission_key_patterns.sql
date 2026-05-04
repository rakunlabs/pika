-- Per-capability path-glob restrictions.
--
-- Each row scopes one (permission, capability key) grant to a single
-- doublestar glob pattern. When a (permission_id, key) pair has zero rows
-- here, the grant is unrestricted (matches any path) — preserves the prior
-- behavior for every existing permission row.
--
-- The (permission_id, key) pair must already exist in permission_keys; the
-- composite FK with ON DELETE CASCADE makes pattern removal automatic when
-- a key is deselected from a permission.
CREATE TABLE IF NOT EXISTS permission_key_patterns (
    permission_id TEXT NOT NULL,
    key           TEXT NOT NULL,
    pattern       TEXT NOT NULL,
    PRIMARY KEY (permission_id, key, pattern),
    FOREIGN KEY (permission_id, key) REFERENCES permission_keys(permission_id, key) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_permission_key_patterns_lookup
    ON permission_key_patterns(permission_id, key);
