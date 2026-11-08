package stats

import (
	"net/http"
	"testing"
)

func TestCaptureHeadersAllowlist(t *testing.T) {
	h := http.Header{
		"Content-Type":                           {"application/json"},
		"User-Agent":                             {"claude-code/1.0"},
		"Retry-After":                            {"30"},
		"X-Request-Id":                           {"req-1"},
		"X-Cuttle-Request-ID":                    {"sid-1"},
		"X-Opencode-Session":                     {"ses-1"},
		"Cf-Ray":                                 {"abc"},
		"Anthropic-Ratelimit-Requests-Remaining": {"10"},
		"X-Ratelimit-Limit":                      {"100"},
		"Authorization":                          {"Bearer secret"},
		"Cookie":                                 {"session=secret"},
		"Set-Cookie":                             {"session=secret"},
		"X-Api-Key":                              {"secret"},
		"X-Goog-Api-Key":                         {"secret"},
		"Api-Key":                                {"secret"},
		"X-Other":                                {"ignored"},
	}
	got := captureHeaders(h)
	for _, want := range []string{
		"content-type", "user-agent", "retry-after", "x-request-id",
		"x-cuttle-request-id", "x-opencode-session", "cf-ray",
		"anthropic-ratelimit-requests-remaining", "x-ratelimit-limit",
	} {
		if _, ok := got[want]; !ok {
			t.Fatalf("missing %q in %+v", want, got)
		}
	}
	for _, banned := range []string{
		"authorization", "cookie", "set-cookie", "x-api-key",
		"x-goog-api-key", "api-key", "x-other",
	} {
		if _, ok := got[banned]; ok {
			t.Fatalf("captured forbidden header %q", banned)
		}
	}
}

func TestCaptureHeadersEmpty(t *testing.T) {
	if got := captureHeaders(http.Header{"Authorization": {"secret"}}); got != nil {
		t.Fatalf("captureHeaders() = %+v, want nil", got)
	}
}
