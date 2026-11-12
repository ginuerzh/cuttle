package admin

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ginuerzh/cuttle/internal/config"
)

func TestSampleUsageOnceRecordsPoints(t *testing.T) {
	srv, _, _, _ := newTestAdmin(t)
	var amount atomic.Value
	amount.Store("11.12")
	vendor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"balance_infos":[{"currency":"USD","total_balance":%q}]}`, amount.Load())
	}))
	defer vendor.Close()
	setUsageProviders(t, srv, config.Provider{
		Name: "deepseek", Type: "deepseek", Chat: vendor.URL + "/v1",
		Keys: []config.ProviderKey{{Name: "key-1", Key: "sk-1"}},
	})

	base := time.Now().UTC()
	srv.SampleUsageOnce(context.Background(), base)
	amount.Store("10.50")
	srv.SampleUsageOnce(context.Background(), base.Add(10*time.Minute))

	groups, err := srv.stats.BalanceSeries(context.Background(), base.Add(-time.Minute), base.Add(11*time.Minute), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 || len(groups[0].Points) != 2 {
		t.Fatalf("groups = %+v, want two points", groups)
	}
	if got := groups[0].Points[1].Amount; got != 10.50 {
		t.Fatalf("second amount = %v", got)
	}
}

func TestSampleUsageOnceSkipsDisabledProvider(t *testing.T) {
	srv, _, _, _ := newTestAdmin(t)
	var hits atomic.Int32
	vendor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Write([]byte(`{"balance_infos":[{"currency":"USD","total_balance":"1"}]}`))
	}))
	defer vendor.Close()
	disabled := false
	setUsageProviders(t, srv, config.Provider{
		Name: "deepseek", Type: "deepseek", Chat: vendor.URL + "/v1", Enabled: &disabled,
		Keys: []config.ProviderKey{{Name: "key-1", Key: "sk-1"}},
	})

	srv.SampleUsageOnce(context.Background(), time.Now().UTC())
	if got := hits.Load(); got != 0 {
		t.Fatalf("upstream hits = %d, want 0 for a disabled provider", got)
	}
}

func TestSampleUsageOnceIgnoresFailedRead(t *testing.T) {
	srv, _, _, _ := newTestAdmin(t)
	vendor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer vendor.Close()
	setUsageProviders(t, srv, config.Provider{
		Name: "deepseek", Type: "deepseek", Chat: vendor.URL + "/v1",
		Keys: []config.ProviderKey{{Name: "key-1", Key: "sk-1"}},
	})

	now := time.Now().UTC()
	srv.SampleUsageOnce(context.Background(), now)
	groups, err := srv.stats.BalanceSeries(context.Background(), now.Add(-time.Minute), now.Add(time.Minute), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 0 {
		t.Fatalf("groups = %+v, want no reading for a failed read", groups)
	}
}

func TestUsageSamplerOffWhenIntervalZero(t *testing.T) {
	srv, _, _, _ := newTestAdmin(t)
	var hits atomic.Int32
	vendor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Write([]byte(`{"balance_infos":[{"currency":"USD","total_balance":"1"}]}`))
	}))
	defer vendor.Close()
	setUsageProviders(t, srv, config.Provider{
		Name: "deepseek", Type: "deepseek", Chat: vendor.URL + "/v1",
		Keys: []config.ProviderKey{{Name: "key-1", Key: "sk-1"}},
	})
	off := config.Duration(0)
	srv.mu.Lock()
	srv.cfg.Usage.SampleInterval = &off
	srv.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	go srv.StartUsageSampler(ctx)
	time.Sleep(60 * time.Millisecond)
	cancel()

	if got := hits.Load(); got != 0 {
		t.Fatalf("upstream hits = %d, want 0 while the sampler is off", got)
	}
}
