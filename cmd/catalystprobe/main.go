package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/joho/godotenv"

	"github.com/hnic/trading-floor/internal/catalyst"
	"github.com/hnic/trading-floor/internal/execution/kalshi"
)

const defaultHTTPTimeout = 10 * time.Second

type sourcePoller interface {
	Poll(ctx context.Context) catalyst.SourceSnapshot
}

type probeConfig struct {
	CatalystID string
	Tickers    []string
	Interval   time.Duration
	Duration   time.Duration
	Once       bool
	OutPath    string
}

type commonOptions struct {
	tickers    *string
	catalystID *string
	interval   *time.Duration
	duration   *time.Duration
	once       *bool
	out        *string
}

func main() {
	_ = godotenv.Load()
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	var err error
	switch os.Args[1] {
	case "generic-json":
		err = runGenericJSON(os.Args[2:])
	case "espn-lineup":
		err = runESPNLineup(os.Args[2:])
	default:
		usage()
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "catalystprobe: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Println("usage: catalystprobe <generic-json|espn-lineup> [flags]")
	fmt.Println()
	fmt.Println("generic-json flags:")
	fmt.Println("  --source-url URL --tickers KX1,KX2 [--source-key path.to.field] [--interval 1s] [--duration 10m] [--out file.jsonl]")
	fmt.Println()
	fmt.Println("espn-lineup flags:")
	fmt.Println("  --league fifa.world --event-id 760475 --players \"Jonathan David,Rayners\" --tickers KX1,KX2 [--interval 1s] [--duration 2h] [--out file.jsonl]")
}

func runGenericJSON(args []string) error {
	fs := flag.NewFlagSet("generic-json", flag.ExitOnError)
	sourceURL := fs.String("source-url", "", "official source JSON URL")
	sourceKey := fs.String("source-key", "", "optional dot path to fingerprint within JSON")
	sourceID := fs.String("source-id", "", "optional source id")
	common := commonFlags(fs, "generic-json")
	_ = fs.Parse(args)
	cfg := common.config()
	if strings.TrimSpace(*sourceURL) == "" {
		return fmt.Errorf("--source-url is required")
	}
	if cfg.CatalystID == "" {
		cfg.CatalystID = firstNonEmpty(*sourceID, *sourceURL)
	}
	poller := &genericJSONPoller{
		client:    &http.Client{Timeout: defaultHTTPTimeout},
		sourceURL: strings.TrimSpace(*sourceURL),
		sourceID:  strings.TrimSpace(*sourceID),
		sourceKey: strings.TrimSpace(*sourceKey),
	}
	return runProbe(cfg, poller)
}

func runESPNLineup(args []string) error {
	fs := flag.NewFlagSet("espn-lineup", flag.ExitOnError)
	baseURL := fs.String("espn-base-url", "https://site.web.api.espn.com/apis/site/v2/sports/soccer", "ESPN soccer API base URL")
	league := fs.String("league", "", "ESPN league, e.g. fifa.world")
	eventID := fs.String("event-id", "", "ESPN event id")
	players := fs.String("players", "", "comma-separated watched player names")
	common := commonFlags(fs, "espn-lineup")
	_ = fs.Parse(args)
	cfg := common.config()
	if strings.TrimSpace(*league) == "" {
		return fmt.Errorf("--league is required")
	}
	if strings.TrimSpace(*eventID) == "" {
		return fmt.Errorf("--event-id is required")
	}
	if cfg.CatalystID == "" {
		cfg.CatalystID = "espn-lineup:" + strings.TrimSpace(*league) + ":" + strings.TrimSpace(*eventID)
	}
	poller := &espnLineupPoller{
		client:  &http.Client{Timeout: defaultHTTPTimeout},
		baseURL: strings.TrimRight(strings.TrimSpace(*baseURL), "/"),
		league:  strings.TrimSpace(*league),
		eventID: strings.TrimSpace(*eventID),
		players: splitCSV(*players),
	}
	return runProbe(cfg, poller)
}

func commonFlags(fs *flag.FlagSet, defaultCatalyst string) commonOptions {
	tickers := fs.String("tickers", "", "comma-separated Kalshi market tickers")
	catalystID := fs.String("catalyst-id", defaultCatalyst, "stable catalyst id")
	interval := fs.Duration("interval", time.Second, "poll interval")
	duration := fs.Duration("duration", 10*time.Minute, "max run duration")
	once := fs.Bool("once", false, "take one sample and exit")
	out := fs.String("out", "", "JSONL output path, default stdout")
	fs.SetOutput(os.Stdout)
	return commonOptions{
		tickers:    tickers,
		catalystID: catalystID,
		interval:   interval,
		duration:   duration,
		once:       once,
		out:        out,
	}
}

func (o commonOptions) config() probeConfig {
	return probeConfig{
		CatalystID: strings.TrimSpace(*o.catalystID),
		Tickers:    splitCSV(*o.tickers),
		Interval:   *o.interval,
		Duration:   *o.duration,
		Once:       *o.once,
		OutPath:    strings.TrimSpace(*o.out),
	}
}

func runProbe(cfg probeConfig, poller sourcePoller) error {
	if poller == nil {
		return fmt.Errorf("source poller required")
	}
	if len(cfg.Tickers) == 0 {
		return fmt.Errorf("--tickers is required")
	}
	if cfg.Interval <= 0 {
		cfg.Interval = time.Second
	}
	if cfg.Duration <= 0 {
		cfg.Duration = 10 * time.Minute
	}

	client, err := kalshi.NewClient(kalshi.DefaultConfig())
	if err != nil {
		return fmt.Errorf("kalshi config: %w", err)
	}
	var out io.WriteCloser
	if cfg.OutPath == "" || cfg.OutPath == "-" {
		out = nopWriteCloser{Writer: os.Stdout}
	} else {
		out, err = os.OpenFile(cfg.OutPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return fmt.Errorf("open output: %w", err)
		}
	}
	defer func() {
		_ = out.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), cfg.Duration)
	defer cancel()

	detector := catalyst.NewDetector(uuid.NewString(), cfg.CatalystID)
	ticker := time.NewTicker(cfg.Interval)
	defer ticker.Stop()

	for {
		if err := pollOnce(ctx, out, detector, poller, client, cfg.Tickers); err != nil {
			return err
		}
		if cfg.Once {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func pollOnce(ctx context.Context, out io.Writer, detector *catalyst.Detector, poller sourcePoller, client *kalshi.Client, tickers []string) error {
	observedAt := time.Now().UTC()
	source := poller.Poll(ctx)
	markets := pollKalshiMarkets(ctx, client, tickers)
	obs := detector.Observe(observedAt, source, markets)
	raw, err := json.Marshal(obs)
	if err != nil {
		return fmt.Errorf("marshal observation: %w", err)
	}
	if _, err := out.Write(append(raw, '\n')); err != nil {
		return fmt.Errorf("write observation: %w", err)
	}
	return nil
}

func pollKalshiMarkets(ctx context.Context, client *kalshi.Client, tickers []string) []catalyst.MarketSnapshot {
	snapshots := make([]catalyst.MarketSnapshot, 0, len(tickers))
	for _, ticker := range tickers {
		ticker = strings.ToUpper(strings.TrimSpace(ticker))
		if ticker == "" {
			continue
		}
		snapshot := catalyst.MarketSnapshot{Ticker: ticker}
		resp, err := client.GetMarket(ctx, ticker)
		if err != nil {
			snapshot.Error = err.Error()
			snapshot.Fingerprint = catalyst.FingerprintJSON(map[string]any{"ticker": ticker, "error": snapshot.Error})
			snapshots = append(snapshots, snapshot)
			continue
		}
		market := resp.Market
		snapshot.Title = market.Title
		snapshot.Subtitle = market.Subtitle
		snapshot.Status = market.Status
		snapshot.YesBidDollars = market.YesBidDollars
		snapshot.YesAskDollars = market.YesAskDollars
		snapshot.NoBidDollars = market.NoBidDollars
		snapshot.NoAskDollars = market.NoAskDollars
		snapshot.LastPriceDollars = market.LastPriceDollars
		snapshot.Fingerprint = catalyst.FingerprintJSON(map[string]any{
			"ticker":  snapshot.Ticker,
			"status":  snapshot.Status,
			"yes_bid": snapshot.YesBidDollars,
			"yes_ask": snapshot.YesAskDollars,
			"no_bid":  snapshot.NoBidDollars,
			"no_ask":  snapshot.NoAskDollars,
			"last":    snapshot.LastPriceDollars,
		})
		snapshots = append(snapshots, snapshot)
	}
	return snapshots
}

type genericJSONPoller struct {
	client    *http.Client
	sourceURL string
	sourceID  string
	sourceKey string
}

func (p *genericJSONPoller) Poll(ctx context.Context) catalyst.SourceSnapshot {
	snapshot := catalyst.SourceSnapshot{
		Kind: "generic_json",
		ID:   firstNonEmpty(p.sourceID, p.sourceURL),
	}
	value, err := fetchJSON(ctx, p.client, p.sourceURL)
	if err != nil {
		snapshot.Error = err.Error()
		snapshot.Fingerprint = catalyst.FingerprintJSON(map[string]any{"error": snapshot.Error})
		return snapshot
	}
	selected := value
	if p.sourceKey != "" {
		if extracted, ok := selectDotPath(value, p.sourceKey); ok {
			selected = extracted
		} else {
			snapshot.Error = "source_key_not_found"
		}
	}
	snapshot.Fingerprint = catalyst.FingerprintJSON(selected)
	snapshot.State = summarizeJSONState(selected)
	return snapshot
}

type espnLineupPoller struct {
	client  *http.Client
	baseURL string
	league  string
	eventID string
	players []string
}

type espnSummary struct {
	Header struct {
		Name      string `json:"name"`
		ShortName string `json:"shortName"`
	} `json:"header"`
	Rosters []struct {
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
	} `json:"rosters"`
	Injuries []any `json:"injuries"`
}

func (p *espnLineupPoller) Poll(ctx context.Context) catalyst.SourceSnapshot {
	sourceID := strings.TrimSpace(p.league) + ":" + strings.TrimSpace(p.eventID)
	snapshot := catalyst.SourceSnapshot{Kind: "espn_lineup", ID: sourceID}
	endpoint := strings.TrimRight(p.baseURL, "/") + "/" + p.league + "/summary?event=" + p.eventID
	raw, err := fetchJSON(ctx, p.client, endpoint)
	if err != nil {
		snapshot.Error = err.Error()
		snapshot.Fingerprint = catalyst.FingerprintJSON(map[string]any{"error": snapshot.Error})
		return snapshot
	}
	encoded, _ := json.Marshal(raw)
	var summary espnSummary
	if err := json.Unmarshal(encoded, &summary); err != nil {
		snapshot.Error = err.Error()
		snapshot.Fingerprint = catalyst.FingerprintJSON(map[string]any{"error": snapshot.Error})
		return snapshot
	}
	state := summarizeESPNLineup(summary, p.players)
	snapshot.State = state
	snapshot.Fingerprint = catalyst.FingerprintJSON(state)
	return snapshot
}

func summarizeESPNLineup(summary espnSummary, players []string) map[string]any {
	teams := make([]string, 0, len(summary.Rosters))
	totalRoster := 0
	activeKnown := 0
	activeTrue := 0
	starterKnown := 0
	starterTrue := 0
	watched := map[string]any{}
	playerSet := normalizedSet(players)

	for _, group := range summary.Rosters {
		team := firstNonEmpty(group.Team.DisplayName, group.Team.ShortName, group.Team.Abbreviation)
		if team != "" {
			teams = append(teams, team)
		}
		for _, entry := range group.Roster {
			totalRoster++
			if entry.Active != nil {
				activeKnown++
				if *entry.Active {
					activeTrue++
				}
			}
			if entry.Starter != nil {
				starterKnown++
				if *entry.Starter {
					starterTrue++
				}
			}
			name := firstNonEmpty(entry.Athlete.DisplayName, entry.Athlete.FullName, entry.Athlete.ShortName)
			key := normalizeName(name)
			if _, ok := playerSet[key]; ok {
				watched[name] = map[string]any{
					"team":     team,
					"active":   entry.Active,
					"starter":  entry.Starter,
					"position": firstNonEmpty(entry.Position.DisplayName, entry.Position.Abbreviation),
				}
			}
		}
	}
	sort.Strings(teams)
	return map[string]any{
		"event_name":    firstNonEmpty(summary.Header.Name, summary.Header.ShortName),
		"teams":         teams,
		"roster_count":  totalRoster,
		"active_known":  activeKnown,
		"active_true":   activeTrue,
		"starter_known": starterKnown,
		"starter_true":  starterTrue,
		"injury_count":  len(summary.Injuries),
		"watched":       watched,
	}
}

func fetchJSON(ctx context.Context, client *http.Client, endpoint string) (any, error) {
	if client == nil {
		client = &http.Client{Timeout: defaultHTTPTimeout}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "hnic-trading-floor-catalystprobe/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("http status %d", resp.StatusCode)
	}
	var out any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

func selectDotPath(value any, path string) (any, bool) {
	current := value
	for _, part := range strings.Split(path, ".") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		switch node := current.(type) {
		case map[string]any:
			next, ok := node[part]
			if !ok {
				return nil, false
			}
			current = next
		case []any:
			idx, err := strconv.Atoi(part)
			if err != nil || idx < 0 || idx >= len(node) {
				return nil, false
			}
			current = node[idx]
		default:
			return nil, false
		}
	}
	return current, true
}

func summarizeJSONState(value any) map[string]any {
	switch node := value.(type) {
	case map[string]any:
		return map[string]any{
			"type": "object",
			"keys": sortedKeys(node),
		}
	case []any:
		return map[string]any{
			"type": "array",
			"len":  len(node),
		}
	default:
		return map[string]any{
			"type":  fmt.Sprintf("%T", value),
			"value": value,
		}
	}
}

func sortedKeys(value map[string]any) []string {
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) > 20 {
		return keys[:20]
	}
	return keys
}

func splitCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func normalizedSet(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		if key := normalizeName(value); key != "" {
			out[key] = struct{}{}
		}
	}
	return out
}

func normalizeName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

type nopWriteCloser struct {
	io.Writer
}

func (n nopWriteCloser) Close() error { return nil }
