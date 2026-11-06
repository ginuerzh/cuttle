package stats

import (
	"net"
	"net/http"
	"strings"

	xrecorder "github.com/go-gost/x/recorder"
)

// requestContext extracts bounded, useful correlation fields from the x
// recorder object. It never returns request or response bodies.
func requestContext(ro *xrecorder.HandlerRecorderObject) (uri, method, clientIP, userAgent, upstream string) {
	if ro == nil {
		return "", "", "", "", ""
	}
	if ro.HTTP != nil {
		uri = strings.TrimSpace(ro.HTTP.OriginalURI)
		if uri == "" {
			uri = strings.TrimSpace(ro.HTTP.URI)
		}
		method = strings.TrimSpace(ro.HTTP.Method)
		if headers := clientRequestHeaders(ro); headers != nil {
			userAgent = strings.TrimSpace(headers.Get("User-Agent"))
			clientIP = forwardedClientIP(headers.Get("X-Forwarded-For"), headers.Get("X-Real-IP"))
		}
	}
	if clientIP == "" {
		clientIP = firstIP(ro.ClientIP)
	}
	if clientIP == "" {
		clientIP = firstIP(ro.ClientAddr)
	}
	if clientIP == "" {
		clientIP = firstIP(ro.RemoteAddr)
	}
	if ro.HTTP != nil {
		host := strings.TrimSpace(ro.Host)
		if host == "" {
			host = strings.TrimSpace(ro.DstAddr)
		}
		if host != "" {
			upstream = strings.TrimSuffix(host, "/") + ro.HTTP.URI
		}
	}
	return uri, method, clientIP, userAgent, upstream
}

// clientRequestHeaders returns the headers the client sent. x records
// OriginalRequest before node header rewrites, so it still has the client UA
// after the gateway injects its own upstream User-Agent.
func clientRequestHeaders(ro *xrecorder.HandlerRecorderObject) http.Header {
	if ro == nil || ro.HTTP == nil {
		return nil
	}
	if ro.HTTP.OriginalRequest != nil && len(ro.HTTP.OriginalRequest.Header) > 0 {
		return ro.HTTP.OriginalRequest.Header
	}
	return ro.HTTP.Request.Header
}

// forwardedClientIP returns the first address from X-Forwarded-For, falling
// back to X-Real-IP. Both are client-controlled, so callers must only use them
// for diagnostics, never for authorization.
func forwardedClientIP(forwardedFor, realIP string) string {
	if forwardedFor != "" {
		if first, _, ok := strings.Cut(forwardedFor, ","); ok {
			forwardedFor = first
		}
		if ip := firstIP(forwardedFor); ip != "" {
			return ip
		}
	}
	return firstIP(realIP)
}

func firstIP(addr string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(addr); err == nil {
		return host
	}
	return addr
}
