ALTER TABLE theses
    DROP CONSTRAINT IF EXISTS chk_theses_status;

ALTER TABLE theses
    ADD CONSTRAINT chk_theses_status
    CHECK (status IN ('embryo', 'nursery', 'pending_execution', 'prosecuted', 'active', 'resolved'));
