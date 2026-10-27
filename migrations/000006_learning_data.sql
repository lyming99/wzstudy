-- Personal learning data belongs to an authenticated query client. New record
-- kinds and clocks are separate from the legacy feedback and content streams.
ALTER TABLE workspace_records DROP CONSTRAINT workspace_records_kind_check;
ALTER TABLE workspace_records ADD CONSTRAINT workspace_records_kind_check
    CHECK (kind IN ('asset','collection','plan','delivery','settings','client','intake',
                    'feedback_event','feedback_state','feedback_clock',
                    'catalog_collection','catalog_change','catalog_clock',
                    'learning_state','learning_event','learning_clock',
                    'learning_record','learning_intervals','learning_session'));

CREATE UNIQUE INDEX workspace_learning_state_client_item
    ON workspace_records ((record->>'client_id'), (record->>'item_id'))
    WHERE kind='learning_state';
CREATE UNIQUE INDEX workspace_learning_event_client_id
    ON workspace_records ((record->>'client_id'), (record->>'event_id'))
    WHERE kind='learning_event';
CREATE UNIQUE INDEX workspace_learning_event_client_sequence
    ON workspace_records ((record->>'client_id'), ((record->>'sequence')::bigint))
    WHERE kind='learning_event';
CREATE UNIQUE INDEX workspace_learning_record_client_id
    ON workspace_records ((record->>'client_id'), (record->>'id'))
    WHERE kind='learning_record';
