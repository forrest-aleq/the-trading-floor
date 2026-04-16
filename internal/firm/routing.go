package firm

import (
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
