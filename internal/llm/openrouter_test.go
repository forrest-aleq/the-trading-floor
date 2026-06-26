package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestOpenRouterClientSupportsStructuredJSON(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		want    bool
	}{
		{
			name:    "localhost LM Studio",
			baseURL: "http://127.0.0.1:1234/v1",
			want:    false,
		},
		{
			name:    "localhost hostname",
			baseURL: "http://localhost:11434/v1",
			want:    false,
		},
		{
			name:    "openrouter",
			baseURL: "https://openrouter.ai/api/v1",
			want:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &OpenRouterClient{baseURL: tt.baseURL}
			if got := client.supportsStructuredJSON(); got != tt.want {
				t.Fatalf("supportsStructuredJSON() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsLocalLLM(t *testing.T) {
	if !isLocalLLM("http://127.0.0.1:1234/v1") {
		t.Fatal("expected localhost endpoint to be treated as local")
	}
	if isLocalLLM("https://openrouter.ai/api/v1") {
		t.Fatal("expected remote endpoint to not be treated as local")
	}
}

func TestOpenRouterClientRetriesLocal500s(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			http.Error(w, "lm studio hiccup", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}],"model":"local","usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	defer server.Close()

	client := NewOpenRouterClient(OpenRouterConfig{
		BaseURL: server.URL,
		Model:   "local-model",
	})

	resp, err := client.Complete(context.Background(), Request{
		Messages: []Message{{Role: RoleUser, Content: "ping"}},
	})
	if err != nil {
		t.Fatalf("Complete returned error: %v", err)
	}
	if resp.Content != "ok" {
		t.Fatalf("unexpected response content %q", resp.Content)
	}
	if attempts != 2 {
		t.Fatalf("expected 2 attempts, got %d", attempts)
	}
}

func TestOpenRouterClientFallsBackWhenPrimaryModelUnavailable(t *testing.T) {
	var requested []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		requested = append(requested, body["model"].(string))
		if len(requested) == 1 {
			w.WriteHeader(http.StatusPaymentRequired)
			_, _ = w.Write([]byte(`{"error":{"message":"Insufficient credits","code":402}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"ok\":true}"}}],"model":"openai/gpt-oss-120b:free","usage":{"prompt_tokens":2,"completion_tokens":3}}`))
	}))
	defer server.Close()

	client := NewOpenRouterClient(OpenRouterConfig{
		BaseURL:        server.URL,
		Model:          "paid/model",
		FallbackModels: []string{"openai/gpt-oss-120b:free"},
	})

	resp, err := client.Complete(context.Background(), Request{
		Messages: []Message{{Role: RoleUser, Content: "return JSON"}},
		JSONMode: true,
	})
	if err != nil {
		t.Fatalf("Complete returned error: %v", err)
	}
	if resp.Content != `{"ok":true}` {
		t.Fatalf("unexpected response content %q", resp.Content)
	}
	if len(requested) != 2 {
		t.Fatalf("expected 2 model attempts, got %d", len(requested))
	}
	if requested[0] != "paid/model" || requested[1] != "openai/gpt-oss-120b:free" {
		t.Fatalf("unexpected requested models %#v", requested)
	}
}

func TestOpenRouterClientPreservesReasoningFromLocalProviders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"FINAL_DECISION\ntradeable: false\nscore: 10\ninstruments: none\ndirection: none\nurgency: 0.0\ncategory: corporate\nreasoning: reject\nEND_FINAL_DECISION","reasoning":"bullet one\nbullet two"}}],"model":"ollama","usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	defer server.Close()

	client := NewOpenRouterClient(OpenRouterConfig{
		BaseURL: server.URL,
		Model:   "qwen3:8b",
	})

	resp, err := client.Complete(context.Background(), Request{
		Messages: []Message{{Role: RoleUser, Content: "scan"}},
	})
	if err != nil {
		t.Fatalf("Complete returned error: %v", err)
	}
	if got := resp.Content; got != "<think>\nbullet one\nbullet two\n</think>\n\nFINAL_DECISION\ntradeable: false\nscore: 10\ninstruments: none\ndirection: none\nurgency: 0.0\ncategory: corporate\nreasoning: reject\nEND_FINAL_DECISION" {
		t.Fatalf("unexpected normalized content %q", got)
	}
}

func TestOpenRouterClientJSONModeUsesFinalContentWithoutReasoning(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"ok\":true}","reasoning":"I will now produce JSON."}}],"model":"ollama","usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	defer server.Close()

	client := NewOpenRouterClient(OpenRouterConfig{
		BaseURL: server.URL,
		Model:   "qwen3.5:9b",
	})

	resp, err := client.Complete(context.Background(), Request{
		Messages: []Message{{Role: RoleUser, Content: "return JSON"}},
		JSONMode: true,
	})
	if err != nil {
		t.Fatalf("Complete returned error: %v", err)
	}
	if got := resp.Content; got != `{"ok":true}` {
		t.Fatalf("unexpected JSON-mode content %q", got)
	}
}

func TestMakeLimiterDefaultsToTwoForLocalLLM(t *testing.T) {
	limiter := makeLimiter("http://127.0.0.1:1234/v1", 0)
	if limiter == nil {
		t.Fatal("expected limiter for local LLM")
	}
	if cap(limiter) != 2 {
		t.Fatalf("expected local limiter capacity 2, got %d", cap(limiter))
	}
}

func TestMakeLimiterUsesConfiguredCapacity(t *testing.T) {
	limiter := makeLimiter("http://127.0.0.1:1234/v1", 5)
	if limiter == nil {
		t.Fatal("expected limiter for local LLM")
	}
	if cap(limiter) != 5 {
		t.Fatalf("expected configured limiter capacity 5, got %d", cap(limiter))
	}
}

func TestApplyLocalJSONControlsAddsNoThinkForLocalQwenJSON(t *testing.T) {
	messages := []orMessage{
		{Role: string(RoleSystem), Content: "Return JSON only."},
		{Role: string(RoleUser), Content: "Scan this signal."},
	}

	got := applyLocalJSONControls("http://127.0.0.1:1234/v1", "qwen/qwen3-8b", true, messages)
	if got[0].Content != "/no_think\nReturn JSON only." {
		t.Fatalf("unexpected system message %q", got[0].Content)
	}
}

func TestApplyLocalJSONControlsLeavesRemoteModelsUntouched(t *testing.T) {
	messages := []orMessage{
		{Role: string(RoleSystem), Content: "Return JSON only."},
	}

	got := applyLocalJSONControls("https://openrouter.ai/api/v1", "qwen/qwen3-8b", true, messages)
	if got[0].Content != "Return JSON only." {
		t.Fatalf("expected remote model to be unchanged, got %q", got[0].Content)
	}
}

func TestOpenRouterClientExcludesReasoningForRemoteJSONMode(t *testing.T) {
	t.Setenv("LLM_JSON_REASONING_EFFORT", "low")
	var body map[string]any
	client := &OpenRouterClient{
		apiKey:  "test-key",
		baseURL: "https://openrouter.ai/api/v1",
		model:   "deepseek/deepseek-v4-flash",
		http: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatalf("decode request body: %v", err)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"{\"ok\":true}"}}],"model":"deepseek/deepseek-v4-flash","usage":{"prompt_tokens":1,"completion_tokens":1}}`)),
				Header:     make(http.Header),
			}, nil
		})},
	}

	_, err := client.Complete(context.Background(), Request{
		Messages: []Message{{Role: RoleUser, Content: "return JSON"}},
		JSONMode: true,
	})
	if err != nil {
		t.Fatalf("Complete returned error: %v", err)
	}

	reasoning, ok := body["reasoning"].(map[string]any)
	if !ok {
		t.Fatalf("expected reasoning object in request body, got %#v", body["reasoning"])
	}
	if got, ok := reasoning["exclude"].(bool); !ok || !got {
		t.Fatalf("expected reasoning.exclude=true, got %#v", reasoning["exclude"])
	}
	if got := reasoning["effort"]; got != "low" {
		t.Fatalf("expected reasoning.effort=low, got %#v", got)
	}
}

func TestOpenRouterClientCanDisableReasoningForRemoteJSONMode(t *testing.T) {
	t.Setenv("LLM_JSON_REASONING_EFFORT", "off")
	var body map[string]any
	client := &OpenRouterClient{
		apiKey:  "test-key",
		baseURL: "https://openrouter.ai/api/v1",
		model:   "qwen/qwen3-235b-a22b-2507",
		http: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatalf("decode request body: %v", err)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"{\"ok\":true}"}}],"model":"qwen/qwen3-235b-a22b-2507","usage":{"prompt_tokens":1,"completion_tokens":1}}`)),
				Header:     make(http.Header),
			}, nil
		})},
	}

	_, err := client.Complete(context.Background(), Request{
		Messages: []Message{{Role: RoleUser, Content: "return JSON"}},
		JSONMode: true,
	})
	if err != nil {
		t.Fatalf("Complete returned error: %v", err)
	}
	if _, ok := body["reasoning"]; ok {
		t.Fatalf("expected no reasoning object in request body, got %#v", body["reasoning"])
	}
}

func TestOpenRouterClientEnablesReasoningForRemoteThoughtMode(t *testing.T) {
	t.Setenv("LLM_REASONING_ENABLED", "true")
	t.Setenv("LLM_REASONING_EFFORT", "medium")

	var body map[string]any
	client := &OpenRouterClient{
		apiKey:  "test-key",
		baseURL: "https://openrouter.ai/api/v1",
		model:   "qwen/qwen3.6-35b-a3b",
		http: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatalf("decode request body: %v", err)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"FINAL_DECISION\ntradeable: false\nscore: 10\ninstruments: none\ndirection: none\nurgency: 0.0\ncategory: macro\nreasoning: no edge\nEND_FINAL_DECISION"}}],"model":"qwen/qwen3.6-35b-a3b","usage":{"prompt_tokens":1,"completion_tokens":1}}`)),
				Header:     make(http.Header),
			}, nil
		})},
	}

	_, err := client.Complete(context.Background(), Request{
		Messages: []Message{{Role: RoleUser, Content: "think then decide"}},
		JSONMode: false,
	})
	if err != nil {
		t.Fatalf("Complete returned error: %v", err)
	}

	reasoning, ok := body["reasoning"].(map[string]any)
	if !ok {
		t.Fatalf("expected reasoning object in request body, got %#v", body["reasoning"])
	}
	if got, ok := reasoning["enabled"].(bool); !ok || !got {
		t.Fatalf("expected reasoning.enabled=true, got %#v", reasoning["enabled"])
	}
	if got := reasoning["effort"]; got != "medium" {
		t.Fatalf("expected reasoning.effort=medium, got %#v", got)
	}
}

func TestOpenRouterClientAddsProviderRouting(t *testing.T) {
	allowFallbacks := false
	requireParameters := true
	var body map[string]any
	client := NewOpenRouterClient(OpenRouterConfig{
		APIKey:  "test-key",
		BaseURL: "https://openrouter.ai/api/v1",
		Model:   "openai/gpt-oss-120b",
		Provider: &ProviderRouting{
			Order:             []string{"groq"},
			AllowFallbacks:    &allowFallbacks,
			RequireParameters: &requireParameters,
		},
	})
	client.http = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"OK"}}],"model":"openai/gpt-oss-120b","usage":{"prompt_tokens":1,"completion_tokens":1}}`)),
			Header:     make(http.Header),
		}, nil
	})}

	_, err := client.Complete(context.Background(), Request{
		Messages: []Message{{Role: RoleUser, Content: "ping"}},
	})
	if err != nil {
		t.Fatalf("Complete returned error: %v", err)
	}

	provider, ok := body["provider"].(map[string]any)
	if !ok {
		t.Fatalf("expected provider object in request body, got %#v", body["provider"])
	}
	order, ok := provider["order"].([]any)
	if !ok || len(order) != 1 || order[0] != "groq" {
		t.Fatalf("unexpected provider.order %#v", provider["order"])
	}
	if got := provider["allow_fallbacks"]; got != false {
		t.Fatalf("expected allow_fallbacks=false, got %#v", got)
	}
	if got := provider["require_parameters"]; got != true {
		t.Fatalf("expected require_parameters=true, got %#v", got)
	}
}

func TestOpenRouterClientOmitsProviderRoutingForLocalLLM(t *testing.T) {
	t.Setenv("LLM_SPEED_PROVIDER_ALLOW_FALLBACKS", "true")
	t.Setenv("LLM_SPEED_PROVIDER_REQUIRE_PARAMETERS", "true")

	var body map[string]any
	client := NewOpenRouterClient(OpenRouterConfig{
		APIKey:    "test-key",
		BaseURL:   "http://127.0.0.1:11434/v1",
		Model:     "qwen3.5:9b",
		EnvPrefix: "LLM_SPEED",
	})
	client.http = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"OK"}}],"model":"qwen3.5:9b","usage":{"prompt_tokens":1,"completion_tokens":1}}`)),
			Header:     make(http.Header),
		}, nil
	})}

	_, err := client.Complete(context.Background(), Request{
		Messages: []Message{{Role: RoleUser, Content: "ping"}},
	})
	if err != nil {
		t.Fatalf("Complete returned error: %v", err)
	}
	if _, ok := body["provider"]; ok {
		t.Fatalf("expected no provider object for local LLM, got %#v", body["provider"])
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestApplyLocalJSONControlsHandlesOllamaQwenModels(t *testing.T) {
	messages := []orMessage{
		{Role: string(RoleSystem), Content: "Return JSON only."},
	}

	got := applyLocalJSONControls("http://127.0.0.1:11434/v1", "qwen3:8b", true, messages)
	if got[0].Content != "/no_think\nReturn JSON only." {
		t.Fatalf("unexpected system message %q", got[0].Content)
	}
}

func TestApplyLocalJSONControlsHandlesNonQwenLocalModels(t *testing.T) {
	messages := []orMessage{
		{Role: string(RoleSystem), Content: "Return JSON only."},
	}

	got := applyLocalJSONControls("http://127.0.0.1:11434/v1", "glm-4.7-flash:latest", true, messages)
	if got[0].Content != "/no_think\nReturn JSON only." {
		t.Fatalf("unexpected system message %q", got[0].Content)
	}
}

func TestOpenRouterClientUsesContextDeadlineAsHTTPTimeout(t *testing.T) {
	client := &OpenRouterClient{
		http: &http.Client{Timeout: 120 * time.Second},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	derived := client.httpClientFor(ctx)
	if derived.Timeout <= 0 {
		t.Fatal("expected positive derived timeout")
	}
	if derived.Timeout > 50*time.Millisecond {
		t.Fatalf("expected derived timeout to respect context deadline, got %s", derived.Timeout)
	}
}
