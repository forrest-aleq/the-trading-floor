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
	apiKey  string
	baseURL string
	model   string
	http    *http.Client
	limiter chan struct{}
}

type OpenRouterConfig struct {
	APIKey  string // OPENROUTER_API_KEY or ANTHROPIC_API_KEY
	BaseURL string // https://openrouter.ai/api/v1 or foundry URL
	Model   string // e.g. "anthropic/claude-sonnet-4-20250514", "qwen/qwen3.5-72b"
}

func NewOpenRouterClient(cfg OpenRouterConfig) *OpenRouterClient {
	if cfg.APIKey == "" {
		cfg.APIKey = os.Getenv("OPENROUTER_API_KEY")
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://openrouter.ai/api/v1"
	}

	return &OpenRouterClient{
		apiKey:  cfg.APIKey,
		baseURL: cfg.BaseURL,
		model:   cfg.Model,
		http: &http.Client{
			Timeout: 120 * time.Second,
		},
		limiter: makeLimiter(cfg.BaseURL),
	}
}

type orRequest struct {
	Model          string            `json:"model"`
	Messages       []orMessage       `json:"messages"`
	Temperature    float64           `json:"temperature,omitempty"`
	MaxTokens      int               `json:"max_tokens,omitempty"`
	ResponseFormat *orResponseFormat `json:"response_format,omitempty"`
}

type orMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type orResponseFormat struct {
	Type string `json:"type"` // "json_object"
}

type orResponse struct {
	ID      string `json:"id"`
	Choices []struct {
		Message struct {
			Content string `json:"content"`
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

	orReq := orRequest{
		Model:       model,
		Messages:    messages,
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
	}
	if req.JSONMode && c.supportsStructuredJSON() {
		orReq.ResponseFormat = &orResponseFormat{Type: "json_object"}
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

	return &Response{
		Content:      orResp.Choices[0].Message.Content,
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

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, 0, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode, fmt.Errorf("api error (status %d): %s", resp.StatusCode, string(respBody))
	}

	return respBody, resp.StatusCode, nil
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

func makeLimiter(baseURL string) chan struct{} {
	maxConcurrent := 0
	if raw := strings.TrimSpace(os.Getenv("LLM_MAX_CONCURRENCY")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			maxConcurrent = parsed
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

// DefaultRouter creates a router using OpenRouter-compatible clients for all tiers.
// The default speed/analysis ids match the local LM Studio setup used for this repo.
func DefaultRouter() *Router {
	apiKey := os.Getenv("OPENROUTER_API_KEY")
	baseURL := os.Getenv("LLM_BASE_URL")
	if baseURL == "" {
		baseURL = "https://openrouter.ai/api/v1"
	}

	speedModel := os.Getenv("LLM_MODEL_SPEED")
	if speedModel == "" {
		speedModel = "qwen/qwen3.5-9b"
	}
	analysisModel := os.Getenv("LLM_MODEL_ANALYSIS")
	if analysisModel == "" {
		analysisModel = "qwen/qwen3.5-35b-a3b"
	}
	criticalModel := os.Getenv("LLM_MODEL_CRITICAL")
	if criticalModel == "" {
		criticalModel = "anthropic/claude-sonnet-4-20250514"
	}

	speed := NewOpenRouterClient(OpenRouterConfig{
		APIKey:  apiKey,
		BaseURL: baseURL,
		Model:   speedModel,
	})

	analysis := NewOpenRouterClient(OpenRouterConfig{
		APIKey:  apiKey,
		BaseURL: baseURL,
		Model:   analysisModel,
	})

	critical := NewOpenRouterClient(OpenRouterConfig{
		APIKey:  apiKey,
		BaseURL: baseURL,
		Model:   criticalModel,
	})

	return NewRouter(speed, analysis, critical)
}
