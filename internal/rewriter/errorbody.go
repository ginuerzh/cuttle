package rewriter

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
	"unicode/utf8"
)

const maxErrorMessage = 512

var (
	// Credentials can be echoed back by upstreams in error messages. Redact
	// the common shapes before persisting anything.
	secretPatterns = []struct {
		re          *regexp.Regexp
		replacement string
	}{
		{regexp.MustCompile(`(?i)\b(bearer)\s+[A-Za-z0-9._~+/=-]+`), "${1} [redacted]"},
		{regexp.MustCompile(`(?i)\b(api[-_ ]?key|authorization|token|secret|password)\s*([:=])\s*\S+`), "${1}${2} [redacted]"},
		{regexp.MustCompile(`\b(?:sk|oc_sk|user)[-_][A-Za-z0-9_-]{8,}\b`), "[redacted]"},
	}
)

// extractErrorSummary returns a redacted, bounded error message and type from
// a response body or one SSE data line. It returns empty strings for payloads
// that do not look like errors, so it is safe to call on ordinary responses.
func extractErrorSummary(b []byte) (message, typ string) {
	payload := bytes.TrimSpace(b)
	if len(payload) == 0 {
		return "", ""
	}
	if bytes.HasPrefix(payload, []byte("data:")) {
		payload = bytes.TrimSpace(payload[len("data:"):])
	}
	if len(payload) == 0 || payload[0] != '{' {
		return "", ""
	}
	if !looksLikeError(payload) {
		return "", ""
	}

	var env struct {
		Error   json.RawMessage `json:"error"`
		Message string          `json:"message"`
		Cause   string          `json:"cause"`
		Type    string          `json:"type"`
		Code    string          `json:"code"`
		Success *bool           `json:"success"`
	}
	if err := json.Unmarshal(payload, &env); err != nil {
		return "", ""
	}

	message, typ = parseErrorField(env.Error)
	if message == "" {
		message = strings.TrimSpace(env.Message)
	}
	if message == "" {
		message = strings.TrimSpace(env.Cause)
	}
	if typ == "" {
		typ = strings.TrimSpace(env.Type)
	}
	if typ == "" {
		typ = strings.TrimSpace(env.Code)
	}
	if message == "" {
		message = typ
	}
	if message == "" && env.Success != nil && !*env.Success {
		message = "upstream rejected the request"
	}
	if message == "" {
		return "", ""
	}
	if env.Cause != "" && env.Cause != message && !strings.Contains(message, env.Cause) {
		message += ": " + env.Cause
	}

	return redactErrorMessage(message), typ
}

func looksLikeError(payload []byte) bool {
	return bytes.Contains(payload, []byte(`"error"`)) ||
		bytes.Contains(payload, []byte(`"success":false`)) ||
		bytes.Contains(payload, []byte(`"success": false`)) ||
		bytes.Contains(payload, []byte(`"cause"`)) ||
		bytes.Contains(payload, []byte(`"type":"error"`)) ||
		bytes.Contains(payload, []byte(`"type": "error"`))
}

// parseErrorField handles both {"error":"..."} and {"error":{...}}.
func parseErrorField(raw json.RawMessage) (message, typ string) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s), ""
	}
	var obj struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	}
	if json.Unmarshal(raw, &obj) == nil {
		return strings.TrimSpace(obj.Message), strings.TrimSpace(firstNonEmpty(obj.Type, obj.Code))
	}
	return strings.TrimSpace(string(raw)), ""
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func redactErrorMessage(s string) string {
	for _, p := range secretPatterns {
		s = p.re.ReplaceAllString(s, p.replacement)
	}
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= maxErrorMessage {
		return s
	}
	runes := []rune(s)
	return string(runes[:maxErrorMessage]) + "..."
}
