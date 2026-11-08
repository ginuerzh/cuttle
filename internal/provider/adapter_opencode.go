package provider

import "github.com/ginuerzh/cuttle/internal/config"

// openCodeAdapter covers both OpenCode gateways. Their model catalogs and
// base URLs are independent; only the per-family API rules are shared.
type openCodeAdapter struct{}

func (openCodeAdapter) classify(id string) []API {
	switch {
	case containsFold(id, "deepseek"):
		return []API{Chat}
	case containsFold(id, "glm"):
		return []API{Anthropic}
	default:
		return nil
	}
}

func (openCodeAdapter) auth(api API) config.Auth { return openCodeAuth(api) }

func (openCodeAdapter) listAll() bool { return false }

// keylessCatalog: OpenCode's catalog endpoints are public. They also reject an
// invalid bearer token even though the catalog needs no auth, so sending a
// configured placeholder key would break model discovery.
func (openCodeAdapter) keylessCatalog() bool { return true }
