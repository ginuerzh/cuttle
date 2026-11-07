package stats

import "testing"

func TestClassifyError(t *testing.T) {
	tests := []struct {
		name   string
		status int
		err    string
		stage  string
		want   string
	}{
		{"unauthenticated", 401, "", "", ErrorClassUpstreamAuth},
		{"forbidden", 403, "", "", ErrorClassUpstreamForbidden},
		{"not found", 404, "", "", ErrorClassUpstreamNotFound},
		{"rate limited", 429, "", "", ErrorClassUpstreamQuota},
		{"bad request", 400, "invalid body", "", ErrorClassUpstreamBadRequest},
		{"server error", 502, "upstream unavailable", "", ErrorClassUpstreamUnavailable},
		{"header timeout", 0, "read tcp 1.2.3.4:443: i/o timeout", "", ErrorClassGatewayTimeout},
		{"network reset", 0, "connection reset by peer", "", ErrorClassNetwork},
		{"status zero truncation", 0, "unexpected EOF", "", ErrorClassUpstreamTruncated},
		{"stream truncated", 200, "unexpected EOF", "", ErrorClassUpstreamTruncated},
		{"terminal chunk", 200, "Upstream stream ended before terminal chunk", "", ErrorClassUpstreamTruncated},
		{"client disconnect", 200, "write tcp 1.2.3.4:8787->5.6.7.8:1234: write: broken pipe", "", ErrorClassClientDisconnect},
		{"stream rewrite truncated", 200, "unmarshal stream chunk: unexpected end of JSON input", "stream_rewrite", ErrorClassUpstreamTruncated},
		{"rewrite failure", 200, "invalid tool call", "response_rewrite", ErrorClassRewriteError},
		{"success", 200, "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ClassifyError(tt.status, tt.err, tt.stage); got != tt.want {
				t.Fatalf("ClassifyError(%d, %q, %q) = %q, want %q",
					tt.status, tt.err, tt.stage, got, tt.want)
			}
		})
	}
}
