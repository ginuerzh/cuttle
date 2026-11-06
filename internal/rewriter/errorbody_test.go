package rewriter

import (
	"strings"
	"testing"
)

func TestExtractErrorSummary(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		wantMessage string
		wantType    string
	}{
		{
			name:        "anthropic error object",
			body:        `{"type":"error","error":{"type":"authentication_error","message":"Authentication Fails, Your api key: sk-abcdefgh12345678 is invalid"}}`,
			wantMessage: "Authentication Fails, Your api key: [redacted] is invalid",
			wantType:    "authentication_error",
		},
		{
			name:        "openai error object",
			body:        `{"error":{"message":"rate limit exceeded","type":"rate_limit_error","code":"rate_limit_exceeded"}}`,
			wantMessage: "rate limit exceeded",
			wantType:    "rate_limit_error",
		},
		{
			name:        "commandcode envelope",
			body:        `{"success":false,"status":403,"message":"Request refused","cause":"content policy"}`,
			wantMessage: "Request refused: content policy",
		},
		{
			name:        "sse data line",
			body:        `data: {"type":"error","error":{"type":"overloaded_error","message":"temporarily unavailable"}}`,
			wantMessage: "temporarily unavailable",
			wantType:    "overloaded_error",
		},
		{
			name:        "error string",
			body:        `{"error":"bad gateway"}`,
			wantMessage: "bad gateway",
		},
		{
			name:        "ordinary response",
			body:        `{"choices":[{"message":{"content":"hello"}}]}`,
			wantMessage: "",
		},
		{
			name:        "not json",
			body:        `<html>bad gateway</html>`,
			wantMessage: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotMessage, gotType := extractErrorSummary([]byte(tt.body))
			if gotMessage != tt.wantMessage || gotType != tt.wantType {
				t.Fatalf("extractErrorSummary() = %q, %q; want %q, %q",
					gotMessage, gotType, tt.wantMessage, tt.wantType)
			}
		})
	}
}

func TestExtractErrorSummaryBoundsAndRedacts(t *testing.T) {
	message, _ := extractErrorSummary([]byte(`{"error":{"message":"` + strings.Repeat("x", 2000) + `"}}`))
	if len(message) > maxErrorMessage+3 {
		t.Fatalf("message length = %d, want <= %d", len(message), maxErrorMessage+3)
	}
	if !strings.HasSuffix(message, "...") {
		t.Fatalf("message = %q, want ellipsis", message)
	}

	message, _ = extractErrorSummary([]byte(`{"error":{"message":"Bearer abc.def.ghi and authorization: sk-secret12345678"}}`))
	if strings.Contains(message, "abc.def.ghi") || strings.Contains(message, "sk-secret12345678") {
		t.Fatalf("message was not redacted: %q", message)
	}
}
