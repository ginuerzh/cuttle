package gateway

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	gwconfig "github.com/ginuerzh/cuttle/internal/config"
)

func slowStreamServer(t *testing.T, tag string) *httptest.Server {
	t.Helper()

	srv := testUpstream(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		for i := 0; i < 10; i++ {
			fmt.Fprintf(w, "data: %s-%d\n\n", tag, i)
			if flusher != nil {
				flusher.Flush()
			}
			time.Sleep(20 * time.Millisecond)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestReloadKeepsInflightStreamAndSwitchesNewRequests(t *testing.T) {
	upA := slowStreamServer(t, "A")
	upB := testUpstream(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("B"))
	}))
	defer upB.Close()

	gw := startGateway(t, testConfig(upA.URL))

	resp, err := http.Post("http://"+gw.Addr().String()+"/stream", "application/json",
		strings.NewReader(`{"model":"deepseek-chat","messages":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	br := bufio.NewReader(resp.Body)
	first, err := br.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(first, "data: A-0") {
		t.Fatalf("first event = %q, want data: A-0", first)
	}

	if err := gw.Reload(testConfig(upB.URL)); err != nil {
		t.Fatalf("reload: %v", err)
	}

	rest, err := io.ReadAll(br)
	if err != nil {
		t.Fatal(err)
	}
	got := first + string(rest)
	if n := strings.Count(got, "data: A-"); n != 10 {
		t.Fatalf("in-flight stream events = %d, want 10 (stream was interrupted): %q", n, got)
	}

	if body := getBody(t, gw); body != "B" {
		t.Fatalf("new request body = %q, want B", body)
	}

	bad := testConfig(upB.URL)
	bad.Routes[0].Nodes = []string{"missing"}
	if err := gw.Reload(bad); err == nil {
		t.Fatal("reload with an unknown provider should fail")
	}
	if body := getBody(t, gw); body != "B" {
		t.Fatalf("after failed reload body = %q, want B (old hop must keep serving)", body)
	}
}

func TestReloadDoesNotWaitForCatalogRefresh(t *testing.T) {
	var calls atomic.Int32
	refreshStarted := make(chan struct{})
	catalog := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			fmt.Fprint(w, `{"data":[{"id":"deepseek-chat","limit":{"output":8192}}]}`)
			return
		}
		close(refreshStarted)
		<-r.Context().Done()
	}))
	t.Cleanup(catalog.Close)

	cfg := testConfig(catalog.URL)
	gw, err := New(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = gw.Close() })

	// A new provider has no cached catalog. Reload must build with no cap and
	// refresh in the background instead of waiting for the blocked endpoint.
	next := testConfig(catalog.URL)
	next.Providers[0].ID = "p_0000000002"
	next.Nodes[0].Provider = next.Providers[0].ID

	started := time.Now()
	if err := gw.Reload(next); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		t.Fatalf("Reload() with blocked catalog refresh took %s, want <= 250ms", elapsed)
	}
	waitForCatalogSignal(t, refreshStarted, "background catalog refresh")
}

func getBody(t *testing.T, gw *Gateway) string {
	t.Helper()

	resp, err := http.Post("http://"+gw.Addr().String()+"/", "application/json",
		strings.NewReader(`{"model":"deepseek-chat","messages":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// keep gwconfig imported for the package's other files.
var _ = gwconfig.Config{}

func TestReloadAppliesCapturePolicy(t *testing.T) {
	up := testUpstream(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	defer up.Close()

	cfg := testConfig(up.URL)
	cfg.Stats.Capture = gwconfig.Capture{
		Mode:     "errors",
		MaxBytes: 1048576,
		Retain:   gwconfig.Duration(24 * time.Hour),
	}
	gw := startGateway(t, cfg)
	m := gw.Capture()
	if _, ok := m.Start("s", "r", "m"); !ok {
		t.Fatal("not captured before reload")
	}
	m.Offer("s", "request", []byte("abcdef"), []byte("ghijkl"), "", 0, 0)
	cap, _ := m.Finish("s")
	if len(cap.TruncatedFields) != 0 {
		t.Fatalf("unexpected truncation before reload: %+v", cap.TruncatedFields)
	}

	cfg = testConfig(up.URL)
	cfg.Stats.Capture = gwconfig.Capture{
		Mode:     "errors",
		MaxBytes: 4,
		Retain:   gwconfig.Duration(24 * time.Hour),
	}
	if err := gw.Reload(cfg); err != nil {
		t.Fatal(err)
	}

	m = gw.Capture()
	if _, ok := m.Start("s2", "r", "m"); !ok {
		t.Fatal("not captured after reload")
	}
	m.Offer("s2", "request", []byte("abcdef"), []byte("ghijkl"), "", 0, 0)
	cap, _ = m.Finish("s2")
	if cap.RequestOriginal != "abcd" {
		t.Fatalf("requestOriginal = %q, want abcd", cap.RequestOriginal)
	}
	if len(cap.TruncatedFields) != 2 {
		t.Fatalf("truncatedFields = %+v", cap.TruncatedFields)
	}
}
