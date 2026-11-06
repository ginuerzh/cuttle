package rewriter

import (
	"bufio"
	"bytes"
	"context"
	"net/http"
	"net/textproto"

	corerewriter "github.com/go-gost/core/rewriter"
)

// RequestIDHeaderName is the response header that carries the gateway's
// per-request correlation ID.
const RequestIDHeaderName = "X-LLM-GW-Request-ID"

// RequestIDRewriterName is the x rewriter-registry name for HeaderRewriter.
const RequestIDRewriterName = "llm-gw-request-id"

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
