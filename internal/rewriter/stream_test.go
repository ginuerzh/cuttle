package rewriter

import "testing"

func TestExtractStreamState(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantReason string
		wantDone   bool
	}{
		{"done sentinel", `data: [DONE]`, "", true},
		{"anthropic message delta", `data: {"type":"message_delta","delta":{"stop_reason":"tool_use"}}`, "tool_use", false},
		{"anthropic top-level", `{"stop_reason":"end_turn"}`, "end_turn", false},
		{"openai choice", `data: {"choices":[{"finish_reason":"length"}]}`, "length", false},
		{"openai null first choice", `{"choices":[{"finish_reason":null},{"finish_reason":"stop"}]}`, "stop", false},
		{"responses api", `{"response":{"stop_reason":"max_output_tokens"}}`, "max_output_tokens", false},
		{"ordinary event", `data: {"choices":[{"delta":{"content":"hi"}}]}`, "", false},
		{"not json", `data: hello`, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reason, done := extractStreamState([]byte(tt.body))
			if reason != tt.wantReason || done != tt.wantDone {
				t.Fatalf("extractStreamState() = %q, %v; want %q, %v",
					reason, done, tt.wantReason, tt.wantDone)
			}
		})
	}
}
