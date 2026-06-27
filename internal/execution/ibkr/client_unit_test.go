package ibkr

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/scmhub/ibsync"

	"github.com/hnic/trading-floor/pkg/model"
)

type fakeConnection struct {
	mu           sync.Mutex
	connectErr   error
	connectCalls int
	loopCalls    int
}

func (f *fakeConnection) Connect(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.connectCalls++
	return f.connectErr
}

func (f *fakeConnection) Disconnect() {}

func (f *fakeConnection) IB() *ibsync.IB { return nil }

func (f *fakeConnection) IsConnected() bool { return false }

func (f *fakeConnection) IsPaper() bool { return true }

func (f *fakeConnection) Status() ConnectionStatus { return ConnectionStatus{} }

func (f *fakeConnection) RunReconnectLoop(ctx context.Context) {
	f.mu.Lock()
	f.loopCalls++
	f.mu.Unlock()
	<-ctx.Done()
}

func (f *fakeConnection) counts() (connects int, loops int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.connectCalls, f.loopCalls
}

func TestClientConnectStartsReconnectLoopOnInitialFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	conn := &fakeConnection{connectErr: errors.New("unavailable")}
	client := &Client{conn: conn, log: slog.Default()}

	if err := client.Connect(ctx); err == nil {
		t.Fatal("expected connect error")
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		connects, loops := conn.counts()
		if connects == 1 && loops == 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	connects, loops := conn.counts()
	t.Fatalf("expected one connect and one reconnect loop start, got connects=%d loops=%d", connects, loops)
}

func TestClientConnectStartsReconnectLoopOnlyOnce(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	conn := &fakeConnection{connectErr: errors.New("unavailable")}
	client := &Client{conn: conn, log: slog.Default()}

	_ = client.Connect(ctx)
	_ = client.Connect(ctx)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		connects, loops := conn.counts()
		if connects == 2 && loops == 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	connects, loops := conn.counts()
	t.Fatalf("expected two connect attempts and one reconnect loop start, got connects=%d loops=%d", connects, loops)
}

func TestRunBlockingIBCallRespectsContextDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := runBlockingIBCall(ctx, func() error {
		<-make(chan struct{})
		return nil
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline exceeded, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
		t.Fatalf("expected blocking call to stop promptly, took %s", elapsed)
	}
}

func TestAccountSummaryFromValuesParsesAccountUpdateFields(t *testing.T) {
	t.Setenv("IBKR_ACCOUNT", "DU123")

	summary, ok, err := accountSummaryFromValues(ibsync.AccountValues{
		{Account: "DU123", Tag: "NetLiquidation", Value: "1011575.88", Currency: "USD"},
		{Account: "DU123", Tag: "TotalCashValue", Value: "2744047.00", Currency: "USD"},
		{Account: "DU123", Tag: "BuyingPower", Value: "338900.00", Currency: "USD"},
		{Account: "DU123", Tag: "SMA", Value: "-117000.00", Currency: "USD"},
		{Account: "DU123", Tag: "MaintMarginReq", Value: "669000.00", Currency: "USD"},
		{Account: "DU123", Tag: "ExcessLiquidity", Value: "358900.00", Currency: "USD"},
		{Account: "DU123", Tag: "RegTEquity", Value: "800000.00", Currency: "USD"},
		{Account: "DU123", Tag: "RegTMargin", Value: "600000.00", Currency: "USD"},
	}, []string{"DU123"})
	if err != nil {
		t.Fatalf("accountSummaryFromValues returned error: %v", err)
	}
	if !ok {
		t.Fatal("expected account values to produce a summary")
	}
	if summary.NetLiquidation != 1011575.88 {
		t.Fatalf("net liquidation = %.2f", summary.NetLiquidation)
	}
	if summary.Cash != 2744047.00 {
		t.Fatalf("cash = %.2f", summary.Cash)
	}
	if summary.SMA != -117000.00 {
		t.Fatalf("SMA = %.2f", summary.SMA)
	}
	if summary.MaintMarginReq != 669000.00 {
		t.Fatalf("maintenance margin = %.2f", summary.MaintMarginReq)
	}
	if summary.ExcessLiquidity != 358900.00 {
		t.Fatalf("excess liquidity = %.2f", summary.ExcessLiquidity)
	}
	if summary.RegTEquity != 800000.00 || summary.RegTMargin != 600000.00 {
		t.Fatalf("RegT fields not parsed: %+v", summary)
	}
}

func TestAccountSummaryFromValuesRejectsAmbiguousAccounts(t *testing.T) {
	_, ok, err := accountSummaryFromValues(ibsync.AccountValues{
		{Account: "DU123", Tag: "NetLiquidation", Value: "1000", Currency: "USD"},
		{Account: "DU456", Tag: "NetLiquidation", Value: "2000", Currency: "USD"},
	}, nil)
	if err == nil {
		t.Fatal("expected ambiguous account error")
	}
	if ok {
		t.Fatal("expected ambiguous values not to produce a summary")
	}
}

func TestNormalizeQuantityUsesWholeSharesForStocks(t *testing.T) {
	if got := normalizeQuantity(12.75, "STK"); got != 12 {
		t.Fatalf("expected stock quantity to floor to 12, got %.4f", got)
	}
	if got := normalizeQuantity(0.5, "STK"); got != 0 {
		t.Fatalf("expected sub-share stock quantity to be invalid, got %.4f", got)
	}
	if got := normalizeQuantity(2.4, "OPT"); got != 2 {
		t.Fatalf("expected option quantity to round to 2, got %.4f", got)
	}
}

func TestBuildContractNormalizesKnownListingSuffixes(t *testing.T) {
	contract := BuildContract(model.Instrument{
		Symbol:   "VOW3.DE",
		SecType:  "STK",
		Exchange: "SMART",
		Currency: "USD",
	})

	if contract.Symbol != "VOW3" {
		t.Fatalf("expected IBKR symbol VOW3, got %q", contract.Symbol)
	}
	if contract.Currency != "EUR" {
		t.Fatalf("expected EUR currency, got %q", contract.Currency)
	}
	if contract.Exchange != "SMART" {
		t.Fatalf("expected SMART exchange, got %q", contract.Exchange)
	}
	if contract.PrimaryExchange != "IBIS" {
		t.Fatalf("expected IBIS primary exchange, got %q", contract.PrimaryExchange)
	}
}

func TestBuildContractLeavesUnknownDotSymbolsUntouched(t *testing.T) {
	contract := BuildContract(model.Instrument{
		Symbol:   "BRK.B",
		SecType:  "STK",
		Exchange: "SMART",
		Currency: "USD",
	})

	if contract.Symbol != "BRK.B" {
		t.Fatalf("expected class-share symbol to remain BRK.B, got %q", contract.Symbol)
	}
	if contract.Currency != "USD" {
		t.Fatalf("expected USD currency, got %q", contract.Currency)
	}
	if contract.PrimaryExchange != "" {
		t.Fatalf("expected no primary exchange for unknown suffix, got %q", contract.PrimaryExchange)
	}
}

func TestBuildContractNormalizesETFToStockContract(t *testing.T) {
	contract := BuildContract(model.Instrument{
		Symbol:   "UNG",
		SecType:  "ETF",
		Exchange: "SMART",
		Currency: "USD",
	})

	if contract.SecType != "STK" {
		t.Fatalf("expected IBKR ETF contract to use STK sec type, got %q", contract.SecType)
	}
}

func TestNormalizeStockOrderRouteSmartRoutesDirectUSExchange(t *testing.T) {
	contract := &ibsync.Contract{
		ConID:           131217639,
		Symbol:          "BB",
		SecType:         "STK",
		Exchange:        "NYSE",
		PrimaryExchange: "NYSE",
		Currency:        "USD",
	}

	normalizeStockOrderRoute(contract)

	if contract.Exchange != "SMART" {
		t.Fatalf("expected SMART exchange, got %q", contract.Exchange)
	}
	if contract.PrimaryExchange != "NYSE" {
		t.Fatalf("expected NYSE primary exchange, got %q", contract.PrimaryExchange)
	}
	if contract.ConID != 131217639 {
		t.Fatalf("expected conId preserved, got %d", contract.ConID)
	}
}

func TestNormalizeStockOrderRouteLeavesNonUSDExchangeAlone(t *testing.T) {
	contract := &ibsync.Contract{
		Symbol:          "9984",
		SecType:         "STK",
		Exchange:        "TSEJ",
		PrimaryExchange: "TSEJ",
		Currency:        "JPY",
	}

	normalizeStockOrderRoute(contract)

	if contract.Exchange != "TSEJ" {
		t.Fatalf("expected non-USD exchange unchanged, got %q", contract.Exchange)
	}
}

func TestQualifyContractRejectsPredictionMarketInstrument(t *testing.T) {
	client := &Client{conn: &fakeConnection{}, log: slog.Default()}
	_, err := client.qualifyContract(context.Background(), model.Instrument{
		Symbol:   "KXTHORNE",
		SecType:  "STK",
		Currency: "USD",
	})
	if err == nil {
		t.Fatal("expected prediction-market symbol to be rejected before IBKR qualification")
	}
	if !strings.Contains(err.Error(), "prediction-market instrument") {
		t.Fatalf("expected prediction-market rejection, got %v", err)
	}
}

func TestOrderReferenceSanitizesAndCapsLength(t *testing.T) {
	got := orderReference("order/with spaces-and_symbols-1234567890-extra")
	want := "orderwithspaces-and_symbols-1234"
	if got != want {
		t.Fatalf("order reference = %q, want %q", got, want)
	}
}

func TestResolveOrderAccountUsesExplicitEnv(t *testing.T) {
	t.Setenv("IBKR_ACCOUNT", " DU12345 ")

	if got := resolveOrderAccount([]string{"DU999"}); got != "DU12345" {
		t.Fatalf("resolveOrderAccount = %q, want explicit env account", got)
	}
}

func TestResolveOrderAccountUsesSingleManagedAccount(t *testing.T) {
	t.Setenv("IBKR_ACCOUNT", "")

	if got := resolveOrderAccount([]string{" DU12345 "}); got != "DU12345" {
		t.Fatalf("resolveOrderAccount = %q, want single managed account", got)
	}
}

func TestResolveOrderAccountLeavesAmbiguousAccountsUnset(t *testing.T) {
	t.Setenv("IBKR_ACCOUNT", "")

	if got := resolveOrderAccount([]string{"DU1", "DU2"}); got != "" {
		t.Fatalf("resolveOrderAccount = %q, want empty for ambiguous accounts", got)
	}
}

func TestResolveBrokerDataAccountPrefersExplicitPnLAccount(t *testing.T) {
	t.Setenv("IBKR_PNL_ACCOUNT", " DU777 ")
	t.Setenv("IBKR_ACCOUNT", "DU123")

	if got := resolveBrokerDataAccount([]string{"DU123"}, []string{"DU123"}); got != "DU777" {
		t.Fatalf("resolveBrokerDataAccount = %q, want explicit PnL account", got)
	}
}

func TestResolveBrokerDataAccountUsesSingleSummaryAccount(t *testing.T) {
	t.Setenv("IBKR_PNL_ACCOUNT", "")
	t.Setenv("IBKR_ACCOUNT", "")

	if got := resolveBrokerDataAccount([]string{"DU999"}, []string{"DU123", "DU123"}); got != "DU123" {
		t.Fatalf("resolveBrokerDataAccount = %q, want single summary account", got)
	}
}

func TestResolveBrokerDataAccountFallsBackToSingleManagedAccount(t *testing.T) {
	t.Setenv("IBKR_PNL_ACCOUNT", "")
	t.Setenv("IBKR_ACCOUNT", "")

	if got := resolveBrokerDataAccount([]string{"DU123"}, nil); got != "DU123" {
		t.Fatalf("resolveBrokerDataAccount = %q, want single managed account", got)
	}
}

func TestResolveBrokerDataAccountRejectsAmbiguousAccounts(t *testing.T) {
	t.Setenv("IBKR_PNL_ACCOUNT", "")
	t.Setenv("IBKR_ACCOUNT", "")

	if got := resolveBrokerDataAccount([]string{"DU1", "DU2"}, []string{"DU1", "DU2"}); got != "" {
		t.Fatalf("resolveBrokerDataAccount = %q, want empty for ambiguous accounts", got)
	}
	if !multipleUniqueAccounts([]string{"DU1", "DU2"}) {
		t.Fatal("expected multiple unique accounts to be detected")
	}
}

func TestValidateBrokerLotSizeRejectsTokyoOddLot(t *testing.T) {
	order := model.Order{
		Instrument: model.Instrument{
			Symbol:   "9984.T",
			SecType:  "STK",
			Exchange: "SMART",
			Currency: "JPY",
		},
		Quantity: 1,
	}
	contract := &ibsync.Contract{
		Symbol:          "9984",
		SecType:         "STK",
		Exchange:        "SMART",
		PrimaryExchange: "TSEJ",
		Currency:        "JPY",
	}

	if err := validateBrokerLotSize(order, contract); err == nil {
		t.Fatal("expected Tokyo odd-lot order to be rejected")
	}
}

func TestValidateBrokerLotSizeAllowsTokyoBoardLot(t *testing.T) {
	order := model.Order{
		Instrument: model.Instrument{
			Symbol:   "9984.T",
			SecType:  "STK",
			Exchange: "SMART",
			Currency: "JPY",
		},
		Quantity: 100,
	}
	contract := &ibsync.Contract{
		Symbol:          "9984",
		SecType:         "STK",
		Exchange:        "SMART",
		PrimaryExchange: "TSEJ",
		Currency:        "JPY",
	}

	if err := validateBrokerLotSize(order, contract); err != nil {
		t.Fatalf("expected Tokyo board-lot order to pass, got %v", err)
	}
}

func TestPendingOrderErrorRequiresBrokerAcceptedStatus(t *testing.T) {
	trade := ibsync.NewTrade(&ibsync.Contract{}, &ibsync.Order{OrderID: 42}, ibsync.OrderStatus{
		OrderID: 42,
		Status:  ibsync.ApiPending,
	})

	pending := pendingOrderError(trade, nil)
	if pending != nil {
		t.Fatalf("expected ApiPending to remain unacknowledged, got %+v", pending)
	}

	trade = ibsync.NewTrade(&ibsync.Contract{}, &ibsync.Order{OrderID: 43}, ibsync.OrderStatus{
		OrderID: 43,
		Status:  ibsync.PendingSubmit,
	})
	pending = pendingOrderError(trade, nil)
	if pending != nil {
		t.Fatalf("expected PendingSubmit to remain unacknowledged, got %+v", pending)
	}
}

func TestPendingOrderErrorRecognizesSubmitted(t *testing.T) {
	trade := ibsync.NewTrade(&ibsync.Contract{}, &ibsync.Order{OrderID: 44}, ibsync.OrderStatus{
		OrderID: 44,
		Status:  ibsync.Submitted,
	})

	pending := pendingOrderError(trade, nil)
	if pending == nil {
		t.Fatal("expected Submitted to be treated as broker-accepted pending order")
	}
	if pending.OrderID != 44 || pending.Status != string(ibsync.Submitted) {
		t.Fatalf("unexpected pending order error: %+v", pending)
	}
}

func TestUnacknowledgedBrokerOrderErrorIsTyped(t *testing.T) {
	cause := errors.New("ack timeout")
	trade := ibsync.NewTrade(&ibsync.Contract{}, &ibsync.Order{OrderID: 45}, ibsync.OrderStatus{
		OrderID: 45,
		Status:  ibsync.ApiPending,
	})

	err := unacknowledgedBrokerOrderError(trade, cause)
	var unack *UnacknowledgedOrderError
	if !errors.As(err, &unack) {
		t.Fatalf("expected typed unacknowledged order error, got %T", err)
	}
	if unack.OrderID != 45 || unack.Status != string(ibsync.ApiPending) {
		t.Fatalf("unexpected unacknowledged order error: %+v", unack)
	}
	if !errors.Is(err, cause) {
		t.Fatalf("expected unacknowledged order error to wrap cause")
	}
	if !strings.Contains(err.Error(), "TWS API order precautions") {
		t.Fatalf("expected ApiPending diagnostic hint, got %v", err)
	}
}

func TestTerminalBrokerErrorCodeFilter(t *testing.T) {
	if !isTerminalBrokerErrorCode(200) {
		t.Fatal("expected no-security-definition code to be terminal")
	}
	if !isTerminalBrokerErrorCode(388) {
		t.Fatal("expected minimum-size rejection code to be terminal")
	}
	if !isTerminalBrokerErrorCode(10243) {
		t.Fatal("expected fractional-order rejection code to be terminal")
	}
	if !isTerminalBrokerErrorCode(10052) {
		t.Fatal("expected invalid time-in-force rejection code to be terminal")
	}
	if !isTerminalBrokerErrorCode(10268) {
		t.Fatal("expected unsupported deprecated order-attribute rejection code to be terminal")
	}
	if !isTerminalBrokerErrorCode(10318) {
		t.Fatal("expected fractional quantity rejection code to be terminal")
	}
	if isTerminalBrokerErrorCode(2104) {
		t.Fatal("expected market data farm status code to be non-terminal")
	}
}

func TestTerminalBrokerLogErrorReportsBrokerMessage(t *testing.T) {
	err := terminalBrokerLogError(46, []ibsync.TradeLogEntry{
		{Message: "OrderStatus"},
		{ErrorCode: 10268, Message: "The 'EtradeOnly' order attribute is not supported."},
	})
	if err == nil {
		t.Fatal("expected terminal broker log error")
	}
	if !strings.Contains(err.Error(), "EtradeOnly") || !strings.Contains(err.Error(), "10268") {
		t.Fatalf("expected broker message and code in error, got %v", err)
	}
}

func TestLatestBrokerLogEntryReturnsLastUsefulEntry(t *testing.T) {
	entry, ok := latestBrokerLogEntry([]ibsync.TradeLogEntry{
		{},
		{Message: "OrderStatus"},
		{ErrorCode: 10318, Message: "Fractional quantity rejected"},
	})
	if !ok {
		t.Fatal("expected latest broker log entry")
	}
	if entry.ErrorCode != 10318 || entry.Message != "Fractional quantity rejected" {
		t.Fatalf("unexpected latest broker log entry: %+v", entry)
	}
}
