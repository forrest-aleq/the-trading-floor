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

type quoteView interface {
	LatestQuote(model.Instrument) (model.MarketQuote, bool)
}

type realizedVolatilityView interface {
	RealizedVolatility(model.Instrument, time.Duration) (float64, bool)
}

type quoteResolver interface {
	BestEffortQuote(context.Context, model.Instrument) (model.Instrument, model.MarketQuote, bool)
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
		s.applyQuoteState(ctx, inst)
		s.applyRealizedVolatility(ctx, inst)
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
		if resolvedQuoteInst, quote, ok := s.bestEffortQuote(ctx, priceInst); ok {
			base.Instrument = mergeInstrument(priceInst, resolvedQuoteInst)
			applyMarketQuote(base, quote)
			priceInst = base.Instrument
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
		s.applyQuoteState(base, priceInst)
		s.applyRealizedVolatility(base, priceInst)
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

func (s *Service) bestEffortQuote(ctx context.Context, inst model.Instrument) (model.Instrument, model.MarketQuote, bool) {
	if s == nil || s.prices == nil || inst.Symbol == "" {
		return model.Instrument{}, model.MarketQuote{}, false
	}
	if resolver, ok := s.prices.(quoteResolver); ok {
		if resolved, quote, ok := resolver.BestEffortQuote(ctx, inst); ok {
			return resolved, quote, true
		}
	}
	if cached, ok := s.prices.(quoteView); ok {
		if quote, ok := cached.LatestQuote(inst); ok {
			return inst, quote, true
		}
	}
	return model.Instrument{}, model.MarketQuote{}, false
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

func (s *Service) applyQuoteState(ctx *model.MarketContext, inst model.Instrument) {
	if s == nil || s.prices == nil || ctx == nil || inst.Symbol == "" {
		return
	}
	view, ok := s.prices.(quoteView)
	if !ok {
		ctx.Notes = appendNote(ctx.Notes, "live quote unavailable")
		return
	}
	quote, ok := view.LatestQuote(inst)
	if !ok {
		ctx.Notes = appendNote(ctx.Notes, "live quote unavailable")
		return
	}
	applyMarketQuote(ctx, quote)
}

func applyMarketQuote(ctx *model.MarketContext, quote model.MarketQuote) {
	if ctx == nil || quote.ReferencePrice() <= 0 {
		return
	}
	ctx.BidPrice = quote.Bid
	ctx.AskPrice = quote.Ask
	ctx.MidPrice = quote.MidPrice()
	ctx.SpreadBps = quote.SpreadBps()
	ctx.LastVolume = quote.Volume
	if !quote.ObservedAt.IsZero() {
		ageSeconds := time.Since(quote.ObservedAt).Seconds()
		if ageSeconds > 0 {
			ctx.QuoteAgeSeconds = ageSeconds
		}
	}
	if ctx.CurrentPrice <= 0 {
		ctx.CurrentPrice = quote.ReferencePrice()
	}
}

func (s *Service) applyRealizedVolatility(ctx *model.MarketContext, inst model.Instrument) {
	if s == nil || s.prices == nil || ctx == nil || inst.Symbol == "" {
		return
	}
	view, ok := s.prices.(realizedVolatilityView)
	if !ok {
		ctx.Notes = appendNote(ctx.Notes, "realized volatility unavailable")
		return
	}
	if vol, ok := view.RealizedVolatility(inst, 24*time.Hour); ok {
		ctx.RealizedVol1dPct = vol
	}
	if vol, ok := view.RealizedVolatility(inst, 5*24*time.Hour); ok {
		ctx.RealizedVol5dPct = vol
	}
	if ctx.RealizedVol1dPct <= 0 && ctx.RealizedVol5dPct <= 0 {
		ctx.Notes = appendNote(ctx.Notes, "realized volatility unavailable")
	}
}

func FormatForPrompt(ctx *model.MarketContext) string {
	if ctx == nil {
		return ""
	}

	lines := make([]string, 0, 12)
	if ctx.Instrument.Symbol != "" {
		lines = append(lines, "Primary instrument: "+ctx.Instrument.Label())
	}
	if ctx.CurrentPrice > 0 {
		lines = append(lines, "Current price: "+formatFloat(ctx.CurrentPrice))
	}
	if ctx.BidPrice > 0 || ctx.AskPrice > 0 {
		lines = append(lines, "Top of book: bid="+formatFloat(ctx.BidPrice)+" ask="+formatFloat(ctx.AskPrice)+" mid="+formatFloat(ctx.MidPrice)+" spread_bps="+formatFloat(ctx.SpreadBps))
	}
	if ctx.LastVolume > 0 {
		lines = append(lines, "Last observed volume: "+strconv.FormatInt(ctx.LastVolume, 10))
	}
	if ctx.QuoteAgeSeconds > 0 {
		lines = append(lines, "Quote age seconds: "+formatFloat(ctx.QuoteAgeSeconds))
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
	if ctx.RealizedVol1dPct != 0 {
		lines = append(lines, "Realized vol 1d pct: "+formatFloat(ctx.RealizedVol1dPct))
	}
	if ctx.RealizedVol5dPct != 0 {
		lines = append(lines, "Realized vol 5d pct: "+formatFloat(ctx.RealizedVol5dPct))
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
