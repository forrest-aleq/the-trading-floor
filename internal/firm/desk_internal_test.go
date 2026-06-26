package firm

import (
	"testing"

	"github.com/hnic/trading-floor/pkg/model"
)

func TestNormalizePositionSizeUsesWholeShareWhenReferencePriceMissing(t *testing.T) {
	desk := &Desk{Capital: 15000}
	thesis := &model.Thesis{
		Instrument: model.Instrument{
			Symbol:   "MSFT",
			SecType:  "STK",
			Exchange: "SMART",
			Currency: "USD",
		},
		PositionSize: 0.01,
	}

	desk.normalizePositionSize(thesis)

	if thesis.PositionSize != 1 {
		t.Fatalf("expected missing-price stock sizing to use one whole share, got %.4f", thesis.PositionSize)
	}
}
