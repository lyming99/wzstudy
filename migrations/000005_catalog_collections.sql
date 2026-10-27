-- Independent published catalogue snapshots and change cursor. Legacy
-- "collection" records and the public content change stream remain untouched.
ALTER TABLE workspace_records DROP CONSTRAINT workspace_records_kind_check;
ALTER TABLE workspace_records ADD CONSTRAINT workspace_records_kind_check
    CHECK (kind IN ('asset','collection','plan','delivery','settings','client','intake',
                    'feedback_event','feedback_state','feedback_clock',
                    'catalog_collection','catalog_change','catalog_clock'));

CREATE UNIQUE INDEX workspace_catalog_change_cursor
    ON workspace_records (((record->>'cursor')::bigint))
    WHERE kind='catalog_change';
