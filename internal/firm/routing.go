package firm

import (
	"sort"
	"strings"

	"github.com/hnic/trading-floor/pkg/signal"
)

// domainShouldReviewSignal applies a deterministic pre-filter before the LLM
// scanner so irrelevant desks do not burn inference budget on every signal.
func domainShouldReviewSignal(domain string, sig signal.Signal) bool {
	return ShouldDomainReviewSignal(domain, sig)
}

// ShouldDomainReviewSignal applies the same deterministic intake filter used by
// the live floor. Replay/backfill paths should call this instead of inventing
// their own routing logic.
func ShouldDomainReviewSignal(domain string, sig signal.Signal) bool {
	relevant := relevantDomainsForSignal(sig)
	if len(relevant) == 0 {
		return true
	}
	for _, candidate := range relevant {
		if candidate == domain {
			return true
		}
	}
	return false
}

// ShouldDeskReviewSignal applies desk-specific deterministic intake rules after
// broad domain routing so each desk only spends inference on the signal
// families it is meant to cover.
func ShouldDeskReviewSignal(deskID, domain string, sig signal.Signal) bool {
	allowed, _ := deskRoutingDecision(deskID, domain, sig)
	return allowed
}

func relevantDomainsForSignal(sig signal.Signal) []string {
	return RelevantDomainsForSignal(sig)
}

// RelevantDomainsForSignal returns the desk domains that should see a signal
// before any LLM work is spent on it.
func RelevantDomainsForSignal(sig signal.Signal) []string {
	policy := activeRoutingPolicy()
	set := newDomainSet()

	if targets := internalTargetDomains(sig); len(targets) > 0 {
		set.add(targets...)
		return set.values()
	}

	set.add(sourceDomainsForSignal(sig)...)

	set.add(policy.CategoryDomainRules[strings.TrimSpace(strings.ToLower(sig.Category))]...)
	set.add(policy.SignalTypeDomainRules[strings.TrimSpace(strings.ToLower(string(sig.Type)))]...)

	if len(set.values()) == 0 {
		set.add(policy.FallbackDomains...)
	}

	return set.values()
}

func sourceDomainsForSignal(sig signal.Signal) []string {
	policy := activeRoutingPolicy()
	meta := sig.EvidenceMeta
	if meta == nil {
		return nil
	}

	set := newDomainSet()
	sourceType := strings.TrimSpace(strings.ToLower(meta.SourceType))
	ownerGroup := strings.TrimSpace(strings.ToLower(meta.SourceOwnerGroup))
	originRegion := strings.TrimSpace(strings.ToLower(meta.OriginRegion))

	set.add(policy.SourceTypeDomainRules[sourceType]...)
	if sourceType == "market" && instrumentEntityCount(sig) > 0 {
		set.add("sector")
	}

	set.add(policy.OwnerGroupDomainRules[ownerGroup]...)

	if hasEntityType(sig, "country") {
		set.add("geopolitical", "macro", "tail")
	}

	if originRegion != "" && originRegion != "us" && originRegion != "global" {
		set.add("geopolitical", "macro")
	}

	if instrumentEntityCount(sig) >= 2 {
		set.add("flows", "systematic")
	}

	return set.values()
}

func deskRoutingDecision(deskID, domain string, sig signal.Signal) (bool, int) {
	if !ShouldDomainReviewSignal(domain, sig) {
		return false, 0
	}
	if targets := internalTargetDomains(sig); len(targets) > 0 {
		return true, 0
	}

	policy := activeRoutingPolicy()
	rule, ok := policy.DeskRules[strings.TrimSpace(strings.ToLower(deskID))]
	if !ok {
		return true, 0
	}
	matched, priority := rule.matches(sig)
	if !matched {
		return false, 0
	}
	return true, priority
}

func selectDeskTargetsForSignal(desks []*Desk, sig signal.Signal) []*Desk {
	policy := activeRoutingPolicy()
	candidates := make([]deskCandidate, 0, len(desks))
	for _, desk := range desks {
		if desk == nil {
			continue
		}
		if originDesk := internalOriginDesk(sig); originDesk != "" && originDesk == desk.ID {
			continue
		}
		matched, priority := deskRoutingDecision(desk.ID, desk.Domain, sig)
		if !matched {
			continue
		}
		candidates = append(candidates, deskCandidate{desk: desk, priority: priority})
	}
	if len(candidates) == 0 {
		return nil
	}

	if len(policy.DomainGroupMaxMatches) == 0 {
		return desksFromCandidates(candidates)
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].desk.Domain != candidates[j].desk.Domain {
			return candidates[i].desk.Domain < candidates[j].desk.Domain
		}
		if candidates[i].desk.ABGroup != candidates[j].desk.ABGroup {
			return candidates[i].desk.ABGroup < candidates[j].desk.ABGroup
		}
		if candidates[i].priority != candidates[j].priority {
			return candidates[i].priority > candidates[j].priority
		}
		return candidates[i].desk.ID < candidates[j].desk.ID
	})

	counts := make(map[string]int, len(candidates))
	selected := make([]*Desk, 0, len(candidates))
	for _, candidate := range candidates {
		key := candidate.desk.Domain + "::" + candidate.desk.ABGroup
		if max := policy.DomainGroupMaxMatches[candidate.desk.Domain]; max > 0 {
			if counts[key] >= max {
				continue
			}
		}
		counts[key]++
		selected = append(selected, candidate.desk)
	}

	return selected
}

type deskCandidate struct {
	desk     *Desk
	priority int
}

func desksFromCandidates(candidates []deskCandidate) []*Desk {
	selected := make([]*Desk, 0, len(candidates))
	for _, candidate := range candidates {
		selected = append(selected, candidate.desk)
	}
	return selected
}

func (r deskRule) matches(sig signal.Signal) (bool, int) {
	searchText := signalSearchText(sig)
	meta := sig.EvidenceMeta
	selectorCount := 0

	if len(r.Categories) > 0 {
		selectorCount++
		if !routingContainsString(r.Categories, strings.TrimSpace(strings.ToLower(sig.Category))) {
			return false, 0
		}
	}
	if len(r.SignalTypes) > 0 {
		selectorCount++
		if !routingContainsString(r.SignalTypes, strings.TrimSpace(strings.ToLower(string(sig.Type)))) {
			return false, 0
		}
	}
	if len(r.SourcePrefixes) > 0 {
		selectorCount++
		if !matchesSourcePrefix(r.SourcePrefixes, sig.Source) {
			return false, 0
		}
	}
	if len(r.SourceTypes) > 0 {
		selectorCount++
		sourceType := ""
		if meta != nil {
			sourceType = strings.TrimSpace(strings.ToLower(meta.SourceType))
		}
		if !routingContainsString(r.SourceTypes, sourceType) {
			return false, 0
		}
	}
	if len(r.SourceOwnerGroups) > 0 {
		selectorCount++
		ownerGroup := ""
		if meta != nil {
			ownerGroup = strings.TrimSpace(strings.ToLower(meta.SourceOwnerGroup))
		}
		if !routingContainsString(r.SourceOwnerGroups, ownerGroup) {
			return false, 0
		}
	}
	if len(r.OriginRegions) > 0 {
		selectorCount++
		region := ""
		if meta != nil {
			region = strings.TrimSpace(strings.ToLower(meta.OriginRegion))
		}
		if !routingContainsString(r.OriginRegions, region) {
			return false, 0
		}
	}
	if len(r.EntityTypes) > 0 {
		selectorCount++
		if !signalHasEntityType(sig, r.EntityTypes) {
			return false, 0
		}
	}
	if len(r.EntityIDs) > 0 {
		selectorCount++
		if !signalHasEntityID(sig, r.EntityIDs) {
			return false, 0
		}
	}
	if len(r.EntityNames) > 0 {
		selectorCount++
		if !signalHasEntityName(sig, r.EntityNames) {
			return false, 0
		}
	}
	if len(r.KeywordsAny) > 0 {
		selectorCount++
		if !containsAnyKeyword(searchText, r.KeywordsAny) {
			return false, 0
		}
	}
	if len(r.KeywordsAll) > 0 {
		selectorCount++
		if !containsAllKeywords(searchText, r.KeywordsAll) {
			return false, 0
		}
	}
	if len(r.ExcludeKeywords) > 0 && containsAnyKeyword(searchText, r.ExcludeKeywords) {
		return false, 0
	}
	if r.MinUrgency > 0 {
		selectorCount++
		if sig.Urgency < r.MinUrgency {
			return false, 0
		}
	}
	if selectorCount == 0 {
		return true, r.Priority
	}
	return true, r.Priority
}

func signalSearchText(sig signal.Signal) string {
	parts := []string{
		sig.Source,
		sig.Category,
		sig.OriginalText,
		sig.Translated,
		string(sig.Raw),
	}
	for _, entity := range sig.Entities {
		parts = append(parts, entity.Name, entity.ID, entity.Type)
	}
	return strings.ToLower(strings.Join(parts, "\n"))
}

func routingContainsString(values []string, needle string) bool {
	needle = strings.TrimSpace(strings.ToLower(needle))
	if needle == "" {
		return false
	}
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func matchesSourcePrefix(prefixes []string, source string) bool {
	source = strings.TrimSpace(strings.ToLower(source))
	if source == "" {
		return false
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(source, prefix) {
			return true
		}
	}
	return false
}

func signalHasEntityType(sig signal.Signal, values []string) bool {
	for _, entity := range sig.Entities {
		if routingContainsString(values, entity.Type) {
			return true
		}
	}
	return false
}

func signalHasEntityID(sig signal.Signal, values []string) bool {
	for _, entity := range sig.Entities {
		if routingContainsString(values, entity.ID) {
			return true
		}
	}
	return false
}

func signalHasEntityName(sig signal.Signal, values []string) bool {
	for _, entity := range sig.Entities {
		name := strings.TrimSpace(strings.ToLower(entity.Name))
		if name == "" {
			continue
		}
		for _, value := range values {
			if strings.Contains(name, value) {
				return true
			}
		}
	}
	return false
}

func containsAnyKeyword(text string, keywords []string) bool {
	for _, keyword := range keywords {
		if strings.Contains(text, keyword) {
			return true
		}
	}
	return false
}

func containsAllKeywords(text string, keywords []string) bool {
	for _, keyword := range keywords {
		if !strings.Contains(text, keyword) {
			return false
		}
	}
	return true
}

func hasEntityType(sig signal.Signal, entityType string) bool {
	entityType = strings.TrimSpace(strings.ToLower(entityType))
	if entityType == "" {
		return false
	}
	for _, entity := range sig.Entities {
		if strings.TrimSpace(strings.ToLower(entity.Type)) == entityType {
			return true
		}
	}
	return false
}

func instrumentEntityCount(sig signal.Signal) int {
	count := 0
	for _, entity := range sig.Entities {
		if strings.TrimSpace(strings.ToLower(entity.Type)) == "instrument" {
			count++
		}
	}
	return count
}

type domainSet struct {
	valuesInOrder []string
	seen          map[string]struct{}
}

func newDomainSet() *domainSet {
	return &domainSet{
		valuesInOrder: make([]string, 0, 8),
		seen:          make(map[string]struct{}, 8),
	}
}

func (s *domainSet) add(values ...string) {
	for _, value := range values {
		value = strings.TrimSpace(strings.ToLower(value))
		if value == "" {
			continue
		}
		if _, ok := s.seen[value]; ok {
			continue
		}
		s.seen[value] = struct{}{}
		s.valuesInOrder = append(s.valuesInOrder, value)
	}
}

func (s *domainSet) values() []string {
	return append([]string(nil), s.valuesInOrder...)
}
