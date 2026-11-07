package gateway

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"io"
	"net"
	"net/http"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/go-gost/core/handler"
	"github.com/go-gost/core/logger"
	"github.com/go-gost/core/metadata"

	gwconfig "github.com/go-gost/llm-gw/internal/config"
	"github.com/go-gost/llm-gw/internal/stats"
)

const (
	// gatewayAuthHeaderLimit bounds the request line plus headers buffered by
	// the auth handler. The bufio.Reader may also hold body bytes beyond the
	// head; those are replayed to the next handler and are never buffered in
	// full here.
	gatewayAuthHeaderLimit = 1 << 20
	gatewayAuthReadTimeout = 15 * time.Second
	// gatewayAuthChunkLineLimit bounds a chunk-size line or trailer line read
	// while passing a chunked body through. Chunk data itself is streamed.
	gatewayAuthChunkLineLimit = 32 << 10
)

var (
	gatewayAuthErrorBody     = []byte(`{"error":{"message":"invalid gateway key","type":"authentication_error"}}`)
	gatewayMalformedHeadBody = []byte(`{"error":{"message":"malformed request","type":"invalid_request_error"}}`)

	errGatewayAuthHeadTooLarge            = errors.New("request head too large")
	errGatewayAuthInvalidTransferEncoding = errors.New("invalid transfer-encoding")
	errGatewayAuthInvalidContentLength    = errors.New("invalid content-length")
	errGatewayAuthInvalidChunkedBody      = errors.New("invalid chunked body")
)

// authPolicy is the compiled form of gateway.keys. Disabled entries stay in
// the policy so the failure reason can distinguish a disabled key from an
// unknown one.
type authPolicy struct {
	required bool
	entries  []authEntry
}

type authEntry struct {
	name    string
	digest  [32]byte
	enabled bool
}

func newAuthPolicy(keys []gwconfig.GatewayKey) *authPolicy {
	p := &authPolicy{required: len(keys) > 0}
	for _, key := range keys {
		if key.Key == "" {
			// Validation rejects empty keys; skip them defensively so an
			// invalid config cannot authenticate an empty credential.
			continue
		}
		p.entries = append(p.entries, authEntry{
			name:    key.Name,
			digest:  sha256.Sum256([]byte(key.Key)),
			enabled: key.Enabled == nil || *key.Enabled,
		})
	}
	return p
}

func (p *authPolicy) hasEnabled() bool {
	if p == nil {
		return false
	}
	for _, entry := range p.entries {
		if entry.enabled {
			return true
		}
	}
	return false
}

// authenticate compares every candidate with every entry. It never exits on
// the first match so timing does not reveal the matching position. Attribution
// goes to the first enabled matching entry in configuration order; a disabled
// match only changes the failure reason.
func (p *authPolicy) authenticate(candidates []string) (name, reason string, ok bool) {
	if len(candidates) == 0 {
		return "", "missing", false
	}

	digests := make([][32]byte, len(candidates))
	for i, candidate := range candidates {
		digests[i] = sha256.Sum256([]byte(candidate))
	}

	var (
		matchedName string
		matched     bool
		disabledHit bool
	)
	for _, entry := range p.entries {
		var hit int
		for i := range digests {
			hit |= subtle.ConstantTimeCompare(entry.digest[:], digests[i][:])
		}
		if hit != 1 {
			continue
		}
		if entry.enabled {
			if !matched {
				matchedName = entry.name
				matched = true
			}
			continue
		}
		disabledHit = true
	}

	if matched {
		return matchedName, "", true
	}
	if disabledHit {
		return "", "disabled", false
	}
	return "", "invalid", false
}

// gatewayAuthHandler authenticates data-plane requests when gateway.keys is
// non-empty. It buffers the request head and replays it byte for byte together
// with the buffered body bytes. The next handler receives a connection that
// yields exactly one request and then EOF, so x's keep-alive loop cannot serve
// a second request on the connection without re-authenticating it.
type gatewayAuthHandler struct {
	next   handler.Handler
	policy *atomic.Pointer[authPolicy]
	log    logger.Logger
}

func newGatewayAuthHandler(next handler.Handler, policy *atomic.Pointer[authPolicy], log logger.Logger) *gatewayAuthHandler {
	return &gatewayAuthHandler{next: next, policy: policy, log: log}
}

func (h *gatewayAuthHandler) Init(md metadata.Metadata) error {
	return h.next.Init(md)
}

func (h *gatewayAuthHandler) Handle(ctx context.Context, conn net.Conn, opts ...handler.HandleOption) error {
	defer conn.Close()

	policy := h.policy.Load()
	if policy == nil || !policy.required {
		return h.next.Handle(ctx, conn, opts...)
	}

	_ = conn.SetReadDeadline(time.Now().Add(gatewayAuthReadTimeout))
	br := bufio.NewReader(conn)
	head, err := readGatewayAuthHead(br)
	_ = conn.SetReadDeadline(time.Time{})
	if err != nil {
		h.log.Warnf("gateway auth: malformed request from %s: %v", gatewayAuthRemoteAddr(conn), err)
		return writeGatewayAuthResponse(conn, http.StatusBadRequest, gatewayMalformedHeadBody, false)
	}

	method, target, header, ok := parseGatewayAuthHead(head)
	if !ok {
		h.log.Warnf("gateway auth: malformed request from %s", gatewayAuthRemoteAddr(conn))
		return writeGatewayAuthResponse(conn, http.StatusBadRequest, gatewayMalformedHeadBody, false)
	}

	// Log only the path: a query string can carry a credential. The method is
	// sanitized because it is client-controlled and reaches the log line.
	logMethod := sanitizeGatewayAuthMethod(method)
	logTarget := gatewayAuthLogTarget(target)

	body, err := gatewayAuthBodyReader(br, header)
	if err != nil {
		h.log.Warnf("gateway auth: %s %s from %s: %v", logMethod, logTarget, gatewayAuthRemoteAddr(conn), err)
		return writeGatewayAuthResponse(conn, http.StatusBadRequest, gatewayMalformedHeadBody, false)
	}

	name, reason, authenticated := policy.authenticate(gatewayAuthCandidates(header))
	if !authenticated {
		h.log.Warnf("gateway auth: %s %s from %s: %s", logMethod, logTarget, gatewayAuthRemoteAddr(conn), reason)
		return writeGatewayAuthResponse(conn, http.StatusUnauthorized, gatewayAuthErrorBody, true)
	}

	// Yield the first request and then EOF. x would otherwise loop and
	// process a second request on the already-authenticated connection
	// without re-entering this handler, even though responses advertise
	// Connection: close.
	nextConn := &replayConn{Conn: conn, r: io.MultiReader(bytes.NewReader(head), body)}
	return h.next.Handle(stats.WithGatewayKey(ctx, name), nextConn, opts...)
}

// gatewayAuthBodyReader returns a reader that reproduces the framed request
// body exactly as received and then returns io.EOF. Content-Length bodies are
// bounded with io.LimitReader; chunked bodies are passed through chunk by
// chunk. A body can therefore never extend past its declared boundary into the
// next request on the same connection.
func gatewayAuthBodyReader(br *bufio.Reader, header textproto.MIMEHeader) (io.Reader, error) {
	if values := header.Values("Transfer-Encoding"); len(values) > 0 {
		encodings := strings.Split(strings.Join(values, ","), ",")
		chunked := false
		for i, encoding := range encodings {
			if !strings.EqualFold(strings.TrimSpace(encoding), "chunked") {
				continue
			}
			if i != len(encodings)-1 {
				return nil, errGatewayAuthInvalidTransferEncoding
			}
			chunked = true
		}
		if !chunked {
			return nil, errGatewayAuthInvalidTransferEncoding
		}
		// Transfer-Encoding takes precedence over Content-Length.
		return newGatewayAuthChunkedReader(br), nil
	}

	values := header.Values("Content-Length")
	if len(values) == 0 {
		return bytes.NewReader(nil), nil
	}
	if len(values) != 1 {
		return nil, errGatewayAuthInvalidContentLength
	}
	value := strings.TrimSpace(values[0])
	if value == "" {
		return nil, errGatewayAuthInvalidContentLength
	}
	for i := 0; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			return nil, errGatewayAuthInvalidContentLength
		}
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return nil, errGatewayAuthInvalidContentLength
	}
	return io.LimitReader(br, n), nil
}

// gatewayAuthLogTarget returns only the path of a request target for logging.
// Query strings can carry credentials, userinfo can carry a password, and the
// decoded path can hide control characters; none of them may reach the logs.
// The raw target is never returned.
func gatewayAuthLogTarget(target string) string {
	if target == "*" {
		return "*"
	}
	u, err := url.Parse(target)
	if err != nil {
		return "/"
	}
	if u.Path != "" {
		// Re-escape from the decoded path instead of trusting RawPath: a
		// lower-case escape such as %0a would otherwise be preserved, and
		// control bytes must always be rendered as visible escapes.
		return (&url.URL{Path: u.Path}).EscapedPath()
	}
	return "/"
}

// sanitizeGatewayAuthMethod returns the request method when it is printable
// ASCII and a fixed placeholder otherwise. The method is client-controlled and
// is interpolated into a log line.
func sanitizeGatewayAuthMethod(method string) string {
	for i := 0; i < len(method); i++ {
		if method[i] < 0x21 || method[i] > 0x7e {
			return "-"
		}
	}
	return method
}

// readGatewayAuthHead reads the request line and headers, including the blank
// line that terminates the head. It never reads the body. Each logical line is
// accumulated across bufio.ErrBufferFull fragments before it is appended to the
// head and tested for blankness, and the accumulated size is checked before
// every append so a single unterminated line cannot allocate more than the
// limit plus one bufio buffer.
func readGatewayAuthHead(br *bufio.Reader) ([]byte, error) {
	head := make([]byte, 0, 512)
	line := make([]byte, 0, 512)
	for {
		frag, err := br.ReadSlice('\n')
		if len(head)+len(line)+len(frag) > gatewayAuthHeaderLimit {
			return head, errGatewayAuthHeadTooLarge
		}
		line = append(line, frag...)
		if err == bufio.ErrBufferFull {
			continue
		}
		head = append(head, line...)
		if err != nil {
			return head, err
		}
		if isBlankGatewayAuthLine(line) {
			return head, nil
		}
		line = line[:0]
	}
}

func isBlankGatewayAuthLine(line []byte) bool {
	return bytes.Equal(line, []byte("\r\n")) || bytes.Equal(line, []byte("\n"))
}

// parseGatewayAuthHead validates the request line and parses the header block.
// It returns the method and target for logging, or ok=false for a malformed
// head.
func parseGatewayAuthHead(head []byte) (method, target string, header textproto.MIMEHeader, ok bool) {
	lineEnd := bytes.IndexByte(head, '\n')
	if lineEnd < 0 {
		return "", "", nil, false
	}
	fields := strings.Fields(strings.TrimSuffix(string(head[:lineEnd]), "\r"))
	if len(fields) != 3 {
		return "", "", nil, false
	}
	if _, _, valid := http.ParseHTTPVersion(fields[2]); !valid {
		return "", "", nil, false
	}

	header, err := textproto.NewReader(bufio.NewReader(bytes.NewReader(head[lineEnd+1:]))).ReadMIMEHeader()
	if err != nil {
		return "", "", nil, false
	}
	return fields[0], fields[1], header, true
}

// gatewayAuthCandidates collects every supported credential shape. A request
// is accepted when any candidate matches; a junk value in one header does not
// veto a valid value in another.
func gatewayAuthCandidates(header textproto.MIMEHeader) []string {
	var candidates []string
	add := func(value string) {
		if value = strings.TrimSpace(value); value != "" {
			candidates = append(candidates, value)
		}
	}

	for _, value := range header.Values("Authorization") {
		i := strings.IndexFunc(value, func(r rune) bool { return r == ' ' || r == '\t' })
		if i <= 0 || !strings.EqualFold(value[:i], "Bearer") {
			continue
		}
		add(value[i+1:])
	}
	for _, name := range []string{"x-api-key", "x-goog-api-key", "api-key"} {
		for _, value := range header.Values(name) {
			add(value)
		}
	}
	return candidates
}

type gatewayAuthChunkState int

const (
	gatewayAuthChunkSize gatewayAuthChunkState = iota
	gatewayAuthChunkData
	gatewayAuthChunkDataEnd
	gatewayAuthChunkTrailer
	gatewayAuthChunkDone
)

// gatewayAuthChunkedReader passes a chunked body through without decoding it:
// it yields the chunk-size lines, the data bytes, the CRLF separators and the
// trailer section exactly as received, then returns io.EOF after the terminal
// chunk. Only one chunk-size or trailer line is buffered at a time.
type gatewayAuthChunkedReader struct {
	br        *bufio.Reader
	pending   []byte
	remaining int64
	state     gatewayAuthChunkState
}

func newGatewayAuthChunkedReader(br *bufio.Reader) io.Reader {
	return &gatewayAuthChunkedReader{br: br, state: gatewayAuthChunkSize}
}

func (r *gatewayAuthChunkedReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	for {
		if len(r.pending) > 0 {
			n := copy(p, r.pending)
			r.pending = r.pending[n:]
			return n, nil
		}

		switch r.state {
		case gatewayAuthChunkSize:
			line, err := readGatewayAuthChunkLine(r.br)
			if err != nil {
				return 0, err
			}
			size, err := parseGatewayAuthChunkSize(line)
			if err != nil {
				return 0, err
			}
			r.pending = line
			if size == 0 {
				r.state = gatewayAuthChunkTrailer
			} else {
				r.remaining = size
				r.state = gatewayAuthChunkData
			}

		case gatewayAuthChunkData:
			if r.remaining == 0 {
				r.state = gatewayAuthChunkDataEnd
				continue
			}
			n := len(p)
			if int64(n) > r.remaining {
				n = int(r.remaining)
			}
			nr, err := r.br.Read(p[:n])
			if nr > 0 {
				r.remaining -= int64(nr)
			}
			if nr > 0 || err != nil {
				return nr, err
			}

		case gatewayAuthChunkDataEnd:
			line, err := readGatewayAuthChunkLine(r.br)
			if err != nil {
				return 0, err
			}
			if !isBlankGatewayAuthLine(line) {
				return 0, errGatewayAuthInvalidChunkedBody
			}
			r.pending = line
			r.state = gatewayAuthChunkSize

		case gatewayAuthChunkTrailer:
			line, err := readGatewayAuthChunkLine(r.br)
			if err != nil {
				return 0, err
			}
			r.pending = line
			if isBlankGatewayAuthLine(line) {
				r.state = gatewayAuthChunkDone
			}

		case gatewayAuthChunkDone:
			return 0, io.EOF
		}
	}
}

// readGatewayAuthChunkLine reads one chunk header or trailer line including
// its terminating newline, capped at gatewayAuthChunkLineLimit.
func readGatewayAuthChunkLine(br *bufio.Reader) ([]byte, error) {
	line := make([]byte, 0, 64)
	for {
		frag, err := br.ReadSlice('\n')
		line = append(line, frag...)
		if len(line) > gatewayAuthChunkLineLimit {
			return nil, errGatewayAuthInvalidChunkedBody
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		if err != nil {
			return nil, err
		}
		return line, nil
	}
}

// parseGatewayAuthChunkSize parses the hexadecimal size before any chunk
// extension. The line still includes its CRLF.
func parseGatewayAuthChunkSize(line []byte) (int64, error) {
	line = bytes.TrimSuffix(line, []byte("\n"))
	line = bytes.TrimSuffix(line, []byte("\r"))
	if i := bytes.IndexByte(line, ';'); i >= 0 {
		line = line[:i]
	}
	line = bytes.TrimSpace(line)
	if len(line) == 0 || len(line) > 16 {
		return 0, errGatewayAuthInvalidChunkedBody
	}
	n, err := strconv.ParseInt(string(line), 16, 64)
	if err != nil || n < 0 {
		return 0, errGatewayAuthInvalidChunkedBody
	}
	return n, nil
}

func writeGatewayAuthResponse(conn net.Conn, status int, payload []byte, challenge bool) error {
	header := http.Header{
		"Content-Type": {"application/json"},
		"Connection":   {"close"},
	}
	if challenge {
		header.Set("WWW-Authenticate", `Bearer realm="llm-gw"`)
	}
	resp := &http.Response{
		StatusCode:    status,
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        header,
		Body:          io.NopCloser(bytes.NewReader(payload)),
		ContentLength: int64(len(payload)),
		Close:         true,
	}
	_ = conn.SetWriteDeadline(time.Now().Add(gatewayAuthReadTimeout))
	return resp.Write(conn)
}

func gatewayAuthRemoteAddr(conn net.Conn) string {
	if addr := conn.RemoteAddr(); addr != nil {
		return addr.String()
	}
	return "unknown"
}
