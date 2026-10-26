-- Client-private feedback events, aggregate state and independent feedback clocks.
-- No historical identity, credential, material or content cursor is rewritten.
ALTER TABLE workspace_records DROP CONSTRAINT workspace_records_kind_check;
ALTER TABLE workspace_records ADD CONSTRAINT workspace_records_kind_check
    CHECK (kind IN ('asset','collection','plan','delivery','settings','client','intake',
                    'feedback_event','feedback_state','feedback_clock'));

CREATE UNIQUE INDEX workspace_feedback_state_client_item
    ON workspace_records ((record->>'client_id'), (record->>'item_id'))
    WHERE kind='feedback_state';
CREATE UNIQUE INDEX workspace_feedback_event_client_id
    ON workspace_records ((record->>'client_id'), (record->>'event_id'))
    WHERE kind='feedback_event';
CREATE UNIQUE INDEX workspace_feedback_event_client_sequence
    ON workspace_records ((record->>'client_id'), ((record->>'sequence')::bigint))
    WHERE kind='feedback_event';
