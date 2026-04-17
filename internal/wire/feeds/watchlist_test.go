package feeds

import (
	"testing"

	"github.com/hnic/trading-floor/internal/marketrefs"
	"github.com/hnic/trading-floor/pkg/model"
)

func TestDefaultWatchlistIsReferenceOnly(t *testing.T) {
	t.Parallel()

	got := symbols(DefaultWatchlist())
	want := symbols(marketrefs.BootstrapWatchlist())

	if len(got) != len(want) {
		t.Fatalf("default watchlist size = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("default watchlist[%d] = %s, want %s", i, got[i], want[i])
		}
	}
}

func TestDefaultEarningsWatchlistIsCorporateUniverse(t *testing.T) {
	t.Parallel()

	got := symbols(DefaultEarningsWatchlist())
	want := symbols(marketrefs.EarningsWatchlist())

	if len(got) != len(want) {
		t.Fatalf("earnings watchlist size = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("earnings watchlist[%d] = %s, want %s", i, got[i], want[i])
		}
	}
}

func TestMarketSignalStateOnlyEmitsMeaningfulMoves(t *testing.T) {
	t.Parallel()

	var state marketSignalState

	if state.shouldEmit(100) {
		t.Fatal("first observation should prime state, not emit")
	}
	if state.shouldEmit(100.4) {
		t.Fatal("sub-threshold move should not emit")
	}
	if !state.shouldEmit(101.0) {
		t.Fatal("threshold move should emit")
	}
	if state.shouldEmit(101.3) {
		t.Fatal("small move after emission should not emit")
	}
}

func symbols(instruments []model.Instrument) []string {
	out := make([]string, 0, len(instruments))
	for _, inst := range instruments {
		out = append(out, inst.Symbol)
	}
	return out
}
