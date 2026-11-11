package admin

import (
	"net/http"

	"github.com/ginuerzh/cuttle/internal/stats"
)

// handleAttempts lists discarded upstream attempts newest first. The status
// filter applies only to a positive value: an attempt that never got a
// response stores status 0, so 0 means "any status".
func (s *Server) handleAttempts(w http.ResponseWriter, r *http.Request) {
	if s.stats == nil {
		writeError(w, http.StatusServiceUnavailable, "stats disabled")
		return
	}
	from, to := parseRange(r)
	q := r.URL.Query()
	rows, err := s.stats.Attempts(r.Context(), stats.AttemptFilter{
		From:       from,
		To:         to,
		Route:      q.Get("route"),
		Node:       q.Get("node"),
		Provider:   q.Get("provider"),
		Status:     atoi(q.Get("status")),
		ErrorClass: q.Get("errorClass"),
		Limit:      atoi(q.Get("limit")),
		Offset:     atoi(q.Get("offset")),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	labels := s.newLabelSet(r.Context())
	views := make([]attemptView, 0, len(rows))
	for _, row := range rows {
		views = append(views, labels.attemptView(row))
	}
	writeJSON(w, http.StatusOK, nonNilSlice(views))
}
