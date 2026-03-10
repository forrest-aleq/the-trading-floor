package main

import (
	"context"
	"testing"
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
func (f *fakeBroker) ReqMarketData(ctx context.Context, inst model.Instrument) (*ibkr.MarketData, error) {
	return &ibkr.MarketData{
		ConID:     42,
		Symbol:    inst.Symbol,
		Last:      100,
		Bid:       99.5,
		Ask:       100.5,
		Volume:    1000,
		Timestamp: time.Now().UnixMilli(),
	}, nil
}

type oneShotFeed struct {
	signal signal.Signal
}

func (f oneShotFeed) Name() string { return "test-feed" }

func (f oneShotFeed) Start(ctx context.Context, out chan<- signal.Signal) error {
	select {
	case out <- f.signal:
	case <-ctx.Done():
		return ctx.Err()
	}

	<-ctx.Done()
	return ctx.Err()
}

func TestSmokeFullPipeline(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	broker := &fakeBroker{}
	router := llm.NewRouter(
		staticLLMClient{response: `{"tradeable":true,"score":85,"instruments":[{"symbol":"AAPL","sec_type":"STK","currency":"USD"}],"direction":"long","urgency":0.9,"category":"corporate","reasoning":"earnings surprise"}`},
		staticLLMClient{response: `{"instrument":{"symbol":"AAPL","sec_type":"STK","currency":"USD","exchange":"SMART"},"direction":"long","entry_price":100,"target_price":110,"stop_loss":95,"conviction":0.8,"time_horizon_hours":24,"position_size_pct":0.1,"strategy":"event","evidence":["beat and raise","strong guide"],"counter_args":["already priced in"],"kill_rules":[{"condition":"price_below_stop","threshold":95,"action":"close"}],"reasoning":"post-earnings follow-through"}`},
		staticLLMClient{response: `{"verdict":"survived","bear_args":["multiple expansion risk"],"missing_data":["next quarter guide"],"historical_analogues":["mixed reactions after beats"],"crowded_score":0.3,"confidence_adjustment":0.05,"reasoning":"still valid"}`},
	)

	execMgr := execution.NewManager(broker)
	bk := book.NewBook(broker, 1_000_000)
	riskGate := risk.NewGate(risk.DefaultLimits())
	beliefGraph := belief.NewGraph()
	learnWorker := memory.NewLearnWorker(beliefGraph)
	scan := scanner.NewEngine(router, 40)
	researchDesk := research.NewDesk(router, 0.65)
	prosecutor := research.NewProsecutor(router)

	wireMgr := wire.NewManager()
	wireMgr.RegisterFeed(oneShotFeed{signal: signal.Signal{
		ID:        "test-signal-1",
		Source:    "test",
		Type:      signal.TypeNews,
		Category:  "corporate",
		Timestamp: time.Now(),
		Urgency:   0.9,
		Raw:       []byte(`"Apple beats earnings expectations and raises guidance"`),
	}})

	floor := firm.NewFloor(wireMgr)
	desk := firm.NewDesk(firm.DeskConfig{
		ID:          "test-desk",
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
		OnTrade:     floor.RecordTrade,
	})
	floor.AddDesk(desk)

	closed := make(chan struct{}, 1)
	monitor := book.NewMonitor(bk, desk.GetThesis, func(pos *model.Position, exitPrice float64, reason string) {
		outcome, err := bk.ClosePosition(pos.ID, exitPrice, reason)
		if err != nil {
			t.Errorf("ClosePosition failed: %v", err)
			return
		}
		thesis, ok := desk.GetThesis(pos.ThesisID)
		if !ok {
			t.Errorf("missing thesis %s", pos.ThesisID)
			return
		}
		desk.ProcessOutcome(ctx, thesis, outcome)
		select {
		case closed <- struct{}{}:
		default:
		}
	})

	runDone := make(chan error, 1)
	go func() {
		runDone <- floor.Run(ctx)
	}()

	waitFor(t, 5*time.Second, func() bool {
		return len(bk.GetOpenPositions()) == 1
	})

	if got := floor.Stats().TradesExecuted; got != 1 {
		t.Fatalf("expected 1 trade executed, got %d", got)
	}

	bk.Mark(map[string]float64{"AAPL": 94})
	monitor.RunOnce()

	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for monitor-driven close")
	}

	if got := len(bk.GetOpenPositions()); got != 0 {
		t.Fatalf("expected no open positions, got %d", got)
	}

	states := beliefGraph.All()
	if len(states) != 1 {
		t.Fatalf("expected 1 belief state, got %d", len(states))
	}
	if states[0].FailureCount != 1 {
		t.Fatalf("expected 1 recorded failure, got %+v", states[0])
	}

	cancel()
	select {
	case err := <-runDone:
		if err != context.Canceled && err != nil {
			t.Fatalf("floor.Run returned unexpected error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for floor shutdown")
	}
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}

	t.Fatal("condition not met before timeout")
}
