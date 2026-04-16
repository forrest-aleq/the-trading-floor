package research

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/hnic/trading-floor/internal/llm"
)

type structuredResponseMode string

const (
	structuredResponseModeJSON    structuredResponseMode = "structured_json"
	structuredResponseModeThought structuredResponseMode = "thought_block"
)

const (
	terminalJSONStart = "FINAL_JSON"
	terminalJSONEnd   = "END_FINAL_JSON"
)

var (
	structuredThoughtTimeout  = readStructuredDurationEnv("STRUCTURED_THOUGHT_TIMEOUT", 18*time.Second)
	structuredJSONRetryTimout = readStructuredDurationEnv("STRUCTURED_JSON_RETRY_TIMEOUT", 10*time.Second)
	structuredJSONRetryTokens = readStructuredIntEnv("STRUCTURED_JSON_RETRY_MAX_TOKENS", 640)
)

func detectStructuredResponseMode(envName, model string) structuredResponseMode {
	override := strings.ToLower(strings.TrimSpace(envName))
	switch override {
	case "json", "structured_json", "structured":
		return structuredResponseModeJSON
	case "thought", "thoughts", "thinking", "thought_block":
		return structuredResponseModeThought
	}
	if isThoughtFriendlyModel(model) {
		return structuredResponseModeThought
	}
	return structuredResponseModeJSON
}

func isThoughtFriendlyModel(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	return strings.Contains(model, "qwen/") ||
		strings.Contains(model, "qwen3:") ||
		strings.Contains(model, "qwen2.5:") ||
		strings.HasPrefix(model, "qwen")
}

func researchSelectedModel() string {
	if model := strings.TrimSpace(os.Getenv("RESEARCH_MODEL")); model != "" {
		return model
	}
	if model := strings.TrimSpace(os.Getenv("LLM_MODEL_ANALYSIS")); model != "" {
		return model
	}
	return "qwen/qwen3.5-35b-a3b"
}

func criticalSelectedModel() string {
	if model := strings.TrimSpace(os.Getenv("PROSECUTION_MODEL")); model != "" {
		return model
	}
	if model := strings.TrimSpace(os.Getenv("COUNCIL_MODEL")); model != "" {
		return model
	}
	if model := strings.TrimSpace(os.Getenv("LLM_MODEL_CRITICAL")); model != "" {
		return model
	}
	return "qwen/qwen3.5-35b-a3b"
}

func structuredCompilerModel(envName string) string {
	for _, key := range []string{envName, "STRUCTURED_COMPILER_MODEL", "SCANNER_COMPILER_MODEL"} {
		if model := strings.TrimSpace(os.Getenv(key)); model != "" {
			return model
		}
	}
	return ""
}

func structuredRetryModel(envName, fallback, selected string) string {
	for _, key := range []string{envName, "STRUCTURED_RETRY_MODEL"} {
		if model := strings.TrimSpace(os.Getenv(key)); model != "" {
			return model
		}
	}
	if model := strings.TrimSpace(fallback); model != "" {
		return model
	}
	return strings.TrimSpace(selected)
}

func addTerminalJSONContract(systemPrompt string) string {
	return strings.TrimSpace(systemPrompt) + `

You MUST end with exactly one terminal JSON block:
FINAL_JSON
{ ... valid JSON matching the schema ... }
END_FINAL_JSON

Do not omit the terminal JSON block.`
}

func extractStructuredJSON(raw string) (string, error) {
	if cleaned, err := llm.ExtractJSON(raw); err == nil {
		return cleaned, nil
	}

	block, err := extractTerminalJSONBlock(raw)
	if err != nil {
		return "", err
	}
	return llm.ExtractJSON(block)
}

func extractTerminalJSONBlock(raw string) (string, error) {
	upper := strings.ToUpper(raw)
	start := strings.Index(upper, terminalJSONStart)
	end := strings.LastIndex(upper, terminalJSONEnd)
	if start < 0 || end < 0 || end <= start {
		return "", fmt.Errorf("terminal JSON block missing")
	}
	block := strings.TrimSpace(raw[start+len(terminalJSONStart) : end])
	if block == "" {
		return "", fmt.Errorf("terminal JSON block empty")
	}
	return block, nil
}

func truncateForCompiler(value string, max int) string {
	value = strings.TrimSpace(value)
	if max <= 0 || len(value) <= max {
		return value
	}
	if block, err := extractTerminalJSONBlock(value); err == nil && len(block) <= max {
		return block
	}
	if block, err := extractTerminalJSONBlock(value); err == nil && len(block) > max {
		return block[len(block)-max:]
	}
	return value[len(value)-max:]
}

func askStructuredWithRetry(ctx context.Context, router *llm.Router, tier llm.Tier, mode structuredResponseMode, baseSystemPrompt, thoughtPrefix, prompt string, maxTokens int, temperature float64) (string, bool, error) {
	if mode == structuredResponseModeThought {
		thoughtPrompt := addTerminalJSONContract(thoughtPrefix + "\n\n" + baseSystemPrompt)
		primaryCtx, cancel := withStructuredTimeout(ctx, structuredThoughtTimeout)
		resp, err := router.AskWithLimit(primaryCtx, tier, thoughtPrompt, prompt, maxTokens, temperature)
		cancel()
		if err != nil {
			return "", false, err
		}
		if _, err := extractStructuredJSON(resp); err == nil {
			return resp, false, nil
		}
		retryCtx, retryCancel := withStructuredTimeout(ctx, structuredJSONRetryTimout)
		fallbackResp, fallbackErr := router.AskJSONWithLimit(retryCtx, tier, baseSystemPrompt, prompt, minStructuredRetryTokens(maxTokens), temperature)
		retryCancel()
		if fallbackErr == nil {
			return fallbackResp, true, nil
		}
		return resp, false, nil
	}

	resp, err := router.AskJSONWithLimit(ctx, tier, baseSystemPrompt, prompt, maxTokens, temperature)
	if err != nil {
		return "", false, err
	}
	return resp, false, nil
}

func withStructuredTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, timeout)
}

func minStructuredRetryTokens(maxTokens int) int {
	if structuredJSONRetryTokens > 0 && structuredJSONRetryTokens < maxTokens {
		return structuredJSONRetryTokens
	}
	if maxTokens > 512 {
		return 512
	}
	return maxTokens
}

func readStructuredDurationEnv(name string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func readStructuredIntEnv(name string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}
