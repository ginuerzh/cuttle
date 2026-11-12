package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-gost/core/logger"

	"github.com/ginuerzh/cuttle/internal/identity"
)

// MigrateIdentities gives every entity a stable id and rewrites the name-based
// associations to ids: node.provider, node.key (scoped to the node's own
// provider), route.nodes and entrypoints[].routes[].route.
//
// Ids derive from the entity name, so a config that cannot be written back (a
// read-only mount) derives the same ids on every load. Provider key ids also
// include their provider id, because key names are only unique per provider.
//
// changed reports whether the config needed any assignment or rewrite. A
// reference that is neither a known id nor a unique name is an error naming
// the entity, the field and the offending value.
func MigrateIdentities(c *Config) (changed bool, err error) {
	if c == nil {
		return false, fmt.Errorf("config is nil")
	}

	providerIDs := make(map[string]struct{}, len(c.Providers))
	providerNames := make(map[string][]string, len(c.Providers))
	providersByID := make(map[string]*Provider, len(c.Providers))
	for i := range c.Providers {
		p := &c.Providers[i]
		if p.ID == "" {
			p.ID = identity.Derive(identity.Provider, strings.TrimSpace(p.Name))
			changed = true
		}
		for j := range p.Keys {
			k := &p.Keys[j]
			if k.ID == "" {
				k.ID = identity.Derive(identity.ProviderKey, p.ID+"\x00"+strings.TrimSpace(k.Name))
				changed = true
			}
		}
		providerIDs[p.ID] = struct{}{}
		name := strings.TrimSpace(p.Name)
		providerNames[name] = append(providerNames[name], p.ID)
		providersByID[p.ID] = p
	}

	nodeIDs := make(map[string]struct{}, len(c.Nodes))
	nodeNames := make(map[string][]string, len(c.Nodes))
	for i := range c.Nodes {
		n := &c.Nodes[i]
		if n.ID == "" {
			n.ID = identity.Derive(identity.Node, strings.TrimSpace(n.Name))
			changed = true
		}
		nodeIDs[n.ID] = struct{}{}
		name := strings.TrimSpace(n.Name)
		nodeNames[name] = append(nodeNames[name], n.ID)
	}

	routeIDs := make(map[string]struct{}, len(c.Routes))
	routeNames := make(map[string][]string, len(c.Routes))
	for i := range c.Routes {
		r := &c.Routes[i]
		if r.ID == "" {
			r.ID = identity.Derive(identity.Route, strings.TrimSpace(r.Name))
			changed = true
		}
		routeIDs[r.ID] = struct{}{}
		name := strings.TrimSpace(r.Name)
		routeNames[name] = append(routeNames[name], r.ID)
	}

	for i := range c.Entrypoints {
		e := &c.Entrypoints[i]
		if e.ID == "" {
			e.ID = identity.Derive(identity.Entrypoint, strings.TrimSpace(e.Name))
			changed = true
		}
	}
	for i := range c.Gateway.Keys {
		k := &c.Gateway.Keys[i]
		if k.ID == "" {
			k.ID = identity.Derive(identity.GatewayKey, strings.TrimSpace(k.Name))
			changed = true
		}
	}

	for i := range c.Nodes {
		n := &c.Nodes[i]
		scope := fmt.Sprintf("node %q", strings.TrimSpace(n.Name))
		providerRef := strings.TrimSpace(n.Provider)
		if providerRef == "" {
			// Validate reports the missing provider.
			continue
		}
		providerID, err := resolveReference(scope, "provider", providerRef, providerIDs, providerNames)
		if err != nil {
			return changed, err
		}
		if n.Provider != providerID {
			n.Provider = providerID
			changed = true
		}
		keyRef := strings.TrimSpace(n.Key)
		if keyRef == "" {
			continue
		}
		keyIDs := make(map[string]struct{}, len(providersByID[providerID].Keys))
		keyNames := make(map[string][]string, len(providersByID[providerID].Keys))
		for _, k := range providersByID[providerID].Keys {
			keyIDs[k.ID] = struct{}{}
			name := strings.TrimSpace(k.Name)
			keyNames[name] = append(keyNames[name], k.ID)
		}
		keyID, err := resolveReference(scope, "key", keyRef, keyIDs, keyNames)
		if err != nil {
			return changed, fmt.Errorf("%w on provider %q", err, strings.TrimSpace(providersByID[providerID].Name))
		}
		if n.Key != keyID {
			n.Key = keyID
			changed = true
		}
	}

	for i := range c.Routes {
		r := &c.Routes[i]
		scope := fmt.Sprintf("route %q", strings.TrimSpace(r.Name))
		for j, ref := range r.Nodes {
			ref = strings.TrimSpace(ref)
			if ref == "" {
				// Validate reports the empty reference.
				continue
			}
			nodeID, err := resolveReference(scope, "node", ref, nodeIDs, nodeNames)
			if err != nil {
				return changed, err
			}
			if r.Nodes[j] != nodeID {
				r.Nodes[j] = nodeID
				changed = true
			}
		}
	}

	for i := range c.Entrypoints {
		e := &c.Entrypoints[i]
		scope := fmt.Sprintf("entrypoint %q", strings.TrimSpace(e.Name))
		for j := range e.Routes {
			ref := strings.TrimSpace(e.Routes[j].Route)
			if ref == "" {
				// Validate reports the missing route.
				continue
			}
			routeID, err := resolveReference(scope, "route", ref, routeIDs, routeNames)
			if err != nil {
				return changed, err
			}
			if e.Routes[j].Route != routeID {
				e.Routes[j].Route = routeID
				changed = true
			}
		}
	}

	return changed, nil
}

// resolveReference returns the id a reference points at: ref itself when it is
// a known id, otherwise the id of the single entity with that name.
func resolveReference(scope, field, ref string, ids map[string]struct{}, names map[string][]string) (string, error) {
	if _, ok := ids[ref]; ok {
		return ref, nil
	}
	if matches := names[ref]; len(matches) == 1 {
		return matches[0], nil
	}
	return "", fmt.Errorf("%s: %s reference %q is neither an id nor a unique %s name", scope, field, ref, field)
}

// writeBackupFile writes the pre-migration copy. A test replaces it to force a
// write-back failure on a host where file permissions cannot (root).
var writeBackupFile = os.WriteFile

// writeBackMigrated copies path to path.bak-<unix> and rewrites it with the
// migrated config. Failures never abort a load: a read-only config mount keeps
// serving, and derived ids stay stable across restarts.
//
// A symlinked config is left untouched with a warning. os.Rename over the link
// would replace the link itself, not the hand-maintained file it points at.
func writeBackMigrated(path string, c *Config, l logger.Logger) {
	info, err := os.Lstat(path)
	if err != nil {
		migrationWarn(l, "read %s: %v", path, err)
		return
	}
	if info.Mode()&os.ModeSymlink != 0 {
		migrationWarn(l, "skip write-back for symlinked config %s", path)
		return
	}
	original, err := os.ReadFile(path)
	if err != nil {
		migrationWarn(l, "read %s: %v", path, err)
		return
	}
	backup := path + ".bak-" + strconv.FormatInt(time.Now().Unix(), 10)
	if err := writeBackupFile(backup, original, 0o600); err != nil {
		// Without a backup the rewrite could lose the user's file.
		migrationWarn(l, "backup %s: %v", backup, err)
		return
	}
	if err := Write(path, c); err != nil {
		migrationWarn(l, "write %s: %v", path, err)
		return
	}
	if l != nil {
		l.Infof("config migrated: entity ids written to %s (backup %s)", path, backup)
	}
}

func migrationWarn(l logger.Logger, format string, args ...any) {
	if l != nil {
		l.Warnf("config migration: "+format, args...)
	}
}
