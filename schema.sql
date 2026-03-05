CREATE
EXTENSION IF NOT EXISTS pgcrypto;

CREATE TYPE withdrawal_status AS ENUM (
    'pending',
    'confirmed',
    'failed'
);

CREATE TABLE users
(
    id         UUID PRIMARY KEY        DEFAULT gen_random_uuid(),
    balance    NUMERIC(20, 8) NOT NULL DEFAULT 0 CHECK (balance >= 0),
    created_at TIMESTAMPTZ    NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ    NOT NULL DEFAULT NOW()
);

CREATE TABLE withdrawals
(
    id              UUID PRIMARY KEY           DEFAULT gen_random_uuid(),
    user_id         UUID              NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    amount          NUMERIC(20, 8)    NOT NULL CHECK (amount > 0),
    currency        VARCHAR(10)       NOT NULL,
    destination     TEXT              NOT NULL,
    status          withdrawal_status NOT NULL DEFAULT 'pending',
    idempotency_key VARCHAR(100)      NOT NULL,
    created_at      TIMESTAMPTZ       NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ       NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, idempotency_key)
);