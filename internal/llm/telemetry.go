package llm

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"
)

type usageContextKey struct{}

// UsageContext identifies which desk and pipeline stage caused an LLM call.
type UsageContext struct {
	DeskID   string
	Domain   string
	Stage    string
	SignalID string
	ThesisID string
}

type TokenUsageSnapshot struct {
	Attempts             int64
	Calls                int64
	Errors               int64
	InputTokens          int64
	OutputTokens         int64
	TotalTokens          int64
	EstimatedInputTokens int64
	DeskTokens           map[string]int64
	DeskCalls            map[string]int64
	DeskAttempts         map[string]int64
	DeskErrors           map[string]int64
	StageTokens          map[string]int64
	StageCalls           map[string]int64
	StageAttempts        map[string]int64
	StageErrors          map[string]int64
	ModelTokens          map[string]int64
	ModelCalls           map[string]int64
	ModelAttempts        map[string]int64
	ModelErrors          map[string]int64
	LastModel            string
	LastUpdated          time.Time
}

type tokenUsageMeter struct {
	mu sync.Mutex

	attempts             int64
	calls                int64
	errors               int64
	inputTokens          int64
	outputTokens         int64
	estimatedInputTokens int64
	deskTokens           map[string]int64
	deskCalls            map[string]int64
	deskAttempts         map[string]int64
	deskErrors           map[string]int64
	stageTokens          map[string]int64
	stageCalls           map[string]int64
	stageAttempts        map[string]int64
	stageErrors          map[string]int64
	modelTokens          map[string]int64
	modelCalls           map[string]int64
	modelAttempts        map[string]int64
	modelErrors          map[string]int64
	lastModel            string
	lastUpdated          time.Time
}

var globalTokenUsage = newTokenUsageMeter()

func newTokenUsageMeter() *tokenUsageMeter {
	return &tokenUsageMeter{
		deskTokens:    make(map[string]int64),
		deskCalls:     make(map[string]int64),
		deskAttempts:  make(map[string]int64),
		deskErrors:    make(map[string]int64),
		stageTokens:   make(map[string]int64),
		stageCalls:    make(map[string]int64),
		stageAttempts: make(map[string]int64),
		stageErrors:   make(map[string]int64),
		modelTokens:   make(map[string]int64),
		modelCalls:    make(map[string]int64),
		modelAttempts: make(map[string]int64),
		modelErrors:   make(map[string]int64),
	}
}

func WithUsageContext(ctx context.Context, next UsageContext) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	current, _ := UsageContextFrom(ctx)
	if strings.TrimSpace(next.DeskID) != "" {
		current.DeskID = strings.TrimSpace(next.DeskID)
	}
	if strings.TrimSpace(next.Domain) != "" {
		current.Domain = strings.TrimSpace(next.Domain)
	}
	if strings.TrimSpace(next.Stage) != "" {
		current.Stage = strings.TrimSpace(next.Stage)
	}
	if strings.TrimSpace(next.SignalID) != "" {
		current.SignalID = strings.TrimSpace(next.SignalID)
	}
	if strings.TrimSpace(next.ThesisID) != "" {
		current.ThesisID = strings.TrimSpace(next.ThesisID)
	}
	return context.WithValue(ctx, usageContextKey{}, current)
}

func UsageContextFrom(ctx context.Context) (UsageContext, bool) {
	if ctx == nil {
		return UsageContext{}, false
	}
	usage, ok := ctx.Value(usageContextKey{}).(UsageContext)
	return usage, ok
}

func TokenUsageStats() TokenUsageSnapshot {
	return globalTokenUsage.snapshot()
}

func recordTokenUsage(ctx context.Context, req Request, resp *Response, elapsed time.Duration) {
	if resp == nil {
		return
	}
	usage, _ := UsageContextFrom(ctx)
	model := firstNonEmptyString(resp.Model, req.Model)
	inputTokens := int64(resp.InputTokens)
	outputTokens := int64(resp.OutputTokens)
	totalTokens := inputTokens + outputTokens
	globalTokenUsage.addSuccess(usage, model, inputTokens, outputTokens)

	fields := []any{
		"model", model,
		"tier", tierName(req.Tier),
		"input_tokens", inputTokens,
		"output_tokens", outputTokens,
		"total_tokens", totalTokens,
		"elapsed_ms", elapsed.Milliseconds(),
		"json_mode", req.JSONMode,
	}
	if usage.DeskID != "" {
		fields = append(fields, "desk_id", usage.DeskID)
	}
	if usage.Domain != "" {
		fields = append(fields, "domain", usage.Domain)
	}
	if usage.Stage != "" {
		fields = append(fields, "stage", usage.Stage)
	}
	if usage.SignalID != "" {
		fields = append(fields, "signal_id", usage.SignalID)
	}
	if usage.ThesisID != "" {
		fields = append(fields, "thesis_id", usage.ThesisID)
	}
	slog.Default().With("component", "llm_usage").Info("llm tokens used", fields...)
}

func recordTokenFailure(ctx context.Context, req Request, model string, err error, elapsed time.Duration) {
	if err == nil {
		return
	}
	usage, _ := UsageContextFrom(ctx)
	model = firstNonEmptyString(model, req.Model)
	estimatedInputTokens := estimateRequestInputTokens(req)
	globalTokenUsage.addFailure(usage, model, estimatedInputTokens)

	fields := []any{
		"model", model,
		"tier", tierName(req.Tier),
		"estimated_input_tokens", estimatedInputTokens,
		"elapsed_ms", elapsed.Milliseconds(),
		"json_mode", req.JSONMode,
		"error", err,
	}
	if usage.DeskID != "" {
		fields = append(fields, "desk_id", usage.DeskID)
	}
	if usage.Domain != "" {
		fields = append(fields, "domain", usage.Domain)
	}
	if usage.Stage != "" {
		fields = append(fields, "stage", usage.Stage)
	}
	if usage.SignalID != "" {
		fields = append(fields, "signal_id", usage.SignalID)
	}
	if usage.ThesisID != "" {
		fields = append(fields, "thesis_id", usage.ThesisID)
	}
	slog.Default().With("component", "llm_usage").Warn("llm request failed", fields...)
}

func (m *tokenUsageMeter) addSuccess(usage UsageContext, model string, inputTokens, outputTokens int64) {
	if m == nil {
		return
	}
	totalTokens := inputTokens + outputTokens
	now := time.Now().UTC()

	m.mu.Lock()
	defer m.mu.Unlock()

	m.attempts++
	m.calls++
	m.inputTokens += inputTokens
	m.outputTokens += outputTokens
	m.lastModel = model
	m.lastUpdated = now

	if usage.DeskID != "" {
		m.deskAttempts[usage.DeskID]++
		m.deskTokens[usage.DeskID] += totalTokens
		m.deskCalls[usage.DeskID]++
	}
	if usage.Stage != "" {
		m.stageAttempts[usage.Stage]++
		m.stageTokens[usage.Stage] += totalTokens
		m.stageCalls[usage.Stage]++
	}
	if model != "" {
		m.modelAttempts[model]++
		m.modelTokens[model] += totalTokens
		m.modelCalls[model]++
	}
}

func (m *tokenUsageMeter) addFailure(usage UsageContext, model string, estimatedInputTokens int64) {
	if m == nil {
		return
	}
	now := time.Now().UTC()

	m.mu.Lock()
	defer m.mu.Unlock()

	m.attempts++
	m.errors++
	m.estimatedInputTokens += estimatedInputTokens
	m.lastModel = model
	m.lastUpdated = now

	if usage.DeskID != "" {
		m.deskAttempts[usage.DeskID]++
		m.deskErrors[usage.DeskID]++
	}
	if usage.Stage != "" {
		m.stageAttempts[usage.Stage]++
		m.stageErrors[usage.Stage]++
	}
	if model != "" {
		m.modelAttempts[model]++
		m.modelErrors[model]++
	}
}

func (m *tokenUsageMeter) snapshot() TokenUsageSnapshot {
	if m == nil {
		return TokenUsageSnapshot{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	return TokenUsageSnapshot{
		Attempts:             m.attempts,
		Calls:                m.calls,
		Errors:               m.errors,
		InputTokens:          m.inputTokens,
		OutputTokens:         m.outputTokens,
		TotalTokens:          m.inputTokens + m.outputTokens,
		EstimatedInputTokens: m.estimatedInputTokens,
		DeskTokens:           copyInt64Map(m.deskTokens),
		DeskCalls:            copyInt64Map(m.deskCalls),
		DeskAttempts:         copyInt64Map(m.deskAttempts),
		DeskErrors:           copyInt64Map(m.deskErrors),
		StageTokens:          copyInt64Map(m.stageTokens),
		StageCalls:           copyInt64Map(m.stageCalls),
		StageAttempts:        copyInt64Map(m.stageAttempts),
		StageErrors:          copyInt64Map(m.stageErrors),
		ModelTokens:          copyInt64Map(m.modelTokens),
		ModelCalls:           copyInt64Map(m.modelCalls),
		ModelAttempts:        copyInt64Map(m.modelAttempts),
		ModelErrors:          copyInt64Map(m.modelErrors),
		LastModel:            m.lastModel,
		LastUpdated:          m.lastUpdated,
	}
}

func resetTokenUsageForTest() {
	globalTokenUsage = newTokenUsageMeter()
}

func copyInt64Map(values map[string]int64) map[string]int64 {
	if len(values) == 0 {
		return nil
	}
	copied := make(map[string]int64, len(values))
	for key, value := range values {
		copied[key] = value
	}
	return copied
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func tierName(tier Tier) string {
	switch tier {
	case TierCritical:
		return "critical"
	case TierAnalysis:
		return "analysis"
	default:
		return "speed"
	}
}

func estimateRequestInputTokens(req Request) int64 {
	var chars int64
	for _, msg := range req.Messages {
		chars += int64(len(msg.Content))
	}
	if chars <= 0 {
		return 0
	}
	return (chars + 3) / 4
}
