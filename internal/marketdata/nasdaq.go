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
	"strconv"
	"strings"
	"time"

	"github.com/hnic/trading-floor/pkg/model"
)

const (
	defaultNasdaqBaseURL = "https://api.nasdaq.com"
	defaultNasdaqTimeout = 10 * time.Second
)

var nasdaqETFUniverse = map[string]struct{}{
	"DIA": {}, "EWY": {}, "GDX": {}, "GLD": {}, "IWM": {}, "QQQ": {}, "SMH": {}, "SPY": {}, "TLT": {}, "XLE": {}, "XLK": {},
}

// NasdaqProvider is a no-key quote fallback for US stocks and ETFs. It is used
// behind paid providers so throttling does not force zero-notional market
// orders in paper execution.
type NasdaqProvider struct {
	client  *http.Client
	baseURL string
}

func NewNasdaqProvider() *NasdaqProvider {
	return &NasdaqProvider{
		client:  newNasdaqHTTPClient(),
		baseURL: resolveNasdaqBaseURL(),
	}
}

func (p *NasdaqProvider) Snapshot(ctx context.Context, inst model.Instrument) (*Snapshot, error) {
	if p == nil {
		return nil, fmt.Errorf("nil Nasdaq provider")
	}
	symbol := nasdaqSymbol(inst)
	if symbol == "" {
		return nil, fmt.Errorf("empty Nasdaq symbol")
	}

	endpoint, err := url.Parse(strings.TrimRight(p.baseURL, "/") + "/api/quote/" + url.PathEscape(symbol) + "/info")
	if err != nil {
		return nil, err
	}
	query := endpoint.Query()
	query.Set("assetclass", nasdaqAssetClass(inst))
	endpoint.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; TradingFloor/1.0)")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Origin", "https://www.nasdaq.com")
	req.Header.Set("Referer", "https://www.nasdaq.com/")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("nasdaq status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	return parseNasdaqSnapshot(body, inst)
}

func (p *NasdaqProvider) Snapshots(ctx context.Context, instruments []model.Instrument) (map[string]*Snapshot, error) {
	out := make(map[string]*Snapshot, len(instruments))
	var lastErr error
	for _, inst := range instruments {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		snapshot, err := p.Snapshot(ctx, inst)
		if err != nil {
			lastErr = err
			continue
		}
		if usableSnapshot(snapshot) {
			out[inst.Key()] = snapshot
		}
	}
	if len(out) == 0 && lastErr != nil {
		return out, lastErr
	}
	return out, nil
}

func (p *NasdaqProvider) HistoricalBars(ctx context.Context, inst model.Instrument, _ time.Time, _, _, _ string, _ bool) ([]HistoricalBar, error) {
	snapshot, err := p.Snapshot(ctx, inst)
	if err != nil {
		return nil, err
	}
	price := bestPrice(snapshot)
	if price <= 0 {
		return nil, fmt.Errorf("nasdaq historical fallback unavailable for %s", inst.Label())
	}
	observedAt := snapshot.ObservedAt
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}
	return []HistoricalBar{{
		Time:   observedAt,
		Close:  price,
		Volume: snapshot.Volume,
	}}, nil
}

type nasdaqInfoResponse struct {
	Data *struct {
		Symbol    string             `json:"symbol"`
		Primary   nasdaqQuotePayload `json:"primaryData"`
		Secondary nasdaqQuotePayload `json:"secondaryData"`
	} `json:"data"`
	Status struct {
		RCode int `json:"rCode"`
	} `json:"status"`
}

type nasdaqQuotePayload struct {
	LastSalePrice      string `json:"lastSalePrice"`
	BidPrice           string `json:"bidPrice"`
	AskPrice           string `json:"askPrice"`
	Volume             string `json:"volume"`
	LastTradeTimeStamp string `json:"lastTradeTimestamp"`
}

func parseNasdaqSnapshot(body []byte, inst model.Instrument) (*Snapshot, error) {
	var payload nasdaqInfoResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("parse nasdaq response: %w", err)
	}
	if payload.Data == nil {
		return nil, fmt.Errorf("nasdaq response missing data for %s", inst.Label())
	}
	quote := payload.Data.Primary
	last := parseNasdaqNumber(quote.LastSalePrice)
	bid := parseNasdaqNumber(quote.BidPrice)
	ask := parseNasdaqNumber(quote.AskPrice)
	if last <= 0 && bid <= 0 && ask <= 0 {
		quote = payload.Data.Secondary
		last = parseNasdaqNumber(quote.LastSalePrice)
		bid = parseNasdaqNumber(quote.BidPrice)
		ask = parseNasdaqNumber(quote.AskPrice)
	}
	if last <= 0 && bid <= 0 && ask <= 0 {
		return nil, fmt.Errorf("nasdaq response had no usable quote for %s", inst.Label())
	}

	observedAt := parseNasdaqTimestamp(quote.LastTradeTimeStamp)
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}
	symbol := strings.TrimSpace(payload.Data.Symbol)
	if symbol == "" {
		symbol = inst.Symbol
	}
	return &Snapshot{
		Symbol:     symbol,
		Last:       last,
		Bid:        bid,
		Ask:        ask,
		Volume:     int64(parseNasdaqNumber(quote.Volume)),
		ObservedAt: observedAt,
	}, nil
}

func parseNasdaqNumber(raw string) float64 {
	cleaned := strings.TrimSpace(raw)
	cleaned = strings.TrimPrefix(cleaned, "$")
	cleaned = strings.TrimSuffix(cleaned, "%")
	cleaned = strings.ReplaceAll(cleaned, ",", "")
	if cleaned == "" || cleaned == "N/A" {
		return 0
	}
	value, err := strconv.ParseFloat(cleaned, 64)
	if err != nil {
		return 0
	}
	return value
}

func parseNasdaqTimestamp(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	raw = strings.TrimPrefix(raw, "Closed at ")
	raw = strings.TrimSuffix(raw, " ET")
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		loc = time.FixedZone("ET", -5*3600)
	}
	for _, layout := range []string{"Jan 2, 2006 3:04 PM", "Jan 2, 2006"} {
		if parsed, err := time.ParseInLocation(layout, raw, loc); err == nil {
			return parsed.UTC()
		}
	}
	return time.Time{}
}

func nasdaqSymbol(inst model.Instrument) string {
	symbol := strings.ToUpper(strings.TrimSpace(inst.Symbol))
	if symbol == "" || strings.Contains(symbol, ".") || strings.HasPrefix(symbol, "^") {
		return ""
	}
	return symbol
}

func nasdaqAssetClass(inst model.Instrument) string {
	symbol := strings.ToUpper(strings.TrimSpace(inst.Symbol))
	secType := strings.ToUpper(strings.TrimSpace(inst.SecType))
	if secType == "ETF" {
		return "etf"
	}
	if _, ok := nasdaqETFUniverse[symbol]; ok {
		return "etf"
	}
	return "stocks"
}

func resolveNasdaqBaseURL() string {
	if value := strings.TrimSpace(os.Getenv("NASDAQ_API_BASE_URL")); value != "" {
		return strings.TrimRight(value, "/")
	}
	return defaultNasdaqBaseURL
}

func newNasdaqHTTPClient() *http.Client {
	return &http.Client{
		Timeout: defaultNasdaqTimeout,
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          32,
			MaxConnsPerHost:       8,
			MaxIdleConnsPerHost:   4,
			IdleConnTimeout:       60 * time.Second,
			TLSHandshakeTimeout:   5 * time.Second,
			ResponseHeaderTimeout: 8 * time.Second,
			ExpectContinueTimeout: time.Second,
		},
	}
}
