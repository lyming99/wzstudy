-- Independent query clients and immutable successful-intake receipts.
-- Existing materials, source IDs, cursors and archived records are untouched.
ALTER TABLE workspace_records DROP CONSTRAINT workspace_records_kind_check;
ALTER TABLE workspace_records ADD CONSTRAINT workspace_records_kind_check
    CHECK (kind IN ('asset','collection','plan','delivery','settings','client','intake'));

CREATE UNIQUE INDEX workspace_client_key_hash
    ON workspace_records ((record->'internal'->>'key_hash'))
    WHERE kind='client' AND COALESCE(record->'internal'->>'key_hash','') <> '';
CREATE INDEX workspace_intake_received_at
    ON workspace_records ((record->>'received_at')) WHERE kind='intake';
