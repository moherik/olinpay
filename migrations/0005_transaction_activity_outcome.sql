-- Add outcome metadata introduced after the initial activity table shipped.
ALTER TABLE transaction_activities
    ADD COLUMN IF NOT EXISTS attempt INTEGER NOT NULL DEFAULT 0;

ALTER TABLE transaction_activities
    ADD COLUMN IF NOT EXISTS signature_valid BOOLEAN;

ALTER TABLE transaction_activities
    ADD COLUMN IF NOT EXISTS applied BOOLEAN;
