package rewriter

import (
	"bytes"
	"encoding/json"

	"github.com/ginuerzh/cuttle/internal/effort"
)

// clampEffort lowers explicit reasoning-effort fields above ceiling. It never
// injects a missing field, changes a lower value, or rewrites a body that does
// not need clamping.
func clampEffort(body []byte, ceiling string) ([]byte, error) {
	ceilingRank, ok := effort.Rank(ceiling)
	if !ok || len(body) == 0 {
		return body, nil
	}
	if !bytes.Contains(body, []byte("effort")) && !bytes.Contains(body, []byte("thinkingLevel")) {
		return body, nil
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		// Non-JSON request bodies, including SSE-framed bodies, pass through.
		return body, nil
	}

	changed, err := clampStringEffort(raw, "reasoning_effort", ceiling, ceilingRank)
	if err != nil {
		return body, err
	}
	anyChanged := changed
	for _, parent := range []string{"reasoning", "output_config"} {
		changed, err = clampNestedStringEffort(raw, parent, "effort", ceiling, ceilingRank)
		if err != nil {
			return body, err
		}
		anyChanged = anyChanged || changed
	}
	changed, err = clampGeminiEffort(raw, ceiling, ceilingRank)
	if err != nil {
		return body, err
	}
	anyChanged = anyChanged || changed
	if !anyChanged {
		return body, nil
	}

	out, err := json.Marshal(raw)
	if err != nil {
		return body, err
	}
	return out, nil
}

func clampStringEffort(fields map[string]json.RawMessage, field, ceiling string, ceilingRank int) (bool, error) {
	value, ok := fields[field]
	if !ok {
		return false, nil
	}
	var level string
	if err := json.Unmarshal(value, &level); err != nil {
		return false, err
	}
	rank, ok := effort.Rank(level)
	if !ok || rank <= ceilingRank {
		return false, nil
	}
	clamped, err := json.Marshal(ceiling)
	if err != nil {
		return false, err
	}
	fields[field] = clamped
	return true, nil
}

func clampNestedStringEffort(fields map[string]json.RawMessage, parent, field, ceiling string, ceilingRank int) (bool, error) {
	value, ok := fields[parent]
	if !ok {
		return false, nil
	}
	var nested map[string]json.RawMessage
	if err := json.Unmarshal(value, &nested); err != nil {
		return false, err
	}
	changed, err := clampStringEffort(nested, field, ceiling, ceilingRank)
	if err != nil || !changed {
		return false, err
	}
	clamped, err := json.Marshal(nested)
	if err != nil {
		return false, err
	}
	fields[parent] = clamped
	return true, nil
}

func clampGeminiEffort(fields map[string]json.RawMessage, ceiling string, ceilingRank int) (bool, error) {
	value, ok := fields["generationConfig"]
	if !ok {
		return false, nil
	}
	var config map[string]json.RawMessage
	if err := json.Unmarshal(value, &config); err != nil {
		return false, err
	}
	changed, err := clampNestedStringEffort(config, "thinkingConfig", "thinkingLevel", ceiling, ceilingRank)
	if err != nil || !changed {
		return false, err
	}
	clamped, err := json.Marshal(config)
	if err != nil {
		return false, err
	}
	fields["generationConfig"] = clamped
	return true, nil
}
