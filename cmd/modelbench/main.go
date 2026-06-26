package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/joho/godotenv"

	"github.com/hnic/trading-floor/internal/llm"
)

const defaultModels = "z-ai/glm-5.2,moonshotai/kimi-k2.7-code,minimax/minimax-m3,deepseek/deepseek-v4-pro,qwen/qwen3.7-max"
const defaultComparators = ""

type catalogResponse struct {
	Data []catalogModel `json:"data"`
}

type catalogModel struct {
	ID                  string   `json:"id"`
	Name                string   `json:"name"`
	Created             int64    `json:"created"`
	ContextLength       int      `json:"context_length"`
	SupportedParameters []string `json:"supported_parameters"`
	Pricing             struct {
		Prompt     string `json:"prompt"`
		Completion string `json:"completion"`
	} `json:"pricing"`
}

type benchResult struct {
	Mode         string
	CaseID       string
	Model        string
	Name         string
	Catalog      bool
	StructuredOK bool
	DecisionOK   bool
	SideOK       bool
	CategoryOK   bool
	RiskOK       bool
	Score        int
	Latency      time.Duration
	InputTokens  int
	OutputTokens int
	Response     string
	Err          error
}

type benchCase struct {
	ID               string
	Title            string
	Market           string
	ExpectedTrade    bool
	ExpectedSide     string
	ExpectedDir      string
	ExpectedCategory string
	MaxDollars       float64
}

type parsedDecision struct {
	Tradeable   bool
	Side        string
	Direction   string
	Category    string
	MaxDollars  float64
	Score       float64
	Instruments string
	Urgency     float64
	Reasoning   string
}

type summaryKey struct {
	Mode  string
	Model string
}

type summaryBucket struct {
	Mode         string
	Model        string
	Cases        int
	StructuredOK int
	DecisionOK   int
	SideOK       int
	CategoryOK   int
	RiskOK       int
	ScoreTotal   int
	Failures     int
	LatencyTotal time.Duration
	InputTokens  int
	OutputTokens int
}

func main() {
	_ = godotenv.Load()

	modelsFlag := flag.String("models", firstNonEmpty(os.Getenv("MODEL_HARNESS_MODELS"), defaultModels), "comma-separated OpenRouter model ids")
	comparatorsFlag := flag.String("comparators", "", "optional comma-separated closed/proprietary comparator model ids")
	includeClosedFlag := flag.Bool("include-closed", false, "include the default closed comparator set")
	modeFlag := flag.String("mode", firstNonEmpty(os.Getenv("MODEL_HARNESS_MODE"), "both"), "benchmark mode: json, thought, or both")
	repeatFlag := flag.Int("repeat", 1, "number of times to call each model")
	timeoutFlag := flag.Duration("timeout", 45*time.Second, "per-model timeout")
	maxTokensFlag := flag.Int("max-tokens", 700, "max completion tokens per model")
	flag.Parse()

	models := splitCSV(*modelsFlag)
	if *includeClosedFlag {
		models = append(models, splitCSV(defaultComparators)...)
	}
	models = append(models, splitCSV(*comparatorsFlag)...)
	models = dedupe(models)
	if len(models) == 0 {
		fmt.Fprintln(os.Stderr, "no models configured")
		os.Exit(2)
	}
	if *repeatFlag < 1 {
		*repeatFlag = 1
	}
	modes := benchmarkModes(*modeFlag)
	if len(modes) == 0 {
		fmt.Fprintln(os.Stderr, "mode must be json, thought, or both")
		os.Exit(2)
	}

	baseURL := firstNonEmpty(os.Getenv("LLM_BASE_URL"), "https://openrouter.ai/api/v1")
	apiKey := os.Getenv("OPENROUTER_API_KEY")
	catalog := loadCatalog(context.Background(), baseURL, apiKey)
	cases := scannerEvalCases()

	fmt.Println("run\tmode\tcase\tmodel\tname\tcatalog\tcontext\tprompt_per_m\tcompletion_per_m\tstructured\tdecision_ok\tside_ok\tcategory_ok\trisk_ok\tscore\tlatency_ms\tinput_tokens\toutput_tokens\tresponse_or_error")
	successes := 0
	summary := map[summaryKey]*summaryBucket{}
	for run := 1; run <= *repeatFlag; run++ {
		for _, model := range models {
			for _, mode := range modes {
				for _, benchCase := range cases {
					result := runBench(model, mode, benchCase, baseURL, apiKey, catalog, *timeoutFlag, *maxTokensFlag)
					if result.Err == nil && result.StructuredOK && result.DecisionOK {
						successes++
					}
					meta := catalogModel{}
					if catalog != nil {
						meta = catalog[model]
					}
					fmt.Printf("%d\t%s\t%s\t%s\t%s\t%t\t%d\t%s\t%s\t%t\t%t\t%t\t%t\t%t\t%d\t%d\t%d\t%d\t%s\n",
						run,
						result.Mode,
						result.CaseID,
						result.Model,
						result.Name,
						result.Catalog,
						meta.ContextLength,
						pricePerMillion(meta.Pricing.Prompt),
						pricePerMillion(meta.Pricing.Completion),
						result.StructuredOK,
						result.DecisionOK,
						result.SideOK,
						result.CategoryOK,
						result.RiskOK,
						result.Score,
						result.Latency.Milliseconds(),
						result.InputTokens,
						result.OutputTokens,
						preview(result),
					)
					recordSummary(summary, result)
				}
			}
		}
	}
	printSummary(summary)

	if successes == 0 {
		os.Exit(1)
	}
}

func runBench(model, mode string, benchCase benchCase, baseURL, apiKey string, catalog map[string]catalogModel, timeout time.Duration, maxTokens int) benchResult {
	result := benchResult{Mode: mode, CaseID: benchCase.ID, Model: model}
	if meta, ok := catalog[model]; ok {
		result.Catalog = true
		result.Name = meta.Name
	}

	client := llm.NewOpenRouterClient(llm.OpenRouterConfig{
		APIKey:    apiKey,
		BaseURL:   baseURL,
		Model:     model,
		EnvPrefix: "LLM_SPEED",
	})

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	start := time.Now()
	resp, err := client.Complete(ctx, llm.Request{
		Model:       model,
		Messages:    benchMessages(mode, benchCase),
		Temperature: 0,
		MaxTokens:   maxTokens,
		JSONMode:    mode == "json",
	})
	result.Latency = time.Since(start)
	if err != nil {
		result.Err = err
		return result
	}

	result.Response = strings.TrimSpace(resp.Content)
	result.InputTokens = resp.InputTokens
	result.OutputTokens = resp.OutputTokens
	scoreResult(&result, benchCase)
	return result
}

func benchMessages(mode string, benchCase benchCase) []llm.Message {
	market := strings.TrimSpace(fmt.Sprintf(`Evaluate this prediction market for a $50 bankroll.

Case: %s
Title: %s

%s

Bot constraints:
- max order %.2f dollars
- min conviction 0.70
- do not trade without explicit positive expected value after spread
- if there is no clear edge, choose tradeable=false`, benchCase.ID, benchCase.Title, benchCase.Market, benchCase.MaxDollars))
	if mode == "thought" {
		return []llm.Message{
			{
				Role: llm.RoleSystem,
				Content: strings.TrimSpace(`You are a Kalshi trading scanner. Reason carefully about edge, spread, liquidity, and bankroll risk.

You MUST end with exactly one terminal decision block:
FINAL_DECISION
tradeable: true|false
score: 0-100
instruments: SYMBOL:SECTYPE:CURRENCY, ...
direction: long|short|none
urgency: 0.0-1.0
category: macro|corporate|geopolitical|flows|tail|volatility|sector|systematic|prediction_market
max_dollars: numeric dollars, 0 when no trade
reasoning: short explanation
END_FINAL_DECISION`),
			},
			{Role: llm.RoleUser, Content: market},
		}
	}
	return []llm.Message{
		{
			Role:    llm.RoleSystem,
			Content: "You are a Kalshi trading scanner. Return compact JSON only. Do not include markdown. Use category exactly one of: macro, corporate, geopolitical, flows, tail, volatility, sector, systematic, prediction_market.",
		},
		{
			Role:    llm.RoleUser,
			Content: "Return JSON with fields: tradeable boolean, side one of \"yes\"|\"no\"|\"none\", limit_price_cents integer, max_dollars number, conviction number, category string, rationale string.\n\n" + market,
		},
	}
}

func scannerEvalCases() []benchCase {
	return []benchCase{
		{
			ID:               "no_edge_wide_spread",
			Title:            "Reject a wide-spread market with no independent edge",
			ExpectedTrade:    false,
			ExpectedSide:     "none",
			ExpectedDir:      "none",
			ExpectedCategory: "prediction_market",
			MaxDollars:       1,
			Market: strings.TrimSpace(`Market:
Ticker: EXAMPLE-NOEDGE-26
Question: Will the next US economic release beat consensus?
Yes bid/ask: 44/49 cents
No bid/ask: 51/56 cents
Volume: 62,500 contracts
Close: 36 hours from now
Evidence supplied: no independent forecast, no primary data, no liquid-market proxy divergence.`),
		},
		{
			ID:               "yes_edge_proxy_divergence",
			Title:            "Buy yes when liquid proxy implies much higher probability",
			ExpectedTrade:    true,
			ExpectedSide:     "yes",
			ExpectedDir:      "long",
			ExpectedCategory: "prediction_market",
			MaxDollars:       1,
			Market: strings.TrimSpace(`Market:
Ticker: EXAMPLE-YESEDGE-26
Question: Will the Fed cut rates at the next meeting?
Yes bid/ask: 43/45 cents
No bid/ask: 55/57 cents
Volume: 184,000 contracts
Close: 18 hours from now
Independent proxy: Fed funds futures imply 61-64% probability after a fresh FOMC speaker and OIS repricing.
Execution note: yes ask 45 leaves positive edge after spread under the proxy estimate.`),
		},
		{
			ID:               "no_side_mispriced",
			Title:            "Buy no when yes is overpriced versus evidence",
			ExpectedTrade:    true,
			ExpectedSide:     "no",
			ExpectedDir:      "short",
			ExpectedCategory: "prediction_market",
			MaxDollars:       1,
			Market: strings.TrimSpace(`Market:
Ticker: EXAMPLE-NOEDGE-EXPRESS-26
Question: Will a named company file for bankruptcy before month-end?
Yes bid/ask: 70/73 cents
No bid/ask: 27/30 cents
Volume: 91,000 contracts
Close: 72 hours from now
Fresh evidence: company announced completed refinancing, current bonds rallied, CDS tightened, and no filing appears in court dockets.
Execution note: no ask 30 is below the evidence-implied fair probability of at least 45%.`),
		},
		{
			ID:               "illiquid_reject",
			Title:            "Reject apparent edge when liquidity and spread are unusable",
			ExpectedTrade:    false,
			ExpectedSide:     "none",
			ExpectedDir:      "none",
			ExpectedCategory: "prediction_market",
			MaxDollars:       1,
			Market: strings.TrimSpace(`Market:
Ticker: EXAMPLE-ILLIQUID-26
Question: Will a niche sports outcome happen tonight?
Yes bid/ask: 18/41 cents
No bid/ask: 59/82 cents
Volume: 11 contracts
Close: 6 hours from now
Evidence supplied: social media rumor only, no primary-source confirmation.
Execution note: quoted spread is too wide to enter under a $50 bankroll.`),
		},
	}
}

func loadCatalog(ctx context.Context, baseURL, apiKey string) map[string]catalogModel {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/models", nil)
	if err != nil {
		return nil
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil
	}

	var decoded catalogResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil
	}
	out := make(map[string]catalogModel, len(decoded.Data))
	for _, model := range decoded.Data {
		out[model.ID] = model
	}
	return out
}

func validJSONObject(content string) bool {
	content = strings.TrimSpace(content)
	if content == "" {
		return false
	}

	var obj map[string]any
	if json.Unmarshal([]byte(content), &obj) == nil {
		return true
	}

	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")
	if start < 0 || end <= start {
		return false
	}
	return json.Unmarshal([]byte(content[start:end+1]), &obj) == nil
}

func scoreResult(result *benchResult, benchCase benchCase) {
	decision, ok := parseDecision(result.Mode, result.Response)
	result.StructuredOK = ok
	if !ok {
		return
	}
	result.DecisionOK = decision.Tradeable == benchCase.ExpectedTrade
	if result.Mode == "json" {
		result.SideOK = strings.EqualFold(decision.Side, benchCase.ExpectedSide)
	} else {
		result.SideOK = strings.EqualFold(decision.Direction, benchCase.ExpectedDir)
	}
	result.CategoryOK = strings.EqualFold(decision.Category, benchCase.ExpectedCategory)
	result.RiskOK = riskMatches(decision, benchCase)

	score := 40
	if result.DecisionOK {
		score += 30
	}
	if result.SideOK {
		score += 15
	}
	if result.CategoryOK {
		score += 10
	}
	if result.RiskOK {
		score += 5
	}
	result.Score = score
}

func parseDecision(mode, content string) (parsedDecision, bool) {
	if mode == "thought" {
		return parseThoughtDecision(content)
	}
	return parseJSONDecision(content)
}

func parseJSONDecision(content string) (parsedDecision, bool) {
	obj, ok := extractJSONObject(content)
	if !ok {
		return parsedDecision{}, false
	}
	tradeable, ok := boolValue(obj["tradeable"])
	if !ok {
		return parsedDecision{}, false
	}
	side, ok := requiredDecisionString(obj, "side")
	if !ok {
		return parsedDecision{}, false
	}
	category, ok := requiredDecisionString(obj, "category")
	if !ok {
		return parsedDecision{}, false
	}
	maxDollars, ok := numericField(obj, "max_dollars")
	if !ok {
		return parsedDecision{}, false
	}
	if _, ok := numericField(obj, "limit_price_cents"); !ok {
		return parsedDecision{}, false
	}
	if _, ok := numericField(obj, "conviction"); !ok {
		return parsedDecision{}, false
	}
	rationale, ok := requiredDecisionString(obj, "rationale")
	if !ok || rationale == "none" {
		return parsedDecision{}, false
	}
	return parsedDecision{
		Tradeable:  tradeable,
		Side:       side,
		Direction:  normalizeDecisionValue(stringValue(obj["direction"])),
		Category:   category,
		MaxDollars: maxDollars,
	}, true
}

func parseThoughtDecision(content string) (parsedDecision, bool) {
	upper := strings.ToUpper(content)
	start := strings.Index(upper, "FINAL_DECISION")
	end := strings.LastIndex(upper, "END_FINAL_DECISION")
	if start < 0 || end <= start {
		return parsedDecision{}, false
	}
	fields := map[string]string{}
	for _, line := range strings.Split(content[start:end], "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		fields[strings.ToLower(strings.TrimSpace(key))] = strings.TrimSpace(value)
	}
	for _, key := range []string{"tradeable", "score", "instruments", "direction", "urgency", "category", "reasoning", "max_dollars"} {
		if strings.TrimSpace(fields[key]) == "" {
			return parsedDecision{}, false
		}
	}
	rawTradeable, ok := fields["tradeable"]
	if !ok {
		return parsedDecision{}, false
	}
	tradeable, ok := parseBool(rawTradeable)
	if !ok {
		return parsedDecision{}, false
	}
	score, ok := parseNumber(fields["score"])
	if !ok {
		return parsedDecision{}, false
	}
	urgency, ok := parseNumber(fields["urgency"])
	if !ok {
		return parsedDecision{}, false
	}
	maxDollars, ok := parseNumber(fields["max_dollars"])
	if !ok {
		return parsedDecision{}, false
	}
	return parsedDecision{
		Tradeable:   tradeable,
		Direction:   normalizeDecisionValue(fields["direction"]),
		Category:    normalizeDecisionValue(fields["category"]),
		MaxDollars:  maxDollars,
		Score:       score,
		Instruments: strings.TrimSpace(fields["instruments"]),
		Urgency:     urgency,
		Reasoning:   strings.TrimSpace(fields["reasoning"]),
	}, true
}

func extractJSONObject(content string) (map[string]any, bool) {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil, false
	}
	var obj map[string]any
	if json.Unmarshal([]byte(content), &obj) == nil {
		return obj, true
	}
	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")
	if start < 0 || end <= start {
		return nil, false
	}
	if json.Unmarshal([]byte(content[start:end+1]), &obj) != nil {
		return nil, false
	}
	return obj, true
}

func boolValue(value any) (bool, bool) {
	switch typed := value.(type) {
	case bool:
		return typed, true
	case string:
		return parseBool(typed)
	default:
		return false, false
	}
}

func parseBool(raw string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "true", "yes", "trade", "1":
		return true, true
	case "false", "no", "none", "reject", "0":
		return false, true
	default:
		return false, false
	}
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}

func requiredDecisionString(obj map[string]any, key string) (string, bool) {
	value, ok := obj[key]
	if !ok {
		return "", false
	}
	normalized := normalizeDecisionValue(stringValue(value))
	if strings.TrimSpace(normalized) == "" {
		return "", false
	}
	return normalized, true
}

func numericField(obj map[string]any, key string) (float64, bool) {
	value, ok := obj[key]
	if !ok {
		return 0, false
	}
	return parseNumericAny(value)
}

func parseNumericAny(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case json.Number:
		parsed, _ := typed.Float64()
		return parsed, true
	case string:
		return parseNumber(typed)
	}
	return 0, false
}

func parseNumber(raw string) (float64, bool) {
	var parsed float64
	if _, err := fmt.Sscanf(strings.TrimSpace(raw), "%f", &parsed); err == nil {
		return parsed, true
	}
	return 0, false
}

func normalizeDecisionValue(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	raw = strings.Trim(raw, "\"'` .,;")
	switch raw {
	case "", "n/a", "na", "null":
		return "none"
	default:
		return raw
	}
}

func riskMatches(decision parsedDecision, benchCase benchCase) bool {
	if !benchCase.ExpectedTrade {
		return decision.MaxDollars <= 0
	}
	if decision.MaxDollars <= 0 {
		return false
	}
	return decision.MaxDollars <= benchCase.MaxDollars
}

func validTerminalDecision(content string) bool {
	upper := strings.ToUpper(content)
	start := strings.Index(upper, "FINAL_DECISION")
	end := strings.LastIndex(upper, "END_FINAL_DECISION")
	if start < 0 || end <= start {
		return false
	}
	block := strings.ToLower(content[start:end])
	for _, required := range []string{"tradeable:", "score:", "instruments:", "direction:", "urgency:", "category:", "reasoning:", "max_dollars:"} {
		if !strings.Contains(block, required) {
			return false
		}
	}
	return true
}

func recordSummary(summary map[summaryKey]*summaryBucket, result benchResult) {
	key := summaryKey{Mode: result.Mode, Model: result.Model}
	bucket := summary[key]
	if bucket == nil {
		bucket = &summaryBucket{Mode: result.Mode, Model: result.Model}
		summary[key] = bucket
	}
	bucket.Cases++
	if result.StructuredOK {
		bucket.StructuredOK++
	}
	if result.DecisionOK {
		bucket.DecisionOK++
	}
	if result.SideOK {
		bucket.SideOK++
	}
	if result.CategoryOK {
		bucket.CategoryOK++
	}
	if result.RiskOK {
		bucket.RiskOK++
	}
	if result.Err != nil {
		bucket.Failures++
	}
	bucket.ScoreTotal += result.Score
	bucket.LatencyTotal += result.Latency
	bucket.InputTokens += result.InputTokens
	bucket.OutputTokens += result.OutputTokens
}

func printSummary(summary map[summaryKey]*summaryBucket) {
	fmt.Println("# summary")
	fmt.Println("mode\tmodel\tcases\tstructured_rate\tdecision_accuracy\tside_accuracy\tcategory_accuracy\trisk_accuracy\tavg_score\tavg_latency_ms\tinput_tokens\toutput_tokens\tfailures")
	for _, bucket := range orderedSummary(summary) {
		cases := float64(bucket.Cases)
		if cases == 0 {
			continue
		}
		fmt.Printf("%s\t%s\t%d\t%.3f\t%.3f\t%.3f\t%.3f\t%.3f\t%.1f\t%d\t%d\t%d\t%d\n",
			bucket.Mode,
			bucket.Model,
			bucket.Cases,
			float64(bucket.StructuredOK)/cases,
			float64(bucket.DecisionOK)/cases,
			float64(bucket.SideOK)/cases,
			float64(bucket.CategoryOK)/cases,
			float64(bucket.RiskOK)/cases,
			float64(bucket.ScoreTotal)/cases,
			(bucket.LatencyTotal / time.Duration(bucket.Cases)).Milliseconds(),
			bucket.InputTokens,
			bucket.OutputTokens,
			bucket.Failures,
		)
	}
}

func orderedSummary(summary map[summaryKey]*summaryBucket) []*summaryBucket {
	out := make([]*summaryBucket, 0, len(summary))
	for _, bucket := range summary {
		out = append(out, bucket)
	}
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].Mode < out[i].Mode || (out[j].Mode == out[i].Mode && out[j].Model < out[i].Model) {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

func preview(result benchResult) string {
	var text string
	if result.Err != nil {
		text = "ERROR: " + result.Err.Error()
	} else {
		text = result.Response
	}
	text = strings.ReplaceAll(text, "\n", " ")
	text = strings.ReplaceAll(text, "\t", " ")
	if len(text) > 220 {
		return text[:217] + "..."
	}
	return text
}

func splitCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		model := strings.TrimSpace(part)
		if model == "" {
			continue
		}
		if _, ok := seen[model]; ok {
			continue
		}
		seen[model] = struct{}{}
		out = append(out, model)
	}
	return out
}

func dedupe(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		key := strings.ToLower(strings.TrimSpace(value))
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, strings.TrimSpace(value))
	}
	return out
}

func benchmarkModes(raw string) []string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "both", "":
		return []string{"json", "thought"}
	case "json", "structured":
		return []string{"json"}
	case "thought", "thinking", "reasoning":
		return []string{"thought"}
	default:
		return nil
	}
}

func pricePerMillion(raw string) string {
	if raw == "" {
		return ""
	}
	var value float64
	if _, err := fmt.Sscanf(raw, "%f", &value); err != nil {
		return raw
	}
	return fmt.Sprintf("%.4f", value*1_000_000)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
