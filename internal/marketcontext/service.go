package marketcontext

import (
	"context"
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/hnic/trading-floor/pkg/model"
	"github.com/hnic/trading-floor/pkg/signal"
)

type PriceView interface {
	LatestPrice(model.Instrument) (float64, bool)
	PriceChange(model.Instrument, time.Duration) (float64, bool)
}

type Service struct {
	prices PriceView
}

type priceResolver interface {
	BestEffortPrice(context.Context, model.Instrument) (model.Instrument, float64, bool)
}

func NewService(prices PriceView) *Service {
	return &Service{
		prices: prices,
	}
}

func (s *Service) BuildOpportunityContext(opp *model.Opportunity, sig signal.Signal) *model.MarketContext {
	if opp == nil {
		return nil
	}

	inst := primaryInstrument(opp.Instruments)
	ctx := &model.MarketContext{
		SnapshotAt:       time.Now().UTC(),
		Instrument:       inst,
		SignalAgeMinutes: signalAgeMinutes(sig.Timestamp),
	}

	if s.prices == nil || inst.Symbol == "" {
		ctx.Notes = append(ctx.Notes, "live price context unavailable")
	} else {
		if price, ok := s.prices.LatestPrice(inst); ok {
			ctx.CurrentPrice = price
		} else {
			ctx.Notes = append(ctx.Notes, "latest price unavailable")
		}
		if change, ok := s.prices.PriceChange(inst, 15*time.Minute); ok {
			ctx.Return15mPct = change
		}
		if change, ok := s.prices.PriceChange(inst, time.Hour); ok {
			ctx.Return1hPct = change
		}
		if change, ok := s.prices.PriceChange(inst, 4*time.Hour); ok {
			ctx.Return4hPct = change
		}
	}

	if extracted := extractConsensusSnapshot(sig); extracted != nil {
		ctx.ConsensusAvailable = true
		ctx.ActualEPS = extracted.EPS
		ctx.EstimatedEPS = extracted.EPSEstimated
		ctx.ActualRevenue = extracted.Revenue
		ctx.EstimatedRevenue = extracted.RevenueEstimated
		ctx.SurpriseMagnitude = extracted.surpriseMagnitude()
	} else {
		ctx.Notes = append(ctx.Notes, "consensus snapshot unavailable")
	}

	ctx.Notes = append(ctx.Notes, "implied move unavailable")
	return ctx
}

func (s *Service) BuildThesisContext(ctx context.Context, thesis *model.Thesis) *model.MarketContext {
	if thesis == nil {
		return nil
	}

	base := cloneMarketContext(thesis.MarketContext)
	inst := thesis.PrimaryInstrument()
	if inst.Symbol == "" && base != nil {
		inst = base.Instrument
	}
	if inst.Symbol == "" {
		return base
	}
	if base == nil {
		base = &model.MarketContext{}
	}

	base.SnapshotAt = time.Now().UTC()
	base.Instrument = inst

	resolvedInst, price, ok := s.bestEffortPrice(ctx, inst)
	if ok && price > 0 {
		base.Instrument = mergeInstrument(inst, resolvedInst)
		base.CurrentPrice = price
	} else if base.CurrentPrice <= 0 {
		base.Notes = appendNote(base.Notes, "latest price unavailable")
	}

	if s.prices != nil {
		priceInst := base.Instrument
		if priceInst.Symbol == "" {
			priceInst = inst
		}
		if change, ok := s.prices.PriceChange(priceInst, 15*time.Minute); ok {
			base.Return15mPct = change
		}
		if change, ok := s.prices.PriceChange(priceInst, time.Hour); ok {
			base.Return1hPct = change
		}
		if change, ok := s.prices.PriceChange(priceInst, 4*time.Hour); ok {
			base.Return4hPct = change
		}
	}

	if !base.ImpliedMoveAvailable {
		base.Notes = appendNote(base.Notes, "implied move unavailable")
	}
	return base
}

type consensusSnapshot struct {
	EPS              float64 `json:"eps"`
	EPSEstimated     float64 `json:"epsEstimated"`
	Revenue          float64 `json:"revenue"`
	RevenueEstimated float64 `json:"revenueEstimated"`
}

func (c consensusSnapshot) surpriseMagnitude() float64 {
	epsMagnitude := relativeSurprise(c.EPS, c.EPSEstimated)
	revenueMagnitude := relativeSurprise(c.Revenue, c.RevenueEstimated)
	switch {
	case epsMagnitude > 0 && revenueMagnitude > 0:
		return (epsMagnitude + revenueMagnitude) / 2
	case epsMagnitude > 0:
		return epsMagnitude
	default:
		return revenueMagnitude
	}
}

func extractConsensusSnapshot(sig signal.Signal) *consensusSnapshot {
	if len(sig.Raw) == 0 {
		return nil
	}
	var snapshot consensusSnapshot
	if err := json.Unmarshal(sig.Raw, &snapshot); err == nil {
		if snapshot.EPSEstimated != 0 || snapshot.RevenueEstimated != 0 {
			return &snapshot
		}
	}
	var wrapped struct {
		Data consensusSnapshot `json:"data"`
	}
	if err := json.Unmarshal(sig.Raw, &wrapped); err == nil {
		if wrapped.Data.EPSEstimated != 0 || wrapped.Data.RevenueEstimated != 0 {
			return &wrapped.Data
		}
	}
	return nil
}

func (s *Service) bestEffortPrice(ctx context.Context, inst model.Instrument) (model.Instrument, float64, bool) {
	if s.prices == nil || inst.Symbol == "" {
		return model.Instrument{}, 0, false
	}
	if resolver, ok := s.prices.(priceResolver); ok {
		if resolved, price, ok := resolver.BestEffortPrice(ctx, inst); ok {
			return resolved, price, true
		}
	}
	if price, ok := s.prices.LatestPrice(inst); ok && price > 0 {
		return inst, price, true
	}
	return model.Instrument{}, 0, false
}

func primaryInstrument(instruments []model.Instrument) model.Instrument {
	if len(instruments) == 0 {
		return model.Instrument{}
	}
	return instruments[0]
}

func signalAgeMinutes(ts time.Time) float64 {
	if ts.IsZero() {
		return 0
	}
	age := time.Since(ts).Minutes()
	if age < 0 {
		return 0
	}
	return age
}

func relativeSurprise(actual, estimate float64) float64 {
	if actual == 0 || estimate == 0 {
		return 0
	}
	return math.Abs(actual-estimate) / math.Abs(estimate)
}

func FormatForPrompt(ctx *model.MarketContext) string {
	if ctx == nil {
		return ""
	}

	lines := make([]string, 0, 8)
	if ctx.Instrument.Symbol != "" {
		lines = append(lines, "Primary instrument: "+ctx.Instrument.Label())
	}
	if ctx.CurrentPrice > 0 {
		lines = append(lines, "Current price: "+formatFloat(ctx.CurrentPrice))
	}
	if ctx.SignalAgeMinutes > 0 {
		lines = append(lines, "Signal age minutes: "+formatFloat(ctx.SignalAgeMinutes))
	}
	if ctx.Return15mPct != 0 {
		lines = append(lines, "Return 15m pct: "+formatFloat(ctx.Return15mPct))
	}
	if ctx.Return1hPct != 0 {
		lines = append(lines, "Return 1h pct: "+formatFloat(ctx.Return1hPct))
	}
	if ctx.Return4hPct != 0 {
		lines = append(lines, "Return 4h pct: "+formatFloat(ctx.Return4hPct))
	}
	if ctx.ConsensusAvailable {
		lines = append(lines, "Consensus snapshot: eps="+formatFloat(ctx.ActualEPS)+" vs est="+formatFloat(ctx.EstimatedEPS)+", revenue="+formatFloat(ctx.ActualRevenue)+" vs est="+formatFloat(ctx.EstimatedRevenue))
		lines = append(lines, "Surprise magnitude: "+formatFloat(ctx.SurpriseMagnitude))
	}
	if len(ctx.Notes) > 0 {
		lines = append(lines, "Context gaps: "+strings.Join(ctx.Notes, ", "))
	}
	return strings.Join(lines, "\n")
}

func formatFloat(value float64) string {
	return strings.TrimRight(strings.TrimRight(strconv.FormatFloat(value, 'f', 4, 64), "0"), ".")
}

func cloneMarketContext(ctx *model.MarketContext) *model.MarketContext {
	if ctx == nil {
		return nil
	}
	clone := *ctx
	if len(ctx.Notes) > 0 {
		clone.Notes = append([]string(nil), ctx.Notes...)
	}
	return &clone
}

func mergeInstrument(primary, resolved model.Instrument) model.Instrument {
	merged := resolved
	if merged.Symbol == "" {
		merged.Symbol = primary.Symbol
	}
	if merged.SecType == "" {
		merged.SecType = primary.SecType
	}
	if merged.Exchange == "" {
		merged.Exchange = primary.Exchange
	}
	if merged.Currency == "" {
		merged.Currency = primary.Currency
	}
	if merged.Expiry == "" {
		merged.Expiry = primary.Expiry
	}
	if merged.Strike <= 0 {
		merged.Strike = primary.Strike
	}
	if merged.Right == "" {
		merged.Right = primary.Right
	}
	if merged.Multiplier == "" {
		merged.Multiplier = primary.Multiplier
	}
	if merged.ConID == 0 {
		merged.ConID = primary.ConID
	}
	return merged
}

func appendNote(notes []string, note string) []string {
	note = strings.TrimSpace(note)
	if note == "" {
		return notes
	}
	for _, existing := range notes {
		if existing == note {
			return notes
		}
	}
	return append(notes, note)
}
