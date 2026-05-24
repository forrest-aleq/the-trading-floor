package kalshi

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
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
