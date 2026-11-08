package rewriter

import (
	"bufio"
	"bytes"
	"context"
	"net/http"
	"net/textproto"
	"strings"

	corerewriter "github.com/go-gost/core/rewriter"
)

// RequestIDHeaderName is the response header that carries the gateway's
// per-request correlation ID.
const RequestIDHeaderName = "X-Cuttle-Request-ID"

// RequestIDRewriterName is the x rewriter-registry name for HeaderRewriter.
const RequestIDRewriterName = "cuttle-request-id"

// OpenCodeSessionHeaderName is the header OpenCode Go uses for session
// affinity. Clients do not reliably send it; the gateway injects a fallback.
const OpenCodeSessionHeaderName = "x-opencode-session"

// OpenCodeSessionRewriterName is the x rewriter-registry name for
// OpenCodeSessionRewriter.
const OpenCodeSessionRewriterName = "cuttle-opencode-session"

// HeaderRewriter injects the x session ID into an HTTP header block. It is
// registered as an x header-rewrite plugin and fails open: a parse error
// returns the original header block so a diagnostic header cannot break a
// response.
type HeaderRewriter struct{}

// Rewrite implements core/rewriter.Rewriter.
func (r *HeaderRewriter) Rewrite(ctx context.Context, b []byte, opts ...corerewriter.RewriteOption) ([]byte, error) {
	_ = ctx

	var ro corerewriter.RewriteOptions
	for _, opt := range opts {
		opt(&ro)
	}
	md, _ := ro.Metadata.(map[string]any)
	sid := stringValue(md["sid"])
	if sid == "" {
		return b, nil
	}

	header, err := textproto.NewReader(bufio.NewReader(bytes.NewReader(b))).ReadMIMEHeader()
	if err != nil {
		return b, nil
	}
	h := http.Header(header)
	h.Set(RequestIDHeaderName, sid)

	var out bytes.Buffer
	if err := h.Write(&out); err != nil {
		return b, nil
	}
	out.WriteString("\r\n")
	return out.Bytes(), nil
}

// OpenCodeSessionRewriter adds a fallback x-opencode-session header using the
// gateway request ID. It leaves an existing client session intact and fails
// open, like HeaderRewriter.
type OpenCodeSessionRewriter struct{}

// Rewrite implements core/rewriter.Rewriter.
func (r *OpenCodeSessionRewriter) Rewrite(ctx context.Context, b []byte, opts ...corerewriter.RewriteOption) ([]byte, error) {
	_ = ctx

	var ro corerewriter.RewriteOptions
	for _, opt := range opts {
		opt(&ro)
	}
	md, _ := ro.Metadata.(map[string]any)
	sid := stringValue(md["sid"])
	if sid == "" {
		return b, nil
	}

	header, err := textproto.NewReader(bufio.NewReader(bytes.NewReader(b))).ReadMIMEHeader()
	if err != nil {
		return b, nil
	}
	h := http.Header(header)
	if hasSessionHeader(h) {
		return b, nil
	}
	h.Set(OpenCodeSessionHeaderName, sid)

	var out bytes.Buffer
	if err := h.Write(&out); err != nil {
		return b, nil
	}
	out.WriteString("\r\n")
	return out.Bytes(), nil
}

// hasSessionHeader reports whether the client already sent a session-like
// header. Claude Code sends its own native session header, which OpenCode Go
// recognizes; injecting x-opencode-session on top could override it and hurt
// routing/caching. Only clients with no session header at all need the
// fallback.
func hasSessionHeader(h http.Header) bool {
	for name, values := range h {
		if !strings.Contains(strings.ToLower(name), "session") {
			continue
		}
		if strings.TrimSpace(strings.Join(values, "")) != "" {
			return true
		}
	}
	return false
}
