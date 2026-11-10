package admin

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/ginuerzh/cuttle/internal/config"
	"github.com/ginuerzh/cuttle/internal/identity"
)

// nodeIndexByID returns the index of the node with the id, or -1. It is the
// write-side lookup: mutations only address nodes by id.
func nodeIndexByID(c *config.Config, id string) int {
	if id == "" {
		return -1
	}
	for i := range c.Nodes {
		if c.Nodes[i].ID == id {
			return i
		}
	}
	return -1
}

// sameNodeRef reports whether a route's node reference points at n: the node
// id, or until Task 7 the display name for an id-less config.
func sameNodeRef(ref string, n config.Node) bool {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return false
	}
	return ref == n.ID || ref == strings.TrimSpace(n.Name)
}

func (s *Server) handleListNodes(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	writeJSON(w, http.StatusOK, nonNilSlice(s.cfg.Nodes))
}

func (s *Server) handleGetNode(w http.ResponseWriter, r *http.Request) {
	ref := r.PathValue("id")

	s.mu.Lock()
	defer s.mu.Unlock()

	// Reads still accept the display name until Task 7 removes the
	// transition fallback.
	if n, ok := s.cfg.Node(ref); ok {
		writeJSON(w, http.StatusOK, n)
		return
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

	// The server owns the id; a client-supplied one is ignored.
	id, err := identity.New(identity.Node)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	n.ID = id

	err = s.mutate(func(c *config.Config) error {
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
	id := r.PathValue("id")
	var n config.Node
	if err := decode(w, r, &n); err != nil {
		return
	}
	if n.ID != "" && n.ID != id {
		writeError(w, http.StatusBadRequest, "id in body does not match the path")
		return
	}
	n.ID = id

	err := s.mutate(func(c *config.Config) error {
		i := nodeIndexByID(c, id)
		if i < 0 {
			return fmt.Errorf("node %q not found", id)
		}
		if strings.TrimSpace(n.Name) == "" {
			// The name is a display label: an omitted one keeps the
			// stored label instead of blanking it.
			n.Name = c.Nodes[i].Name
		}
		c.Nodes[i] = n
		return nil
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteNode(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	err := s.mutate(func(c *config.Config) error {
		i := nodeIndexByID(c, id)
		if i < 0 {
			return fmt.Errorf("node %q not found", id)
		}
		target := c.Nodes[i]
		for _, rt := range c.Routes {
			for _, ref := range rt.Nodes {
				if sameNodeRef(ref, target) {
					return fmt.Errorf("node %s is used by route %s",
						entityLabel(target.Name, target.ID), entityLabel(rt.Name, rt.ID))
				}
			}
		}
		c.Nodes = append(c.Nodes[:i], c.Nodes[i+1:]...)
		return nil
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
