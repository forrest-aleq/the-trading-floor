package llm

import (
	"context"
)

// Tier determines which model handles the request
type Tier int

const (
	TierSpeed    Tier = iota // Qwen 7B — translation, filtering, scanning
	TierAnalysis             // Qwen 72B — research, synthesis, thesis formation
	TierCritical             // Claude Sonnet — prosecution, council, critical decisions
)

// Role in a conversation
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// Message is a single chat message
type Message struct {
	Role    Role   `json:"role"`
	Content string `json:"content"`
}

// Request to an LLM
type Request struct {
	Messages    []Message         `json:"messages"`
	Model       string            `json:"model,omitempty"` // Override model selection
	Temperature float64           `json:"temperature,omitempty"`
	MaxTokens   int               `json:"max_tokens,omitempty"`
	JSONMode    bool              `json:"json_mode,omitempty"`
	Tier        Tier              `json:"-"` // Used by router, not sent to API
}

// Response from an LLM
type Response struct {
	Content      string `json:"content"`
	Model        string `json:"model"`
	InputTokens  int    `json:"input_tokens"`
	OutputTokens int    `json:"output_tokens"`
}

// Client is the interface for all LLM providers
type Client interface {
	Complete(ctx context.Context, req Request) (*Response, error)
}

// Router sends requests to the right model based on tier
type Router struct {
	speed    Client // Qwen 7B or fast model
	analysis Client // Qwen 72B or mid-tier model
	critical Client // Claude Sonnet
}

func NewRouter(speed, analysis, critical Client) *Router {
	return &Router{
		speed:    speed,
		analysis: analysis,
		critical: critical,
	}
}

func (r *Router) Complete(ctx context.Context, req Request) (*Response, error) {
	switch req.Tier {
	case TierCritical:
		return r.critical.Complete(ctx, req)
	case TierAnalysis:
		return r.analysis.Complete(ctx, req)
	default:
		return r.speed.Complete(ctx, req)
	}
}

// Convenience for single-prompt calls
func (r *Router) Ask(ctx context.Context, tier Tier, system, prompt string) (string, error) {
	req := Request{
		Messages: []Message{
			{Role: RoleSystem, Content: system},
			{Role: RoleUser, Content: prompt},
		},
		Tier:        tier,
		MaxTokens:   4096,
		Temperature: 0.7,
	}
	resp, err := r.Complete(ctx, req)
	if err != nil {
		return "", err
	}
	return resp.Content, nil
}

// AskJSON is like Ask but requests JSON output
func (r *Router) AskJSON(ctx context.Context, tier Tier, system, prompt string) (string, error) {
	req := Request{
		Messages: []Message{
			{Role: RoleSystem, Content: system},
			{Role: RoleUser, Content: prompt},
		},
		Tier:        tier,
		MaxTokens:   4096,
		Temperature: 0.3,
		JSONMode:    true,
	}
	resp, err := r.Complete(ctx, req)
	if err != nil {
		return "", err
	}
	return resp.Content, nil
}
