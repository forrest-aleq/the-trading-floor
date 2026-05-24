package kalshi

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hnic/trading-floor/pkg/model"
)

type ExecutionMode string

const (
	ExecutionDisabled ExecutionMode = "disabled"
	ExecutionDryRun   ExecutionMode = "dry_run"
	ExecutionLive     ExecutionMode = "live"
)

type MapperConfig struct {
	MaxOrderCents int64
	MinOrderCents int64
	RiskPctEquity float64
	MinConviction float64
}

type Mapper struct {
	cfg MapperConfig
}

type MappedOrder struct {
	Request            OrderRequest `json:"request"`
	EstimatedRiskCents int64        `json:"estimated_risk_cents"`
	MaxOrderCents      int64        `json:"max_order_cents"`
	ThesisID           string       `json:"thesis_id"`
	DeskID             string       `json:"desk_id"`
	Direction          string       `json:"direction"`
	ContractIntent     string       `json:"contract_intent"`
}

type Executor struct {
	client      *Client
	mapper      *Mapper
	mode        ExecutionMode
	journalPath string
	log         *slog.Logger
}

type ExecutionResult struct {
	Mode        ExecutionMode  `json:"mode"`
	DryRun      bool           `json:"dry_run"`
	MappedOrder MappedOrder    `json:"mapped_order"`
	Response    *OrderResponse `json:"response,omitempty"`
	RecordedAt  time.Time      `json:"recorded_at"`
}

func NewMapper(cfg MapperConfig) *Mapper {
	if cfg.MaxOrderCents <= 0 {
		cfg.MaxOrderCents = parseDollarEnvCents("KALSHI_MAX_ORDER_DOLLARS", defaultMaxOrderDols)
	}
	if cfg.MinOrderCents <= 0 {
		cfg.MinOrderCents = parseDollarEnvCents("KALSHI_MIN_ORDER_DOLLARS", "0")
	}
	if cfg.RiskPctEquity <= 0 {
		cfg.RiskPctEquity = readFloatEnv("KALSHI_RISK_PCT_EQUITY", 0)
	}
	if cfg.MinConviction <= 0 {
		cfg.MinConviction = readFloatEnv("KALSHI_MIN_CONVICTION", 0.65)
	}
	return &Mapper{cfg: cfg}
}

func NewExecutor(client *Client, mapper *Mapper, mode ExecutionMode, journalPath string) *Executor {
	if mapper == nil {
		mapper = NewMapper(MapperConfig{})
	}
	if mode == "" {
		mode = ExecutionDryRun
	}
	if journalPath == "" {
		journalPath = strings.TrimSpace(os.Getenv("KALSHI_DRY_RUN_JOURNAL"))
	}
	if journalPath == "" {
		journalPath = "kalshi_dry_run.jsonl"
	}
	return &Executor{
		client:      client,
		mapper:      mapper,
		mode:        mode,
		journalPath: journalPath,
		log:         slog.Default().With("component", "kalshi-execution"),
	}
}

func NewExecutorFromEnv() (*Executor, error) {
	mode := parseExecutionMode(os.Getenv("KALSHI_EXECUTION_MODE"))
	if mode == ExecutionDisabled {
		return nil, nil
	}
	client, err := NewClient(DefaultConfig())
	if err != nil {
		return nil, err
	}
	return NewExecutor(client, NewMapper(MapperConfig{}), mode, strings.TrimSpace(os.Getenv("KALSHI_DRY_RUN_JOURNAL"))), nil
}

func parseExecutionMode(raw string) ExecutionMode {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "dry", "dry-run", "dry_run", "shadow", "validate":
		return ExecutionDryRun
	case "live", "real":
		return ExecutionLive
	case "off", "false", "disabled":
		return ExecutionDisabled
	default:
		return ExecutionDryRun
	}
}

func (e *Executor) IsDryRun() bool {
	return e == nil || e.mode != ExecutionLive
}

func (e *Executor) AccountEquityCents(ctx context.Context) (int64, error) {
	if e == nil || e.client == nil || !e.client.hasCredentials() {
		return 0, fmt.Errorf("kalshi credentials unavailable")
	}
	balance, err := e.client.GetBalance(ctx)
	if err != nil {
		return 0, err
	}
	equityCents := balance.Balance + balance.PortfolioValue
	if equityCents <= 0 {
		return 0, fmt.Errorf("kalshi account equity unavailable")
	}
	return equityCents, nil
}

func (e *Executor) SubmitThesis(ctx context.Context, thesis *model.Thesis) (*ExecutionResult, error) {
	if e == nil || e.mapper == nil {
		return nil, fmt.Errorf("nil kalshi executor")
	}
	maxOrderCents := e.effectiveMaxOrderCents(ctx)
	mapped, err := e.mapper.MapThesisWithMaxOrderCents(thesis, maxOrderCents)
	if err != nil {
		return nil, err
	}
	if e.client != nil {
		validation, err := (&Client{maxOrderCents: maxOrderCents}).ValidateOrder(mapped.Request)
		if err != nil {
			return nil, err
		}
		mapped.EstimatedRiskCents = validation.EstimatedRiskCents
		mapped.MaxOrderCents = validation.MaxOrderCents
	}

	result := &ExecutionResult{
		Mode:        e.mode,
		DryRun:      e.mode != ExecutionLive,
		MappedOrder: mapped,
		RecordedAt:  time.Now().UTC(),
	}

	if e.mode == ExecutionLive {
		if e.client == nil {
			return nil, fmt.Errorf("nil kalshi client")
		}
		resp, err := e.client.CreateOrder(ctx, mapped.Request)
		if err != nil {
			_ = e.record(result, err)
			return nil, err
		}
		result.Response = resp
	}

	if err := e.record(result, nil); err != nil {
		e.log.Warn("record kalshi execution journal failed", "error", err)
	}
	return result, nil
}

func (e *Executor) record(result *ExecutionResult, submitErr error) error {
	if e == nil || result == nil || strings.TrimSpace(e.journalPath) == "" {
		return nil
	}
	entry := map[string]any{
		"recorded_at":  result.RecordedAt,
		"mode":         result.Mode,
		"dry_run":      result.DryRun,
		"mapped_order": result.MappedOrder,
	}
	if result.Response != nil {
		entry["response"] = result.Response
	}
	if submitErr != nil {
		entry["error"] = submitErr.Error()
	}
	raw, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(e.journalPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		_ = f.Close()
	}()
	if _, err := f.Write(append(raw, '\n')); err != nil {
		return err
	}
	return nil
}

func (m *Mapper) MapThesis(thesis *model.Thesis) (MappedOrder, error) {
	return m.MapThesisWithMaxOrderCents(thesis, m.cfg.MaxOrderCents)
}

func (m *Mapper) MapThesisWithMaxOrderCents(thesis *model.Thesis, maxOrderCents int64) (MappedOrder, error) {
	if thesis == nil {
		return MappedOrder{}, fmt.Errorf("nil thesis")
	}
	if thesis.Conviction < m.cfg.MinConviction {
		return MappedOrder{}, fmt.Errorf("conviction %.2f below kalshi minimum %.2f", thesis.Conviction, m.cfg.MinConviction)
	}

	ticker := strings.ToUpper(strings.TrimSpace(thesis.PrimaryInstrument().Symbol))
	if ticker == "" {
		return MappedOrder{}, fmt.Errorf("kalshi ticker required")
	}
	if !strings.HasPrefix(ticker, "KX") {
		return MappedOrder{}, fmt.Errorf("refusing non-Kalshi ticker %q", ticker)
	}

	entryPrice := kalshiEntryPrice(thesis)
	if entryPrice <= 0 || entryPrice >= 1 {
		return MappedOrder{}, fmt.Errorf("kalshi thesis requires entry_price or market_context price between 0.01 and 0.99")
	}

	side := "yes"
	orderPrice := entryPrice
	intent := "buy_yes"
	switch thesis.Direction {
	case model.Short:
		side = "no"
		intent = "buy_no"
	case model.Long:
	default:
		return MappedOrder{}, fmt.Errorf("kalshi direction must be long or short")
	}
	if orderPrice < 0.01 || orderPrice > 0.99 {
		return MappedOrder{}, fmt.Errorf("mapped kalshi order price %.4f outside 0.01..0.99", orderPrice)
	}

	if maxOrderCents <= 0 {
		maxOrderCents = parseDollarEnvCents("KALSHI_MAX_ORDER_DOLLARS", defaultMaxOrderDols)
	}
	count := kalshiOrderCount(maxOrderCents, side, orderPrice)
	if count <= 0 {
		return MappedOrder{}, fmt.Errorf("risk cap %s too small for one contract at %.4f", FormatCents(maxOrderCents), orderPrice)
	}

	req := OrderRequest{
		Ticker:                  ticker,
		ClientOrderID:           kalshiClientOrderID(thesis.ID),
		Side:                    side,
		Action:                  "buy",
		Count:                   count,
		TimeInForce:             "fill_or_kill",
		SelfTradePreventionType: "taker_at_cross",
		CancelOrderOnPause:      true,
	}
	if side == "yes" {
		req.YesPriceDollars = formatFixedDollar(orderPrice)
	} else {
		req.NoPriceDollars = formatFixedDollar(orderPrice)
	}
	validation, err := (&Client{maxOrderCents: maxOrderCents}).ValidateOrder(req)
	if err != nil {
		return MappedOrder{}, err
	}
	req.BuyMaxCost = validation.EstimatedRiskCents
	return MappedOrder{
		Request:            req,
		EstimatedRiskCents: validation.EstimatedRiskCents,
		MaxOrderCents:      validation.MaxOrderCents,
		ThesisID:           thesis.ID,
		DeskID:             thesis.DeskID,
		Direction:          string(thesis.Direction),
		ContractIntent:     intent,
	}, nil
}

func (e *Executor) effectiveMaxOrderCents(ctx context.Context) int64 {
	if e == nil || e.mapper == nil {
		return 0
	}
	maxOrderCents := e.mapper.cfg.MaxOrderCents
	if maxOrderCents <= 0 {
		maxOrderCents = parseDollarEnvCents("KALSHI_MAX_ORDER_DOLLARS", defaultMaxOrderDols)
	}
	riskPct := e.mapper.cfg.RiskPctEquity
	if riskPct <= 0 || e.client == nil || !e.client.hasCredentials() {
		return maxOrderCents
	}

	balance, err := e.client.GetBalance(ctx)
	if err != nil {
		e.log.Warn("kalshi balance unavailable for dynamic sizing; using static cap",
			"error", err,
			"static_cap", FormatCents(maxOrderCents),
		)
		return maxOrderCents
	}
	equityCents := balance.Balance + balance.PortfolioValue
	if equityCents <= 0 {
		return maxOrderCents
	}

	dynamic := int64(math.Ceil(float64(equityCents) * riskPct / 100.0))
	minOrderCents := e.mapper.cfg.MinOrderCents
	if minOrderCents > 0 && dynamic < minOrderCents && equityCents >= minOrderCents {
		dynamic = minOrderCents
	}
	if maxOrderCents > 0 && dynamic > maxOrderCents {
		dynamic = maxOrderCents
	}
	if dynamic <= 0 {
		return maxOrderCents
	}
	return dynamic
}

func kalshiEntryPrice(thesis *model.Thesis) float64 {
	if thesis == nil {
		return 0
	}
	for _, candidate := range []float64{
		thesis.EntryPrice,
		marketContextPrice(thesis),
		thesis.TargetPrice,
	} {
		if candidate > 0 && candidate < 1 {
			return candidate
		}
	}
	return 0
}

func marketContextPrice(thesis *model.Thesis) float64 {
	if thesis == nil || thesis.MarketContext == nil {
		return 0
	}
	for _, candidate := range []float64{
		thesis.MarketContext.MidPrice,
		thesis.MarketContext.CurrentPrice,
		thesis.MarketContext.AskPrice,
		thesis.MarketContext.BidPrice,
	} {
		if candidate > 0 && candidate < 1 {
			return candidate
		}
	}
	return 0
}

func kalshiOrderCount(maxOrderCents int64, side string, orderPrice float64) int64 {
	if maxOrderCents <= 0 || orderPrice <= 0 || orderPrice >= 1 {
		return 0
	}
	riskPerContract := orderPrice
	riskCents := int64(math.Ceil(riskPerContract * 100))
	if riskCents <= 0 {
		return 0
	}
	return maxOrderCents / riskCents
}

func kalshiClientOrderID(thesisID string) string {
	thesisID = strings.TrimSpace(thesisID)
	if thesisID == "" {
		thesisID = uuid.NewString()
	}
	id := "tf-" + thesisID
	if len(id) <= 64 {
		return id
	}
	return id[:64]
}

func formatFixedDollar(value float64) string {
	return fmt.Sprintf("%.4f", value)
}

func readFloatEnv(name string, fallback float64) float64 {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return fallback
	}
	return parsed
}
