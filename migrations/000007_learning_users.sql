-- Additive: administrator ingestion and independently authenticated learners.
-- No historical Client learning data is reassigned, copied or deleted.
-- client_id in legacy learning/feedback records is an immutable owner ID; new
-- sessions use the learning user's UUID, never an application Client's UUID.
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
                    'learning_user','learning_user_session'));

CREATE UNIQUE INDEX workspace_learning_user_email
    ON workspace_records ((record->'profile'->>'email'))
    WHERE kind='learning_user';
CREATE INDEX workspace_learning_user_session_owner
    ON workspace_records ((record->>'user_id'), (record->>'expires_at'))
    WHERE kind='learning_user_session';
