CREATE TABLE merchant_settings (
    merchant_id TEXT PRIMARY KEY REFERENCES merchants (id) ON DELETE CASCADE,
    return_url  TEXT NOT NULL DEFAULT '',
    success_return_url TEXT NOT NULL DEFAULT '',
    failure_return_url TEXT NOT NULL DEFAULT '',
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
