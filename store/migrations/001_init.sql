-- Trading Floor: Initial Schema
-- Requires: CREATE EXTENSION vector; (pgvector)

CREATE EXTENSION IF NOT EXISTS vector;
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-----------------------------------------------------------
-- Signals
-----------------------------------------------------------
CREATE TABLE signals (
    id              TEXT PRIMARY KEY,
    source          TEXT NOT NULL,
    type            TEXT NOT NULL,
    category        TEXT,
    content         TEXT,
    language        TEXT DEFAULT 'en',
    translated      TEXT,
    entities        JSONB,
    urgency         FLOAT DEFAULT 0,
    strength        FLOAT DEFAULT 0,
    direction       TEXT,
    embedding       vector(1536),
    content_hash    TEXT,
    created_at      TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_signals_embedding ON signals USING ivfflat (embedding vector_cosine_ops);
CREATE INDEX idx_signals_content_hash ON signals (content_hash);
CREATE INDEX idx_signals_created_at ON signals (created_at);
CREATE INDEX idx_signals_source ON signals (source);

-----------------------------------------------------------
-- Opportunities (scanner output)
-----------------------------------------------------------
CREATE TABLE opportunities (
    id              TEXT PRIMARY KEY,
    signal_ids      TEXT[] NOT NULL,
    instruments     JSONB NOT NULL,
    direction       TEXT,
    urgency         FLOAT,
    score           FLOAT,
    category        TEXT,
    cascade_info    JSONB,
    created_at      TIMESTAMPTZ DEFAULT NOW()
);

-----------------------------------------------------------
-- Theses (research output)
-----------------------------------------------------------
CREATE TABLE theses (
    id              TEXT PRIMARY KEY,
    opportunity_id  TEXT REFERENCES opportunities(id),
    desk_id         TEXT NOT NULL,
    strategy        TEXT NOT NULL,
    instrument      JSONB NOT NULL,
    direction       TEXT NOT NULL,
    conviction      FLOAT,
    health          FLOAT DEFAULT 0.5,
    evidence        JSONB,
    counter_args    JSONB,
    entry_price     FLOAT,
    target_price    FLOAT,
    stop_loss       FLOAT,
    position_size   FLOAT,
    time_horizon    INTERVAL,
    kill_rules      JSONB,
    status          TEXT DEFAULT 'embryo',
    prosecution     JSONB,
    council_verdict JSONB,
    outcome         JSONB,
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    resolved_at     TIMESTAMPTZ
);

CREATE INDEX idx_theses_desk ON theses (desk_id);
CREATE INDEX idx_theses_status ON theses (status);
CREATE INDEX idx_theses_strategy ON theses (strategy);

-----------------------------------------------------------
-- Positions (live book)
-----------------------------------------------------------
CREATE TABLE positions (
    id              TEXT PRIMARY KEY,
    thesis_id       TEXT REFERENCES theses(id),
    desk_id         TEXT NOT NULL,
    instrument      JSONB NOT NULL,
    direction       TEXT NOT NULL,
    quantity        FLOAT NOT NULL,
    entry_price     FLOAT NOT NULL,
    current_price   FLOAT,
    unrealized_pnl  FLOAT DEFAULT 0,
    realized_pnl    FLOAT DEFAULT 0,
    ibkr_order_id   INT,
    ibkr_contract_id INT,
    status          TEXT DEFAULT 'open',
    opened_at       TIMESTAMPTZ DEFAULT NOW(),
    closed_at       TIMESTAMPTZ
);

CREATE INDEX idx_positions_desk ON positions (desk_id);
CREATE INDEX idx_positions_status ON positions (status);

-----------------------------------------------------------
-- Belief Graph (competence states)
-----------------------------------------------------------
CREATE TABLE competence_states (
    key             TEXT PRIMARY KEY,
    desk_id         TEXT NOT NULL,
    capability      TEXT NOT NULL,
    context         TEXT,
    regime          TEXT,
    trust           FLOAT DEFAULT 0.55,
    confidence      FLOAT DEFAULT 0.35,
    success_count   INT DEFAULT 0,
    failure_count   INT DEFAULT 0,
    total_pnl       FLOAT DEFAULT 0,
    sharpe          FLOAT DEFAULT 0,
    autonomy_mode   TEXT DEFAULT 'restricted',
    is_backfilled   BOOLEAN DEFAULT false,
    updated_at      TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_competence_desk ON competence_states (desk_id);
CREATE INDEX idx_competence_capability ON competence_states (capability);

-----------------------------------------------------------
-- Engrams (cached winning plays)
-----------------------------------------------------------
CREATE TABLE engrams (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    intent_key      TEXT NOT NULL,
    context_pattern TEXT NOT NULL,
    capability      TEXT NOT NULL,
    desk_id         TEXT,
    layer           INT DEFAULT 1,
    action_plan     JSONB NOT NULL,
    success_count   INT DEFAULT 1,
    failure_count   INT DEFAULT 0,
    avg_return      FLOAT DEFAULT 0,
    sharpe          FLOAT DEFAULT 0,
    regime_tags     TEXT[],
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    updated_at      TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_engrams_intent ON engrams (intent_key);
CREATE INDEX idx_engrams_desk ON engrams (desk_id);
CREATE INDEX idx_engrams_capability ON engrams (capability);

-----------------------------------------------------------
-- Anti-Portfolio (rejected theses + counterfactual)
-----------------------------------------------------------
CREATE TABLE anti_portfolio (
    id               UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    thesis_snapshot  JSONB NOT NULL,
    rejection_reason TEXT NOT NULL,
    desk_id          TEXT NOT NULL,
    strategy         TEXT NOT NULL,
    instrument       JSONB NOT NULL,
    direction        TEXT NOT NULL,
    would_have_entry FLOAT,
    would_have_exit  FLOAT,
    would_have_pnl   FLOAT,
    would_have_outcome TEXT,
    evaluated_at     TIMESTAMPTZ,
    created_at       TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_anti_portfolio_desk ON anti_portfolio (desk_id);
CREATE INDEX idx_anti_portfolio_reason ON anti_portfolio (rejection_reason);

-----------------------------------------------------------
-- Episodes (full trade lifecycle for episodic memory)
-----------------------------------------------------------
CREATE TABLE episodes (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    thesis_id       TEXT REFERENCES theses(id),
    desk_id         TEXT NOT NULL,
    strategy        TEXT NOT NULL,
    instrument      JSONB NOT NULL,
    regime          TEXT,
    entry_signal    JSONB,
    entry_fill      JSONB,
    exit_signal     JSONB,
    exit_fill       JSONB,
    holding_period  INTERVAL,
    realized_pnl    FLOAT,
    return_pct      FLOAT,
    risk_reward     FLOAT,
    error_class     TEXT,
    lesson          TEXT,
    created_at      TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_episodes_desk ON episodes (desk_id);
CREATE INDEX idx_episodes_strategy ON episodes (strategy);
CREATE INDEX idx_episodes_regime ON episodes (regime);

-----------------------------------------------------------
-- Audit Log (immutable, append-only)
-----------------------------------------------------------
CREATE TABLE audit_log (
    id              BIGSERIAL PRIMARY KEY,
    timestamp       TIMESTAMPTZ DEFAULT NOW(),
    desk_id         TEXT,
    event_type      TEXT NOT NULL,
    event_data      JSONB NOT NULL,
    thesis_id       TEXT,
    position_id     TEXT,
    order_id        INT
);

CREATE INDEX idx_audit_timestamp ON audit_log (timestamp);
CREATE INDEX idx_audit_desk ON audit_log (desk_id);
CREATE INDEX idx_audit_type ON audit_log (event_type);

-----------------------------------------------------------
-- Desk Performance (daily snapshots for CEO referee)
-----------------------------------------------------------
CREATE TABLE desk_performance (
    id              BIGSERIAL PRIMARY KEY,
    desk_id         TEXT NOT NULL,
    date            DATE NOT NULL,
    ab_group        TEXT NOT NULL,
    domain          TEXT NOT NULL,
    daily_pnl       FLOAT DEFAULT 0,
    daily_return    FLOAT DEFAULT 0,
    trades_taken    INT DEFAULT 0,
    trades_won      INT DEFAULT 0,
    sharpe_30d      FLOAT,
    max_drawdown    FLOAT,
    capital_allocated FLOAT,
    capital_deployed FLOAT,
    autonomy_level  TEXT,
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(desk_id, date)
);

-----------------------------------------------------------
-- Regime State (current market regime detection)
-----------------------------------------------------------
CREATE TABLE regime_state (
    id              BIGSERIAL PRIMARY KEY,
    dimension       TEXT NOT NULL,
    value           TEXT NOT NULL,
    previous_value  TEXT,
    confidence      FLOAT DEFAULT 0,
    detected_at     TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(dimension)
);

INSERT INTO regime_state (dimension, value) VALUES
    ('volatility', 'medium'),
    ('trend', 'neutral'),
    ('risk', 'neutral'),
    ('liquidity', 'normal');
