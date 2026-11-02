-- Opt-in, account-owned content channels. Existing records and ownership are
-- preserved; this migration does not publish content or create channel rows.
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
                    'agent','agent_task','agent_event','agent_release','agent_runtime',
                    'agent_task_template','agent_collection_plan','agent_collection_run',
                    'learning_channel'));

-- The service always writes <owner>:singleton; also enforce one channel per
-- workspace in the database if another caller chooses a different record ID.
CREATE UNIQUE INDEX workspace_learning_channel_owner
    ON workspace_records ((COALESCE(record->>'_workspace_user_id','')))
    WHERE kind='learning_channel';
