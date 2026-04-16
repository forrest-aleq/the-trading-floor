package research

import "time"

func ReloadRuntimeConfig() {
	structuredThoughtTimeout = readStructuredDurationEnv("STRUCTURED_THOUGHT_TIMEOUT", 18*time.Second)
	structuredJSONRetryTimout = readStructuredDurationEnv("STRUCTURED_JSON_RETRY_TIMEOUT", 10*time.Second)
	structuredJSONRetryTokens = readStructuredIntEnv("STRUCTURED_JSON_RETRY_MAX_TOKENS", 640)

	researchMaxTokens = readStructuredIntEnv("RESEARCH_MAX_TOKENS", 1024)
	researchThoughtTimeout = readStructuredDurationEnv("RESEARCH_THOUGHT_TIMEOUT", 30*time.Second)
	researchRetryTimeout = readStructuredDurationEnv("RESEARCH_RETRY_TIMEOUT", 20*time.Second)
	researchRetryMaxTokens = readStructuredIntEnv("RESEARCH_RETRY_MAX_TOKENS", 384)
	researchCompilerTimeout = readStructuredDurationEnv("RESEARCH_COMPILER_TIMEOUT", 35*time.Second)
	researchCompilerMaxTokens = readStructuredIntEnv("RESEARCH_COMPILER_MAX_TOKENS", 900)
	researchDefaultPosition = readStructuredFloatEnv("RESEARCH_DEFAULT_POSITION_SIZE_PCT", 0.01)

	prosecutionMaxTokens = readStructuredIntEnv("PROSECUTION_MAX_TOKENS", 768)
	prosecutionCompilerTimeout = readStructuredDurationEnv("PROSECUTION_COMPILER_TIMEOUT", 25*time.Second)
	prosecutionCompilerMaxTokens = readStructuredIntEnv("PROSECUTION_COMPILER_MAX_TOKENS", 900)

	councilPerspectiveMaxTokens = readStructuredIntEnv("COUNCIL_MAX_TOKENS", 384)
	councilPerspectiveTimeout = readStructuredDurationEnv("COUNCIL_TIMEOUT", 25*time.Second)
	councilCompilerTimeout = readStructuredDurationEnv("COUNCIL_COMPILER_TIMEOUT", 25*time.Second)
	councilCompilerMaxTokens = readStructuredIntEnv("COUNCIL_COMPILER_MAX_TOKENS", 600)
}
