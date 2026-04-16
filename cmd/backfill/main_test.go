package main

import (
	"errors"
	"testing"

	"github.com/hnic/trading-floor/pkg/model"
)

func TestNormalizeReplayModeDefaultsToResearch(t *testing.T) {
	if got := normalizeReplayMode(""); got != "research" {
		t.Fatalf("expected research default, got %q", got)
	}
	if got := normalizeReplayMode("scan"); got != "scan" {
		t.Fatalf("expected explicit scan mode, got %q", got)
	}
	if got := normalizeReplayMode("backtest"); got != "backtest" {
		t.Fatalf("expected explicit backtest mode, got %q", got)
	}
}

func TestFilterDomainsHonorsAllowList(t *testing.T) {
	got := filterDomains([]string{"macro", "tail", "corporate"}, map[string]struct{}{
		"tail":  {},
		"macro": {},
	})
	if len(got) != 2 || got[0] != "macro" || got[1] != "tail" {
		t.Fatalf("unexpected filtered domains: %#v", got)
	}
}

func TestFilterDomainsSortsWhenAllowListIsEmpty(t *testing.T) {
	got := filterDomains([]string{"tail", "macro", "corporate"}, nil)
	if len(got) != 3 || got[0] != "corporate" || got[1] != "macro" || got[2] != "tail" {
		t.Fatalf("unexpected sorted domains: %#v", got)
	}
}

func TestClassifyReplayErrorBucketsStructuredFailures(t *testing.T) {
	tests := map[string]string{
		"research JSON extraction: terminal JSON block missing": "json_extraction",
		"research response validation: missing field":           "validation",
		"research parse error: invalid character":               "parse_error",
		"context deadline exceeded":                             "timeout",
		"other failure":                                         "other",
	}
	for input, want := range tests {
		if got := classifyReplayError(errors.New(input)); got != want {
			t.Fatalf("classifyReplayError(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestCloneThesisDeepCopiesSlices(t *testing.T) {
	thesis := &model.Thesis{
		Evidence:    []model.Evidence{{Content: "one"}},
		CounterArgs: []string{"risk"},
		Legs:        []model.TradeLeg{{Instrument: model.Instrument{Symbol: "AAPL", SecType: "STK", Currency: "USD"}}},
	}

	cloned := cloneThesis(thesis)
	cloned.Evidence[0].Content = "changed"
	cloned.CounterArgs[0] = "changed"
	cloned.Legs[0].Instrument.Symbol = "MSFT"

	if thesis.Evidence[0].Content != "one" {
		t.Fatalf("expected evidence slice to be copied, got %#v", thesis.Evidence)
	}
	if thesis.CounterArgs[0] != "risk" {
		t.Fatalf("expected counter args slice to be copied, got %#v", thesis.CounterArgs)
	}
	if thesis.Legs[0].Instrument.Symbol != "AAPL" {
		t.Fatalf("expected legs slice to be copied, got %#v", thesis.Legs)
	}
}

func TestRecordScanRejectTracksGlobalAndDomainCounts(t *testing.T) {
	summary := replaySummary{ScanRejects: map[string]int{}}
	stats := domainStats{}

	recordScanReject(&summary, &stats, "score_below_threshold")
	recordScanReject(&summary, &stats, "score_below_threshold")

	if got := summary.ScanRejects["score_below_threshold"]; got != 2 {
		t.Fatalf("unexpected global reject count %d", got)
	}
	if got := stats.ScanRejects["score_below_threshold"]; got != 2 {
		t.Fatalf("unexpected domain reject count %d", got)
	}
}

func TestRecordResearchRejectTracksGlobalAndDomainCounts(t *testing.T) {
	summary := replaySummary{ResearchRejects: map[string]int{}}
	stats := domainStats{}

	recordResearchReject(&summary, &stats, "conviction_below_threshold")
	recordResearchReject(&summary, &stats, "conviction_below_threshold")

	if got := summary.ResearchRejects["conviction_below_threshold"]; got != 2 {
		t.Fatalf("unexpected global reject count %d", got)
	}
	if got := stats.ResearchRejects["conviction_below_threshold"]; got != 2 {
		t.Fatalf("unexpected domain reject count %d", got)
	}
}
