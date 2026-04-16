package institutional

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hnic/trading-floor/pkg/evidence"
	"github.com/hnic/trading-floor/pkg/model"
	"github.com/hnic/trading-floor/pkg/signal"
)

type SignalContextOptions struct {
	Compact              bool
	Indent               string
	ContentLimit         int
	RelatedLimit         int
	EntityLimit          int
	IncludeEvidence      bool
	IncludeInstitutional bool
}

func BuildSignalContext(sig signal.Signal, opts SignalContextOptions) string {
	var sb strings.Builder
	write := func(line string, args ...any) {
		if sb.Len() > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(opts.Indent)
		sb.WriteString(fmt.Sprintf(line, args...))
	}

	if opts.IncludeInstitutional && strings.TrimSpace(sig.InstitutionalContext) != "" {
		for _, line := range strings.Split(strings.TrimSpace(sig.InstitutionalContext), "\n") {
			if sb.Len() > 0 {
				sb.WriteByte('\n')
			}
			sb.WriteString(opts.Indent)
			sb.WriteString(line)
		}
	}

	write("Source: %s", sig.Source)
	write("Type: %s", sig.Type)
	write("Category: %s", sig.Category)
	if sig.Urgency > 0 {
		write("Urgency: %.2f", sig.Urgency)
	}
	if sig.Direction != "" {
		write("Signal direction: %s", sig.Direction)
	}
	if sig.ClusterID != "" {
		write("Cluster: %s", sig.ClusterID)
	}
	if sig.NarrativeClusterID != "" {
		write("Narrative: %s", sig.NarrativeClusterID)
	}
	if len(sig.RelatedSignalIDs) > 0 {
		write("Related signals: %d (%s)", len(sig.RelatedSignalIDs), strings.Join(SampleStrings(sig.RelatedSignalIDs, opts.RelatedLimit), ", "))
	}
	if len(sig.Entities) > 0 {
		write("Entities: %s", strings.Join(SampleEntities(sig.Entities, opts.EntityLimit), ", "))
	}
	if len(sig.Languages) > 0 {
		write("Original language: %s", strings.ToLower(sig.Languages[0]))
	}
	if sig.TranslationProvider != "" || sig.TranslationConfidence > 0 {
		write("Translation: provider=%s confidence=%.2f", sig.TranslationProvider, sig.TranslationConfidence)
	}
	if len(sig.CorroboratingSources) > 0 {
		write("Corroborating sources: %s", strings.Join(SampleStrings(sig.CorroboratingSources, opts.RelatedLimit), ", "))
	}
	if len(sig.CorroboratingEntities) > 0 && !opts.Compact {
		write("Corroborating entities: %s", strings.Join(SampleStrings(sig.CorroboratingEntities, opts.RelatedLimit), ", "))
	}
	if len(sig.CorroboratingLanguages) > 0 {
		write("Corroborating languages: %s", strings.Join(SampleStrings(sig.CorroboratingLanguages, opts.RelatedLimit), ", "))
	}
	if opts.IncludeEvidence && sig.EvidenceMeta != nil {
		evidenceBlock := BuildEvidenceContext(sig.EvidenceMeta, EvidenceContextOptions{
			Compact: opts.Compact,
			Indent:  opts.Indent,
		})
		if evidenceBlock != "" {
			if sb.Len() > 0 {
				sb.WriteByte('\n')
			}
			sb.WriteString(evidenceBlock)
		}
	}

	if content := SignalContent(sig); content != "" && opts.ContentLimit != 0 {
		write("Content: %s", TruncateForPrompt(content, opts.ContentLimit))
	}

	return sb.String()
}

type EvidenceContextOptions struct {
	Compact bool
	Indent  string
}

func BuildEvidenceContext(meta *evidence.Metadata, opts EvidenceContextOptions) string {
	if meta == nil {
		return ""
	}
	var sb strings.Builder
	write := func(line string, args ...any) {
		if sb.Len() > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(opts.Indent)
		sb.WriteString(fmt.Sprintf(line, args...))
	}

	write("Source trust: %.2f", meta.SourceTrust)
	if meta.SourceTier != "" || meta.SourceType != "" {
		write("Source quality: tier=%s type=%s", meta.SourceTier, meta.SourceType)
	}
	if !opts.Compact && (meta.SourceDomain != "" || meta.SourceOwnerGroup != "") {
		write("Source lineage: domain=%s owner_group=%s", meta.SourceDomain, meta.SourceOwnerGroup)
	}
	if meta.OriginalLanguage != "" || meta.OriginRegion != "" {
		write("Original language / region: %s / %s", meta.OriginalLanguage, meta.OriginRegion)
	}
	if meta.TranslationProvider != "" || meta.TranslationConfidence > 0 {
		write("Translation: provider=%s confidence=%.2f", meta.TranslationProvider, meta.TranslationConfidence)
	}
	if len(meta.CorroboratingOwnerGroups) > 0 {
		write("Independent owner groups: %s", strings.Join(SampleStrings(meta.CorroboratingOwnerGroups, 6), ", "))
	}
	if meta.LeadTimeObservations > 0 {
		write("Historical lead time: avg %.2fh across %d narratives (score %.2f)", meta.LeadTimeAverageHours, meta.LeadTimeObservations, meta.LeadTimeScore)
	}
	if meta.DistinctLanguages > 0 {
		write("Distinct languages: %d", meta.DistinctLanguages)
	}
	write("Freshness: %s (age %.1fh / window %.1fh)", meta.FreshnessStatus, meta.FreshnessAgeHours, meta.FreshnessWindowHours)
	write("Distinct sources / owner groups / languages: %d / %d / %d", meta.DistinctSources, meta.DistinctOwnerGroups, meta.DistinctLanguages)
	if meta.HasPrimarySource {
		write("Has primary source: %t", meta.HasPrimarySource)
	}
	if meta.ContradictionCount > 0 {
		write("Contradictions: %d (%s)", meta.ContradictionCount, meta.ContradictionSeverity)
	}
	write("Evidence score: %.2f", meta.EvidenceScore)
	if vector := meta.ConfidenceVector; vector != nil && vector.Present() {
		write(
			"Confidence vector: fact=%.2f novelty=%.2f market_map=%.2f expression=%.2f execution=%.2f competence=%.2f",
			vector.FactConfidence,
			vector.NoveltyConfidence,
			vector.MarketMappingConfidence,
			vector.ExpressionConfidence,
			vector.ExecutionConfidence,
			vector.CompetenceConfidence,
		)
	}
	return sb.String()
}

func BuildCollaborationContext(input *model.CollaborationInput, indent string) string {
	if input == nil {
		return ""
	}
	lines := []string{
		"Institutional context:",
		fmt.Sprintf("%scolleague.from_desk=%s", indent, input.OriginDesk),
		fmt.Sprintf("%scolleague.from_domain=%s", indent, input.OriginDomain),
		fmt.Sprintf("%scolleague.kind=%s", indent, input.Kind),
		fmt.Sprintf("%scolleague.requested_action=%s", indent, input.RequestedAction),
		fmt.Sprintf("%scolleague.peer_trust=%.2f", indent, input.RelationshipTrust),
		fmt.Sprintf("%scolleague.peer_confidence=%.2f", indent, input.RelationshipConfidence),
	}
	if input.Summary != "" {
		lines = append(lines, fmt.Sprintf("%scolleague.summary=%s", indent, input.Summary))
	}
	return strings.Join(lines, "\n")
}

func SignalContent(sig signal.Signal) string {
	switch {
	case strings.TrimSpace(sig.Translated) != "":
		return strings.TrimSpace(sig.Translated)
	case strings.TrimSpace(sig.OriginalText) != "":
		return strings.TrimSpace(sig.OriginalText)
	case len(sig.Raw) == 0:
		return ""
	}

	var decoded string
	if err := json.Unmarshal(sig.Raw, &decoded); err == nil {
		return strings.TrimSpace(decoded)
	}
	return strings.TrimSpace(string(sig.Raw))
}

func SampleEntities(entities []signal.Entity, limit int) []string {
	if len(entities) == 0 {
		return nil
	}
	if limit > 0 && len(entities) > limit {
		entities = entities[:limit]
	}
	values := make([]string, 0, len(entities))
	for _, entity := range entities {
		label := strings.TrimSpace(entity.Name)
		if label == "" {
			continue
		}
		if kind := strings.TrimSpace(entity.Type); kind != "" {
			label += ":" + kind
		}
		values = append(values, label)
	}
	return values
}

func SampleStrings(values []string, limit int) []string {
	if len(values) == 0 {
		return nil
	}
	if limit > 0 && len(values) > limit {
		values = values[:limit]
	}
	sampled := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		sampled = append(sampled, value)
	}
	return sampled
}

func TruncateForPrompt(value string, max int) string {
	value = strings.TrimSpace(value)
	if max <= 0 || len(value) <= max {
		return value
	}
	if max <= 3 {
		return value[:max]
	}
	return value[:max-3] + "..."
}
