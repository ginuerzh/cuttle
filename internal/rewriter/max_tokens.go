package rewriter

import (
	"encoding/json"
	"strings"
)

// clampMaxTokens lowers an oversized output-token field to cap. It never
// raises a smaller value or adds a missing field. Invalid JSON, non-integer
// values and unsupported protocols leave the body unchanged.
func clampMaxTokens(body []byte, protocol string, cap int) ([]byte, error) {
	if cap <= 0 || len(body) == 0 {
		return body, nil
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return body, nil
	}

	if strings.EqualFold(protocol, "gemini") {
		return clampGeminiMaxTokens(body, raw, cap)
	}

	for _, field := range outputTokenFields(protocol) {
		value, ok := raw[field]
		if !ok {
			continue
		}
		tokens, ok := decodeTokenCount(value)
		if !ok || tokens <= cap {
			return body, nil
		}
		return replaceTokenCount(body, raw, field, cap)
	}
	return body, nil
}

func outputTokenFields(protocol string) []string {
	switch strings.ToLower(protocol) {
	case "anthropic":
		return []string{"max_tokens"}
	case "chat":
		return []string{"max_tokens", "max_completion_tokens"}
	case "responses":
		return []string{"max_output_tokens"}
	default:
		return nil
	}
}

func clampGeminiMaxTokens(body []byte, raw map[string]json.RawMessage, cap int) ([]byte, error) {
	configRaw, ok := raw["generationConfig"]
	if !ok {
		return body, nil
	}
	var config map[string]json.RawMessage
	if err := json.Unmarshal(configRaw, &config); err != nil {
		return body, nil
	}
	value, ok := config["maxOutputTokens"]
	if !ok {
		return body, nil
	}
	tokens, ok := decodeTokenCount(value)
	if !ok || tokens <= cap {
		return body, nil
	}
	clamped, err := replaceTokenCount(configRaw, config, "maxOutputTokens", cap)
	if err != nil {
		return body, err
	}
	raw["generationConfig"] = clamped
	out, err := json.Marshal(raw)
	if err != nil {
		return body, err
	}
	return out, nil
}

func decodeTokenCount(raw json.RawMessage) (int, bool) {
	var tokens int
	if err := json.Unmarshal(raw, &tokens); err != nil {
		return 0, false
	}
	return tokens, true
}

func replaceTokenCount(body []byte, fields map[string]json.RawMessage, field string, cap int) ([]byte, error) {
	value, err := json.Marshal(cap)
	if err != nil {
		return body, err
	}
	fields[field] = value
	out, err := json.Marshal(fields)
	if err != nil {
		return body, err
	}
	return out, nil
}
