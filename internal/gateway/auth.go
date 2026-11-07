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
	// the auth handler. The body is never buffered here.
	gatewayAuthHeaderLimit = 1 << 20
	gatewayAuthReadTimeout = 15 * time.Second
)

var (
	gatewayAuthErrorBody     = []byte(`{"error":{"message":"invalid gateway key","type":"authentication_error"}}`)
	gatewayMalformedHeadBody = []byte(`{"error":{"message":"malformed request","type":"invalid_request_error"}}`)

	errGatewayAuthHeadTooLarge = errors.New("request head too large")
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
// non-empty. It buffers only the request head, replays it byte for byte and
// passes the untouched buffered stream to the next handler.
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

	name, reason, authenticated := policy.authenticate(gatewayAuthCandidates(header))
	if !authenticated {
		h.log.Warnf("gateway auth: %s %s from %s: %s", method, target, gatewayAuthRemoteAddr(conn), reason)
		return writeGatewayAuthResponse(conn, http.StatusUnauthorized, gatewayAuthErrorBody, true)
	}

	nextConn := &replayConn{Conn: conn, r: io.MultiReader(bytes.NewReader(head), br)}
	return h.next.Handle(stats.WithGatewayKey(ctx, name), nextConn, opts...)
}

// readGatewayAuthHead reads the request line and headers, including the blank
// line that terminates the head. It never reads the body.
func readGatewayAuthHead(br *bufio.Reader) ([]byte, error) {
	head := make([]byte, 0, 512)
	for {
		line, err := br.ReadBytes('\n')
		if len(head)+len(line) > gatewayAuthHeaderLimit {
			return head, errGatewayAuthHeadTooLarge
		}
		head = append(head, line...)
		if err != nil {
			return head, err
		}
		if isBlankGatewayAuthLine(line) {
			return head, nil
		}
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
