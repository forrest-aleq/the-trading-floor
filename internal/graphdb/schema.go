package graphdb

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hnic/trading-floor/pkg/model"
	"github.com/hnic/trading-floor/pkg/signal"
	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

func (c *Client) bootstrapSchema(ctx context.Context) error {
	return c.executeWrite(ctx, func(tx neo4j.ManagedTransaction) error {
		for _, statement := range schemaStatements() {
			if err := runQuery(ctx, tx, statement, nil); err != nil {
				return fmt.Errorf("bootstrap graph schema: %w", err)
			}
		}
		return nil
	})
}

func schemaStatements() []string {
	return []string{
		"CREATE CONSTRAINT signal_id IF NOT EXISTS FOR (n:Signal) REQUIRE n.id IS UNIQUE",
		"CREATE CONSTRAINT thesis_id IF NOT EXISTS FOR (n:Thesis) REQUIRE n.id IS UNIQUE",
		"CREATE CONSTRAINT position_id IF NOT EXISTS FOR (n:Position) REQUIRE n.id IS UNIQUE",
		"CREATE CONSTRAINT outcome_id IF NOT EXISTS FOR (n:Outcome) REQUIRE n.id IS UNIQUE",
		"CREATE CONSTRAINT desk_id IF NOT EXISTS FOR (n:Desk) REQUIRE n.id IS UNIQUE",
		"CREATE CONSTRAINT source_id IF NOT EXISTS FOR (n:Source) REQUIRE n.id IS UNIQUE",
		"CREATE CONSTRAINT owner_group_id IF NOT EXISTS FOR (n:OwnerGroup) REQUIRE n.id IS UNIQUE",
		"CREATE CONSTRAINT entity_id IF NOT EXISTS FOR (n:Entity) REQUIRE n.id IS UNIQUE",
		"CREATE CONSTRAINT entity_alias_id IF NOT EXISTS FOR (n:EntityAlias) REQUIRE n.id IS UNIQUE",
		"CREATE CONSTRAINT instrument_id IF NOT EXISTS FOR (n:Instrument) REQUIRE n.id IS UNIQUE",
		"CREATE CONSTRAINT venue_id IF NOT EXISTS FOR (n:Venue) REQUIRE n.id IS UNIQUE",
		"CREATE CONSTRAINT language_code IF NOT EXISTS FOR (n:Language) REQUIRE n.code IS UNIQUE",
		"CREATE CONSTRAINT structure_class_id IF NOT EXISTS FOR (n:StructureClass) REQUIRE n.id IS UNIQUE",
		"CREATE CONSTRAINT evidence_id IF NOT EXISTS FOR (n:Evidence) REQUIRE n.id IS UNIQUE",
		"CREATE CONSTRAINT antipf_id IF NOT EXISTS FOR (n:AntiPortfolioDecision) REQUIRE n.id IS UNIQUE",
		"CREATE CONSTRAINT market_context_id IF NOT EXISTS FOR (n:MarketContext) REQUIRE n.id IS UNIQUE",
		"CREATE CONSTRAINT factor_id IF NOT EXISTS FOR (n:Factor) REQUIRE n.id IS UNIQUE",
		"CREATE CONSTRAINT region_id IF NOT EXISTS FOR (n:Region) REQUIRE n.id IS UNIQUE",
		"CREATE CONSTRAINT narrative_cluster_id IF NOT EXISTS FOR (n:NarrativeCluster) REQUIRE n.id IS UNIQUE",
		"CREATE INDEX source_name IF NOT EXISTS FOR (n:Source) ON (n.name)",
		"CREATE INDEX entity_name IF NOT EXISTS FOR (n:Entity) ON (n.name)",
		"CREATE INDEX thesis_status IF NOT EXISTS FOR (n:Thesis) ON (n.status)",
	}
}

func (c *Client) linkSignalLineage(ctx context.Context, tx neo4j.ManagedTransaction, sig signal.Signal, now time.Time) error {
	if strings.TrimSpace(sig.Source) == "" {
		return nil
	}

	sourceID := strings.ToLower(strings.TrimSpace(sig.Source))
	meta := sig.EvidenceMeta
	if err := runQuery(ctx, tx, `
		MERGE (src:Source {id: $id})
		SET src.name = $name,
		    src.domain = $domain,
		    src.tier = $tier,
		    src.type = $type,
		    src.trust = $trust,
		    src.updated_at = $updated_at`,
		map[string]any{
			"id":         sourceID,
			"name":       sig.Source,
			"domain":     evidenceString(meta, func() string { return meta.SourceDomain }),
			"tier":       evidenceString(meta, func() string { return meta.SourceTier }),
			"type":       evidenceString(meta, func() string { return meta.SourceType }),
			"trust":      evidenceFloat(meta, func() float64 { return meta.SourceTrust }),
			"updated_at": now,
		},
	); err != nil {
		return err
	}

	if err := runQuery(ctx, tx, `
		MATCH (s:Signal {id: $signal_id})
		MATCH (src:Source {id: $source_id})
		MERGE (s)-[r:SOURCED_FROM]->(src)
		SET r.event_time = $event_time,
		    r.observed_time = $observed_time,
		    r.decision_time = $decision_time`,
		map[string]any{
			"signal_id":     sig.ID,
			"source_id":     sourceID,
			"event_time":    normalizeTime(sig.Timestamp, now),
			"observed_time": normalizeTime(sig.Timestamp, now),
			"decision_time": now,
		},
	); err != nil {
		return err
	}

	ownerGroup := evidenceString(meta, func() string { return meta.SourceOwnerGroup })
	if ownerGroup != "" {
		if err := runQuery(ctx, tx, `
			MERGE (o:OwnerGroup {id: $id})
			SET o.name = $name,
			    o.updated_at = $updated_at`,
			map[string]any{
				"id":         ownerGroup,
				"name":       ownerGroup,
				"updated_at": now,
			},
		); err != nil {
			return err
		}
		if err := runQuery(ctx, tx, `
			MATCH (src:Source {id: $source_id})
			MATCH (o:OwnerGroup {id: $owner_group})
			MERGE (src)-[r:OWNED_BY]->(o)
			SET r.observed_time = $observed_time,
			    r.decision_time = $decision_time`,
			map[string]any{
				"source_id":     sourceID,
				"owner_group":   ownerGroup,
				"observed_time": normalizeTime(sig.Timestamp, now),
				"decision_time": now,
			},
		); err != nil {
			return err
		}
	}

	lang := primaryLanguage(sig)
	if lang == "" {
		return nil
	}
	if err := runQuery(ctx, tx, `
		MERGE (l:Language {code: $code})
		SET l.updated_at = $updated_at`,
		map[string]any{
			"code":       lang,
			"updated_at": now,
		},
	); err != nil {
		return err
	}
	return runQuery(ctx, tx, `
		MATCH (s:Signal {id: $signal_id})
		MATCH (l:Language {code: $language})
		MERGE (s)-[r:ORIGINAL_LANGUAGE]->(l)
		SET r.observed_time = $observed_time,
		    r.decision_time = $decision_time`,
		map[string]any{
			"signal_id":     sig.ID,
			"language":      lang,
			"observed_time": normalizeTime(sig.Timestamp, now),
			"decision_time": now,
		},
	)
}

func (c *Client) linkSignalEntities(ctx context.Context, tx neo4j.ManagedTransaction, sig signal.Signal, now time.Time) error {
	for _, entity := range sig.Entities {
		name := strings.TrimSpace(entity.Name)
		if name == "" {
			continue
		}
		entityID := entityNodeID(entity)
		if err := runQuery(ctx, tx, `
			MERGE (e:Entity {id: $id})
			SET e.name = $name,
			    e.type = $type,
			    e.external_id = $external_id,
			    e.updated_at = $updated_at`,
			map[string]any{
				"id":          entityID,
				"name":        name,
				"type":        strings.TrimSpace(entity.Type),
				"external_id": strings.TrimSpace(entity.ID),
				"updated_at":  now,
			},
		); err != nil {
			return err
		}
		if err := runQuery(ctx, tx, `
			MATCH (s:Signal {id: $signal_id})
			MATCH (e:Entity {id: $entity_id})
			MERGE (s)-[r:MENTIONS]->(e)
			SET r.event_time = $event_time,
			    r.observed_time = $observed_time,
			    r.decision_time = $decision_time`,
			map[string]any{
				"signal_id":     sig.ID,
				"entity_id":     entityID,
				"event_time":    normalizeTime(sig.Timestamp, now),
				"observed_time": normalizeTime(sig.Timestamp, now),
				"decision_time": now,
			},
		); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) linkSignalRelations(ctx context.Context, tx neo4j.ManagedTransaction, sig signal.Signal, now time.Time) error {
	for _, relatedID := range sig.RelatedSignalIDs {
		relatedID = strings.TrimSpace(relatedID)
		if relatedID == "" || relatedID == sig.ID {
			continue
		}
		if err := runQuery(ctx, tx, `
			MERGE (other:Signal {id: $related_id})
			ON CREATE SET other.created_at = $updated_at`,
			map[string]any{
				"related_id": relatedID,
				"updated_at": now,
			},
		); err != nil {
			return err
		}
		if err := runQuery(ctx, tx, `
			MATCH (s:Signal {id: $signal_id})
			MATCH (other:Signal {id: $related_id})
			MERGE (s)-[r:RELATED_TO]->(other)
			SET r.observed_time = $observed_time,
			    r.decision_time = $decision_time`,
			map[string]any{
				"signal_id":     sig.ID,
				"related_id":    relatedID,
				"observed_time": normalizeTime(sig.Timestamp, now),
				"decision_time": now,
			},
		); err != nil {
			return err
		}
	}

	if sig.EvidenceMeta == nil {
		return nil
	}
	for _, conflictingID := range sig.EvidenceMeta.ConflictingSignalIDs {
		conflictingID = strings.TrimSpace(conflictingID)
		if conflictingID == "" || conflictingID == sig.ID {
			continue
		}
		if err := runQuery(ctx, tx, `
			MERGE (other:Signal {id: $conflicting_id})
			ON CREATE SET other.created_at = $updated_at`,
			map[string]any{
				"conflicting_id": conflictingID,
				"updated_at":     now,
			},
		); err != nil {
			return err
		}
		if err := runQuery(ctx, tx, `
			MATCH (s:Signal {id: $signal_id})
			MATCH (other:Signal {id: $conflicting_id})
			MERGE (s)-[r:CONTRADICTED_BY]->(other)
			SET r.observed_time = $observed_time,
			    r.decision_time = $decision_time,
			    r.severity = $severity`,
			map[string]any{
				"signal_id":      sig.ID,
				"conflicting_id": conflictingID,
				"observed_time":  normalizeTime(sig.Timestamp, now),
				"decision_time":  now,
				"severity":       sig.EvidenceMeta.ContradictionSeverity,
			},
		); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) linkThesisEvidence(ctx context.Context, tx neo4j.ManagedTransaction, thesis *model.Thesis, now time.Time) error {
	for i, item := range thesis.Evidence {
		evidenceID := fmt.Sprintf("%s:%d", thesis.ID, i)
		if err := runQuery(ctx, tx, `
			MERGE (e:Evidence {id: $id})
			SET e.source = $source,
			    e.content = $content,
			    e.weight = $weight,
			    e.signal_id = $signal_id,
			    e.updated_at = $updated_at`,
			map[string]any{
				"id":         evidenceID,
				"source":     item.Source,
				"content":    item.Content,
				"weight":     item.Weight,
				"signal_id":  item.SignalID,
				"updated_at": now,
			},
		); err != nil {
			return err
		}
		if err := runQuery(ctx, tx, `
			MATCH (t:Thesis {id: $thesis_id})
			MATCH (e:Evidence {id: $evidence_id})
			MERGE (t)-[r:FORMED_FROM]->(e)
			SET r.observed_time = $observed_time,
			    r.decision_time = $decision_time`,
			map[string]any{
				"thesis_id":     thesis.ID,
				"evidence_id":   evidenceID,
				"observed_time": normalizeTime(thesis.CreatedAt, now),
				"decision_time": now,
			},
		); err != nil {
			return err
		}
		if strings.TrimSpace(item.SignalID) == "" {
			continue
		}
		if err := runQuery(ctx, tx, `
			MERGE (s:Signal {id: $signal_id})
			ON CREATE SET s.created_at = $updated_at`,
			map[string]any{
				"signal_id":  item.SignalID,
				"updated_at": now,
			},
		); err != nil {
			return err
		}
		if err := runQuery(ctx, tx, `
			MATCH (e:Evidence {id: $evidence_id})
			MATCH (s:Signal {id: $signal_id})
			MERGE (e)-[r:SOURCED_FROM]->(s)
			SET r.observed_time = $observed_time,
			    r.decision_time = $decision_time`,
			map[string]any{
				"evidence_id":   evidenceID,
				"signal_id":     item.SignalID,
				"observed_time": normalizeTime(thesis.CreatedAt, now),
				"decision_time": now,
			},
		); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) linkTradeInstruments(ctx context.Context, tx neo4j.ManagedTransaction, ownerLabel, ownerID string, instruments []model.Instrument, relType string, now time.Time) error {
	for _, instrument := range instruments {
		if strings.TrimSpace(instrument.Symbol) == "" {
			continue
		}
		if err := upsertInstrument(ctx, tx, instrument, now); err != nil {
			return err
		}
		query := fmt.Sprintf(`
			MATCH (owner:%s {id: $owner_id})
			MATCH (i:Instrument {id: $instrument_id})
			MERGE (owner)-[r:%s]->(i)
			SET r.observed_time = $observed_time,
			    r.decision_time = $decision_time`, ownerLabel, relType)
		if err := runQuery(ctx, tx, query, map[string]any{
			"owner_id":      ownerID,
			"instrument_id": instrumentNodeID(instrument),
			"observed_time": now,
			"decision_time": now,
		}); err != nil {
			return err
		}
	}
	return nil
}

func upsertInstrument(ctx context.Context, tx neo4j.ManagedTransaction, instrument model.Instrument, now time.Time) error {
	if strings.TrimSpace(instrument.Symbol) == "" {
		return nil
	}
	if err := runQuery(ctx, tx, `
		MERGE (i:Instrument {id: $id})
		SET i.symbol = $symbol,
		    i.sec_type = $sec_type,
		    i.exchange = $exchange,
		    i.currency = $currency,
		    i.expiry = $expiry,
		    i.strike = $strike,
		    i.right = $right,
		    i.multiplier = $multiplier,
		    i.con_id = $con_id,
		    i.updated_at = $updated_at`,
		map[string]any{
			"id":         instrumentNodeID(instrument),
			"symbol":     instrument.Symbol,
			"sec_type":   instrument.SecType,
			"exchange":   instrument.Exchange,
			"currency":   instrument.Currency,
			"expiry":     instrument.Expiry,
			"strike":     instrument.Strike,
			"right":      instrument.Right,
			"multiplier": instrument.Multiplier,
			"con_id":     instrument.ConID,
			"updated_at": now,
		},
	); err != nil {
		return err
	}
	if strings.TrimSpace(instrument.Exchange) == "" {
		return nil
	}
	if err := runQuery(ctx, tx, `
		MERGE (v:Venue {id: $id})
		SET v.name = $name,
		    v.updated_at = $updated_at`,
		map[string]any{
			"id":         strings.ToUpper(strings.TrimSpace(instrument.Exchange)),
			"name":       strings.ToUpper(strings.TrimSpace(instrument.Exchange)),
			"updated_at": now,
		},
	); err != nil {
		return err
	}
	return runQuery(ctx, tx, `
		MATCH (i:Instrument {id: $instrument_id})
		MATCH (v:Venue {id: $venue_id})
		MERGE (i)-[r:TRADED_ON]->(v)
		SET r.observed_time = $observed_time,
		    r.decision_time = $decision_time`,
		map[string]any{
			"instrument_id": instrumentNodeID(instrument),
			"venue_id":      strings.ToUpper(strings.TrimSpace(instrument.Exchange)),
			"observed_time": now,
			"decision_time": now,
		},
	)
}

func instrumentNodeID(instrument model.Instrument) string {
	return instrument.Key()
}

func entityNodeID(entity signal.Entity) string {
	typ := strings.ToLower(strings.TrimSpace(entity.Type))
	name := strings.ToUpper(strings.Join(strings.Fields(strings.TrimSpace(entity.Name)), " "))
	if typ == "" {
		typ = "unknown"
	}
	return typ + ":" + name
}

func primaryLanguage(sig signal.Signal) string {
	if len(sig.Languages) > 0 && strings.TrimSpace(sig.Languages[0]) != "" {
		return strings.TrimSpace(strings.ToLower(sig.Languages[0]))
	}
	return "en"
}

func normalizeTime(value time.Time, fallback time.Time) time.Time {
	if value.IsZero() {
		return fallback
	}
	return value.UTC()
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func evidenceString(meta interface{ Present() bool }, getter func() string) string {
	if meta == nil || !meta.Present() {
		return ""
	}
	return getter()
}

func evidenceFloat(meta interface{ Present() bool }, getter func() float64) float64 {
	if meta == nil || !meta.Present() {
		return 0
	}
	return getter()
}

func evidenceInt(meta interface{ Present() bool }, getter func() int) int {
	if meta == nil || !meta.Present() {
		return 0
	}
	return getter()
}

func evidenceBool(meta interface{ Present() bool }, getter func() bool) bool {
	if meta == nil || !meta.Present() {
		return false
	}
	return getter()
}
