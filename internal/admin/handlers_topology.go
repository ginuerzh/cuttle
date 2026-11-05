package admin

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"

	"github.com/go-gost/llm-gw/internal/stats"
)

const maxTopologyPositions = 10000

type topologyPositionsView struct {
	Positions map[string]stats.Position `json:"positions"`
}

func (s *Server) handleGetTopologyPositions(w http.ResponseWriter, r *http.Request) {
	if s.stats == nil {
		writeError(w, http.StatusServiceUnavailable, "stats store is not available")
		return
	}
	positions, err := s.stats.LoadTopologyPositions(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if positions == nil {
		positions = map[string]stats.Position{}
	}
	writeJSON(w, http.StatusOK, topologyPositionsView{Positions: positions})
}

func (s *Server) handlePutTopologyPositions(w http.ResponseWriter, r *http.Request) {
	if s.stats == nil {
		writeError(w, http.StatusServiceUnavailable, "stats store is not available")
		return
	}
	var in topologyPositionsView
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := dec.Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateTopologyPositions(in.Positions); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.stats.SaveTopologyPositions(r.Context(), in.Positions); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func validateTopologyPositions(positions map[string]stats.Position) error {
	if len(positions) > maxTopologyPositions {
		return fmt.Errorf("too many positions: %d", len(positions))
	}
	for id, p := range positions {
		if strings.TrimSpace(id) == "" {
			return fmt.Errorf("node id is required")
		}
		if len(id) > 256 {
			return fmt.Errorf("node id is too long")
		}
		if math.IsNaN(p.X) || math.IsInf(p.X, 0) || math.IsNaN(p.Y) || math.IsInf(p.Y, 0) {
			return fmt.Errorf("position for %q is not finite", id)
		}
	}
	return nil
}
