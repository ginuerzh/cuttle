package provider

import "github.com/ginuerzh/cuttle/internal/config"

// genericAdapter has no static knowledge of models. Models are discovered
// from every configured API base and keep the API that listed them.
type genericAdapter struct{}

func (genericAdapter) classify(string) []API { return nil }

func (genericAdapter) auth(api API) config.Auth { return defaultAuth(api) }

func (genericAdapter) listAll() bool { return true }

func (genericAdapter) keylessCatalog() bool { return false }
