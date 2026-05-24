package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// OpenRouter client — OpenAI-compatible API that routes to any model.
// Also works with Claude Foundry by changing the base URL.
type OpenRouterClient struct {
	apiKey   string
	baseURL  string
	model    string
	provider *orProviderRouting
	http     *http.Client
	limiter  chan struct{}
}

type OpenRouterConfig struct {
	APIKey         string // OPENROUTER_API_KEY or ANTHROPIC_API_KEY
	BaseURL        string // https://openrouter.ai/api/v1 or foundry URL
	Model          string // e.g. "deepseek/deepseek-v4-flash", "qwen/qwen3.6-35b-a3b"
	MaxConcurrency int
	Provider       *ProviderRouting
	EnvPrefix      string
}

type ProviderRouting struct {
	Order             []string
	Only              []string
	Ignore            []string
	Sort              string
	AllowFallbacks    *bool
	RequireParameters *bool
	DataCollection    string
	ZDR               *bool
}

func NewOpenRouterClient(cfg OpenRouterConfig) *OpenRouterClient {
	if cfg.APIKey == "" {
		cfg.APIKey = os.Getenv("OPENROUTER_API_KEY")
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://openrouter.ai/api/v1"
	}

	provider := cfg.Provider
	if provider == nil && cfg.EnvPrefix != "" {
		provider = providerRoutingFromEnv(cfg.EnvPrefix)
	}
	if provider == nil {
		provider = providerRoutingFromEnv("LLM")
	}

	return &OpenRouterClient{
		apiKey:   cfg.APIKey,
		baseURL:  cfg.BaseURL,
		model:    cfg.Model,
		provider: provider.toOpenRouter(),
		http: &http.Client{
			Timeout: 120 * time.Second,
		},
		limiter: makeLimiter(cfg.BaseURL, cfg.MaxConcurrency),
	}
}

type orRequest struct {
	Model          string             `json:"model"`
	Messages       []orMessage        `json:"messages"`
	Temperature    float64            `json:"temperature,omitempty"`
	MaxTokens      int                `json:"max_tokens,omitempty"`
	ResponseFormat *orResponseFormat  `json:"response_format,omitempty"`
	Reasoning      *orReasoning       `json:"reasoning,omitempty"`
	Provider       *orProviderRouting `json:"provider,omitempty"`
}

type orMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type orResponseFormat struct {
	Type string `json:"type"` // "json_object"
}

type orReasoning struct {
	Exclude   bool   `json:"exclude,omitempty"`
	Effort    string `json:"effort,omitempty"`
	MaxTokens int    `json:"max_tokens,omitempty"`
	Enabled   bool   `json:"enabled,omitempty"`
}

type orProviderRouting struct {
	Order             []string `json:"order,omitempty"`
	Only              []string `json:"only,omitempty"`
	Ignore            []string `json:"ignore,omitempty"`
	Sort              string   `json:"sort,omitempty"`
	AllowFallbacks    *bool    `json:"allow_fallbacks,omitempty"`
	RequireParameters *bool    `json:"require_parameters,omitempty"`
	DataCollection    string   `json:"data_collection,omitempty"`
	ZDR               *bool    `json:"zdr,omitempty"`
}

type orResponse struct {
	ID      string `json:"id"`
	Choices []struct {
		Message struct {
			Content   string `json:"content"`
			Reasoning string `json:"reasoning"`
		} `json:"message"`
	} `json:"choices"`
	Model string `json:"model"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Code    int    `json:"code"`
	} `json:"error"`
}

func (c *OpenRouterClient) Complete(ctx context.Context, req Request) (*Response, error) {
	model := c.model
	if req.Model != "" {
		model = req.Model
	}

	messages := make([]orMessage, len(req.Messages))
	for i, m := range req.Messages {
		messages[i] = orMessage{Role: string(m.Role), Content: m.Content}
	}
	messages = applyLocalJSONControls(c.baseURL, model, req.JSONMode, messages)

	orReq := orRequest{
		Model:       model,
		Messages:    messages,
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
		Provider:    c.provider,
	}
	if req.JSONMode && c.supportsStructuredJSON() {
		orReq.ResponseFormat = &orResponseFormat{Type: "json_object"}
	}
	if c.supportsReasoningControls() {
		orReq.Reasoning = openRouterReasoningConfig(req.JSONMode)
	}

	body, err := json.Marshal(orReq)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	if c.limiter != nil {
		select {
		case c.limiter <- struct{}{}:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		defer func() { <-c.limiter }()
	}

	respBody, err := c.doChatCompletion(ctx, body)
	if err != nil {
		return nil, err
	}

	var orResp orResponse
	if err := json.Unmarshal(respBody, &orResp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	if orResp.Error != nil {
		return nil, fmt.Errorf("api error: %s", orResp.Error.Message)
	}

	if len(orResp.Choices) == 0 {
		return nil, fmt.Errorf("no choices in response")
	}

	content := normalizeChoiceContent(orResp.Choices[0].Message.Content, orResp.Choices[0].Message.Reasoning)

	return &Response{
		Content:      content,
		Model:        orResp.Model,
		InputTokens:  orResp.Usage.PromptTokens,
		OutputTokens: orResp.Usage.CompletionTokens,
	}, nil
}

func (c *OpenRouterClient) doChatCompletion(ctx context.Context, body []byte) ([]byte, error) {
	attempts := 1
	if isLocalLLM(c.baseURL) {
		attempts = 3
	}

	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		respBody, status, err := c.doChatCompletionOnce(ctx, body)
		if err == nil {
			return respBody, nil
		}

		lastErr = err
		if !shouldRetryLocalLLM(c.baseURL, status, attempt, attempts) {
			return nil, err
		}

		delay := retryDelay(attempt)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}

	return nil, lastErr
}

func (c *OpenRouterClient) doChatCompletionOnce(ctx context.Context, body []byte) ([]byte, int, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, 0, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.httpClientFor(ctx).Do(httpReq)
	if err != nil {
		return nil, 0, fmt.Errorf("http request: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode, fmt.Errorf("api error (status %d): %s", resp.StatusCode, string(respBody))
	}

	return respBody, resp.StatusCode, nil
}

func (c *OpenRouterClient) httpClientFor(ctx context.Context) *http.Client {
	if c.http == nil {
		return http.DefaultClient
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		return c.http
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return c.http
	}

	timeout := c.http.Timeout
	if timeout <= 0 || remaining < timeout {
		timeout = remaining
	}
	if timeout == c.http.Timeout {
		return c.http
	}

	clone := *c.http
	clone.Timeout = timeout
	return &clone
}

func (c *OpenRouterClient) supportsStructuredJSON() bool {
	u, err := url.Parse(c.baseURL)
	if err != nil {
		return true
	}

	host := strings.ToLower(u.Hostname())
	switch host {
	case "127.0.0.1", "localhost", "::1":
		return false
	default:
		return true
	}
}

func (c *OpenRouterClient) supportsReasoningControls() bool {
	u, err := url.Parse(c.baseURL)
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(u.Hostname()), "openrouter.ai")
}

func openRouterReasoningConfig(jsonMode bool) *orReasoning {
	if jsonMode {
		effort := firstNonEmptyEnv("LLM_JSON_REASONING_EFFORT", "low")
		switch strings.ToLower(strings.TrimSpace(effort)) {
		case "off", "none", "false", "disabled", "disable":
			return nil
		}
		return &orReasoning{
			Exclude: true,
			Effort:  effort,
		}
	}
	if !readBoolEnvDefault("LLM_REASONING_ENABLED", true) {
		return nil
	}

	reasoning := &orReasoning{
		Enabled: true,
	}
	if maxTokens := readPositiveIntEnv("LLM_REASONING_MAX_TOKENS"); maxTokens > 0 {
		reasoning.MaxTokens = maxTokens
		return reasoning
	}
	reasoning.Effort = firstNonEmptyEnv("LLM_REASONING_EFFORT", "medium")
	return reasoning
}

func applyLocalJSONControls(baseURL, model string, jsonMode bool, messages []orMessage) []orMessage {
	if !jsonMode || !isLocalLLM(baseURL) || len(messages) == 0 {
		return messages
	}

	for i := range messages {
		if strings.Contains(messages[i].Content, "/no_think") || strings.Contains(messages[i].Content, "/think") {
			return messages
		}
	}

	preferred := 0
	for i, message := range messages {
		if message.Role == string(RoleSystem) {
			preferred = i
			break
		}
	}

	messages[preferred].Content = "/no_think\n" + messages[preferred].Content
	return messages
}

func makeLimiter(baseURL string, configured int) chan struct{} {
	maxConcurrent := configured
	if maxConcurrent <= 0 {
		if raw := strings.TrimSpace(os.Getenv("LLM_MAX_CONCURRENCY")); raw != "" {
			if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
				maxConcurrent = parsed
			}
		}
	}
	if maxConcurrent == 0 && isLocalLLM(baseURL) {
		maxConcurrent = 2
	}
	if maxConcurrent <= 0 {
		return nil
	}
	return make(chan struct{}, maxConcurrent)
}

func isLocalLLM(baseURL string) bool {
	u, err := url.Parse(baseURL)
	if err != nil {
		return false
	}

	host := strings.ToLower(u.Hostname())
	return host == "127.0.0.1" || host == "localhost" || host == "::1"
}

func shouldRetryLocalLLM(baseURL string, statusCode, attempt, attempts int) bool {
	if !isLocalLLM(baseURL) {
		return false
	}
	if attempt >= attempts-1 {
		return false
	}
	switch statusCode {
	case http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func retryDelay(attempt int) time.Duration {
	switch attempt {
	case 0:
		return 200 * time.Millisecond
	case 1:
		return 500 * time.Millisecond
	default:
		return time.Second
	}
}

func normalizeChoiceContent(content, reasoning string) string {
	content = strings.TrimSpace(content)
	reasoning = strings.TrimSpace(reasoning)

	switch {
	case reasoning == "":
		return content
	case content == "":
		return "<think>\n" + reasoning + "\n</think>"
	default:
		return "<think>\n" + reasoning + "\n</think>\n\n" + content
	}
}

// DefaultRouter creates a router using OpenRouter-compatible clients for all tiers.
// The default cloud ids favor current OpenRouter open-weight model families.
func DefaultRouter() *Router {
	apiKey := os.Getenv("OPENROUTER_API_KEY")
	baseURL := os.Getenv("LLM_BASE_URL")
	if baseURL == "" {
		baseURL = "https://openrouter.ai/api/v1"
	}

	speedModel := os.Getenv("LLM_MODEL_SPEED")
	if speedModel == "" {
		if isLocalLLM(baseURL) {
			speedModel = "qwen3:8b"
		} else {
			speedModel = "openai/gpt-oss-120b"
		}
	}
	analysisModel := os.Getenv("LLM_MODEL_ANALYSIS")
	if analysisModel == "" {
		if isLocalLLM(baseURL) {
			analysisModel = "qwen3:30b"
		} else {
			analysisModel = "openai/gpt-oss-120b"
		}
	}
	criticalModel := os.Getenv("LLM_MODEL_CRITICAL")
	if criticalModel == "" {
		if isLocalLLM(baseURL) {
			criticalModel = analysisModel
		} else {
			criticalModel = "deepseek/deepseek-v4-pro"
		}
	}
	speedConcurrency := readConcurrencyEnv("LLM_SPEED_MAX_CONCURRENCY")
	analysisConcurrency := readConcurrencyEnv("LLM_ANALYSIS_MAX_CONCURRENCY")
	criticalConcurrency := readConcurrencyEnv("LLM_CRITICAL_MAX_CONCURRENCY")

	speed := NewOpenRouterClient(OpenRouterConfig{
		APIKey:         apiKey,
		BaseURL:        baseURL,
		Model:          speedModel,
		MaxConcurrency: speedConcurrency,
		EnvPrefix:      "LLM_SPEED",
	})

	analysis := NewOpenRouterClient(OpenRouterConfig{
		APIKey:         apiKey,
		BaseURL:        baseURL,
		Model:          analysisModel,
		MaxConcurrency: analysisConcurrency,
		EnvPrefix:      "LLM_ANALYSIS",
	})

	critical := NewOpenRouterClient(OpenRouterConfig{
		APIKey:         apiKey,
		BaseURL:        baseURL,
		Model:          criticalModel,
		MaxConcurrency: criticalConcurrency,
		EnvPrefix:      "LLM_CRITICAL",
	})

	return NewRouter(speed, analysis, critical)
}

func readConcurrencyEnv(name string) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return 0
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed <= 0 {
		return 0
	}
	return parsed
}

func providerRoutingFromEnv(prefix string) *ProviderRouting {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return nil
	}
	routing := &ProviderRouting{
		Order:          readCSVEnv(prefix + "_PROVIDER_ORDER"),
		Only:           readCSVEnv(prefix + "_PROVIDER_ONLY"),
		Ignore:         readCSVEnv(prefix + "_PROVIDER_IGNORE"),
		Sort:           strings.TrimSpace(os.Getenv(prefix + "_PROVIDER_SORT")),
		DataCollection: strings.TrimSpace(os.Getenv(prefix + "_PROVIDER_DATA_COLLECTION")),
	}
	if value, ok := readOptionalBoolEnv(prefix + "_PROVIDER_ALLOW_FALLBACKS"); ok {
		routing.AllowFallbacks = &value
	}
	if value, ok := readOptionalBoolEnv(prefix + "_PROVIDER_REQUIRE_PARAMETERS"); ok {
		routing.RequireParameters = &value
	}
	if value, ok := readOptionalBoolEnv(prefix + "_PROVIDER_ZDR"); ok {
		routing.ZDR = &value
	}
	if routing.empty() {
		return nil
	}
	return routing
}

func (p *ProviderRouting) toOpenRouter() *orProviderRouting {
	if p == nil || p.empty() {
		return nil
	}
	return &orProviderRouting{
		Order:             p.Order,
		Only:              p.Only,
		Ignore:            p.Ignore,
		Sort:              p.Sort,
		AllowFallbacks:    p.AllowFallbacks,
		RequireParameters: p.RequireParameters,
		DataCollection:    p.DataCollection,
		ZDR:               p.ZDR,
	}
}

func (p *ProviderRouting) empty() bool {
	if p == nil {
		return true
	}
	return len(p.Order) == 0 &&
		len(p.Only) == 0 &&
		len(p.Ignore) == 0 &&
		p.Sort == "" &&
		p.AllowFallbacks == nil &&
		p.RequireParameters == nil &&
		p.DataCollection == "" &&
		p.ZDR == nil
}

func readCSVEnv(name string) []string {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		value := strings.TrimSpace(part)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func readOptionalBoolEnv(name string) (bool, bool) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return false, false
	}
	parsed, err := strconv.ParseBool(raw)
	if err != nil {
		return false, false
	}
	return parsed, true
}

func readPositiveIntEnv(name string) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return 0
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed <= 0 {
		return 0
	}
	return parsed
}

func readBoolEnvDefault(name string, fallback bool) bool {
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

func firstNonEmptyEnv(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
