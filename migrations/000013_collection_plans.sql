-- Additive collection scheduling; retired legacy 'plan' records remain inert.
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
                    'agent_task_template','agent_collection_plan','agent_collection_run'));

CREATE UNIQUE INDEX workspace_collection_run_trigger
    ON workspace_records ((COALESCE(record->>'_workspace_user_id','')), (record->>'plan_id'), (record->>'trigger_key'))
    WHERE kind='agent_collection_run';

CREATE INDEX workspace_collection_plan_due
    ON workspace_records ((record->>'next_run_at'))
    WHERE kind='agent_collection_plan' AND record->>'enabled'='true' AND record->>'deleted_at' IS NULL;

CREATE INDEX workspace_collection_run_plan
    ON workspace_records ((COALESCE(record->>'_workspace_user_id','')), (record->>'plan_id'), (record->>'created_at'))
    WHERE kind='agent_collection_run';

CREATE INDEX workspace_collection_active_task
    ON workspace_records ((COALESCE(record->>'_workspace_user_id','')), (record->>'plan_id'))
    WHERE kind='agent_task' AND record->>'plan_id' IS NOT NULL
      AND record->>'status' IN ('pending','claimed','running');
