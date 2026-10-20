-- Redacted, bounded request/response/event trace for transaction operations.
CREATE TABLE IF NOT EXISTS transaction_activities (
    id             TEXT PRIMARY KEY,
    transaction_id TEXT REFERENCES transactions (id) ON DELETE CASCADE,
    merchant_id    TEXT NOT NULL,
    environment    TEXT NOT NULL DEFAULT '',
    reference_id   TEXT NOT NULL DEFAULT '',
    direction      TEXT NOT NULL,
    phase          TEXT NOT NULL,
    provider       TEXT NOT NULL DEFAULT '',
    event_type     TEXT NOT NULL DEFAULT '',
    method         TEXT NOT NULL DEFAULT '',
    url            TEXT NOT NULL DEFAULT '',
    status_code    INTEGER NOT NULL DEFAULT 0,
    duration_ms    BIGINT NOT NULL DEFAULT 0,
    attempt        INTEGER NOT NULL DEFAULT 0,
    signature_valid BOOLEAN,
    applied        BOOLEAN,
    headers        JSONB NOT NULL DEFAULT '{}'::jsonb,
    body           TEXT NOT NULL DEFAULT '',
    error          TEXT NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS transaction_activities_transaction_idx
    ON transaction_activities (transaction_id, created_at, id);

CREATE INDEX IF NOT EXISTS transaction_activities_reference_idx
    ON transaction_activities (merchant_id, environment, reference_id, created_at, id);
