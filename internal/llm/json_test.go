package llm

import "testing"

func TestExtractJSON(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{
			name:  "plain JSON",
			input: `{"tradeable": true, "score": 85}`,
		},
		{
			name:  "markdown code fence",
			input: "Here's the result:\n```json\n{\"tradeable\": true}\n```\n",
		},
		{
			name:  "plain code fence",
			input: "```\n{\"tradeable\": true}\n```",
		},
		{
			name:  "prose around JSON",
			input: "Based on my analysis:\n{\"tradeable\": true, \"score\": 90}\nThat's my assessment.",
		},
		{
			name:    "no JSON",
			input:   "This is just plain text with no JSON at all.",
			wantErr: true,
		},
		{
			name:  "whitespace padded",
			input: "  \n  {\"score\": 50}  \n  ",
		},
		{
			name:  "think tags around JSON",
			input: "<think>\ninternal reasoning\n</think>\n\n{\"tradeable\": false, \"score\": 12}",
		},
		{
			name:  "multiple JSON objects prefers last valid candidate",
			input: "schema example {\"tradeable\": false}\nfinal answer {\"tradeable\": true, \"score\": 88}",
		},
		{
			name:  "json array candidate",
			input: "notes before\n[{\"symbol\":\"AAPL\"},{\"symbol\":\"MSFT\"}]\nafter",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ExtractJSON(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result == "" {
				t.Fatal("expected non-empty result")
			}
		})
	}
}

func TestValidateJSONFields(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		required []string
		wantErr  bool
	}{
		{
			name:     "all present",
			input:    `{"tradeable": true, "score": 85, "instruments": []}`,
			required: []string{"tradeable", "score"},
		},
		{
			name:     "missing field",
			input:    `{"tradeable": true}`,
			required: []string{"tradeable", "score"},
			wantErr:  true,
		},
		{
			name:     "invalid JSON",
			input:    `not json`,
			required: []string{"tradeable"},
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateJSONFields(tt.input, tt.required)
			if tt.wantErr && err == nil {
				t.Fatal("expected error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
