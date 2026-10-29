-- Collection agent identities, server-assigned tasks and task-scoped progress.
-- Additive only: existing kinds, sources, clients and intake receipts are kept.

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
                    'agent','agent_task','agent_event'));

CREATE UNIQUE INDEX workspace_agent_key_hash
    ON workspace_records ((record->'internal'->>'key_hash'))
    WHERE kind='agent' AND COALESCE(record->'internal'->>'key_hash','') <> '';

CREATE UNIQUE INDEX workspace_agent_slug
    ON workspace_records ((COALESCE(record->>'_workspace_user_id','')), (record->'agent'->>'slug'))
    WHERE kind='agent';

CREATE INDEX workspace_agent_task_status
    ON workspace_records ((COALESCE(record->>'_workspace_user_id','')), (record->>'status'), (record->>'priority'), (record->>'created_at'))
    WHERE kind='agent_task';

CREATE INDEX workspace_agent_task_agent
    ON workspace_records ((COALESCE(record->>'_workspace_user_id','')), (record->>'agent_id'))
    WHERE kind='agent_task';

CREATE INDEX workspace_agent_event_task
    ON workspace_records ((COALESCE(record->>'_workspace_user_id','')), (record->>'task_id'), (record->>'attempt'))
    WHERE kind='agent_event';
