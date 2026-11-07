package stats

import "strings"

// Error classes are stable, low-cardinality labels used by the UI and future
// alerting. They describe where a failure came from, not its exact message.
const (
	ErrorClassUpstreamAuth        = "upstream_auth"
	ErrorClassUpstreamForbidden   = "upstream_forbidden"
	ErrorClassUpstreamNotFound    = "upstream_not_found"
	ErrorClassUpstreamQuota       = "upstream_quota"
	ErrorClassUpstreamBadRequest  = "upstream_bad_request"
	ErrorClassUpstreamUnavailable = "upstream_unavailable"
	ErrorClassUpstreamTruncated   = "upstream_truncated"
	ErrorClassUpstreamError       = "upstream_error"
	ErrorClassGatewayTimeout      = "gateway_timeout"
	ErrorClassNetwork             = "network"
	ErrorClassRewriteError        = "rewrite_error"
	ErrorClassClientError         = "client_error"
	ErrorClassClientDisconnect    = "client_disconnect"
)

// ClassifyError maps an HTTP status, transport error and rewrite stage to one
// stable error class. The message is only used for failures that have no
// useful status (0) or that happen inside an otherwise successful stream.
func ClassifyError(status int, errStr, stage string) string {
	msg := strings.ToLower(strings.TrimSpace(errStr))
	if isTruncation(msg) {
		return ErrorClassUpstreamTruncated
	}
	if isClientDisconnect(msg) {
		return ErrorClassClientDisconnect
	}
	if stage != "" {
		return ErrorClassRewriteError
	}
	if status == 0 {
		switch {
		case msg == "":
			return ""
		case strings.Contains(msg, "timeout"), strings.Contains(msg, "timed out"):
			return ErrorClassGatewayTimeout
		default:
			return ErrorClassNetwork
		}
	}
	switch {
	case status == 200 || status == 0:
		if msg == "" {
			return ""
		}
		return ErrorClassUpstreamError
	case status == 401:
		return ErrorClassUpstreamAuth
	case status == 403:
		return ErrorClassUpstreamForbidden
	case status == 404:
		return ErrorClassUpstreamNotFound
	case status == 429:
		return ErrorClassUpstreamQuota
	case status >= 400 && status < 500:
		return ErrorClassUpstreamBadRequest
	case status >= 500:
		return ErrorClassUpstreamUnavailable
	}
	return ""
}

func isTruncation(msg string) bool {
	return strings.Contains(msg, "unexpected eof") ||
		strings.Contains(msg, "unexpected end of json input") ||
		strings.Contains(msg, "unexpected end of file") ||
		strings.Contains(msg, "terminal chunk") ||
		strings.Contains(msg, "stream ended before") ||
		strings.Contains(msg, "truncated")
}

func isClientDisconnect(msg string) bool {
	return strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "use of closed network connection") ||
		strings.Contains(msg, "client disconnected")
}
