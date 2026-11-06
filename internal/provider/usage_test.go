package provider

import (
	"context"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-gost/llm-gw/internal/config"
)

func usageProvider(t *testing.T, cfg config.Provider) UsageProvider {
	t.Helper()
	p, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	up, ok := p.(UsageProvider)
	if !ok {
		t.Fatal("provider does not implement UsageProvider")
	}
	return up
}

func TestUsageDeepSeek(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user/balance" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-test" {
			t.Errorf("auth = %q", got)
		}
		w.Write([]byte(`{"is_available":true,"balance_infos":[
			{"currency":"CNY","total_balance":"110.00","granted_balance":"10.00","topped_up_balance":"100.00"},
			{"currency":"USD","total_balance":"2.50","granted_balance":"0","topped_up_balance":"2.50"}]}`))
	}))
	defer srv.Close()

	accounts, err := usageProvider(t, config.Provider{
		Name: "deepseek", Type: "deepseek", Chat: srv.URL + "/v1", Keys: []string{"sk-test"},
	}).Usage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 1 || len(accounts[0].Balance) != 2 {
		t.Fatalf("accounts = %+v", accounts)
	}
	if accounts[0].Balance[0].Currency != "CNY" || accounts[0].Balance[0].Total != 110 {
		t.Fatalf("balance = %+v", accounts[0].Balance)
	}
	if accounts[0].CheckedAt.IsZero() || accounts[0].Error != "" {
		t.Fatalf("account = %+v", accounts[0])
	}
}

func TestUsageCommandCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/alpha/billing/credits" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-test" {
			t.Errorf("auth = %q", got)
		}
		w.Write([]byte(`{"credits":{"monthlyCredits":12.3,"purchasedCredits":2,"freeCredits":0},
			"windowLimits":{"limited":true,
				"fiveHour":{"used":4.2,"cap":10,"resetAt":4102444800000},
				"weekly":{"used":9,"cap":50,"resetAt":"2030-01-01T00:00:00Z"}}}`))
	}))
	defer srv.Close()

	accounts, err := usageProvider(t, config.Provider{
		Name: "commandcode", Type: "commandcode", Chat: srv.URL + "/provider/v1", Keys: []string{"sk-test"},
	}).Usage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 1 {
		t.Fatalf("accounts = %+v", accounts)
	}
	acc := accounts[0]
	if len(acc.Balance) != 1 || acc.Balance[0].Currency != "USD" ||
		math.Abs(acc.Balance[0].Total-14.3) > 1e-9 {
		t.Fatalf("balance = %+v", acc.Balance)
	}
	if len(acc.Windows) != 2 {
		t.Fatalf("windows = %+v", acc.Windows)
	}
	if acc.Windows[0].Name != "5h" || math.Abs(acc.Windows[0].UsedPercent-42) > 1e-9 {
		t.Fatalf("5h = %+v", acc.Windows[0])
	}
	if acc.Windows[1].Name != "weekly" || math.Abs(acc.Windows[1].UsedPercent-18) > 1e-9 {
		t.Fatalf("weekly = %+v", acc.Windows[1])
	}
	if acc.Windows[0].ResetsAt == nil || acc.Windows[1].ResetsAt == nil {
		t.Fatalf("resets = %+v", acc.Windows)
	}
}

func TestUsageOpenCodeGo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/zen/go/v1/usage" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-test" {
			t.Errorf("auth = %q", got)
		}
		if got := r.Header.Get("x-api-key"); got != "sk-test" {
			t.Errorf("x-api-key = %q", got)
		}
		w.Write([]byte(`{"usage":{
			"rolling":{"percent":37,"resetsAt":"2026-08-26T14:12:03.000Z"},
			"weekly":{"percent":12,"resetsAt":"2026-08-30T00:00:00.000Z"},
			"monthly":{"percent":0,"resetsAt":"2026-09-01T00:00:00.000Z"}}}`))
	}))
	defer srv.Close()

	accounts, err := usageProvider(t, config.Provider{
		Name: "opencode-go", Type: "opencode-go", Chat: srv.URL + "/zen/go/v1", Keys: []string{"sk-test"},
	}).Usage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 1 || len(accounts[0].Windows) != 3 {
		t.Fatalf("accounts = %+v", accounts)
	}
	windows := accounts[0].Windows
	if windows[0].Name != "5h" || windows[0].UsedPercent != 37 {
		t.Fatalf("rolling = %+v", windows[0])
	}
	if windows[1].Name != "weekly" || windows[1].UsedPercent != 12 {
		t.Fatalf("weekly = %+v", windows[1])
	}
	if windows[2].Name != "monthly" || windows[2].UsedPercent != 0 {
		t.Fatalf("monthly = %+v", windows[2])
	}
	if windows[0].ResetsAt == nil || windows[1].ResetsAt == nil {
		t.Fatalf("resets = %+v", windows)
	}
	if windows[2].ResetsAt != nil {
		t.Fatalf("0%% monthly reset = %v, want nil", windows[2].ResetsAt)
	}
}

func TestUsageOpenCodeGoBasePaths(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Write([]byte(`{"usage":{"rolling":{"percent":1}}}`))
	}))
	defer srv.Close()

	cases := []struct {
		base string
		want string
	}{
		{srv.URL + "/zen/go", "/zen/go/v1/usage"},
		{srv.URL + "/zen/go/v1", "/zen/go/v1/usage"},
		{srv.URL + "/zen/go/v1/", "/zen/go/v1/usage"},
		{srv.URL + "/inference/go", "/inference/go/v1/usage"},
		{srv.URL + "/inference/go/v1", "/inference/go/v1/usage"},
		{srv.URL + "/inference/go/openai/v1", "/inference/go/v1/usage"},
		{srv.URL + "/inference/go/openai/v1/", "/inference/go/v1/usage"},
		{srv.URL + "/inference/go/anthropic", "/inference/go/v1/usage"},
		{srv.URL + "/proxy/v1", "/proxy/v1/usage"},
	}
	for _, tc := range cases {
		gotPath = ""
		accounts, err := usageProvider(t, config.Provider{
			Name: "opencode-go", Type: "opencode-go", Chat: tc.base, Keys: []string{"sk-test"},
		}).Usage(context.Background())
		if err != nil {
			t.Fatalf("%s: %v", tc.base, err)
		}
		if len(accounts) != 1 || accounts[0].Error != "" {
			t.Fatalf("%s: %+v", tc.base, accounts)
		}
		if gotPath != tc.want {
			t.Fatalf("%s: path = %q, want %q", tc.base, gotPath, tc.want)
		}
	}
}

func TestUsageUnsupported(t *testing.T) {
	cases := []config.Provider{
		{Name: "zen", Type: "opencode-zen", Chat: "https://opencode.ai/zen/v1"},
		{Name: "generic", Type: "generic", Chat: "https://example.com/v1"},
	}
	for _, cfg := range cases {
		_, err := usageProvider(t, cfg).Usage(context.Background())
		var ue *UsageUnsupportedError
		if !errors.As(err, &ue) {
			t.Fatalf("%s: err = %v, want UsageUnsupportedError", cfg.Type, err)
		}
		if cfg.Type == "opencode-zen" && !strings.Contains(ue.Note, "https://opencode.ai/console") {
			t.Fatalf("zen note = %q", ue.Note)
		}
	}
}

func TestUsageNoKeys(t *testing.T) {
	accounts, err := usageProvider(t, config.Provider{
		Name: "deepseek", Type: "deepseek", Chat: "https://api.deepseek.com/v1",
	}).Usage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 0 {
		t.Fatalf("accounts = %+v", accounts)
	}
}

func TestUsageMultipleKeys(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		total := "1"
		if r.Header.Get("Authorization") == "Bearer sk-two" {
			total = "2"
		}
		w.Write([]byte(`{"balance_infos":[{"currency":"USD","total_balance":"` + total + `"}]}`))
	}))
	defer srv.Close()

	accounts, err := usageProvider(t, config.Provider{
		Name: "deepseek", Type: "deepseek", Chat: srv.URL + "/v1",
		Keys: []string{"sk-one", "sk-two"},
	}).Usage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 2 {
		t.Fatalf("accounts = %+v", accounts)
	}
	if accounts[0].KeyIndex != 0 || accounts[1].KeyIndex != 1 {
		t.Fatalf("key indexes = %d/%d", accounts[0].KeyIndex, accounts[1].KeyIndex)
	}
	if accounts[0].Balance[0].Total != 1 || accounts[1].Balance[0].Total != 2 {
		t.Fatalf("balances = %+v/%+v", accounts[0].Balance, accounts[1].Balance)
	}
}

func TestUsageBaseFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user/balance" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-test" {
			t.Errorf("auth = %q", got)
		}
		w.Write([]byte(`{"balance_infos":[{"currency":"USD","total_balance":"1"}]}`))
	}))
	defer srv.Close()

	accounts, err := usageProvider(t, config.Provider{
		Name: "deepseek", Type: "deepseek", Anthropic: srv.URL + "/anthropic", Keys: []string{"sk-test"},
	}).Usage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 1 || accounts[0].Error != "" {
		t.Fatalf("accounts = %+v", accounts)
	}
}

func TestUsageHTTPErrorRedactsKey(t *testing.T) {
	const key = "sk-secret-1234"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("bad key " + key))
	}))
	defer srv.Close()

	accounts, err := usageProvider(t, config.Provider{
		Name: "deepseek", Type: "deepseek", Chat: srv.URL + "/v1", Keys: []string{key},
	}).Usage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 1 {
		t.Fatalf("accounts = %+v", accounts)
	}
	msg := accounts[0].Error
	if !strings.Contains(msg, "500") || !strings.Contains(msg, "***") || strings.Contains(msg, key) {
		t.Fatalf("error = %q", msg)
	}
}

func TestUsageMalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not-json"))
	}))
	defer srv.Close()

	accounts, err := usageProvider(t, config.Provider{
		Name: "deepseek", Type: "deepseek", Chat: srv.URL + "/v1", Keys: []string{"sk-test"},
	}).Usage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 1 || accounts[0].Error == "" {
		t.Fatalf("accounts = %+v", accounts)
	}
}

func TestUsageCanceledContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"balance_infos":[{"currency":"USD","total_balance":"1"}]}`))
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	accounts, err := usageProvider(t, config.Provider{
		Name: "deepseek", Type: "deepseek", Chat: srv.URL + "/v1", Keys: []string{"sk-test"},
	}).Usage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 1 || accounts[0].Error == "" {
		t.Fatalf("accounts = %+v", accounts)
	}
}

func TestUsageErrorRedactsKeyBeforeTruncating(t *testing.T) {
	const key = "sk-secret-1234"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(strings.Repeat("x", 195) + " " + key))
	}))
	defer srv.Close()

	accounts, err := usageProvider(t, config.Provider{
		Name: "deepseek", Type: "deepseek", Chat: srv.URL + "/v1", Keys: []string{key},
	}).Usage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 1 {
		t.Fatalf("accounts = %+v", accounts)
	}
	msg := accounts[0].Error
	if strings.Contains(msg, "sk-") {
		t.Fatalf("error leaked a key prefix after truncation: %q", msg)
	}
}
