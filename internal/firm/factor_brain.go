package firm

import (
	"math"
	"sort"
	"strings"

	"github.com/hnic/trading-floor/pkg/model"
)

type factorContribution struct {
	DeskID string
	Domain string
	Gross  float64
	Net    float64
}

type factorExposure struct {
	Factor            string
	Gross             float64
	Net               float64
	GrossPctNAV       float64
	NetPctNAV         float64
	DeskCount         int
	DeskContributions map[string]factorContribution
}

func (c *CEO) factorExposures(nav float64, positions []*model.Position) []factorExposure {
	policy := activeFactorPolicy()
	domains := c.deskDomains()
	factors := make(map[string]*factorExposure)

	for _, pos := range positions {
		if pos == nil || pos.Status != "open" || pos.Shadow {
			continue
		}

		domain := strings.ToLower(strings.TrimSpace(domains[pos.DeskID]))
		gross := pos.GrossExposure()
		net := pos.SignedExposure()
		for _, rule := range resolveFactorRules(policy, domain, pos) {
			exposure := factors[rule.ID]
			if exposure == nil {
				exposure = &factorExposure{
					Factor:            rule.ID,
					DeskContributions: make(map[string]factorContribution),
				}
				factors[rule.ID] = exposure
			}
			weightedGross := gross * rule.Weight
			weightedNet := net * rule.Weight
			exposure.Gross += weightedGross
			exposure.Net += weightedNet
			contrib := exposure.DeskContributions[pos.DeskID]
			contrib.DeskID = pos.DeskID
			contrib.Domain = domain
			contrib.Gross += weightedGross
			contrib.Net += weightedNet
			exposure.DeskContributions[pos.DeskID] = contrib
		}
	}

	result := make([]factorExposure, 0, len(factors))
	for _, exposure := range factors {
		exposure.DeskCount = len(exposure.DeskContributions)
		if nav > 0 {
			exposure.GrossPctNAV = (exposure.Gross / nav) * 100
			exposure.NetPctNAV = (exposure.Net / nav) * 100
		}
		result = append(result, *exposure)
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].Gross == result[j].Gross {
			return result[i].Factor < result[j].Factor
		}
		return result[i].Gross > result[j].Gross
	})
	return result
}

func resolveFactorRules(policy factorPolicy, domain string, pos *model.Position) []factorRule {
	if pos == nil {
		return nil
	}
	inst := pos.PrimaryInstrument()
	secType := strings.ToUpper(strings.TrimSpace(inst.SecType))
	symbol := strings.ToUpper(strings.TrimSpace(inst.Symbol))
	structure := strings.ToLower(strings.TrimSpace(pos.Structure))
	currency := strings.ToUpper(strings.TrimSpace(inst.Currency))

	matches := make([]factorRule, 0, len(policy.Rules))
	for _, rule := range policy.Rules {
		if !matchesFactorRule(rule, domain, secType, symbol, structure, currency) {
			continue
		}
		matches = append(matches, rule)
	}
	return matches
}

func matchesFactorRule(rule factorRule, domain, secType, symbol, structure, currency string) bool {
	if len(rule.Domains) > 0 && !containsString(rule.Domains, domain) {
		return false
	}
	if len(rule.SecTypes) > 0 && !containsString(rule.SecTypes, secType) {
		return false
	}
	if len(rule.Symbols) > 0 && !containsString(rule.Symbols, symbol) {
		return false
	}
	if len(rule.Structures) > 0 && !containsString(rule.Structures, structure) {
		return false
	}
	if len(rule.Currencies) > 0 && !containsString(rule.Currencies, currency) {
		return false
	}
	return true
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func (c *CEO) deskDomains() map[string]string {
	byID := make(map[string]string, len(c.desks))
	for _, desk := range c.desks {
		if desk == nil {
			continue
		}
		byID[desk.ID] = desk.Domain
	}
	return byID
}

func (c *CEO) crowdedFactorPenalties(nav float64, positions []*model.Position) map[string]float64 {
	policy := activeFactorPolicy()
	exposures := c.factorExposures(nav, positions)
	penalties := make(map[string]float64)
	if nav <= 0 {
		return penalties
	}

	for _, factor := range exposures {
		if factor.GrossPctNAV < policy.AlertGrossExposurePct || factor.DeskCount < policy.HiddenOverlapDeskCount {
			continue
		}
		crowdStrength := clampFactor((factor.GrossPctNAV - policy.AlertGrossExposurePct) / policy.AlertGrossExposurePct)
		for deskID, contrib := range factor.DeskContributions {
			if factor.Gross <= 0 {
				continue
			}
			share := contrib.Gross / factor.Gross
			penalties[deskID] += share * crowdStrength * policy.ReallocationPenalty
		}
	}

	for deskID, penalty := range penalties {
		penalties[deskID] = clampFactor(penalty)
	}
	return penalties
}

func clampFactor(value float64) float64 {
	return math.Max(0, math.Min(1, value))
}
