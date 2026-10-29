-- All user data, including ingestion, is now private to its owning user.
-- Existing unowned sources/items/records remain archived and are NOT assigned
-- to an administrator, first login, or every user. No existing row is deleted.
ALTER TABLE sources DROP CONSTRAINT sources_slug_key;
CREATE UNIQUE INDEX sources_owner_slug
    ON sources ((COALESCE(record->'source'->>'owner_id','')), slug);
CREATE INDEX items_workspace_updated
    ON items ((record->>'owner_id'), updated_at DESC, id DESC);
CREATE INDEX workspace_record_owner
    ON workspace_records (kind, (record->>'_workspace_user_id'));
DROP INDEX workspace_catalog_change_cursor;
CREATE UNIQUE INDEX workspace_catalog_change_cursor
    ON workspace_records ((COALESCE(record->>'_workspace_user_id','')), ((record->>'cursor')::bigint))
    WHERE kind='catalog_change';

ALTER TABLE workspace_records DROP CONSTRAINT workspace_records_kind_check;
ALTER TABLE workspace_records ADD CONSTRAINT workspace_records_kind_check
    CHECK (kind IN ('asset','collection','plan','delivery','settings','client','intake',
                    'system_administrator','learning_roadmap',
                    'feedback_event','feedback_state','feedback_clock',
                    'catalog_collection','catalog_change','catalog_clock',
                    'learning_state','learning_event','learning_clock',
                    'learning_record','learning_intervals','learning_session',
                    'learning_draft','learning_draft_event',
                    'personal_collection','personal_collection_event',
                    'learning_user','learning_user_session',
                    'workspace_clock','workspace_change'));
