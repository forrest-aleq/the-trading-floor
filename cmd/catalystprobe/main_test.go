package main

import "testing"

func TestSelectDotPathHandlesObjectsAndArrays(t *testing.T) {
	value := map[string]any{
		"chart": map[string]any{
			"entries": []any{
				map[string]any{"name": "first"},
				map[string]any{"name": "second"},
			},
		},
	}
	got, ok := selectDotPath(value, "chart.entries.1.name")
	if !ok {
		t.Fatal("expected dot path to resolve")
	}
	if got != "second" {
		t.Fatalf("got %v, want second", got)
	}
	if _, ok := selectDotPath(value, "chart.entries.5.name"); ok {
		t.Fatal("expected missing array path to fail")
	}
}

func TestSummarizeESPNLineupTracksWatchedPlayers(t *testing.T) {
	active := true
	starter := false
	summary := espnSummary{}
	summary.Header.Name = "Canada vs South Africa"
	summary.Rosters = append(summary.Rosters, struct {
		Team struct {
			DisplayName  string `json:"displayName"`
			ShortName    string `json:"shortDisplayName"`
			Abbreviation string `json:"abbreviation"`
		} `json:"team"`
		Roster []struct {
			Active  *bool `json:"active"`
			Starter *bool `json:"starter"`
			Athlete struct {
				FullName    string `json:"fullName"`
				DisplayName string `json:"displayName"`
				ShortName   string `json:"shortName"`
			} `json:"athlete"`
			Position struct {
				DisplayName  string `json:"displayName"`
				Abbreviation string `json:"abbreviation"`
			} `json:"position"`
		} `json:"roster"`
	}{})
	summary.Rosters[0].Team.DisplayName = "Canada"
	summary.Rosters[0].Roster = append(summary.Rosters[0].Roster, struct {
		Active  *bool `json:"active"`
		Starter *bool `json:"starter"`
		Athlete struct {
			FullName    string `json:"fullName"`
			DisplayName string `json:"displayName"`
			ShortName   string `json:"shortName"`
		} `json:"athlete"`
		Position struct {
			DisplayName  string `json:"displayName"`
			Abbreviation string `json:"abbreviation"`
		} `json:"position"`
	}{})
	summary.Rosters[0].Roster[0].Active = &active
	summary.Rosters[0].Roster[0].Starter = &starter
	summary.Rosters[0].Roster[0].Athlete.DisplayName = "Jonathan David"
	summary.Rosters[0].Roster[0].Position.DisplayName = "Forward"

	state := summarizeESPNLineup(summary, []string{"Jonathan David"})
	if state["roster_count"] != 1 || state["active_true"] != 1 || state["starter_true"] != 0 {
		t.Fatalf("unexpected lineup state: %+v", state)
	}
	watched := state["watched"].(map[string]any)
	if _, ok := watched["Jonathan David"]; !ok {
		t.Fatalf("expected watched player entry, got %+v", watched)
	}
}
