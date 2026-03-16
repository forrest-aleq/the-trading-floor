package marketcontext

import (
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
