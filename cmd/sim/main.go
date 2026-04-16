package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/hnic/trading-floor/internal/book"
	"github.com/hnic/trading-floor/internal/execution"
	"github.com/hnic/trading-floor/internal/execution/ibkr"
	"github.com/hnic/trading-floor/internal/firm"
	"github.com/hnic/trading-floor/internal/llm"
	"github.com/hnic/trading-floor/internal/memory"
	"github.com/hnic/trading-floor/internal/memory/belief"
	"github.com/hnic/trading-floor/internal/research"
	"github.com/hnic/trading-floor/internal/risk"
	"github.com/hnic/trading-floor/internal/scanner"
	"github.com/hnic/trading-floor/internal/wire"
	"github.com/hnic/trading-floor/pkg/model"
	"github.com/hnic/trading-floor/pkg/signal"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	if err := runSimulation(); err != nil {
		fmt.Fprintf(os.Stderr, "sim: %v\n", err)
		os.Exit(1)
	}
}

func runSimulation() error {
	firm.ReloadRuntimeConfig()
	research.ReloadRuntimeConfig()
	scanner.ReloadRuntimeConfig()

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	broker := &fakeBroker{}
	router := llm.NewRouter(
		staticLLMClient{response: `{"tradeable":true,"score":88,"instruments":[{"symbol":"AAPL","sec_type":"STK","currency":"USD"}],"direction":"long","urgency":0.9,"category":"corporate","reasoning":"earnings surprise with actionable follow-through"}`},
		staticLLMClient{response: `{"instrument":{"symbol":"AAPL","sec_type":"STK","currency":"USD","exchange":"SMART"},"direction":"long","entry_price":100,"target_price":110,"stop_loss":95,"conviction":0.82,"time_horizon_hours":24,"position_size_pct":0.01,"strategy":"event","evidence":["beat and raise","strong guide"],"counter_args":["already priced in"],"kill_rules":[{"condition":"price_below_stop","threshold":95,"action":"close"}],"reasoning":"post-earnings follow-through"}`},
		staticLLMClient{response: `{"verdict":"survived","bear_args":["multiple expansion risk"],"missing_data":["next quarter guide"],"historical_analogues":["mixed reactions after beats"],"crowded_score":0.3,"confidence_adjustment":0.03,"reasoning":"still valid"}`},
	)

	execMgr := execution.NewManager(broker)
	bk := book.NewBook(broker, 1_000_000)
	riskGate := risk.NewGate(risk.DefaultLimits())
	beliefGraph := belief.NewGraph()
	engramStore := memory.NewEngramStore()
	learnWorker := memory.NewLearnWorker(beliefGraph, engramStore)
	scan := scanner.NewEngine(router, 40)
	researchDesk := research.NewDesk(router, 0.65)
	prosecutor := research.NewProsecutor(router)

	wireMgr := wire.NewManager()
	wireMgr.RegisterFeed(oneShotFeed{signal: signal.Signal{
		ID:        "sim-signal-1",
		Source:    "demo-wire",
		Type:      signal.TypeNews,
		Category:  "corporate",
		Timestamp: time.Now(),
		Urgency:   0.9,
		Raw:       []byte(`"Apple beats earnings expectations and raises guidance"`),
	}})

	floor := firm.NewFloor(wireMgr, "sim-session")
	desk := firm.NewDesk(firm.DeskConfig{
		ID:          "sim-corporate-a",
		Domain:      "corporate",
		ABGroup:     "A",
		Capital:     100_000,
		LLM:         router,
		Scanner:     scan,
		Research:    researchDesk,
		Prosecutor:  prosecutor,
		RiskGate:    riskGate,
		Execution:   execMgr,
		Book:        bk,
		Beliefs:     beliefGraph,
		LearnWorker: learnWorker,
		Engrams:     engramStore,
		OnTrade:     floor.RecordTrade,
	})
	floor.AddDesk(desk)

	runDone := make(chan error, 1)
	go func() {
		runDone <- floor.Run(ctx)
	}()

	if err := waitFor(3*time.Second, func() bool {
		return len(bk.GetOpenPositions()) == 1
	}); err != nil {
		return fmt.Errorf("trade did not open: %w", err)
	}

	monitor := book.NewMonitor(bk, desk.GetThesis, func(pos *model.Position, exitPrice float64, reason string) {
		outcome, err := bk.ClosePosition(pos.ID, exitPrice, reason)
		if err != nil {
			slog.Error("close position failed", "position_id", pos.ID, "error", err)
			return
		}
		if thesis, ok := desk.GetThesis(pos.ThesisID); ok {
			desk.ProcessOutcome(ctx, thesis, outcome)
		}
	})

	bk.Mark(map[string]float64{"AAPL": 94})
	monitor.RunOnce()

	if err := waitFor(3*time.Second, func() bool {
		return len(bk.GetOpenPositions()) == 0
	}); err != nil {
		return fmt.Errorf("position did not close: %w", err)
	}

	cancel()
	if err := <-runDone; err != nil && err != context.Canceled {
		return fmt.Errorf("floor stopped unexpectedly: %w", err)
	}

	stats := floor.Stats()
	beliefs := beliefGraph.All()
	fmt.Printf("simulation complete\n")
	fmt.Printf("trades_executed=%d signals_processed=%d open_positions=%d beliefs=%d\n",
		stats.TradesExecuted,
		stats.SignalsProcessed,
		len(bk.GetOpenPositions()),
		len(beliefs),
	)
	return nil
}

type staticLLMClient struct {
	response string
}

func (c staticLLMClient) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	return &llm.Response{Content: c.response}, nil
}

type fakeBroker struct{}

func (f *fakeBroker) IsConnected() bool { return true }
func (f *fakeBroker) IsPaper() bool     { return true }

func (f *fakeBroker) PlaceOrder(ctx context.Context, order model.Order) (*model.Fill, error) {
	price := order.LimitPrice
	if price <= 0 {
		price = 100
	}

	instrument := order.Instrument
	instrument.ConID = 42

	return &model.Fill{
		OrderID:     order.ID,
		IBKROrderID: 1001,
		Instrument:  instrument,
		Direction:   order.Direction,
		Quantity:    order.Quantity,
		AvgPrice:    price,
		FilledAt:    time.Now(),
	}, nil
}

func (f *fakeBroker) CancelOrder(ctx context.Context, orderID int64) error { return nil }

func (f *fakeBroker) GetPositions(ctx context.Context) ([]ibkr.IBKRPosition, error) {
	return nil, nil
}

func (f *fakeBroker) GetAccountSummary(ctx context.Context) (*ibkr.AccountSummary, error) {
	return &ibkr.AccountSummary{
		NetLiquidation: 1_000_000,
		BuyingPower:    2_000_000,
		Cash:           1_000_000,
	}, nil
}

type oneShotFeed struct {
	signal signal.Signal
}

func (f oneShotFeed) Name() string { return "sim-feed" }

func (f oneShotFeed) Start(ctx context.Context, out chan<- signal.Signal) error {
	select {
	case out <- f.signal:
	case <-ctx.Done():
		return ctx.Err()
	}

	<-ctx.Done()
	return ctx.Err()
}

func waitFor(timeout time.Duration, condition func() bool) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return nil
		}
		time.Sleep(25 * time.Millisecond)
	}
	return fmt.Errorf("condition not satisfied before timeout")
}
