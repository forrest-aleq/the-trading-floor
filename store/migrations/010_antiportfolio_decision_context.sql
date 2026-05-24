ALTER TABLE anti_portfolio
    ADD COLUMN IF NOT EXISTS thesis_id TEXT,
    ADD COLUMN IF NOT EXISTS opportunity_id TEXT,
    ADD COLUMN IF NOT EXISTS domain TEXT,
    ADD COLUMN IF NOT EXISTS status_at_rejection TEXT,
    ADD COLUMN IF NOT EXISTS conviction FLOAT,
    ADD COLUMN IF NOT EXISTS position_size FLOAT,
    ADD COLUMN IF NOT EXISTS entry_price FLOAT,
    ADD COLUMN IF NOT EXISTS target_price FLOAT,
    ADD COLUMN IF NOT EXISTS stop_loss FLOAT,
    ADD COLUMN IF NOT EXISTS prosecution_verdict TEXT,
    ADD COLUMN IF NOT EXISTS prosecution_confidence FLOAT,
    ADD COLUMN IF NOT EXISTS council_approved BOOLEAN,
    ADD COLUMN IF NOT EXISTS council_weighted_vote_score FLOAT,
    ADD COLUMN IF NOT EXISTS council_voice_count INT,
    ADD COLUMN IF NOT EXISTS counterfactual_status TEXT DEFAULT 'not_evaluated' NOT NULL,
    ADD COLUMN IF NOT EXISTS metadata JSONB DEFAULT '{}'::jsonb NOT NULL;

UPDATE anti_portfolio
SET
    thesis_id = COALESCE(thesis_id, thesis_snapshot->>'id'),
    opportunity_id = COALESCE(opportunity_id, thesis_snapshot->>'opportunity_id'),
    domain = COALESCE(domain, thesis_snapshot->>'domain'),
    status_at_rejection = COALESCE(status_at_rejection, thesis_snapshot->>'status'),
    conviction = COALESCE(conviction, NULLIF(thesis_snapshot->>'conviction', '')::float8),
    position_size = COALESCE(position_size, NULLIF(thesis_snapshot->>'position_size', '')::float8),
    entry_price = COALESCE(entry_price, NULLIF(thesis_snapshot->>'entry_price', '')::float8),
    target_price = COALESCE(target_price, NULLIF(thesis_snapshot->>'target_price', '')::float8),
    stop_loss = COALESCE(stop_loss, NULLIF(thesis_snapshot->>'stop_loss', '')::float8),
    prosecution_verdict = COALESCE(prosecution_verdict, thesis_snapshot->'prosecution'->>'verdict'),
    prosecution_confidence = COALESCE(prosecution_confidence, NULLIF(thesis_snapshot->'prosecution'->>'confidence', '')::float8),
    council_approved = COALESCE(council_approved, NULLIF(thesis_snapshot->'council_verdict'->>'approved', '')::boolean),
    council_weighted_vote_score = COALESCE(council_weighted_vote_score, NULLIF(thesis_snapshot->'council_verdict'->>'weighted_vote_score', '')::float8),
    council_voice_count = COALESCE(council_voice_count, jsonb_array_length(COALESCE(thesis_snapshot->'council_verdict'->'voices', '[]'::jsonb))),
    counterfactual_status = CASE
        WHEN would_have_pnl IS NULL THEN 'not_evaluated'
        ELSE 'evaluated'
    END
WHERE thesis_snapshot IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_anti_portfolio_thesis_id ON anti_portfolio (thesis_id);
CREATE INDEX IF NOT EXISTS idx_anti_portfolio_created_at ON anti_portfolio (created_at DESC);
CREATE INDEX IF NOT EXISTS idx_anti_portfolio_counterfactual_status ON anti_portfolio (counterfactual_status);
