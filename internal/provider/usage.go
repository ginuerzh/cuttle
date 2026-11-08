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

	// OpenCodeZenUsageNote explains why Zen has no per-key balance.
	OpenCodeZenUsageNote = "OpenCode Zen exposes credits only in the OpenCode console: https://opencode.ai/console"
	// GenericUsageNote is returned for provider types without a known contract.
	GenericUsageNote = "provider type has no known balance query"
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

// UsageSupport reports whether typ has a known balance/quota contract and,
// when it does not, the operator-facing reason.
func UsageSupport(typ string) (bool, string) {
	switch strings.ToLower(strings.TrimSpace(typ)) {
	case "deepseek", "commandcode", "opencode-go", "openrouter":
		return true, ""
	case "opencode-zen":
		return false, OpenCodeZenUsageNote
	default:
		return false, GenericUsageNote
	}
}

// Usage queries the configured upstream balance and quota windows, one report
// per configured key.
func (p *baseProvider) Usage(ctx context.Context) ([]AccountUsage, error) {
	typ := strings.ToLower(strings.TrimSpace(p.cfg.Type))
	if supported, note := UsageSupport(typ); !supported {
		return nil, &UsageUnsupportedError{
			Provider: p.cfg.Name,
			Type:     p.cfg.Type,
			Note:     note,
		}
	}
	var kind string
	switch typ {
	case "deepseek":
		kind = "deepseek"
	case "commandcode":
		kind = "commandcode"
	case "opencode-go":
		kind = "opencode-go"
	case "openrouter":
		kind = "openrouter"
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
	case "commandcode-subscriptions":
		u.Path = "/alpha/billing/subscriptions"
	case "opencode-go":
		path := strings.TrimRight(u.Path, "/")
		switch {
		case strings.HasSuffix(path, "/inference/go/openai/v1"):
			path = strings.TrimSuffix(path, "/openai/v1") + "/v1/usage"
		case strings.HasSuffix(path, "/inference/go/anthropic"):
			path = strings.TrimSuffix(path, "/anthropic") + "/v1/usage"
		case strings.HasSuffix(path, "/inference/go/v1"):
			path += "/usage"
		case strings.HasSuffix(path, "/inference/go"):
			path += "/v1/usage"
		case strings.HasSuffix(path, "/zen/go/v1"):
			path += "/usage"
		case strings.HasSuffix(path, "/zen/go"):
			path += "/v1/usage"
		default:
			path += "/usage"
		}
		u.Path = path
	case "openrouter":
		u.Path = strings.TrimRight(u.Path, "/") + "/key"
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

	body, err := p.usageBody(ctx, endpoint, key)
	if err != nil {
		acc.Error = redactUsageError(err, key)
		return acc
	}

	var balance []BalancePart
	var windows []QuotaWindow
	var note string
	switch kind {
	case "deepseek":
		balance, windows, err = parseDeepSeekUsage(body)
	case "commandcode":
		var usage commandCodeUsage
		usage, err = parseCommandCodeUsage(body)
		if err == nil {
			balance, windows = usage.Balance, p.enrichCommandCodeUsage(ctx, key, usage)
		}
	case "opencode-go":
		balance, windows, err = parseOpenCodeGoUsage(body)
	case "openrouter":
		balance, windows, note, err = parseOpenRouterUsage(body)
	default:
		err = fmt.Errorf("usage is not supported for %q", kind)
	}
	if err != nil {
		acc.Error = redactUsageError(err, key)
		return acc
	}

	acc.Balance = balance
	acc.Windows = windows
	acc.Note = note
	acc.CheckedAt = time.Now().UTC()
	return acc
}

// usageBody performs one authenticated usage request and returns the body of a
// 2xx reply. Errors are already redacted so callers cannot leak a key.
func (p *baseProvider) usageBody(ctx context.Context, endpoint, key string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "llm-gw")
	applyAuth(req, p.Auth(Chat), key)

	resp, err := NewHTTPClient(p.cfg.TLS, usageTimeout).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, usageBodyLimit))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := fmt.Sprintf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
		msg = redactUsageText(msg, key)
		if len(msg) > usageErrorSnippet {
			msg = msg[:usageErrorSnippet]
		}
		return nil, errors.New(msg)
	}
	return body, nil
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

type commandCodeUsage struct {
	Balance          []BalancePart
	Windows          []QuotaWindow
	MonthlyRemaining float64
	MonthlyGranted   float64
	MonthlyKnown     bool
}

func parseCommandCodeUsage(body []byte) (commandCodeUsage, error) {
	var r struct {
		Credits struct {
			Monthly   any `json:"monthlyCredits"`
			Granted   any `json:"monthlyCreditsGranted"`
			Purchased any `json:"purchasedCredits"`
			Free      any `json:"freeCredits"`
		} `json:"credits"`
		Windows *struct {
			FiveHour *usageCommandWindow `json:"fiveHour"`
			Weekly   *usageCommandWindow `json:"weekly"`
		} `json:"windowLimits"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return commandCodeUsage{}, err
	}

	monthly, monthlyOK := usageNumber(r.Credits.Monthly)
	granted, grantedOK := usageNumber(r.Credits.Granted)

	var out commandCodeUsage
	total := 0.0
	known := false
	for _, v := range []any{r.Credits.Monthly, r.Credits.Purchased, r.Credits.Free} {
		if n, ok := usageNumber(v); ok {
			total += math.Max(0, n)
			known = true
		}
	}
	if known {
		out.Balance = []BalancePart{{Currency: "USD", Total: total}}
	}

	add := func(name string, w *usageCommandWindow) {
		if w == nil {
			return
		}
		used, ok1 := usageNumber(w.Used)
		limit, ok2 := usageNumber(w.Cap)
		if !ok1 || !ok2 || limit <= 0 {
			return
		}
		out.Windows = append(out.Windows, QuotaWindow{
			Name:        name,
			UsedPercent: usageClampPercent(100 * math.Max(0, used) / limit),
			ResetsAt:    usageTime(w.ResetAt),
		})
	}
	if r.Windows != nil {
		add("5h", r.Windows.FiveHour)
		add("weekly", r.Windows.Weekly)
	}

	if grantedOK && granted > 0 && monthlyOK {
		out.Windows = append(out.Windows, commandCodeMonthlyWindow(granted, monthly, nil))
	}
	out.MonthlyRemaining = math.Max(0, monthly)
	out.MonthlyGranted = granted
	out.MonthlyKnown = monthlyOK

	if len(out.Balance) == 0 && len(out.Windows) == 0 {
		return commandCodeUsage{}, errors.New("no credits or windows in the reply")
	}
	return out, nil
}

func commandCodeMonthlyWindow(granted, remaining float64, reset *time.Time) QuotaWindow {
	used := math.Max(0, granted-math.Max(0, remaining))
	return QuotaWindow{
		Name:        "monthly",
		UsedPercent: usageClampPercent(100 * used / granted),
		ResetsAt:    reset,
	}
}

// enrichCommandCodeUsage adds the monthly usage window that the credits reply
// does not carry directly. The vendor reports the remaining monthly credits and
// the subscription plan; the plan maps to the monthly grant, and the
// subscription's currentPeriodEnd is the reset time. A failed lookup leaves the
// existing 5-hour and weekly windows untouched.
func (p *baseProvider) enrichCommandCodeUsage(ctx context.Context, key string, usage commandCodeUsage) []QuotaWindow {
	if !usage.MonthlyKnown {
		return usage.Windows
	}
	sub, ok := p.commandCodeSubscription(ctx, key)
	if !ok {
		return usage.Windows
	}
	for i := range usage.Windows {
		if usage.Windows[i].Name == "monthly" {
			if usage.Windows[i].ResetsAt == nil {
				usage.Windows[i].ResetsAt = sub.CurrentPeriodEnd
			}
			return usage.Windows
		}
	}
	grant := usage.MonthlyGranted
	if grant <= 0 {
		grant, _ = commandCodeMonthlyGrant(sub.PlanID)
	}
	if grant > 0 && usage.MonthlyKnown {
		usage.Windows = append(usage.Windows, commandCodeMonthlyWindow(grant, usage.MonthlyRemaining, sub.CurrentPeriodEnd))
	}
	return usage.Windows
}

type commandCodeSubscription struct {
	PlanID           string
	CurrentPeriodEnd *time.Time
}

func (p *baseProvider) commandCodeSubscription(ctx context.Context, key string) (commandCodeSubscription, bool) {
	endpoint, ok := p.usageURL("commandcode-subscriptions")
	if !ok {
		return commandCodeSubscription{}, false
	}
	// The subscription lookup only enriches the monthly reset time. A short
	// cap keeps a slow endpoint from delaying the credits report.
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	body, err := p.usageBody(ctx, endpoint, key)
	if err != nil {
		return commandCodeSubscription{}, false
	}
	return parseCommandCodeSubscription(body)
}

func parseCommandCodeSubscription(body []byte) (commandCodeSubscription, bool) {
	var r struct {
		Success *bool `json:"success"`
		Data    *struct {
			PlanID           string `json:"planId"`
			CurrentPeriodEnd any    `json:"currentPeriodEnd"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return commandCodeSubscription{}, false
	}
	if r.Success == nil || !*r.Success || r.Data == nil {
		return commandCodeSubscription{}, false
	}
	planID := strings.TrimSpace(r.Data.PlanID)
	if planID == "" {
		return commandCodeSubscription{}, false
	}
	return commandCodeSubscription{
		PlanID:           planID,
		CurrentPeriodEnd: usageTime(r.Data.CurrentPeriodEnd),
	}, true
}

// commandCodeMonthlyGrant maps the subscription plan ID to its monthly credit
// allowance. CommandCode publishes these amounts on its pricing page; the
// credits reply only reports the remaining balance.
func commandCodeMonthlyGrant(planID string) (float64, bool) {
	switch strings.ToLower(strings.TrimSpace(planID)) {
	case "individual-go", "individual-go-v1":
		return 10, true
	case "individual-goat":
		return 70, true
	case "individual-pro":
		return 30, true
	case "individual-pro-v1":
		return 80, true
	case "individual-max":
		return 150, true
	case "individual-ultra":
		return 300, true
	default:
		return 0, false
	}
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

func parseOpenRouterUsage(body []byte) ([]BalancePart, []QuotaWindow, string, error) {
	var r struct {
		Data struct {
			Limit          any    `json:"limit"`
			LimitRemaining any    `json:"limit_remaining"`
			LimitReset     string `json:"limit_reset"`
			Usage          any    `json:"usage"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, nil, "", err
	}

	limit, limitOK := usageNumber(r.Data.Limit)
	remaining, remainingOK := usageNumber(r.Data.LimitRemaining)
	usage, usageOK := usageNumber(r.Data.Usage)

	if limitOK && limit > 0 && remainingOK {
		name := strings.ToLower(strings.TrimSpace(r.Data.LimitReset))
		switch name {
		case "daily", "weekly", "monthly":
		default:
			name = "key limit"
		}
		return []BalancePart{{Currency: "USD", Total: math.Max(0, remaining)}},
			[]QuotaWindow{{
				Name:        name,
				UsedPercent: usageClampPercent(100 * (limit - remaining) / limit),
			}}, "", nil
	}
	if limitOK && limit > 0 {
		return nil, nil, "", errors.New("key limit has no remaining value")
	}
	if usageOK {
		return nil, nil, fmt.Sprintf("unlimited key · all-time usage $%.2f", usage), nil
	}
	return nil, nil, "", errors.New("no key limit or usage in the reply")
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

func redactUsageText(s, key string) string {
	if key == "" {
		return s
	}
	return strings.ReplaceAll(s, key, "***")
}

func redactUsageError(err error, key string) string {
	if err == nil {
		return ""
	}
	return redactUsageText(err.Error(), key)
}
