package admin

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ginuerzh/cuttle/internal/config"
	"github.com/ginuerzh/cuttle/internal/stats"
)

func (s *Server) handleSummary(w http.ResponseWriter, r *http.Request) {
	if s.stats == nil {
		writeError(w, http.StatusServiceUnavailable, "stats disabled")
		return
	}
	from, to := parseRange(r)
	sum, err := s.stats.Summary(r.Context(), from, to)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, sum)
}

// requestFilter builds the stats filter shared by the list and series
// endpoints. ok is false when the request names an entrypoint that no longer
// exists: the caller answers with an empty result instead of an error, exactly
// as the list endpoint always has.
func (s *Server) requestFilter(r *http.Request) (f stats.Filter, ok bool) {
	from, to := parseRange(r)
	q := r.URL.Query()
	f = stats.Filter{
		From:        from,
		To:          to,
		Provider:    q.Get("provider"),
		Model:       q.Get("model"),
		ClientModel: q.Get("clientModel"),
		Route:       q.Get("route"),
		Node:        q.Get("node"),
		GatewayKey:  q.Get("key"),
		OnlyErrors:  q.Get("errors") == "1",
		Captured:    q.Get("captured") == "1",
		ErrorClass:  q.Get("errorClass"),
		Limit:       atoi(q.Get("limit")),
		Offset:      atoi(q.Get("offset")),
	}
	// The entrypoint filter is a client-model match, so it resolves the id
	// against the current entrypoint list instead of a stored column. Order is
	// precedence, so the globs of every earlier entrypoint shadow this one.
	if id := q.Get("entrypoint"); id != "" {
		cfg := s.monitorConfig()
		found := false
		for i, ep := range cfg.Entrypoints {
			if ep.ID != id {
				continue
			}
			f.ClientModelPatterns = ep.Match
			for _, earlier := range cfg.Entrypoints[:i] {
				f.ClientModelExcludes = append(f.ClientModelExcludes, earlier.Match...)
			}
			found = true
			break
		}
		if !found {
			return f, false
		}
	}
	if v := q.Get("status"); v != "" {
		f.Status = atoi(v)
	}
	return f, true
}

func (s *Server) handleRequests(w http.ResponseWriter, r *http.Request) {
	if s.stats == nil {
		writeError(w, http.StatusServiceUnavailable, "stats disabled")
		return
	}
	f, ok := s.requestFilter(r)
	if !ok {
		writeJSON(w, http.StatusOK, []requestView{})
		return
	}
	rows, err := s.stats.Query(r.Context(), f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	labels := s.newLabelSet(r.Context())
	views := make([]requestView, 0, len(rows))
	for _, row := range rows {
		views = append(views, labels.requestView(row))
	}
	writeJSON(w, http.StatusOK, nonNilSlice(views))
}

// seriesResponse is the wire shape of GET /api/stats/series: Bucket is the
// bucket width, each point's Bucket is its start time.
type seriesResponse struct {
	Bucket string              `json:"bucket"`
	Points []stats.SeriesPoint `json:"points"`
}

const (
	defaultSeriesBucket = time.Hour
	minSeriesBucket     = time.Second
	maxSeriesBucket     = 24 * time.Hour
)

// parseBucket parses ?bucket=. An absent value means one hour; a value that
// cannot be parsed is rejected rather than silently coerced, and a valid but
// absurd width is clamped so a typo cannot ask for millions of bars.
func parseBucket(v string) (time.Duration, error) {
	if strings.TrimSpace(v) == "" {
		return defaultSeriesBucket, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("invalid bucket %q", v)
	}
	if d < minSeriesBucket {
		d = minSeriesBucket
	}
	if d > maxSeriesBucket {
		d = maxSeriesBucket
	}
	return d, nil
}

func (s *Server) handleSeries(w http.ResponseWriter, r *http.Request) {
	if s.stats == nil {
		writeError(w, http.StatusServiceUnavailable, "stats disabled")
		return
	}
	bucket, err := parseBucket(r.URL.Query().Get("bucket"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	f, ok := s.requestFilter(r)
	if !ok {
		writeJSON(w, http.StatusOK, seriesResponse{Bucket: bucket.String(), Points: []stats.SeriesPoint{}})
		return
	}
	series, err := s.stats.Series(r.Context(), f, bucket)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, seriesResponse{Bucket: bucket.String(), Points: nonNilPoints(series.Points)})
}

// nonNilPoints keeps the payload an array, never null.
func nonNilPoints(points []stats.SeriesPoint) []stats.SeriesPoint {
	if points == nil {
		return []stats.SeriesPoint{}
	}
	return points
}

func (s *Server) handleRequest(w http.ResponseWriter, r *http.Request) {
	if s.stats == nil {
		writeError(w, http.StatusServiceUnavailable, "stats disabled")
		return
	}
	row, err := s.stats.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if row == nil {
		writeError(w, http.StatusNotFound, "request not found")
		return
	}
	labels := s.newLabelSet(r.Context())
	view := labels.requestView(*row)
	attempts, err := s.stats.AttemptsForRequest(r.Context(), row.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, attempt := range attempts {
		view.Attempts = append(view.Attempts, labels.attemptView(attempt))
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) handleErrorSummary(w http.ResponseWriter, r *http.Request) {
	if s.stats == nil {
		writeError(w, http.StatusServiceUnavailable, "stats disabled")
		return
	}
	from, to := parseRange(r)
	sum, err := s.stats.ErrorSummary(r.Context(), from, to)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, sum)
}

func (s *Server) handleAlerts(w http.ResponseWriter, r *http.Request) {
	if s.stats == nil {
		writeError(w, http.StatusServiceUnavailable, "stats disabled")
		return
	}
	alerts, err := s.stats.Alerts(r.Context(), time.Now())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, nonNilSlice(alerts))
}

func parseRange(r *http.Request) (time.Time, time.Time) {
	now := time.Now()
	from := now.Add(-24 * time.Hour)
	to := now

	if v := r.URL.Query().Get("from"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			from = t
		}
	}
	if v := r.URL.Query().Get("to"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			to = t
		}
	}
	return from, to
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func (s *Server) handleGraph(w http.ResponseWriter, r *http.Request) {
	if s.stats == nil {
		writeError(w, http.StatusServiceUnavailable, "stats disabled")
		return
	}
	from, to := parseRange(r)
	cfg := s.monitorConfig()
	g, err := s.stats.GraphWithFilter(r.Context(), from, to, cfg.ResolveEntrypointID, cfg.TopologyIncludes)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, g)
}

// monitorConfig copies the topology fields used to attribute and filter
// statistics, so a stats query never holds the admin config lock.
func (s *Server) monitorConfig() *config.Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	return &config.Config{
		Entrypoints: append([]config.Entrypoint(nil), s.cfg.Entrypoints...),
		Routes:      append([]config.Route(nil), s.cfg.Routes...),
		Nodes:       append([]config.Node(nil), s.cfg.Nodes...),
	}
}
