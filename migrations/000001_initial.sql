CREATE TABLE feed_clock (
    id smallint PRIMARY KEY CHECK (id = 1),
    cursor bigint NOT NULL DEFAULT 0 CHECK (cursor >= 0)
);
INSERT INTO feed_clock(id, cursor) VALUES (1, 0);

CREATE TABLE sources (
    id text PRIMARY KEY,
    slug text NOT NULL UNIQUE,
    record jsonb NOT NULL
);

CREATE TABLE items (
    id text PRIMARY KEY,
    source_id text NOT NULL REFERENCES sources(id),
    external_id text NOT NULL,
    record jsonb NOT NULL,
    live jsonb,
    published_at timestamptz,
    updated_at timestamptz NOT NULL,
    UNIQUE(source_id, external_id)
);
CREATE INDEX items_latest ON items(published_at DESC, id DESC) WHERE live IS NOT NULL;
CREATE INDEX items_source_latest ON items(source_id, published_at DESC, id DESC) WHERE live IS NOT NULL;
CREATE INDEX items_admin_updated ON items(updated_at DESC, id DESC);
CREATE INDEX items_category ON items((live->>'category')) WHERE live IS NOT NULL;
CREATE INDEX items_tags ON items USING gin((live->'tags')) WHERE live IS NOT NULL;

-- No sequence: feed_clock is updated under a transaction lock. This is deliberately
-- a commit-ordered durable outbox, not a best-effort process-local notification bus.
CREATE TABLE changes (
    cursor bigint PRIMARY KEY,
    source_id text NOT NULL REFERENCES sources(id),
    item_id text NOT NULL REFERENCES items(id) DEFERRABLE INITIALLY DEFERRED,
    record jsonb NOT NULL
);
CREATE INDEX changes_source_cursor ON changes(source_id, cursor);
