package research

import (
	"fmt"
	"os"
	"strings"

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
	return strings.Contains(model, "qwen/")
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
