package firm

import (
	"strings"

	"github.com/hnic/trading-floor/pkg/signal"
)

// domainShouldReviewSignal applies a deterministic pre-filter before the LLM
// scanner so irrelevant desks do not burn inference budget on every signal.
func domainShouldReviewSignal(domain string, sig signal.Signal) bool {
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
	set := newDomainSet()

	if targets := internalTargetDomains(sig); len(targets) > 0 {
		set.add(targets...)
		return set.values()
	}

	set.add(sourceDomainsForSignal(sig)...)

	switch strings.TrimSpace(strings.ToLower(sig.Category)) {
	case "geopolitical":
		set.add("geopolitical", "tail")
	case "macro":
		set.add("macro", "tail")
	case "corporate":
		set.add("corporate", "sector")
	case "flows":
		set.add("flows", "volatility")
	case "tail":
		set.add("tail", "volatility", "macro", "geopolitical")
	case "volatility":
		set.add("volatility", "tail")
	case "sector":
		set.add("sector", "corporate")
	case "market":
		set.add("systematic", "volatility", "sector")
	}

	switch sig.Type {
	case signal.TypeFiling:
		set.add("corporate")
	case signal.TypeEconomic:
		set.add("macro", "tail")
	case signal.TypePrice:
		set.add("systematic", "volatility", "sector")
	case signal.TypeSocial, signal.TypeFlow:
		set.add("flows", "volatility")
	case signal.TypeAlternative:
		set.add("macro", "sector")
	}

	if len(set.values()) == 0 {
		set.add("macro", "systematic")
	}

	return set.values()
}

func sourceDomainsForSignal(sig signal.Signal) []string {
	source := strings.TrimSpace(strings.ToLower(sig.Source))
	if source == "" {
		return nil
	}

	set := newDomainSet()

	switch {
	case strings.HasPrefix(source, "fred"):
		set.add("macro", "tail", "systematic")
	case strings.HasPrefix(source, "fed-"):
		set.add("macro", "tail")
	case strings.Contains(source, "world"), strings.Contains(source, "politico"), strings.Contains(source, "le-monde"):
		set.add("geopolitical", "macro", "tail")
	case strings.Contains(source, "markets"):
		set.add("macro", "volatility", "systematic")
	case strings.Contains(source, "companies"), strings.Contains(source, "equities"), strings.Contains(source, "edgar"), strings.Contains(source, "earnings"):
		set.add("corporate", "sector")
	case strings.Contains(source, "finextra"), strings.Contains(source, "fintech"):
		set.add("corporate", "sector", "systematic")
	case strings.HasPrefix(source, "stocktwits"), strings.HasPrefix(source, "reddit/"):
		set.add("flows", "volatility")
	case strings.HasPrefix(source, "ibkr-market"):
		set.add("systematic", "volatility")
		if instrumentEntityCount(sig) > 0 {
			set.add("sector")
		}
	}

	if hasEntityType(sig, "country") {
		set.add("geopolitical", "macro", "tail")
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
