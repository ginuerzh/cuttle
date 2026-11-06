package tokenest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
)

// DefaultSafetyFactor biases every estimate upward so context compaction is
// not delayed by an undercount.
const DefaultSafetyFactor = 1.1

const (
	requestOverhead = 3
	messageOverhead = 4
	toolOverhead    = 8
	imageTokens     = 1600
	documentTokens  = 2000
)

// Classes: 0 letter, 1 digit, 2 space, 3 punct, 4 CJK, 5 other.
// Coefficients are calibrated offline against the official DeepSeek V4
// tokenizer; see testdata/calibration.jsonl and the design document.
var charCoef = [6]float64{0.0817, 0.7438, 0.2590, 0.1416, 0.7617, 1.0000}
var runCoef = [6]float64{0.4920, 0.0000, 0.0000, 0.9205, 0.0000, 0.3490}

type countTokensRequest struct {
	System   json.RawMessage      `json:"system"`
	Messages []countTokensMessage `json:"messages"`
	Tools    []json.RawMessage    `json:"tools"`
}

type countTokensMessage struct {
	Content json.RawMessage `json:"content"`
}

type contentBlock struct {
	Type     string          `json:"type"`
	Text     string          `json:"text"`
	Thinking string          `json:"thinking"`
	Name     string          `json:"name"`
	Input    json.RawMessage `json:"input"`
	Content  json.RawMessage `json:"content"`
}

// EstimateAnthropic returns a conservative token estimate for an Anthropic
// count_tokens request body. It never returns 0.
func EstimateAnthropic(body []byte, safetyFactor float64) (int, error) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return 0, fmt.Errorf("tokenest: body is not a JSON object")
	}

	var req countTokensRequest
	if err := json.Unmarshal(trimmed, &req); err != nil {
		return 0, fmt.Errorf("tokenest: %w", err)
	}
	if safetyFactor <= 0 {
		safetyFactor = DefaultSafetyFactor
	}

	total := float64(requestOverhead)
	total += countContent(req.System)
	for _, m := range req.Messages {
		total += messageOverhead + countContent(m.Content)
	}
	for _, tool := range req.Tools {
		total += toolOverhead + estimateText(string(tool))
	}

	n := int(math.Ceil(total * safetyFactor))
	if n < 1 {
		n = 1
	}
	return n, nil
}

// countContent counts a string or an array of content blocks.
func countContent(raw json.RawMessage) float64 {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return 0
	}
	switch trimmed[0] {
	case '"':
		var s string
		if err := json.Unmarshal(trimmed, &s); err == nil {
			return estimateText(s)
		}
	case '[':
		var blocks []json.RawMessage
		if err := json.Unmarshal(trimmed, &blocks); err == nil {
			var total float64
			for _, b := range blocks {
				total += countBlock(b)
			}
			return total
		}
	}
	return 0
}

func countBlock(raw json.RawMessage) float64 {
	var b contentBlock
	if err := json.Unmarshal(raw, &b); err != nil {
		return estimateText(string(raw))
	}
	switch b.Type {
	case "text":
		return estimateText(b.Text)
	case "thinking", "redacted_thinking":
		return estimateText(b.Thinking)
	case "tool_use":
		return estimateText(b.Name) + estimateText(string(b.Input))
	case "tool_result":
		return countContent(b.Content)
	case "image":
		return imageTokens
	case "document":
		return documentTokens
	default:
		return estimateText(string(raw))
	}
}

// estimateText is the raw text model used by EstimateAnthropic and by the
// calibration test.
func estimateText(text string) float64 {
	var chars, runs [6]int
	prev := -1
	for _, r := range text {
		c := classOf(r)
		chars[c]++
		if c != prev {
			runs[c]++
		}
		prev = c
	}

	var total float64
	for i := range chars {
		total += charCoef[i]*float64(chars[i]) + runCoef[i]*float64(runs[i])
	}
	return total
}

func classOf(r rune) int {
	switch {
	case r < 0x80:
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
			return 0
		case r >= '0' && r <= '9':
			return 1
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			return 2
		default:
			return 3
		}
	case r >= 0x4E00 && r <= 0x9FFF,
		r >= 0x3040 && r <= 0x30FF,
		r >= 0xAC00 && r <= 0xD7AF:
		return 4
	default:
		return 5
	}
}
