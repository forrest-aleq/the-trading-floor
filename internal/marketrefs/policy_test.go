package marketrefs

import "testing"

func TestParsePolicyRequiresRegimeInstruments(t *testing.T) {
	t.Parallel()

	_, err := parsePolicy([]byte(`{
	  "market_signal_watchlist":[],
	  "startup_pricing_watchlist":[],
	  "earnings_watchlist":[],
	  "regime_instruments":{"volatility":{"symbol":"VIX","sec_type":"IND","exchange":"CBOE","currency":"USD"}}
	}`))
	if err == nil {
		t.Fatal("expected error for incomplete regime instruments")
	}
}

func TestParsePolicyNormalizesInstruments(t *testing.T) {
	t.Parallel()

	policy, err := parsePolicy([]byte(`{
	  "market_signal_watchlist":[{"symbol":" spy ","sec_type":"stk","exchange":"smart","currency":"usd"}],
	  "startup_pricing_watchlist":[{"symbol":" qqq ","sec_type":"stk","exchange":"smart","currency":"usd"}],
	  "earnings_watchlist":[{"symbol":" aapl ","sec_type":"stk","exchange":"smart","currency":"usd"}],
	  "regime_detection_mode":" proxy ",
	  "regime_instruments":{
	    "volatility":{"symbol":" vix ","sec_type":"ind","exchange":"cboe","currency":"usd"},
	    "trend":{"symbol":" spy ","sec_type":"stk","exchange":"smart","currency":"usd"},
	    "risk":{"symbol":" tlt ","sec_type":"stk","exchange":"smart","currency":"usd"}
	  }
	}`))
	if err != nil {
		t.Fatalf("parsePolicy returned error: %v", err)
	}
	if got := policy.MarketSignalWatchlist[0].Symbol; got != "SPY" {
		t.Fatalf("market signal symbol = %s, want SPY", got)
	}
	if got := policy.StartupPricingWatchlist[0].Symbol; got != "QQQ" {
		t.Fatalf("startup pricing symbol = %s, want QQQ", got)
	}
	if got := policy.EarningsWatchlist[0].Symbol; got != "AAPL" {
		t.Fatalf("earnings symbol = %s, want AAPL", got)
	}
	if got := policy.RegimeDetectionMode; got != "proxy" {
		t.Fatalf("regime detection mode = %s, want proxy", got)
	}
	if got := policy.RegimeInstruments.Volatility.Symbol; got != "VIX" {
		t.Fatalf("volatility symbol = %s, want VIX", got)
	}
}

func TestParsePolicyAllowsEmptySignalAndStartupWatchlists(t *testing.T) {
	t.Parallel()

	policy, err := parsePolicy([]byte(`{
	  "market_signal_watchlist":[],
	  "startup_pricing_watchlist":[],
	  "earnings_watchlist":[],
	  "regime_instruments":{
	    "volatility":{"symbol":"VIX","sec_type":"IND","exchange":"CBOE","currency":"USD"},
	    "trend":{"symbol":"SPY","sec_type":"STK","exchange":"SMART","currency":"USD"},
	    "risk":{"symbol":"TLT","sec_type":"STK","exchange":"SMART","currency":"USD"}
	  }
	}`))
	if err != nil {
		t.Fatalf("parsePolicy returned error: %v", err)
	}
	if len(policy.MarketSignalWatchlist) != 0 {
		t.Fatalf("market signal watchlist len = %d, want 0", len(policy.MarketSignalWatchlist))
	}
	if len(policy.StartupPricingWatchlist) != 0 {
		t.Fatalf("startup pricing watchlist len = %d, want 0", len(policy.StartupPricingWatchlist))
	}
	if policy.RegimeDetectionMode != "off" {
		t.Fatalf("regime detection mode = %s, want off", policy.RegimeDetectionMode)
	}
}

func TestParsePolicyAllowsEmptyEarningsUniverse(t *testing.T) {
	t.Parallel()

	policy, err := parsePolicy([]byte(`{
	  "market_signal_watchlist":[],
	  "startup_pricing_watchlist":[],
	  "earnings_watchlist":[],
	  "regime_detection_mode":"off",
	  "regime_instruments":{
	    "volatility":{"symbol":"VIX","sec_type":"IND","exchange":"CBOE","currency":"USD"},
	    "trend":{"symbol":"SPY","sec_type":"STK","exchange":"SMART","currency":"USD"},
	    "risk":{"symbol":"TLT","sec_type":"STK","exchange":"SMART","currency":"USD"}
	  }
	}`))
	if err != nil {
		t.Fatalf("parsePolicy returned error: %v", err)
	}
	if len(policy.EarningsWatchlist) != 0 {
		t.Fatalf("earnings watchlist len = %d, want 0", len(policy.EarningsWatchlist))
	}
}
