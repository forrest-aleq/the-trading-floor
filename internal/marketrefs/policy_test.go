package marketrefs

import "testing"

func TestParsePolicyRequiresRegimeInstruments(t *testing.T) {
	t.Parallel()

	_, err := parsePolicy([]byte(`{
	  "bootstrap_watchlist":[{"symbol":"SPY","sec_type":"STK","exchange":"SMART","currency":"USD"}],
	  "earnings_watchlist":[{"symbol":"AAPL","sec_type":"STK","exchange":"SMART","currency":"USD"}],
	  "regime_instruments":{"volatility":{"symbol":"VIX","sec_type":"IND","exchange":"CBOE","currency":"USD"}}
	}`))
	if err == nil {
		t.Fatal("expected error for incomplete regime instruments")
	}
}

func TestParsePolicyNormalizesInstruments(t *testing.T) {
	t.Parallel()

	policy, err := parsePolicy([]byte(`{
	  "bootstrap_watchlist":[{"symbol":" spy ","sec_type":"stk","exchange":"smart","currency":"usd"}],
	  "earnings_watchlist":[{"symbol":" aapl ","sec_type":"stk","exchange":"smart","currency":"usd"}],
	  "regime_instruments":{
	    "volatility":{"symbol":" vix ","sec_type":"ind","exchange":"cboe","currency":"usd"},
	    "trend":{"symbol":" spy ","sec_type":"stk","exchange":"smart","currency":"usd"},
	    "risk":{"symbol":" tlt ","sec_type":"stk","exchange":"smart","currency":"usd"}
	  }
	}`))
	if err != nil {
		t.Fatalf("parsePolicy returned error: %v", err)
	}
	if got := policy.BootstrapWatchlist[0].Symbol; got != "SPY" {
		t.Fatalf("bootstrap symbol = %s, want SPY", got)
	}
	if got := policy.EarningsWatchlist[0].Symbol; got != "AAPL" {
		t.Fatalf("earnings symbol = %s, want AAPL", got)
	}
	if got := policy.RegimeInstruments.Volatility.Symbol; got != "VIX" {
		t.Fatalf("volatility symbol = %s, want VIX", got)
	}
}
