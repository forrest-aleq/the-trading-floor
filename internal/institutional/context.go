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
		_, _ = fmt.Fprintf(&sb, line, args...)
	}

	if opts.IncludeInstitutional && strings.TrimSpace(sig.InstitutionalContext) != "" {
		appendIndentedBlock(&sb, opts.Indent, sig.InstitutionalContext)
	}
	if opts.IncludeInstitutional && sig.Expectation != nil {
		appendIndentedBlock(&sb, opts.Indent, BuildExpectationContext(sig.Expectation, "  "))
	}
	if opts.IncludeInstitutional && sig.Appraisal != nil {
		appendIndentedBlock(&sb, opts.Indent, BuildAppraisalContext(sig.Appraisal, "  "))
	}
	if opts.IncludeInstitutional && sig.ActionSelection != nil {
		appendIndentedBlock(&sb, opts.Indent, BuildActionSelectionContext(sig.ActionSelection, "  "))
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

type ThesisContextOptions struct {
	Indent               string
	IncludeInstitutional bool
	IncludeEvidence      bool
	IncludeCounterArgs   bool
	IncludeQuant         bool
	IncludeProsecution   bool
}

func BuildThesisContext(thesis *model.Thesis, opts ThesisContextOptions) string {
	if thesis == nil {
		return ""
	}

	var sb strings.Builder
	write := func(line string, args ...any) {
		if sb.Len() > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(opts.Indent)
		_, _ = fmt.Fprintf(&sb, line, args...)
	}

	primary := thesis.PrimaryInstrument()
	write("Symbol: %s (%s)", thesis.DisplaySymbol(), primary.SecType)
	write("Direction: %s", thesis.Direction)
	write("Strategy: %s", thesis.Strategy)
	if thesis.Structure != "" {
		write("Structure: %s", thesis.Structure)
	}
	write("Conviction: %.2f", thesis.Conviction)
	write("Entry: %.2f / Target: %.2f / Stop: %.2f", thesis.EntryPrice, thesis.TargetPrice, thesis.StopLoss)
	if thesis.TimeHorizon > 0 {
		write("Time horizon: %s", thesis.TimeHorizon)
	}
	if thesis.PositionSize > 0 {
		write("Position size (notional %%): %.2f", thesis.PositionSize)
	}

	if opts.IncludeInstitutional {
		appendIndentedBlock(&sb, opts.Indent, BuildThesisInstitutionalContext(thesis, "  "))
	}
	if opts.IncludeEvidence {
		appendIndentedBlock(&sb, opts.Indent, BuildThesisEvidenceContext(thesis, "  "))
	}
	if opts.IncludeCounterArgs {
		appendIndentedBlock(&sb, opts.Indent, BuildCounterArgumentContext(thesis.CounterArgs, "  "))
	}
	if opts.IncludeProsecution {
		appendIndentedBlock(&sb, opts.Indent, BuildProsecutionContext(thesis.Prosecution, "  "))
	}
	if opts.IncludeQuant {
		appendIndentedBlock(&sb, opts.Indent, BuildQuantMetricsContext(thesis.QuantMetrics, "  "))
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
		_, _ = fmt.Fprintf(&sb, line, args...)
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

func BuildThesisInstitutionalContext(thesis *model.Thesis, indent string) string {
	if thesis == nil {
		return ""
	}

	lines := []string{"Institutional context:"}
	if thesis.AutonomyMode != "" {
		lines = append(lines, fmt.Sprintf("%sautonomy.mode=%s", indent, thesis.AutonomyMode))
	}
	if thesis.ScanTerritory != "" {
		lines = append(lines, fmt.Sprintf("%sscan.territory=%s", indent, thesis.ScanTerritory))
	}
	if thesis.ExecutionTerritory != "" {
		lines = append(lines, fmt.Sprintf("%sexecution.territory=%s", indent, thesis.ExecutionTerritory))
	}
	if thesis.CompetenceKey != "" {
		lines = append(lines, fmt.Sprintf("%scompetence.key=%s", indent, thesis.CompetenceKey))
	}
	if thesis.CompetenceTrust > 0 {
		lines = append(lines, fmt.Sprintf("%scompetence.trust=%.2f", indent, thesis.CompetenceTrust))
	}
	if thesis.CompetenceConfidence > 0 {
		lines = append(lines, fmt.Sprintf("%scompetence.confidence=%.2f", indent, thesis.CompetenceConfidence))
	}
	if thesis.CollaborationInput != nil {
		for _, line := range strings.Split(BuildCollaborationContext(thesis.CollaborationInput, indent), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || line == "Institutional context:" {
				continue
			}
			lines = append(lines, line)
		}
	}

	if len(lines) == 1 {
		return ""
	}
	return strings.Join(lines, "\n")
}

func BuildThesisEvidenceContext(thesis *model.Thesis, indent string) string {
	if thesis == nil {
		return ""
	}

	var blocks []string
	if len(thesis.Evidence) > 0 {
		lines := []string{"Evidence:"}
		for i, item := range thesis.Evidence {
			line := fmt.Sprintf("%s%d. %s (weight: %.1f)", indent, i+1, strings.TrimSpace(item.Content), item.Weight)
			if item.Source != "" {
				line += fmt.Sprintf(" [source=%s]", item.Source)
			}
			if item.SignalID != "" {
				line += fmt.Sprintf(" [signal_id=%s]", item.SignalID)
			}
			lines = append(lines, line)
		}
		blocks = append(blocks, strings.Join(lines, "\n"))
	}
	if thesis.EvidenceMeta != nil {
		blocks = append(blocks, BuildEvidenceContext(thesis.EvidenceMeta, EvidenceContextOptions{
			Indent:  indent,
			Compact: false,
		}))
	}
	return joinBlocks(blocks...)
}

func BuildCounterArgumentContext(args []string, indent string) string {
	if len(args) == 0 {
		return ""
	}
	lines := []string{"Counter arguments:"}
	for i, arg := range args {
		arg = strings.TrimSpace(arg)
		if arg == "" {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s%d. %s", indent, i+1, arg))
	}
	if len(lines) == 1 {
		return ""
	}
	return strings.Join(lines, "\n")
}

func BuildProsecutionContext(p *model.Prosecution, indent string) string {
	if p == nil {
		return "Prosecution verdict: not prosecuted"
	}
	lines := []string{
		fmt.Sprintf("Prosecution verdict: %s", strings.TrimSpace(p.Verdict)),
		fmt.Sprintf("%sconfidence_adjustment=%.2f", indent, p.Confidence),
	}
	if len(p.BearArgs) > 0 {
		lines = append(lines, fmt.Sprintf("%sbear_args=%s", indent, strings.Join(SampleStrings(p.BearArgs, 5), "; ")))
	}
	if len(p.Analogues) > 0 {
		lines = append(lines, fmt.Sprintf("%shistorical_analogues=%s", indent, strings.Join(SampleStrings(p.Analogues, 4), "; ")))
	}
	return strings.Join(lines, "\n")
}

func BuildQuantMetricsContext(metrics *model.QuantMetrics, indent string) string {
	if metrics == nil {
		return "Quant metrics:\n" + indent + "unavailable"
	}

	lines := []string{
		"Quant metrics:",
		fmt.Sprintf("%smethod=%s", indent, strings.TrimSpace(metrics.Method)),
		fmt.Sprintf("%sdefined_risk=%t", indent, metrics.DefinedRisk),
	}
	if metrics.MaxLoss > 0 {
		lines = append(lines, fmt.Sprintf("%smax_loss=%.2f", indent, metrics.MaxLoss))
	}
	if metrics.MaxGain > 0 {
		lines = append(lines, fmt.Sprintf("%smax_gain=%.2f", indent, metrics.MaxGain))
	}
	if metrics.Breakeven != 0 {
		lines = append(lines, fmt.Sprintf("%sbreakeven=%.2f", indent, metrics.Breakeven))
	}
	if metrics.MarginEstimate > 0 {
		lines = append(lines, fmt.Sprintf("%smargin_estimate=%.2f", indent, metrics.MarginEstimate))
	}
	if metrics.RewardToRisk > 0 {
		lines = append(lines, fmt.Sprintf("%sreward_to_risk=%.2f", indent, metrics.RewardToRisk))
	}
	if metrics.NetDeltaBias != 0 {
		lines = append(lines, fmt.Sprintf("%snet_delta_bias=%.2f", indent, metrics.NetDeltaBias))
	}
	if len(metrics.Warnings) > 0 {
		lines = append(lines, fmt.Sprintf("%swarnings=%s", indent, strings.Join(SampleStrings(metrics.Warnings, 5), "; ")))
	}
	return strings.Join(lines, "\n")
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
	if input.RelationshipHealth > 0 {
		lines = append(lines, fmt.Sprintf("%scolleague.relationship_health=%.2f", indent, input.RelationshipHealth))
	}
	if input.RecoveryScore > 0 {
		lines = append(lines, fmt.Sprintf("%scolleague.recovery_score=%.2f", indent, input.RecoveryScore))
	}
	if input.AppraisalClass != "" {
		lines = append(lines, fmt.Sprintf("%scolleague.appraisal_class=%s", indent, input.AppraisalClass))
	}
	if input.FaceThreatScore > 0 {
		lines = append(lines, fmt.Sprintf("%scolleague.face_threat=%.2f", indent, input.FaceThreatScore))
	}
	if input.SocialCost > 0 {
		lines = append(lines, fmt.Sprintf("%scolleague.social_cost=%.2f", indent, input.SocialCost))
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

func appendIndentedBlock(sb *strings.Builder, indent, block string) {
	block = strings.TrimSpace(block)
	if block == "" {
		return
	}
	for _, line := range strings.Split(block, "\n") {
		if sb.Len() > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(indent)
		sb.WriteString(line)
	}
}

func joinBlocks(blocks ...string) string {
	filtered := make([]string, 0, len(blocks))
	for _, block := range blocks {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		filtered = append(filtered, block)
	}
	return strings.Join(filtered, "\n")
}
