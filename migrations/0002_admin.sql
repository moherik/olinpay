-- openpay.id admin console and environment scoping.
--
-- 1. Environment dimension. A transaction/delivery belongs to the environment
--    of the API key that created it (sk_test_* -> sandbox, sk_live_* ->
--    production). Existing rows predate environments; the seeded API keys are
--    test keys, so they are backfilled to sandbox.
-- 2. Admin console tables: console users with roles, server-side sessions,
--    an append-only audit log, per-merchant API keys, inbound gateway callback
--    history, and DB-stored provider credentials.

ALTER TABLE transactions ADD COLUMN environment TEXT NOT NULL DEFAULT 'sandbox';

ALTER TABLE webhook_deliveries ADD COLUMN environment TEXT NOT NULL DEFAULT 'sandbox';

-- Console-created merchants no longer need a legacy key: their credentials
-- live in api_keys. The column stays unique; multiple NULLs are allowed.
ALTER TABLE merchants ALTER COLUMN api_key_hash DROP NOT NULL;

-- Per-merchant API keys. The single merchants.api_key_hash column stays as a
-- legacy credential path; new keys are issued here so a merchant can hold
-- several labeled keys across environments.
CREATE TABLE IF NOT EXISTS api_keys (
    id             TEXT PRIMARY KEY,
    merchant_id    TEXT        NOT NULL REFERENCES merchants (id),
    label          TEXT        NOT NULL DEFAULT '',
    key_hash       TEXT        NOT NULL UNIQUE,
    key_prefix     TEXT        NOT NULL DEFAULT '',
    environment    TEXT        NOT NULL DEFAULT 'sandbox',
    status         TEXT        NOT NULL DEFAULT 'active',
    last_used_at   TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS api_keys_merchant_idx
    ON api_keys (merchant_id, environment, status);

-- Console accounts. Passwords are bcrypt hashes; roles are owner > operator >
-- tech and are enforced by admin middleware.
CREATE TABLE IF NOT EXISTS admin_users (
    id            TEXT PRIMARY KEY,
    email         TEXT        NOT NULL UNIQUE,
    name          TEXT        NOT NULL DEFAULT '',
    password_hash TEXT        NOT NULL,
    role          TEXT        NOT NULL DEFAULT 'operator',
    status        TEXT        NOT NULL DEFAULT 'active',
    last_login_at TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Server-side sessions. The browser holds the raw token in an HttpOnly cookie;
-- only its SHA-256 hash is stored, so a database leak does not yield sessions.
CREATE TABLE IF NOT EXISTS admin_sessions (
    token_hash TEXT PRIMARY KEY,
    user_id    TEXT        NOT NULL REFERENCES admin_users (id) ON DELETE CASCADE,
    ip         TEXT        NOT NULL DEFAULT '',
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS admin_sessions_user_idx
    ON admin_sessions (user_id);

-- Append-only console audit log. Entries are written by the admin layer for
-- every state-changing call and can never be updated or deleted via the API.
CREATE TABLE IF NOT EXISTS audit_log (
    id     TEXT PRIMARY KEY,
    actor  TEXT        NOT NULL,
    action TEXT        NOT NULL,
    target TEXT        NOT NULL DEFAULT '',
    ip     TEXT        NOT NULL DEFAULT '',
    at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS audit_log_at_idx
    ON audit_log (at DESC);

-- Inbound gateway callback history, written by the webhook receiver for every
-- callback attempt (accepted or rejected). Reprocessing reconstructs the
-- normalized event from the stored fields.
CREATE TABLE IF NOT EXISTS inbound_events (
    id                 TEXT PRIMARY KEY,
    provider           TEXT        NOT NULL,
    environment        TEXT        NOT NULL DEFAULT '',
    transaction_id     TEXT        NOT NULL DEFAULT '',
    event_type         TEXT        NOT NULL DEFAULT '',
    reference_id       TEXT        NOT NULL DEFAULT '',
    provider_reference TEXT        NOT NULL DEFAULT '',
    amount             BIGINT      NOT NULL DEFAULT 0,
    status             TEXT        NOT NULL DEFAULT '',
    signature_valid    BOOLEAN     NOT NULL DEFAULT FALSE,
    parsed             BOOLEAN     NOT NULL DEFAULT FALSE,
    applied            BOOLEAN     NOT NULL DEFAULT FALSE,
    note               TEXT        NOT NULL DEFAULT '',
    remote_addr        TEXT        NOT NULL DEFAULT '',
    received_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS inbound_events_received_idx
    ON inbound_events (received_at DESC);

-- DB-stored provider credentials, one row per (provider adapter, environment):
-- sandbox and production credentials are separate builds. A production row is
-- instantiated as <provider>, a sandbox row as <provider>.sandbox. Values are
-- write-only through the admin API and are merged into the provider build
-- configuration at startup and on every save (hot reload).
CREATE TABLE IF NOT EXISTS provider_configs (
    provider    TEXT NOT NULL,
    environment TEXT NOT NULL,
    config      JSONB       NOT NULL DEFAULT '{}'::jsonb,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by  TEXT        NOT NULL DEFAULT '',
    PRIMARY KEY (provider, environment)
);
