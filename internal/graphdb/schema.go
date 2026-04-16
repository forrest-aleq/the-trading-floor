package graphdb

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hnic/trading-floor/internal/entityresolve"
	"github.com/hnic/trading-floor/pkg/evidence"
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
		"CREATE CONSTRAINT attribution_id IF NOT EXISTS FOR (n:Attribution) REQUIRE n.id IS UNIQUE",
		"CREATE CONSTRAINT thesis_verdict_id IF NOT EXISTS FOR (n:ThesisVerdict) REQUIRE n.id IS UNIQUE",
		"CREATE CONSTRAINT desk_id IF NOT EXISTS FOR (n:Desk) REQUIRE n.id IS UNIQUE",
		"CREATE CONSTRAINT council_voice_id IF NOT EXISTS FOR (n:CouncilVoice) REQUIRE n.id IS UNIQUE",
		"CREATE CONSTRAINT competence_state_key IF NOT EXISTS FOR (n:CompetenceState) REQUIRE n.key IS UNIQUE",
		"CREATE CONSTRAINT quant_query_id IF NOT EXISTS FOR (n:QuantQuery) REQUIRE n.id IS UNIQUE",
		"CREATE CONSTRAINT source_id IF NOT EXISTS FOR (n:Source) REQUIRE n.id IS UNIQUE",
		"CREATE CONSTRAINT owner_group_id IF NOT EXISTS FOR (n:OwnerGroup) REQUIRE n.id IS UNIQUE",
		"CREATE CONSTRAINT entity_id IF NOT EXISTS FOR (n:Entity) REQUIRE n.id IS UNIQUE",
		"CREATE CONSTRAINT entity_alias_id IF NOT EXISTS FOR (n:EntityAlias) REQUIRE n.id IS UNIQUE",
		"CREATE CONSTRAINT instrument_id IF NOT EXISTS FOR (n:Instrument) REQUIRE n.id IS UNIQUE",
		"CREATE CONSTRAINT venue_id IF NOT EXISTS FOR (n:Venue) REQUIRE n.id IS UNIQUE",
		"CREATE CONSTRAINT language_code IF NOT EXISTS FOR (n:Language) REQUIRE n.code IS UNIQUE",
		"CREATE CONSTRAINT structure_class_id IF NOT EXISTS FOR (n:StructureClass) REQUIRE n.id IS UNIQUE",
		"CREATE CONSTRAINT evidence_id IF NOT EXISTS FOR (n:Evidence) REQUIRE n.id IS UNIQUE",
		"CREATE CONSTRAINT evidence_assessment_id IF NOT EXISTS FOR (n:EvidenceAssessment) REQUIRE n.id IS UNIQUE",
		"CREATE CONSTRAINT antipf_id IF NOT EXISTS FOR (n:AntiPortfolioDecision) REQUIRE n.id IS UNIQUE",
		"CREATE CONSTRAINT market_context_id IF NOT EXISTS FOR (n:MarketContext) REQUIRE n.id IS UNIQUE",
		"CREATE CONSTRAINT factor_id IF NOT EXISTS FOR (n:Factor) REQUIRE n.id IS UNIQUE",
		"CREATE CONSTRAINT domain_id IF NOT EXISTS FOR (n:Domain) REQUIRE n.id IS UNIQUE",
		"CREATE CONSTRAINT region_id IF NOT EXISTS FOR (n:Region) REQUIRE n.id IS UNIQUE",
		"CREATE CONSTRAINT narrative_cluster_id IF NOT EXISTS FOR (n:NarrativeCluster) REQUIRE n.id IS UNIQUE",
		"CREATE CONSTRAINT conversation_thread_id IF NOT EXISTS FOR (n:ConversationThread) REQUIRE n.id IS UNIQUE",
		"CREATE CONSTRAINT colleague_message_id IF NOT EXISTS FOR (n:ColleagueMessage) REQUIRE n.id IS UNIQUE",
		"CREATE CONSTRAINT desk_relationship_belief_key IF NOT EXISTS FOR (n:DeskRelationshipBelief) REQUIRE n.key IS UNIQUE",
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

	originRegion := evidenceString(meta, func() string { return meta.OriginRegion })
	if originRegion != "" {
		if err := runQuery(ctx, tx, `
			MERGE (r:Region {id: $id})
			SET r.name = $name,
			    r.updated_at = $updated_at`,
			map[string]any{
				"id":         originRegion,
				"name":       originRegion,
				"updated_at": now,
			},
		); err != nil {
			return err
		}
		if err := runQuery(ctx, tx, `
			MATCH (s:Signal {id: $signal_id})
			MATCH (r:Region {id: $region})
			MERGE (s)-[rel:ORIGINATED_IN]->(r)
			SET rel.event_time = $event_time,
			    rel.observed_time = $observed_time,
			    rel.decision_time = $decision_time`,
			map[string]any{
				"signal_id":     sig.ID,
				"region":        originRegion,
				"event_time":    normalizeTime(sig.Timestamp, now),
				"observed_time": normalizeTime(sig.Timestamp, now),
				"decision_time": now,
			},
		); err != nil {
			return err
		}
		if err := runQuery(ctx, tx, `
			MATCH (src:Source {id: $source_id})
			MATCH (r:Region {id: $region})
			MERGE (src)-[rel:OPERATES_IN]->(r)
			SET rel.observed_time = $observed_time,
			    rel.decision_time = $decision_time`,
			map[string]any{
				"source_id":     sourceID,
				"region":        originRegion,
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
	if err := runQuery(ctx, tx, `
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
	); err != nil {
		return err
	}

	if strings.TrimSpace(sig.Translated) == "" {
		return nil
	}
	if err := runQuery(ctx, tx, `
		MERGE (l:Language {code: 'en'})
		SET l.updated_at = $updated_at`,
		map[string]any{
			"updated_at": now,
		},
	); err != nil {
		return err
	}
	return runQuery(ctx, tx, `
		MATCH (s:Signal {id: $signal_id})
		MATCH (l:Language {code: 'en'})
		MERGE (s)-[r:TRANSLATED_TO]->(l)
		SET r.provider = $provider,
		    r.confidence = $confidence,
		    r.observed_time = $observed_time,
		    r.decision_time = $decision_time`,
		map[string]any{
			"signal_id":     sig.ID,
			"provider":      strings.TrimSpace(sig.TranslationProvider),
			"confidence":    sig.TranslationConfidence,
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
		resolved := entityresolve.Resolve(entity, primaryLanguage(sig))
		entityID := resolved.CanonicalID
		if err := runQuery(ctx, tx, `
			MERGE (e:Entity {id: $id})
			SET e.name = $name,
			    e.type = $type,
			    e.external_id = $external_id,
			    e.language = $language,
			    e.updated_at = $updated_at`,
			map[string]any{
				"id":          entityID,
				"name":        firstNonEmpty(resolved.CanonicalName, name),
				"type":        firstNonEmpty(resolved.Type, strings.TrimSpace(entity.Type)),
				"external_id": strings.TrimSpace(entity.ID),
				"language":    resolved.Language,
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
		aliasID := entityAliasID(entity, sig)
		if aliasID == "" {
			continue
		}
		if err := runQuery(ctx, tx, `
			MERGE (a:EntityAlias {id: $id})
			SET a.name = $name,
			    a.language = $language,
			    a.script = $script,
			    a.confidence = $confidence,
			    a.updated_at = $updated_at`,
			map[string]any{
				"id":         aliasID,
				"name":       name,
				"language":   resolved.Language,
				"script":     resolved.Script,
				"confidence": resolved.Confidence,
				"updated_at": now,
			},
		); err != nil {
			return err
		}
		if err := runQuery(ctx, tx, `
			MATCH (e:Entity {id: $entity_id})
			MATCH (a:EntityAlias {id: $alias_id})
			MERGE (e)-[r:ALIAS]->(a)
			SET r.language = $language,
			    r.script = $script,
			    r.confidence = $confidence,
			    r.observed_time = $observed_time,
			    r.decision_time = $decision_time`,
			map[string]any{
				"entity_id":     entityID,
				"alias_id":      aliasID,
				"language":      resolved.Language,
				"script":        resolved.Script,
				"confidence":    resolved.Confidence,
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

func (c *Client) linkSignalNarrative(ctx context.Context, tx neo4j.ManagedTransaction, sig signal.Signal, now time.Time) error {
	if strings.TrimSpace(sig.NarrativeClusterID) == "" {
		return nil
	}

	languages := append([]string(nil), sig.CorroboratingLanguages...)
	if lang := primaryLanguage(sig); lang != "" && !containsString(languages, lang) {
		languages = append(languages, lang)
	}

	if err := runQuery(ctx, tx, `
		MERGE (n:NarrativeCluster {id: $id})
		ON CREATE SET n.created_at = $created_at
		SET n.category = $category,
		    n.representative_text = CASE
		        WHEN coalesce(n.representative_text, '') = '' THEN $representative_text
		        ELSE n.representative_text
		    END,
		    n.languages = $languages,
		    n.updated_at = $updated_at`,
		map[string]any{
			"id":                  sig.NarrativeClusterID,
			"category":            sig.Category,
			"representative_text": strings.TrimSpace(sig.Translated),
			"languages":           languages,
			"created_at":          normalizeTime(sig.Timestamp, now),
			"updated_at":          now,
		},
	); err != nil {
		return err
	}

	return runQuery(ctx, tx, `
		MATCH (s:Signal {id: $signal_id})
		MATCH (n:NarrativeCluster {id: $narrative_id})
		MERGE (s)-[r:BELONGS_TO_NARRATIVE]->(n)
		SET r.event_time = $event_time,
		    r.observed_time = $observed_time,
		    r.decision_time = $decision_time`,
		map[string]any{
			"signal_id":     sig.ID,
			"narrative_id":  sig.NarrativeClusterID,
			"event_time":    normalizeTime(sig.Timestamp, now),
			"observed_time": normalizeTime(sig.Timestamp, now),
			"decision_time": now,
		},
	)
}

func (c *Client) linkColleagueSignal(ctx context.Context, tx neo4j.ManagedTransaction, sig signal.Signal, now time.Time) error {
	message, ok := model.DecodeColleagueMessage(sig.Raw)
	if !ok || strings.TrimSpace(message.ThreadID) == "" || strings.TrimSpace(message.MessageID) == "" {
		return nil
	}

	if err := runQuery(ctx, tx, `
		MERGE (thread:ConversationThread {id: $id})
		SET thread.root_thesis_id = $root_thesis_id,
		    thread.root_domain = $root_domain,
		    thread.updated_at = $updated_at`,
		map[string]any{
			"id":             message.ThreadID,
			"root_thesis_id": firstNonEmpty(message.RootThesisID, message.ThesisID),
			"root_domain":    strings.TrimSpace(message.OriginDomain),
			"updated_at":     now,
		},
	); err != nil {
		return err
	}

	if err := runQuery(ctx, tx, `
		MERGE (m:ColleagueMessage {id: $id})
		SET m.kind = $kind,
		    m.origin_desk = $origin_desk,
		    m.origin_domain = $origin_domain,
		    m.origin_signal_id = $origin_signal_id,
		    m.thesis_id = $thesis_id,
		    m.parent_thesis_id = $parent_thesis_id,
		    m.root_thesis_id = $root_thesis_id,
		    m.strategy = $strategy,
		    m.structure = $structure,
		    m.conviction = $conviction,
		    m.depth = $depth,
		    m.requested_action = $requested_action,
		    m.subject = $subject,
		    m.summary = $summary,
		    m.display_symbol = $display_symbol,
		    m.updated_at = $updated_at`,
		map[string]any{
			"id":               message.MessageID,
			"kind":             string(message.Kind),
			"origin_desk":      message.OriginDesk,
			"origin_domain":    message.OriginDomain,
			"origin_signal_id": message.OriginSignalID,
			"thesis_id":        message.ThesisID,
			"parent_thesis_id": message.ParentThesisID,
			"root_thesis_id":   message.RootThesisID,
			"strategy":         message.Strategy,
			"structure":        message.Structure,
			"conviction":       message.Conviction,
			"depth":            message.InternalDepth,
			"requested_action": message.RequestedAction,
			"subject":          message.Subject,
			"summary":          message.Summary,
			"display_symbol":   message.DisplaySymbol,
			"updated_at":       now,
		},
	); err != nil {
		return err
	}

	if err := runQuery(ctx, tx, `
		MATCH (s:Signal {id: $signal_id})
		MATCH (m:ColleagueMessage {id: $message_id})
		MERGE (s)-[r:EMBODIES_MESSAGE]->(m)
		SET r.observed_time = $observed_time,
		    r.decision_time = $decision_time`,
		map[string]any{
			"signal_id":     sig.ID,
			"message_id":    message.MessageID,
			"observed_time": normalizeTime(sig.Timestamp, now),
			"decision_time": now,
		},
	); err != nil {
		return err
	}

	if err := runQuery(ctx, tx, `
		MATCH (m:ColleagueMessage {id: $message_id})
		MATCH (thread:ConversationThread {id: $thread_id})
		MERGE (m)-[r:IN_THREAD]->(thread)
		SET r.observed_time = $observed_time,
		    r.decision_time = $decision_time`,
		map[string]any{
			"message_id":    message.MessageID,
			"thread_id":     message.ThreadID,
			"observed_time": normalizeTime(sig.Timestamp, now),
			"decision_time": now,
		},
	); err != nil {
		return err
	}

	if message.OriginDesk != "" {
		if err := runQuery(ctx, tx, `
			MERGE (d:Desk {id: $desk_id})
			SET d.domain = CASE WHEN $domain = '' THEN d.domain ELSE $domain END,
			    d.updated_at = $updated_at`,
			map[string]any{
				"desk_id":    message.OriginDesk,
				"domain":     message.OriginDomain,
				"updated_at": now,
			},
		); err != nil {
			return err
		}
		if err := runQuery(ctx, tx, `
			MATCH (d:Desk {id: $desk_id})
			MATCH (m:ColleagueMessage {id: $message_id})
			MERGE (d)-[r:AUTHORED]->(m)
			SET r.observed_time = $observed_time,
			    r.decision_time = $decision_time`,
			map[string]any{
				"desk_id":       message.OriginDesk,
				"message_id":    message.MessageID,
				"observed_time": normalizeTime(sig.Timestamp, now),
				"decision_time": now,
			},
		); err != nil {
			return err
		}
	}

	if message.ReplyToMessageID != "" {
		if err := runQuery(ctx, tx, `
			MATCH (m:ColleagueMessage {id: $message_id})
			MATCH (prev:ColleagueMessage {id: $reply_to_message_id})
			MERGE (m)-[r:REPLIES_TO]->(prev)
			SET r.decision_time = $decision_time`,
			map[string]any{
				"message_id":          message.MessageID,
				"reply_to_message_id": message.ReplyToMessageID,
				"decision_time":       now,
			},
		); err != nil {
			return err
		}
	}

	if message.ThesisID != "" {
		if err := runQuery(ctx, tx, `
			MATCH (m:ColleagueMessage {id: $message_id})
			MATCH (t:Thesis {id: $thesis_id})
			MERGE (m)-[r:ABOUT]->(t)
			SET r.decision_time = $decision_time`,
			map[string]any{
				"message_id":    message.MessageID,
				"thesis_id":     message.ThesisID,
				"decision_time": now,
			},
		); err != nil {
			return err
		}
	}

	for _, domain := range message.TargetDomains {
		if err := runQuery(ctx, tx, `
			MERGE (d:Domain {id: $domain})
			SET d.updated_at = $updated_at`,
			map[string]any{
				"domain":     domain,
				"updated_at": now,
			},
		); err != nil {
			return err
		}
		if err := runQuery(ctx, tx, `
			MATCH (m:ColleagueMessage {id: $message_id})
			MATCH (d:Domain {id: $domain})
			MERGE (m)-[r:TARGETS]->(d)
			SET r.decision_time = $decision_time`,
			map[string]any{
				"message_id":    message.MessageID,
				"domain":        domain,
				"decision_time": now,
			},
		); err != nil {
			return err
		}
	}

	return nil
}

func (c *Client) linkEvidenceAssessment(ctx context.Context, tx neo4j.ManagedTransaction, nodeLabel, nodeID string, meta *evidence.Metadata, now time.Time) error {
	if meta == nil {
		return nil
	}

	assessmentID := nodeID + ":evidence"
	if err := runQuery(ctx, tx, `
		MERGE (e:EvidenceAssessment {id: $id})
		SET e.evidence_score = $evidence_score,
		    e.source_trust = $source_trust,
		    e.original_language = $original_language,
		    e.origin_region = $origin_region,
		    e.translation_provider = $translation_provider,
		    e.translation_confidence = $translation_confidence,
		    e.lead_time_average_hours = $lead_time_average_hours,
		    e.lead_time_observations = $lead_time_observations,
		    e.lead_time_score = $lead_time_score,
		    e.fact_confidence = $fact_confidence,
		    e.novelty_confidence = $novelty_confidence,
		    e.market_mapping_confidence = $market_mapping_confidence,
		    e.expression_confidence = $expression_confidence,
		    e.execution_confidence = $execution_confidence,
		    e.competence_confidence = $competence_confidence,
		    e.distinct_languages = $distinct_languages,
		    e.freshness_status = $freshness_status,
		    e.contradiction_count = $contradiction_count,
		    e.contradiction_severity = $contradiction_severity,
		    e.updated_at = $updated_at`,
		map[string]any{
			"id":                        assessmentID,
			"evidence_score":            meta.EvidenceScore,
			"source_trust":              meta.SourceTrust,
			"original_language":         strings.TrimSpace(meta.OriginalLanguage),
			"origin_region":             strings.TrimSpace(meta.OriginRegion),
			"translation_provider":      strings.TrimSpace(meta.TranslationProvider),
			"translation_confidence":    meta.TranslationConfidence,
			"lead_time_average_hours":   meta.LeadTimeAverageHours,
			"lead_time_observations":    meta.LeadTimeObservations,
			"lead_time_score":           meta.LeadTimeScore,
			"fact_confidence":           evidenceConfidence(meta, func(v *evidence.ConfidenceVector) float64 { return v.FactConfidence }),
			"novelty_confidence":        evidenceConfidence(meta, func(v *evidence.ConfidenceVector) float64 { return v.NoveltyConfidence }),
			"market_mapping_confidence": evidenceConfidence(meta, func(v *evidence.ConfidenceVector) float64 { return v.MarketMappingConfidence }),
			"expression_confidence":     evidenceConfidence(meta, func(v *evidence.ConfidenceVector) float64 { return v.ExpressionConfidence }),
			"execution_confidence":      evidenceConfidence(meta, func(v *evidence.ConfidenceVector) float64 { return v.ExecutionConfidence }),
			"competence_confidence":     evidenceConfidence(meta, func(v *evidence.ConfidenceVector) float64 { return v.CompetenceConfidence }),
			"distinct_languages":        meta.DistinctLanguages,
			"freshness_status":          strings.TrimSpace(meta.FreshnessStatus),
			"contradiction_count":       meta.ContradictionCount,
			"contradiction_severity":    strings.TrimSpace(meta.ContradictionSeverity),
			"updated_at":                now,
		},
	); err != nil {
		return err
	}

	query := fmt.Sprintf(`
		MATCH (n:%s {id: $node_id})
		MATCH (e:EvidenceAssessment {id: $assessment_id})
		MERGE (n)-[r:HAS_EVIDENCE_ASSESSMENT]->(e)
		SET r.observed_time = $updated_at,
		    r.decision_time = $updated_at`, nodeLabel)
	return runQuery(ctx, tx, query, map[string]any{
		"node_id":       nodeID,
		"assessment_id": assessmentID,
		"updated_at":    now,
	})
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
	return entityresolve.Resolve(entity, "").CanonicalID
}

func entityAliasID(entity signal.Entity, sig signal.Signal) string {
	name := strings.TrimSpace(entity.Name)
	if name == "" {
		return ""
	}
	language := primaryLanguage(sig)
	return language + ":" + entityresolve.NormalizeKey(name)
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

func containsString(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
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

func evidenceConfidence(meta *evidence.Metadata, getter func(*evidence.ConfidenceVector) float64) float64 {
	if meta == nil || !meta.Present() || meta.ConfidenceVector == nil || !meta.ConfidenceVector.Present() {
		return 0
	}
	return getter(meta.ConfidenceVector)
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
