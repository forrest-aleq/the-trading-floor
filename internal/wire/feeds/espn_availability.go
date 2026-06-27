package feeds

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hnic/trading-floor/internal/execution/kalshi"
)

const defaultESPNBaseURL = "https://site.web.api.espn.com/apis/site/v2/sports/soccer"

type ESPNSportsAvailabilityConfig struct {
	BaseURL   string
	Leagues   []string
	Client    *http.Client
	Lookback  time.Duration
	Lookahead time.Duration
}

type ESPNSportsAvailabilityProvider struct {
	baseURL   string
	leagues   []string
	client    *http.Client
	lookback  time.Duration
	lookahead time.Duration

	mu    sync.Mutex
	cache map[string]espnCachedEvent
}

type espnCachedEvent struct {
	summary   *espnSummaryResponse
	fetchedAt time.Time
}

type sportsMarketQuery struct {
	Player string
	Teams  []string
}

type espnScoreboardResponse struct {
	Events []espnScoreboardEvent `json:"events"`
}

type espnScoreboardEvent struct {
	ID           string                 `json:"id"`
	Name         string                 `json:"name"`
	ShortName    string                 `json:"shortName"`
	Date         string                 `json:"date"`
	Competitions []espnEventCompetition `json:"competitions"`
}

type espnEventCompetition struct {
	Competitors []espnEventCompetitor `json:"competitors"`
}

type espnEventCompetitor struct {
	Team espnTeam `json:"team"`
}

type espnTeam struct {
	DisplayName  string `json:"displayName"`
	ShortName    string `json:"shortDisplayName"`
	Abbreviation string `json:"abbreviation"`
}

type espnSummaryResponse struct {
	Rosters []espnRosterGroup `json:"rosters"`
}

type espnRosterGroup struct {
	Team   espnTeam          `json:"team"`
	Roster []espnRosterEntry `json:"roster"`
}

type espnRosterEntry struct {
	Active   *bool        `json:"active"`
	Starter  *bool        `json:"starter"`
	Athlete  espnAthlete  `json:"athlete"`
	Position espnPosition `json:"position"`
}

type espnAthlete struct {
	FullName    string `json:"fullName"`
	DisplayName string `json:"displayName"`
	ShortName   string `json:"shortName"`
}

type espnPosition struct {
	DisplayName  string `json:"displayName"`
	Abbreviation string `json:"abbreviation"`
}

func NewESPNSportsAvailabilityProviderFromEnv() *ESPNSportsAvailabilityProvider {
	if !readFeedBool("KALSHI_SPORTS_AVAILABILITY_ENABLED", true) {
		return nil
	}
	leagues := splitCSV(os.Getenv("KALSHI_SPORTS_ESPN_SOCCER_LEAGUES"))
	if len(leagues) == 0 {
		leagues = []string{"fifa.world", "uefa.euro", "uefa.champions", "eng.1", "esp.1", "ita.1", "ger.1", "fra.1", "usa.1"}
	}
	return NewESPNSportsAvailabilityProvider(ESPNSportsAvailabilityConfig{
		BaseURL:   strings.TrimSpace(os.Getenv("KALSHI_SPORTS_ESPN_BASE_URL")),
		Leagues:   leagues,
		Lookback:  readFeedDuration("KALSHI_SPORTS_ESPN_LOOKBACK", 36*time.Hour),
		Lookahead: readFeedDuration("KALSHI_SPORTS_ESPN_LOOKAHEAD", 14*24*time.Hour),
	})
}

func NewESPNSportsAvailabilityProvider(cfg ESPNSportsAvailabilityConfig) *ESPNSportsAvailabilityProvider {
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		baseURL = defaultESPNBaseURL
	}
	leagues := make([]string, 0, len(cfg.Leagues))
	for _, league := range cfg.Leagues {
		league = strings.ToLower(strings.TrimSpace(league))
		if league != "" {
			leagues = append(leagues, league)
		}
	}
	if len(leagues) == 0 {
		leagues = []string{"fifa.world"}
	}
	client := cfg.Client
	if client == nil {
		client = newFeedHTTPClient()
	}
	lookback := cfg.Lookback
	if lookback <= 0 {
		lookback = 36 * time.Hour
	}
	lookahead := cfg.Lookahead
	if lookahead <= 0 {
		lookahead = 14 * 24 * time.Hour
	}
	return &ESPNSportsAvailabilityProvider{
		baseURL:   baseURL,
		leagues:   leagues,
		client:    client,
		lookback:  lookback,
		lookahead: lookahead,
		cache:     make(map[string]espnCachedEvent),
	}
}

func (p *ESPNSportsAvailabilityProvider) CheckMarket(ctx context.Context, market kalshi.Market, now time.Time) SportsAvailabilityEvidence {
	query, ok := extractSportsMarketQuery(market)
	if !ok {
		return SportsAvailabilityEvidence{}
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	observedAt := now.UTC()
	base := SportsAvailabilityEvidence{
		Status:     "unresolved",
		Source:     "espn",
		Player:     query.Player,
		ObservedAt: &observedAt,
	}

	event, league, ok := p.findEvent(ctx, query, now)
	if !ok {
		base.Reason = "event_not_found"
		return base
	}
	base.League = league
	base.EventID = event.ID
	base.EventName = firstNonEmptyString(event.Name, event.ShortName)

	summary, err := p.fetchSummary(ctx, league, event.ID, now)
	if err != nil {
		base.Reason = "summary_unavailable"
		return base
	}
	entry, team, ok := findRosterEntry(summary, query.Player, query.Teams)
	if !ok {
		base.Reason = "player_not_found"
		return base
	}
	base.Team = team
	base.Active = entry.Active
	base.Starter = entry.Starter
	base.Position = firstNonEmptyString(entry.Position.DisplayName, entry.Position.Abbreviation)
	if entry.Active != nil && !*entry.Active {
		base.Status = "blocked"
		base.Reason = "player_inactive"
		return base
	}
	base.Status = "confirmed"
	base.Reason = "espn_roster_match"
	return base
}

func (p *ESPNSportsAvailabilityProvider) findEvent(ctx context.Context, query sportsMarketQuery, now time.Time) (espnScoreboardEvent, string, bool) {
	if p == nil {
		return espnScoreboardEvent{}, "", false
	}
	dates := espnDateRange(now.Add(-p.lookback), now.Add(p.lookahead))
	for _, league := range p.leagues {
		resp, err := p.fetchScoreboard(ctx, league, dates)
		if err != nil {
			continue
		}
		for _, event := range resp.Events {
			if eventMatchesTeams(event, query.Teams) {
				return event, league, true
			}
		}
	}
	return espnScoreboardEvent{}, "", false
}

func (p *ESPNSportsAvailabilityProvider) fetchScoreboard(ctx context.Context, league, dates string) (*espnScoreboardResponse, error) {
	endpoint := fmt.Sprintf("%s/%s/scoreboard?dates=%s", p.baseURL, url.PathEscape(league), url.QueryEscape(dates))
	var out espnScoreboardResponse
	if err := p.fetchJSON(ctx, endpoint, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (p *ESPNSportsAvailabilityProvider) fetchSummary(ctx context.Context, league, eventID string, now time.Time) (*espnSummaryResponse, error) {
	cacheKey := league + ":" + eventID
	p.mu.Lock()
	if cached, ok := p.cache[cacheKey]; ok && now.Sub(cached.fetchedAt) < 60*time.Second && cached.summary != nil {
		summary := cached.summary
		p.mu.Unlock()
		return summary, nil
	}
	p.mu.Unlock()

	endpoint := fmt.Sprintf("%s/%s/summary?event=%s", p.baseURL, url.PathEscape(league), url.QueryEscape(eventID))
	var out espnSummaryResponse
	if err := p.fetchJSON(ctx, endpoint, &out); err != nil {
		return nil, err
	}
	p.mu.Lock()
	p.cache[cacheKey] = espnCachedEvent{summary: &out, fetchedAt: now}
	p.mu.Unlock()
	return &out, nil
}

func (p *ESPNSportsAvailabilityProvider) fetchJSON(ctx context.Context, endpoint string, out any) error {
	if p == nil || p.client == nil {
		return fmt.Errorf("espn availability client unavailable")
	}
	req, err := newFeedRequest(ctx, http.MethodGet, endpoint)
	if err != nil {
		return err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("espn status %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func extractSportsMarketQuery(market kalshi.Market) (sportsMarketQuery, bool) {
	text := strings.Join([]string{market.Title, market.Subtitle}, " | ")
	if !looksSportsParticipantMarket(text) {
		return sportsMarketQuery{}, false
	}
	player := extractPlayerName(market)
	teams := extractMatchTeams(market.Title)
	if player == "" || len(teams) < 2 {
		return sportsMarketQuery{}, false
	}
	return sportsMarketQuery{Player: player, Teams: teams}, true
}

func looksSportsParticipantMarket(text string) bool {
	text = strings.ToLower(strings.Join(strings.Fields(text), " "))
	for _, term := range []string{
		"goalscorer",
		"goal scorer",
		"shots on target",
		"player assists",
	} {
		if strings.Contains(text, term) {
			return true
		}
	}
	return false
}

func extractPlayerName(market kalshi.Market) string {
	candidates := []string{market.Subtitle, market.Title}
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		for _, sep := range []string{":", " - ", " over ", " under ", " to score"} {
			if idx := strings.Index(strings.ToLower(candidate), sep); idx > 0 {
				candidate = strings.TrimSpace(candidate[:idx])
				break
			}
		}
		candidate = strings.Trim(candidate, " \t\n\r-–—|")
		candidate = stripMarketNoise(candidate)
		if words := strings.Fields(candidate); len(words) >= 2 && len(words) <= 4 {
			return strings.Join(words, " ")
		}
	}
	return ""
}

func extractMatchTeams(title string) []string {
	beforeColon := strings.TrimSpace(title)
	if idx := strings.Index(beforeColon, ":"); idx >= 0 {
		beforeColon = beforeColon[:idx]
	}
	replacers := []string{" vs. ", " vs ", " v. ", " v ", " at "}
	lower := strings.ToLower(beforeColon)
	for _, sep := range replacers {
		if idx := strings.Index(lower, sep); idx > 0 {
			left := strings.TrimSpace(beforeColon[:idx])
			right := strings.TrimSpace(beforeColon[idx+len(sep):])
			if left != "" && right != "" {
				return []string{left, right}
			}
		}
	}
	return nil
}

func stripMarketNoise(value string) string {
	replacements := []string{
		"Over", "Under", "Yes", "No", "Anytime", "First", "Goalscorer", "Goal Scorer",
		"Player", "Points", "Assists", "Rebounds", "Shots", "On Target",
	}
	fields := strings.Fields(value)
	cleaned := make([]string, 0, len(fields))
	for _, field := range fields {
		trimmed := strings.Trim(field, ".,:;()[]{}+")
		skip := false
		for _, replacement := range replacements {
			if strings.EqualFold(trimmed, replacement) {
				skip = true
				break
			}
		}
		if !skip && trimmed != "" && !strings.ContainsAny(trimmed, "0123456789") {
			cleaned = append(cleaned, trimmed)
		}
	}
	return strings.Join(cleaned, " ")
}

func eventMatchesTeams(event espnScoreboardEvent, teams []string) bool {
	if len(teams) < 2 {
		return false
	}
	eventTeams := eventTeamNames(event)
	matches := 0
	for _, team := range teams {
		for _, eventTeam := range eventTeams {
			if namesMatch(team, eventTeam) {
				matches++
				break
			}
		}
	}
	return matches >= 2
}

func eventTeamNames(event espnScoreboardEvent) []string {
	seen := map[string]struct{}{}
	var names []string
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		key := normalizeName(value)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		names = append(names, value)
	}
	add(event.Name)
	add(event.ShortName)
	for _, competition := range event.Competitions {
		for _, competitor := range competition.Competitors {
			add(competitor.Team.DisplayName)
			add(competitor.Team.ShortName)
			add(competitor.Team.Abbreviation)
		}
	}
	return names
}

func findRosterEntry(summary *espnSummaryResponse, player string, teams []string) (espnRosterEntry, string, bool) {
	if summary == nil {
		return espnRosterEntry{}, "", false
	}
	for _, group := range summary.Rosters {
		teamName := firstNonEmptyString(group.Team.DisplayName, group.Team.ShortName, group.Team.Abbreviation)
		if len(teams) > 0 && !teamInQuery(teamName, teams) {
			continue
		}
		for _, entry := range group.Roster {
			if namesMatch(player, firstNonEmptyString(entry.Athlete.DisplayName, entry.Athlete.FullName, entry.Athlete.ShortName)) {
				return entry, teamName, true
			}
		}
	}
	return espnRosterEntry{}, "", false
}

func teamInQuery(team string, teams []string) bool {
	for _, candidate := range teams {
		if namesMatch(team, candidate) {
			return true
		}
	}
	return false
}

func espnDateRange(start, end time.Time) string {
	start = start.UTC()
	end = end.UTC()
	if start.After(end) {
		start, end = end, start
	}
	dates := []string{}
	for day := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, time.UTC); !day.After(end); day = day.AddDate(0, 0, 1) {
		dates = append(dates, day.Format("20060102"))
	}
	if len(dates) == 0 {
		return time.Now().UTC().Format("20060102")
	}
	sort.Strings(dates)
	return strings.Join(dates, "-")
}

func namesMatch(left, right string) bool {
	left = normalizeName(left)
	right = normalizeName(right)
	if left == "" || right == "" {
		return false
	}
	return left == right || strings.Contains(left, right) || strings.Contains(right, left)
}

func normalizeName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	replacer := strings.NewReplacer(
		".", " ",
		",", " ",
		"'", "",
		"’", "",
		"-", " ",
		"_", " ",
		"@", " ",
		" vs ", " ",
		" at ", " ",
	)
	value = replacer.Replace(value)
	return strings.Join(strings.Fields(value), " ")
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func readFeedBool(name string, fallback bool) bool {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(raw)
	if err != nil {
		return fallback
	}
	return parsed
}
