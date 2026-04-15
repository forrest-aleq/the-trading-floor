package main

import (
	"errors"
	"testing"
)

func TestNormalizeReplayModeDefaultsToResearch(t *testing.T) {
	if got := normalizeReplayMode(""); got != "research" {
		t.Fatalf("expected research default, got %q", got)
	}
	if got := normalizeReplayMode("scan"); got != "scan" {
		t.Fatalf("expected explicit scan mode, got %q", got)
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
