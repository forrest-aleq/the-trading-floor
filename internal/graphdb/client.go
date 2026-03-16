package graphdb

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/hnic/trading-floor/pkg/model"
	"github.com/hnic/trading-floor/pkg/signal"
	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

type Client struct {
	driver   neo4j.DriverWithContext
	database string
	log      *slog.Logger
}

func NewFromEnv(ctx context.Context) (*Client, error) {
	uri := strings.TrimSpace(os.Getenv("NEO4J_URI"))
	if uri == "" {
		return nil, nil
	}

	username := strings.TrimSpace(os.Getenv("NEO4J_USERNAME"))
	if username == "" {
		username = "neo4j"
	}
	password := os.Getenv("NEO4J_PASSWORD")
	if strings.TrimSpace(password) == "" {
		return nil, fmt.Errorf("NEO4J_PASSWORD is required when NEO4J_URI is set")
	}

	driver, err := neo4j.NewDriverWithContext(uri, neo4j.BasicAuth(username, password, ""))
	if err != nil {
		return nil, fmt.Errorf("connect neo4j: %w", err)
	}

	if err := driver.VerifyConnectivity(ctx); err != nil {
		_ = driver.Close(ctx)
		return nil, fmt.Errorf("verify neo4j connectivity: %w", err)
	}

	client := &Client{
		driver:   driver,
		database: strings.TrimSpace(os.Getenv("NEO4J_DATABASE")),
		log:      slog.Default().With("component", "graphdb"),
	}

	if err := client.bootstrapSchema(ctx); err != nil {
		_ = driver.Close(ctx)
		return nil, err
	}

	return client, nil
}

func (c *Client) Close(ctx context.Context) error {
	if c == nil || c.driver == nil {
		return nil
	}
	return c.driver.Close(ctx)
}

func (c *Client) UpsertDesk(ctx context.Context, deskID, domain, abGroup string) error {
	if c == nil || c.driver == nil || strings.TrimSpace(deskID) == "" {
		return nil
	}

	now := time.Now().UTC()
	return c.executeWrite(ctx, func(tx neo4j.ManagedTransaction) error {
		return runQuery(ctx, tx, `
			MERGE (d:Desk {id: $id})
			SET d.domain = $domain,
			    d.ab_group = $ab_group,
			    d.updated_at = $updated_at`,
			map[string]any{
				"id":         deskID,
				"domain":     strings.TrimSpace(domain),
				"ab_group":   strings.TrimSpace(abGroup),
				"updated_at": now,
			},
		)
	})
}

func (c *Client) UpsertCompetenceState(ctx context.Context, state *model.CompetenceState) error {
	if c == nil || c.driver == nil || state == nil || strings.TrimSpace(state.Key) == "" {
		return nil
	}

	updatedAt := state.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}

	return c.executeWrite(ctx, func(tx neo4j.ManagedTransaction) error {
		if err := runQuery(ctx, tx, `
			MERGE (c:CompetenceState {key: $key})
			SET c.desk_id = $desk_id,
			    c.capability = $capability,
			    c.context = $context,
			    c.regime = $regime,
			    c.trust = $trust,
			    c.confidence = $confidence,
			    c.success_count = $success_count,
			    c.failure_count = $failure_count,
			    c.total_pnl = $total_pnl,
			    c.sharpe = $sharpe,
			    c.autonomy_mode = $autonomy_mode,
			    c.updated_at = $updated_at`,
			map[string]any{
				"key":           state.Key,
				"desk_id":       strings.TrimSpace(state.DeskID),
				"capability":    strings.TrimSpace(state.Capability),
				"context":       strings.TrimSpace(state.Context),
				"regime":        strings.TrimSpace(state.Regime),
				"trust":         state.Trust,
				"confidence":    state.Confidence,
				"success_count": state.SuccessCount,
				"failure_count": state.FailureCount,
				"total_pnl":     state.TotalPnL,
				"sharpe":        state.Sharpe,
				"autonomy_mode": string(state.Autonomy),
				"updated_at":    updatedAt,
			},
		); err != nil {
			return err
		}
		if strings.TrimSpace(state.DeskID) == "" {
			return nil
		}
		if err := runQuery(ctx, tx, `
			MERGE (d:Desk {id: $desk_id})
			SET d.updated_at = $updated_at`,
			map[string]any{
				"desk_id":    strings.TrimSpace(state.DeskID),
				"updated_at": updatedAt,
			},
		); err != nil {
			return err
		}
		return runQuery(ctx, tx, `
			MATCH (d:Desk {id: $desk_id})
			MATCH (c:CompetenceState {key: $key})
			MERGE (d)-[r:HAS_COMPETENCE]->(c)
			SET r.updated_at = $updated_at`,
			map[string]any{
				"desk_id":    strings.TrimSpace(state.DeskID),
				"key":        state.Key,
				"updated_at": updatedAt,
			},
		)
	})
}

func (c *Client) RecordSignalSeen(ctx context.Context, signalID, deskID, domain string, seenAt time.Time) error {
	if c == nil || c.driver == nil || strings.TrimSpace(signalID) == "" || strings.TrimSpace(deskID) == "" {
		return nil
	}

	if seenAt.IsZero() {
		seenAt = time.Now().UTC()
	}

	return c.executeWrite(ctx, func(tx neo4j.ManagedTransaction) error {
		if err := runQuery(ctx, tx, `
			MERGE (s:Signal {id: $signal_id})
			ON CREATE SET s.created_at = $seen_at`,
			map[string]any{
				"signal_id": signalID,
				"seen_at":   seenAt,
			},
		); err != nil {
			return err
		}

		if err := runQuery(ctx, tx, `
			MERGE (d:Desk {id: $desk_id})
			SET d.domain = CASE WHEN $domain = '' THEN d.domain ELSE $domain END,
			    d.updated_at = $seen_at`,
			map[string]any{
				"desk_id": deskID,
				"domain":  strings.TrimSpace(domain),
				"seen_at": seenAt,
			},
		); err != nil {
			return err
		}

		return runQuery(ctx, tx, `
			MATCH (s:Signal {id: $signal_id})
			MATCH (d:Desk {id: $desk_id})
			MERGE (s)-[r:SEEN_BY]->(d)
			ON CREATE SET r.first_seen_at = $seen_at
			SET r.last_seen_at = $seen_at,
			    r.observed_time = $seen_at,
			    r.decision_time = $seen_at`,
			map[string]any{
				"signal_id": signalID,
				"desk_id":   deskID,
				"seen_at":   seenAt,
			},
		)
	})
}

func (c *Client) UpsertSignal(ctx context.Context, sig signal.Signal) error {
	if c == nil || c.driver == nil || strings.TrimSpace(sig.ID) == "" {
		return nil
	}

	now := time.Now().UTC()
	meta := sig.EvidenceMeta
	return c.executeWrite(ctx, func(tx neo4j.ManagedTransaction) error {
		props := map[string]any{
			"id":                     sig.ID,
			"source":                 sig.Source,
			"type":                   string(sig.Type),
			"category":               sig.Category,
			"translated":             sig.Translated,
			"urgency":                sig.Urgency,
			"strength":               sig.Strength,
			"direction":              string(sig.Direction),
			"cluster_id":             sig.ClusterID,
			"content_hash":           sig.ContentHash,
			"timestamp":              normalizeTime(sig.Timestamp, now),
			"decision_time":          now,
			"original_language":      primaryLanguage(sig),
			"source_domain":          evidenceString(meta, func() string { return meta.SourceDomain }),
			"source_owner_group":     evidenceString(meta, func() string { return meta.SourceOwnerGroup }),
			"source_tier":            evidenceString(meta, func() string { return meta.SourceTier }),
			"source_type":            evidenceString(meta, func() string { return meta.SourceType }),
			"source_trust":           evidenceFloat(meta, func() float64 { return meta.SourceTrust }),
			"freshness_status":       evidenceString(meta, func() string { return meta.FreshnessStatus }),
			"freshness_reason":       evidenceString(meta, func() string { return meta.FreshnessReason }),
			"freshness_age_hours":    evidenceFloat(meta, func() float64 { return meta.FreshnessAgeHours }),
			"freshness_window_hours": evidenceFloat(meta, func() float64 { return meta.FreshnessWindowHours }),
			"distinct_sources":       evidenceInt(meta, func() int { return meta.DistinctSources }),
			"distinct_owner_groups":  evidenceInt(meta, func() int { return meta.DistinctOwnerGroups }),
			"has_primary_source":     evidenceBool(meta, func() bool { return meta.HasPrimarySource }),
			"contradiction_count":    evidenceInt(meta, func() int { return meta.ContradictionCount }),
			"contradiction_severity": evidenceString(meta, func() string {
				return meta.ContradictionSeverity
			}),
			"evidence_score": evidenceFloat(meta, func() float64 { return meta.EvidenceScore }),
		}
		if err := runQuery(ctx, tx, `
			MERGE (s:Signal {id: $id})
			SET s += $props`,
			map[string]any{
				"id":    sig.ID,
				"props": props,
			},
		); err != nil {
			return err
		}

		if err := c.linkSignalLineage(ctx, tx, sig, now); err != nil {
			return err
		}
		if err := c.linkSignalEntities(ctx, tx, sig, now); err != nil {
			return err
		}
		return c.linkSignalRelations(ctx, tx, sig, now)
	})
}

func (c *Client) UpsertThesis(ctx context.Context, thesis *model.Thesis) error {
	if c == nil || c.driver == nil || thesis == nil || strings.TrimSpace(thesis.ID) == "" {
		return nil
	}

	now := time.Now().UTC()
	meta := thesis.EvidenceMeta
	structureClass := thesis.ExecutionCapability()
	if structureClass == "" {
		structureClass = thesis.Structure
	}

	return c.executeWrite(ctx, func(tx neo4j.ManagedTransaction) error {
		if err := runQuery(ctx, tx, `
			MERGE (t:Thesis {id: $id})
			SET t.opportunity_id = $opportunity_id,
			    t.desk_id = $desk_id,
			    t.domain = $domain,
			    t.strategy = $strategy,
			    t.structure = $structure,
			    t.direction = $direction,
			    t.conviction = $conviction,
			    t.health = $health,
			    t.entry_price = $entry_price,
			    t.target_price = $target_price,
			    t.stop_loss = $stop_loss,
			    t.position_size = $position_size,
			    t.status = $status,
			    t.autonomy_mode = $autonomy_mode,
			    t.scan_territory = $scan_territory,
			    t.execution_territory = $execution_territory,
			    t.competence_key = $competence_key,
			    t.competence_trust = $competence_trust,
			    t.competence_confidence = $competence_confidence,
			    t.created_at = $created_at,
			    t.resolved_at = $resolved_at,
			    t.evidence_score = $evidence_score,
			    t.source_trust = $source_trust,
			    t.freshness_status = $freshness_status,
			    t.contradiction_count = $contradiction_count,
			    t.updated_at = $updated_at`,
			map[string]any{
				"id":                    thesis.ID,
				"opportunity_id":        thesis.OpportunityID,
				"desk_id":               thesis.DeskID,
				"domain":                thesis.Domain,
				"strategy":              thesis.Strategy,
				"structure":             thesis.Structure,
				"direction":             string(thesis.Direction),
				"conviction":            thesis.Conviction,
				"health":                thesis.Health,
				"entry_price":           thesis.EntryPrice,
				"target_price":          thesis.TargetPrice,
				"stop_loss":             thesis.StopLoss,
				"position_size":         thesis.PositionSize,
				"status":                string(thesis.Status),
				"autonomy_mode":         string(thesis.AutonomyMode),
				"scan_territory":        thesis.ScanTerritory,
				"execution_territory":   thesis.ExecutionTerritory,
				"competence_key":        thesis.CompetenceKey,
				"competence_trust":      thesis.CompetenceTrust,
				"competence_confidence": thesis.CompetenceConfidence,
				"created_at":            normalizeTime(thesis.CreatedAt, now),
				"resolved_at":           thesis.ResolvedAt,
				"evidence_score":        evidenceFloat(meta, func() float64 { return meta.EvidenceScore }),
				"source_trust":          evidenceFloat(meta, func() float64 { return meta.SourceTrust }),
				"freshness_status":      evidenceString(meta, func() string { return meta.FreshnessStatus }),
				"contradiction_count":   evidenceInt(meta, func() int { return meta.ContradictionCount }),
				"updated_at":            now,
			},
		); err != nil {
			return err
		}

		if err := runQuery(ctx, tx, `
			MERGE (d:Desk {id: $desk_id})
			SET d.domain = CASE WHEN $domain = '' THEN d.domain ELSE $domain END,
			    d.updated_at = $updated_at`,
			map[string]any{
				"desk_id":    thesis.DeskID,
				"domain":     thesis.Domain,
				"updated_at": now,
			},
		); err != nil {
			return err
		}
		if err := runQuery(ctx, tx, `
			MATCH (d:Desk {id: $desk_id})
			MATCH (t:Thesis {id: $thesis_id})
			MERGE (d)-[r:FORMED]->(t)
			ON CREATE SET r.first_seen_at = $created_at
			SET r.observed_time = $created_at,
			    r.decision_time = $decision_time`,
			map[string]any{
				"desk_id":       thesis.DeskID,
				"thesis_id":     thesis.ID,
				"created_at":    normalizeTime(thesis.CreatedAt, now),
				"decision_time": now,
			},
		); err != nil {
			return err
		}

		if strings.TrimSpace(structureClass) != "" {
			if err := runQuery(ctx, tx, `
				MERGE (s:StructureClass {id: $id})
				SET s.name = $name,
				    s.updated_at = $updated_at`,
				map[string]any{
					"id":         structureClass,
					"name":       structureClass,
					"updated_at": now,
				},
			); err != nil {
				return err
			}
			if err := runQuery(ctx, tx, `
				MATCH (t:Thesis {id: $thesis_id})
				MATCH (s:StructureClass {id: $structure_class})
				MERGE (t)-[r:USES_STRUCTURE_CLASS]->(s)
				SET r.observed_time = $decision_time,
				    r.decision_time = $decision_time`,
				map[string]any{
					"thesis_id":       thesis.ID,
					"structure_class": structureClass,
					"decision_time":   now,
				},
			); err != nil {
				return err
			}
		}

		if err := c.linkTradeInstruments(ctx, tx, "Thesis", thesis.ID, thesis.ExecutionInstruments(), "USES_INSTRUMENT", now); err != nil {
			return err
		}
		return c.linkThesisEvidence(ctx, tx, thesis, now)
	})
}

func (c *Client) UpsertPosition(ctx context.Context, pos *model.Position) error {
	if c == nil || c.driver == nil || pos == nil || strings.TrimSpace(pos.ID) == "" {
		return nil
	}

	now := time.Now().UTC()
	return c.executeWrite(ctx, func(tx neo4j.ManagedTransaction) error {
		if err := runQuery(ctx, tx, `
			MERGE (p:Position {id: $id})
			SET p.thesis_id = $thesis_id,
			    p.desk_id = $desk_id,
			    p.structure = $structure,
			    p.direction = $direction,
			    p.quantity = $quantity,
			    p.entry_price = $entry_price,
			    p.current_price = $current_price,
			    p.unrealized_pnl = $unrealized_pnl,
			    p.realized_pnl = $realized_pnl,
			    p.shadow = $shadow,
			    p.status = $status,
			    p.opened_at = $opened_at,
			    p.closed_at = $closed_at,
			    p.updated_at = $updated_at`,
			map[string]any{
				"id":             pos.ID,
				"thesis_id":      pos.ThesisID,
				"desk_id":        pos.DeskID,
				"structure":      pos.Structure,
				"direction":      string(pos.Direction),
				"quantity":       pos.Quantity,
				"entry_price":    pos.EntryPrice,
				"current_price":  pos.CurrentPrice,
				"unrealized_pnl": pos.UnrealizedPnL,
				"realized_pnl":   pos.RealizedPnL,
				"shadow":         pos.Shadow,
				"status":         pos.Status,
				"opened_at":      normalizeTime(pos.OpenedAt, now),
				"closed_at":      pos.ClosedAt,
				"updated_at":     now,
			},
		); err != nil {
			return err
		}

		if err := runQuery(ctx, tx, `
			MERGE (d:Desk {id: $desk_id})
			SET d.updated_at = $updated_at`,
			map[string]any{
				"desk_id":    pos.DeskID,
				"updated_at": now,
			},
		); err != nil {
			return err
		}
		if err := runQuery(ctx, tx, `
			MATCH (d:Desk {id: $desk_id})
			MATCH (p:Position {id: $position_id})
			MERGE (d)-[r:MANAGES]->(p)
			SET r.observed_time = $opened_at,
			    r.decision_time = $decision_time`,
			map[string]any{
				"desk_id":       pos.DeskID,
				"position_id":   pos.ID,
				"opened_at":     normalizeTime(pos.OpenedAt, now),
				"decision_time": now,
			},
		); err != nil {
			return err
		}
		if strings.TrimSpace(pos.ThesisID) != "" {
			if err := runQuery(ctx, tx, `
				MATCH (t:Thesis {id: $thesis_id})
				MATCH (p:Position {id: $position_id})
				MERGE (t)-[r:OPENED_AS]->(p)
				SET r.observed_time = $opened_at,
				    r.decision_time = $decision_time`,
				map[string]any{
					"thesis_id":     pos.ThesisID,
					"position_id":   pos.ID,
					"opened_at":     normalizeTime(pos.OpenedAt, now),
					"decision_time": now,
				},
			); err != nil {
				return err
			}
		}
		return c.linkTradeInstruments(ctx, tx, "Position", pos.ID, pos.ExecutionInstruments(), "HOLDS", now)
	})
}

func (c *Client) RecordOutcome(ctx context.Context, thesis *model.Thesis, pos *model.Position, outcome *model.ThesisOutcome, closedAt time.Time, closeReason string) error {
	if c == nil || c.driver == nil || outcome == nil {
		return nil
	}

	outcomeID := ""
	if thesis != nil && thesis.ID != "" {
		outcomeID = thesis.ID
	} else if pos != nil && pos.ID != "" {
		outcomeID = pos.ID
	}
	if outcomeID == "" {
		return nil
	}
	if closedAt.IsZero() {
		closedAt = time.Now().UTC()
	}

	return c.executeWrite(ctx, func(tx neo4j.ManagedTransaction) error {
		if err := runQuery(ctx, tx, `
			MERGE (o:Outcome {id: $id})
			SET o.profitable = $profitable,
			    o.realized_pnl = $realized_pnl,
			    o.return_pct = $return_pct,
			    o.risk_reward = $risk_reward,
			    o.holding_hours = $holding_hours,
			    o.exit_reason = $exit_reason,
			    o.error_class = $error_class,
			    o.closed_at = $closed_at,
			    o.updated_at = $updated_at`,
			map[string]any{
				"id":            outcomeID,
				"profitable":    outcome.Profitable,
				"realized_pnl":  outcome.RealizedPnL,
				"return_pct":    outcome.ReturnPct,
				"risk_reward":   outcome.RiskReward,
				"holding_hours": outcome.HoldingHours,
				"exit_reason":   firstNonEmpty(closeReason, outcome.ExitReason),
				"error_class":   outcome.ErrorClass,
				"closed_at":     closedAt,
				"updated_at":    closedAt,
			},
		); err != nil {
			return err
		}

		if thesis != nil && thesis.ID != "" {
			if err := runQuery(ctx, tx, `
				MATCH (t:Thesis {id: $thesis_id})
				MATCH (o:Outcome {id: $outcome_id})
				MERGE (t)-[r:RESULTED_IN]->(o)
				SET r.event_time = $closed_at,
				    r.observed_time = $closed_at,
				    r.decision_time = $closed_at`,
				map[string]any{
					"thesis_id":  thesis.ID,
					"outcome_id": outcomeID,
					"closed_at":  closedAt,
				},
			); err != nil {
				return err
			}
		}
		if pos != nil && pos.ID != "" {
			if err := runQuery(ctx, tx, `
				MATCH (p:Position {id: $position_id})
				MATCH (o:Outcome {id: $outcome_id})
				MERGE (p)-[r:RESOLVED_WITH]->(o)
				SET r.event_time = $closed_at,
				    r.observed_time = $closed_at,
				    r.decision_time = $closed_at`,
				map[string]any{
					"position_id": pos.ID,
					"outcome_id":  outcomeID,
					"closed_at":   closedAt,
				},
			); err != nil {
				return err
			}
		}
		return c.linkOutcomeAttribution(ctx, tx, outcomeID, outcome, closedAt)
	})
}

func (c *Client) linkOutcomeAttribution(ctx context.Context, tx neo4j.ManagedTransaction, outcomeID string, outcome *model.ThesisOutcome, closedAt time.Time) error {
	if outcome == nil || outcome.Attribution == nil {
		return nil
	}

	attr := outcome.Attribution
	attributionID := outcomeID + ":attribution"

	if err := runQuery(ctx, tx, `
		MERGE (a:Attribution {id: $id})
		SET a.truth_edge = $truth_edge,
		    a.timing_edge = $timing_edge,
		    a.expression_edge = $expression_edge,
		    a.execution_edge = $execution_edge,
		    a.luck_estimate = $luck_estimate,
		    a.method = $method,
		    a.summary = $summary,
		    a.updated_at = $updated_at`,
		map[string]any{
			"id":              attributionID,
			"truth_edge":      attr.TruthEdge,
			"timing_edge":     attr.TimingEdge,
			"expression_edge": attr.ExpressionEdge,
			"execution_edge":  attr.ExecutionEdge,
			"luck_estimate":   attr.LuckEstimate,
			"method":          strings.TrimSpace(attr.Method),
			"summary":         strings.TrimSpace(attr.Summary),
			"updated_at":      closedAt,
		},
	); err != nil {
		return err
	}
	if err := runQuery(ctx, tx, `
		MATCH (o:Outcome {id: $outcome_id})
		MATCH (a:Attribution {id: $attribution_id})
		MERGE (o)-[r:ATTRIBUTED]->(a)
		SET r.truth_edge = $truth_edge,
		    r.timing_edge = $timing_edge,
		    r.expression_edge = $expression_edge,
		    r.execution_edge = $execution_edge,
		    r.luck_estimate = $luck_estimate,
		    r.observed_time = $closed_at,
		    r.decision_time = $closed_at`,
		map[string]any{
			"outcome_id":      outcomeID,
			"attribution_id":  attributionID,
			"truth_edge":      attr.TruthEdge,
			"timing_edge":     attr.TimingEdge,
			"expression_edge": attr.ExpressionEdge,
			"execution_edge":  attr.ExecutionEdge,
			"luck_estimate":   attr.LuckEstimate,
			"closed_at":       closedAt,
		},
	); err != nil {
		return err
	}

	for _, update := range attr.CompetenceUpdates {
		if strings.TrimSpace(update.Key) == "" {
			continue
		}
		if err := runQuery(ctx, tx, `
			MERGE (c:CompetenceState {key: $key})
			ON CREATE SET c.created_at = $updated_at
			SET c.updated_at = $updated_at`,
			map[string]any{
				"key":        update.Key,
				"updated_at": closedAt,
			},
		); err != nil {
			return err
		}
		if err := runQuery(ctx, tx, `
			MATCH (a:Attribution {id: $attribution_id})
			MATCH (c:CompetenceState {key: $key})
			MERGE (a)-[r:UPDATED {dimension: $dimension}]->(c)
			SET r.score = $score,
			    r.observed_time = $updated_at,
			    r.decision_time = $updated_at`,
			map[string]any{
				"attribution_id": attributionID,
				"key":            update.Key,
				"dimension":      strings.TrimSpace(update.Dimension),
				"score":          update.Score,
				"updated_at":     closedAt,
			},
		); err != nil {
			return err
		}
	}

	return nil
}

func (c *Client) RecordAntiPortfolio(ctx context.Context, thesis *model.Thesis, reason string) error {
	if c == nil || c.driver == nil || thesis == nil || strings.TrimSpace(thesis.ID) == "" {
		return nil
	}

	now := time.Now().UTC()
	id := thesis.ID + ":" + now.Format(time.RFC3339Nano)
	return c.executeWrite(ctx, func(tx neo4j.ManagedTransaction) error {
		if err := runQuery(ctx, tx, `
			MERGE (t:Thesis {id: $id})
			SET t.desk_id = $desk_id,
			    t.domain = $domain,
			    t.strategy = $strategy,
			    t.structure = $structure,
			    t.direction = $direction,
			    t.conviction = $conviction,
			    t.created_at = $created_at,
			    t.updated_at = $updated_at`,
			map[string]any{
				"id":         thesis.ID,
				"desk_id":    thesis.DeskID,
				"domain":     thesis.Domain,
				"strategy":   thesis.Strategy,
				"structure":  thesis.Structure,
				"direction":  string(thesis.Direction),
				"conviction": thesis.Conviction,
				"created_at": normalizeTime(thesis.CreatedAt, now),
				"updated_at": now,
			},
		); err != nil {
			return err
		}
		if err := runQuery(ctx, tx, `
			MERGE (a:AntiPortfolioDecision {id: $id})
			SET a.reason = $reason,
			    a.created_at = $created_at,
			    a.desk_id = $desk_id,
			    a.strategy = $strategy`,
			map[string]any{
				"id":         id,
				"reason":     reason,
				"created_at": now,
				"desk_id":    thesis.DeskID,
				"strategy":   thesis.Strategy,
			},
		); err != nil {
			return err
		}
		return runQuery(ctx, tx, `
			MATCH (t:Thesis {id: $thesis_id})
			MATCH (a:AntiPortfolioDecision {id: $decision_id})
			MERGE (t)-[r:REJECTED_AS]->(a)
			SET r.event_time = $created_at,
			    r.observed_time = $created_at,
			    r.decision_time = $created_at`,
			map[string]any{
				"thesis_id":   thesis.ID,
				"decision_id": id,
				"created_at":  now,
			},
		)
	})
}

func (c *Client) executeWrite(ctx context.Context, fn func(tx neo4j.ManagedTransaction) error) error {
	if c == nil || c.driver == nil {
		return nil
	}

	session := c.driver.NewSession(ctx, neo4j.SessionConfig{
		DatabaseName: c.database,
	})
	defer session.Close(ctx)

	_, err := session.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		return nil, fn(tx)
	})
	return err
}

func runQuery(ctx context.Context, tx neo4j.ManagedTransaction, query string, params map[string]any) error {
	result, err := tx.Run(ctx, query, params)
	if err != nil {
		return err
	}
	for result.Next(ctx) {
	}
	return result.Err()
}
