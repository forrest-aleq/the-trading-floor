package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// OpenRouter client — OpenAI-compatible API that routes to any model.
// Also works with Claude Foundry by changing the base URL.
type OpenRouterClient struct {
	apiKey  string
	baseURL string
	model   string
	http    *http.Client
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
	}
}

type orRequest struct {
	Model       string      `json:"model"`
	Messages    []orMessage `json:"messages"`
	Temperature float64     `json:"temperature,omitempty"`
	MaxTokens   int         `json:"max_tokens,omitempty"`
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
	if req.JSONMode {
		orReq.ResponseFormat = &orResponseFormat{Type: "json_object"}
	}

	body, err := json.Marshal(orReq)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("api error (status %d): %s", resp.StatusCode, string(respBody))
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

// DefaultRouter creates a router using OpenRouter for all tiers.
// In production, speed/analysis would be self-hosted Qwen.
func DefaultRouter() *Router {
	apiKey := os.Getenv("OPENROUTER_API_KEY")
	baseURL := os.Getenv("LLM_BASE_URL")
	if baseURL == "" {
		baseURL = "https://openrouter.ai/api/v1"
	}

	speedModel := os.Getenv("LLM_MODEL_SPEED")
	if speedModel == "" {
		speedModel = "qwen/qwen3.5-7b"
	}
	analysisModel := os.Getenv("LLM_MODEL_ANALYSIS")
	if analysisModel == "" {
		analysisModel = "qwen/qwen3.5-72b"
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
