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
	"sort"
	"strings"
	"time"

	"github.com/hnic/trading-floor/pkg/model"
)

const (
	defaultFMPBaseURL   = "https://financialmodelingprep.com/stable"
	defaultFMPTimeout   = 15 * time.Second
	defaultFMPQuotePath = "/quote"
	defaultFMPHistory   = "/historical-price-eod/full"
)

type FMPProvider struct {
	client  *http.Client
	baseURL string
	apiKey  string
}

func NewFMPProvider(apiKey string) (*FMPProvider, error) {
	resolved := resolveFMPAPIKey(apiKey)
	if resolved == "" {
		return nil, fmt.Errorf("FMP market data provider requires FMP_API_KEY")
	}
	return &FMPProvider{
		client:  newFMPHTTPClient(),
		baseURL: resolveFMPBaseURL(),
		apiKey:  resolved,
	}, nil
}

func (p *FMPProvider) Snapshot(ctx context.Context, inst model.Instrument) (*Snapshot, error) {
	if p == nil {
		return nil, fmt.Errorf("nil FMP provider")
	}
	symbol := fmpSymbol(inst)
	if symbol == "" {
		return nil, fmt.Errorf("empty symbol")
	}

	endpoint, err := url.Parse(strings.TrimRight(p.baseURL, "/") + defaultFMPQuotePath)
	if err != nil {
		return nil, err
	}
	query := endpoint.Query()
	query.Set("symbol", symbol)
	query.Set("apikey", p.apiKey)
	endpoint.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "TradingFloor/1.0")
	req.Header.Set("Accept", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fmp quote status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}

	quote, err := parseFMPQuote(body)
	if err != nil {
		return nil, err
	}
	observedAt := time.Now().UTC()
	if quote.LastSaleTime > 0 {
		observedAt = time.UnixMilli(quote.LastSaleTime).UTC()
	}
	return &Snapshot{
		Symbol:     inst.Symbol,
		Last:       quote.Price,
		Bid:        quote.Bid,
		Ask:        quote.Ask,
		Volume:     quote.Volume,
		ObservedAt: observedAt,
	}, nil
}

func (p *FMPProvider) HistoricalBars(ctx context.Context, inst model.Instrument, end time.Time, duration, barSize, whatToShow string, useRTH bool) ([]HistoricalBar, error) {
	if p == nil {
		return nil, fmt.Errorf("nil FMP provider")
	}
	symbol := fmpSymbol(inst)
	if symbol == "" {
		return nil, fmt.Errorf("empty symbol")
	}

	endpoint, err := url.Parse(strings.TrimRight(p.baseURL, "/") + defaultFMPHistory)
	if err != nil {
		return nil, err
	}
	query := endpoint.Query()
	query.Set("symbol", symbol)
	query.Set("apikey", p.apiKey)
	query.Set("from", end.AddDate(0, 0, -14).Format("2006-01-02"))
	query.Set("to", end.Format("2006-01-02"))
	endpoint.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "TradingFloor/1.0")
	req.Header.Set("Accept", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fmp history status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, err
	}
	return parseFMPHistoricalBars(body)
}

type fmpQuote struct {
	Symbol       string  `json:"symbol"`
	Price        float64 `json:"price"`
	Bid          float64 `json:"bid"`
	Ask          float64 `json:"ask"`
	Volume       int64   `json:"volume"`
	LastSaleTime int64   `json:"lastSaleTime"`
}

type fmpHistoricalBar struct {
	Date  string  `json:"date"`
	Open  float64 `json:"open"`
	High  float64 `json:"high"`
	Low   float64 `json:"low"`
	Close float64 `json:"close"`
}

func parseFMPQuote(body []byte) (fmpQuote, error) {
	var quotes []fmpQuote
	if err := json.Unmarshal(body, &quotes); err == nil && len(quotes) > 0 {
		return quotes[0], nil
	}

	var single fmpQuote
	if err := json.Unmarshal(body, &single); err == nil && (single.Symbol != "" || single.Price > 0 || single.Bid > 0 || single.Ask > 0) {
		return single, nil
	}

	var wrapped struct {
		Data []fmpQuote `json:"data"`
	}
	if err := json.Unmarshal(body, &wrapped); err == nil && len(wrapped.Data) > 0 {
		return wrapped.Data[0], nil
	}

	return fmpQuote{}, fmt.Errorf("unexpected FMP quote response shape")
}

func parseFMPHistoricalBars(body []byte) ([]HistoricalBar, error) {
	var wrapped struct {
		Historical []fmpHistoricalBar `json:"historical"`
	}
	if err := json.Unmarshal(body, &wrapped); err == nil && len(wrapped.Historical) > 0 {
		return mapFMPHistoricalBars(wrapped.Historical)
	}

	var rows []fmpHistoricalBar
	if err := json.Unmarshal(body, &rows); err == nil && len(rows) > 0 {
		return mapFMPHistoricalBars(rows)
	}

	var dataWrapped struct {
		Data []fmpHistoricalBar `json:"data"`
	}
	if err := json.Unmarshal(body, &dataWrapped); err == nil && len(dataWrapped.Data) > 0 {
		return mapFMPHistoricalBars(dataWrapped.Data)
	}

	return nil, fmt.Errorf("unexpected FMP historical response shape")
}

func mapFMPHistoricalBars(rows []fmpHistoricalBar) ([]HistoricalBar, error) {
	out := make([]HistoricalBar, 0, len(rows))
	for _, row := range rows {
		parsed, err := time.Parse("2006-01-02", strings.TrimSpace(row.Date))
		if err != nil {
			return nil, fmt.Errorf("parse FMP historical date %q: %w", row.Date, err)
		}
		out = append(out, HistoricalBar{
			Time:  parsed.UTC(),
			Open:  row.Open,
			High:  row.High,
			Low:   row.Low,
			Close: row.Close,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Time.Before(out[j].Time)
	})
	return out, nil
}

func resolveFMPAPIKey(explicit string) string {
	if key := strings.TrimSpace(explicit); key != "" {
		return key
	}
	if key := strings.TrimSpace(os.Getenv("FMP_API_KEY")); key != "" {
		return key
	}
	return strings.TrimSpace(os.Getenv("EARNINGS_API_KEY"))
}

func resolveFMPBaseURL() string {
	if value := strings.TrimSpace(os.Getenv("FMP_API_BASE_URL")); value != "" {
		return strings.TrimRight(value, "/")
	}
	return defaultFMPBaseURL
}

func fmpSymbol(inst model.Instrument) string {
	symbol := strings.ToUpper(strings.TrimSpace(inst.Symbol))
	if symbol == "" {
		return ""
	}
	if strings.ToUpper(strings.TrimSpace(inst.SecType)) == "IND" && !strings.HasPrefix(symbol, "^") {
		return "^" + symbol
	}
	return symbol
}

func newFMPHTTPClient() *http.Client {
	return &http.Client{
		Timeout: defaultFMPTimeout,
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
