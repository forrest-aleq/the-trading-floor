package firm

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/hnic/trading-floor/pkg/model"
	"github.com/hnic/trading-floor/pkg/signal"
)

func buildInternalSignal(origin signal.Signal, thesis *model.Thesis, originDesk string) (signal.Signal, bool) {
	if thesis == nil || thesis.Conviction < 0.72 {
		return signal.Signal{}, false
	}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(origin.Source)), "internal/") && internalSignalDepth(origin) >= 1 {
		return signal.Signal{}, false
	}

	targets := downstreamDomainsForDesk(thesis.Domain)
	if len(targets) == 0 {
		return signal.Signal{}, false
	}

	text := fmt.Sprintf(
		"Internal thesis from %s desk: %s %s structure=%s strategy=%s conviction=%.2f",
		thesis.Domain,
		thesis.DisplaySymbol(),
		thesis.Direction,
		firstNonEmptyInternal(thesis.Structure, "single"),
		thesis.Strategy,
		thesis.Conviction,
	)
	payload := map[string]any{
		"origin_desk":      originDesk,
		"origin_domain":    thesis.Domain,
		"origin_signal_id": origin.ID,
		"thesis_id":        thesis.ID,
		"target_domains":   targets,
		"structure":        thesis.Structure,
		"strategy":         thesis.Strategy,
		"conviction":       thesis.Conviction,
		"internal_depth":   internalSignalDepth(origin) + 1,
	}
	raw, _ := json.Marshal(payload)

	return signal.Signal{
		ID:                    "internal-" + thesis.ID,
		Source:                "internal/" + originDesk,
		Type:                  signal.TypeAlternative,
		Category:              thesis.Domain,
		Timestamp:             time.Now().UTC(),
		Urgency:               maxInternal(thesis.Conviction, 0.55),
		Strength:              thesis.Conviction,
		Direction:             signalDirectionFromTradeDirection(thesis.Direction),
		Entities:              internalSignalEntities(thesis),
		Languages:             []string{"en"},
		Raw:                   raw,
		OriginalText:          text,
		Translated:            text,
		TranslationProvider:   "internal_identity",
		TranslationConfidence: 1,
	}, true
}

func downstreamDomainsForDesk(domain string) []string {
	set := newDomainSet()
	switch strings.TrimSpace(strings.ToLower(domain)) {
	case "geopolitical":
		set.add("macro", "tail", "volatility", "sector")
	case "macro":
		set.add("volatility", "tail", "systematic", "sector")
	case "corporate":
		set.add("sector", "flows", "volatility")
	case "flows":
		set.add("volatility", "systematic", "sector")
	case "tail":
		set.add("macro", "volatility", "geopolitical", "systematic")
	case "volatility":
		set.add("flows", "tail", "systematic")
	case "sector":
		set.add("corporate", "flows", "systematic")
	case "systematic":
		set.add("flows", "volatility", "macro", "sector")
	}
	return set.values()
}

func internalTargetDomains(sig signal.Signal) []string {
	if len(sig.Raw) == 0 {
		return nil
	}
	var payload struct {
		TargetDomains []string `json:"target_domains"`
	}
	if err := json.Unmarshal(sig.Raw, &payload); err != nil {
		return nil
	}
	set := newDomainSet()
	set.add(payload.TargetDomains...)
	return set.values()
}

func internalOriginDesk(sig signal.Signal) string {
	if len(sig.Raw) == 0 {
		return ""
	}
	var payload struct {
		OriginDesk string `json:"origin_desk"`
	}
	if err := json.Unmarshal(sig.Raw, &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.OriginDesk)
}

func internalSignalDepth(sig signal.Signal) int {
	if len(sig.Raw) == 0 {
		return 0
	}
	var payload struct {
		Depth int `json:"internal_depth"`
	}
	if err := json.Unmarshal(sig.Raw, &payload); err != nil {
		return 0
	}
	if payload.Depth < 0 {
		return 0
	}
	return payload.Depth
}

func internalSignalEntities(thesis *model.Thesis) []signal.Entity {
	if thesis == nil {
		return nil
	}
	instruments := thesis.ExecutionInstruments()
	entities := make([]signal.Entity, 0, len(instruments))
	seen := make(map[string]struct{}, len(instruments))
	for _, inst := range instruments {
		symbol := strings.TrimSpace(inst.Symbol)
		if symbol == "" {
			continue
		}
		if _, ok := seen[symbol]; ok {
			continue
		}
		seen[symbol] = struct{}{}
		entities = append(entities, signal.Entity{Name: symbol, Type: "instrument"})
	}
	return entities
}

func signalDirectionFromTradeDirection(direction model.TradeDirection) signal.Direction {
	if direction == model.Short {
		return signal.Bearish
	}
	return signal.Bullish
}

func maxInternal(values ...float64) float64 {
	maximum := 0.0
	for _, value := range values {
		if value > maximum {
			maximum = value
		}
	}
	return maximum
}

func firstNonEmptyInternal(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
