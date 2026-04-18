package orderflow

import (
	"fmt"
	"strings"
	"time"

	"github.com/hnic/trading-floor/pkg/model"
)

const defaultTimeInForce = "DAY"

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

	orderType := model.OrderMarket
	if thesis.EntryPrice > 0 {
		orderType = model.OrderLimit
	}

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
		LimitPrice:      thesis.EntryPrice,
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
