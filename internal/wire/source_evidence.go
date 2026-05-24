package wire

import (
	"encoding/json"
	"math"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hnic/trading-floor/internal/entityresolve"
	"github.com/hnic/trading-floor/pkg/evidence"
	"github.com/hnic/trading-floor/pkg/signal"
)

type sourceProfile struct {
	Domain     string
	OwnerGroup string
	Tier       string
	Type       string
	Trust      float64
	Region     string
}

var (
	sourceKeywordProfiles = []struct {
		match   string
		profile sourceProfile
	}{
		{match: "sec-edgar", profile: sourceProfile{Domain: "sec.gov", OwnerGroup: "sec", Tier: "primary", Type: "primary", Trust: 0.95, Region: "us"}},
		{match: "fed-", profile: sourceProfile{Domain: "federalreserve.gov", OwnerGroup: "federal_reserve", Tier: "primary", Type: "primary", Trust: 0.93, Region: "us"}},
		{match: "fred", profile: sourceProfile{Domain: "fred.stlouisfed.org", OwnerGroup: "federal_reserve", Tier: "primary", Type: "primary", Trust: 0.92, Region: "us"}},
		{match: "reuters", profile: sourceProfile{Domain: "reuters.com", OwnerGroup: "thomson_reuters", Tier: "major_press", Type: "secondary", Trust: 0.90, Region: "global"}},
		{match: "ft", profile: sourceProfile{Domain: "ft.com", OwnerGroup: "nikkei", Tier: "major_press", Type: "secondary", Trust: 0.86, Region: "europe"}},
		{match: "cnbc", profile: sourceProfile{Domain: "cnbc.com", OwnerGroup: "nbcuniversal", Tier: "major_press", Type: "secondary", Trust: 0.84, Region: "us"}},
		{match: "apnews", profile: sourceProfile{Domain: "apnews.com", OwnerGroup: "associated_press", Tier: "major_press", Type: "secondary", Trust: 0.89, Region: "us"}},
		{match: "stocktwits", profile: sourceProfile{Domain: "stocktwits.com", OwnerGroup: "stocktwits", Tier: "social", Type: "social", Trust: 0.38, Region: "us"}},
		{match: "reddit/", profile: sourceProfile{Domain: "reddit.com", OwnerGroup: "reddit", Tier: "social", Type: "social", Trust: 0.32, Region: "global"}},
		{match: "telegram/", profile: sourceProfile{Domain: "telegram.org", OwnerGroup: "telegram", Tier: "social", Type: "social", Trust: 0.34, Region: "global"}},
		{match: "ibkr-market", profile: sourceProfile{Domain: "interactivebrokers.com", OwnerGroup: "interactive_brokers", Tier: "primary", Type: "market", Trust: 0.97, Region: "global"}},
		{match: "kalshi", profile: sourceProfile{Domain: "kalshi.com", OwnerGroup: "prediction_market", Tier: "market", Type: "alternative", Trust: 0.66, Region: "us"}},
		{match: "polymarket", profile: sourceProfile{Domain: "polymarket.com", OwnerGroup: "prediction_market", Tier: "market", Type: "alternative", Trust: 0.58, Region: "global"}},
		{match: "earnings-calendar", profile: sourceProfile{Domain: "", OwnerGroup: "earnings_provider", Tier: "aggregator", Type: "secondary", Trust: 0.68, Region: "us"}},
		{match: "alternative/", profile: sourceProfile{Domain: "", OwnerGroup: "alternative_data", Tier: "industry", Type: "alternative", Trust: 0.60, Region: "global"}},
	}
	domainProfiles = []struct {
		suffix  string
		profile sourceProfile
	}{
		{suffix: "sec.gov", profile: sourceProfile{Domain: "sec.gov", OwnerGroup: "sec", Tier: "primary", Type: "primary", Trust: 0.95, Region: "us"}},
		{suffix: "federalreserve.gov", profile: sourceProfile{Domain: "federalreserve.gov", OwnerGroup: "federal_reserve", Tier: "primary", Type: "primary", Trust: 0.93, Region: "us"}},
		{suffix: "stlouisfed.org", profile: sourceProfile{Domain: "fred.stlouisfed.org", OwnerGroup: "federal_reserve", Tier: "primary", Type: "primary", Trust: 0.92, Region: "us"}},
		{suffix: "ft.com", profile: sourceProfile{Domain: "ft.com", OwnerGroup: "nikkei", Tier: "major_press", Type: "secondary", Trust: 0.86, Region: "europe"}},
		{suffix: "reuters.com", profile: sourceProfile{Domain: "reuters.com", OwnerGroup: "thomson_reuters", Tier: "major_press", Type: "secondary", Trust: 0.90, Region: "global"}},
		{suffix: "apnews.com", profile: sourceProfile{Domain: "apnews.com", OwnerGroup: "associated_press", Tier: "major_press", Type: "secondary", Trust: 0.89, Region: "us"}},
		{suffix: "cnbc.com", profile: sourceProfile{Domain: "cnbc.com", OwnerGroup: "nbcuniversal", Tier: "major_press", Type: "secondary", Trust: 0.84, Region: "us"}},
		{suffix: "reddit.com", profile: sourceProfile{Domain: "reddit.com", OwnerGroup: "reddit", Tier: "social", Type: "social", Trust: 0.32, Region: "global"}},
		{suffix: "stocktwits.com", profile: sourceProfile{Domain: "stocktwits.com", OwnerGroup: "stocktwits", Tier: "social", Type: "social", Trust: 0.38, Region: "us"}},
		{suffix: "interactivebrokers.com", profile: sourceProfile{Domain: "interactivebrokers.com", OwnerGroup: "interactive_brokers", Tier: "primary", Type: "market", Trust: 0.97, Region: "global"}},
		{suffix: "kalshi.com", profile: sourceProfile{Domain: "kalshi.com", OwnerGroup: "prediction_market", Tier: "market", Type: "alternative", Trust: 0.66, Region: "us"}},
		{suffix: "polymarket.com", profile: sourceProfile{Domain: "polymarket.com", OwnerGroup: "prediction_market", Tier: "market", Type: "alternative", Trust: 0.58, Region: "global"}},
		{suffix: "telegram.org", profile: sourceProfile{Domain: "telegram.org", OwnerGroup: "telegram", Tier: "social", Type: "social", Trust: 0.34, Region: "global"}},
	}
	metricTerms = []string{
		"revenue", "earnings", "guidance", "margin", "cash flow", "burn", "valuation", "headcount",
		"eps", "arr", "churn", "opex", "capex", "inflation", "payrolls", "rate", "yield", "spread",
		"estimate", "surprise", "forecast",
	}
	positiveTerms = []string{"up", "increased", "grew", "rose", "raised", "beat", "improved", "expanded", "stronger", "accelerated"}
	negativeTerms = []string{"down", "decreased", "fell", "declined", "cut", "missed", "weakened", "contracted", "lowered", "deteriorated"}
	numberPattern = regexp.MustCompile(`\$?\b\d[\d,]*(?:\.\d+)?[mbk]?%?\b`)
)

func buildEvidenceMeta(sig signal.Signal) *evidence.Metadata {
	domain := extractSourceDomain(sig)
	profile := inferSourceProfile(sig, domain)
	if profile.Domain == "" {
		profile.Domain = domain
	}

	status, reason, ageHours, windowHours := evaluateFreshness(sig)
	meta := &evidence.Metadata{
		SourceDomain:          profile.Domain,
		SourceOwnerGroup:      profile.OwnerGroup,
		SourceTier:            profile.Tier,
		SourceType:            profile.Type,
		SourceTrust:           profile.Trust,
		OriginalLanguage:      signalLanguage(sig),
		OriginRegion:          inferOriginRegion(sig, profile),
		TranslationProvider:   strings.TrimSpace(sig.TranslationProvider),
		TranslationConfidence: roundEvidence(sig.TranslationConfidence),
		FreshnessStatus:       status,
		FreshnessReason:       reason,
		FreshnessAgeHours:     roundEvidence(ageHours),
		FreshnessWindowHours:  roundEvidence(windowHours),
		DistinctSources:       countNonEmpty(sig.Source),
		DistinctOwnerGroups:   countNonEmpty(profile.OwnerGroup),
		DistinctLanguages:     countNonEmpty(signalLanguage(sig)),
		HasPrimarySource:      profile.Tier == "primary" || profile.Type == "primary" || profile.Type == "market",
	}
	return refreshEvidenceAssessment(sig, meta)
}

func refreshEvidenceAssessment(sig signal.Signal, meta *evidence.Metadata) *evidence.Metadata {
	if meta == nil {
		return nil
	}
	meta.ConfidenceVector = scoreConfidenceVector(sig, meta)
	meta.EvidenceScore = roundEvidence(scoreEvidence(meta))
	return meta
}

func ApplyLearnedSourceReliability(sig signal.Signal, trust, confidence float64) signal.Signal {
	if sig.EvidenceMeta == nil {
		return sig
	}
	meta := sig.EvidenceMeta.Clone()
	weight := math.Max(0, math.Min(0.85, confidence))
	if weight == 0 {
		return sig
	}
	meta.SourceTrust = roundEvidence((meta.SourceTrust * (1 - weight)) + (trust * weight))
	sig.EvidenceMeta = refreshEvidenceAssessment(sig, meta)
	return sig
}

func ApplyLeadTimeBelief(sig signal.Signal, averageHours float64, observations int, score float64) signal.Signal {
	if sig.EvidenceMeta == nil || observations <= 0 {
		return sig
	}
	meta := sig.EvidenceMeta.Clone()
	if meta.LeadTimeObservations == 0 {
		meta.LeadTimeAverageHours = roundEvidence(averageHours)
		meta.LeadTimeObservations = observations
		meta.LeadTimeScore = roundEvidence(score)
	} else {
		totalObs := meta.LeadTimeObservations + observations
		if totalObs > 0 {
			weightedHours := (meta.LeadTimeAverageHours * float64(meta.LeadTimeObservations)) + (averageHours * float64(observations))
			meta.LeadTimeAverageHours = roundEvidence(weightedHours / float64(totalObs))
			meta.LeadTimeObservations = totalObs
		}
		if score > meta.LeadTimeScore {
			meta.LeadTimeScore = roundEvidence(score)
		}
	}
	sig.EvidenceMeta = refreshEvidenceAssessment(sig, meta)
	return sig
}

func inferSourceProfile(sig signal.Signal, domain string) sourceProfile {
	if profile, ok := profileForDomain(domain); ok {
		return applyProfileDefaults(profile, sig)
	}
	if profile, ok := profileForSource(sig.Source); ok {
		if domain != "" && profile.Domain == "" {
			profile.Domain = domain
		}
		return applyProfileDefaults(profile, sig)
	}

	return applyProfileDefaults(sourceProfile{
		Domain:     domain,
		OwnerGroup: fallbackOwnerGroup(sig.Source, domain),
		Tier:       fallbackTier(sig.Type),
		Type:       fallbackSourceType(sig.Type),
		Trust:      fallbackTrust(sig.Type),
	}, sig)
}

func applyProfileDefaults(profile sourceProfile, sig signal.Signal) sourceProfile {
	if profile.OwnerGroup == "" {
		profile.OwnerGroup = fallbackOwnerGroup(sig.Source, profile.Domain)
	}
	if profile.Tier == "" {
		profile.Tier = fallbackTier(sig.Type)
	}
	if profile.Type == "" {
		profile.Type = fallbackSourceType(sig.Type)
	}
	if profile.Trust == 0 {
		profile.Trust = fallbackTrust(sig.Type)
	}
	if profile.Region == "" {
		profile.Region = inferRegionFromLanguage(signalLanguage(sig))
	}
	return profile
}

func profileForDomain(domain string) (sourceProfile, bool) {
	domain = normalizeHost(domain)
	if domain == "" {
		return sourceProfile{}, false
	}
	for _, candidate := range domainProfiles {
		if domain == candidate.suffix || strings.HasSuffix(domain, "."+candidate.suffix) {
			return candidate.profile, true
		}
	}
	return sourceProfile{}, false
}

func profileForSource(source string) (sourceProfile, bool) {
	source = strings.TrimSpace(strings.ToLower(source))
	if source == "" {
		return sourceProfile{}, false
	}
	for _, candidate := range sourceKeywordProfiles {
		if strings.Contains(source, candidate.match) {
			return candidate.profile, true
		}
	}
	return sourceProfile{}, false
}

func extractSourceDomain(sig signal.Signal) string {
	for _, key := range []string{"source_url", "url", "link", "feed_url"} {
		if value := extractRawString(sig.Raw, key); value != "" {
			if host := normalizeHost(value); host != "" {
				return host
			}
		}
	}
	if sig.Source != "" {
		return normalizeHost(sig.Source)
	}
	return ""
}

func extractRawString(raw json.RawMessage, key string) string {
	if len(raw) == 0 {
		return ""
	}

	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		return ""
	}
	value, ok := object[key]
	if !ok {
		return ""
	}
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	return ""
}

func normalizeHost(text string) string {
	text = strings.TrimSpace(strings.ToLower(text))
	if text == "" {
		return ""
	}
	if strings.Contains(text, "/") || strings.Contains(text, ":") {
		if !strings.Contains(text, "://") {
			text = "https://" + text
		}
		parsed, err := url.Parse(text)
		if err == nil {
			text = parsed.Hostname()
		}
	}
	text = strings.TrimPrefix(text, "www.")
	switch {
	case strings.HasPrefix(text, "ft-"):
		return "ft.com"
	case strings.HasPrefix(text, "fed-"):
		return "federalreserve.gov"
	case strings.HasPrefix(text, "reddit/"):
		return "reddit.com"
	case strings.HasPrefix(text, "telegram/"):
		return "telegram.org"
	case strings.HasPrefix(text, "alternative/"):
		return ""
	}
	return text
}

func fallbackOwnerGroup(source, domain string) string {
	domain = normalizeHost(domain)
	if domain != "" {
		parts := strings.Split(domain, ".")
		if len(parts) >= 2 {
			return strings.Join(parts[len(parts)-2:], "_")
		}
		return strings.ReplaceAll(domain, ".", "_")
	}

	source = strings.TrimSpace(strings.ToLower(source))
	source = strings.ReplaceAll(source, "/", "_")
	source = strings.ReplaceAll(source, "-", "_")
	return source
}

func fallbackTier(typ signal.Type) string {
	switch typ {
	case signal.TypePrice, signal.TypeFiling, signal.TypeEconomic:
		return "primary"
	case signal.TypeSocial:
		return "social"
	case signal.TypeAlternative:
		return "industry"
	default:
		return "industry"
	}
}

func fallbackSourceType(typ signal.Type) string {
	switch typ {
	case signal.TypePrice:
		return "market"
	case signal.TypeFiling, signal.TypeEconomic:
		return "primary"
	case signal.TypeSocial:
		return "social"
	case signal.TypeAlternative:
		return "alternative"
	default:
		return "secondary"
	}
}

func fallbackTrust(typ signal.Type) float64 {
	switch typ {
	case signal.TypePrice:
		return 0.97
	case signal.TypeFiling:
		return 0.92
	case signal.TypeEconomic:
		return 0.90
	case signal.TypeSocial:
		return 0.35
	case signal.TypeAlternative:
		return 0.60
	default:
		return 0.65
	}
}

func evaluateFreshness(sig signal.Signal) (status string, reason string, ageHours float64, windowHours float64) {
	windowHours, reason = freshnessWindow(sig)
	if sig.Timestamp.IsZero() {
		return "missing_timestamp", "missing_timestamp", 0, windowHours
	}

	ageHours = time.Since(sig.Timestamp).Hours()
	if ageHours < 0 {
		ageHours = 0
	}
	if windowHours <= 0 {
		return "fresh", "window_exempt", ageHours, 0
	}
	if ageHours <= windowHours {
		return "fresh", reason, ageHours, windowHours
	}
	return "stale", "stale_" + reason, ageHours, windowHours
}

func freshnessWindow(sig signal.Signal) (hours float64, label string) {
	formType := strings.ToUpper(strings.TrimSpace(extractRawString(sig.Raw, "form_type")))
	switch formType {
	case "8-K":
		return 48, "8k"
	case "4":
		return 72, "form4"
	case "SC 13D", "SC 13G":
		return 96, "beneficial_ownership"
	case "10-Q":
		return 168, "10q"
	case "10-K", "DEF 14A", "S-1":
		return 336, "long_form_filing"
	}

	switch sig.Type {
	case signal.TypePrice:
		return 6, "market"
	case signal.TypeSocial:
		return 8, "social"
	case signal.TypeFlow:
		return 24, "flow"
	case signal.TypeNews:
		if sig.Urgency >= 0.8 {
			return 24, "breaking_news"
		}
		return 72, "news"
	case signal.TypeFiling:
		return 168, "filing"
	case signal.TypeEconomic:
		return 168, "economic"
	case signal.TypeAlternative:
		return 72, "alternative"
	default:
		return 72, "default"
	}
}

func scoreEvidence(meta *evidence.Metadata) float64 {
	if meta == nil {
		return 0
	}
	if meta.ConfidenceVector != nil && meta.ConfidenceVector.Present() {
		return clampEvidence(meta.ConfidenceVector.Overall())
	}

	score := legacyEvidenceScore(meta)
	return clampEvidence(score)
}

func legacyEvidenceScore(meta *evidence.Metadata) float64 {
	score := meta.SourceTrust
	switch meta.SourceTier {
	case "primary":
		score += 0.08
	case "major_press":
		score += 0.04
	case "aggregator":
		score -= 0.05
	case "social":
		score -= 0.15
	}

	if meta.HasPrimarySource {
		score += 0.08
	}
	if meta.DistinctSources > 1 {
		score += 0.04 * float64(minInt(meta.DistinctSources-1, 3))
	}
	if meta.DistinctOwnerGroups > 1 {
		score += 0.08 * float64(minInt(meta.DistinctOwnerGroups-1, 2))
	}
	if meta.FreshnessStatus == "fresh" {
		score += 0.05
	}
	if strings.HasPrefix(meta.FreshnessStatus, "stale") {
		score -= 0.35
	}
	if meta.FreshnessStatus == "missing_timestamp" {
		score -= 0.08
	}

	score -= 0.12 * float64(meta.ContradictionCount)
	switch meta.ContradictionSeverity {
	case "high":
		score -= 0.15
	case "medium":
		score -= 0.07
	}

	return score
}

func scoreConfidenceVector(sig signal.Signal, meta *evidence.Metadata) *evidence.ConfidenceVector {
	if meta == nil {
		return nil
	}

	relatedCount := len(sig.RelatedSignalIDs)
	entityCount := len(sig.Entities)
	corroborated := meta.DistinctOwnerGroups >= 2 || meta.DistinctSources >= 2
	crossLingual := meta.DistinctLanguages >= 2
	directional := sig.Direction != signal.Neutral
	freshnessBonus := freshnessConfidence(meta)

	fact := meta.SourceTrust
	if meta.SourceTier == "primary" || meta.SourceType == "market" {
		fact += 0.08
	}
	if meta.HasPrimarySource {
		fact += 0.08
	}
	if corroborated {
		fact += 0.06
	}
	if crossLingual {
		fact += 0.05
	}
	if meta.LeadTimeScore > 0 {
		fact += meta.LeadTimeScore * 0.06
	}
	if meta.TranslationConfidence > 0 && meta.OriginalLanguage != "en" {
		fact += (meta.TranslationConfidence - 0.5) * 0.18
	}
	fact -= contradictionPenalty(meta)
	if strings.HasPrefix(meta.FreshnessStatus, "stale") {
		fact -= 0.20
	}
	fact += freshnessBonus * 0.05

	novelty := 0.22 + (clampEvidence(sig.Urgency) * 0.30)
	novelty += freshnessBonus * 0.22
	novelty -= 0.08 * float64(minInt(relatedCount, 4))
	if meta.DistinctSources > 2 {
		novelty -= 0.03 * float64(minInt(meta.DistinctSources-2, 3))
	}
	if sig.Type == signal.TypeFiling || sig.Type == signal.TypeEconomic || sig.Type == signal.TypeFlow {
		novelty += 0.08
	}
	if crossLingual {
		novelty += 0.06
	}
	if meta.LeadTimeScore > 0 {
		novelty += meta.LeadTimeScore * 0.18
	}
	if meta.OriginRegion != "" && meta.OriginRegion != "global" {
		novelty += 0.03
	}
	if meta.SourceType == "social" && !corroborated {
		novelty -= 0.12
	}

	marketMapping := 0.18
	if entityCount > 0 {
		marketMapping += 0.26
	}
	if strings.TrimSpace(sig.Category) != "" {
		marketMapping += 0.12
	}
	if directional {
		marketMapping += 0.10
	}
	if sig.Type == signal.TypePrice || sig.Type == signal.TypeFiling || sig.Type == signal.TypeEconomic || sig.Type == signal.TypeNews {
		marketMapping += 0.08
	}
	if corroborated {
		marketMapping += 0.08
	}
	if crossLingual {
		marketMapping += 0.04
	}
	if meta.LeadTimeScore > 0 {
		marketMapping += meta.LeadTimeScore * 0.12
	}
	if meta.SourceType == "social" && !corroborated {
		marketMapping -= 0.16
	}

	expression := 0.16
	if entityCount > 0 {
		expression += 0.20
	}
	if directional {
		expression += 0.14
	}
	if sig.Urgency >= 0.5 {
		expression += 0.10
	}
	if strings.TrimSpace(sig.Category) != "" {
		expression += 0.08
	}
	if sig.Type == signal.TypePrice || sig.Type == signal.TypeFiling || sig.Type == signal.TypeEconomic || sig.Type == signal.TypeNews {
		expression += 0.08
	}
	if crossLingual {
		expression += 0.04
	}
	if meta.LeadTimeScore > 0 {
		expression += meta.LeadTimeScore * 0.06
	}
	expression -= contradictionPenalty(meta) * 0.8
	if strings.HasPrefix(meta.FreshnessStatus, "stale") {
		expression -= 0.15
	}
	if meta.SourceType == "social" && !corroborated {
		expression -= 0.16
	}

	execution := 0.18 + (meta.SourceTrust * 0.28)
	execution += freshnessBonus * 0.15
	if meta.HasPrimarySource {
		execution += 0.08
	}
	if corroborated {
		execution += 0.08
	}
	if crossLingual {
		execution += 0.03
	}
	if meta.LeadTimeScore > 0 {
		execution += meta.LeadTimeScore * 0.05
	}
	execution -= contradictionPenalty(meta) * 0.7
	if strings.HasPrefix(meta.FreshnessStatus, "stale") {
		execution -= 0.12
	}
	if meta.SourceType == "social" {
		execution -= 0.10
	}

	competence := 0.18 + (meta.SourceTrust * 0.22)
	if meta.HasPrimarySource {
		competence += 0.10
	}
	if corroborated {
		competence += 0.10
	}
	if entityCount > 0 {
		competence += 0.08
	}
	if directional {
		competence += 0.05
	}
	if crossLingual {
		competence += 0.06
	}
	if meta.LeadTimeScore > 0 {
		competence += meta.LeadTimeScore * 0.08
	}
	competence -= contradictionPenalty(meta) * 0.7
	if meta.SourceType == "social" && !corroborated {
		competence -= 0.10
	}
	if strings.HasPrefix(meta.FreshnessStatus, "stale") {
		competence -= 0.10
	}

	return &evidence.ConfidenceVector{
		FactConfidence:          roundEvidence(clampEvidence(fact)),
		NoveltyConfidence:       roundEvidence(clampEvidence(novelty)),
		MarketMappingConfidence: roundEvidence(clampEvidence(marketMapping)),
		ExpressionConfidence:    roundEvidence(clampEvidence(expression)),
		ExecutionConfidence:     roundEvidence(clampEvidence(execution)),
		CompetenceConfidence:    roundEvidence(clampEvidence(competence)),
	}
}

func inferOriginRegion(sig signal.Signal, profile sourceProfile) string {
	for _, key := range []string{"region", "country", "location"} {
		if value := extractRawString(sig.Raw, key); value != "" {
			if normalized := normalizeRegionLabel(value); normalized != "" {
				return normalized
			}
		}
	}

	for _, entity := range sig.Entities {
		if region := entityRegion(entity, signalLanguage(sig)); region != "" {
			return region
		}
	}

	if region := inferRegionFromLanguage(signalLanguage(sig)); region != "" && region != "global" {
		return region
	}
	if profile.Region != "" {
		return profile.Region
	}
	return "global"
}

func entityRegion(entity signal.Entity, language string) string {
	resolved := entityresolve.Resolve(entity, language)
	switch resolved.CanonicalID {
	case "country:IRAN", "country:SAUDI_ARABIA":
		return "mena"
	case "country:RUSSIA":
		return "russia_cis"
	case "country:CHINA":
		return "greater_china"
	default:
		return ""
	}
}

func inferRegionFromLanguage(language string) string {
	switch normalizeLanguageCode(language) {
	case "ar":
		return "mena"
	case "fa":
		return "iran"
	case "he":
		return "israel"
	case "ru":
		return "russia_cis"
	case "uk", "pl":
		return "cee"
	case "zh":
		return "greater_china"
	case "ja":
		return "japan"
	case "ko":
		return "korea"
	case "fr", "de", "it", "nl":
		return "europe"
	case "es", "pt":
		return "latam"
	case "hi":
		return "india"
	case "id", "vi", "th":
		return "southeast_asia"
	case "tr":
		return "turkey"
	case "en":
		return "global"
	default:
		return ""
	}
}

func normalizeRegionLabel(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return ""
	}
	replacer := strings.NewReplacer("-", "_", " ", "_")
	value = replacer.Replace(value)
	switch value {
	case "mena", "middle_east", "middle_east_and_north_africa", "gulf":
		return "mena"
	case "iran":
		return "iran"
	case "greater_china", "china", "hong_kong", "taiwan":
		return "greater_china"
	case "russia", "cis", "russia_cis":
		return "russia_cis"
	case "us", "usa", "united_states", "north_america":
		return "us"
	case "europe", "eu", "eurozone":
		return "europe"
	case "latam", "latin_america":
		return "latam"
	case "india":
		return "india"
	case "japan":
		return "japan"
	case "korea", "south_korea":
		return "korea"
	case "southeast_asia", "sea", "asean":
		return "southeast_asia"
	case "turkey":
		return "turkey"
	case "israel":
		return "israel"
	case "cee", "central_eastern_europe":
		return "cee"
	case "global":
		return "global"
	default:
		return value
	}
}

func normalizeLanguageCode(language string) string {
	language = strings.TrimSpace(strings.ToLower(language))
	if language == "" {
		return "und"
	}
	if idx := strings.IndexAny(language, "-_"); idx > 0 {
		language = language[:idx]
	}
	return language
}

func freshnessConfidence(meta *evidence.Metadata) float64 {
	if meta == nil {
		return 0
	}
	switch {
	case meta.FreshnessStatus == "fresh":
		return 1
	case meta.FreshnessStatus == "missing_timestamp":
		return 0.35
	case strings.HasPrefix(meta.FreshnessStatus, "stale"):
		return 0
	default:
		return 0.55
	}
}

func contradictionPenalty(meta *evidence.Metadata) float64 {
	if meta == nil {
		return 0
	}
	penalty := 0.10 * float64(meta.ContradictionCount)
	switch meta.ContradictionSeverity {
	case "high":
		penalty += 0.12
	case "medium":
		penalty += 0.06
	}
	return penalty
}

func detectContradictions(sig signal.Signal, refs []signalRef) (int, string, []string) {
	text := canonicalText(sig)
	if strings.TrimSpace(text) == "" || len(refs) == 0 {
		return 0, "", nil
	}

	currentTokens := contradictionTokenSet(text)
	currentMetrics := extractMetrics(text)
	currentNumbers := extractNumbers(text)
	currentDirection := directionScore(text)
	if len(currentTokens) == 0 || len(currentMetrics) == 0 {
		return 0, "", nil
	}

	conflictingIDs := newOrderedStrings()
	count := 0
	high := 0
	medium := 0

	for _, ref := range refs {
		if ref.id == sig.ID || strings.TrimSpace(ref.text) == "" {
			continue
		}

		refTokens := contradictionTokenSet(ref.text)
		if jaccardTokens(currentTokens, refTokens) < 0.10 {
			continue
		}

		metricsOverlap := intersectTerms(currentMetrics, extractMetrics(ref.text))
		if len(metricsOverlap) == 0 {
			continue
		}

		refDirection := directionScore(ref.text)
		hasDirectionalConflict := currentDirection != 0 && refDirection != 0 && currentDirection != refDirection
		hasNumericConflict := numericConflict(currentNumbers, extractNumbers(ref.text))
		if !hasDirectionalConflict && !hasNumericConflict {
			continue
		}

		conflictingIDs.Add(ref.id)
		count++

		trustFloor := math.Min(ref.trust, sigEvidenceTrust(sig))
		trustGap := math.Abs(ref.trust - sigEvidenceTrust(sig))
		if trustFloor >= 0.65 && trustGap <= 0.20 {
			high++
		} else {
			medium++
		}
	}

	severity := ""
	switch {
	case high > 0 || count >= 2:
		severity = "high"
	case medium > 0:
		severity = "medium"
	}

	return count, severity, conflictingIDs.Last(8)
}

func sigEvidenceTrust(sig signal.Signal) float64 {
	if sig.EvidenceMeta != nil && sig.EvidenceMeta.SourceTrust > 0 {
		return sig.EvidenceMeta.SourceTrust
	}
	return fallbackTrust(sig.Type)
}

func contradictionTokenSet(text string) map[string]struct{} {
	base := tokenSet(text)
	for token := range base {
		if len(token) < 3 {
			delete(base, token)
		}
	}
	return base
}

func extractMetrics(text string) map[string]struct{} {
	lowered := strings.ToLower(text)
	result := make(map[string]struct{}, len(metricTerms))
	for _, metric := range metricTerms {
		if strings.Contains(lowered, metric) {
			result[metric] = struct{}{}
		}
	}
	return result
}

func extractNumbers(text string) []float64 {
	matches := numberPattern.FindAllString(strings.ToLower(text), -1)
	values := make([]float64, 0, len(matches))
	for _, raw := range matches {
		value := parseNumericToken(strings.TrimSuffix(strings.TrimPrefix(raw, "$"), "%"))
		if value > 0 {
			values = append(values, value)
		}
	}
	return values
}

func parseNumericToken(token string) float64 {
	token = strings.ReplaceAll(strings.TrimSpace(strings.ToLower(token)), ",", "")
	if token == "" {
		return 0
	}

	multiplier := 1.0
	switch {
	case strings.HasSuffix(token, "b"):
		multiplier = 1_000_000_000
		token = strings.TrimSuffix(token, "b")
	case strings.HasSuffix(token, "m"):
		multiplier = 1_000_000
		token = strings.TrimSuffix(token, "m")
	case strings.HasSuffix(token, "k"):
		multiplier = 1_000
		token = strings.TrimSuffix(token, "k")
	}

	value, err := strconv.ParseFloat(token, 64)
	if err != nil {
		return 0
	}
	return value * multiplier
}

func directionScore(text string) int {
	lowered := strings.ToLower(text)
	positives := 0
	negatives := 0
	for _, term := range positiveTerms {
		positives += strings.Count(lowered, term)
	}
	for _, term := range negativeTerms {
		negatives += strings.Count(lowered, term)
	}
	switch {
	case positives > negatives:
		return 1
	case negatives > positives:
		return -1
	default:
		return 0
	}
}

func numericConflict(current, other []float64) bool {
	if len(current) == 0 || len(other) == 0 {
		return false
	}

	left := current[0]
	right := other[0]
	maxValue := math.Max(left, right)
	if maxValue <= 0 {
		return false
	}
	return math.Abs(left-right)/maxValue >= 0.20
}

func intersectTerms(left, right map[string]struct{}) []string {
	if len(left) == 0 || len(right) == 0 {
		return nil
	}
	items := make([]string, 0, minInt(len(left), len(right)))
	for term := range left {
		if _, ok := right[term]; ok {
			items = append(items, term)
		}
	}
	sort.Strings(items)
	return items
}

func jaccardTokens(left, right map[string]struct{}) float64 {
	if len(left) == 0 || len(right) == 0 {
		return 0
	}
	intersection := 0
	union := len(left)
	seen := make(map[string]struct{}, len(left)+len(right))
	for token := range left {
		seen[token] = struct{}{}
		if _, ok := right[token]; ok {
			intersection++
		}
	}
	for token := range right {
		if _, ok := seen[token]; !ok {
			union++
		}
	}
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

func clampEvidence(value float64) float64 {
	switch {
	case value < 0:
		return 0
	case value > 1:
		return 1
	default:
		return value
	}
}

func roundEvidence(value float64) float64 {
	return math.Round(value*1000) / 1000
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func countNonEmpty(value string) int {
	if strings.TrimSpace(value) == "" {
		return 0
	}
	return 1
}
