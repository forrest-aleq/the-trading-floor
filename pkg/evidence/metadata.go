package evidence

import "strings"

// Metadata captures deterministic evidence quality signals attached to a
// normalized market signal and propagated through decision-making.
type Metadata struct {
	SourceDomain             string   `json:"source_domain,omitempty"`
	SourceOwnerGroup         string   `json:"source_owner_group,omitempty"`
	SourceTier               string   `json:"source_tier,omitempty"`
	SourceType               string   `json:"source_type,omitempty"`
	SourceTrust              float64  `json:"source_trust,omitempty"`
	FreshnessStatus          string   `json:"freshness_status,omitempty"`
	FreshnessReason          string   `json:"freshness_reason,omitempty"`
	FreshnessAgeHours        float64  `json:"freshness_age_hours,omitempty"`
	FreshnessWindowHours     float64  `json:"freshness_window_hours,omitempty"`
	CorroboratingOwnerGroups []string `json:"corroborating_owner_groups,omitempty"`
	DistinctSources          int      `json:"distinct_sources,omitempty"`
	DistinctOwnerGroups      int      `json:"distinct_owner_groups,omitempty"`
	HasPrimarySource         bool     `json:"has_primary_source,omitempty"`
	ContradictionCount       int      `json:"contradiction_count,omitempty"`
	ContradictionSeverity    string   `json:"contradiction_severity,omitempty"`
	ConflictingSignalIDs     []string `json:"conflicting_signal_ids,omitempty"`
	EvidenceScore            float64  `json:"evidence_score,omitempty"`
}

func (m *Metadata) Clone() *Metadata {
	if m == nil {
		return nil
	}

	cloned := *m
	cloned.CorroboratingOwnerGroups = append([]string(nil), m.CorroboratingOwnerGroups...)
	cloned.ConflictingSignalIDs = append([]string(nil), m.ConflictingSignalIDs...)
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
		strings.TrimSpace(m.FreshnessStatus) != "" ||
		m.FreshnessAgeHours > 0 ||
		m.FreshnessWindowHours > 0 ||
		m.DistinctSources > 0 ||
		m.DistinctOwnerGroups > 0 ||
		m.HasPrimarySource ||
		m.ContradictionCount > 0 ||
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
	if m.EvidenceScore < 0.30 {
		return false, "low_evidence_score"
	}

	return true, ""
}
