package kalshi

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/hnic/trading-floor/pkg/model"
)

func TestMapThesisLongBuysYesWithinRiskCap(t *testing.T) {
	mapper := NewMapper(MapperConfig{MaxOrderCents: 200, MinConviction: 0.65})

	mapped, err := mapper.MapThesis(&model.Thesis{
		ID:         "thesis-1",
		DeskID:     "kalshi-rates-a",
		Domain:     "prediction_market",
		Instrument: model.Instrument{Symbol: "KXTEST-26DEC31-YES", SecType: "KALSHI", Currency: "USD"},
		Direction:  model.Long,
		Conviction: 0.82,
		EntryPrice: 0.24,
	})
	if err != nil {
		t.Fatal(err)
	}
	if mapped.Request.Side != "yes" || mapped.Request.Action != "buy" || mapped.ContractIntent != "buy_yes" {
		t.Fatalf("unexpected side/intent: %+v", mapped)
	}
	if mapped.Request.YesPriceDollars != "0.2400" {
		t.Fatalf("yes price = %s, want 0.2400", mapped.Request.YesPriceDollars)
	}
	if mapped.Request.Count != 8 {
		t.Fatalf("count = %d, want 8", mapped.Request.Count)
	}
	if mapped.EstimatedRiskCents > 200 {
		t.Fatalf("risk %d exceeds cap", mapped.EstimatedRiskCents)
	}
}

func TestMapThesisUsesConfiguredTimeInForce(t *testing.T) {
	t.Setenv("KALSHI_ORDER_TIME_IN_FORCE", "good_till_canceled")
	mapper := NewMapper(MapperConfig{MaxOrderCents: 200, MinConviction: 0.65})

	mapped, err := mapper.MapThesis(&model.Thesis{
		ID:         "thesis-gtc",
		DeskID:     "kalshi-rates-a",
		Domain:     "prediction_market",
		Instrument: model.Instrument{Symbol: "KXTEST-26DEC31-YES", SecType: "KALSHI", Currency: "USD"},
		Direction:  model.Long,
		Conviction: 0.82,
		EntryPrice: 0.24,
	})
	if err != nil {
		t.Fatal(err)
	}
	if mapped.Request.TimeInForce != "good_till_canceled" {
		t.Fatalf("time_in_force = %q, want good_till_canceled", mapped.Request.TimeInForce)
	}
}

func TestMapThesisShortBuysNoViaYesAsk(t *testing.T) {
	mapper := NewMapper(MapperConfig{MaxOrderCents: 200, MinConviction: 0.65})

	mapped, err := mapper.MapThesis(&model.Thesis{
		ID:         "thesis-2",
		DeskID:     "kalshi-weather-a",
		Domain:     "prediction_market",
		Instrument: model.Instrument{Symbol: "KXRAIN-26DEC31-YES", SecType: "KALSHI", Currency: "USD"},
		Direction:  model.Short,
		Conviction: 0.8,
		EntryPrice: 0.18,
	})
	if err != nil {
		t.Fatal(err)
	}
	if mapped.Request.Side != "no" || mapped.Request.Action != "buy" || mapped.ContractIntent != "buy_no" {
		t.Fatalf("unexpected side/intent: %+v", mapped)
	}
	if mapped.Request.NoPriceDollars != "0.1800" {
		t.Fatalf("no price = %s, want 0.1800", mapped.Request.NoPriceDollars)
	}
	if mapped.Request.Count != 11 {
		t.Fatalf("count = %d, want 11", mapped.Request.Count)
	}
}

func TestMapThesisUsesDynamicCompoundingCap(t *testing.T) {
	mapper := NewMapper(MapperConfig{MaxOrderCents: 300, MinOrderCents: 100, MinConviction: 0.65})

	mapped, err := mapper.MapThesisWithMaxOrderCents(&model.Thesis{
		ID:         "thesis-compound",
		DeskID:     "kalshi-rates-a",
		Domain:     "prediction_market",
		Instrument: model.Instrument{Symbol: "KXTEST-26DEC31-YES", SecType: "KALSHI", Currency: "USD"},
		Direction:  model.Long,
		Conviction: 0.82,
		EntryPrice: 0.25,
	}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if mapped.Request.Count != 4 {
		t.Fatalf("count = %d, want 4", mapped.Request.Count)
	}
	if mapped.EstimatedRiskCents != 100 {
		t.Fatalf("risk = %d, want 100", mapped.EstimatedRiskCents)
	}
}

func TestExecutorSizesRiskFromAccountEquity(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/trade-api/v2/portfolio/balance" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"balance":5000,"portfolio_value":0,"updated_ts":123}`))
	}))
	defer server.Close()

	client, err := NewClient(Config{
		BaseURL:       server.URL + "/trade-api/v2",
		KeyID:         "key-id",
		PrivateKeyPEM: privateKeyPEM(privateKey),
		MaxOrderCents: 300,
	})
	if err != nil {
		t.Fatal(err)
	}
	executor := NewExecutor(
		client,
		NewMapper(MapperConfig{MaxOrderCents: 300, MinOrderCents: 100, RiskPctEquity: 2, MinConviction: 0.65}),
		ExecutionDryRun,
		t.TempDir()+"/kalshi-dry.jsonl",
	)

	result, err := executor.SubmitThesis(context.Background(), &model.Thesis{
		ID:         "thesis-equity",
		DeskID:     "kalshi-rates-a",
		Domain:     "prediction_market",
		Instrument: model.Instrument{Symbol: "KXTEST-26DEC31-YES", SecType: "KALSHI", Currency: "USD"},
		Direction:  model.Long,
		Conviction: 0.82,
		EntryPrice: 0.25,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.MappedOrder.EstimatedRiskCents != 100 {
		t.Fatalf("risk = %d, want 100", result.MappedOrder.EstimatedRiskCents)
	}
	if result.MappedOrder.MaxOrderCents != 100 {
		t.Fatalf("max order = %d, want dynamic cap 100", result.MappedOrder.MaxOrderCents)
	}
}

func TestExecutorCapsDynamicSizingByAvailableBalance(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/trade-api/v2/portfolio/balance" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"balance":50,"portfolio_value":5000,"updated_ts":123}`))
	}))
	defer server.Close()

	client, err := NewClient(Config{
		BaseURL:       server.URL + "/trade-api/v2",
		KeyID:         "key-id",
		PrivateKeyPEM: privateKeyPEM(privateKey),
		MaxOrderCents: 1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	executor := NewExecutor(
		client,
		NewMapper(MapperConfig{MaxOrderCents: 1000, RiskPctEquity: 10, MinConviction: 0.65}),
		ExecutionDryRun,
		t.TempDir()+"/kalshi-dry.jsonl",
	)

	result, err := executor.SubmitThesis(context.Background(), &model.Thesis{
		ID:         "thesis-available-cash",
		DeskID:     "kalshi-rates-a",
		Domain:     "prediction_market",
		Instrument: model.Instrument{Symbol: "KXTEST-26DEC31-YES", SecType: "KALSHI", Currency: "USD"},
		Direction:  model.Long,
		Conviction: 0.82,
		EntryPrice: 0.25,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.MappedOrder.MaxOrderCents != 50 {
		t.Fatalf("max order = %d, want available cash cap 50", result.MappedOrder.MaxOrderCents)
	}
	if result.MappedOrder.EstimatedRiskCents != 50 {
		t.Fatalf("risk = %d, want 50", result.MappedOrder.EstimatedRiskCents)
	}
}

func TestExecutorRejectsWhenAvailableBalanceTooSmallForContract(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/trade-api/v2/portfolio/balance" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"balance":3,"portfolio_value":5000,"updated_ts":123}`))
	}))
	defer server.Close()

	client, err := NewClient(Config{
		BaseURL:       server.URL + "/trade-api/v2",
		KeyID:         "key-id",
		PrivateKeyPEM: privateKeyPEM(privateKey),
		MaxOrderCents: 1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	executor := NewExecutor(
		client,
		NewMapper(MapperConfig{MaxOrderCents: 1000, RiskPctEquity: 10, MinConviction: 0.65}),
		ExecutionDryRun,
		t.TempDir()+"/kalshi-dry.jsonl",
	)

	_, err = executor.SubmitThesis(context.Background(), &model.Thesis{
		ID:         "thesis-no-cash",
		DeskID:     "kalshi-rates-a",
		Domain:     "prediction_market",
		Instrument: model.Instrument{Symbol: "KXTEST-26DEC31-YES", SecType: "KALSHI", Currency: "USD"},
		Direction:  model.Long,
		Conviction: 0.82,
		EntryPrice: 0.25,
	})
	if err == nil {
		t.Fatal("expected available cash cap to block one-contract order")
	}
	if !strings.Contains(err.Error(), "risk cap $0.03 too small") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExecutorCanOpenOrderUsesCachedZeroCapacity(t *testing.T) {
	t.Setenv("KALSHI_BALANCE_CACHE_TTL", "1h")
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	balanceCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/trade-api/v2/portfolio/balance" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		balanceCalls++
		_, _ = w.Write([]byte(`{"balance":0,"portfolio_value":3304,"updated_ts":123}`))
	}))
	defer server.Close()

	client, err := NewClient(Config{
		BaseURL:       server.URL + "/trade-api/v2",
		KeyID:         "key-id",
		PrivateKeyPEM: privateKeyPEM(privateKey),
		MaxOrderCents: 1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	executor := NewExecutor(
		client,
		NewMapper(MapperConfig{MaxOrderCents: 1000, RiskPctEquity: 10, MinConviction: 0.65}),
		ExecutionLive,
		t.TempDir()+"/kalshi-live.jsonl",
	)

	canOpen, maxOrderCents := executor.CanOpenOrder(context.Background())
	if canOpen || maxOrderCents != 0 {
		t.Fatalf("expected zero capacity, got canOpen=%v max=%d", canOpen, maxOrderCents)
	}
	canOpen, maxOrderCents = executor.CanOpenOrder(context.Background())
	if canOpen || maxOrderCents != 0 {
		t.Fatalf("expected cached zero capacity, got canOpen=%v max=%d", canOpen, maxOrderCents)
	}
	if balanceCalls != 1 {
		t.Fatalf("expected one balance call through cache, got %d", balanceCalls)
	}
}

func TestMapThesisRejectsNonKalshiTicker(t *testing.T) {
	mapper := NewMapper(MapperConfig{MaxOrderCents: 200, MinConviction: 0.65})
	_, err := mapper.MapThesis(&model.Thesis{
		ID:         "thesis-3",
		Instrument: model.Instrument{Symbol: "SPY", SecType: "STK", Currency: "USD"},
		Direction:  model.Long,
		Conviction: 0.8,
		EntryPrice: 0.25,
	})
	if err == nil {
		t.Fatal("expected non-Kalshi ticker to be rejected")
	}
}

func TestMapThesisRejectsPlayerPropWithoutAvailabilityEvidence(t *testing.T) {
	mapper := NewMapper(MapperConfig{MaxOrderCents: 200, MinConviction: 0.65})

	_, err := mapper.MapThesis(kalshiPlayerPropThesis("Norway vs France: Goalscorer | Erling Haaland: 1+"))
	if err == nil {
		t.Fatal("expected unverified player prop to be rejected")
	}
	if !strings.Contains(err.Error(), "kalshi_participant_availability_unverified") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMapThesisRejectsUnavailablePlayerProp(t *testing.T) {
	mapper := NewMapper(MapperConfig{MaxOrderCents: 200, MinConviction: 0.65})

	_, err := mapper.MapThesis(kalshiPlayerPropThesis("Norway vs France: Goalscorer | Erling Haaland: 1+ | participant_availability: blocked source=espn player=Erling Haaland active=false reason=player_inactive"))
	if err == nil {
		t.Fatal("expected unavailable player prop to be rejected")
	}
	if !strings.Contains(err.Error(), "kalshi_participant_availability_blocked") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMapThesisAllowsConfirmedPlayerProp(t *testing.T) {
	mapper := NewMapper(MapperConfig{MaxOrderCents: 200, MinConviction: 0.65})

	mapped, err := mapper.MapThesis(kalshiPlayerPropThesis("Norway vs France: Goalscorer | Erling Haaland: 1+ | participant_availability: confirmed source=espn player=Erling Haaland active=true starter=false reason=espn_roster_match"))
	if err != nil {
		t.Fatal(err)
	}
	if mapped.Request.Ticker != "KXGOAL-26JUN26-HAALAND" {
		t.Fatalf("ticker = %q", mapped.Request.Ticker)
	}
}

func TestMapThesisDoesNotTreatAvailabilityEvidenceAsPlayerDependency(t *testing.T) {
	mapper := NewMapper(MapperConfig{MaxOrderCents: 200, MinConviction: 0.65})

	mapped, err := mapper.MapThesis(&model.Thesis{
		ID:         "thesis-team-market",
		DeskID:     "kalshi-sports-a",
		Domain:     "prediction_market",
		Instrument: model.Instrument{Symbol: "KXWIN-26JUN26-NORFRA", SecType: "KALSHI", Currency: "USD"},
		Direction:  model.Long,
		Conviction: 0.82,
		EntryPrice: 0.24,
		Evidence: []model.Evidence{{
			Source:  "kalshi-market",
			Content: "Norway vs France: Match winner | participant_availability: confirmed source=espn active=true",
			Weight:  1,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if mapped.Request.Ticker != "KXWIN-26JUN26-NORFRA" {
		t.Fatalf("ticker = %q", mapped.Request.Ticker)
	}
}

func TestExecutorDryRunJournalsMappedOrder(t *testing.T) {
	path := t.TempDir() + "/kalshi-dry.jsonl"
	executor := NewExecutor(&Client{maxOrderCents: 200}, NewMapper(MapperConfig{MaxOrderCents: 200, MinConviction: 0.65}), ExecutionDryRun, path)

	result, err := executor.SubmitThesis(context.Background(), &model.Thesis{
		ID:         "thesis-4",
		DeskID:     "kalshi-rates-a",
		Domain:     "prediction_market",
		Instrument: model.Instrument{Symbol: "KXTEST-26DEC31-YES", SecType: "KALSHI", Currency: "USD"},
		Direction:  model.Long,
		Conviction: 0.82,
		EntryPrice: 0.24,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.DryRun {
		t.Fatal("expected dry run result")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if err := json.Unmarshal(raw[:len(raw)-1], &record); err != nil {
		t.Fatal(err)
	}
	if record["mode"] != string(ExecutionDryRun) {
		t.Fatalf("unexpected journal record: %+v", record)
	}
}

func TestExecutorLiveCanaryBlocksSecondSessionOrder(t *testing.T) {
	t.Setenv("KALSHI_LIVE_MAX_ORDERS_PER_SESSION", "1")
	t.Setenv("KALSHI_LIVE_MAX_RISK_DOLLARS_PER_SESSION", "1.00")
	t.Setenv("KALSHI_LIVE_DISABLE_AFTER_FIRST", "true")

	orderCalls := 0
	client := newLiveOrderTestClient(t, &orderCalls)
	executor := NewExecutor(client, NewMapper(MapperConfig{MaxOrderCents: 100, MinConviction: 0.65}), ExecutionLive, t.TempDir()+"/kalshi-live.jsonl")

	first, err := executor.SubmitThesis(context.Background(), liveTestThesis("thesis-live-1", "KXTESTA-26DEC31-YES", 0.25))
	if err != nil {
		t.Fatal(err)
	}
	if first.DryRun || first.Response == nil {
		t.Fatalf("expected live order response, got %+v", first)
	}

	second, err := executor.SubmitThesis(context.Background(), liveTestThesis("thesis-live-2", "KXTESTB-26DEC31-YES", 0.25))
	if err == nil {
		t.Fatal("expected canary cap to block second live order")
	}
	if !strings.Contains(err.Error(), "kalshi_live_canary_already_used") {
		t.Fatalf("unexpected error: %v", err)
	}
	if second == nil || second.Error == "" {
		t.Fatalf("expected failed result to be returned for persistence, got %+v", second)
	}
	if orderCalls != 1 {
		t.Fatalf("order endpoint calls = %d, want 1", orderCalls)
	}
}

func TestExecutorLiveKillSwitchBlocksBeforeSubmit(t *testing.T) {
	t.Setenv("KALSHI_LIVE_KILL_SWITCH", "true")

	orderCalls := 0
	client := newLiveOrderTestClient(t, &orderCalls)
	executor := NewExecutor(client, NewMapper(MapperConfig{MaxOrderCents: 100, MinConviction: 0.65}), ExecutionLive, t.TempDir()+"/kalshi-live.jsonl")

	result, err := executor.SubmitThesis(context.Background(), liveTestThesis("thesis-kill", "KXTESTA-26DEC31-YES", 0.25))
	if err == nil {
		t.Fatal("expected kill switch to block live order")
	}
	if !strings.Contains(err.Error(), "kalshi_live_kill_switch_enabled") {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil || result.Error == "" {
		t.Fatalf("expected failed result to be returned for persistence, got %+v", result)
	}
	if orderCalls != 0 {
		t.Fatalf("order endpoint calls = %d, want 0", orderCalls)
	}
}

func TestExecutorLiveSessionRiskCapBlocksBeforeSubmit(t *testing.T) {
	t.Setenv("KALSHI_LIVE_DISABLE_AFTER_FIRST", "false")
	t.Setenv("KALSHI_LIVE_MAX_ORDERS_PER_SESSION", "10")
	t.Setenv("KALSHI_LIVE_MAX_RISK_DOLLARS_PER_SESSION", "0.75")

	orderCalls := 0
	client := newLiveOrderTestClient(t, &orderCalls)
	executor := NewExecutor(client, NewMapper(MapperConfig{MaxOrderCents: 50, MinConviction: 0.65}), ExecutionLive, t.TempDir()+"/kalshi-live.jsonl")

	if _, err := executor.SubmitThesis(context.Background(), liveTestThesis("thesis-risk-1", "KXRISKA-26DEC31-YES", 0.25)); err != nil {
		t.Fatal(err)
	}
	result, err := executor.SubmitThesis(context.Background(), liveTestThesis("thesis-risk-2", "KXRISKB-26DEC31-YES", 0.25))
	if err == nil {
		t.Fatal("expected session risk cap to block second live order")
	}
	if !strings.Contains(err.Error(), "kalshi_live_session_risk_cap_reached") {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil || result.Error == "" {
		t.Fatalf("expected failed result to be returned for persistence, got %+v", result)
	}
	if orderCalls != 1 {
		t.Fatalf("order endpoint calls = %d, want 1", orderCalls)
	}
}

func TestExecutorLiveReleasesSafetyReservationOnSubmitError(t *testing.T) {
	t.Setenv("KALSHI_LIVE_DISABLE_AFTER_FIRST", "false")
	t.Setenv("KALSHI_LIVE_MAX_ORDERS_PER_SESSION", "10")
	t.Setenv("KALSHI_LIVE_MAX_RISK_DOLLARS_PER_SESSION", "0.75")

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	orderCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/trade-api/v2/portfolio/events/orders" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		orderCalls++
		if orderCalls == 1 {
			http.Error(w, `{"error":{"code":"temporary"}}`, http.StatusInternalServerError)
			return
		}
		var payload createOrderV2Request
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(`{"order_id":"ord-live-2","client_order_id":"` + payload.ClientOrderID + `","fill_count":"0.00","remaining_count":"2.00"}`))
	}))
	defer server.Close()

	client, err := NewClient(Config{
		BaseURL:       server.URL + "/trade-api/v2",
		KeyID:         "key-id",
		PrivateKeyPEM: privateKeyPEM(privateKey),
		LiveTrading:   true,
		LiveConfirm:   LiveConfirmation,
		MaxOrderCents: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	executor := NewExecutor(client, NewMapper(MapperConfig{MaxOrderCents: 50, MinConviction: 0.65}), ExecutionLive, t.TempDir()+"/kalshi-live.jsonl")

	if _, err := executor.SubmitThesis(context.Background(), liveTestThesis("thesis-error-1", "KXERR1-26DEC31-YES", 0.25)); err == nil {
		t.Fatal("expected first submit to fail")
	}
	second, err := executor.SubmitThesis(context.Background(), liveTestThesis("thesis-error-2", "KXERR2-26DEC31-YES", 0.25))
	if err != nil {
		t.Fatalf("expected second submit to pass after reservation release, got %v", err)
	}
	if second == nil || second.Response == nil || second.Response.OrderID != "ord-live-2" {
		t.Fatalf("unexpected second submit result: %+v", second)
	}
	if orderCalls != 2 {
		t.Fatalf("order endpoint calls = %d, want 2", orderCalls)
	}
}

func liveTestThesis(id, ticker string, entryPrice float64) *model.Thesis {
	return &model.Thesis{
		ID:         id,
		DeskID:     "kalshi-macro-a",
		Domain:     "prediction_market",
		Instrument: model.Instrument{Symbol: ticker, SecType: "KALSHI", Currency: "USD"},
		Direction:  model.Long,
		Conviction: 0.82,
		EntryPrice: entryPrice,
	}
}

func kalshiPlayerPropThesis(evidence string) *model.Thesis {
	return &model.Thesis{
		ID:         "thesis-haaland",
		DeskID:     "kalshi-sports-a",
		Domain:     "prediction_market",
		Instrument: model.Instrument{Symbol: "KXGOAL-26JUN26-HAALAND", SecType: "KALSHI", Currency: "USD"},
		Direction:  model.Long,
		Conviction: 0.82,
		EntryPrice: 0.24,
		Evidence: []model.Evidence{{
			Source:  "kalshi-market",
			Content: evidence,
			Weight:  1,
		}},
	}
}

func newLiveOrderTestClient(t *testing.T, orderCalls *int) *Client {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/trade-api/v2/portfolio/events/orders" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		*orderCalls++
		var payload createOrderV2Request
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(`{"order_id":"ord-live","client_order_id":"` + payload.ClientOrderID + `","fill_count":"0.00","remaining_count":"1.00"}`))
	}))
	t.Cleanup(server.Close)

	client, err := NewClient(Config{
		BaseURL:       server.URL + "/trade-api/v2",
		KeyID:         "key-id",
		PrivateKeyPEM: privateKeyPEM(privateKey),
		LiveTrading:   true,
		LiveConfirm:   LiveConfirmation,
		MaxOrderCents: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	return client
}
