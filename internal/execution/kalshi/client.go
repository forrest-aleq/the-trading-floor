package kalshi

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultBaseURL      = "https://external-api.kalshi.com/trade-api/v2"
	defaultTimeout      = 20 * time.Second
	defaultMaxOrderDols = "5.00"
	LiveConfirmation    = "REAL_KALSHI_MONEY"
)

type Config struct {
	BaseURL        string
	KeyID          string
	PrivateKeyPEM  string
	PrivateKeyPath string
	LiveTrading    bool
	LiveConfirm    string
	MaxOrderCents  int64
	HTTPClient     *http.Client
}

func DefaultConfig() Config {
	maxOrderCents := parseDollarEnvCents("KALSHI_MAX_ORDER_DOLLARS", defaultMaxOrderDols)
	return Config{
		BaseURL:       strings.TrimSpace(os.Getenv("KALSHI_BASE_URL")),
		KeyID:         firstNonEmpty(os.Getenv("KALSHI_API_KEY_ID"), os.Getenv("KALSHI_ACCESS_KEY_ID")),
		PrivateKeyPEM: strings.TrimSpace(os.Getenv("KALSHI_PRIVATE_KEY_PEM")),
		PrivateKeyPath: firstNonEmpty(
			os.Getenv("KALSHI_PRIVATE_KEY_PATH"),
			os.Getenv("KALSHI_API_PRIVATE_KEY_PATH"),
		),
		LiveTrading:   readBoolEnv("KALSHI_LIVE_TRADING", false),
		LiveConfirm:   strings.TrimSpace(os.Getenv("KALSHI_LIVE_CONFIRM")),
		MaxOrderCents: maxOrderCents,
	}
}

type Client struct {
	baseURL       string
	keyID         string
	privateKey    *rsa.PrivateKey
	liveTrading   bool
	liveConfirm   string
	maxOrderCents int64
	http          *http.Client
}

func NewClient(cfg Config) (*Client, error) {
	baseURL, err := normalizeBaseURL(cfg.BaseURL)
	if err != nil {
		return nil, err
	}

	var key *rsa.PrivateKey
	if strings.TrimSpace(cfg.KeyID) != "" {
		key, err = loadPrivateKey(cfg)
		if err != nil {
			return nil, err
		}
	}

	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultTimeout}
	}

	return &Client{
		baseURL:       baseURL,
		keyID:         strings.TrimSpace(cfg.KeyID),
		privateKey:    key,
		liveTrading:   cfg.LiveTrading,
		liveConfirm:   strings.TrimSpace(cfg.LiveConfirm),
		maxOrderCents: cfg.MaxOrderCents,
		http:          httpClient,
	}, nil
}

func (c *Client) SetLiveConfirmation(confirm string) {
	if c == nil {
		return
	}
	c.liveConfirm = strings.TrimSpace(confirm)
}

type Balance struct {
	Balance        int64 `json:"balance"`
	PortfolioValue int64 `json:"portfolio_value"`
	UpdatedTS      int64 `json:"updated_ts"`
}

type ExchangeStatus struct {
	ExchangeActive bool   `json:"exchange_active"`
	TradingActive  bool   `json:"trading_active"`
	Message        string `json:"message"`
}

type Market struct {
	Ticker           string `json:"ticker"`
	EventTicker      string `json:"event_ticker"`
	MarketType       string `json:"market_type"`
	Title            string `json:"title"`
	Subtitle         string `json:"subtitle"`
	Status           string `json:"status"`
	YesBidDollars    string `json:"yes_bid_dollars"`
	YesAskDollars    string `json:"yes_ask_dollars"`
	NoBidDollars     string `json:"no_bid_dollars"`
	NoAskDollars     string `json:"no_ask_dollars"`
	LastPriceDollars string `json:"last_price_dollars"`
	CloseTime        string `json:"close_time"`
	ExpirationTime   string `json:"expiration_time"`
}

type MarketsResponse struct {
	Markets []Market `json:"markets"`
	Cursor  string   `json:"cursor"`
}

type Orderbook struct {
	Yes [][]int64 `json:"yes"`
	No  [][]int64 `json:"no"`
}

type OrderbookResponse struct {
	Orderbook Orderbook `json:"orderbook"`
}

type OrderRequest struct {
	Ticker                  string `json:"ticker"`
	Side                    string `json:"side"`   // yes|no
	Action                  string `json:"action"` // buy|sell
	ClientOrderID           string `json:"client_order_id,omitempty"`
	Count                   int64  `json:"count,omitempty"`
	CountFP                 string `json:"count_fp,omitempty"`
	YesPriceDollars         string `json:"yes_price_dollars,omitempty"`
	NoPriceDollars          string `json:"no_price_dollars,omitempty"`
	TimeInForce             string `json:"time_in_force,omitempty"`
	ExpirationTS            int64  `json:"expiration_ts,omitempty"`
	BuyMaxCost              int64  `json:"buy_max_cost,omitempty"`
	PostOnly                *bool  `json:"post_only,omitempty"`
	ReduceOnly              bool   `json:"reduce_only,omitempty"`
	SelfTradePreventionType string `json:"self_trade_prevention_type,omitempty"`
	OrderGroupID            string `json:"order_group_id,omitempty"`
	CancelOrderOnPause      bool   `json:"cancel_order_on_pause"`
	Subaccount              int    `json:"subaccount,omitempty"`
	ExchangeIndex           int    `json:"exchange_index,omitempty"`
}

type OrderResponse struct {
	OrderID              string `json:"order_id"`
	UserID               string `json:"user_id,omitempty"`
	ClientOrderID        string `json:"client_order_id"`
	Ticker               string `json:"ticker"`
	Side                 string `json:"side"`
	Action               string `json:"action"`
	OutcomeSide          string `json:"outcome_side,omitempty"`
	BookSide             string `json:"book_side,omitempty"`
	Type                 string `json:"type,omitempty"`
	Status               string `json:"status"`
	YesPriceDollars      string `json:"yes_price_dollars,omitempty"`
	NoPriceDollars       string `json:"no_price_dollars,omitempty"`
	FillCountFP          string `json:"fill_count_fp,omitempty"`
	RemainingCountFP     string `json:"remaining_count_fp,omitempty"`
	InitialCountFP       string `json:"initial_count_fp,omitempty"`
	TakerFillCostDollars string `json:"taker_fill_cost_dollars,omitempty"`
	MakerFillCostDollars string `json:"maker_fill_cost_dollars,omitempty"`
	TakerFeesDollars     string `json:"taker_fees_dollars,omitempty"`
	MakerFeesDollars     string `json:"maker_fees_dollars,omitempty"`
	ExpirationTime       string `json:"expiration_time,omitempty"`
	CreatedTime          string `json:"created_time,omitempty"`
	LastUpdateTime       string `json:"last_update_time,omitempty"`
	SubaccountNumber     int64  `json:"subaccount_number,omitempty"`
	SelfTradePrevention  string `json:"self_trade_prevention_type,omitempty"`
	OrderGroupID         string `json:"order_group_id,omitempty"`
	CancelOrderOnPause   bool   `json:"cancel_order_on_pause,omitempty"`
	LegacyFillCount      string `json:"fill_count,omitempty"`
	LegacyRemainingCount string `json:"remaining_count,omitempty"`
	TimestampMS          int64  `json:"ts_ms,omitempty"`
	LegacyAverageFill    string `json:"average_fill_price,omitempty"`
	LegacyAverageFeePaid string `json:"average_fee_paid,omitempty"`
}

func (r OrderRequest) PriceDollars() string {
	side := strings.ToLower(strings.TrimSpace(r.Side))
	if side == "no" {
		return strings.TrimSpace(r.NoPriceDollars)
	}
	return strings.TrimSpace(r.YesPriceDollars)
}

func (r *OrderResponse) FilledCount() float64 {
	count, ok := r.filledCount()
	if !ok {
		return 0
	}
	value, _ := count.Float64()
	return value
}

func (r *OrderResponse) HasFill() bool {
	count, ok := r.filledCount()
	return ok && count.Sign() > 0
}

func (r *OrderResponse) IsResting() bool {
	if r == nil {
		return false
	}
	status := strings.ToLower(strings.TrimSpace(r.Status))
	switch status {
	case "open", "pending", "resting", "accepted":
		return true
	}
	remaining, ok := parseOrderCountValue(firstNonEmpty(r.RemainingCountFP, r.LegacyRemainingCount))
	return ok && remaining.Sign() > 0
}

func (r *OrderResponse) filledCount() (*big.Rat, bool) {
	if r == nil {
		return nil, false
	}
	return parseOrderCountValue(firstNonEmpty(r.FillCountFP, r.LegacyFillCount))
}

type OrderValidation struct {
	EstimatedRiskCents int64
	MaxOrderCents      int64
}

type createOrderResponse struct {
	Order OrderResponse `json:"order"`
}

func (c *Client) GetBalance(ctx context.Context) (*Balance, error) {
	var out Balance
	if err := c.do(ctx, http.MethodGet, "/portfolio/balance", nil, nil, &out, true); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) GetExchangeStatus(ctx context.Context) (*ExchangeStatus, error) {
	var out ExchangeStatus
	if err := c.do(ctx, http.MethodGet, "/exchange/status", nil, nil, &out, false); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) GetMarkets(ctx context.Context, status string, limit int, cursor string) (*MarketsResponse, error) {
	q := url.Values{}
	if strings.TrimSpace(status) != "" {
		q.Set("status", strings.TrimSpace(status))
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if strings.TrimSpace(cursor) != "" {
		q.Set("cursor", strings.TrimSpace(cursor))
	}
	var out MarketsResponse
	if err := c.do(ctx, http.MethodGet, "/markets", q, nil, &out, false); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) GetOrderbook(ctx context.Context, ticker string) (*OrderbookResponse, error) {
	ticker = strings.TrimSpace(ticker)
	if ticker == "" {
		return nil, fmt.Errorf("ticker required")
	}
	var out OrderbookResponse
	if err := c.do(ctx, http.MethodGet, "/markets/"+url.PathEscape(ticker)+"/orderbook", nil, nil, &out, false); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) CreateOrder(ctx context.Context, req OrderRequest) (*OrderResponse, error) {
	if !c.liveTrading {
		return nil, fmt.Errorf("kalshi live trading disabled; set KALSHI_LIVE_TRADING=true")
	}
	if c.liveConfirm != LiveConfirmation {
		return nil, fmt.Errorf("kalshi live confirmation missing; set KALSHI_LIVE_CONFIRM=%s", LiveConfirmation)
	}
	if _, err := c.ValidateOrder(req); err != nil {
		return nil, err
	}
	req = normalizeOrderRequest(req)

	var out createOrderResponse
	if err := c.do(ctx, http.MethodPost, "/portfolio/orders", nil, req, &out, true); err != nil {
		return nil, err
	}
	return &out.Order, nil
}

func (c *Client) ValidateOrder(req OrderRequest) (*OrderValidation, error) {
	req = normalizeOrderRequest(req)
	if strings.TrimSpace(req.Ticker) == "" {
		return nil, fmt.Errorf("ticker required")
	}
	if strings.TrimSpace(req.ClientOrderID) == "" {
		return nil, fmt.Errorf("client_order_id required")
	}
	if req.Side != "yes" && req.Side != "no" {
		return nil, fmt.Errorf("side must be yes or no")
	}
	if req.Action != "buy" && req.Action != "sell" {
		return nil, fmt.Errorf("action must be buy or sell")
	}
	count, err := orderCount(req)
	if err != nil {
		return nil, fmt.Errorf("count: %w", err)
	}
	priceRaw := req.YesPriceDollars
	if req.Side == "no" {
		priceRaw = req.NoPriceDollars
	}
	price, err := parsePositiveDecimal(priceRaw)
	if err != nil {
		return nil, fmt.Errorf("price: %w", err)
	}
	if price.Cmp(big.NewRat(1, 100)) < 0 || price.Cmp(big.NewRat(99, 100)) > 0 {
		return nil, fmt.Errorf("price must be between 0.01 and 0.99 dollars")
	}

	risk := new(big.Rat).Set(price)
	if req.Action == "sell" {
		risk.Sub(big.NewRat(1, 1), price)
	}
	risk.Mul(risk, count)
	risk.Mul(risk, big.NewRat(100, 1))
	riskCents, err := ceilRatToInt64(risk)
	if err != nil {
		return nil, err
	}

	maxOrderCents := c.maxOrderCents
	if maxOrderCents <= 0 {
		maxOrderCents = parseDollarEnvCents("KALSHI_MAX_ORDER_DOLLARS", defaultMaxOrderDols)
	}
	if maxOrderCents > 0 && riskCents > maxOrderCents {
		return nil, fmt.Errorf("estimated max risk %s exceeds KALSHI_MAX_ORDER_DOLLARS %s", FormatCents(riskCents), FormatCents(maxOrderCents))
	}
	if req.BuyMaxCost > 0 && req.Action == "buy" && riskCents > req.BuyMaxCost {
		return nil, fmt.Errorf("estimated max risk %s exceeds buy_max_cost %s", FormatCents(riskCents), FormatCents(req.BuyMaxCost))
	}

	return &OrderValidation{
		EstimatedRiskCents: riskCents,
		MaxOrderCents:      maxOrderCents,
	}, nil
}

func (c *Client) do(ctx context.Context, method, path string, query url.Values, body any, out any, auth bool) error {
	endpoint, err := c.endpoint(path, query)
	if err != nil {
		return err
	}

	var reqBody []byte
	if body != nil {
		reqBody, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(reqBody))
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if auth || c.hasCredentials() {
		if err := c.sign(req); err != nil {
			return err
		}
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("kalshi status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	if out == nil || len(respBody) == 0 {
		return nil
	}
	if err := json.Unmarshal(respBody, out); err != nil {
		return fmt.Errorf("decode kalshi response: %w", err)
	}
	return nil
}

func (c *Client) endpoint(path string, query url.Values) (string, error) {
	base, err := url.Parse(c.baseURL)
	if err != nil {
		return "", err
	}
	rel := strings.TrimLeft(path, "/")
	base.Path = strings.TrimRight(base.Path, "/") + "/" + rel
	if query != nil {
		base.RawQuery = query.Encode()
	}
	return base.String(), nil
}

func (c *Client) sign(req *http.Request) error {
	if !c.hasCredentials() {
		return fmt.Errorf("kalshi credentials required: set KALSHI_API_KEY_ID and KALSHI_PRIVATE_KEY_PATH or KALSHI_PRIVATE_KEY_PEM")
	}

	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
	msg := timestamp + req.Method + req.URL.Path
	digest := sha256.Sum256([]byte(msg))
	signature, err := rsa.SignPSS(rand.Reader, c.privateKey, crypto.SHA256, digest[:], &rsa.PSSOptions{
		SaltLength: rsa.PSSSaltLengthEqualsHash,
		Hash:       crypto.SHA256,
	})
	if err != nil {
		return err
	}

	req.Header.Set("KALSHI-ACCESS-KEY", c.keyID)
	req.Header.Set("KALSHI-ACCESS-TIMESTAMP", timestamp)
	req.Header.Set("KALSHI-ACCESS-SIGNATURE", base64.StdEncoding.EncodeToString(signature))
	return nil
}

func (c *Client) hasCredentials() bool {
	return strings.TrimSpace(c.keyID) != "" && c.privateKey != nil
}

func normalizeOrderRequest(req OrderRequest) OrderRequest {
	req.Ticker = strings.TrimSpace(req.Ticker)
	req.ClientOrderID = strings.TrimSpace(req.ClientOrderID)
	req.Side = strings.ToLower(strings.TrimSpace(req.Side))
	req.Action = strings.ToLower(strings.TrimSpace(req.Action))
	if req.Action == "" {
		req.Action = "buy"
	}
	req.CountFP = strings.TrimSpace(req.CountFP)
	req.YesPriceDollars = strings.TrimSpace(req.YesPriceDollars)
	req.NoPriceDollars = strings.TrimSpace(req.NoPriceDollars)
	if strings.TrimSpace(req.TimeInForce) == "" {
		req.TimeInForce = "fill_or_kill"
	}
	switch req.TimeInForce {
	case "fill_or_kill", "good_till_canceled", "immediate_or_cancel":
	default:
		req.TimeInForce = "fill_or_kill"
	}
	if strings.TrimSpace(req.SelfTradePreventionType) == "" {
		req.SelfTradePreventionType = "taker_at_cross"
	}
	req.CancelOrderOnPause = true
	return req
}

func orderCount(req OrderRequest) (*big.Rat, error) {
	count := big.NewRat(0, 1)
	if req.Count > 0 {
		count.SetInt64(req.Count)
	}
	if req.CountFP != "" {
		countFP, err := parsePositiveDecimal(req.CountFP)
		if err != nil {
			return nil, err
		}
		if req.Count > 0 && count.Cmp(countFP) != 0 {
			return nil, fmt.Errorf("count and count_fp mismatch")
		}
		count = countFP
	}
	if count.Sign() <= 0 {
		return nil, fmt.Errorf("value required")
	}
	if !count.IsInt() {
		return nil, fmt.Errorf("whole contract count required")
	}
	return count, nil
}

func parseOrderCountValue(raw string) (*big.Rat, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, false
	}
	value, ok := new(big.Rat).SetString(raw)
	if !ok {
		return nil, false
	}
	return value, true
}

func normalizeBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = defaultBaseURL
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("invalid KALSHI_BASE_URL %q", raw)
	}
	if !strings.HasSuffix(strings.TrimRight(u.Path, "/"), "/trade-api/v2") {
		u.Path = strings.TrimRight(u.Path, "/") + "/trade-api/v2"
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawQuery = ""
	return u.String(), nil
}

func loadPrivateKey(cfg Config) (*rsa.PrivateKey, error) {
	pemBytes := []byte(strings.TrimSpace(cfg.PrivateKeyPEM))
	if len(pemBytes) == 0 && strings.TrimSpace(cfg.PrivateKeyPath) != "" {
		raw, err := os.ReadFile(strings.TrimSpace(cfg.PrivateKeyPath))
		if err != nil {
			return nil, err
		}
		pemBytes = raw
	}
	if len(pemBytes) == 0 {
		return nil, fmt.Errorf("kalshi private key required")
	}

	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, fmt.Errorf("decode kalshi private key PEM")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("kalshi private key must be RSA")
	}
	return key, nil
}

func parseDollarEnvCents(name, fallback string) int64 {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		raw = fallback
	}
	cents, err := parseDollarCents(raw)
	if err != nil {
		cents, _ = parseDollarCents(fallback)
	}
	return cents
}

func parseDollarCents(raw string) (int64, error) {
	rat, err := parsePositiveDecimal(raw)
	if err != nil {
		return 0, err
	}
	rat.Mul(rat, big.NewRat(100, 1))
	return ceilRatToInt64(rat)
}

func parsePositiveDecimal(raw string) (*big.Rat, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("value required")
	}
	value, ok := new(big.Rat).SetString(raw)
	if !ok {
		return nil, fmt.Errorf("invalid decimal %q", raw)
	}
	if value.Sign() <= 0 {
		return nil, fmt.Errorf("must be positive")
	}
	return value, nil
}

func ceilRatToInt64(value *big.Rat) (int64, error) {
	if value == nil {
		return 0, fmt.Errorf("nil value")
	}
	num := new(big.Int).Set(value.Num())
	den := new(big.Int).Set(value.Denom())
	q, r := new(big.Int).QuoRem(num, den, new(big.Int))
	if r.Sign() != 0 {
		q.Add(q, big.NewInt(1))
	}
	if !q.IsInt64() {
		return 0, fmt.Errorf("value too large")
	}
	return q.Int64(), nil
}

func FormatCents(cents int64) string {
	sign := ""
	if cents < 0 {
		sign = "-"
		cents = -cents
	}
	return fmt.Sprintf("%s$%d.%02d", sign, cents/100, cents%100)
}

func readBoolEnv(name string, fallback bool) bool {
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}
