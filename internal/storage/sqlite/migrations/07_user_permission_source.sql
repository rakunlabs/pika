-- Tag each user_permissions assignment with the source that produced it.
--
-- The same user/permission pair may legitimately be granted by multiple
-- sources (e.g. an admin manually assigned "Editor" AND an LDAP group
-- maps to "Editor"). We want to preserve both rows so sync-driven
-- removal of the LDAP source doesn't silently strip the manual grant.
-- That requires source to be part of the primary key, so SQLite's
-- one-and-only ALTER TABLE limitation forces a table rebuild.
--
-- Existing rows were all created via the admin UI, so they're labeled
-- 'local' on copy. The user-sync engine writes rows with
-- source='<sync source ID>'; reconciliation queries delete only
-- rows for one source at a time.

CREATE TABLE user_permissions_new (
    user_id       TEXT NOT NULL,
    permission_id TEXT NOT NULL,
    source        TEXT NOT NULL DEFAULT 'local',
    PRIMARY KEY (user_id, permission_id, source),
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
    FOREIGN KEY (permission_id) REFERENCES permissions(id) ON DELETE CASCADE
);

INSERT INTO user_permissions_new (user_id, permission_id, source)
    SELECT user_id, permission_id, 'local' FROM user_permissions;

DROP TABLE user_permissions;
ALTER TABLE user_permissions_new RENAME TO user_permissions;

CREATE INDEX IF NOT EXISTS idx_user_permissions_user_id ON user_permissions(user_id);
CREATE INDEX IF NOT EXISTS idx_user_permissions_permission_id ON user_permissions(permission_id);
CREATE INDEX IF NOT EXISTS idx_user_permissions_source ON user_permissions(source);
