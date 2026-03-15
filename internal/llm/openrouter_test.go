package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
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

func TestMakeLimiterDefaultsToTwoForLocalLLM(t *testing.T) {
	limiter := makeLimiter("http://127.0.0.1:1234/v1")
	if limiter == nil {
		t.Fatal("expected limiter for local LLM")
	}
	if cap(limiter) != 2 {
		t.Fatalf("expected local limiter capacity 2, got %d", cap(limiter))
	}
}
