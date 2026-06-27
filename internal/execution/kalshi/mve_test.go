package kalshi

import "testing"

func TestMultivariateTickerDetection(t *testing.T) {
	t.Setenv(unsafeAllowMVEWrappersEnv, "false")

	if !IsMultivariateTicker("KXMVESPORTSMULTIGAMEEXTENDED-S202601A7277A770-22D4C50549A") {
		t.Fatal("expected KXMVE wrapper ticker to be detected")
	}
	if IsMultivariateTicker("KXWCGAME-26JUN26NORFRA-FRA") {
		t.Fatal("expected normal Kalshi market ticker to remain non-MVE")
	}
	if !ShouldBlockMultivariateTicker("kxmvEcRosScategory-s2026-test") {
		t.Fatal("expected MVE wrapper to be blocked by default")
	}
}
