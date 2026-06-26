package firm_test

import (
	"context"
	"encoding/json"
	"errors"
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
	orders    atomic.Int32
	lastOrder atomic.Value
	err       error
}

func (b *runtimeStubBroker) IsConnected() bool { return b.connected.Load() }
func (b *runtimeStubBroker) IsPaper() bool     { return true }
func (b *runtimeStubBroker) PlaceOrder(_ context.Context, o model.Order) (*model.Fill, error) {
	b.orders.Add(1)
	b.lastOrder.Store(o)
	if b.err != nil {
		return nil, b.err
	}
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
func (b *runtimeStubBroker) GetOrderStatus(_ context.Context, _ model.Order, _ int64) (*ibkr.OrderLookup, error) {
	return nil, nil
}
func (b *runtimeStubBroker) GetPositions(_ context.Context) ([]ibkr.IBKRPosition, error) {
	return nil, nil
}
func (b *runtimeStubBroker) GetAccountSummary(_ context.Context) (*ibkr.AccountSummary, error) {
	return &ibkr.AccountSummary{NetLiquidation: 1_000_000, Cash: 1_000_000}, nil
}

type runtimeStaticEntryControl struct {
	policy firm.EntryPolicy
}

func (c runtimeStaticEntryControl) CurrentEntryPolicy() firm.EntryPolicy {
	return c.policy
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

func TestDeskSkipsSubTeamWhenRemainingTaskBudgetIsTooLow(t *testing.T) {
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
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	desk.Process(ctx, signal.Signal{
		ID:        "sig-subteam-budget",
		Source:    "test",
		Type:      signal.TypeNews,
		Category:  "corporate",
		Timestamp: time.Now(),
		Urgency:   0.9,
		Raw:       []byte(`AAPL beats earnings estimates and raises guidance`),
	})

	thesis := fetchActiveThesis(t, desk, bk)
	if len(thesis.Evidence) != 2 {
		t.Fatalf("expected sub-team evidence to be skipped when task budget is too low, got %d evidence items", len(thesis.Evidence))
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
	regimeKey := model.Regime{Volatility: "medium", Trend: "neutral", Risk: "neutral", Liquidity: "normal"}.Key()
	for i := 0; i < 8; i++ {
		engrams.Record("macro_STK", "macro_"+regimeKey, "STK", "", []string{"medium", "neutral", "normal"}, true, 2)
	}
	for i := 0; i < 2; i++ {
		engrams.Record("macro_STK", "macro_"+regimeKey, "STK", "", []string{"medium", "neutral", "normal"}, false, -1)
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

func TestDeskBlocksEntryWhenRuntimeHealthDisablesEntries(t *testing.T) {
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
		scriptedLLM{response: string(researchResp)},
		scriptedLLM{response: string(prosecuteResp)},
	)

	broker := &runtimeStubBroker{}
	desk, bk, _ := newRuntimeDeskWithBrokerAndEntryControl(
		t,
		"A",
		"corporate",
		router,
		nil,
		nil,
		nil,
		broker,
		runtimeStaticEntryControl{policy: firm.DisabledEntryPolicy("broker_sync_stale:3m0s", time.Now().UTC())},
	)

	desk.Process(context.Background(), signal.Signal{
		ID:        "sig-runtime-health-block",
		Source:    "test",
		Type:      signal.TypeNews,
		Category:  "corporate",
		Timestamp: time.Now(),
		Urgency:   0.9,
		Raw:       []byte(`AAPL beats earnings estimates and raises guidance`),
	})

	if got := len(bk.GetOpenPositions()); got != 0 {
		t.Fatalf("expected runtime health to block new entries, got %d open positions", got)
	}
	if got := broker.orders.Load(); got != 0 {
		t.Fatalf("expected broker not to receive blocked order, got %d submissions", got)
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

func TestTreatmentDeskExecutesBrokerPaperWhenRestrictedOverrideEnabled(t *testing.T) {
	t.Cleanup(firm.ReloadRuntimeConfig)
	t.Setenv("IBKR_PAPER_ALLOW_RESTRICTED_AUTONOMY", "true")
	firm.ReloadRuntimeConfig()

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

	broker := &runtimeStubBroker{}
	desk, bk, _ := newRuntimeDeskWithBroker(t, "A", "corporate", router, nil, nil, nil, broker)
	desk.Process(context.Background(), signal.Signal{
		ID:        "sig-paper-restricted",
		Source:    "test",
		Type:      signal.TypeNews,
		Category:  "corporate",
		Timestamp: time.Now(),
		Urgency:   0.8,
		Raw:       []byte(`AAPL event catalyst forms`),
	})

	pos := fetchOpenPosition(t, bk)
	if pos.Shadow {
		t.Fatal("expected restricted treatment desk to execute in broker paper mode")
	}
	if got := broker.orders.Load(); got != 1 {
		t.Fatalf("expected one broker paper order, got %d", got)
	}
	thesis, _ := desk.GetThesis(pos.ThesisID)
	if thesis == nil || thesis.AutonomyMode != model.Restricted {
		t.Fatalf("expected autonomy to remain restricted while executing paper, got %+v", thesis)
	}
}

func TestBrokerStockOrderUsesWholeShareMinimum(t *testing.T) {
	t.Cleanup(firm.ReloadRuntimeConfig)
	t.Setenv("IBKR_PAPER_ALLOW_RESTRICTED_AUTONOMY", "true")
	firm.ReloadRuntimeConfig()

	researchResp, _ := json.Marshal(map[string]any{
		"instrument":         map[string]any{"symbol": "MU", "sec_type": "STK", "currency": "USD", "exchange": "SMART"},
		"direction":          "long",
		"entry_price":        100.0,
		"target_price":       120.0,
		"stop_loss":          90.0,
		"conviction":         0.8,
		"time_horizon_hours": 24,
		"position_size_pct":  0.000001,
		"strategy":           "event",
		"evidence":           []string{"catalyst", "follow-through"},
		"counter_args":       []string{"already priced"},
		"kill_rules":         []map[string]any{{"condition": "price_below_stop", "threshold": 90.0, "action": "close"}},
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
		scriptedLLM{response: `{"tradeable":true,"score":85,"instruments":[{"symbol":"MU","sec_type":"STK","currency":"USD"}],"direction":"long","urgency":0.8,"category":"corporate","reasoning":"event"}`},
		scriptedLLM{response: string(researchResp)},
		scriptedLLM{response: string(prosecuteResp)},
	)

	broker := &runtimeStubBroker{}
	desk, _, _ := newRuntimeDeskWithBroker(t, "A", "corporate", router, nil, nil, nil, broker)
	desk.Process(context.Background(), signal.Signal{
		ID:        "sig-whole-share",
		Source:    "test",
		Type:      signal.TypeNews,
		Category:  "corporate",
		Timestamp: time.Now(),
		Urgency:   0.8,
		Raw:       []byte(`MU event catalyst forms`),
	})

	raw := broker.lastOrder.Load()
	if raw == nil {
		t.Fatal("expected broker order")
	}
	order := raw.(model.Order)
	if order.Quantity != 1 {
		t.Fatalf("expected stock order to round up to one whole share, got %.4f", order.Quantity)
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

func TestDeskLeavesPendingPaperOrderOutOfBookUntilFillReconciliation(t *testing.T) {
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

	broker := &runtimeStubBroker{
		err: &execution.PendingFillError{
			OrderID: 99,
			Status:  "Submitted",
			Cause:   errors.New("order accepted but not filled before execution timeout"),
		},
	}
	desk, bk, _ := newRuntimeDeskWithBroker(t, "A", "corporate", router, nil, graph, nil, broker)
	desk.Process(context.Background(), signal.Signal{
		ID:        "sig-pending",
		Source:    "test",
		Type:      signal.TypeNews,
		Category:  "corporate",
		Timestamp: time.Now(),
		Urgency:   0.8,
		Raw:       []byte(`AAPL event catalyst forms`),
	})

	if positions := bk.GetOpenPositions(); len(positions) != 0 {
		t.Fatalf("expected no book position while broker order is still pending, got %d", len(positions))
	}
}

func TestDeskDoesNotInventShadowPositionWhenPaperExecutionTimesOut(t *testing.T) {
	researchResp, _ := json.Marshal(map[string]any{
		"instrument":         map[string]any{"symbol": "TLT", "sec_type": "STK", "currency": "USD", "exchange": "SMART"},
		"direction":          "long",
		"entry_price":        100.0,
		"target_price":       105.0,
		"stop_loss":          97.0,
		"conviction":         0.8,
		"time_horizon_hours": 24,
		"position_size_pct":  0.01,
		"strategy":           "event",
		"evidence":           []string{"policy catalyst"},
		"counter_args":       []string{"timing risk"},
		"kill_rules":         []map[string]any{{"condition": "price_below_stop", "threshold": 97.0, "action": "close"}},
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
		scriptedLLM{response: `{"tradeable":true,"score":82,"instruments":[{"symbol":"TLT","sec_type":"STK","currency":"USD","exchange":"SMART"}],"direction":"long","urgency":0.7,"category":"macro","reasoning":"rates setup"}`},
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
			Key:          belief.CompetenceKey("desk-B", "scan", "macro", regimeKey),
			DeskID:       "desk-B",
			Capability:   "scan",
			Context:      "macro",
			Regime:       regimeKey,
			Trust:        0.86,
			Confidence:   0.74,
			SuccessCount: 120,
			FailureCount: 20,
			Autonomy:     model.Autonomous,
		},
		{
			Key:          belief.CompetenceKey("desk-B", "event", "STK", regimeKey),
			DeskID:       "desk-B",
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

	broker := &runtimeStubBroker{err: context.DeadlineExceeded}
	desk, bk, _ := newRuntimeDeskWithBroker(t, "B", "macro", router, nil, graph, nil, broker)
	desk.Process(context.Background(), signal.Signal{
		ID:        "sig-paper-timeout",
		Source:    "fed-speeches",
		Type:      signal.TypeNews,
		Category:  "macro",
		Timestamp: time.Now(),
		Urgency:   0.7,
		Raw:       []byte(`Fed official comments on rate path`),
	})

	if positions := bk.GetOpenPositions(); len(positions) != 0 {
		t.Fatalf("expected no book position when broker confirmation is missing, got %d", len(positions))
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
	payload, ok := model.DecodeColleagueMessage(published[0].Raw)
	if !ok {
		t.Fatal("expected structured colleague payload")
	}
	if len(payload.TargetDomains) == 0 {
		t.Fatalf("expected internal signal to carry target domains, got %+v", published[0])
	}
	if payload.ThreadID == "" || payload.MessageID == "" {
		t.Fatalf("expected collaboration metadata, got %+v", payload)
	}
	if payload.Kind != model.ColleagueMessageProposal {
		t.Fatalf("expected proposal payload, got %s", payload.Kind)
	}
}

func TestDeskRepliesToInternalColleagueSignal(t *testing.T) {
	researchResp, _ := json.Marshal(map[string]any{
		"instrument":         map[string]any{"symbol": "TLT", "sec_type": "STK", "currency": "USD", "exchange": "SMART"},
		"direction":          "long",
		"entry_price":        100.0,
		"target_price":       106.0,
		"stop_loss":          97.0,
		"conviction":         0.83,
		"time_horizon_hours": 24,
		"position_size_pct":  0.01,
		"strategy":           "event",
		"evidence":           []string{"rates impact", "macro spillover"},
		"counter_args":       []string{"already priced"},
		"kill_rules":         []map[string]any{{"condition": "price_below_stop", "threshold": 97.0, "action": "close"}},
	})
	prosecuteResp, _ := json.Marshal(map[string]any{
		"verdict":               "survived",
		"bear_args":             []string{"timing"},
		"missing_data":          []string{"flow"},
		"historical_analogues":  []string{"prior rate shock"},
		"crowded_score":         0.2,
		"confidence_adjustment": 0.0,
	})

	router := llm.NewRouter(
		scriptedLLM{response: `{"tradeable":true,"score":85,"instruments":[{"symbol":"TLT","sec_type":"STK","currency":"USD"}],"direction":"long","urgency":0.9,"category":"macro","reasoning":"macro spillover"}`},
		scriptedLLM{fn: func(req llm.Request) string {
			if req.JSONMode || strings.Contains(strings.ToLower(req.Messages[0].Content), "trading research desk") {
				return string(researchResp)
			}
			return "sub-team analysis with concrete supporting detail"
		}},
		scriptedLLM{response: string(prosecuteResp)},
	)

	rootMessage := model.ColleagueMessage{
		ThreadID:        model.NewColleagueThreadID("thesis-root"),
		MessageID:       model.NewColleagueMessageID("thesis-root"),
		OriginDesk:      "desk-geo-a",
		OriginDomain:    "geopolitical",
		OriginSignalID:  "sig-root",
		ThesisID:        "thesis-root",
		RootThesisID:    "thesis-root",
		TargetDomains:   []string{"macro", "tail"},
		Strategy:        "event",
		Structure:       "single",
		Conviction:      0.84,
		InternalDepth:   1,
		Kind:            model.ColleagueMessageProposal,
		RequestedAction: "review",
		Summary:         "Internal thesis from geopolitical desk: XLE long structure=single strategy=event conviction=0.84",
		DisplaySymbol:   "XLE",
	}

	var published []signal.Signal
	macroDesk, _, _ := newRuntimeDeskWithOptions(t, "A", "macro", router, nil, nil, func(_ context.Context, sig signal.Signal) error {
		published = append(published, sig)
		return nil
	})
	macroDesk.Process(context.Background(), signal.Signal{
		ID:           "sig-internal-received",
		Source:       "internal/desk-geo-a",
		Type:         signal.TypeAlternative,
		Category:     "geopolitical",
		Timestamp:    time.Now(),
		Urgency:      0.9,
		Raw:          rootMessage.Encode(),
		Translated:   rootMessage.Summary,
		OriginalText: rootMessage.Summary,
	})

	if len(published) != 1 {
		t.Fatalf("expected one reply signal to be published, got %d", len(published))
	}
	payload, ok := model.DecodeColleagueMessage(published[0].Raw)
	if !ok {
		t.Fatal("expected structured colleague reply payload")
	}
	if payload.Kind != model.ColleagueMessageReply {
		t.Fatalf("expected reply payload, got %s", payload.Kind)
	}
	if payload.ThreadID != rootMessage.ThreadID {
		t.Fatalf("expected reply to stay on thread %s, got %s", rootMessage.ThreadID, payload.ThreadID)
	}
	if payload.ReplyToMessageID != rootMessage.MessageID {
		t.Fatalf("expected reply_to_message_id %s, got %s", rootMessage.MessageID, payload.ReplyToMessageID)
	}
	if len(payload.TargetDomains) != 1 || payload.TargetDomains[0] != "geopolitical" {
		t.Fatalf("expected reply to route back to geopolitical, got %#v", payload.TargetDomains)
	}
}

func TestDeskWeightsInternalColleagueSignalByPeerBelief(t *testing.T) {
	researchResp, _ := json.Marshal(map[string]any{
		"instrument":         map[string]any{"symbol": "TLT", "sec_type": "STK", "currency": "USD", "exchange": "SMART"},
		"direction":          "long",
		"entry_price":        100.0,
		"target_price":       106.0,
		"stop_loss":          97.0,
		"conviction":         0.80,
		"time_horizon_hours": 24,
		"position_size_pct":  0.01,
		"strategy":           "event",
		"evidence":           []string{"rates impact", "macro spillover"},
		"counter_args":       []string{"already priced"},
		"kill_rules":         []map[string]any{{"condition": "price_below_stop", "threshold": 97.0, "action": "close"}},
	})
	prosecuteResp, _ := json.Marshal(map[string]any{
		"verdict":               "survived",
		"bear_args":             []string{"timing"},
		"missing_data":          []string{"flow"},
		"historical_analogues":  []string{"prior rate shock"},
		"crowded_score":         0.2,
		"confidence_adjustment": 0.0,
	})

	router := llm.NewRouter(
		scriptedLLM{response: `{"tradeable":true,"score":85,"instruments":[{"symbol":"TLT","sec_type":"STK","currency":"USD"}],"direction":"long","urgency":0.9,"category":"macro","reasoning":"macro spillover"}`},
		scriptedLLM{fn: func(req llm.Request) string {
			if req.JSONMode || strings.Contains(strings.ToLower(req.Messages[0].Content), "trading research desk") {
				return string(researchResp)
			}
			return "sub-team analysis with concrete supporting detail"
		}},
		scriptedLLM{response: string(prosecuteResp)},
	)

	var published []signal.Signal
	macroDesk, bk, beliefGraph := newRuntimeDeskWithOptions(t, "A", "macro", router, nil, nil, func(_ context.Context, sig signal.Signal) error {
		published = append(published, sig)
		return nil
	})
	peerKey := belief.PeerBeliefKey("desk-geo-a", "desk-A", "macro", model.Regime{Volatility: "medium", Trend: "neutral", Risk: "neutral", Liquidity: "normal"}.Key())
	for i := 0; i < 4; i++ {
		beliefGraph.ApplyPeerSuccess(peerKey, 1.5)
	}

	rootMessage := model.ColleagueMessage{
		ThreadID:        model.NewColleagueThreadID("thesis-root"),
		MessageID:       model.NewColleagueMessageID("thesis-root"),
		OriginDesk:      "desk-geo-a",
		OriginDomain:    "geopolitical",
		OriginSignalID:  "sig-root",
		ThesisID:        "thesis-root",
		RootThesisID:    "thesis-root",
		TargetDomains:   []string{"macro", "tail"},
		Strategy:        "event",
		Structure:       "single",
		Conviction:      0.84,
		InternalDepth:   1,
		Kind:            model.ColleagueMessageProposal,
		RequestedAction: "review",
		Summary:         "Internal thesis from geopolitical desk: XLE long structure=single strategy=event conviction=0.84",
		DisplaySymbol:   "XLE",
	}

	macroDesk.Process(context.Background(), signal.Signal{
		ID:           "sig-internal-weighted",
		Source:       "internal/desk-geo-a",
		Type:         signal.TypeAlternative,
		Category:     "geopolitical",
		Timestamp:    time.Now(),
		Urgency:      0.9,
		Raw:          rootMessage.Encode(),
		Translated:   rootMessage.Summary,
		OriginalText: rootMessage.Summary,
	})

	if len(published) != 1 {
		t.Fatalf("expected one reply signal to be published, got %d", len(published))
	}
	payload, ok := model.DecodeColleagueMessage(published[0].Raw)
	if !ok {
		t.Fatal("expected structured colleague reply payload")
	}
	if payload.Conviction <= 0.80 {
		t.Fatalf("expected colleague weighting to lift conviction above base 0.80, got %.4f", payload.Conviction)
	}
	thesis := fetchActiveThesis(t, macroDesk, bk)
	if thesis.CollaborationInput == nil || thesis.CollaborationInput.OriginDesk != "desk-geo-a" {
		t.Fatalf("expected thesis collaboration input to be attached, got %+v", thesis.CollaborationInput)
	}
	foundColleagueEvidence := false
	for _, item := range thesis.Evidence {
		if item.Source == "colleague:desk-geo-a" {
			foundColleagueEvidence = true
			break
		}
	}
	if !foundColleagueEvidence {
		t.Fatalf("expected colleague evidence to be attached, got %+v", thesis.Evidence)
	}
}

func TestDeskInjectsColleagueContextIntoScannerPrompt(t *testing.T) {
	var scannerPrompt string
	router := llm.NewRouter(
		scriptedLLM{fn: func(req llm.Request) string {
			scannerPrompt = req.Messages[len(req.Messages)-1].Content
			return `{"tradeable":false,"score":10,"instruments":[],"direction":"none","urgency":0.1,"category":"macro","reasoning":"not actionable"}`
		}},
		scriptedLLM{response: `{}`},
		scriptedLLM{response: `{}`},
	)

	macroDesk, _, beliefGraph := newRuntimeDeskWithOptions(t, "A", "macro", router, nil, nil, nil)
	peerKey := belief.PeerBeliefKey("desk-geo-a", "desk-A", "macro", model.Regime{Volatility: "medium", Trend: "neutral", Risk: "neutral", Liquidity: "normal"}.Key())
	for i := 0; i < 3; i++ {
		beliefGraph.ApplyPeerSuccess(peerKey, 1.5)
	}

	rootMessage := model.ColleagueMessage{
		ThreadID:        model.NewColleagueThreadID("thesis-root"),
		MessageID:       model.NewColleagueMessageID("thesis-root"),
		OriginDesk:      "desk-geo-a",
		OriginDomain:    "geopolitical",
		OriginSignalID:  "sig-root",
		ThesisID:        "thesis-root",
		RootThesisID:    "thesis-root",
		TargetDomains:   []string{"macro", "tail"},
		Strategy:        "event",
		Structure:       "single",
		Conviction:      0.84,
		InternalDepth:   1,
		Kind:            model.ColleagueMessageProposal,
		RequestedAction: "review",
		Summary:         "Internal thesis from geopolitical desk: XLE long structure=single strategy=event conviction=0.84",
		DisplaySymbol:   "XLE",
	}

	macroDesk.Process(context.Background(), signal.Signal{
		ID:           "sig-internal-scanner-context",
		Source:       "internal/desk-geo-a",
		Type:         signal.TypeAlternative,
		Category:     "geopolitical",
		Timestamp:    time.Now(),
		Urgency:      0.9,
		Raw:          rootMessage.Encode(),
		Translated:   rootMessage.Summary,
		OriginalText: rootMessage.Summary,
	})

	if !strings.Contains(scannerPrompt, "Institutional context:") {
		t.Fatalf("expected scanner prompt to include colleague context, got %q", scannerPrompt)
	}
	if !strings.Contains(scannerPrompt, "colleague.from_desk=desk-geo-a") {
		t.Fatalf("expected scanner prompt to include origin desk, got %q", scannerPrompt)
	}
	if !strings.Contains(scannerPrompt, "colleague.peer_trust=") {
		t.Fatalf("expected scanner prompt to include peer trust, got %q", scannerPrompt)
	}
}

func newRuntimeDesk(t *testing.T, group string, router *llm.Router, engrams *memory.EngramStore, graph *belief.Graph) (*firm.Desk, *book.Book, *belief.Graph) {
	return newRuntimeDeskWithOptions(t, group, "corporate", router, engrams, graph, nil)
}

func newRuntimeDeskWithOptions(t *testing.T, group, domain string, router *llm.Router, engrams *memory.EngramStore, graph *belief.Graph, publish func(context.Context, signal.Signal) error) (*firm.Desk, *book.Book, *belief.Graph) {
	t.Helper()

	return newRuntimeDeskWithBrokerAndEntryControl(t, group, domain, router, engrams, graph, publish, nil, nil)
}

func newRuntimeDeskWithBroker(t *testing.T, group, domain string, router *llm.Router, engrams *memory.EngramStore, graph *belief.Graph, publish func(context.Context, signal.Signal) error, broker *runtimeStubBroker) (*firm.Desk, *book.Book, *belief.Graph) {
	return newRuntimeDeskWithBrokerAndEntryControl(t, group, domain, router, engrams, graph, publish, broker, nil)
}

func newRuntimeDeskWithBrokerAndEntryControl(t *testing.T, group, domain string, router *llm.Router, engrams *memory.EngramStore, graph *belief.Graph, publish func(context.Context, signal.Signal) error, broker *runtimeStubBroker, entryControl firm.EntryControl) (*firm.Desk, *book.Book, *belief.Graph) {
	t.Helper()

	if broker == nil {
		broker = &runtimeStubBroker{}
	}
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
		EntryControl:  entryControl,
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
