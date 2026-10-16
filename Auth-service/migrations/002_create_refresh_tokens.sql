-- 002_create_refresh_tokens.sql
-- Creates the refresh_tokens table used for rotating refresh token flow.

CREATE TABLE IF NOT EXISTS refresh_tokens (
    id          BIGSERIAL     PRIMARY KEY,
    user_id     BIGINT        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash  VARCHAR(64)   NOT NULL,   -- SHA-256 hex digest
    expires_at  TIMESTAMPTZ   NOT NULL,
    created_at  TIMESTAMPTZ   NOT NULL DEFAULT NOW(),

    CONSTRAINT refresh_tokens_hash_unique UNIQUE (token_hash)
);

-- Fast lookup by hash (used on every token refresh / logout).
CREATE INDEX IF NOT EXISTS idx_refresh_tokens_hash       ON refresh_tokens (token_hash);

-- Fast cleanup of all tokens for a user (logout-all / delete-account).
CREATE INDEX IF NOT EXISTS idx_refresh_tokens_user_id    ON refresh_tokens (user_id);

-- Fast cleanup of expired tokens (background job).
CREATE INDEX IF NOT EXISTS idx_refresh_tokens_expires_at ON refresh_tokens (expires_at);
