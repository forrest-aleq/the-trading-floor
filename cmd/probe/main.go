package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/joho/godotenv"

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

const (
	probeRuntimeTimeout = 2 * time.Minute
	probeOpenTimeout    = 90 * time.Second
)

func main() {
	_ = godotenv.Load()
	firm.ReloadRuntimeConfig()
	research.ReloadRuntimeConfig()
	scanner.ReloadRuntimeConfig()

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	if err := runProbe(); err != nil {
		fmt.Fprintf(os.Stderr, "probe: %v\n", err)
		os.Exit(1)
	}
}

func runProbe() error {
	ctx, cancel := context.WithTimeout(context.Background(), probeRuntimeTimeout)
	defer cancel()

	router := llm.DefaultRouter()
	broker := &probeBroker{}
	execMgr := execution.NewManager(broker)
	bk := book.NewBook(broker, 1_000_000)
	riskGate := risk.NewGate(risk.DefaultLimits())
	beliefGraph := belief.NewGraph()
	engramStore := memory.NewEngramStore()
	learnWorker := memory.NewLearnWorker(beliefGraph, engramStore)
	scan := scanner.NewEngine(router, 70)
	researchDesk := research.NewDesk(router, 0.65)
	prosecutor := research.NewProsecutor(router)

	wireMgr := wire.NewManager()
	wireMgr.RegisterFeed(oneShotFeed{signal: syntheticProbeSignal()})

	floor := firm.NewFloor(wireMgr, "probe-session")
	desk := firm.NewDesk(firm.DeskConfig{
		ID:          "probe-corporate-a",
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

	if err := waitFor(probeOpenTimeout, func() bool {
		return len(bk.GetOpenPositions()) == 1
	}); err != nil {
		return fmt.Errorf("probe did not open a position: %w", err)
	}

	positions := bk.GetOpenPositions()
	pos := positions[0]
	if !pos.Shadow {
		return fmt.Errorf("expected restricted autonomy to open a shadow position, got live position %s", pos.ID)
	}

	thesis, ok := desk.GetThesis(pos.ThesisID)
	if !ok {
		return fmt.Errorf("probe thesis %s not found in active desk state", pos.ThesisID)
	}

	exitPrice := thesis.TargetPrice
	if exitPrice <= 0 {
		exitPrice = thesis.EntryPrice * 1.05
	}

	outcome, err := bk.ClosePosition(pos.ID, exitPrice, "synthetic_probe_target")
	if err != nil {
		return fmt.Errorf("closing probe position: %w", err)
	}
	desk.ProcessOutcome(ctx, thesis, outcome)

	cancel()
	if err := <-runDone; err != nil && err != context.Canceled {
		return fmt.Errorf("probe floor stopped unexpectedly: %w", err)
	}

	stats := floor.Stats()
	fmt.Printf("probe complete\n")
	fmt.Printf("signal=%s\n", syntheticProbeSignal().ID)
	fmt.Printf("thesis_id=%s\n", thesis.ID)
	fmt.Printf("autonomy_mode=%s\n", thesis.AutonomyMode)
	fmt.Printf("shadow=%t\n", pos.Shadow)
	fmt.Printf("status=%s\n", thesis.Status)
	fmt.Printf("strategy=%s\n", thesis.Strategy)
	fmt.Printf("conviction=%.2f\n", thesis.Conviction)
	fmt.Printf("signals_processed=%d\n", stats.SignalsProcessed)
	fmt.Printf("open_positions=%d\n", len(bk.GetOpenPositions()))
	fmt.Printf("beliefs=%d\n", len(beliefGraph.All()))
	return nil
}

func syntheticProbeSignal() signal.Signal {
	return signal.Signal{
		ID:        "synthetic-corporate-beat-nvda",
		Source:    "synthetic-probe",
		Type:      signal.TypeNews,
		Category:  "corporate",
		Timestamp: time.Now(),
		Urgency:   0.98,
		Strength:  0.95,
		Direction: signal.Bullish,
		Entities: []signal.Entity{
			{Name: "NVIDIA", Type: "company", ID: "NVDA"},
			{Name: "NVDA", Type: "instrument", ID: "NVDA"},
		},
		CorroboratingSources: []string{"company-pr", "reuters", "sec-8k"},
		Translated:           "NVIDIA (NASDAQ: NVDA) reported quarterly revenue 18% above consensus, raised next-quarter data center guidance by 15%, expanded gross margin outlook, and said hyperscaler demand accelerated. Shares traded 11% higher premarket and management announced additional AI systems backlog visibility through the next two quarters.",
	}
}

type probeBroker struct{}

func (b *probeBroker) IsConnected() bool { return true }
func (b *probeBroker) IsPaper() bool     { return true }

func (b *probeBroker) PlaceOrder(ctx context.Context, order model.Order) (*model.Fill, error) {
	price := order.LimitPrice
	if price <= 0 {
		price = 100
	}

	instrument := order.Instrument
	instrument.ConID = 1001

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

func (b *probeBroker) CancelOrder(ctx context.Context, orderID int64) error { return nil }

func (b *probeBroker) GetPositions(ctx context.Context) ([]ibkr.IBKRPosition, error) {
	return nil, nil
}

func (b *probeBroker) GetAccountSummary(ctx context.Context) (*ibkr.AccountSummary, error) {
	return &ibkr.AccountSummary{
		NetLiquidation: 1_000_000,
		BuyingPower:    2_000_000,
		Cash:           1_000_000,
	}, nil
}

type oneShotFeed struct {
	signal signal.Signal
}

func (f oneShotFeed) Name() string { return "probe-feed" }

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
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("condition not satisfied before timeout")
}
