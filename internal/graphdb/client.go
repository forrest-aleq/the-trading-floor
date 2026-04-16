package graphdb

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"os"
	"strings"
	"time"

	"github.com/hnic/trading-floor/pkg/evidence"
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
			    c.trust_ceiling = $trust_ceiling,
			    c.confidence_ceiling = $confidence_ceiling,
			    c.validated_outcomes = $validated_outcomes,
			    c.success_count = $success_count,
			    c.failure_count = $failure_count,
			    c.total_pnl = $total_pnl,
			    c.sharpe = $sharpe,
			    c.autonomy_mode = $autonomy_mode,
			    c.updated_at = $updated_at`,
			map[string]any{
				"key":                state.Key,
				"desk_id":            strings.TrimSpace(state.DeskID),
				"capability":         strings.TrimSpace(state.Capability),
				"context":            strings.TrimSpace(state.Context),
				"regime":             strings.TrimSpace(state.Regime),
				"trust":              state.Trust,
				"confidence":         state.Confidence,
				"trust_ceiling":      state.TrustCeiling,
				"confidence_ceiling": state.ConfidenceCeiling,
				"validated_outcomes": state.ValidatedOutcomes,
				"success_count":      state.SuccessCount,
				"failure_count":      state.FailureCount,
				"total_pnl":          state.TotalPnL,
				"sharpe":             state.Sharpe,
				"autonomy_mode":      string(state.Autonomy),
				"updated_at":         updatedAt,
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

func (c *Client) UpsertDeskRelationshipBelief(ctx context.Context, state *model.DeskRelationshipBelief) error {
	if c == nil || c.driver == nil || state == nil || strings.TrimSpace(state.Key) == "" {
		return nil
	}

	updatedAt := state.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}

	return c.executeWrite(ctx, func(tx neo4j.ManagedTransaction) error {
		if err := runQuery(ctx, tx, `
			MERGE (b:DeskRelationshipBelief {key: $key})
			SET b.origin_desk = $origin_desk,
			    b.receiving_desk = $receiving_desk,
			    b.domain = $domain,
			    b.regime = $regime,
			    b.trust = $trust,
			    b.confidence = $confidence,
			    b.success_count = $success_count,
			    b.failure_count = $failure_count,
			    b.updated_at = $updated_at`,
			map[string]any{
				"key":            state.Key,
				"origin_desk":    strings.TrimSpace(state.OriginDesk),
				"receiving_desk": strings.TrimSpace(state.ReceivingDesk),
				"domain":         strings.TrimSpace(state.Domain),
				"regime":         strings.TrimSpace(state.Regime),
				"trust":          state.Trust,
				"confidence":     state.Confidence,
				"success_count":  state.SuccessCount,
				"failure_count":  state.FailureCount,
				"updated_at":     updatedAt,
			},
		); err != nil {
			return err
		}
		if err := runQuery(ctx, tx, `
			MERGE (origin:Desk {id: $origin_desk})
			SET origin.updated_at = $updated_at`,
			map[string]any{
				"origin_desk": strings.TrimSpace(state.OriginDesk),
				"updated_at":  updatedAt,
			},
		); err != nil {
			return err
		}
		if err := runQuery(ctx, tx, `
			MERGE (receiver:Desk {id: $receiving_desk})
			SET receiver.updated_at = $updated_at`,
			map[string]any{
				"receiving_desk": strings.TrimSpace(state.ReceivingDesk),
				"updated_at":     updatedAt,
			},
		); err != nil {
			return err
		}
		if err := runQuery(ctx, tx, `
			MATCH (origin:Desk {id: $origin_desk})
			MATCH (b:DeskRelationshipBelief {key: $key})
			MERGE (origin)-[r:HAS_PEER_BELIEF]->(b)
			SET r.updated_at = $updated_at`,
			map[string]any{
				"origin_desk": strings.TrimSpace(state.OriginDesk),
				"key":         state.Key,
				"updated_at":  updatedAt,
			},
		); err != nil {
			return err
		}
		return runQuery(ctx, tx, `
			MATCH (receiver:Desk {id: $receiving_desk})
			MATCH (b:DeskRelationshipBelief {key: $key})
			MERGE (receiver)-[r:HAS_PEER_BELIEF]->(b)
			SET r.updated_at = $updated_at`,
			map[string]any{
				"receiving_desk": strings.TrimSpace(state.ReceivingDesk),
				"key":            state.Key,
				"updated_at":     updatedAt,
			},
		)
	})
}

func (c *Client) UpsertSourceReliabilityBelief(ctx context.Context, state *model.SourceReliabilityBelief) error {
	if c == nil || c.driver == nil || state == nil || strings.TrimSpace(state.Key) == "" {
		return nil
	}

	updatedAt := state.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}

	return c.executeWrite(ctx, func(tx neo4j.ManagedTransaction) error {
		if err := runQuery(ctx, tx, `
			MERGE (b:SourceReliabilityBelief {key: $key})
			SET b.source_domain = $source_domain,
			    b.owner_group = $owner_group,
			    b.signal_domain = $signal_domain,
			    b.language = $language,
			    b.region = $region,
			    b.trust = $trust,
			    b.confidence = $confidence,
			    b.success_count = $success_count,
			    b.failure_count = $failure_count,
			    b.updated_at = $updated_at`,
			map[string]any{
				"key":           state.Key,
				"source_domain": strings.TrimSpace(state.SourceDomain),
				"owner_group":   strings.TrimSpace(state.OwnerGroup),
				"signal_domain": strings.TrimSpace(state.SignalDomain),
				"language":      strings.TrimSpace(state.Language),
				"region":        strings.TrimSpace(state.Region),
				"trust":         state.Trust,
				"confidence":    state.Confidence,
				"success_count": state.SuccessCount,
				"failure_count": state.FailureCount,
				"updated_at":    updatedAt,
			},
		); err != nil {
			return err
		}
		if strings.TrimSpace(state.SourceDomain) != "" {
			if err := runQuery(ctx, tx, `
				MERGE (src:Source {id: $source_id})
				SET src.name = CASE WHEN src.name IS NULL OR src.name = '' THEN $source_domain ELSE src.name END,
				    src.updated_at = $updated_at`,
				map[string]any{
					"source_id":     strings.ToLower(strings.TrimSpace(state.SourceDomain)),
					"source_domain": strings.TrimSpace(state.SourceDomain),
					"updated_at":    updatedAt,
				},
			); err != nil {
				return err
			}
			if err := runQuery(ctx, tx, `
				MATCH (src:Source {id: $source_id})
				MATCH (b:SourceReliabilityBelief {key: $key})
				MERGE (src)-[r:HAS_RELIABILITY_BELIEF]->(b)
				SET r.updated_at = $updated_at`,
				map[string]any{
					"source_id":  strings.ToLower(strings.TrimSpace(state.SourceDomain)),
					"key":        state.Key,
					"updated_at": updatedAt,
				},
			); err != nil {
				return err
			}
		}
		if strings.TrimSpace(state.SignalDomain) != "" {
			if err := runQuery(ctx, tx, `
				MERGE (d:Domain {id: $domain})
				SET d.updated_at = $updated_at`,
				map[string]any{
					"domain":     strings.TrimSpace(state.SignalDomain),
					"updated_at": updatedAt,
				},
			); err != nil {
				return err
			}
			if err := runQuery(ctx, tx, `
				MATCH (d:Domain {id: $domain})
				MATCH (b:SourceReliabilityBelief {key: $key})
				MERGE (b)-[r:RELIABLE_FOR]->(d)
				SET r.updated_at = $updated_at`,
				map[string]any{
					"domain":     strings.TrimSpace(state.SignalDomain),
					"key":        state.Key,
					"updated_at": updatedAt,
				},
			); err != nil {
				return err
			}
		}
		return nil
	})
}

func (c *Client) LoadDeskRelationshipBeliefs(ctx context.Context) ([]*model.DeskRelationshipBelief, error) {
	if c == nil || c.driver == nil {
		return nil, nil
	}

	session := c.driver.NewSession(ctx, neo4j.SessionConfig{DatabaseName: c.database})
	defer session.Close(ctx)

	result, err := session.ExecuteRead(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		rows, err := tx.Run(ctx, `
			MATCH (b:DeskRelationshipBelief)
			RETURN b.key AS key,
			       b.origin_desk AS origin_desk,
			       b.receiving_desk AS receiving_desk,
			       b.domain AS domain,
			       b.regime AS regime,
			       coalesce(b.trust, 0.0) AS trust,
			       coalesce(b.confidence, 0.0) AS confidence,
			       coalesce(b.success_count, 0) AS success_count,
			       coalesce(b.failure_count, 0) AS failure_count,
			       b.updated_at AS updated_at
			ORDER BY b.updated_at DESC`,
			nil,
		)
		if err != nil {
			return nil, err
		}

		beliefs := make([]*model.DeskRelationshipBelief, 0)
		for rows.Next(ctx) {
			record := rows.Record()
			updatedAt, _ := record.Get("updated_at")
			beliefs = append(beliefs, &model.DeskRelationshipBelief{
				Key:           strings.TrimSpace(toString(recordValue(record, "key"))),
				OriginDesk:    strings.TrimSpace(toString(recordValue(record, "origin_desk"))),
				ReceivingDesk: strings.TrimSpace(toString(recordValue(record, "receiving_desk"))),
				Domain:        strings.TrimSpace(toString(recordValue(record, "domain"))),
				Regime:        strings.TrimSpace(toString(recordValue(record, "regime"))),
				Trust:         toFloat(recordValue(record, "trust")),
				Confidence:    toFloat(recordValue(record, "confidence")),
				SuccessCount:  toInt(recordValue(record, "success_count")),
				FailureCount:  toInt(recordValue(record, "failure_count")),
				UpdatedAt:     toTime(updatedAt),
			})
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return beliefs, nil
	})
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, nil
	}
	return result.([]*model.DeskRelationshipBelief), nil
}

func (c *Client) LoadSourceReliabilityBeliefs(ctx context.Context) ([]*model.SourceReliabilityBelief, error) {
	if c == nil || c.driver == nil {
		return nil, nil
	}

	session := c.driver.NewSession(ctx, neo4j.SessionConfig{DatabaseName: c.database})
	defer session.Close(ctx)

	result, err := session.ExecuteRead(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		rows, err := tx.Run(ctx, `
			MATCH (b:SourceReliabilityBelief)
			RETURN b.key AS key,
			       b.source_domain AS source_domain,
			       b.owner_group AS owner_group,
			       b.signal_domain AS signal_domain,
			       b.language AS language,
			       b.region AS region,
			       coalesce(b.trust, 0.0) AS trust,
			       coalesce(b.confidence, 0.0) AS confidence,
			       coalesce(b.success_count, 0) AS success_count,
			       coalesce(b.failure_count, 0) AS failure_count,
			       b.updated_at AS updated_at
			ORDER BY b.updated_at DESC`,
			nil,
		)
		if err != nil {
			return nil, err
		}

		beliefs := make([]*model.SourceReliabilityBelief, 0)
		for rows.Next(ctx) {
			record := rows.Record()
			updatedAt, _ := record.Get("updated_at")
			beliefs = append(beliefs, &model.SourceReliabilityBelief{
				Key:          strings.TrimSpace(toString(recordValue(record, "key"))),
				SourceDomain: strings.TrimSpace(toString(recordValue(record, "source_domain"))),
				OwnerGroup:   strings.TrimSpace(toString(recordValue(record, "owner_group"))),
				SignalDomain: strings.TrimSpace(toString(recordValue(record, "signal_domain"))),
				Language:     strings.TrimSpace(toString(recordValue(record, "language"))),
				Region:       strings.TrimSpace(toString(recordValue(record, "region"))),
				Trust:        toFloat(recordValue(record, "trust")),
				Confidence:   toFloat(recordValue(record, "confidence")),
				SuccessCount: toInt(recordValue(record, "success_count")),
				FailureCount: toInt(recordValue(record, "failure_count")),
				UpdatedAt:    toTime(updatedAt),
			})
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return beliefs, nil
	})
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, nil
	}
	return result.([]*model.SourceReliabilityBelief), nil
}

func (c *Client) UpsertSourceLeadTimeBelief(ctx context.Context, state *model.SourceLeadTimeBelief) error {
	if c == nil || c.driver == nil || state == nil || strings.TrimSpace(state.Key) == "" {
		return nil
	}

	updatedAt := state.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}

	return c.executeWrite(ctx, func(tx neo4j.ManagedTransaction) error {
		if err := runQuery(ctx, tx, `
			MERGE (b:SourceLeadTimeBelief {key: $key})
			SET b.source = $source,
			    b.signal_domain = $signal_domain,
			    b.language = $language,
			    b.region = $region,
			    b.average_hours = $average_hours,
			    b.observations = $observations,
			    b.score = $score,
			    b.updated_at = $updated_at`,
			map[string]any{
				"key":           state.Key,
				"source":        strings.TrimSpace(state.Source),
				"signal_domain": strings.TrimSpace(state.SignalDomain),
				"language":      strings.TrimSpace(state.Language),
				"region":        strings.TrimSpace(state.Region),
				"average_hours": state.AverageHours,
				"observations":  state.Observations,
				"score":         state.Score,
				"updated_at":    updatedAt,
			},
		); err != nil {
			return err
		}
		if strings.TrimSpace(state.Source) != "" {
			if err := runQuery(ctx, tx, `
				MERGE (src:Source {id: $source_id})
				SET src.name = CASE WHEN src.name IS NULL OR src.name = '' THEN $source ELSE src.name END,
				    src.updated_at = $updated_at`,
				map[string]any{
					"source_id":  strings.ToLower(strings.TrimSpace(state.Source)),
					"source":     strings.TrimSpace(state.Source),
					"updated_at": updatedAt,
				},
			); err != nil {
				return err
			}
			if err := runQuery(ctx, tx, `
				MATCH (src:Source {id: $source_id})
				MATCH (b:SourceLeadTimeBelief {key: $key})
				MERGE (src)-[r:HAS_LEAD_TIME_BELIEF]->(b)
				SET r.updated_at = $updated_at`,
				map[string]any{
					"source_id":  strings.ToLower(strings.TrimSpace(state.Source)),
					"key":        state.Key,
					"updated_at": updatedAt,
				},
			); err != nil {
				return err
			}
		}
		if strings.TrimSpace(state.SignalDomain) != "" {
			if err := runQuery(ctx, tx, `
				MERGE (d:Domain {id: $domain})
				SET d.updated_at = $updated_at`,
				map[string]any{
					"domain":     strings.TrimSpace(state.SignalDomain),
					"updated_at": updatedAt,
				},
			); err != nil {
				return err
			}
			if err := runQuery(ctx, tx, `
				MATCH (b:SourceLeadTimeBelief {key: $key})
				MATCH (d:Domain {id: $domain})
				MERGE (b)-[r:LEADS_IN]->(d)
				SET r.average_hours = $average_hours,
				    r.observations = $observations,
				    r.score = $score,
				    r.updated_at = $updated_at`,
				map[string]any{
					"key":           state.Key,
					"domain":        strings.TrimSpace(state.SignalDomain),
					"average_hours": state.AverageHours,
					"observations":  state.Observations,
					"score":         state.Score,
					"updated_at":    updatedAt,
				},
			); err != nil {
				return err
			}
		}
		return nil
	})
}

func (c *Client) LoadSourceLeadTimeBeliefs(ctx context.Context) ([]*model.SourceLeadTimeBelief, error) {
	if c == nil || c.driver == nil {
		return nil, nil
	}

	session := c.driver.NewSession(ctx, neo4j.SessionConfig{DatabaseName: c.database})
	defer session.Close(ctx)

	result, err := session.ExecuteRead(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		rows, err := tx.Run(ctx, `
			MATCH (b:SourceLeadTimeBelief)
			RETURN b.key AS key,
			       b.source AS source,
			       b.signal_domain AS signal_domain,
			       b.language AS language,
			       b.region AS region,
			       coalesce(b.average_hours, 0.0) AS average_hours,
			       coalesce(b.observations, 0) AS observations,
			       coalesce(b.score, 0.0) AS score,
			       b.updated_at AS updated_at
			ORDER BY b.updated_at DESC`,
			nil,
		)
		if err != nil {
			return nil, err
		}

		beliefs := make([]*model.SourceLeadTimeBelief, 0)
		for rows.Next(ctx) {
			record := rows.Record()
			updatedAt, _ := record.Get("updated_at")
			beliefs = append(beliefs, &model.SourceLeadTimeBelief{
				Key:          strings.TrimSpace(toString(recordValue(record, "key"))),
				Source:       strings.TrimSpace(toString(recordValue(record, "source"))),
				SignalDomain: strings.TrimSpace(toString(recordValue(record, "signal_domain"))),
				Language:     strings.TrimSpace(toString(recordValue(record, "language"))),
				Region:       strings.TrimSpace(toString(recordValue(record, "region"))),
				AverageHours: toFloat(recordValue(record, "average_hours")),
				Observations: toInt(recordValue(record, "observations")),
				Score:        toFloat(recordValue(record, "score")),
				UpdatedAt:    toTime(updatedAt),
			})
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return beliefs, nil
	})
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, nil
	}
	return result.([]*model.SourceLeadTimeBelief), nil
}

func (c *Client) RecordPortfolioSnapshot(ctx context.Context, snapshot *model.PortfolioGraphSnapshot) error {
	if c == nil || c.driver == nil || snapshot == nil || strings.TrimSpace(snapshot.ID) == "" || strings.TrimSpace(snapshot.PortfolioID) == "" {
		return nil
	}

	observedAt := snapshot.ObservedAt
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}

	return c.executeWrite(ctx, func(tx neo4j.ManagedTransaction) error {
		if err := runQuery(ctx, tx, `
			MERGE (p:Portfolio {id: $portfolio_id})
			SET p.session_id = CASE WHEN $session_id = '' THEN p.session_id ELSE $session_id END,
			    p.updated_at = $observed_at`,
			map[string]any{
				"portfolio_id": snapshot.PortfolioID,
				"session_id":   strings.TrimSpace(snapshot.SessionID),
				"observed_at":  observedAt,
			},
		); err != nil {
			return err
		}

		if err := runQuery(ctx, tx, `
			MERGE (snap:PortfolioSnapshot {id: $snapshot_id})
			SET snap.portfolio_id = $portfolio_id,
			    snap.session_id = $session_id,
			    snap.nav = $nav,
			    snap.cash = $cash,
			    snap.gross_exposure = $gross_exposure,
			    snap.net_exposure = $net_exposure,
			    snap.max_drawdown = $max_drawdown,
			    snap.open_positions = $open_positions,
			    snap.observed_at = $observed_at,
			    snap.updated_at = $observed_at`,
			map[string]any{
				"snapshot_id":    snapshot.ID,
				"portfolio_id":   snapshot.PortfolioID,
				"session_id":     strings.TrimSpace(snapshot.SessionID),
				"nav":            snapshot.NAV,
				"cash":           snapshot.Cash,
				"gross_exposure": snapshot.GrossExposure,
				"net_exposure":   snapshot.NetExposure,
				"max_drawdown":   snapshot.MaxDrawdown,
				"open_positions": snapshot.OpenPositions,
				"observed_at":    observedAt,
			},
		); err != nil {
			return err
		}

		if err := runQuery(ctx, tx, `
			MATCH (p:Portfolio {id: $portfolio_id})
			MATCH (snap:PortfolioSnapshot {id: $snapshot_id})
			MERGE (p)-[r:HAS_SNAPSHOT]->(snap)
			SET r.observed_time = $observed_at,
			    r.decision_time = $observed_at`,
			map[string]any{
				"portfolio_id": snapshot.PortfolioID,
				"snapshot_id":  snapshot.ID,
				"observed_at":  observedAt,
			},
		); err != nil {
			return err
		}

		for _, factor := range snapshot.Factors {
			factorID := strings.TrimSpace(factor.Factor)
			if factorID == "" {
				continue
			}
			exposureID := snapshot.ID + "|" + factorID
			if err := runQuery(ctx, tx, `
				MERGE (f:Factor {id: $factor_id})
				SET f.updated_at = $observed_at`,
				map[string]any{
					"factor_id":   factorID,
					"observed_at": observedAt,
				},
			); err != nil {
				return err
			}
			if err := runQuery(ctx, tx, `
				MERGE (exp:PortfolioFactorExposure {id: $exposure_id})
				SET exp.factor = $factor_id,
				    exp.gross = $gross,
				    exp.net = $net,
				    exp.gross_pct_nav = $gross_pct_nav,
				    exp.net_pct_nav = $net_pct_nav,
				    exp.desk_count = $desk_count,
				    exp.observed_at = $observed_at,
				    exp.updated_at = $observed_at`,
				map[string]any{
					"exposure_id":   exposureID,
					"factor_id":     factorID,
					"gross":         factor.Gross,
					"net":           factor.Net,
					"gross_pct_nav": factor.GrossPctNAV,
					"net_pct_nav":   factor.NetPctNAV,
					"desk_count":    factor.DeskCount,
					"observed_at":   observedAt,
				},
			); err != nil {
				return err
			}
			if err := runQuery(ctx, tx, `
				MATCH (snap:PortfolioSnapshot {id: $snapshot_id})
				MATCH (exp:PortfolioFactorExposure {id: $exposure_id})
				MERGE (snap)-[r:HAS_FACTOR_EXPOSURE]->(exp)
				SET r.observed_time = $observed_at,
				    r.decision_time = $observed_at`,
				map[string]any{
					"snapshot_id": snapshot.ID,
					"exposure_id": exposureID,
					"observed_at": observedAt,
				},
			); err != nil {
				return err
			}
			if err := runQuery(ctx, tx, `
				MATCH (exp:PortfolioFactorExposure {id: $exposure_id})
				MATCH (f:Factor {id: $factor_id})
				MERGE (exp)-[r:FOR_FACTOR]->(f)
				SET r.observed_time = $observed_at,
				    r.decision_time = $observed_at`,
				map[string]any{
					"exposure_id": exposureID,
					"factor_id":   factorID,
					"observed_at": observedAt,
				},
			); err != nil {
				return err
			}
			if err := runQuery(ctx, tx, `
				MATCH (snap:PortfolioSnapshot {id: $snapshot_id})
				MATCH (f:Factor {id: $factor_id})
				MERGE (snap)-[r:EXPOSED_TO]->(f)
				SET r.gross = $gross,
				    r.net = $net,
				    r.gross_pct_nav = $gross_pct_nav,
				    r.net_pct_nav = $net_pct_nav,
				    r.desk_count = $desk_count,
				    r.observed_time = $observed_at,
				    r.decision_time = $observed_at`,
				map[string]any{
					"snapshot_id":   snapshot.ID,
					"factor_id":     factorID,
					"gross":         factor.Gross,
					"net":           factor.Net,
					"gross_pct_nav": factor.GrossPctNAV,
					"net_pct_nav":   factor.NetPctNAV,
					"desk_count":    factor.DeskCount,
					"observed_at":   observedAt,
				},
			); err != nil {
				return err
			}

			for _, contribution := range factor.Contributions {
				deskID := strings.TrimSpace(contribution.DeskID)
				if deskID == "" {
					continue
				}
				contributionID := exposureID + "|" + deskID
				if err := runQuery(ctx, tx, `
					MERGE (d:Desk {id: $desk_id})
					SET d.domain = CASE WHEN $domain = '' THEN d.domain ELSE $domain END,
					    d.updated_at = $observed_at`,
					map[string]any{
						"desk_id":     deskID,
						"domain":      strings.TrimSpace(contribution.Domain),
						"observed_at": observedAt,
					},
				); err != nil {
					return err
				}
				if err := runQuery(ctx, tx, `
					MERGE (c:DeskFactorContribution {id: $contribution_id})
					SET c.desk_id = $desk_id,
					    c.domain = $domain,
					    c.gross = $gross,
					    c.net = $net,
					    c.gross_share = $gross_share,
					    c.observed_at = $observed_at,
					    c.updated_at = $observed_at`,
					map[string]any{
						"contribution_id": contributionID,
						"desk_id":         deskID,
						"domain":          strings.TrimSpace(contribution.Domain),
						"gross":           contribution.Gross,
						"net":             contribution.Net,
						"gross_share":     contribution.GrossShare,
						"observed_at":     observedAt,
					},
				); err != nil {
					return err
				}
				if err := runQuery(ctx, tx, `
					MATCH (d:Desk {id: $desk_id})
					MATCH (c:DeskFactorContribution {id: $contribution_id})
					MERGE (d)-[r:CONTRIBUTED_TO]->(c)
					SET r.observed_time = $observed_at,
					    r.decision_time = $observed_at`,
					map[string]any{
						"desk_id":         deskID,
						"contribution_id": contributionID,
						"observed_at":     observedAt,
					},
				); err != nil {
					return err
				}
				if err := runQuery(ctx, tx, `
					MATCH (c:DeskFactorContribution {id: $contribution_id})
					MATCH (exp:PortfolioFactorExposure {id: $exposure_id})
					MERGE (c)-[r:IN_FACTOR_EXPOSURE]->(exp)
					SET r.observed_time = $observed_at,
					    r.decision_time = $observed_at`,
					map[string]any{
						"contribution_id": contributionID,
						"exposure_id":     exposureID,
						"observed_at":     observedAt,
					},
				); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func (c *Client) CouncilVoiceTelemetry(ctx context.Context, domain string) (map[string]model.CouncilVoiceStats, error) {
	stats := make(map[string]model.CouncilVoiceStats)
	if c == nil || c.driver == nil || strings.TrimSpace(domain) == "" {
		return stats, nil
	}

	session := c.driver.NewSession(ctx, neo4j.SessionConfig{DatabaseName: c.database})
	defer session.Close(ctx)

	result, err := session.ExecuteRead(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		rows, err := tx.Run(ctx, `
			MATCH (v:CouncilVoice)-[r:ACCURACY]->(d:Domain {id: $domain})
			RETURN v.id AS id,
			       v.name AS name,
			       coalesce(r.correct_calls, 0) AS correct_calls,
			       coalesce(r.total_calls, 0) AS total_calls,
			       coalesce(r.score_sum, 0.0) AS score_sum`,
			map[string]any{"domain": strings.TrimSpace(domain)})
		if err != nil {
			return nil, err
		}

		telemetry := make(map[string]model.CouncilVoiceStats)
		for rows.Next(ctx) {
			record := rows.Record()
			id, _ := record.Get("id")
			name, _ := record.Get("name")
			correctCalls, _ := record.Get("correct_calls")
			totalCalls, _ := record.Get("total_calls")
			scoreSum, _ := record.Get("score_sum")

			voiceID := strings.TrimSpace(toString(id))
			total := toInt(totalCalls)
			average := 0.0
			if total > 0 {
				average = toFloat(scoreSum) / float64(total)
			}
			sampleConfidence := math.Min(float64(total)/10.0, 1.0)
			blended := average * sampleConfidence
			telemetry[voiceID] = model.CouncilVoiceStats{
				Name:         strings.TrimSpace(toString(name)),
				Weight:       councilVoiceWeight(blended),
				Accuracy:     clampGraphSigned(average),
				CorrectCalls: toInt(correctCalls),
				TotalCalls:   total,
			}
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return telemetry, nil
	})
	if err != nil {
		return nil, err
	}
	typed, _ := result.(map[string]model.CouncilVoiceStats)
	if typed == nil {
		return stats, nil
	}
	return typed, nil
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

func (c *Client) RecordCollaborationInput(ctx context.Context, thesis *model.Thesis, sig signal.Signal, receivingDeskID, receivingDomain string) error {
	if c == nil || c.driver == nil || thesis == nil || strings.TrimSpace(thesis.ID) == "" {
		return nil
	}
	message, ok := model.DecodeColleagueMessage(sig.Raw)
	if !ok || strings.TrimSpace(message.ThreadID) == "" {
		return nil
	}

	now := time.Now().UTC()
	return c.executeWrite(ctx, func(tx neo4j.ManagedTransaction) error {
		if err := runQuery(ctx, tx, `
			MERGE (receiver:Desk {id: $desk_id})
			SET receiver.domain = CASE WHEN $domain = '' THEN receiver.domain ELSE $domain END,
			    receiver.updated_at = $updated_at`,
			map[string]any{
				"desk_id":    strings.TrimSpace(receivingDeskID),
				"domain":     strings.TrimSpace(receivingDomain),
				"updated_at": now,
			},
		); err != nil {
			return err
		}

		if err := runQuery(ctx, tx, `
			MERGE (thread:ConversationThread {id: $thread_id})
			SET thread.updated_at = $updated_at`,
			map[string]any{
				"thread_id":  message.ThreadID,
				"updated_at": now,
			},
		); err != nil {
			return err
		}

		if err := runQuery(ctx, tx, `
			MATCH (t:Thesis {id: $thesis_id})
			MATCH (thread:ConversationThread {id: $thread_id})
			MERGE (t)-[r:PART_OF_THREAD]->(thread)
			SET r.decision_time = $decision_time`,
			map[string]any{
				"thesis_id":     thesis.ID,
				"thread_id":     message.ThreadID,
				"decision_time": now,
			},
		); err != nil {
			return err
		}

		if message.MessageID != "" {
			if err := runQuery(ctx, tx, `
				MERGE (m:ColleagueMessage {id: $message_id})
				SET m.updated_at = $updated_at`,
				map[string]any{
					"message_id": message.MessageID,
					"updated_at": now,
				},
			); err != nil {
				return err
			}
			if err := runQuery(ctx, tx, `
				MATCH (m:ColleagueMessage {id: $message_id})
				MATCH (t:Thesis {id: $thesis_id})
				MERGE (m)-[r:INFORMED]->(t)
				SET r.observed_time = $observed_time,
				    r.decision_time = $decision_time`,
				map[string]any{
					"message_id":    message.MessageID,
					"thesis_id":     thesis.ID,
					"observed_time": normalizeTime(sig.Timestamp, now),
					"decision_time": now,
				},
			); err != nil {
				return err
			}
		}

		if message.OriginDesk != "" {
			if err := runQuery(ctx, tx, `
				MERGE (origin:Desk {id: $origin_desk})
				SET origin.domain = CASE WHEN $origin_domain = '' THEN origin.domain ELSE $origin_domain END,
				    origin.updated_at = $updated_at`,
				map[string]any{
					"origin_desk":   message.OriginDesk,
					"origin_domain": message.OriginDomain,
					"updated_at":    now,
				},
			); err != nil {
				return err
			}
			if err := runQuery(ctx, tx, `
				MATCH (origin:Desk {id: $origin_desk})
				MATCH (receiver:Desk {id: $receiver_desk})
				MERGE (origin)-[r:INFORMED_COLLEAGUE]->(receiver)
				SET r.thread_id = $thread_id,
				    r.last_message_id = $message_id,
				    r.observed_time = $observed_time,
				    r.decision_time = $decision_time`,
				map[string]any{
					"origin_desk":   message.OriginDesk,
					"receiver_desk": strings.TrimSpace(receivingDeskID),
					"thread_id":     message.ThreadID,
					"message_id":    message.MessageID,
					"observed_time": normalizeTime(sig.Timestamp, now),
					"decision_time": now,
				},
			); err != nil {
				return err
			}
		}

		if message.ThesisID != "" {
			if err := runQuery(ctx, tx, `
				MATCH (parent:Thesis {id: $parent_thesis_id})
				MATCH (child:Thesis {id: $child_thesis_id})
				MERGE (parent)-[r:INFORMED]->(child)
				SET r.thread_id = $thread_id,
				    r.message_id = $message_id,
				    r.observed_time = $observed_time,
				    r.decision_time = $decision_time`,
				map[string]any{
					"parent_thesis_id": message.ThesisID,
					"child_thesis_id":  thesis.ID,
					"thread_id":        message.ThreadID,
					"message_id":       message.MessageID,
					"observed_time":    normalizeTime(sig.Timestamp, now),
					"decision_time":    now,
				},
			); err != nil {
				return err
			}
		}
		return nil
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
			"id":                              sig.ID,
			"source":                          sig.Source,
			"type":                            string(sig.Type),
			"category":                        sig.Category,
			"original_text":                   sig.OriginalText,
			"translated":                      sig.Translated,
			"translation_provider":            sig.TranslationProvider,
			"translation_confidence":          sig.TranslationConfidence,
			"urgency":                         sig.Urgency,
			"strength":                        sig.Strength,
			"direction":                       string(sig.Direction),
			"cluster_id":                      sig.ClusterID,
			"narrative_cluster_id":            sig.NarrativeClusterID,
			"content_hash":                    sig.ContentHash,
			"timestamp":                       normalizeTime(sig.Timestamp, now),
			"decision_time":                   now,
			"original_language":               primaryLanguage(sig),
			"corroborating_languages":         sig.CorroboratingLanguages,
			"source_domain":                   evidenceString(meta, func() string { return meta.SourceDomain }),
			"source_owner_group":              evidenceString(meta, func() string { return meta.SourceOwnerGroup }),
			"source_tier":                     evidenceString(meta, func() string { return meta.SourceTier }),
			"source_type":                     evidenceString(meta, func() string { return meta.SourceType }),
			"source_trust":                    evidenceFloat(meta, func() float64 { return meta.SourceTrust }),
			"origin_region":                   evidenceString(meta, func() string { return meta.OriginRegion }),
			"lead_time_average_hours":         evidenceFloat(meta, func() float64 { return meta.LeadTimeAverageHours }),
			"lead_time_observations":          evidenceInt(meta, func() int { return meta.LeadTimeObservations }),
			"lead_time_score":                 evidenceFloat(meta, func() float64 { return meta.LeadTimeScore }),
			"distinct_languages":              evidenceInt(meta, func() int { return meta.DistinctLanguages }),
			"translation_provider_evidence":   evidenceString(meta, func() string { return meta.TranslationProvider }),
			"translation_confidence_evidence": evidenceFloat(meta, func() float64 { return meta.TranslationConfidence }),
			"freshness_status":                evidenceString(meta, func() string { return meta.FreshnessStatus }),
			"freshness_reason":                evidenceString(meta, func() string { return meta.FreshnessReason }),
			"freshness_age_hours":             evidenceFloat(meta, func() float64 { return meta.FreshnessAgeHours }),
			"freshness_window_hours":          evidenceFloat(meta, func() float64 { return meta.FreshnessWindowHours }),
			"distinct_sources":                evidenceInt(meta, func() int { return meta.DistinctSources }),
			"distinct_owner_groups":           evidenceInt(meta, func() int { return meta.DistinctOwnerGroups }),
			"has_primary_source":              evidenceBool(meta, func() bool { return meta.HasPrimarySource }),
			"contradiction_count":             evidenceInt(meta, func() int { return meta.ContradictionCount }),
			"contradiction_severity": evidenceString(meta, func() string {
				return meta.ContradictionSeverity
			}),
			"evidence_score": evidenceFloat(meta, func() float64 { return meta.EvidenceScore }),
			"fact_confidence": evidenceConfidence(meta, func(v *evidence.ConfidenceVector) float64 {
				return v.FactConfidence
			}),
			"novelty_confidence": evidenceConfidence(meta, func(v *evidence.ConfidenceVector) float64 {
				return v.NoveltyConfidence
			}),
			"market_mapping_confidence": evidenceConfidence(meta, func(v *evidence.ConfidenceVector) float64 {
				return v.MarketMappingConfidence
			}),
			"expression_confidence": evidenceConfidence(meta, func(v *evidence.ConfidenceVector) float64 {
				return v.ExpressionConfidence
			}),
			"execution_confidence": evidenceConfidence(meta, func(v *evidence.ConfidenceVector) float64 {
				return v.ExecutionConfidence
			}),
			"evidence_competence_confidence": evidenceConfidence(meta, func(v *evidence.ConfidenceVector) float64 {
				return v.CompetenceConfidence
			}),
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
		if err := c.linkSignalRelations(ctx, tx, sig, now); err != nil {
			return err
		}
		if err := c.linkSignalNarrative(ctx, tx, sig, now); err != nil {
			return err
		}
		if err := c.linkColleagueSignal(ctx, tx, sig, now); err != nil {
			return err
		}
		return c.linkEvidenceAssessment(ctx, tx, "Signal", sig.ID, sig.EvidenceMeta, now)
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
			    t.fact_confidence = $fact_confidence,
			    t.novelty_confidence = $novelty_confidence,
			    t.market_mapping_confidence = $market_mapping_confidence,
			    t.expression_confidence = $expression_confidence,
			    t.execution_confidence = $execution_confidence,
			    t.evidence_competence_confidence = $evidence_competence_confidence,
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
				"fact_confidence": evidenceConfidence(meta, func(v *evidence.ConfidenceVector) float64 {
					return v.FactConfidence
				}),
				"novelty_confidence": evidenceConfidence(meta, func(v *evidence.ConfidenceVector) float64 {
					return v.NoveltyConfidence
				}),
				"market_mapping_confidence": evidenceConfidence(meta, func(v *evidence.ConfidenceVector) float64 {
					return v.MarketMappingConfidence
				}),
				"expression_confidence": evidenceConfidence(meta, func(v *evidence.ConfidenceVector) float64 {
					return v.ExpressionConfidence
				}),
				"execution_confidence": evidenceConfidence(meta, func(v *evidence.ConfidenceVector) float64 {
					return v.ExecutionConfidence
				}),
				"evidence_competence_confidence": evidenceConfidence(meta, func(v *evidence.ConfidenceVector) float64 {
					return v.CompetenceConfidence
				}),
				"source_trust":        evidenceFloat(meta, func() float64 { return meta.SourceTrust }),
				"freshness_status":    evidenceString(meta, func() string { return meta.FreshnessStatus }),
				"contradiction_count": evidenceInt(meta, func() int { return meta.ContradictionCount }),
				"updated_at":          now,
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
		if err := c.linkThesisMarketContext(ctx, tx, thesis, now); err != nil {
			return err
		}
		if err := c.linkThesisQuantMetrics(ctx, tx, thesis, now); err != nil {
			return err
		}
		if err := c.linkCouncilVerdict(ctx, tx, thesis, now); err != nil {
			return err
		}
		if err := c.linkThesisEvidence(ctx, tx, thesis, now); err != nil {
			return err
		}
		return c.linkEvidenceAssessment(ctx, tx, "Thesis", thesis.ID, thesis.EvidenceMeta, now)
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
		if err := c.linkOutcomeAttribution(ctx, tx, outcomeID, outcome, closedAt); err != nil {
			return err
		}
		if err := c.updateCouncilVoiceAccuracy(ctx, tx, thesis, outcome, closedAt); err != nil {
			return err
		}
		return c.linkSurpriseValidation(ctx, tx, thesis, outcomeID, outcome, closedAt)
	})
}

func (c *Client) linkThesisMarketContext(ctx context.Context, tx neo4j.ManagedTransaction, thesis *model.Thesis, now time.Time) error {
	if thesis == nil || thesis.MarketContext == nil {
		return nil
	}

	marketContextID := thesis.ID + ":market-context"
	marketCtx := thesis.MarketContext
	if err := runQuery(ctx, tx, `
		MERGE (m:MarketContext {id: $id})
		SET m.symbol = $symbol,
		    m.sec_type = $sec_type,
		    m.current_price = $current_price,
		    m.return_15m_pct = $return_15m_pct,
		    m.return_1h_pct = $return_1h_pct,
		    m.return_4h_pct = $return_4h_pct,
		    m.signal_age_minutes = $signal_age_minutes,
		    m.consensus_available = $consensus_available,
		    m.actual_eps = $actual_eps,
		    m.estimated_eps = $estimated_eps,
		    m.actual_revenue = $actual_revenue,
		    m.estimated_revenue = $estimated_revenue,
		    m.surprise_magnitude = $surprise_magnitude,
		    m.implied_move_available = $implied_move_available,
		    m.implied_move_pct = $implied_move_pct,
		    m.notes = $notes,
		    m.snapshot_at = $snapshot_at,
		    m.updated_at = $updated_at`,
		map[string]any{
			"id":                     marketContextID,
			"symbol":                 marketCtx.Instrument.Symbol,
			"sec_type":               marketCtx.Instrument.SecType,
			"current_price":          marketCtx.CurrentPrice,
			"return_15m_pct":         marketCtx.Return15mPct,
			"return_1h_pct":          marketCtx.Return1hPct,
			"return_4h_pct":          marketCtx.Return4hPct,
			"signal_age_minutes":     marketCtx.SignalAgeMinutes,
			"consensus_available":    marketCtx.ConsensusAvailable,
			"actual_eps":             marketCtx.ActualEPS,
			"estimated_eps":          marketCtx.EstimatedEPS,
			"actual_revenue":         marketCtx.ActualRevenue,
			"estimated_revenue":      marketCtx.EstimatedRevenue,
			"surprise_magnitude":     marketCtx.SurpriseMagnitude,
			"implied_move_available": marketCtx.ImpliedMoveAvailable,
			"implied_move_pct":       marketCtx.ImpliedMovePct,
			"notes":                  marketCtx.Notes,
			"snapshot_at":            normalizeTime(marketCtx.SnapshotAt, now),
			"updated_at":             now,
		},
	); err != nil {
		return err
	}

	return runQuery(ctx, tx, `
		MATCH (t:Thesis {id: $thesis_id})
		MATCH (m:MarketContext {id: $market_context_id})
		MERGE (t)-[r:ASSESSED_IN]->(m)
		SET r.truth_score = $truth_score,
		    r.novelty_score = $novelty_score,
		    r.priced_in_score = $priced_in_score,
		    r.reaction_gap_score = $reaction_gap_score,
		    r.unmoved_asset_score = $unmoved_asset_score,
		    r.summary = $summary,
		    r.observed_time = $updated_at,
		    r.decision_time = $updated_at`,
		map[string]any{
			"thesis_id":          thesis.ID,
			"market_context_id":  marketContextID,
			"truth_score":        surpriseScore(thesis.SurpriseAssessment, func(a *model.SurpriseAssessment) float64 { return a.TruthScore }),
			"novelty_score":      surpriseScore(thesis.SurpriseAssessment, func(a *model.SurpriseAssessment) float64 { return a.NoveltyScore }),
			"priced_in_score":    surpriseScore(thesis.SurpriseAssessment, func(a *model.SurpriseAssessment) float64 { return a.PricedInScore }),
			"reaction_gap_score": surpriseScore(thesis.SurpriseAssessment, func(a *model.SurpriseAssessment) float64 { return a.ReactionGapScore }),
			"unmoved_asset_score": surpriseScore(thesis.SurpriseAssessment, func(a *model.SurpriseAssessment) float64 {
				return a.UnmovedAssetScore
			}),
			"summary":    surpriseSummary(thesis.SurpriseAssessment),
			"updated_at": now,
		},
	)
}

func (c *Client) linkThesisQuantMetrics(ctx context.Context, tx neo4j.ManagedTransaction, thesis *model.Thesis, now time.Time) error {
	if thesis == nil || thesis.QuantMetrics == nil {
		return nil
	}

	queryID := thesis.ID + ":quant"
	metrics := thesis.QuantMetrics
	if err := runQuery(ctx, tx, `
		MERGE (q:QuantQuery {id: $id})
		SET q.method = $method,
		    q.defined_risk = $defined_risk,
		    q.max_loss = $max_loss,
		    q.max_gain = $max_gain,
		    q.breakeven = $breakeven,
		    q.margin_estimate = $margin_estimate,
		    q.reward_to_risk = $reward_to_risk,
		    q.net_delta_bias = $net_delta_bias,
		    q.warnings = $warnings,
		    q.updated_at = $updated_at`,
		map[string]any{
			"id":              queryID,
			"method":          metrics.Method,
			"defined_risk":    metrics.DefinedRisk,
			"max_loss":        metrics.MaxLoss,
			"max_gain":        metrics.MaxGain,
			"breakeven":       metrics.Breakeven,
			"margin_estimate": metrics.MarginEstimate,
			"reward_to_risk":  metrics.RewardToRisk,
			"net_delta_bias":  metrics.NetDeltaBias,
			"warnings":        metrics.Warnings,
			"updated_at":      now,
		},
	); err != nil {
		return err
	}
	if err := runQuery(ctx, tx, `
		MATCH (t:Thesis {id: $thesis_id})
		MATCH (q:QuantQuery {id: $query_id})
		MERGE (t)-[r:USED_TOOL]->(q)
		SET r.tool = 'quant_toolbox',
		    r.observed_time = $updated_at,
		    r.decision_time = $updated_at`,
		map[string]any{
			"thesis_id":  thesis.ID,
			"query_id":   queryID,
			"updated_at": now,
		},
	); err != nil {
		return err
	}
	if thesis.MarketContext != nil {
		return runQuery(ctx, tx, `
			MATCH (q:QuantQuery {id: $query_id})
			MATCH (m:MarketContext {id: $market_context_id})
			MERGE (q)-[r:RETURNED]->(m)
			SET r.observed_time = $updated_at,
			    r.decision_time = $updated_at`,
			map[string]any{
				"query_id":          queryID,
				"market_context_id": thesis.ID + ":market-context",
				"updated_at":        now,
			},
		)
	}
	return nil
}

func (c *Client) linkCouncilVerdict(ctx context.Context, tx neo4j.ManagedTransaction, thesis *model.Thesis, now time.Time) error {
	if thesis == nil || thesis.CouncilVerdict == nil {
		return nil
	}

	verdictID := thesis.ID + ":council"
	verdict := thesis.CouncilVerdict
	if err := runQuery(ctx, tx, `
		MERGE (v:ThesisVerdict {id: $id})
		SET v.thesis_id = $thesis_id,
		    v.approved = $approved,
		    v.adjusted_size = $adjusted_size,
		    v.adjusted_conviction = $adjusted_conviction,
		    v.weighted_vote_score = $weighted_vote_score,
		    v.total_weight = $total_weight,
		    v.updated_at = $updated_at`,
		map[string]any{
			"id":                  verdictID,
			"thesis_id":           thesis.ID,
			"approved":            verdict.Approved,
			"adjusted_size":       verdict.AdjustedSize,
			"adjusted_conviction": verdict.AdjustedConviction,
			"weighted_vote_score": verdict.WeightedVoteScore,
			"total_weight":        verdict.TotalWeight,
			"updated_at":          now,
		},
	); err != nil {
		return err
	}
	if err := runQuery(ctx, tx, `
		MATCH (t:Thesis {id: $thesis_id})
		MATCH (v:ThesisVerdict {id: $verdict_id})
		MERGE (t)-[r:COUNCIL_REVIEWED]->(v)
		SET r.observed_time = $updated_at,
		    r.decision_time = $updated_at`,
		map[string]any{
			"thesis_id":  thesis.ID,
			"verdict_id": verdictID,
			"updated_at": now,
		},
	); err != nil {
		return err
	}

	for _, voice := range verdict.Voices {
		voiceID := strings.TrimSpace(voice.Name)
		if voiceID == "" {
			continue
		}
		if err := runQuery(ctx, tx, `
			MERGE (cv:CouncilVoice {id: $id})
			SET cv.name = $name,
			    cv.updated_at = $updated_at`,
			map[string]any{
				"id":         voiceID,
				"name":       voice.Name,
				"updated_at": now,
			},
		); err != nil {
			return err
		}
		if err := runQuery(ctx, tx, `
			MATCH (cv:CouncilVoice {id: $voice_id})
			MATCH (v:ThesisVerdict {id: $verdict_id})
			MERGE (cv)-[r:CONTRIBUTED_TO]->(v)
			SET r.perspective = $perspective,
			    r.reasoning = $reasoning,
			    r.recommendation = $recommendation,
			    r.conviction_adjustment = $conviction_adjustment,
			    r.size_adjustment = $size_adjustment,
			    r.weight = $weight,
			    r.historical_accuracy = $historical_accuracy,
			    r.observations = $observations,
			    r.observed_time = $updated_at,
			    r.decision_time = $updated_at`,
			map[string]any{
				"voice_id":              voiceID,
				"verdict_id":            verdictID,
				"perspective":           strings.TrimSpace(voice.Perspective),
				"reasoning":             strings.TrimSpace(voice.Reasoning),
				"recommendation":        string(voice.Recommendation),
				"conviction_adjustment": voice.ConvictionAdjustment,
				"size_adjustment":       voice.SizeAdjustment,
				"weight":                voice.Weight,
				"historical_accuracy":   voice.HistoricalAccuracy,
				"observations":          voice.Observations,
				"updated_at":            now,
			},
		); err != nil {
			return err
		}
	}

	return nil
}

func (c *Client) linkSurpriseValidation(ctx context.Context, tx neo4j.ManagedTransaction, thesis *model.Thesis, outcomeID string, outcome *model.ThesisOutcome, closedAt time.Time) error {
	if thesis == nil || !hasGraphSurpriseAssessment(thesis.SurpriseAssessment) || outcome == nil || outcome.Attribution == nil {
		return nil
	}

	validationScore := computeSurpriseValidation(thesis.SurpriseAssessment, outcome.Attribution)
	return runQuery(ctx, tx, `
		MATCH (t:Thesis {id: $thesis_id})
		MATCH (o:Outcome {id: $outcome_id})
		MERGE (t)-[r:SURPRISE_VALIDATED]->(o)
		SET r.predicted_edge = $predicted_edge,
		    r.actual_edge = $actual_edge,
		    r.validation_score = $validation_score,
		    r.observed_time = $closed_at,
		    r.decision_time = $closed_at`,
		map[string]any{
			"thesis_id":        thesis.ID,
			"outcome_id":       outcomeID,
			"predicted_edge":   surprisePredictedEdge(thesis.SurpriseAssessment),
			"actual_edge":      surpriseActualEdge(outcome.Attribution),
			"validation_score": validationScore,
			"closed_at":        closedAt,
		},
	)
}

func surpriseScore(assessment *model.SurpriseAssessment, getter func(*model.SurpriseAssessment) float64) float64 {
	if !hasGraphSurpriseAssessment(assessment) {
		return 0
	}
	return getter(assessment)
}

func surpriseSummary(assessment *model.SurpriseAssessment) string {
	if !hasGraphSurpriseAssessment(assessment) {
		return ""
	}
	return strings.TrimSpace(assessment.Summary)
}

func surprisePredictedEdge(assessment *model.SurpriseAssessment) float64 {
	if !hasGraphSurpriseAssessment(assessment) {
		return 0
	}
	raw := (assessment.TruthScore +
		assessment.NoveltyScore +
		assessment.ReactionGapScore +
		assessment.UnmovedAssetScore +
		(1 - assessment.PricedInScore)) / 5
	return clampGraphSigned((raw * 2) - 1)
}

func surpriseActualEdge(attr *model.OutcomeAttribution) float64 {
	if attr == nil {
		return 0
	}
	return clampGraphSigned((attr.TruthEdge * 0.7) + (attr.TimingEdge * 0.3))
}

func computeSurpriseValidation(assessment *model.SurpriseAssessment, attr *model.OutcomeAttribution) float64 {
	predicted := surprisePredictedEdge(assessment)
	actual := surpriseActualEdge(attr)
	alignment := 1 - (math.Abs(predicted-actual) / 2)
	if predicted*actual < 0 {
		alignment = -alignment
	}
	return clampGraphSigned(alignment)
}

func clampGraphSigned(value float64) float64 {
	if value > 1 {
		return 1
	}
	if value < -1 {
		return -1
	}
	return value
}

func hasGraphSurpriseAssessment(assessment *model.SurpriseAssessment) bool {
	if assessment == nil {
		return false
	}
	return assessment.TruthScore != 0 ||
		assessment.NoveltyScore != 0 ||
		assessment.PricedInScore != 0 ||
		assessment.ReactionGapScore != 0 ||
		assessment.UnmovedAssetScore != 0 ||
		strings.TrimSpace(assessment.Summary) != ""
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

func (c *Client) updateCouncilVoiceAccuracy(ctx context.Context, tx neo4j.ManagedTransaction, thesis *model.Thesis, outcome *model.ThesisOutcome, closedAt time.Time) error {
	if thesis == nil || thesis.CouncilVerdict == nil || len(thesis.CouncilVerdict.Voices) == 0 || outcome == nil || outcome.Attribution == nil {
		return nil
	}

	domainID := strings.TrimSpace(thesis.Domain)
	if domainID == "" {
		domainID = "unknown"
	}
	if err := runQuery(ctx, tx, `
		MERGE (d:Domain {id: $id})
		SET d.name = $name,
		    d.updated_at = $updated_at`,
		map[string]any{
			"id":         domainID,
			"name":       domainID,
			"updated_at": closedAt,
		},
	); err != nil {
		return err
	}

	for _, voice := range thesis.CouncilVerdict.Voices {
		voiceID := strings.TrimSpace(voice.Name)
		if voiceID == "" {
			continue
		}
		if err := runQuery(ctx, tx, `
			MERGE (cv:CouncilVoice {id: $id})
			SET cv.name = $name,
			    cv.updated_at = $updated_at`,
			map[string]any{
				"id":         voiceID,
				"name":       voice.Name,
				"updated_at": closedAt,
			},
		); err != nil {
			return err
		}

		score, counted := councilVoiceContributionScore(voice, outcome.Attribution)
		if !counted {
			continue
		}
		correctIncrement := 0
		if score > 0 {
			correctIncrement = 1
		}
		if err := runQuery(ctx, tx, `
			MATCH (cv:CouncilVoice {id: $voice_id})
			MATCH (d:Domain {id: $domain_id})
			MERGE (cv)-[r:ACCURACY]->(d)
			ON CREATE SET r.correct_calls = 0,
			              r.total_calls = 0,
			              r.score_sum = 0.0
			SET r.correct_calls = coalesce(r.correct_calls, 0) + $correct_increment,
			    r.total_calls = coalesce(r.total_calls, 0) + 1,
			    r.score_sum = coalesce(r.score_sum, 0.0) + $score,
			    r.last_score = $score,
			    r.last_recommendation = $recommendation,
			    r.last_weight = $weight,
			    r.last_observed_at = $updated_at,
			    r.updated_at = $updated_at`,
			map[string]any{
				"voice_id":          voiceID,
				"domain_id":         domainID,
				"correct_increment": correctIncrement,
				"score":             score,
				"recommendation":    string(voice.Recommendation),
				"weight":            voice.Weight,
				"updated_at":        closedAt,
			},
		); err != nil {
			return err
		}
	}

	return nil
}

func councilVoiceContributionScore(voice model.CouncilVoiceContribution, attr *model.OutcomeAttribution) (float64, bool) {
	if attr == nil {
		return 0, false
	}

	orientation := councilVoiceOrientation(voice)
	if orientation == 0 {
		return 0, false
	}

	outcomeScore := clampGraphSigned(
		(attr.TruthEdge * 0.40) +
			(attr.TimingEdge * 0.20) +
			(attr.ExpressionEdge * 0.25) +
			(attr.ExecutionEdge * 0.15),
	)
	if math.Abs(outcomeScore) < 0.05 {
		return 0, false
	}

	intensity := math.Max(math.Abs(voice.ConvictionAdjustment), math.Abs(voice.SizeAdjustment-1))
	if intensity < 0.05 {
		intensity = 0.05
	}
	intensity = math.Min(intensity*4, 1)
	score := outcomeScore * float64(orientation) * intensity
	return clampGraphSigned(score), true
}

func councilVoiceOrientation(voice model.CouncilVoiceContribution) int {
	switch normalizeCouncilRecommendation(voice.Recommendation) {
	case model.CouncilApprove:
		return 1
	case model.CouncilReject, model.CouncilAbstain:
		return -1
	}
	if voice.ConvictionAdjustment >= 0.05 {
		return 1
	}
	if voice.ConvictionAdjustment <= -0.05 || voice.SizeAdjustment < 0.9 {
		return -1
	}
	return 0
}

func normalizeCouncilRecommendation(value model.CouncilRecommendation) model.CouncilRecommendation {
	switch value {
	case model.CouncilApprove, model.CouncilReject, model.CouncilAbstain:
		return value
	default:
		return model.CouncilAbstain
	}
}

func councilVoiceWeight(score float64) float64 {
	return math.Max(0.75, math.Min(1.35, 1+(score*0.35)))
}

func recordValue(record *neo4j.Record, key string) any {
	if record == nil {
		return nil
	}
	value, _ := record.Get(key)
	return value
}

func toString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	default:
		return fmt.Sprintf("%v", value)
	}
}

func toInt(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case int32:
		return int(typed)
	case float64:
		return int(typed)
	case float32:
		return int(typed)
	default:
		return 0
	}
}

func toFloat(value any) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case float32:
		return float64(typed)
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	case int32:
		return float64(typed)
	default:
		return 0
	}
}

func toTime(value any) time.Time {
	switch typed := value.(type) {
	case time.Time:
		return typed.UTC()
	default:
		return time.Time{}
	}
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
