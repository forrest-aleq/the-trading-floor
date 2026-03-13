package firm_test

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hnic/trading-floor/internal/execution/ibkr"
	"github.com/hnic/trading-floor/internal/firm"
	"github.com/hnic/trading-floor/internal/llm"
	"github.com/hnic/trading-floor/internal/research"
	"github.com/hnic/trading-floor/internal/risk"
	"github.com/hnic/trading-floor/internal/scanner"
	"github.com/hnic/trading-floor/internal/wire"
	"github.com/hnic/trading-floor/pkg/model"
	"github.com/hnic/trading-floor/pkg/signal"

	"github.com/hnic/trading-floor/internal/book"
	"github.com/hnic/trading-floor/internal/execution"
	"github.com/hnic/trading-floor/internal/memory"
	"github.com/hnic/trading-floor/internal/memory/belief"
)

// stubLLM returns a canned JSON response regardless of prompt.
type stubLLM struct {
	response string
}

func (s *stubLLM) Complete(_ context.Context, req llm.Request) (*llm.Response, error) {
	return &llm.Response{Content: s.response, Model: "stub"}, nil
}

// stubBroker implements execution.Broker with no-ops.
type stubBroker struct {
	connected atomic.Bool
}

func (b *stubBroker) IsConnected() bool                                              { return b.connected.Load() }
func (b *stubBroker) IsPaper() bool                                                  { return true }
func (b *stubBroker) PlaceOrder(_ context.Context, o model.Order) (*model.Fill, error) {
	return &model.Fill{
		OrderID:    o.ID,
		Instrument: o.Instrument,
		Direction:  o.Direction,
		Quantity:   o.Quantity,
		AvgPrice:   o.LimitPrice,
		FilledAt:   time.Now(),
	}, nil
}
func (b *stubBroker) CancelOrder(_ context.Context, _ int64) error                   { return nil }
func (b *stubBroker) GetPositions(_ context.Context) ([]ibkr.IBKRPosition, error)    { return nil, nil }
func (b *stubBroker) GetAccountSummary(_ context.Context) (*ibkr.AccountSummary, error) {
	return &ibkr.AccountSummary{NetLiquidation: 1_000_000, Cash: 1_000_000}, nil
}

// stubFeed emits a single signal and then blocks.
type stubFeed struct {
	sig signal.Signal
}

func (f *stubFeed) Name() string { return "stub" }
func (f *stubFeed) Start(ctx context.Context, out chan<- signal.Signal) error {
	out <- f.sig
	<-ctx.Done()
	return ctx.Err()
}

// TestSmokeEndToEnd verifies the full pipeline from signal to trade.
func TestSmokeEndToEnd(t *testing.T) {
	// Scanner response: tradeable opportunity
	scanResp, _ := json.Marshal(map[string]any{
		"tradeable":   true,
		"score":       85,
		"instruments": []map[string]any{{"symbol": "AAPL", "sec_type": "STK", "currency": "USD"}},
		"direction":   "long",
		"urgency":     0.8,
		"category":    "corporate",
		"reasoning":   "Strong earnings beat",
	})

	// Research response: high-conviction thesis
	researchResp, _ := json.Marshal(map[string]any{
		"instrument":         map[string]any{"symbol": "AAPL", "sec_type": "STK", "currency": "USD", "exchange": "SMART"},
		"direction":          "long",
		"entry_price":        190.0,
		"target_price":       210.0,
		"stop_loss":          180.0,
		"conviction":         0.85,
		"time_horizon_hours": 72,
		"position_size_pct":  0.05,
		"strategy":           "event",
		"evidence":           []string{"Earnings beat by 15%", "Guidance raised"},
		"counter_args":       []string{"Market already expected beat"},
		"kill_rules":         []map[string]any{{"condition": "price below 175", "threshold": 175.0, "action": "close"}},
		"reasoning":          "Post-earnings momentum play",
	})

	// Prosecution response: thesis survives
	prosecuteResp, _ := json.Marshal(map[string]any{
		"verdict":               "survived",
		"bear_args":             []string{"Crowded trade", "Multiple expansion peaked"},
		"missing_data":          []string{"Institutional flow data"},
		"historical_analogues":  []string{"AAPL Q1 2024 post-earnings"},
		"crowded_score":         0.4,
		"confidence_adjustment": 0.02,
		"reasoning":             "Thesis holds despite minor concerns",
	})

	// Wire up stubs: scanner uses speed tier, research uses analysis, prosecutor uses critical
	speedLLM := &stubLLM{response: string(scanResp)}
	analysisLLM := &stubLLM{response: string(researchResp)}
	criticalLLM := &stubLLM{response: string(prosecuteResp)}
	router := llm.NewRouter(speedLLM, analysisLLM, criticalLLM)

	broker := &stubBroker{}
	broker.connected.Store(true)

	execMgr := execution.NewManager(broker)
	bk := book.NewBook(broker, 1_000_000)
	riskGate := risk.NewGate(risk.DefaultLimits())
	beliefGraph := belief.NewGraph()
	learnWorker := memory.NewLearnWorker(beliefGraph)
	scan := scanner.NewEngine(router, 70)
	researchDesk := research.NewDesk(router, 0.65)
	prosecutor := research.NewProsecutor(router)

	tradeCount := int64(0)
	desk := firm.NewDesk(firm.DeskConfig{
		ID:          "test-desk-a1",
		Domain:      "corporate",
		ABGroup:     "A",
		Capital:     25_000,
		Scanner:     scan,
		Research:    researchDesk,
		Prosecutor:  prosecutor,
		RiskGate:    riskGate,
		Execution:   execMgr,
		Book:        bk,
		Beliefs:     beliefGraph,
		LearnWorker: learnWorker,
		OnTrade:     func() { atomic.AddInt64(&tradeCount, 1) },
	})

	testSig := signal.Signal{
		ID:        "test-sig-1",
		Source:    "test",
		Type:      signal.TypeNews,
		Category:  "corporate",
		Timestamp: time.Now(),
		Urgency:   0.8,
		Raw:       []byte(`AAPL beats earnings estimates by 15%, raises guidance`),
	}

	wireMgr := wire.NewManager()
	wireMgr.RegisterFeed(&stubFeed{sig: testSig})

	floor := firm.NewFloor(wireMgr, "test-session")
	floor.AddDesk(desk)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_ = floor.Run(ctx)

	// Verify a trade was executed
	trades := atomic.LoadInt64(&tradeCount)
	if trades == 0 {
		t.Fatal("expected at least 1 trade, got 0 — pipeline did not execute end-to-end")
	}

	// Verify position exists in the book
	positions := bk.GetOpenPositions()
	if len(positions) == 0 {
		t.Fatal("expected open position in book")
	}

	pos := positions[0]
	if pos.Instrument.Symbol != "AAPL" {
		t.Fatalf("expected AAPL position, got %s", pos.Instrument.Symbol)
	}
	if pos.Direction != model.Long {
		t.Fatalf("expected long direction, got %s", pos.Direction)
	}

	t.Logf("smoke test passed: %d trade(s), position %s %s @ %.2f",
		trades, pos.Direction, pos.Instrument.Symbol, pos.EntryPrice)
}
