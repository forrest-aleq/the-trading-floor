-- Trading Floor: Schema Hardening
-- Unique constraints, CHECK constraints, JSONB schema validation, event log

-----------------------------------------------------------
-- Add CHECK constraints for enum-like columns
-----------------------------------------------------------
ALTER TABLE theses ADD CONSTRAINT chk_theses_direction
    CHECK (direction IN ('long', 'short'));
ALTER TABLE theses ADD CONSTRAINT chk_theses_status
    CHECK (status IN ('embryo', 'nursery', 'prosecuted', 'active', 'resolved'));
ALTER TABLE theses ADD CONSTRAINT chk_theses_conviction
    CHECK (conviction >= 0 AND conviction <= 1);

ALTER TABLE positions ADD CONSTRAINT chk_positions_direction
    CHECK (direction IN ('long', 'short'));
ALTER TABLE positions ADD CONSTRAINT chk_positions_status
    CHECK (status IN ('open', 'closing', 'closed'));
ALTER TABLE positions ADD CONSTRAINT chk_positions_quantity
    CHECK (quantity > 0);
ALTER TABLE positions ADD CONSTRAINT chk_positions_entry_price
    CHECK (entry_price > 0);

ALTER TABLE signals ADD CONSTRAINT chk_signals_urgency
    CHECK (urgency >= 0 AND urgency <= 1);
ALTER TABLE signals ADD CONSTRAINT chk_signals_strength
    CHECK (strength >= 0 AND strength <= 1);

ALTER TABLE opportunities ADD CONSTRAINT chk_opportunities_score
    CHECK (score >= 0 AND score <= 100);
ALTER TABLE opportunities ADD CONSTRAINT chk_opportunities_urgency
    CHECK (urgency >= 0 AND urgency <= 1);

ALTER TABLE competence_states ADD CONSTRAINT chk_competence_trust
    CHECK (trust >= 0 AND trust <= 1);
ALTER TABLE competence_states ADD CONSTRAINT chk_competence_confidence
    CHECK (confidence >= 0 AND confidence <= 1);
ALTER TABLE competence_states ADD CONSTRAINT chk_competence_autonomy
    CHECK (autonomy_mode IN ('restricted', 'supervised', 'autonomous'));

-----------------------------------------------------------
-- Unique constraints to prevent duplicates
-----------------------------------------------------------
ALTER TABLE signals ADD CONSTRAINT uq_signals_content_hash
    UNIQUE (content_hash) DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE anti_portfolio ADD CONSTRAINT uq_anti_portfolio_thesis
    UNIQUE (desk_id, (thesis_snapshot->>'id'), rejection_reason);

-----------------------------------------------------------
-- Composite index for thesis lookups by desk + status
-----------------------------------------------------------
CREATE INDEX IF NOT EXISTS idx_theses_desk_status ON theses (desk_id, status);
CREATE INDEX IF NOT EXISTS idx_positions_desk_status ON positions (desk_id, status);

-----------------------------------------------------------
-- Event log table for infrastructure/system events
-----------------------------------------------------------
CREATE TABLE IF NOT EXISTS event_log (
    id              BIGSERIAL PRIMARY KEY,
    timestamp       TIMESTAMPTZ DEFAULT NOW() NOT NULL,
    event_type      TEXT NOT NULL,
    session_id      TEXT,
    trace_id        TEXT,
    desk_id         TEXT,
    severity        TEXT DEFAULT 'info' CHECK (severity IN ('debug', 'info', 'warn', 'error', 'fatal')),
    message         TEXT NOT NULL,
    metadata        JSONB DEFAULT '{}'::jsonb
);

CREATE INDEX IF NOT EXISTS idx_event_log_timestamp ON event_log (timestamp);
CREATE INDEX IF NOT EXISTS idx_event_log_type ON event_log (event_type);
CREATE INDEX IF NOT EXISTS idx_event_log_session ON event_log (session_id);
CREATE INDEX IF NOT EXISTS idx_event_log_trace ON event_log (trace_id);
CREATE INDEX IF NOT EXISTS idx_event_log_severity ON event_log (severity);
