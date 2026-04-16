package scanner

import (
	_ "embed"
	"fmt"
	"os"
	"strings"
	"sync"
)

//go:embed structured_system.txt
var embeddedStructuredPrompt string

//go:embed structured_fast_system.txt
var embeddedStructuredFastPrompt string

//go:embed thought_system.txt
var embeddedThoughtPrompt string

//go:embed compiler_prompt.txt
var embeddedCompilerPrompt string

type promptPolicy struct {
	structuredPrompt     string
	structuredFastPrompt string
	thoughtPrompt        string
	compilerPrompt       string
}

var (
	promptPolicyOnce sync.Once
	promptPolicyData promptPolicy
	promptPolicyErr  error
)

func activePromptPolicy() promptPolicy {
	promptPolicyOnce.Do(func() {
		promptPolicyData, promptPolicyErr = loadPromptPolicy()
		if promptPolicyErr != nil {
			panic(promptPolicyErr)
		}
	})
	return promptPolicyData
}

func loadPromptPolicy() (promptPolicy, error) {
	return promptPolicy{
		structuredPrompt:     loadPromptText("SCANNER_PROMPT_FILE", embeddedStructuredPrompt),
		structuredFastPrompt: loadPromptText("SCANNER_FAST_PROMPT_FILE", embeddedStructuredFastPrompt),
		thoughtPrompt:        loadPromptText("SCANNER_THOUGHT_PROMPT_FILE", embeddedThoughtPrompt),
		compilerPrompt:       loadPromptText("SCANNER_COMPILER_PROMPT_FILE", embeddedCompilerPrompt),
	}, nil
}

func loadPromptText(envName, fallback string) string {
	path := strings.TrimSpace(os.Getenv(envName))
	if path == "" {
		return strings.TrimSpace(fallback)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		panic(fmt.Errorf("read prompt file %s: %w", path, err))
	}
	return strings.TrimSpace(string(raw))
}
