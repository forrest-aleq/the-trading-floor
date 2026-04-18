CREATE TABLE IF NOT EXISTS working_orders (
    id               TEXT PRIMARY KEY,
    thesis_id        TEXT,
    desk_id          TEXT NOT NULL,
    state            TEXT NOT NULL,
    broker_order_id  BIGINT,
    display_symbol   TEXT,
    paper            BOOLEAN DEFAULT false,
    submitted_at     TIMESTAMPTZ,
    updated_at       TIMESTAMPTZ DEFAULT NOW() NOT NULL,
    order_payload    JSONB NOT NULL,
    snapshot_payload JSONB NOT NULL,
    fill_payload     JSONB
);

CREATE INDEX IF NOT EXISTS idx_working_orders_state ON working_orders (state);
CREATE INDEX IF NOT EXISTS idx_working_orders_updated_at ON working_orders (updated_at);
CREATE INDEX IF NOT EXISTS idx_working_orders_desk_state ON working_orders (desk_id, state);
