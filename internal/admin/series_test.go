package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/ginuerzh/cuttle/internal/config"
	"github.com/ginuerzh/cuttle/internal/stats"
)

// seriesJSON mirrors the wire shape the console consumes.
type seriesJSON struct {
	Bucket string `json:"bucket"`
	Points []struct {
		Bucket   string `json:"bucket"`
		Requests int64  `json:"requests"`
		Errors   int64  `json:"errors"`
	} `json:"points"`
}

func TestSeriesEndpointBucketsAndFilters(t *testing.T) {
	s, rel, _, ts := newTestAdmin(t)
	s.mu.Lock()
	s.cfg.Entrypoints = []config.Entrypoint{{ID: "e1", Name: "Claude", Match: []string{"claude-*"}}}
	s.mu.Unlock()
	client, _ := login(t, ts.URL)

	base := time.Now().Add(-2 * time.Hour).Truncate(time.Hour)
	for _, row := range []stats.Request{
		{ID: "s1", TS: base.Add(time.Minute), Node: "n1", Provider: "p1", Status: 200, ClientModel: "claude-sonnet"},
		{ID: "s2", TS: base.Add(2 * time.Minute), Node: "n1", Provider: "p1", Status: 429, Error: "slow down", ClientModel: "claude-sonnet"},
		{ID: "s3", TS: base.Add(time.Hour + time.Minute), Node: "n2", Provider: "p1", Status: 200, ClientModel: "gpt-5"},
	} {
		if err := rel.store.Insert(context.Background(), row); err != nil {
			t.Fatal(err)
		}
	}

	get := func(v url.Values) (int, []byte) {
		t.Helper()
		u := ts.URL + "/api/stats/series"
		if len(v) > 0 {
			u += "?" + v.Encode()
		}
		resp := doJSON(t, client, http.MethodGet, u, "", "")
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode, b
	}
	series := func(v url.Values) seriesJSON {
		t.Helper()
		code, b := get(v)
		if code != http.StatusOK {
			t.Fatalf("GET series = %d: %s", code, b)
		}
		var out seriesJSON
		if err := jsonDecode(bytes.NewReader(b), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}

	window := url.Values{
		"from":   {base.UTC().Format(time.RFC3339)},
		"to":     {base.Add(2 * time.Hour).UTC().Format(time.RFC3339)},
		"bucket": {"1h"},
	}

	all := series(window)
	if all.Bucket != "1h0m0s" {
		t.Fatalf("bucket echo = %q, want 1h0m0s", all.Bucket)
	}
	if len(all.Points) != 2 {
		t.Fatalf("points = %+v, want the two non-empty hours", all.Points)
	}
	if all.Points[0].Requests != 2 || all.Points[0].Errors != 1 {
		t.Fatalf("first point = %+v, want 2 requests / 1 error", all.Points[0])
	}
	if all.Points[1].Requests != 1 || all.Points[1].Errors != 0 {
		t.Fatalf("second point = %+v", all.Points[1])
	}
	if all.Points[0].Bucket != base.UTC().Format(time.RFC3339) {
		t.Fatalf("first bucket = %q, want %q", all.Points[0].Bucket, base.UTC().Format(time.RFC3339))
	}

	// The node filter matches the list endpoint's total for the same node.
	byNode := url.Values{}
	for k, v := range window {
		byNode[k] = v
	}
	byNode.Set("node", "n1")
	nodeSeries := series(byNode)
	var sum int64
	for _, p := range nodeSeries.Points {
		sum += p.Requests
	}
	listURL := ts.URL + "/api/requests?" + url.Values{
		"from": {window.Get("from")},
		"to":   {window.Get("to")},
		"node": {"n1"},
	}.Encode()
	resp := doJSON(t, client, http.MethodGet, listURL, "", "")
	rows, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	var list []json.RawMessage
	if err := jsonDecode(bytes.NewReader(rows), &list); err != nil {
		t.Fatal(err)
	}
	if sum != int64(len(list)) {
		t.Fatalf("series sum = %d, list rows = %d, want the same total", sum, len(list))
	}

	// The entrypoint filter is glob-based, exactly like the list endpoint.
	byEntrypoint := url.Values{}
	for k, v := range window {
		byEntrypoint[k] = v
	}
	byEntrypoint.Set("entrypoint", "e1")
	entrySeries := series(byEntrypoint)
	var entrySum int64
	for _, p := range entrySeries.Points {
		entrySum += p.Requests
	}
	if entrySum != 2 {
		t.Fatalf("entrypoint series sum = %d, want the two claude rows", entrySum)
	}

	// An entrypoint that no longer exists yields an empty series, not an error.
	gone := url.Values{}
	for k, v := range window {
		gone[k] = v
	}
	gone.Set("entrypoint", "e_missing")
	code, b := get(gone)
	if code != http.StatusOK {
		t.Fatalf("unknown entrypoint = %d: %s", code, b)
	}
	if got := series(gone); got.Points == nil || len(got.Points) != 0 {
		t.Fatalf("unknown entrypoint points = %+v, want []", got.Points)
	}
	if !bytes.Contains(b, []byte(`"points":[]`)) {
		t.Fatalf("unknown entrypoint body = %s, want an empty points array", b)
	}

	// A bad duration is rejected; an absurd one is clamped.
	bad := url.Values{}
	for k, v := range window {
		bad[k] = v
	}
	bad.Set("bucket", "abc")
	if code, b := get(bad); code != http.StatusBadRequest {
		t.Fatalf("bucket=abc = %d: %s, want 400", code, b)
	}
	// A window that would produce an absurd number of points is rejected, not
	// rendered: the clamp bounds the width, not the count.
	dense := url.Values{}
	for k, v := range window {
		dense[k] = v
	}
	dense.Set("from", base.Add(-168*time.Hour).UTC().Format(time.RFC3339))
	dense.Set("to", base.UTC().Format(time.RFC3339))
	dense.Set("bucket", "1s")
	if code, b := get(dense); code != http.StatusBadRequest {
		t.Fatalf("168h at 1s = %d: %s, want 400", code, b)
	}

	huge := url.Values{}
	for k, v := range window {
		huge[k] = v
	}
	huge.Set("bucket", "1000h")
	if got := series(huge); got.Bucket != "24h0m0s" {
		t.Fatalf("bucket=1000h echo = %q, want the 24h clamp", got.Bucket)
	}
}
