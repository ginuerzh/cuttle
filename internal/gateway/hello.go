package gateway

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/go-gost/core/handler"
	"github.com/go-gost/core/logger"
	"github.com/go-gost/core/metadata"
)

const (
	helloPath        = "/api/hello"
	helloReadTimeout = 15 * time.Second
)

// helloHandler answers Claude Code's connection-warming probe locally.
// Anthropic returns 200 with a small JSON body for GET and a bodyless 200 for
// HEAD; forwarding the probe to an arbitrary provider only produces a 404.
type helloHandler struct {
	next handler.Handler
	log  logger.Logger
}

func newHelloHandler(next handler.Handler, log logger.Logger) *helloHandler {
	return &helloHandler{next: next, log: log}
}

func (h *helloHandler) Init(md metadata.Metadata) error {
	return h.next.Init(md)
}

func (h *helloHandler) Handle(ctx context.Context, conn net.Conn, opts ...handler.HandleOption) error {
	defer conn.Close()

	_ = conn.SetReadDeadline(time.Now().Add(helloReadTimeout))
	br := bufio.NewReader(conn)
	line, err := br.ReadSlice('\n')
	_ = conn.SetReadDeadline(time.Time{})
	if err != nil {
		return h.next.Handle(ctx, &replayConn{Conn: conn, r: io.MultiReader(bytes.NewReader(line), br)}, opts...)
	}
	if !isHelloLine(line) {
		return h.next.Handle(ctx, &replayConn{Conn: conn, r: io.MultiReader(bytes.NewReader(line), br)}, opts...)
	}

	req, err := http.ReadRequest(bufio.NewReader(io.MultiReader(bytes.NewReader(line), br)))
	if err != nil {
		return err
	}
	h.log.Debugf("hello probe: %s %s", req.Method, req.URL.Path)
	return writeHelloResponse(conn, req)
}

func isHelloLine(line []byte) bool {
	fields := strings.Fields(string(line))
	if len(fields) < 2 {
		return false
	}
	if fields[0] != http.MethodHead && fields[0] != http.MethodGet {
		return false
	}
	target := fields[1]
	if i := strings.IndexByte(target, '?'); i >= 0 {
		target = target[:i]
	}
	return target == helloPath
}

func writeHelloResponse(conn net.Conn, req *http.Request) error {
	payload := []byte(`{"message":"hello"}`)
	if req.Method == http.MethodHead {
		payload = nil
	}
	resp := &http.Response{
		StatusCode:    http.StatusOK,
		ProtoMajor:    req.ProtoMajor,
		ProtoMinor:    req.ProtoMinor,
		Header:        http.Header{"Content-Type": {"application/json"}, "Connection": {"close"}},
		Body:          io.NopCloser(bytes.NewReader(payload)),
		ContentLength: int64(len(payload)),
		Close:         true,
	}
	_ = conn.SetWriteDeadline(time.Now().Add(helloReadTimeout))
	return resp.Write(conn)
}
