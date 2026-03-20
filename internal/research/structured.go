package research

import (
	"os"
	"strings"
)

type structuredResponseMode string

const (
	structuredResponseModeJSON    structuredResponseMode = "structured_json"
	structuredResponseModeThought structuredResponseMode = "thought_block"
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
