package research

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
)

//go:embed prosecution_system.txt
var embeddedProsecutionPrompt string

//go:embed research_system.txt
var embeddedResearchPrompt string

//go:embed research_fast_system.txt
var embeddedResearchFastPrompt string

//go:embed research_user_compact_prefix.txt
var embeddedResearchUserCompactPrefix string

//go:embed research_thought_prefix.txt
var embeddedResearchThoughtPrefix string

//go:embed research_compiler_prompt.txt
var embeddedResearchCompilerPrompt string

//go:embed prosecution_thought_prefix.txt
var embeddedProsecutionThoughtPrefix string

//go:embed prosecution_compiler_prompt.txt
var embeddedProsecutionCompilerPrompt string

//go:embed council_thought_prefix.txt
var embeddedCouncilThoughtPrefix string

//go:embed council_compiler_prompt.txt
var embeddedCouncilCompilerPrompt string

//go:embed council_voices.json
var embeddedCouncilVoices []byte

type promptPolicy struct {
	researchPrompt            string
	researchFastPrompt        string
	researchUserCompactPrefix string
	researchThoughtPrefix     string
	researchCompilerPrompt    string
	prosecutionPrompt         string
	prosecutionThoughtPrefix  string
	prosecutionCompilerPrompt string
	councilThoughtPrefix      string
	councilCompilerPrompt     string
	councilArchetypes         []Archetype
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
	archetypes, err := loadCouncilArchetypes()
	if err != nil {
		return promptPolicy{}, err
	}
	return promptPolicy{
		researchPrompt:            loadPromptText("RESEARCH_PROMPT_FILE", embeddedResearchPrompt),
		researchFastPrompt:        loadPromptText("RESEARCH_FAST_PROMPT_FILE", embeddedResearchFastPrompt),
		researchUserCompactPrefix: loadPromptText("RESEARCH_COMPACT_PREFIX_FILE", embeddedResearchUserCompactPrefix),
		researchThoughtPrefix:     loadPromptText("RESEARCH_THOUGHT_PREFIX_FILE", embeddedResearchThoughtPrefix),
		researchCompilerPrompt:    loadPromptText("RESEARCH_COMPILER_PROMPT_FILE", embeddedResearchCompilerPrompt),
		prosecutionPrompt:         loadPromptText("PROSECUTION_PROMPT_FILE", embeddedProsecutionPrompt),
		prosecutionThoughtPrefix:  loadPromptText("PROSECUTION_THOUGHT_PREFIX_FILE", embeddedProsecutionThoughtPrefix),
		prosecutionCompilerPrompt: loadPromptText("PROSECUTION_COMPILER_PROMPT_FILE", embeddedProsecutionCompilerPrompt),
		councilThoughtPrefix:      loadPromptText("COUNCIL_THOUGHT_PREFIX_FILE", embeddedCouncilThoughtPrefix),
		councilCompilerPrompt:     loadPromptText("COUNCIL_COMPILER_PROMPT_FILE", embeddedCouncilCompilerPrompt),
		councilArchetypes:         archetypes,
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

func loadCouncilArchetypes() ([]Archetype, error) {
	raw := embeddedCouncilVoices
	if path := strings.TrimSpace(os.Getenv("COUNCIL_VOICES_FILE")); path != "" {
		loaded, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read council voices %s: %w", path, err)
		}
		raw = loaded
	}

	var data []Archetype
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("decode council voices: %w", err)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("council voices policy must contain at least one archetype")
	}

	normalized := make([]Archetype, 0, len(data))
	seen := make(map[string]struct{}, len(data))
	for _, item := range data {
		item.Name = strings.TrimSpace(item.Name)
		item.Prompt = strings.TrimSpace(item.Prompt)
		if item.Name == "" || item.Prompt == "" {
			return nil, fmt.Errorf("council voices policy contains empty name or prompt")
		}
		key := strings.ToLower(item.Name)
		if _, ok := seen[key]; ok {
			return nil, fmt.Errorf("council voices policy contains duplicate voice %s", item.Name)
		}
		seen[key] = struct{}{}
		normalized = append(normalized, item)
	}
	return normalized, nil
}
