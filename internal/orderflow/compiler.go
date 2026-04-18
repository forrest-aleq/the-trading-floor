package orderflow

import (
	"fmt"
	"strings"
	"time"

	"github.com/hnic/trading-floor/pkg/model"
)

const defaultTimeInForce = "DAY"

const (
	maxSmartQuoteAgeSeconds      = 120.0
	adaptiveMaxSpreadBps         = 5.0
	midPriceMaxSpreadBps         = 40.0
	explicitMidpriceToleranceBps = 15.0
)

// Compiler deterministically translates theses and positions into executable orders.
// Agentic reasoning stops at the thesis; order construction happens here.
type Compiler struct{}

func NewCompiler() *Compiler {
	return &Compiler{}
}

type EntryInput struct {
	DeskID  string
	Thesis  *model.Thesis
	Units   float64
	ExitTIF string
}

func (c *Compiler) CompileEntry(input EntryInput) (*model.Order, error) {
	if input.Thesis == nil {
		return nil, fmt.Errorf("nil thesis")
	}
	if strings.TrimSpace(input.DeskID) == "" {
		return nil, fmt.Errorf("desk id required")
	}

	thesis := input.Thesis
	instrument := thesis.PrimaryInstrument()
	if instrument.Symbol == "" {
		return nil, fmt.Errorf("primary instrument required")
	}

	quantity := input.Units
	if quantity <= 0 {
		quantity = thesis.PositionSize
	}
	if quantity <= 0 {
		return nil, fmt.Errorf("position size must be positive")
	}

	orderType, limitPrice := chooseEntryOrder(thesis)

	notional := thesisNotional(thesis, quantity)

	order := &model.Order{
		ID:              thesis.ID,
		ThesisID:        thesis.ID,
		DeskID:          input.DeskID,
		Structure:       firstNonEmpty(thesis.Structure, "single"),
		Instrument:      instrument,
		Legs:            cloneLegs(thesis.Legs),
		Direction:       thesis.Direction,
		Quantity:        quantity,
		OrderType:       orderType,
		LimitPrice:      limitPrice,
		StopPrice:       thesis.StopLoss,
		TimeInForce:     firstNonEmpty(input.ExitTIF, defaultTimeInForce),
		Notional:        notional,
		ExecutionIntent: buildExecutionIntent(thesis, orderType),
	}
	return order, nil
}

func (c *Compiler) CompileExit(pos *model.Position) (*model.Order, error) {
	if pos == nil {
		return nil, fmt.Errorf("nil position")
	}
	if pos.ID == "" {
		return nil, fmt.Errorf("position id required")
	}
	if pos.Quantity <= 0 {
		return nil, fmt.Errorf("position quantity must be positive")
	}

	order := &model.Order{
		ID:          pos.ID + "-close",
		ThesisID:    pos.ThesisID,
		DeskID:      pos.DeskID,
		Structure:   firstNonEmpty(pos.Structure, "single"),
		Instrument:  pos.PrimaryInstrument(),
		Legs:        cloneLegs(pos.Legs),
		Direction:   opposite(pos.Direction),
		Quantity:    pos.Quantity,
		OrderType:   model.OrderMarket,
		TimeInForce: defaultTimeInForce,
	}
	return order, nil
}

func cloneLegs(legs []model.TradeLeg) []model.TradeLeg {
	if len(legs) == 0 {
		return nil
	}
	cloned := make([]model.TradeLeg, len(legs))
	copy(cloned, legs)
	return cloned
}

func opposite(direction model.TradeDirection) model.TradeDirection {
	if direction == model.Short {
		return model.Long
	}
	return model.Short
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func thesisNotional(thesis *model.Thesis, quantity float64) float64 {
	if thesis == nil {
		return 0
	}
	originalEntry := thesis.EntryPrice
	if originalEntry <= 0 {
		thesis.EntryPrice = thesisReferencePrice(thesis)
	}
	notional := thesis.GrossEntryNotional(quantity)
	thesis.EntryPrice = originalEntry
	return notional
}

func thesisReferencePrice(thesis *model.Thesis) float64 {
	if thesis == nil {
		return 0
	}
	candidates := []float64{thesis.EntryPrice}
	if thesis.MarketContext != nil {
		candidates = append(candidates, thesis.MarketContext.CurrentPrice)
	}
	candidates = append(candidates, thesis.TargetPrice, thesis.StopLoss)
	for _, price := range candidates {
		if price > 0 {
			return price
		}
	}
	return 0
}

func buildExecutionIntent(thesis *model.Thesis, orderType model.OrderType) *model.ExecutionIntent {
	if thesis == nil {
		return nil
	}

	intent := &model.ExecutionIntent{
		DecidedAt:      time.Now().UTC(),
		ReferencePrice: thesisReferencePrice(thesis),
	}
	if thesis.MarketContext != nil {
		intent.BidPrice = thesis.MarketContext.BidPrice
		intent.AskPrice = thesis.MarketContext.AskPrice
		intent.MidPrice = thesis.MarketContext.MidPrice
		intent.SpreadBps = thesis.MarketContext.SpreadBps
		intent.QuoteAgeSeconds = thesis.MarketContext.QuoteAgeSeconds
		if !thesis.MarketContext.SnapshotAt.IsZero() {
			intent.DecidedAt = thesis.MarketContext.SnapshotAt.UTC()
		}
		if intent.ReferencePrice <= 0 {
			intent.ReferencePrice = thesis.MarketContext.CurrentPrice
		}
		if intent.ReferencePrice <= 0 {
			intent.ReferencePrice = thesis.MarketContext.MidPrice
		}
	}

	switch {
	case orderType == model.OrderLimit && thesis.EntryPrice > 0:
		intent.DecisionPrice = thesis.EntryPrice
	case intent.ReferencePrice > 0:
		intent.DecisionPrice = intent.ReferencePrice
	default:
		intent.DecisionPrice = thesis.EntryPrice
	}

	if intent.DecisionPrice <= 0 && intent.MidPrice > 0 {
		intent.DecisionPrice = intent.MidPrice
	}
	if intent.ReferencePrice <= 0 {
		intent.ReferencePrice = intent.DecisionPrice
	}

	if intent.DecisionPrice <= 0 && intent.ReferencePrice <= 0 && intent.BidPrice <= 0 && intent.AskPrice <= 0 {
		return nil
	}
	return intent
}

func chooseEntryOrder(thesis *model.Thesis) (model.OrderType, float64) {
	if thesis == nil {
		return model.OrderMarket, 0
	}
	if thesis.EntryPrice > 0 {
		if shouldUseMidPrice(thesis, thesis.EntryPrice) {
			if capPrice := aggressiveTouch(thesis.Direction, thesis.MarketContext); capPrice > 0 {
				return model.OrderMidPrice, capPrice
			}
		}
		return model.OrderLimit, thesis.EntryPrice
	}
	if thesis.IsMultiLeg() {
		return model.OrderMarket, 0
	}
	if !hasFreshQuote(thesis.MarketContext) {
		return model.OrderMarket, 0
	}

	switch {
	case thesis.MarketContext.SpreadBps > 0 && thesis.MarketContext.SpreadBps <= adaptiveMaxSpreadBps:
		if price := aggressiveTouch(thesis.Direction, thesis.MarketContext); price > 0 {
			return model.OrderAdaptive, price
		}
	case thesis.MarketContext.SpreadBps > 0 && thesis.MarketContext.SpreadBps <= midPriceMaxSpreadBps:
		if price := aggressiveTouch(thesis.Direction, thesis.MarketContext); price > 0 {
			return model.OrderMidPrice, price
		}
	default:
		if price := passiveTouch(thesis.Direction, thesis.MarketContext); price > 0 {
			return model.OrderLimit, price
		}
	}

	if price := thesisReferencePrice(thesis); price > 0 {
		return model.OrderLimit, price
	}
	return model.OrderMarket, 0
}

func shouldUseMidPrice(thesis *model.Thesis, entryPrice float64) bool {
	if thesis == nil || thesis.MarketContext == nil || entryPrice <= 0 {
		return false
	}
	if !hasFreshQuote(thesis.MarketContext) {
		return false
	}
	if thesis.IsMultiLeg() {
		return false
	}
	spread := thesis.MarketContext.SpreadBps
	if spread <= adaptiveMaxSpreadBps || spread > midPriceMaxSpreadBps {
		return false
	}
	reference := thesis.MarketContext.MidPrice
	if reference <= 0 {
		reference = thesis.MarketContext.CurrentPrice
	}
	if reference <= 0 {
		return false
	}
	diffBps := absBps(entryPrice, reference)
	return diffBps <= explicitMidpriceToleranceBps
}

func hasFreshQuote(ctx *model.MarketContext) bool {
	if ctx == nil {
		return false
	}
	if ctx.QuoteAgeSeconds > maxSmartQuoteAgeSeconds {
		return false
	}
	return ctx.BidPrice > 0 || ctx.AskPrice > 0 || ctx.MidPrice > 0
}

func aggressiveTouch(direction model.TradeDirection, ctx *model.MarketContext) float64 {
	if ctx == nil {
		return 0
	}
	if direction == model.Short {
		if ctx.BidPrice > 0 {
			return ctx.BidPrice
		}
	} else {
		if ctx.AskPrice > 0 {
			return ctx.AskPrice
		}
	}
	if ctx.MidPrice > 0 {
		return ctx.MidPrice
	}
	return ctx.CurrentPrice
}

func passiveTouch(direction model.TradeDirection, ctx *model.MarketContext) float64 {
	if ctx == nil {
		return 0
	}
	if direction == model.Short {
		if ctx.AskPrice > 0 {
			return ctx.AskPrice
		}
	} else {
		if ctx.BidPrice > 0 {
			return ctx.BidPrice
		}
	}
	if ctx.MidPrice > 0 {
		return ctx.MidPrice
	}
	return ctx.CurrentPrice
}

func absBps(a, b float64) float64 {
	if a <= 0 || b <= 0 {
		return 0
	}
	diff := a - b
	if diff < 0 {
		diff = -diff
	}
	return (diff / b) * 10000
}
