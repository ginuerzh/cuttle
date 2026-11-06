package rewriter

import (
	"bytes"
	"encoding/json"
	"strings"
)

// extractStreamState observes one SSE data line or a non-streaming response
// body. It reports the terminal finish reason and whether the [DONE] sentinel
// was seen.
func extractStreamState(b []byte) (finishReason string, done bool) {
	payload := bytes.TrimSpace(b)
	if len(payload) == 0 {
		return "", false
	}
	if bytes.HasPrefix(payload, []byte("data:")) {
		payload = bytes.TrimSpace(payload[len("data:"):])
	}
	if bytes.Equal(payload, []byte("[DONE]")) {
		return "", true
	}
	if len(payload) == 0 || payload[0] != '{' {
		return "", false
	}

	var env struct {
		Type       string `json:"type"`
		StopReason string `json:"stop_reason"`
		Delta      struct {
			StopReason string `json:"stop_reason"`
		} `json:"delta"`
		Choices []struct {
			FinishReason *string `json:"finish_reason"`
		} `json:"choices"`
		Response struct {
			StopReason string `json:"stop_reason"`
		} `json:"response"`
	}
	if err := json.Unmarshal(payload, &env); err != nil {
		return "", false
	}

	switch {
	case env.Delta.StopReason != "":
		return strings.TrimSpace(env.Delta.StopReason), false
	case env.StopReason != "":
		return strings.TrimSpace(env.StopReason), false
	case env.Response.StopReason != "":
		return strings.TrimSpace(env.Response.StopReason), false
	}
	for _, choice := range env.Choices {
		if choice.FinishReason != nil && strings.TrimSpace(*choice.FinishReason) != "" {
			return strings.TrimSpace(*choice.FinishReason), false
		}
	}
	return "", false
}
