package kalshi

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestValidateOrderCapsRisk(t *testing.T) {
	client, err := NewClient(Config{MaxOrderCents: 200})
	if err != nil {
		t.Fatal(err)
	}

	validation, err := client.ValidateOrder(OrderRequest{
		Ticker:          "KXTEST-26DEC31-YES",
		ClientOrderID:   "test-order",
		Side:            "yes",
		Action:          "buy",
		Count:           2,
		YesPriceDollars: "0.5000",
	})
	if err != nil {
		t.Fatal(err)
	}
	if validation.EstimatedRiskCents != 100 {
		t.Fatalf("estimated risk = %d, want 100", validation.EstimatedRiskCents)
	}

	_, err = client.ValidateOrder(OrderRequest{
		Ticker:         "KXTEST-26DEC31-YES",
		ClientOrderID:  "too-big",
		Side:           "no",
		Action:         "buy",
		Count:          5,
		NoPriceDollars: "0.9000",
	})
	if err == nil {
		t.Fatal("expected risk cap violation")
	}
}

func TestCreateOrderRequiresLiveTrading(t *testing.T) {
	client, err := NewClient(Config{MaxOrderCents: 200})
	if err != nil {
		t.Fatal(err)
	}

	_, err = client.CreateOrder(context.Background(), OrderRequest{
		Ticker:          "KXTEST-26DEC31-YES",
		ClientOrderID:   "blocked",
		Side:            "yes",
		Action:          "buy",
		Count:           1,
		YesPriceDollars: "0.1000",
	})
	if err == nil {
		t.Fatal("expected disabled live trading to block order")
	}
}

func TestCreateOrderRequiresLiveConfirmation(t *testing.T) {
	client, err := NewClient(Config{LiveTrading: true, MaxOrderCents: 200})
	if err != nil {
		t.Fatal(err)
	}

	_, err = client.CreateOrder(context.Background(), OrderRequest{
		Ticker:          "KXTEST-26DEC31-YES",
		ClientOrderID:   "blocked-confirm",
		Side:            "yes",
		Action:          "buy",
		Count:           1,
		YesPriceDollars: "0.1000",
	})
	if err == nil {
		t.Fatal("expected missing live confirmation to block order")
	}
}

func TestGetMarketUsesTickerEndpoint(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/trade-api/v2/markets/KXMVESPORTSMULTIGAMEEXTENDED-S202601A7277A770-22D4C50549A" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"market":{"ticker":"KXMVESPORTSMULTIGAMEEXTENDED-S202601A7277A770-22D4C50549A","mve_collection_ticker":"KXMVESPORTSMULTIGAMEEXTENDED","mve_selected_legs":[{"event_ticker":"KXMLBGAME-26JUN262145LADSD","market_ticker":"KXMLBGAME-26JUN262145LADSD-LAD","side":"yes"}]}}`))
	}))
	defer server.Close()

	client, err := NewClient(Config{
		BaseURL:       server.URL + "/trade-api/v2",
		KeyID:         "key-id",
		PrivateKeyPEM: privateKeyPEM(privateKey),
		MaxOrderCents: 200,
	})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.GetMarket(context.Background(), "KXMVESPORTSMULTIGAMEEXTENDED-S202601A7277A770-22D4C50549A")
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Market.MVESelectedLegs) != 1 || resp.Market.MVESelectedLegs[0].MarketTicker != "KXMLBGAME-26JUN262145LADSD-LAD" {
		t.Fatalf("unexpected market response: %+v", resp)
	}
}

func TestGetMarketsWithMVEFilterUsesQuery(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/trade-api/v2/markets" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("mve_filter"); got != "exclude" {
			t.Fatalf("mve_filter = %q, want exclude", got)
		}
		if got := r.URL.Query().Get("status"); got != "open" {
			t.Fatalf("status = %q, want open", got)
		}
		_, _ = w.Write([]byte(`{"markets":[{"ticker":"KXFEDCUT-26"}],"cursor":""}`))
	}))
	defer server.Close()

	client, err := NewClient(Config{
		BaseURL:       server.URL + "/trade-api/v2",
		KeyID:         "key-id",
		PrivateKeyPEM: privateKeyPEM(privateKey),
		MaxOrderCents: 200,
	})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.GetMarketsWithMVEFilter(context.Background(), "open", 250, "", "exclude")
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Markets) != 1 || resp.Markets[0].Ticker != "KXFEDCUT-26" {
		t.Fatalf("unexpected markets response: %+v", resp)
	}
}

func TestCreateOrderUsesCurrentKalshiOrderEndpointAndSchema(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/trade-api/v2/portfolio/events/orders" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["side"] != "ask" {
			t.Fatalf("unexpected side payload: %+v", payload)
		}
		if payload["price"] != "0.8300" {
			t.Fatalf("price = %v, want 0.8300", payload["price"])
		}
		if _, ok := payload["action"]; ok {
			t.Fatalf("legacy action field should not be sent: %+v", payload)
		}
		if _, ok := payload["no_price_dollars"]; ok {
			t.Fatalf("legacy no_price_dollars field should not be sent: %+v", payload)
		}
		if payload["count"] != "1.00" {
			t.Fatalf("count = %v, want 1.00", payload["count"])
		}
		_, _ = w.Write([]byte(`{"order_id":"ord-1","client_order_id":"client-123","fill_count":"0.00","remaining_count":"1.00"}`))
	}))
	defer server.Close()

	client, err := NewClient(Config{
		BaseURL:       server.URL + "/trade-api/v2",
		KeyID:         "key-id",
		PrivateKeyPEM: privateKeyPEM(privateKey),
		LiveTrading:   true,
		LiveConfirm:   LiveConfirmation,
		MaxOrderCents: 200,
	})
	if err != nil {
		t.Fatal(err)
	}

	resp, err := client.CreateOrder(context.Background(), OrderRequest{
		Ticker:         "KXTEST-26DEC31-YES",
		ClientOrderID:  "client-123",
		Side:           "no",
		Action:         "buy",
		Count:          1,
		NoPriceDollars: "0.1700",
		TimeInForce:    "immediate_or_cancel",
		BuyMaxCost:     17,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.OrderID != "ord-1" || !resp.IsResting() || resp.HasFill() {
		t.Fatalf("unexpected order response: %+v", resp)
	}
}

func TestAuthenticatedRequestSignsKalshiHeaders(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/trade-api/v2/portfolio/balance" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		keyID := r.Header.Get("KALSHI-ACCESS-KEY")
		timestamp := r.Header.Get("KALSHI-ACCESS-TIMESTAMP")
		signature := r.Header.Get("KALSHI-ACCESS-SIGNATURE")
		if keyID != "key-id" || timestamp == "" || signature == "" {
			t.Fatalf("missing auth headers: key=%q ts=%q sig=%q", keyID, timestamp, signature)
		}
		rawSig, err := base64.StdEncoding.DecodeString(signature)
		if err != nil {
			t.Fatal(err)
		}
		msg := timestamp + http.MethodGet + "/trade-api/v2/portfolio/balance"
		digest := sha256.Sum256([]byte(msg))
		if err := rsa.VerifyPSS(&privateKey.PublicKey, crypto.SHA256, digest[:], rawSig, &rsa.PSSOptions{
			SaltLength: rsa.PSSSaltLengthEqualsHash,
			Hash:       crypto.SHA256,
		}); err != nil {
			t.Fatalf("signature verification failed: %v", err)
		}
		_, _ = w.Write([]byte(`{"balance":5000,"portfolio_value":5000,"updated_ts":123}`))
	}))
	defer server.Close()

	client, err := NewClient(Config{
		BaseURL:       server.URL + "/trade-api/v2",
		KeyID:         "key-id",
		PrivateKeyPEM: privateKeyPEM(privateKey),
		MaxOrderCents: 200,
	})
	if err != nil {
		t.Fatal(err)
	}

	balance, err := client.GetBalance(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if balance.Balance != 5000 || balance.PortfolioValue != 5000 {
		t.Fatalf("unexpected balance: %+v", balance)
	}
}

func privateKeyPEM(key *rsa.PrivateKey) string {
	return string(pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	}))
}
