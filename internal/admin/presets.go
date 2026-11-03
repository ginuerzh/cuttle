package admin

import "github.com/go-gost/llm-gw/internal/config"

// Preset is a template used when creating a provider. The chosen wire decides
// the protocol, pathMap and auth that are written into the config.
type Preset struct {
	Name       string            `json:"name"`
	Wire       string            `json:"wire"`
	BaseURL    string            `json:"baseURL"`
	Protocol   string            `json:"protocol"`
	PathMap    map[string]string `json:"pathMap"`
	Auth       config.Auth       `json:"auth"`
	HealthPath string            `json:"healthPath"`
}

var presets = []Preset{
	{
		Name:       "deepseek",
		Wire:       "openai",
		BaseURL:    "https://api.deepseek.com",
		Protocol:   "openai",
		PathMap:    map[string]string{"/v1/chat/completions": "/chat/completions", "/v1/responses": "/chat/completions"},
		Auth:       config.Auth{Header: "Authorization", Prefix: "Bearer "},
		HealthPath: "/models",
	},
	{
		Name:       "deepseek-anthropic",
		Wire:       "anthropic",
		BaseURL:    "https://api.deepseek.com",
		Protocol:   "anthropic",
		PathMap:    map[string]string{"/v1/messages": "/anthropic/v1/messages"},
		Auth:       config.Auth{Header: "x-api-key"},
		HealthPath: "/models",
	},
	{
		Name:       "opencode",
		Wire:       "openai",
		BaseURL:    "https://opencode.ai",
		Protocol:   "openai",
		PathMap:    map[string]string{"/v1/messages": "/zen/go/v1/chat/completions", "/v1/responses": "/zen/go/v1/chat/completions", "/v1/chat/completions": "/zen/go/v1/chat/completions"},
		Auth:       config.Auth{Header: "Authorization", Prefix: "Bearer ", KeyHeaders: []config.KeyHeader{{Header: "x-api-key"}}},
		HealthPath: "/zen/go/v1/models",
	},
	{
		Name:       "opencode-anthropic",
		Wire:       "anthropic",
		BaseURL:    "https://opencode.ai",
		Protocol:   "anthropic",
		PathMap:    map[string]string{"/v1/messages": "/zen/v1/messages"},
		Auth:       config.Auth{Header: "x-api-key"},
		HealthPath: "/zen/v1/models",
	},
	{
		Name:       "commandcode",
		Wire:       "openai",
		BaseURL:    "https://api.commandcode.ai",
		Protocol:   "openai",
		PathMap:    map[string]string{"/v1/chat/completions": "/provider/v1/chat/completions", "/v1/responses": "/provider/v1/responses"},
		Auth:       config.Auth{Header: "Authorization", Prefix: "Bearer "},
		HealthPath: "/provider/v1/models",
	},
	{
		Name:       "commandcode-anthropic",
		Wire:       "anthropic",
		BaseURL:    "https://api.commandcode.ai",
		Protocol:   "anthropic",
		PathMap:    map[string]string{"/v1/messages": "/provider/v1/messages"},
		Auth:       config.Auth{Header: "Authorization", Prefix: "Bearer "},
		HealthPath: "/provider/v1/models",
	},
}
