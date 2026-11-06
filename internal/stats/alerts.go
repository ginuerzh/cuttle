package stats

import (
	"context"
	"fmt"
	"time"
)

// Alert is one current-window anomaly. P1 computes alerts on demand; it does
// not push them anywhere.
type Alert struct {
	ID       string `json:"id"`
	Severity string `json:"severity"`
	Title    string `json:"title"`
	Detail   string `json:"detail"`
	Count    int64  `json:"count"`
	Since    string `json:"since,omitempty"`
}

const alertWindow = 15 * time.Minute

// Alerts evaluates the P1 rules over the last 15 minutes.
func (s *Store) Alerts(ctx context.Context, now time.Time) ([]Alert, error) {
	from := now.Add(-alertWindow)
	var total, errors int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*),
		COALESCE(SUM(CASE WHEN `+failedExpr+` THEN 1 ELSE 0 END),0)
		FROM requests WHERE ts >= ? AND ts < ?`,
		from.UnixMilli(), now.UnixMilli()).Scan(&total, &errors); err != nil {
		return nil, err
	}

	rows, err := s.db.QueryContext(ctx, `SELECT
		COALESCE(NULLIF(error_class,''),'unknown'), COUNT(*), MIN(ts)
		FROM requests WHERE ts >= ? AND ts < ? AND `+failedExpr+`
		GROUP BY 1`, from.UnixMilli(), now.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type stat struct {
		count int64
		since int64
	}
	byClass := make(map[string]stat)
	for rows.Next() {
		var class string
		var count, since int64
		if err := rows.Scan(&class, &count, &since); err != nil {
			return nil, err
		}
		byClass[class] = stat{count: count, since: since}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var out []Alert
	if st := byClass[ErrorClassUpstreamAuth]; st.count > 0 {
		out = append(out, Alert{
			ID:       "auth",
			Severity: "critical",
			Title:    "上游认证失败",
			Detail:   fmt.Sprintf("最近 15 分钟出现 %d 次上游 401", st.count),
			Count:    st.count,
			Since:    time.UnixMilli(st.since).UTC().Format(time.RFC3339),
		})
	}
	if total >= 10 && float64(errors)/float64(total) > 0.20 {
		out = append(out, Alert{
			ID:       "error_rate",
			Severity: "warning",
			Title:    "错误率偏高",
			Detail:   fmt.Sprintf("最近 15 分钟错误率 %.1f%% (%d/%d)", 100*float64(errors)/float64(total), errors, total),
			Count:    errors,
		})
	}
	if st := byClass[ErrorClassGatewayTimeout]; st.count > 0 {
		out = append(out, Alert{
			ID:       "gateway_timeout",
			Severity: "warning",
			Title:    "网关等待上游超时",
			Detail:   fmt.Sprintf("最近 15 分钟出现 %d 次上游响应头超时", st.count),
			Count:    st.count,
			Since:    time.UnixMilli(st.since).UTC().Format(time.RFC3339),
		})
	}
	if st := byClass[ErrorClassUpstreamTruncated]; st.count > 3 {
		out = append(out, Alert{
			ID:       "truncation",
			Severity: "warning",
			Title:    "上游流截断偏多",
			Detail:   fmt.Sprintf("最近 15 分钟出现 %d 次流截断", st.count),
			Count:    st.count,
			Since:    time.UnixMilli(st.since).UTC().Format(time.RFC3339),
		})
	}
	return out, nil
}
