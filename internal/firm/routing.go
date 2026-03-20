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

	return set.values()
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
