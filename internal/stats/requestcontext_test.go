package stats

import (
	"net/http"
	"testing"

	xrecorder "github.com/go-gost/x/recorder"
)

func TestRequestContext(t *testing.T) {
	ro := &xrecorder.HandlerRecorderObject{
		Host:       "172.67.69.147:443",
		ClientAddr: "10.0.0.5:12345",
		RemoteAddr: "10.0.0.6:54321",
		HTTP: &xrecorder.HTTPRecorderObject{
			Method:      "POST",
			URI:         "/chat/completions",
			OriginalURI: "/v1/messages",
			Request: xrecorder.HTTPRequestRecorderObject{
				Header: http.Header{
					"User-Agent":      []string{"claude-code/1.0"},
					"X-Forwarded-For": []string{"203.0.113.9, 10.0.0.1"},
					"X-Real-Ip":       []string{"198.51.100.7"},
				},
			},
		},
	}
	uri, method, clientIP, userAgent, upstream := requestContext(ro)
	if uri != "/v1/messages" {
		t.Fatalf("uri = %q", uri)
	}
	if method != "POST" {
		t.Fatalf("method = %q", method)
	}
	if clientIP != "203.0.113.9" {
		t.Fatalf("clientIP = %q", clientIP)
	}
	if userAgent != "claude-code/1.0" {
		t.Fatalf("userAgent = %q", userAgent)
	}
	if upstream != "172.67.69.147:443/chat/completions" {
		t.Fatalf("upstream = %q", upstream)
	}
}

func TestRequestContextPrefersOriginalHeaders(t *testing.T) {
	ro := &xrecorder.HandlerRecorderObject{
		HTTP: &xrecorder.HTTPRecorderObject{
			Method: "POST",
			URI:    "/v1/messages",
			Request: xrecorder.HTTPRequestRecorderObject{
				Header: http.Header{
					"User-Agent":      {"cuttle/main-test"},
					"X-Forwarded-For": {"10.0.0.1"},
				},
			},
			OriginalRequest: &xrecorder.HTTPRequestRecorderObject{
				Header: http.Header{
					"User-Agent":      {"claude-cli/2.1.318"},
					"X-Forwarded-For": {"203.0.113.9"},
				},
			},
		},
	}
	_, _, clientIP, userAgent, _ := requestContext(ro)
	if userAgent != "claude-cli/2.1.318" {
		t.Fatalf("userAgent = %q", userAgent)
	}
	if clientIP != "203.0.113.9" {
		t.Fatalf("clientIP = %q", clientIP)
	}
}

func TestRequestContextFallsBackToClientAddr(t *testing.T) {
	ro := &xrecorder.HandlerRecorderObject{
		ClientAddr: "10.0.0.5:12345",
		HTTP: &xrecorder.HTTPRecorderObject{
			Method: "GET",
			URI:    "/v1/models",
		},
	}
	uri, method, clientIP, userAgent, upstream := requestContext(ro)
	if uri != "/v1/models" || method != "GET" {
		t.Fatalf("uri/method = %q/%q", uri, method)
	}
	if clientIP != "10.0.0.5" {
		t.Fatalf("clientIP = %q", clientIP)
	}
	if userAgent != "" || upstream != "" {
		t.Fatalf("userAgent/upstream = %q/%q", userAgent, upstream)
	}
}
