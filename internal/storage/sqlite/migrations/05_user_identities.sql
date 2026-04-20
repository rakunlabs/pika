-- Link users to one or more external identity providers (OAuth2, LDAP,
-- Header strategies) so a single user row can unify multiple login
-- methods. Without this, every OAuth2 login is invisible to the admin UI
-- (no row to list, no user_id to kick) and the same human arriving from
-- two providers produces two separate uncoordinated "users".
--
-- Design notes:
--   * user_identities is additive: local login continues to work as-is,
--     just using password_hash on users.
--   * A user may have zero or many identities. The legacy local-user rows
--     stay identity-less — their password on users is the credential.
--   * (provider, subject) is globally unique: one OIDC sub can only ever
--     map to one pika user.
--   * Nullable users.email + users.display_name enable auto-linking by
--     verified email when configured in AuthSettings.
--   * users.external marks "no password, external-only" rows so password
--     login attempts against them fail cleanly at the strategy layer
--     instead of bcrypt-matching an empty hash.

ALTER TABLE users ADD COLUMN email TEXT;
ALTER TABLE users ADD COLUMN display_name TEXT;
ALTER TABLE users ADD COLUMN external INTEGER NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS user_identities (
    id            TEXT PRIMARY KEY,
    user_id       TEXT NOT NULL,
    provider      TEXT NOT NULL,
    subject       TEXT NOT NULL,
    email         TEXT,
    display_name  TEXT,
    created_at    TEXT NOT NULL,
    last_login_at TEXT,
    UNIQUE (provider, subject),
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_user_identities_user_id
    ON user_identities (user_id);

CREATE INDEX IF NOT EXISTS idx_users_email
    ON users (email)
    WHERE email IS NOT NULL AND email != '';
