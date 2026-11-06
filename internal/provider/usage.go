package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	usageTimeout      = 5 * time.Second
	usageBodyLimit    = 1 << 20
	usageErrorSnippet = 200
)

// BalancePart is one currency's remaining balance.
type BalancePart struct {
	Currency string  `json:"currency,omitempty"`
	Total    float64 `json:"total"`
	Granted  float64 `json:"granted,omitempty"`
	ToppedUp float64 `json:"toppedUp,omitempty"`
}

// QuotaWindow is one subscription or plan usage window.
type QuotaWindow struct {
	Name        string     `json:"name"` // "5h", "weekly", "monthly"
	UsedPercent float64    `json:"usedPercent"`
	ResetsAt    *time.Time `json:"resetsAt,omitempty"`
}

// AccountUsage is one configured key's upstream usage report.
type AccountUsage struct {
	KeyIndex  int           `json:"-"`
	Balance   []BalancePart `json:"balance,omitempty"`
	Windows   []QuotaWindow `json:"windows,omitempty"`
	Note      string        `json:"note,omitempty"`
	Error     string        `json:"error,omitempty"`
	CheckedAt time.Time     `json:"checkedAt"`
}

// UsageProvider is the optional upstream balance/quota query.
type UsageProvider interface {
	Usage(ctx context.Context) ([]AccountUsage, error)
}

// UsageUnsupportedError reports a provider type that has no known balance
// query. Note is safe to show to an operator.
type UsageUnsupportedError struct {
	Provider string
	Type     string
	Note     string
}

func (e *UsageUnsupportedError) Error() string {
	if e == nil {
		return ""
	}
	if e.Note != "" {
		return e.Note
	}
	if e.Type != "" {
		return fmt.Sprintf("usage is not supported for provider type %q", e.Type)
	}
	return "usage is not supported"
}

// Usage queries the configured upstream balance and quota windows, one report
// per configured key.
func (p *baseProvider) Usage(ctx context.Context) ([]AccountUsage, error) {
	typ := strings.ToLower(strings.TrimSpace(p.cfg.Type))
	var kind string
	switch typ {
	case "deepseek":
		kind = "deepseek"
	case "commandcode":
		kind = "commandcode"
	case "opencode-go":
		kind = "opencode-go"
	case "opencode-zen":
		return nil, &UsageUnsupportedError{
			Provider: p.cfg.Name,
			Type:     p.cfg.Type,
			Note:     "OpenCode Zen exposes credits only in the OpenCode console: https://opencode.ai/console",
		}
	default:
		return nil, &UsageUnsupportedError{
			Provider: p.cfg.Name,
			Type:     p.cfg.Type,
			Note:     "provider type has no known balance query",
		}
	}

	if p.usageBaseURL() == "" {
		return nil, &UsageUnsupportedError{
			Provider: p.cfg.Name,
			Type:     p.cfg.Type,
			Note:     "no API base URL configured",
		}
	}
	if len(p.cfg.Keys) == 0 {
		return []AccountUsage{}, nil
	}

	accounts := make([]AccountUsage, len(p.cfg.Keys))
	var wg sync.WaitGroup
	for i, key := range p.cfg.Keys {
		i, key := i, key
		wg.Add(1)
		go func() {
			defer wg.Done()
			accounts[i] = p.usageAccount(ctx, kind, key, i)
		}()
	}
	wg.Wait()
	return accounts, nil
}

func (p *baseProvider) usageBaseURL() string {
	for _, api := range []API{Chat, Responses, Anthropic, Gemini} {
		if base, ok := p.BaseURL(api); ok && strings.TrimSpace(base) != "" {
			return base
		}
	}
	return ""
}

func (p *baseProvider) usageURL(kind string) (string, bool) {
	base := p.usageBaseURL()
	if base == "" {
		return "", false
	}
	u, err := url.Parse(base)
	if err != nil {
		return "", false
	}
	switch kind {
	case "deepseek":
		u.Path = "/user/balance"
	case "commandcode":
		u.Path = "/alpha/billing/credits"
	case "opencode-go":
		path := strings.TrimRight(u.Path, "/")
		switch {
		case strings.HasSuffix(path, "/zen/go/v1"):
			path += "/usage"
		case strings.HasSuffix(path, "/zen/go"):
			path += "/v1/usage"
		default:
			path += "/usage"
		}
		u.Path = path
	default:
		return "", false
	}
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), true
}

func (p *baseProvider) usageAccount(ctx context.Context, kind, key string, index int) AccountUsage {
	acc := AccountUsage{KeyIndex: index}

	endpoint, ok := p.usageURL(kind)
	if !ok {
		acc.Error = "usage endpoint is not configured"
		return acc
	}

	ctx, cancel := context.WithTimeout(ctx, usageTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		acc.Error = redactUsageError(err, key)
		return acc
	}
	req.Header.Set("User-Agent", "llm-gw")
	applyAuth(req, p.Auth(Chat), key)

	resp, err := NewHTTPClient(p.cfg.TLS, usageTimeout).Do(req)
	if err != nil {
		acc.Error = redactUsageError(err, key)
		return acc
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, usageBodyLimit))
	if err != nil {
		acc.Error = redactUsageError(err, key)
		return acc
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet := strings.TrimSpace(string(body))
		if len(snippet) > usageErrorSnippet {
			snippet = snippet[:usageErrorSnippet]
		}
		acc.Error = redactUsageError(fmt.Errorf("status %d: %s", resp.StatusCode, snippet), key)
		return acc
	}

	var balance []BalancePart
	var windows []QuotaWindow
	switch kind {
	case "deepseek":
		balance, windows, err = parseDeepSeekUsage(body)
	case "commandcode":
		balance, windows, err = parseCommandCodeUsage(body)
	case "opencode-go":
		balance, windows, err = parseOpenCodeGoUsage(body)
	default:
		err = fmt.Errorf("usage is not supported for %q", kind)
	}
	if err != nil {
		acc.Error = redactUsageError(err, key)
		return acc
	}

	acc.Balance = balance
	acc.Windows = windows
	acc.CheckedAt = time.Now().UTC()
	return acc
}

func parseDeepSeekUsage(body []byte) ([]BalancePart, []QuotaWindow, error) {
	var r struct {
		Infos []struct {
			Currency string `json:"currency"`
			Total    any    `json:"total_balance"`
			Granted  any    `json:"granted_balance"`
			Topped   any    `json:"topped_up_balance"`
		} `json:"balance_infos"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, nil, err
	}
	out := make([]BalancePart, 0, len(r.Infos))
	for _, info := range r.Infos {
		total, ok := usageNumber(info.Total)
		if !ok {
			continue
		}
		granted, _ := usageNumber(info.Granted)
		topped, _ := usageNumber(info.Topped)
		out = append(out, BalancePart{
			Currency: strings.ToUpper(strings.TrimSpace(info.Currency)),
			Total:    total,
			Granted:  granted,
			ToppedUp: topped,
		})
	}
	if len(out) == 0 {
		return nil, nil, errors.New("no balance in the reply")
	}
	return out, nil, nil
}

type usageCommandWindow struct {
	Used    any `json:"used"`
	Cap     any `json:"cap"`
	ResetAt any `json:"resetAt"`
}

func parseCommandCodeUsage(body []byte) ([]BalancePart, []QuotaWindow, error) {
	var r struct {
		Credits struct {
			Monthly   any `json:"monthlyCredits"`
			Purchased any `json:"purchasedCredits"`
			Free      any `json:"freeCredits"`
		} `json:"credits"`
		Windows *struct {
			FiveHour *usageCommandWindow `json:"fiveHour"`
			Weekly   *usageCommandWindow `json:"weekly"`
		} `json:"windowLimits"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, nil, err
	}

	var balance []BalancePart
	total := 0.0
	known := false
	for _, v := range []any{r.Credits.Monthly, r.Credits.Purchased, r.Credits.Free} {
		if n, ok := usageNumber(v); ok {
			total += math.Max(0, n)
			known = true
		}
	}
	if known {
		balance = []BalancePart{{Currency: "USD", Total: total}}
	}

	var windows []QuotaWindow
	add := func(name string, w *usageCommandWindow) {
		if w == nil {
			return
		}
		used, ok1 := usageNumber(w.Used)
		limit, ok2 := usageNumber(w.Cap)
		if !ok1 || !ok2 || limit <= 0 {
			return
		}
		windows = append(windows, QuotaWindow{
			Name:        name,
			UsedPercent: usageClampPercent(100 * math.Max(0, used) / limit),
			ResetsAt:    usageTime(w.ResetAt),
		})
	}
	if r.Windows != nil {
		add("5h", r.Windows.FiveHour)
		add("weekly", r.Windows.Weekly)
	}

	if len(balance) == 0 && len(windows) == 0 {
		return nil, nil, errors.New("no credits or windows in the reply")
	}
	return balance, windows, nil
}

func parseOpenCodeGoUsage(body []byte) ([]BalancePart, []QuotaWindow, error) {
	type window struct {
		Percent  *float64 `json:"percent"`
		ResetsAt string   `json:"resetsAt"`
	}
	var r struct {
		Usage map[string]window `json:"usage"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, nil, err
	}

	out := make([]QuotaWindow, 0, 3)
	for _, item := range []struct {
		key  string
		name string
	}{
		{"rolling", "5h"},
		{"weekly", "weekly"},
		{"monthly", "monthly"},
	} {
		w, ok := r.Usage[item.key]
		if !ok || w.Percent == nil {
			continue
		}
		q := QuotaWindow{Name: item.name, UsedPercent: usageClampPercent(*w.Percent)}
		if q.UsedPercent > 0 {
			if t, err := time.Parse(time.RFC3339Nano, w.ResetsAt); err == nil {
				q.ResetsAt = &t
			}
		}
		out = append(out, q)
	}
	if len(out) == 0 {
		return nil, nil, errors.New("no usage windows in the reply")
	}
	return nil, out, nil
}

func usageNumber(v any) (float64, bool) {
	switch t := v.(type) {
	case nil:
		return 0, false
	case json.Number:
		n, err := t.Float64()
		return n, err == nil
	case float64:
		return t, true
	case float32:
		return float64(t), true
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case string:
		n, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
		if err != nil {
			return 0, false
		}
		return n, true
	}
	return 0, false
}

func usageTime(v any) *time.Time {
	switch t := v.(type) {
	case nil:
		return nil
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return nil
		}
		if ts, err := time.Parse(time.RFC3339Nano, s); err == nil {
			return &ts
		}
		if n, err := strconv.ParseFloat(s, 64); err == nil {
			return usageTime(n)
		}
	case json.Number:
		if n, err := t.Float64(); err == nil {
			return usageTime(n)
		}
	case float64:
		if math.IsNaN(t) || math.IsInf(t, 0) || t <= 0 {
			return nil
		}
		if t < 1e12 {
			t *= 1000
		}
		ts := time.UnixMilli(int64(t)).UTC()
		return &ts
	case int:
		return usageTime(float64(t))
	case int64:
		return usageTime(float64(t))
	}
	return nil
}

func usageClampPercent(v float64) float64 {
	if math.IsNaN(v) {
		return 0
	}
	return math.Max(0, math.Min(100, v))
}

func redactUsageError(err error, key string) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if key != "" {
		msg = strings.ReplaceAll(msg, key, "***")
	}
	return msg
}
