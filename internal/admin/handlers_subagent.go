package admin

import (
	"net/http"
	"time"

	"github.com/ginuerzh/cuttle/internal/agent"
)

// subagentHealthRow is the wire shape of one client's marker coverage. Status is
// ok when the client sent traffic and at least one row carried its marker,
// absent when the client sent nothing in the window, and regressed when it sent
// traffic but never marked it.
type subagentHealthRow struct {
	Source    string  `json:"source"`
	Client    string  `json:"client"`
	Requests  int64   `json:"requests"`
	Marked    int64   `json:"marked"`
	MarkedPct float64 `json:"markedPct"`
	Status    string  `json:"status"`
}

const (
	subagentStatusOK        = "ok"
	subagentStatusAbsent    = "absent"
	subagentStatusRegressed = "regressed"
)

// subagentHealth classifies one client's coverage. A client with traffic and
// zero marked rows is reported as regressed even when it is legitimately not an
// agent (a plain SDK client): the check exists to catch a harness that stopped
// sending its marker, and a false warning is cheaper than a silent one. The
// console wording says the marker is expected only for agent clients.
func subagentHealth(client string, requests, marked int64) subagentHealthRow {
	row := subagentHealthRow{
		Source:   agent.SourceOfClient(client),
		Client:   client,
		Requests: requests,
		Marked:   marked,
	}
	switch {
	case requests == 0:
		row.Status = subagentStatusAbsent
	case marked == 0:
		row.Status = subagentStatusRegressed
	default:
		row.Status = subagentStatusOK
		row.MarkedPct = float64(marked) * 100 / float64(requests)
	}
	return row
}

// handleSubagentHealth reports, per known client, how much traffic it sent and
// how much of that carried its subagent marker. It is observation only: it
// never touches the hop group, the config or any marker the data plane reads.
func (s *Server) handleSubagentHealth(w http.ResponseWriter, r *http.Request) {
	if s.stats == nil {
		writeError(w, http.StatusServiceUnavailable, "stats disabled")
		return
	}
	// The shared parseRange silently falls back on a bad value, which is fine
	// for the chart but not here: a regression check must not quietly measure a
	// different window than the operator asked for.
	for _, key := range []string{"from", "to"} {
		if v := r.URL.Query().Get(key); v != "" {
			if _, err := time.Parse(time.RFC3339, v); err != nil {
				writeError(w, http.StatusBadRequest, "invalid "+key)
				return
			}
		}
	}
	from, to := parseRange(r)

	rows, err := s.stats.SubagentHealth(r.Context(), from, to)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	seen := make(map[string]bool, len(rows))
	out := make([]subagentHealthRow, 0, len(agent.KnownClients()))
	for _, client := range agent.KnownClients() {
		seen[client] = true
		var requests, marked int64
		for _, row := range rows {
			if row.Client == client {
				requests, marked = row.Requests, row.Marked
				break
			}
		}
		out = append(out, subagentHealth(client, requests, marked))
	}
	// A client the store knows but the marker table does not still deserves a
	// row: it means the UA mapping and the marker table drifted apart.
	for _, row := range rows {
		if seen[row.Client] {
			continue
		}
		out = append(out, subagentHealth(row.Client, row.Requests, row.Marked))
	}
	writeJSON(w, http.StatusOK, out)
}
