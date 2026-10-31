-- Collection agent program releases. Metadata only; package bytes are stored
-- under DATA_DIR/agent-releases and never inside the database.

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
                    'workspace_clock','workspace_change',
                    'agent','agent_task','agent_event','agent_release'));

CREATE UNIQUE INDEX workspace_agent_release_version
    ON workspace_records (
        (COALESCE(record->>'_workspace_user_id','')),
        (record->>'version'),
        (record->>'platform'),
        (record->>'arch')
    )
    WHERE kind='agent_release';

CREATE INDEX workspace_agent_release_created
    ON workspace_records ((COALESCE(record->>'_workspace_user_id','')), (record->>'created_at'))
    WHERE kind='agent_release';
