-- Additive migration: existing source records, content snapshots and cursors
-- remain compatible. Do not edit an already-applied migration.
CREATE TABLE workspace_records (
    kind text NOT NULL,
    id text NOT NULL,
    record jsonb NOT NULL,
    PRIMARY KEY (kind,id),
    CHECK (kind IN ('asset','collection','plan','delivery','settings'))
);
CREATE INDEX workspace_plan_status ON workspace_records ((record->>'status')) WHERE kind='plan';
CREATE INDEX workspace_delivery_cursor ON workspace_records (((record->>'cursor')::bigint)) WHERE kind='delivery';
CREATE INDEX items_media_kind ON items ((COALESCE(NULLIF(record->'draft'->>'kind',''),'article'))) WHERE record->>'deleted_at' IS NULL;
