-- 001_create_users.sql
-- Creates the users table for the auth service.

CREATE TABLE IF NOT EXISTS users (
    id            BIGSERIAL     PRIMARY KEY,
    name          VARCHAR(100)  NOT NULL,
    email         VARCHAR(254)  NOT NULL,
    password_hash TEXT          NOT NULL,
    role          VARCHAR(50)   NOT NULL DEFAULT 'customer',
    created_at    TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ   NOT NULL DEFAULT NOW(),

    CONSTRAINT users_email_unique UNIQUE (email)
);

-- Automatically update updated_at on every row change.
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER users_updated_at
    BEFORE UPDATE ON users
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

-- Index for fast login lookups.
CREATE INDEX IF NOT EXISTS idx_users_email ON users (email);
