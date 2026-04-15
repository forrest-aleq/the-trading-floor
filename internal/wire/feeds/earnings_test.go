package feeds

import "testing"

func TestResolveFMPAPIKeyPrecedence(t *testing.T) {
	t.Setenv("FMP_API_KEY", "from-fmp")
	t.Setenv("EARNINGS_API_KEY", "from-legacy")

	if got := resolveFMPAPIKey("explicit"); got != "explicit" {
		t.Fatalf("explicit key = %q, want explicit", got)
	}
	if got := resolveFMPAPIKey(""); got != "from-fmp" {
		t.Fatalf("resolved key = %q, want from-fmp", got)
	}
}

func TestResolveFMPAPIKeyFallsBackToLegacyAlias(t *testing.T) {
	t.Setenv("FMP_API_KEY", "")
	t.Setenv("EARNINGS_API_KEY", "from-legacy")

	if got := resolveFMPAPIKey(""); got != "from-legacy" {
		t.Fatalf("resolved key = %q, want from-legacy", got)
	}
}
