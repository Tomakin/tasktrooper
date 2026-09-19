-- Browser sign-ins for a server reached over the web (WEB_AUTH_USERS). Keyed by
-- the SHA-256 of the cookie value, never the value itself.
CREATE TABLE IF NOT EXISTS web_sessions (
    token_hash   TEXT PRIMARY KEY,
    username     TEXT NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at   TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS web_sessions_expires_at_idx ON web_sessions (expires_at);
