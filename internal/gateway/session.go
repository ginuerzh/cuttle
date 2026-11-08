package gateway

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
)

// sessionHeaders name the conversation a request belongs to. The list
// follows the headers the supported agents send; the first non-empty value
// wins. Keep it in sync with the clients cuttle serves.
var sessionHeaders = []string{
	"x-claude-code-session-id",
	"x-opencode-session",
	"x-session-affinity",
	"x-session-id",
	"session_id",
	"session-id",
	"x-conversation-id",
	"x-deepseek-harness-session-id",
}

// subagentHeader separates a Claude Code subagent's stick from its lead's.
const subagentHeader = "x-claude-code-agent-id"

// clientIDFromRequest returns the hashed identity of the conversation a
// request belongs to. An empty result means the client named no session; the
// selector then falls back to weighted random selection.
func clientIDFromRequest(req *http.Request) string {
	if req == nil {
		return ""
	}

	var session string
	for _, name := range sessionHeaders {
		if v := strings.TrimSpace(req.Header.Get(name)); v != "" {
			session = v
			break
		}
	}
	if session == "" {
		return ""
	}

	id := "cuttle:" + hashSessionValue(session)
	if agent := strings.TrimSpace(req.Header.Get(subagentHeader)); agent != "" {
		id += "|agent:" + hashSessionValue(agent)
	}
	return id
}

// hashSessionValue keeps raw session and subagent values out of the context
// and bounds the identity's length.
func hashSessionValue(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
