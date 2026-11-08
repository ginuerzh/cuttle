package gateway

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/go-gost/core/handler"
	"github.com/go-gost/core/logger"
	"github.com/go-gost/core/metadata"

	"github.com/ginuerzh/cuttle/internal/tokenest"
)

const countTokensPath = "/v1/messages/count_tokens"

// Test seams: the production values are 32 MiB and 15s.
var (
	countTokensBodyLimit   = 32 << 20
	countTokensReadTimeout = 15 * time.Second
)

// countTokensHandler answers Anthropic count_tokens requests locally for routes
// whose upstream has no native counter. Every other request is replayed into
// the wrapped forward handler unchanged.
type countTokensHandler struct {
	next   handler.Handler
	policy *atomic.Pointer[countTokensPolicy]
	log    logger.Logger
}

func newCountTokensHandler(next handler.Handler, policy *atomic.Pointer[countTokensPolicy], log logger.Logger) *countTokensHandler {
	return &countTokensHandler{next: next, policy: policy, log: log}
}

func (h *countTokensHandler) Init(md metadata.Metadata) error {
	return h.next.Init(md)
}

func (h *countTokensHandler) Handle(ctx context.Context, conn net.Conn, opts ...handler.HandleOption) error {
	defer conn.Close()

	_ = conn.SetReadDeadline(time.Now().Add(countTokensReadTimeout))
	br := bufio.NewReader(conn)
	head, err := br.Peek(5)
	if err != nil || string(head) != "POST " {
		_ = conn.SetReadDeadline(time.Time{})
		return h.next.Handle(ctx, &bufferedConn{Conn: conn, r: br}, opts...)
	}

	line, err := br.ReadSlice('\n')
	_ = conn.SetReadDeadline(time.Time{})
	if err != nil {
		return h.next.Handle(ctx, &replayConn{Conn: conn, r: io.MultiReader(bytes.NewReader(line), br)}, opts...)
	}
	if !isCountTokensLine(line) {
		return h.next.Handle(ctx, &replayConn{Conn: conn, r: io.MultiReader(bytes.NewReader(line), br)}, opts...)
	}

	_ = conn.SetReadDeadline(time.Now().Add(countTokensReadTimeout))
	req, err := http.ReadRequest(bufio.NewReader(io.MultiReader(bytes.NewReader(line), br)))
	if err != nil {
		_ = conn.SetReadDeadline(time.Time{})
		return err
	}

	if strings.EqualFold(req.Header.Get("Expect"), "100-continue") {
		if _, err := io.WriteString(conn, "HTTP/1.1 100 Continue\r\n\r\n"); err != nil {
			return err
		}
	}

	body, err := io.ReadAll(io.LimitReader(req.Body, int64(countTokensBodyLimit)+1))
	_ = conn.SetReadDeadline(time.Time{})
	if err != nil {
		return err
	}
	if len(body) > countTokensBodyLimit {
		return writeCountTokensError(conn, req, http.StatusRequestEntityTooLarge, "count_tokens body exceeds 32 MiB")
	}

	if enc := req.Header.Get("Content-Encoding"); enc != "" && !strings.EqualFold(enc, "identity") {
		return h.delegate(ctx, conn, req, body, opts...)
	}

	n, err := tokenest.EstimateAnthropic(body, tokenest.DefaultSafetyFactor)
	if err != nil {
		return h.delegate(ctx, conn, req, body, opts...)
	}

	var v struct {
		Model string `json:"model"`
	}
	_ = json.Unmarshal(body, &v)
	if !h.policy.Load().estimateFor(v.Model) {
		return h.delegate(ctx, conn, req, body, opts...)
	}

	h.log.Debugf("count_tokens estimate: model=%q tokens=%d bytes=%d", v.Model, n, len(body))
	return writeCountTokensOK(conn, req, n)
}

func (h *countTokensHandler) delegate(ctx context.Context, conn net.Conn, req *http.Request, body []byte, opts ...handler.HandleOption) error {
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	req.TransferEncoding = nil

	var buf bytes.Buffer
	if err := req.Write(&buf); err != nil {
		return err
	}
	return h.next.Handle(ctx, &replayConn{Conn: conn, r: bytes.NewReader(buf.Bytes())}, opts...)
}

func isCountTokensLine(line []byte) bool {
	fields := strings.Fields(string(line))
	if len(fields) < 2 || fields[0] != http.MethodPost {
		return false
	}
	target := fields[1]
	if i := strings.IndexByte(target, '?'); i >= 0 {
		target = target[:i]
	}
	return target == countTokensPath
}

func writeCountTokensOK(conn net.Conn, req *http.Request, n int) error {
	payload, err := json.Marshal(map[string]int{"input_tokens": n})
	if err != nil {
		return err
	}
	return writeCountTokensResponse(conn, req, http.StatusOK, payload)
}

func writeCountTokensError(conn net.Conn, req *http.Request, status int, message string) error {
	payload, err := json.Marshal(map[string]any{
		"type": "error",
		"error": map[string]string{
			"type":    "invalid_request_error",
			"message": message,
		},
	})
	if err != nil {
		return err
	}
	return writeCountTokensResponse(conn, req, status, payload)
}

func writeCountTokensResponse(conn net.Conn, req *http.Request, status int, payload []byte) error {
	resp := &http.Response{
		StatusCode:    status,
		ProtoMajor:    req.ProtoMajor,
		ProtoMinor:    req.ProtoMinor,
		Header:        http.Header{"Content-Type": {"application/json"}, "Connection": {"close"}},
		Body:          io.NopCloser(bytes.NewReader(payload)),
		ContentLength: int64(len(payload)),
		Close:         true,
	}
	_ = conn.SetWriteDeadline(time.Now().Add(countTokensReadTimeout))
	return resp.Write(conn)
}

type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) {
	return c.r.Read(p)
}

type replayConn struct {
	net.Conn
	r io.Reader
}

func (c *replayConn) Read(p []byte) (int, error) {
	return c.r.Read(p)
}
