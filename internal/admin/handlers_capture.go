package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type captureRuleInput struct {
	Route       string `json:"route"`
	ClientModel string `json:"clientModel"`
	Reason      string `json:"reason"`
	Limit       int    `json:"limit"`
	TTLSeconds  int    `json:"ttlSeconds"`
}

func (s *Server) handleListCaptures(w http.ResponseWriter, r *http.Request) {
	if s.capture == nil {
		writeError(w, http.StatusServiceUnavailable, "capture manager is not available")
		return
	}
	writeJSON(w, http.StatusOK, nonNilSlice(s.capture.Rules()))
}

func (s *Server) handleCreateCapture(w http.ResponseWriter, r *http.Request) {
	if s.capture == nil {
		writeError(w, http.StatusServiceUnavailable, "capture manager is not available")
		return
	}
	var in captureRuleInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if in.TTLSeconds <= 0 {
		writeError(w, http.StatusBadRequest, "ttlSeconds must be positive")
		return
	}
	if route := strings.TrimSpace(in.Route); route != "" {
		s.mu.Lock()
		found := false
		for _, r := range s.cfg.Routes {
			if r.Name == route {
				found = true
				break
			}
		}
		s.mu.Unlock()
		if !found {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("route %q does not exist", route))
			return
		}
	}
	rule, err := s.capture.AddRule(in.Route, in.ClientModel, in.Reason, in.Limit,
		time.Duration(in.TTLSeconds)*time.Second)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, rule)
}

func (s *Server) handleDeleteCapture(w http.ResponseWriter, r *http.Request) {
	if s.capture == nil {
		writeError(w, http.StatusServiceUnavailable, "capture manager is not available")
		return
	}
	if !s.capture.RemoveRule(r.PathValue("id")) {
		writeError(w, http.StatusNotFound, fmt.Sprintf("capture rule %q not found", r.PathValue("id")))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
