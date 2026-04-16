package orderflow

import (
	"fmt"
	"strings"

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

	order := &model.Order{
		ID:          thesis.ID,
		ThesisID:    thesis.ID,
		DeskID:      input.DeskID,
		Structure:   firstNonEmpty(thesis.Structure, "single"),
		Instrument:  instrument,
		Legs:        cloneLegs(thesis.Legs),
		Direction:   thesis.Direction,
		Quantity:    quantity,
		OrderType:   orderType,
		LimitPrice:  thesis.EntryPrice,
		StopPrice:   thesis.StopLoss,
		TimeInForce: firstNonEmpty(input.ExitTIF, defaultTimeInForce),
		Notional:    thesis.GrossEntryNotional(quantity),
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
