-- Team accounts: named operators sharing one ArKex instance.
-- Legacy single-password (settings auth.password_hash) stays for username ARTEX;
-- additional members live here.

CREATE TABLE IF NOT EXISTS auth_users (
    id            BIGSERIAL PRIMARY KEY,
    username      TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    role          TEXT NOT NULL DEFAULT 'operator'
                  CHECK (role IN ('admin', 'operator', 'viewer')),
    pwd_version   BIGINT NOT NULL DEFAULT 1,
    disabled      BOOLEAN NOT NULL DEFAULT false,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_auth_users_role ON auth_users(role) WHERE NOT disabled;
