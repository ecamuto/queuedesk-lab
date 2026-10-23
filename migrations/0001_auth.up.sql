-- 0001_auth: utenti e sessioni opache.

-- pgcrypto fornisce gen_random_uuid() (da PostgreSQL 13 è anche nel core,
-- ma abilitarla esplicitamente rende la dipendenza visibile).
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE users (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    email         TEXT        NOT NULL CHECK (email <> ''),
    password_hash TEXT        NOT NULL,
    role          TEXT        NOT NULL DEFAULT 'requester'
                              CHECK (role IN ('requester', 'agent')),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Unicità case-insensitive: "Ada@Example.com" e "ada@example.com" sono lo
-- stesso utente. Le query di login usano lower(email) e sfruttano l'indice.
CREATE UNIQUE INDEX users_email_lower_key ON users (lower(email));

CREATE TABLE sessions (
    -- SHA-256 del token: il token in chiaro non viene mai salvato.
    token_hash BYTEA       PRIMARY KEY CHECK (octet_length(token_hash) = 32),
    -- Se l'utente viene eliminato, le sue sessioni non hanno più senso.
    user_id    UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL,
    CHECK (expires_at > created_at)
);

-- Per la pulizia periodica delle sessioni scadute (vedi BACKLOG).
CREATE INDEX sessions_expires_at_idx ON sessions (expires_at);
-- Per il CASCADE e per un eventuale "logout da tutti i dispositivi".
CREATE INDEX sessions_user_id_idx ON sessions (user_id);
