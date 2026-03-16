ALTER TABLE theses
    ADD COLUMN IF NOT EXISTS market_context JSONB,
    ADD COLUMN IF NOT EXISTS surprise_assessment JSONB;
