package firm_test

import (
	"context"
	"encoding/json"
	"strings"
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
			if req.JSONMode || strings.Contains(strings.ToLower(req.Messages[0].Content), "trading research desk") {
				return string(researchResp)
			}
			return "sub-team analysis with concrete supporting detail"
		}},
		scriptedLLM{response: string(prosecuteResp)},
	)

	desk, bk, _ := newRuntimeDesk(t, "A", router, nil, nil)
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

	deskA, bookA, _ := newRuntimeDesk(t, "A", router, engrams, nil)
	deskB, bookB, _ := newRuntimeDesk(t, "B", router, engrams, nil)

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

func TestTreatmentDeskUsesShadowModeUntilCompetenceIsEarned(t *testing.T) {
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
		"evidence":           []string{"catalyst", "follow-through"},
		"counter_args":       []string{"already priced"},
		"kill_rules":         []map[string]any{{"condition": "price_below_stop", "threshold": 95.0, "action": "close"}},
	})
	prosecuteResp, _ := json.Marshal(map[string]any{
		"verdict":               "survived",
		"bear_args":             []string{"crowded"},
		"missing_data":          []string{"flow"},
		"historical_analogues":  []string{"prior event"},
		"crowded_score":         0.2,
		"confidence_adjustment": 0.0,
	})

	router := llm.NewRouter(
		scriptedLLM{response: `{"tradeable":true,"score":85,"instruments":[{"symbol":"AAPL","sec_type":"STK","currency":"USD"}],"direction":"long","urgency":0.8,"category":"corporate","reasoning":"event"}`},
		scriptedLLM{response: string(researchResp)},
		scriptedLLM{response: string(prosecuteResp)},
	)

	deskA, bookA, _ := newRuntimeDesk(t, "A", router, nil, nil)
	deskB, bookB, _ := newRuntimeDesk(t, "B", router, nil, nil)

	sig := signal.Signal{
		ID:        "sig-shadow",
		Source:    "test",
		Type:      signal.TypeNews,
		Category:  "corporate",
		Timestamp: time.Now(),
		Urgency:   0.8,
		Raw:       []byte(`AAPL event catalyst forms`),
	}

	deskA.Process(context.Background(), sig)
	deskB.Process(context.Background(), sig)

	posA := fetchOpenPosition(t, bookA)
	if !posA.Shadow {
		t.Fatal("expected treatment desk to open a shadow position on cold start")
	}
	thesisA, _ := deskA.GetThesis(posA.ThesisID)
	if thesisA == nil || thesisA.AutonomyMode != model.Restricted {
		t.Fatalf("expected restricted autonomy for treatment desk, got %+v", thesisA)
	}

	posB := fetchOpenPosition(t, bookB)
	if posB.Shadow {
		t.Fatal("expected control desk to execute live position")
	}
	thesisB, _ := deskB.GetThesis(posB.ThesisID)
	if thesisB == nil || thesisB.AutonomyMode != model.Autonomous {
		t.Fatalf("expected control desk to remain autonomous, got %+v", thesisB)
	}
}

func TestTreatmentDeskExecutesLiveWhenCompetenceIsKnown(t *testing.T) {
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
		"evidence":           []string{"catalyst", "follow-through"},
		"counter_args":       []string{"already priced"},
		"kill_rules":         []map[string]any{{"condition": "price_below_stop", "threshold": 95.0, "action": "close"}},
	})
	prosecuteResp, _ := json.Marshal(map[string]any{
		"verdict":               "survived",
		"bear_args":             []string{"crowded"},
		"missing_data":          []string{"flow"},
		"historical_analogues":  []string{"prior event"},
		"crowded_score":         0.2,
		"confidence_adjustment": 0.0,
	})

	router := llm.NewRouter(
		scriptedLLM{response: `{"tradeable":true,"score":85,"instruments":[{"symbol":"AAPL","sec_type":"STK","currency":"USD"}],"direction":"long","urgency":0.8,"category":"corporate","reasoning":"event"}`},
		scriptedLLM{response: string(researchResp)},
		scriptedLLM{response: string(prosecuteResp)},
	)

	graph := belief.NewGraph()
	regimeKey := model.Regime{
		Volatility: "medium",
		Trend:      "neutral",
		Risk:       "neutral",
		Liquidity:  "normal",
	}.Key()
	graph.Load([]*model.CompetenceState{
		{
			Key:          belief.CompetenceKey("desk-A", "scan", "corporate", regimeKey),
			DeskID:       "desk-A",
			Capability:   "scan",
			Context:      "corporate",
			Regime:       regimeKey,
			Trust:        0.86,
			Confidence:   0.74,
			SuccessCount: 120,
			FailureCount: 20,
			Autonomy:     model.Autonomous,
		},
		{
			Key:          belief.CompetenceKey("desk-A", "event", "STK", regimeKey),
			DeskID:       "desk-A",
			Capability:   "event",
			Context:      "STK",
			Regime:       regimeKey,
			Trust:        0.86,
			Confidence:   0.74,
			SuccessCount: 120,
			FailureCount: 20,
			Autonomy:     model.Autonomous,
		},
	})

	desk, bk, _ := newRuntimeDesk(t, "A", router, nil, graph)
	desk.Process(context.Background(), signal.Signal{
		ID:        "sig-live",
		Source:    "test",
		Type:      signal.TypeNews,
		Category:  "corporate",
		Timestamp: time.Now(),
		Urgency:   0.8,
		Raw:       []byte(`AAPL event catalyst forms`),
	})

	pos := fetchOpenPosition(t, bk)
	if pos.Shadow {
		t.Fatal("expected treatment desk to execute live once competence is known")
	}
}

func TestDeskPublishesInternalSignalForStrongApprovedThesis(t *testing.T) {
	researchResp, _ := json.Marshal(map[string]any{
		"instrument":         map[string]any{"symbol": "XLE", "sec_type": "STK", "currency": "USD", "exchange": "SMART"},
		"direction":          "long",
		"entry_price":        100.0,
		"target_price":       108.0,
		"stop_loss":          96.0,
		"conviction":         0.84,
		"time_horizon_hours": 24,
		"position_size_pct":  0.01,
		"strategy":           "event",
		"evidence":           []string{"shipping disruption", "energy beta"},
		"counter_args":       []string{"false alarm"},
		"kill_rules":         []map[string]any{{"condition": "price_below_stop", "threshold": 96.0, "action": "close"}},
	})
	prosecuteResp, _ := json.Marshal(map[string]any{
		"verdict":               "survived",
		"bear_args":             []string{"timing"},
		"missing_data":          []string{"flow"},
		"historical_analogues":  []string{"prior shipping shock"},
		"crowded_score":         0.2,
		"confidence_adjustment": 0.0,
	})

	router := llm.NewRouter(
		scriptedLLM{response: `{"tradeable":true,"score":85,"instruments":[{"symbol":"XLE","sec_type":"STK","currency":"USD"}],"direction":"long","urgency":0.9,"category":"geopolitical","reasoning":"shipping shock"}`},
		scriptedLLM{fn: func(req llm.Request) string {
			if req.JSONMode || strings.Contains(strings.ToLower(req.Messages[0].Content), "trading research desk") {
				return string(researchResp)
			}
			return "sub-team analysis with concrete supporting detail"
		}},
		scriptedLLM{response: string(prosecuteResp)},
	)

	var published []signal.Signal
	desk, _, _ := newRuntimeDeskWithOptions(t, "A", "geopolitical", router, nil, nil, func(_ context.Context, sig signal.Signal) error {
		published = append(published, sig)
		return nil
	})
	desk.Process(context.Background(), signal.Signal{
		ID:        "sig-internal",
		Source:    "telegram/mena",
		Type:      signal.TypeNews,
		Category:  "geopolitical",
		Timestamp: time.Now(),
		Urgency:   0.9,
		Raw:       []byte(`{"title":"Shipping disruption reported near Strait of Hormuz"}`),
	})

	if len(published) != 1 {
		t.Fatalf("expected one internal signal to be published, got %d", len(published))
	}
	if published[0].Source != "internal/desk-A" {
		t.Fatalf("unexpected internal signal source: %s", published[0].Source)
	}
	var payload struct {
		TargetDomains []string `json:"target_domains"`
	}
	if err := json.Unmarshal(published[0].Raw, &payload); err != nil {
		t.Fatalf("decode internal signal payload: %v", err)
	}
	if len(payload.TargetDomains) == 0 {
		t.Fatalf("expected internal signal to carry target domains, got %+v", published[0])
	}
}

func newRuntimeDesk(t *testing.T, group string, router *llm.Router, engrams *memory.EngramStore, graph *belief.Graph) (*firm.Desk, *book.Book, *belief.Graph) {
	return newRuntimeDeskWithOptions(t, group, "corporate", router, engrams, graph, nil)
}

func newRuntimeDeskWithOptions(t *testing.T, group, domain string, router *llm.Router, engrams *memory.EngramStore, graph *belief.Graph, publish func(context.Context, signal.Signal) error) (*firm.Desk, *book.Book, *belief.Graph) {
	t.Helper()

	broker := &runtimeStubBroker{}
	broker.connected.Store(true)

	execMgr := execution.NewManager(broker)
	bk := book.NewBook(broker, 1_000_000)
	riskGate := risk.NewGate(risk.DefaultLimits())
	beliefGraph := graph
	if beliefGraph == nil {
		beliefGraph = belief.NewGraph()
	}
	scan := scanner.NewEngine(router, 40)
	researchDesk := research.NewDesk(router, 0.65)
	prosecutor := research.NewProsecutor(router)
	council := research.NewCouncil(router)

	desk := firm.NewDesk(firm.DeskConfig{
		ID:            "desk-" + group,
		Domain:        domain,
		ABGroup:       group,
		Capital:       25_000,
		LLM:           router,
		Scanner:       scan,
		Research:      researchDesk,
		Prosecutor:    prosecutor,
		Council:       council,
		RiskGate:      riskGate,
		Execution:     execMgr,
		Book:          bk,
		Beliefs:       beliefGraph,
		Engrams:       engrams,
		PublishSignal: publish,
	})

	return desk, bk, beliefGraph
}

func fetchActiveThesis(t *testing.T, desk *firm.Desk, bk *book.Book) *model.Thesis {
	t.Helper()

	pos := fetchOpenPosition(t, bk)
	thesis, ok := desk.GetThesis(pos.ThesisID)
	if !ok || thesis == nil {
		t.Fatalf("expected active thesis for position %s", pos.ThesisID)
	}
	return thesis
}

func fetchOpenPosition(t *testing.T, bk *book.Book) *model.Position {
	t.Helper()

	positions := bk.GetOpenPositions()
	if len(positions) != 1 {
		t.Fatalf("expected exactly one open position, got %d", len(positions))
	}
	return positions[0]
}
