package admin

import (
	"context"

	"github.com/ginuerzh/cuttle/internal/identity"
	"github.com/ginuerzh/cuttle/internal/stats"
)

// labelSet resolves entity ids to display names for the statistics surfaces.
// Every kind maps an id to the last name the console showed for it.
type labelSet struct {
	Provider   map[string]string
	Node       map[string]string
	Route      map[string]string
	Entrypoint map[string]string
	GatewayKey map[string]string
}

// newLabelSet merges the labels the store kept with the current configuration.
// The store knows the names of entities that have since been deleted or
// renamed, and the config is the authority for everything it still lists, so
// the config wins on a conflict.
func (s *Server) newLabelSet(ctx context.Context) labelSet {
	var l labelSet
	if s.stats != nil {
		stored, err := s.stats.Labels(ctx)
		if err != nil {
			s.log.Warnf("load entity labels: %v", err)
		} else {
			l = labelSetFrom(stored)
		}
	}

	s.mu.Lock()
	current := s.cfg.LabelsByID()
	s.mu.Unlock()
	l.apply(labelSetFrom(current))
	return l
}

// labelSetFrom groups the store and config label maps by entity kind.
func labelSetFrom(labels map[string]map[string]string) labelSet {
	return labelSet{
		Provider:   labels[string(identity.Provider)],
		Node:       labels[string(identity.Node)],
		Route:      labels[string(identity.Route)],
		Entrypoint: labels[string(identity.Entrypoint)],
		GatewayKey: labels[string(identity.GatewayKey)],
	}
}

// apply overlays other on l. Names from other win, and a nil map in l takes
// other's map.
func (l *labelSet) apply(other labelSet) {
	for _, item := range []struct {
		dst *map[string]string
		src map[string]string
	}{
		{&l.Provider, other.Provider},
		{&l.Node, other.Node},
		{&l.Route, other.Route},
		{&l.Entrypoint, other.Entrypoint},
		{&l.GatewayKey, other.GatewayKey},
	} {
		if len(item.src) == 0 {
			continue
		}
		if *item.dst == nil {
			*item.dst = make(map[string]string, len(item.src))
		}
		for id, name := range item.src {
			(*item.dst)[id] = name
		}
	}
}

// label resolves id in m. An id the labels do not know is shown as itself, so
// a stats row never loses its reference when the entity is gone.
func label(m map[string]string, id string) string {
	if id == "" {
		return ""
	}
	if name := m[id]; name != "" {
		return name
	}
	return id
}

func (l labelSet) provider(id string) string   { return label(l.Provider, id) }
func (l labelSet) node(id string) string       { return label(l.Node, id) }
func (l labelSet) route(id string) string      { return label(l.Route, id) }
func (l labelSet) entrypoint(id string) string { return label(l.Entrypoint, id) }
func (l labelSet) gatewayKey(id string) string { return label(l.GatewayKey, id) }

// requestView is one request row with the display names of the entities it
// refers to: a row stores ids, and the labels come from the current config,
// then the last known name, then the raw id.
type requestView struct {
	stats.Request
	ProviderName   string `json:"providerName"`
	NodeName       string `json:"nodeName"`
	RouteName      string `json:"routeName"`
	GatewayKeyName string `json:"gatewayKeyName"`
}

func (l labelSet) requestView(row stats.Request) requestView {
	return requestView{
		Request:        row,
		ProviderName:   l.provider(row.Provider),
		NodeName:       l.node(row.Node),
		RouteName:      l.route(row.Route),
		GatewayKeyName: l.gatewayKey(row.GatewayKey),
	}
}
