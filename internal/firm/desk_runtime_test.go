package firm_test

import (
	"context"
	"encoding/json"
	"sync/atomic"
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
	"github.com/hnic/trading-floor/pkg/model"
	"github.com/hnic/trading-floor/pkg/signal"
)

type scriptedLLM struct {
	response string
	fn       func(req llm.Request) string
}

func (s scriptedLLM) Complete(_ context.Context, req llm.Request) (*llm.Response, error) {
	if s.fn != nil {
		return &llm.Response{Content: s.fn(req), Model: "scripted"}, nil
	}
	return &llm.Response{Content: s.response, Model: "scripted"}, nil
}

type runtimeStubBroker struct {
	connected atomic.Bool
}

func (b *runtimeStubBroker) IsConnected() bool { return b.connected.Load() }
func (b *runtimeStubBroker) IsPaper() bool     { return true }
func (b *runtimeStubBroker) PlaceOrder(_ context.Context, o model.Order) (*model.Fill, error) {
	return &model.Fill{
		OrderID:    o.ID,
		Instrument: o.Instrument,
		Direction:  o.Direction,
		Quantity:   o.Quantity,
		AvgPrice:   o.LimitPrice,
		FilledAt:   time.Now(),
	}, nil
}
func (b *runtimeStubBroker) CancelOrder(_ context.Context, _ int64) error { return nil }
func (b *runtimeStubBroker) GetPositions(_ context.Context) ([]ibkr.IBKRPosition, error) {
	return nil, nil
}
func (b *runtimeStubBroker) GetAccountSummary(_ context.Context) (*ibkr.AccountSummary, error) {
	return &ibkr.AccountSummary{NetLiquidation: 1_000_000, Cash: 1_000_000}, nil
}

func TestDeskSkipsCouncilForSmallPctAndSpawnsSubTeam(t *testing.T) {
	researchResp, _ := json.Marshal(map[string]any{
		"instrument":         map[string]any{"symbol": "AAPL", "sec_type": "STK", "currency": "USD", "exchange": "SMART"},
		"direction":          "long",
		"entry_price":        100.0,
		"target_price":       110.0,
		"stop_loss":          95.0,
		"conviction":         0.8,
		"time_horizon_hours": 24,
		"position_size_pct":  0.01,
		"strategy":           "event",
		"evidence":           []string{"earnings beat", "guide raised"},
		"counter_args":       []string{"already priced"},
		"kill_rules":         []map[string]any{{"condition": "price_below_stop", "threshold": 95.0, "action": "close"}},
	})
	prosecuteResp, _ := json.Marshal(map[string]any{
		"verdict":               "survived",
		"bear_args":             []string{"crowded trade"},
		"missing_data":          []string{"flow"},
		"historical_analogues":  []string{"prior beats"},
		"crowded_score":         0.2,
		"confidence_adjustment": 0.0,
	})

	router := llm.NewRouter(
		scriptedLLM{response: `{"tradeable":true,"score":85,"instruments":[{"symbol":"AAPL","sec_type":"STK","currency":"USD"}],"direction":"long","urgency":0.9,"category":"corporate","reasoning":"earnings surprise"}`},
		scriptedLLM{fn: func(req llm.Request) string {
			if req.JSONMode {
				return string(researchResp)
			}
			return "sub-team analysis with concrete supporting detail"
		}},
		scriptedLLM{response: string(prosecuteResp)},
	)

	desk, bk := newRuntimeDesk(t, "A", router, nil)
	ctx := context.Background()
	desk.Process(ctx, signal.Signal{
		ID:        "sig-1",
		Source:    "test",
		Type:      signal.TypeNews,
		Category:  "corporate",
		Timestamp: time.Now(),
		Urgency:   0.9,
		Raw:       []byte(`AAPL beats earnings estimates and raises guidance`),
	})

	thesis := fetchActiveThesis(t, desk, bk)
	if thesis.CouncilVerdict != nil {
		t.Fatalf("expected no council verdict for 1%% position, got %+v", thesis.CouncilVerdict)
	}
	if len(thesis.Evidence) <= 2 {
		t.Fatalf("expected sub-team evidence to be merged, got %d evidence items", len(thesis.Evidence))
	}
}

func TestControlDeskSkipsEngramBoost(t *testing.T) {
	researchResp, _ := json.Marshal(map[string]any{
		"instrument":         map[string]any{"symbol": "AAPL", "sec_type": "STK", "currency": "USD", "exchange": "SMART"},
		"direction":          "long",
		"entry_price":        100.0,
		"target_price":       110.0,
		"stop_loss":          95.0,
		"conviction":         0.7,
		"time_horizon_hours": 24,
		"position_size_pct":  0.01,
		"strategy":           "macro",
		"evidence":           []string{"macro signal", "risk improving"},
		"counter_args":       []string{"unexpected hawkishness"},
		"kill_rules":         []map[string]any{{"condition": "price_below_stop", "threshold": 95.0, "action": "close"}},
	})
	prosecuteResp, _ := json.Marshal(map[string]any{
		"verdict":               "survived",
		"bear_args":             []string{"timing risk"},
		"missing_data":          []string{"flow"},
		"historical_analogues":  []string{"macro analog"},
		"crowded_score":         0.2,
		"confidence_adjustment": 0.0,
	})

	router := llm.NewRouter(
		scriptedLLM{response: `{"tradeable":true,"score":85,"instruments":[{"symbol":"AAPL","sec_type":"STK","currency":"USD"}],"direction":"long","urgency":0.7,"category":"macro","reasoning":"macro setup"}`},
		scriptedLLM{response: string(researchResp)},
		scriptedLLM{response: string(prosecuteResp)},
	)

	engrams := memory.NewEngramStore()
	for i := 0; i < 8; i++ {
		engrams.Record("macro_STK", "macro_medium_neutral_risk_on_normal", "macro", "", []string{"medium", "neutral", "risk_on"}, true, 2)
	}
	for i := 0; i < 2; i++ {
		engrams.Record("macro_STK", "macro_medium_neutral_risk_on_normal", "macro", "", []string{"medium", "neutral", "risk_on"}, false, -1)
	}

	deskA, bookA := newRuntimeDesk(t, "A", router, engrams)
	deskB, bookB := newRuntimeDesk(t, "B", router, engrams)

	sig := signal.Signal{
		ID:        "sig-2",
		Source:    "test",
		Type:      signal.TypeNews,
		Category:  "macro",
		Timestamp: time.Now(),
		Urgency:   0.7,
		Raw:       []byte(`Macro conditions improve for large cap equities`),
	}

	deskA.Process(context.Background(), sig)
	deskB.Process(context.Background(), sig)

	thesisA := fetchActiveThesis(t, deskA, bookA)
	thesisB := fetchActiveThesis(t, deskB, bookB)

	if thesisA.Conviction <= thesisB.Conviction {
		t.Fatalf("expected Group A conviction boost from engrams, got A=%.2f B=%.2f", thesisA.Conviction, thesisB.Conviction)
	}
}

func newRuntimeDesk(t *testing.T, group string, router *llm.Router, engrams *memory.EngramStore) (*firm.Desk, *book.Book) {
	t.Helper()

	broker := &runtimeStubBroker{}
	broker.connected.Store(true)

	execMgr := execution.NewManager(broker)
	bk := book.NewBook(broker, 1_000_000)
	riskGate := risk.NewGate(risk.DefaultLimits())
	beliefGraph := belief.NewGraph()
	scan := scanner.NewEngine(router, 40)
	researchDesk := research.NewDesk(router, 0.65)
	prosecutor := research.NewProsecutor(router)
	council := research.NewCouncil(router)

	desk := firm.NewDesk(firm.DeskConfig{
		ID:         "desk-" + group,
		Domain:     "corporate",
		ABGroup:    group,
		Capital:    25_000,
		LLM:        router,
		Scanner:    scan,
		Research:   researchDesk,
		Prosecutor: prosecutor,
		Council:    council,
		RiskGate:   riskGate,
		Execution:  execMgr,
		Book:       bk,
		Beliefs:    beliefGraph,
		Engrams:    engrams,
	})

	return desk, bk
}

func fetchActiveThesis(t *testing.T, desk *firm.Desk, bk *book.Book) *model.Thesis {
	t.Helper()

	positions := bk.GetOpenPositions()
	if len(positions) != 1 {
		t.Fatalf("expected exactly one open position, got %d", len(positions))
	}
	thesis, ok := desk.GetThesis(positions[0].ThesisID)
	if !ok || thesis == nil {
		t.Fatalf("expected active thesis for position %s", positions[0].ThesisID)
	}
	return thesis
}
