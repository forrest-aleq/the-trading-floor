package feeds

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hnic/trading-floor/internal/execution/kalshi"
)

func TestExtractSportsMarketQueryFromKalshiGoalscorerMarket(t *testing.T) {
	query, ok := extractSportsMarketQuery(kalshi.Market{
		Title:    "Norway vs France: Goalscorer",
		Subtitle: "Erling Haaland: 1+",
	})
	if !ok {
		t.Fatal("expected player market query")
	}
	if query.Player != "Erling Haaland" {
		t.Fatalf("player = %q", query.Player)
	}
	if len(query.Teams) != 2 || query.Teams[0] != "Norway" || query.Teams[1] != "France" {
		t.Fatalf("teams = %+v", query.Teams)
	}
}

func TestESPNAvailabilityConfirmsRosterPlayer(t *testing.T) {
	provider := newStubESPNProvider(t, true)

	evidence := provider.CheckMarket(context.Background(), kalshi.Market{
		Title:    "Norway vs France: Goalscorer",
		Subtitle: "Erling Haaland: 1+",
	}, time.Date(2026, 6, 26, 18, 0, 0, 0, time.UTC))

	if evidence.Status != "confirmed" {
		t.Fatalf("status = %q reason=%q", evidence.Status, evidence.Reason)
	}
	if evidence.EventID != "760475" || evidence.Team != "Norway" || evidence.Player != "Erling Haaland" {
		t.Fatalf("unexpected evidence: %+v", evidence)
	}
	if evidence.Active == nil || !*evidence.Active {
		t.Fatalf("expected active player evidence: %+v", evidence)
	}
	line := evidence.EvidenceLine()
	if !strings.Contains(line, "participant_availability: confirmed") || !strings.Contains(line, "active=true") {
		t.Fatalf("unexpected evidence line: %s", line)
	}
}

func TestESPNAvailabilityBlocksInactiveRosterPlayer(t *testing.T) {
	provider := newStubESPNProvider(t, false)

	evidence := provider.CheckMarket(context.Background(), kalshi.Market{
		Title:    "Norway vs France: Goalscorer",
		Subtitle: "Erling Haaland: 1+",
	}, time.Date(2026, 6, 26, 18, 0, 0, 0, time.UTC))

	if evidence.Status != "blocked" || evidence.Reason != "player_inactive" {
		t.Fatalf("unexpected evidence: %+v", evidence)
	}
	if evidence.Active == nil || *evidence.Active {
		t.Fatalf("expected inactive player evidence: %+v", evidence)
	}
}

func newStubESPNProvider(t *testing.T, active bool) *ESPNSportsAvailabilityProvider {
	t.Helper()
	activeValue := "false"
	if active {
		activeValue = "true"
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/fifa.world/scoreboard"):
			_, _ = w.Write([]byte(`{
				"events": [{
					"id": "760475",
					"name": "France at Norway",
					"shortName": "FRA @ NOR",
					"date": "2026-06-26T19:00Z",
					"competitions": [{
						"competitors": [
							{"team": {"displayName": "Norway", "abbreviation": "NOR"}},
							{"team": {"displayName": "France", "abbreviation": "FRA"}}
						]
					}]
				}]
			}`))
		case strings.HasSuffix(r.URL.Path, "/fifa.world/summary"):
			_, _ = w.Write([]byte(`{
				"rosters": [{
					"team": {"displayName": "Norway", "abbreviation": "NOR"},
					"roster": [{
						"active": ` + activeValue + `,
						"starter": false,
						"athlete": {"displayName": "Erling Haaland", "fullName": "Erling Haaland"},
						"position": {"displayName": "Substitute", "abbreviation": "SUB"}
					}]
				}]
			}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	return NewESPNSportsAvailabilityProvider(ESPNSportsAvailabilityConfig{
		BaseURL:   server.URL,
		Leagues:   []string{"fifa.world"},
		Client:    server.Client(),
		Lookback:  24 * time.Hour,
		Lookahead: 24 * time.Hour,
	})
}
