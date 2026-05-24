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

const defaultModels = "openai/gpt-oss-120b,qwen/qwen3-235b-a22b-2507,qwen/qwen3.6-35b-a3b,qwen/qwen3.5-397b-a17b,deepseek/deepseek-v4-flash,google/gemma-4-26b-a4b-it"
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
	Model        string
	Name         string
	Catalog      bool
	StructuredOK bool
	Latency      time.Duration
	InputTokens  int
	OutputTokens int
	Response     string
	Err          error
}

func main() {
	_ = godotenv.Load()

	modelsFlag := flag.String("models", firstNonEmpty(os.Getenv("MODEL_HARNESS_MODELS"), defaultModels), "comma-separated OpenRouter model ids")
	comparatorsFlag := flag.String("comparators", "", "optional comma-separated closed/proprietary comparator model ids")
	includeClosedFlag := flag.Bool("include-closed", false, "include the default closed comparator set")
	modeFlag := flag.String("mode", firstNonEmpty(os.Getenv("MODEL_HARNESS_MODE"), "both"), "benchmark mode: json, thought, or both")
	repeatFlag := flag.Int("repeat", 1, "number of times to call each model")
	timeoutFlag := flag.Duration("timeout", 45*time.Second, "per-model timeout")
	maxTokensFlag := flag.Int("max-tokens", 220, "max completion tokens per model")
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

	fmt.Println("run\tmode\tmodel\tname\tcatalog\tcontext\tprompt_per_m\tcompletion_per_m\tstructured\tlatency_ms\tinput_tokens\toutput_tokens\tresponse_or_error")
	successes := 0
	for run := 1; run <= *repeatFlag; run++ {
		for _, model := range models {
			for _, mode := range modes {
				result := runBench(model, mode, baseURL, apiKey, catalog, *timeoutFlag, *maxTokensFlag)
				if result.Err == nil && result.StructuredOK {
					successes++
				}
				meta := catalogModel{}
				if catalog != nil {
					meta = catalog[model]
				}
				fmt.Printf("%d\t%s\t%s\t%s\t%t\t%d\t%s\t%s\t%t\t%d\t%d\t%d\t%s\n",
					run,
					result.Mode,
					result.Model,
					result.Name,
					result.Catalog,
					meta.ContextLength,
					pricePerMillion(meta.Pricing.Prompt),
					pricePerMillion(meta.Pricing.Completion),
					result.StructuredOK,
					result.Latency.Milliseconds(),
					result.InputTokens,
					result.OutputTokens,
					preview(result),
				)
			}
		}
	}

	if successes == 0 {
		os.Exit(1)
	}
}

func runBench(model, mode, baseURL, apiKey string, catalog map[string]catalogModel, timeout time.Duration, maxTokens int) benchResult {
	result := benchResult{Mode: mode, Model: model}
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
		Messages:    benchMessages(mode),
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
	if mode == "thought" {
		result.StructuredOK = validTerminalDecision(result.Response)
	} else {
		result.StructuredOK = validJSONObject(result.Response)
	}
	return result
}

func benchMessages(mode string) []llm.Message {
	market := strings.TrimSpace(`Evaluate this prediction market for a $50 bankroll.

Market:
Ticker: EXAMPLE-TEST-26
Title: Will a major US economic release beat consensus this week?
Yes bid/ask: 44/47 cents
No bid/ask: 52/55 cents
Volume: 62,500 contracts
Close: 36 hours from now
Bot constraints: max order $1, min conviction 0.70, do not trade without explicit edge.

Choose tradeable=false unless the provided facts justify positive expected value after spread.`)
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
reasoning: short explanation
END_FINAL_DECISION`),
			},
			{Role: llm.RoleUser, Content: market},
		}
	}
	return []llm.Message{
		{
			Role:    llm.RoleSystem,
			Content: "You are a Kalshi trading scanner. Return compact JSON only. Do not include markdown.",
		},
		{
			Role:    llm.RoleUser,
			Content: "Return JSON with fields: tradeable boolean, side one of \"yes\"|\"no\"|\"none\", limit_price_cents integer, max_dollars number, conviction number, rationale string.\n\n" + market,
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

func validTerminalDecision(content string) bool {
	upper := strings.ToUpper(content)
	start := strings.Index(upper, "FINAL_DECISION")
	end := strings.LastIndex(upper, "END_FINAL_DECISION")
	if start < 0 || end <= start {
		return false
	}
	block := strings.ToLower(content[start:end])
	for _, required := range []string{"tradeable:", "score:", "instruments:", "direction:", "urgency:", "category:", "reasoning:"} {
		if !strings.Contains(block, required) {
			return false
		}
	}
	return true
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
