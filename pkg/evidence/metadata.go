package evidence

import "strings"

type ConfidenceVector struct {
	FactConfidence          float64 `json:"fact_confidence,omitempty"`
	NoveltyConfidence       float64 `json:"novelty_confidence,omitempty"`
	MarketMappingConfidence float64 `json:"market_mapping_confidence,omitempty"`
	ExpressionConfidence    float64 `json:"expression_confidence,omitempty"`
	ExecutionConfidence     float64 `json:"execution_confidence,omitempty"`
	CompetenceConfidence    float64 `json:"competence_confidence,omitempty"`
}

func (v *ConfidenceVector) Clone() *ConfidenceVector {
	if v == nil {
		return nil
	}
	cloned := *v
	return &cloned
}

func (v *ConfidenceVector) Present() bool {
	if v == nil {
		return false
	}
	return v.FactConfidence > 0 ||
		v.NoveltyConfidence > 0 ||
		v.MarketMappingConfidence > 0 ||
		v.ExpressionConfidence > 0 ||
		v.ExecutionConfidence > 0 ||
		v.CompetenceConfidence > 0
}

func (v *ConfidenceVector) Overall() float64 {
	if v == nil {
		return 0
	}
	total := v.FactConfidence +
		v.NoveltyConfidence +
		v.MarketMappingConfidence +
		v.ExpressionConfidence +
		v.ExecutionConfidence +
		v.CompetenceConfidence
	return total / 6
}

// Metadata captures deterministic evidence quality signals attached to a
// normalized market signal and propagated through decision-making.
type Metadata struct {
	SourceDomain             string            `json:"source_domain,omitempty"`
	SourceOwnerGroup         string            `json:"source_owner_group,omitempty"`
	SourceTier               string            `json:"source_tier,omitempty"`
	SourceType               string            `json:"source_type,omitempty"`
	SourceTrust              float64           `json:"source_trust,omitempty"`
	OriginalLanguage         string            `json:"original_language,omitempty"`
	OriginRegion             string            `json:"origin_region,omitempty"`
	TranslationProvider      string            `json:"translation_provider,omitempty"`
	TranslationConfidence    float64           `json:"translation_confidence,omitempty"`
	FreshnessStatus          string            `json:"freshness_status,omitempty"`
	FreshnessReason          string            `json:"freshness_reason,omitempty"`
	FreshnessAgeHours        float64           `json:"freshness_age_hours,omitempty"`
	FreshnessWindowHours     float64           `json:"freshness_window_hours,omitempty"`
	LeadTimeAverageHours     float64           `json:"lead_time_average_hours,omitempty"`
	LeadTimeObservations     int               `json:"lead_time_observations,omitempty"`
	LeadTimeScore            float64           `json:"lead_time_score,omitempty"`
	CorroboratingOwnerGroups []string          `json:"corroborating_owner_groups,omitempty"`
	DistinctSources          int               `json:"distinct_sources,omitempty"`
	DistinctOwnerGroups      int               `json:"distinct_owner_groups,omitempty"`
	DistinctLanguages        int               `json:"distinct_languages,omitempty"`
	HasPrimarySource         bool              `json:"has_primary_source,omitempty"`
	ContradictionCount       int               `json:"contradiction_count,omitempty"`
	ContradictionSeverity    string            `json:"contradiction_severity,omitempty"`
	ConflictingSignalIDs     []string          `json:"conflicting_signal_ids,omitempty"`
	ConfidenceVector         *ConfidenceVector `json:"confidence_vector,omitempty"`
	EvidenceScore            float64           `json:"evidence_score,omitempty"`
}

func (m *Metadata) Clone() *Metadata {
	if m == nil {
		return nil
	}

	cloned := *m
	cloned.CorroboratingOwnerGroups = append([]string(nil), m.CorroboratingOwnerGroups...)
	cloned.ConflictingSignalIDs = append([]string(nil), m.ConflictingSignalIDs...)
	cloned.ConfidenceVector = m.ConfidenceVector.Clone()
	return &cloned
}

func (m *Metadata) Present() bool {
	if m == nil {
		return false
	}

	return strings.TrimSpace(m.SourceDomain) != "" ||
		strings.TrimSpace(m.SourceOwnerGroup) != "" ||
		strings.TrimSpace(m.SourceTier) != "" ||
		strings.TrimSpace(m.SourceType) != "" ||
		m.SourceTrust > 0 ||
		strings.TrimSpace(m.OriginalLanguage) != "" ||
		strings.TrimSpace(m.OriginRegion) != "" ||
		strings.TrimSpace(m.TranslationProvider) != "" ||
		m.TranslationConfidence > 0 ||
		strings.TrimSpace(m.FreshnessStatus) != "" ||
		m.FreshnessAgeHours > 0 ||
		m.FreshnessWindowHours > 0 ||
		m.LeadTimeAverageHours > 0 ||
		m.LeadTimeObservations > 0 ||
		m.LeadTimeScore > 0 ||
		m.DistinctSources > 0 ||
		m.DistinctOwnerGroups > 0 ||
		m.DistinctLanguages > 0 ||
		m.HasPrimarySource ||
		m.ContradictionCount > 0 ||
		(m.ConfidenceVector != nil && m.ConfidenceVector.Present()) ||
		m.EvidenceScore > 0
}

func (m *Metadata) IndependentCorroboration() bool {
	if m == nil {
		return false
	}

	return m.DistinctOwnerGroups >= 2
}

// DeterministicGate returns whether the evidence quality is sufficient to
// spend more reasoning budget or capital on the signal.
func (m *Metadata) DeterministicGate() (bool, string) {
	if !m.Present() {
		return true, ""
	}

	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(m.FreshnessStatus)), "stale") {
		return false, "stale_signal_evidence"
	}
	if strings.EqualFold(m.ContradictionSeverity, "high") && m.ContradictionCount > 0 {
		return false, "contradictory_signal_evidence"
	}
	if strings.EqualFold(m.SourceType, "social") && !m.IndependentCorroboration() {
		return false, "uncorroborated_social_signal"
	}
	if m.SourceTrust > 0 && m.SourceTrust < 0.45 && !m.IndependentCorroboration() {
		return false, "low_integrity_evidence"
	}
	if m.ConfidenceVector != nil {
		if m.ConfidenceVector.FactConfidence > 0 && m.ConfidenceVector.FactConfidence < 0.30 {
			return false, "low_fact_confidence"
		}
		if m.ConfidenceVector.MarketMappingConfidence > 0 && m.ConfidenceVector.MarketMappingConfidence < 0.25 {
			return false, "low_market_mapping_confidence"
		}
		if m.ConfidenceVector.ExpressionConfidence > 0 && m.ConfidenceVector.ExpressionConfidence < 0.22 {
			return false, "low_expression_confidence"
		}
		if m.ConfidenceVector.ExecutionConfidence > 0 && m.ConfidenceVector.ExecutionConfidence < 0.20 {
			return false, "low_execution_confidence"
		}
		if m.ConfidenceVector.CompetenceConfidence > 0 && m.ConfidenceVector.CompetenceConfidence < 0.20 {
			return false, "low_competence_confidence"
		}
	}
	if m.EvidenceScore < 0.30 {
		return false, "low_evidence_score"
	}

	return true, ""
}
