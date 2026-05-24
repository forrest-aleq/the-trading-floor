package model

import "testing"

func TestNormalizeKalshiInstrument(t *testing.T) {
	inst := NormalizeKalshiInstrument(Instrument{
		Symbol:     " kxratecut-26 ",
		SecType:    "STK",
		Exchange:   "SMART",
		Currency:   "",
		Expiry:     "20260601",
		Strike:     10,
		Right:      "C",
		Multiplier: "100",
		ConID:      123,
	})

	if inst.Symbol != "KXRATECUT-26" {
		t.Fatalf("expected uppercase Kalshi ticker, got %q", inst.Symbol)
	}
	if inst.SecType != SecTypeKalshi || inst.Exchange != SecTypeKalshi {
		t.Fatalf("expected Kalshi venue fields, got sec_type=%q exchange=%q", inst.SecType, inst.Exchange)
	}
	if inst.Currency != "USD" {
		t.Fatalf("expected USD currency, got %q", inst.Currency)
	}
	if inst.Expiry != "" || inst.Strike != 0 || inst.Right != "" || inst.Multiplier != "" || inst.ConID != 0 {
		t.Fatalf("expected derivative/IBKR fields to be cleared, got %+v", inst)
	}
}

func TestInstrumentIsKalshi(t *testing.T) {
	for _, inst := range []Instrument{
		{Symbol: "KXBTC-26", SecType: "STK"},
		{Symbol: "SOME-MARKET", SecType: SecTypeKalshi},
	} {
		if !inst.IsKalshi() {
			t.Fatalf("expected %+v to be recognized as Kalshi", inst)
		}
	}
}

func TestIsKalshiTickerAvoidsPlainEquityPrefixes(t *testing.T) {
	if !IsKalshiTicker("KXBTC-26") {
		t.Fatal("expected hyphenated KX event ticker to be Kalshi")
	}
	if IsKalshiTicker("KXIN") {
		t.Fatal("expected plain equity-style KX prefix to stay non-Kalshi")
	}
}
