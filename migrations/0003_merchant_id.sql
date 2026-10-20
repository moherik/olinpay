-- Merchant IDs are human-typed AW identifiers: AW plus eight unambiguous
-- uppercase letters or digits. Existing incompatible rows make this migration
-- fail and must be reset or cleaned up before it can be applied.
ALTER TABLE merchants
    ADD CONSTRAINT merchants_id_aw_format_check
    CHECK (id ~ '^AW[ABCDEFGHJKLMNPQRSTUVWXYZ23456789]{8}$');
