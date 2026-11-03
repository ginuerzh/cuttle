package admin

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-gost/llm-gw/internal/stats"
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

func (s *Server) handleRequests(w http.ResponseWriter, r *http.Request) {
	if s.stats == nil {
		writeError(w, http.StatusServiceUnavailable, "stats disabled")
		return
	}
	from, to := parseRange(r)
	q := r.URL.Query()
	f := stats.Filter{
		From:     from,
		To:       to,
		Provider: q.Get("provider"),
		Model:    q.Get("model"),
		Limit:    atoi(q.Get("limit")),
		Offset:   atoi(q.Get("offset")),
	}
	if v := q.Get("status"); v != "" {
		f.Status = atoi(v)
	}
	rows, err := s.stats.Query(r.Context(), f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rows)
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
	writeJSON(w, http.StatusOK, row)
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
