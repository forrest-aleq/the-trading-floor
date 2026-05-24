package marketdata

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hnic/trading-floor/pkg/model"
)

const (
	defaultPolygonBaseURL      = "https://api.polygon.io"
	defaultPolygonTimeout      = 15 * time.Second
	polygonStocksSnapshotPath  = "/v2/snapshot/locale/us/markets/stocks/tickers"
	polygonIndicesSnapshotPath = "/v3/snapshot/indices"
)

type MassivePlan string

const (
	MassivePlanUnknown   MassivePlan = ""
	MassivePlanBasicFree MassivePlan = "basic_free"
	MassivePlanStarter   MassivePlan = "starter"
	MassivePlanDeveloper MassivePlan = "developer"
	MassivePlanAdvanced  MassivePlan = "advanced"
)

type PolygonProvider struct {
	client  *http.Client
	baseURL string
	apiKey  string
}

func NewPolygonProvider(apiKey string) (*PolygonProvider, error) {
	resolved := resolvePolygonAPIKey(apiKey)
	if resolved == "" {
		return nil, fmt.Errorf("polygon market data provider requires POLYGON_API_KEY or MASSIVE_API_KEY")
	}
	return &PolygonProvider{
		client:  newPolygonHTTPClient(),
		baseURL: resolvePolygonBaseURL(),
		apiKey:  resolved,
	}, nil
}

func (p *PolygonProvider) Snapshot(ctx context.Context, inst model.Instrument) (*Snapshot, error) {
	if p == nil {
		return nil, fmt.Errorf("nil polygon provider")
	}

	switch strings.ToUpper(strings.TrimSpace(inst.SecType)) {
	case "IND":
		return p.indexSnapshot(ctx, inst)
	default:
		return p.stockSnapshot(ctx, inst)
	}
}

func (p *PolygonProvider) HistoricalBars(ctx context.Context, inst model.Instrument, end time.Time, duration, barSize, whatToShow string, useRTH bool) ([]HistoricalBar, error) {
	if p == nil {
		return nil, fmt.Errorf("nil polygon provider")
	}

	symbol := polygonAggsTicker(inst)
	if symbol == "" {
		return nil, fmt.Errorf("empty symbol")
	}
	if end.IsZero() {
		end = time.Now().UTC()
	}

	multiplier, timespan := polygonAggsGranularity(barSize)
	from := polygonAggsStart(end, duration).Format("2006-01-02")
	to := end.Format("2006-01-02")
	endpoint, err := url.Parse(strings.TrimRight(p.baseURL, "/"))
	if err != nil {
		return nil, err
	}
	endpoint.Path = path.Join(endpoint.Path, "/v2/aggs/ticker", symbol, "range", multiplier, timespan, from, to)
	query := endpoint.Query()
	query.Set("adjusted", "true")
	query.Set("sort", "asc")
	query.Set("limit", "5000")
	query.Set("apiKey", p.apiKey)
	endpoint.RawQuery = query.Encode()

	body, err := p.doJSONRequest(ctx, endpoint.String())
	if err != nil {
		return nil, err
	}
	return parsePolygonHistoricalBars(body)
}

func (p *PolygonProvider) stockSnapshot(ctx context.Context, inst model.Instrument) (*Snapshot, error) {
	symbol := polygonStockTicker(inst)
	if symbol == "" {
		return nil, fmt.Errorf("empty symbol")
	}

	endpoint, err := url.Parse(strings.TrimRight(p.baseURL, "/"))
	if err != nil {
		return nil, err
	}
	endpoint.Path = path.Join(endpoint.Path, polygonStocksSnapshotPath, symbol)
	query := endpoint.Query()
	query.Set("apiKey", p.apiKey)
	endpoint.RawQuery = query.Encode()

	body, err := p.doJSONRequest(ctx, endpoint.String())
	if err != nil {
		return nil, err
	}

	var payload polygonStockSnapshotResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	ticker := payload.Ticker
	if ticker.Ticker == "" && ticker.LastTrade.Price == 0 && ticker.LastQuote.Bid == 0 && ticker.LastQuote.Ask == 0 {
		return nil, fmt.Errorf("unexpected polygon stock snapshot response shape")
	}

	observedAt := unixMillisUTC(payload.Ticker.Updated)
	if observedAt.IsZero() {
		observedAt = unixNanosUTC(ticker.LastTrade.Timestamp)
	}
	if observedAt.IsZero() {
		observedAt = unixNanosUTC(ticker.LastQuote.Timestamp)
	}
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}

	last := ticker.LastTrade.Price
	if last <= 0 {
		last = ticker.Min.Close
	}
	if last <= 0 {
		last = ticker.Day.Close
	}

	volume := int64(ticker.Min.Volume)
	if volume <= 0 {
		volume = int64(ticker.Day.Volume)
	}

	return &Snapshot{
		Symbol:     firstNonEmpty(ticker.Ticker, inst.Symbol),
		Last:       last,
		Bid:        ticker.LastQuote.Bid,
		Ask:        ticker.LastQuote.Ask,
		Volume:     volume,
		ObservedAt: observedAt,
	}, nil
}

func (p *PolygonProvider) indexSnapshot(ctx context.Context, inst model.Instrument) (*Snapshot, error) {
	symbol := polygonIndexTicker(inst)
	if symbol == "" {
		return nil, fmt.Errorf("empty symbol")
	}

	endpoint, err := url.Parse(strings.TrimRight(p.baseURL, "/") + polygonIndicesSnapshotPath)
	if err != nil {
		return nil, err
	}
	query := endpoint.Query()
	query.Set("ticker", symbol)
	query.Set("limit", "1")
	query.Set("apiKey", p.apiKey)
	endpoint.RawQuery = query.Encode()

	body, err := p.doJSONRequest(ctx, endpoint.String())
	if err != nil {
		return nil, err
	}

	var payload polygonIndexSnapshotResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	if len(payload.Results) == 0 {
		return nil, fmt.Errorf("no index snapshot results for %s", symbol)
	}
	row := payload.Results[0]
	if row.Value <= 0 && row.Session.Close <= 0 {
		return nil, fmt.Errorf("unexpected polygon index snapshot response shape")
	}

	last := row.Value
	if last <= 0 {
		last = row.Session.Close
	}
	observedAt := unixNanosUTC(row.LastUpdated)
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}

	return &Snapshot{
		Symbol:     strings.TrimPrefix(firstNonEmpty(row.Ticker, symbol), "I:"),
		Last:       last,
		Volume:     int64(row.Session.Volume),
		ObservedAt: observedAt,
	}, nil
}

func (p *PolygonProvider) doJSONRequest(ctx context.Context, endpoint string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "TradingFloor/1.0")
	req.Header.Set("Accept", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return nil, fmt.Errorf("polygon status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return io.ReadAll(io.LimitReader(resp.Body, 2<<20))
}

type polygonStockSnapshotResponse struct {
	Ticker polygonStockSnapshot `json:"ticker"`
}

type polygonStockSnapshot struct {
	Ticker    string               `json:"ticker"`
	Updated   int64                `json:"updated"`
	LastTrade polygonTradeSnapshot `json:"lastTrade"`
	LastQuote polygonQuoteSnapshot `json:"lastQuote"`
	Min       polygonAggSnapshot   `json:"min"`
	Day       polygonAggSnapshot   `json:"day"`
	PrevDay   polygonAggSnapshot   `json:"prevDay"`
}

type polygonTradeSnapshot struct {
	Price     float64 `json:"p"`
	Timestamp int64   `json:"t"`
}

type polygonQuoteSnapshot struct {
	Bid       float64 `json:"p"`
	BidSize   float64 `json:"s"`
	Ask       float64 `json:"P"`
	AskSize   float64 `json:"S"`
	Timestamp int64   `json:"t"`
}

type polygonAggSnapshot struct {
	Close  float64 `json:"c"`
	Volume float64 `json:"v"`
}

type polygonIndexSnapshotResponse struct {
	Results []polygonIndexSnapshot `json:"results"`
}

type polygonIndexSnapshot struct {
	Ticker      string              `json:"ticker"`
	Value       float64             `json:"value"`
	LastUpdated int64               `json:"last_updated"`
	Session     polygonIndexSession `json:"session"`
}

type polygonIndexSession struct {
	Close  float64 `json:"close"`
	Volume float64 `json:"volume"`
}

type polygonAggsResponse struct {
	Results []polygonAggBar `json:"results"`
}

type polygonAggBar struct {
	Close  float64 `json:"c"`
	High   float64 `json:"h"`
	Low    float64 `json:"l"`
	Open   float64 `json:"o"`
	Time   int64   `json:"t"`
	Volume float64 `json:"v"`
}

func parsePolygonHistoricalBars(body []byte) ([]HistoricalBar, error) {
	var payload polygonAggsResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	if len(payload.Results) == 0 {
		return nil, fmt.Errorf("unexpected polygon historical response shape")
	}

	out := make([]HistoricalBar, 0, len(payload.Results))
	for _, row := range payload.Results {
		barTime := unixMillisUTC(row.Time)
		if barTime.IsZero() {
			continue
		}
		out = append(out, HistoricalBar{
			Time:   barTime,
			Open:   row.Open,
			High:   row.High,
			Low:    row.Low,
			Close:  row.Close,
			Volume: int64(row.Volume),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Time.Before(out[j].Time)
	})
	if len(out) == 0 {
		return nil, fmt.Errorf("unexpected polygon historical response shape")
	}
	return out, nil
}

func resolvePolygonAPIKey(explicit string) string {
	if key := strings.TrimSpace(explicit); key != "" {
		return key
	}
	if key := strings.TrimSpace(os.Getenv("POLYGON_API_KEY")); key != "" {
		return key
	}
	return strings.TrimSpace(os.Getenv("MASSIVE_API_KEY"))
}

func resolvePolygonBaseURL() string {
	if value := strings.TrimSpace(os.Getenv("POLYGON_API_BASE_URL")); value != "" {
		return strings.TrimRight(value, "/")
	}
	if value := strings.TrimSpace(os.Getenv("MASSIVE_API_BASE_URL")); value != "" {
		return strings.TrimRight(value, "/")
	}
	return defaultPolygonBaseURL
}

func ResolveDefaultMarketDataProvider() string {
	if resolvePolygonAPIKey("") != "" {
		return "massive"
	}
	if resolveFMPAPIKey("") != "" {
		return "fmp"
	}
	return ""
}

func ResolveMassivePlan() MassivePlan {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv("MASSIVE_PLAN")))
	if raw == "" {
		raw = strings.ToLower(strings.TrimSpace(os.Getenv("POLYGON_PLAN")))
	}
	switch MassivePlan(raw) {
	case MassivePlanBasicFree, MassivePlanStarter, MassivePlanDeveloper, MassivePlanAdvanced:
		return MassivePlan(raw)
	default:
		return MassivePlanUnknown
	}
}

func polygonStockTicker(inst model.Instrument) string {
	return strings.ToUpper(strings.TrimSpace(inst.Symbol))
}

func polygonIndexTicker(inst model.Instrument) string {
	symbol := strings.ToUpper(strings.TrimSpace(inst.Symbol))
	if symbol == "" {
		return ""
	}
	if strings.HasPrefix(symbol, "I:") {
		return symbol
	}
	return "I:" + symbol
}

func polygonAggsTicker(inst model.Instrument) string {
	if strings.ToUpper(strings.TrimSpace(inst.SecType)) == "IND" {
		return polygonIndexTicker(inst)
	}
	return polygonStockTicker(inst)
}

func polygonAggsGranularity(barSize string) (string, string) {
	fields := strings.Fields(strings.ToLower(strings.TrimSpace(barSize)))
	if len(fields) < 2 {
		return "1", "day"
	}
	n, err := strconv.Atoi(fields[0])
	if err != nil || n <= 0 {
		n = 1
	}
	unit := strings.TrimSuffix(fields[1], "s")
	switch unit {
	case "sec", "second":
		return strconv.Itoa(n), "second"
	case "min", "minute":
		return strconv.Itoa(n), "minute"
	case "hour":
		return strconv.Itoa(n), "hour"
	case "day":
		return strconv.Itoa(n), "day"
	case "week":
		return strconv.Itoa(n), "week"
	case "month":
		return strconv.Itoa(n), "month"
	default:
		return "1", "day"
	}
}

func polygonAggsStart(end time.Time, duration string) time.Time {
	if end.IsZero() {
		end = time.Now().UTC()
	}
	fields := strings.Fields(strings.ToUpper(strings.TrimSpace(duration)))
	if len(fields) < 2 {
		return end.AddDate(0, 0, -14)
	}
	n, err := strconv.Atoi(fields[0])
	if err != nil || n <= 0 {
		n = 14
	}
	switch strings.TrimSuffix(fields[1], "S") {
	case "D":
		return end.AddDate(0, 0, -n)
	case "W":
		return end.AddDate(0, 0, -7*n)
	case "M":
		return end.AddDate(0, -n, 0)
	case "Y":
		return end.AddDate(-n, 0, 0)
	default:
		return end.AddDate(0, 0, -14)
	}
}

func unixMillisUTC(value int64) time.Time {
	if value <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(value).UTC()
}

func unixNanosUTC(value int64) time.Time {
	if value <= 0 {
		return time.Time{}
	}
	return time.Unix(0, value).UTC()
}

func newPolygonHTTPClient() *http.Client {
	return &http.Client{
		Timeout: defaultPolygonTimeout,
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          32,
			MaxConnsPerHost:       16,
			MaxIdleConnsPerHost:   8,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 10 * time.Second,
			ExpectContinueTimeout: time.Second,
		},
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
