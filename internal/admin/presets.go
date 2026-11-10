package admin

// Preset fills a new provider with a type and the base URL of every API the
// upstream endpoint serves. Empty fields mean the endpoint does not offer that
// API. A preset carries no id: the server assigns one on create.
type Preset struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	Chat      string `json:"chat,omitempty"`
	Responses string `json:"responses,omitempty"`
	Anthropic string `json:"anthropic,omitempty"`
	Gemini    string `json:"gemini,omitempty"`
}

var presets = []Preset{
	{
		Name:      "deepseek",
		Type:      "deepseek",
		Chat:      "https://api.deepseek.com/v1",
		Responses: "https://api.deepseek.com/v1",
		Anthropic: "https://api.deepseek.com/anthropic",
	},
	{
		Name:      "opencode-go",
		Type:      "opencode-go",
		Chat:      "https://opencode.ai/inference/go/openai/v1",
		Responses: "https://opencode.ai/inference/go/openai/v1",
		Anthropic: "https://opencode.ai/inference/go/anthropic",
	},
	{
		Name:      "opencode-zen",
		Type:      "opencode-zen",
		Chat:      "https://opencode.ai/zen/v1",
		Responses: "https://opencode.ai/zen/v1",
		Anthropic: "https://opencode.ai/zen",
	},
	{
		Name:      "commandcode",
		Type:      "commandcode",
		Chat:      "https://api.commandcode.ai/provider/v1",
		Responses: "https://api.commandcode.ai/provider/v1",
		Anthropic: "https://api.commandcode.ai/provider",
	},
	{
		Name:      "openrouter",
		Type:      "openrouter",
		Chat:      "https://openrouter.ai/api/v1",
		Responses: "https://openrouter.ai/api/v1",
		Anthropic: "https://openrouter.ai/api",
	},
}
