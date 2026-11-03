package admin

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/go-gost/llm-gw/internal/config"
)

func (s *Server) handleListNodes(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	writeJSON(w, http.StatusOK, s.cfg.Nodes)
}

func (s *Server) handleGetNode(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, n := range s.cfg.Nodes {
		if n.Name == name {
			writeJSON(w, http.StatusOK, n)
			return
		}
	}
	writeError(w, http.StatusNotFound, "node not found")
}

func (s *Server) handleCreateNode(w http.ResponseWriter, r *http.Request) {
	var n config.Node
	if err := decode(w, r, &n); err != nil {
		return
	}
	if strings.TrimSpace(n.Name) == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	err := s.mutate(func(c *config.Config) error {
		for _, existing := range c.Nodes {
			if existing.Name == n.Name {
				return fmt.Errorf("node %q already exists", n.Name)
			}
		}
		c.Nodes = append(c.Nodes, n)
		return nil
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (s *Server) handleUpdateNode(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var n config.Node
	if err := decode(w, r, &n); err != nil {
		return
	}
	if n.Name != "" && n.Name != name {
		writeError(w, http.StatusBadRequest, "name in body does not match the path")
		return
	}
	n.Name = name
	err := s.mutate(func(c *config.Config) error {
		for i := range c.Nodes {
			if c.Nodes[i].Name == name {
				c.Nodes[i] = n
				return nil
			}
		}
		return fmt.Errorf("node %q not found", name)
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteNode(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	err := s.mutate(func(c *config.Config) error {
		for _, rt := range c.Routes {
			for _, ref := range rt.Nodes {
				if ref == name {
					return fmt.Errorf("node %q is used by route %q", name, rt.Name)
				}
			}
		}
		out := c.Nodes[:0]
		found := false
		for _, n := range c.Nodes {
			if n.Name == name {
				found = true
				continue
			}
			out = append(out, n)
		}
		if !found {
			return fmt.Errorf("node %q not found", name)
		}
		c.Nodes = out
		return nil
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
