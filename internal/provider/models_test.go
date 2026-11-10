package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/ginuerzh/cuttle/internal/config"
)

func newModelServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

func findModel(models []Model, id string) (Model, bool) {
	for _, m := range models {
		if m.ID == id {
			return m, true
		}
	}
	return Model{}, false
}

func TestModelsListShapes(t *testing.T) {
	t.Run("openai", func(t *testing.T) {
		srv := newModelServer(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v1/models" {
				t.Errorf("path = %q, want /v1/models", r.URL.Path)
			}
			if got := r.Header.Get("Authorization"); got != "Bearer sk-test" {
				t.Errorf("Authorization = %q", got)
			}
			fmt.Fprint(w, `{"data":[{"id":"chat-model","name":"Chat Model"}]}`)
		})
		p := mustProvider(t, config.Provider{
			Name: "p",
			Type: "generic",
			Chat: srv.URL + "/v1",
			Keys: []config.ProviderKey{{Name: "key-1", Key: "sk-test"}},
		})

		models, err := p.Models(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(models) != 1 {
			t.Fatalf("models = %+v, want one", models)
		}
		if models[0].ID != "chat-model" || models[0].Name != "Chat Model" {
			t.Fatalf("model = %+v", models[0])
		}
		if !slices.Equal(models[0].APIs, []API{Chat}) {
			t.Fatalf("APIs = %v, want [chat]", models[0].APIs)
		}
	})

	t.Run("anthropic", func(t *testing.T) {
		srv := newModelServer(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v1/models" {
				t.Errorf("path = %q, want /v1/models", r.URL.Path)
			}
			if got := r.Header.Get("x-api-key"); got != "sk-test" {
				t.Errorf("x-api-key = %q", got)
			}
			fmt.Fprint(w, `{"data":[{"id":"claude-model","display_name":"Claude Model"}]}`)
		})
		p := mustProvider(t, config.Provider{
			Name:      "p",
			Type:      "generic",
			Anthropic: srv.URL,
			Keys:      []config.ProviderKey{{Name: "key-1", Key: "sk-test"}},
		})

		models, err := p.Models(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(models) != 1 || models[0].ID != "claude-model" || models[0].Name != "Claude Model" {
			t.Fatalf("models = %+v", models)
		}
		if !slices.Equal(models[0].APIs, []API{Anthropic}) {
			t.Fatalf("APIs = %v, want [anthropic]", models[0].APIs)
		}
	})
}

func TestModelsGeminiNamesAreStripped(t *testing.T) {
	srv := newModelServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1beta/models" {
			t.Errorf("path = %q, want /v1beta/models", r.URL.Path)
		}
		if got := r.Header.Get("x-goog-api-key"); got != "sk-test" {
			t.Errorf("x-goog-api-key = %q", got)
		}
		fmt.Fprint(w, `{"models":[
			{"name":"models/gemini-2.5-pro","displayName":"Gemini 2.5 Pro"},
			{"name":"models/gemini-2.5-flash"}
		]}`)
	})
	p := mustProvider(t, config.Provider{
		Name:   "p",
		Type:   "generic",
		Gemini: srv.URL + "/v1beta",
		Keys:   []config.ProviderKey{{Name: "key-1", Key: "sk-test"}},
	})

	models, err := p.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 {
		t.Fatalf("models = %+v, want two", models)
	}
	if models[0].ID != "gemini-2.5-pro" || models[0].Name != "Gemini 2.5 Pro" {
		t.Fatalf("first model = %+v", models[0])
	}
	if models[1].ID != "gemini-2.5-flash" || models[1].Name != "gemini-2.5-flash" {
		t.Fatalf("second model = %+v", models[1])
	}
	if !slices.Equal(models[0].APIs, []API{Gemini}) {
		t.Fatalf("APIs = %v, want [gemini]", models[0].APIs)
	}
}

func TestModelsPartialFailureKeepsOtherAPIs(t *testing.T) {
	chatSrv := newModelServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":[{"id":"ok-model"}]}`)
	})
	anthropicSrv := newModelServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	p := mustProvider(t, config.Provider{
		Name:      "p",
		Type:      "generic",
		Chat:      chatSrv.URL,
		Anthropic: anthropicSrv.URL,
	})

	models, err := p.Models(context.Background())
	if err != nil {
		t.Fatalf("Models returned an error after one API succeeded: %v", err)
	}
	if len(models) != 1 || models[0].ID != "ok-model" {
		t.Fatalf("models = %+v, want ok-model", models)
	}
	if !slices.Equal(models[0].APIs, []API{Chat}) {
		t.Fatalf("APIs = %v, want [chat]", models[0].APIs)
	}
}

func TestModelsGenericRecordsSourceAPI(t *testing.T) {
	srv := newModelServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			fmt.Fprint(w, `{"data":[{"id":"chat-model"}]}`)
		case "/anthropic/v1/models":
			fmt.Fprint(w, `{"data":[{"id":"anthropic-model"}]}`)
		default:
			http.NotFound(w, r)
		}
	})
	p := mustProvider(t, config.Provider{
		Name:      "p",
		Type:      "generic",
		Chat:      srv.URL + "/v1",
		Anthropic: srv.URL + "/anthropic",
	})

	models, err := p.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	chatModel, ok := findModel(models, "chat-model")
	if !ok || !slices.Equal(chatModel.APIs, []API{Chat}) {
		t.Fatalf("chat-model = %+v", chatModel)
	}
	anthropicModel, ok := findModel(models, "anthropic-model")
	if !ok || !slices.Equal(anthropicModel.APIs, []API{Anthropic}) {
		t.Fatalf("anthropic-model = %+v", anthropicModel)
	}
}

func TestModelsVendorUsesChatPrimaryAndStaticRules(t *testing.T) {
	var requests int
	srv := newModelServer(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/models" {
			t.Errorf("path = %q, want /models", r.URL.Path)
		}
		fmt.Fprint(w, `{"data":[{"id":"deepseek-chat"}]}`)
	})
	p := mustProvider(t, config.Provider{
		Name:      "p",
		Type:      "deepseek",
		Chat:      srv.URL,
		Responses: srv.URL,
		Anthropic: srv.URL + "/anthropic",
	})

	models, err := p.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want one chat listing", requests)
	}
	if len(models) != 1 || models[0].ID != "deepseek-chat" {
		t.Fatalf("models = %+v", models)
	}
	if !slices.Equal(models[0].APIs, []API{Chat, Responses, Anthropic}) {
		t.Fatalf("APIs = %v, want static deepseek classification", models[0].APIs)
	}
}

func TestModelsVendorFallsBackToFirstConfiguredAPI(t *testing.T) {
	var paths []string
	srv := newModelServer(t, func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		fmt.Fprint(w, `{"data":[{"id":"deepseek-chat"}]}`)
	})
	p := mustProvider(t, config.Provider{
		Name:      "p",
		Type:      "deepseek",
		Anthropic: srv.URL + "/anthropic",
	})

	models, err := p.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(paths, []string{"/anthropic/v1/models"}) {
		t.Fatalf("paths = %v, want the anthropic list endpoint", paths)
	}
	if len(models) != 1 || models[0].ID != "deepseek-chat" {
		t.Fatalf("models = %+v", models)
	}
}

func TestModelsOpenRouterMetadata(t *testing.T) {
	srv := newModelServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/models" {
			t.Errorf("path = %q, want /api/v1/models", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("Authorization = %q, want empty for a public catalog", got)
		}
		fmt.Fprint(w, `{"data":[
			{"id":"anthropic/claude-sonnet-4","name":"Anthropic: Claude Sonnet 4","context_length":200000,
			 "architecture":{"input_modalities":["text","image"],"output_modalities":["text"]},
			 "pricing":{"prompt":"0.000003","completion":"0.000015"},
			 "top_provider":{"max_completion_tokens":64000},
			 "endpoints":["/api/v1/chat/completions","/api/v1/responses","/api/v1/messages"]},
			{"id":"free/model","name":"Free Model","context_length":32000,
			 "pricing":{"prompt":"0","completion":"0"},
			 "endpoints":["/api/v1/chat/completions"]},
			{"id":"chat-only","name":"Chat Only","context_length":8000,
			 "pricing":{"prompt":"0.000001","completion":"0.000002"},
			 "endpoints":["/api/v1/chat/completions"]},
			{"id":"router/auto","name":"Auto Router","context_length":200000,
			 "pricing":{"prompt":"-1","completion":"-1"},
			 "endpoints":["/api/v1/chat/completions"]},
			{"id":"image-only","name":"Image Only",
			 "endpoints":["/api/v1/images"]}
		]}`)
	})
	p := mustProvider(t, config.Provider{
		Name:      "openrouter",
		Type:      "openrouter",
		Chat:      srv.URL + "/api/v1",
		Responses: srv.URL + "/api/v1",
		Anthropic: srv.URL + "/api",
		Keys:      []config.ProviderKey{{Name: "key-1", Key: "sk-or-test"}},
	})

	models, err := p.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	claude, ok := findModel(models, "anthropic/claude-sonnet-4")
	if !ok {
		t.Fatalf("anthropic model missing from %+v", models)
	}
	if !slices.Equal(claude.APIs, []API{Chat, Responses, Anthropic}) {
		t.Fatalf("anthropic APIs = %v", claude.APIs)
	}
	if claude.Context != 200000 || claude.Output != 64000 {
		t.Fatalf("anthropic limits = context %d output %d", claude.Context, claude.Output)
	}
	if !slices.Equal(claude.Inputs, []string{"text", "image"}) || !slices.Equal(claude.Outputs, []string{"text"}) {
		t.Fatalf("anthropic modalities = %v -> %v", claude.Inputs, claude.Outputs)
	}
	if claude.Price == nil || math.Abs(claude.Price.Input-3) > 1e-9 || math.Abs(claude.Price.Output-15) > 1e-9 {
		t.Fatalf("anthropic price = %+v, want 3/15", claude.Price)
	}

	free, ok := findModel(models, "free/model")
	if !ok || free.Price == nil || free.Price.Input != 0 || free.Price.Output != 0 {
		t.Fatalf("free model = %+v, want a 0/0 price", free)
	}

	chatOnly, ok := findModel(models, "chat-only")
	if !ok || !slices.Equal(chatOnly.APIs, []API{Chat}) {
		t.Fatalf("chat-only model = %+v, want [chat]", chatOnly)
	}

	router, ok := findModel(models, "router/auto")
	if !ok {
		t.Fatal("router model missing")
	}
	if router.Price != nil {
		t.Fatalf("router price = %+v, want nil for the -1 sentinel", router.Price)
	}

	if _, ok := findModel(models, "image-only"); ok {
		t.Fatal("image-only model without a text endpoint should be filtered out")
	}
}

func TestModelsEmptyListIsNonNil(t *testing.T) {
	srv := newModelServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":[]}`)
	})
	p := mustProvider(t, config.Provider{
		Name: "p",
		Type: "generic",
		Chat: srv.URL,
	})

	models, err := p.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if models == nil {
		t.Fatal("Models returned a nil catalog for a successful empty list")
	}
	if len(models) != 0 {
		t.Fatalf("models = %+v, want empty", models)
	}
}

func TestModelsAllAPIsFail(t *testing.T) {
	srv := newModelServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusBadGateway)
	})
	p := mustProvider(t, config.Provider{
		Name:      "p",
		Type:      "generic",
		Chat:      srv.URL,
		Responses: srv.URL,
	})

	if _, err := p.Models(context.Background()); err == nil {
		t.Fatal("Models should fail when every API listing fails")
	}
}

func TestModelsAppliesAuthWithoutKeys(t *testing.T) {
	srv := newModelServer(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Org"); got != "acme" {
			t.Errorf("X-Org = %q, want acme", got)
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("Authorization = %q, want empty for a keyless provider", got)
		}
		fmt.Fprint(w, `{"data":[{"id":"chat-model"}]}`)
	})
	p := mustProvider(t, config.Provider{
		Name: "p",
		Type: "generic",
		Chat: srv.URL + "/v1",
		Auth: config.Auth{Extra: map[string]string{"X-Org": "acme"}},
	})

	if _, err := p.Models(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestModelsAppliesKeyHeadersWithKey(t *testing.T) {
	srv := newModelServer(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer sk-test" {
			t.Errorf("Authorization = %q, want the adapter default", got)
		}
		if got := r.Header.Get("X-Extra"); got != "Key sk-test" {
			t.Errorf("X-Extra = %q, want Key sk-test", got)
		}
		fmt.Fprint(w, `{"data":[{"id":"chat-model"}]}`)
	})
	p := mustProvider(t, config.Provider{
		Name: "p",
		Type: "generic",
		Chat: srv.URL + "/v1",
		Keys: []config.ProviderKey{{Name: "key-1", Key: "sk-test"}},
		Auth: config.Auth{KeyHeaders: []config.KeyHeader{{Header: "X-Extra", Prefix: "Key "}}},
	})

	if _, err := p.Models(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestModelsAnthropicVersionHeader(t *testing.T) {
	cases := []struct {
		name string
		auth config.Auth
		want string
	}{
		{name: "default", want: AnthropicVersion},
		{
			name: "extra override",
			auth: config.Auth{Extra: map[string]string{"anthropic-version": "2024-01-01"}},
			want: "2024-01-01",
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			srv := newModelServer(t, func(w http.ResponseWriter, r *http.Request) {
				if got := r.Header.Get("anthropic-version"); got != tt.want {
					t.Errorf("anthropic-version = %q, want %q", got, tt.want)
				}
				fmt.Fprint(w, `{"data":[{"id":"claude-model"}]}`)
			})
			p := mustProvider(t, config.Provider{
				Name:      "p",
				Type:      "generic",
				Anthropic: srv.URL,
				Auth:      tt.auth,
			})

			if _, err := p.Models(context.Background()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestModelsIntersectAPIsWithConfiguredBases(t *testing.T) {
	srv := newModelServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":[{"id":"deepseek-v4-flash"},{"id":"glm-4.8"}]}`)
	})
	p := mustProvider(t, config.Provider{
		Name: "p",
		Type: "opencode-go",
		Chat: srv.URL + "/v1",
	})

	models, err := p.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	deepseek, ok := findModel(models, "deepseek-v4-flash")
	if !ok || !slices.Equal(deepseek.APIs, []API{Chat}) {
		t.Fatalf("deepseek-v4-flash = %+v, want [chat]", deepseek)
	}
	glm, ok := findModel(models, "glm-4.8")
	if !ok {
		t.Fatalf("models = %+v, want glm-4.8 even though it has no usable API", models)
	}
	if len(glm.APIs) != 0 {
		t.Fatalf("glm-4.8 APIs = %v, want empty when only chat is configured", glm.APIs)
	}
}

func TestModelsOpenCodeCatalogIsKeyless(t *testing.T) {
	srv := newModelServer(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "" {
			http.Error(w, `{"error":{"message":"Invalid credential"}}`, http.StatusUnauthorized)
			return
		}
		if got := r.Header.Get("x-api-key"); got != "" {
			t.Errorf("x-api-key = %q, want empty for a keyless catalog", got)
		}
		if got := r.Header.Get("X-Org"); got != "acme" {
			t.Errorf("X-Org = %q, want acme", got)
		}
		fmt.Fprint(w, `{"data":[{"id":"deepseek-v4-flash"}]}`)
	})
	p := mustProvider(t, config.Provider{
		Name: "opencode-go",
		Type: "opencode-go",
		Chat: srv.URL + "/v1",
		Keys: []config.ProviderKey{{Name: "key-1", Key: "invalid-key"}},
		Auth: config.Auth{Extra: map[string]string{"X-Org": "acme"}},
	})

	models, err := p.Models(context.Background())
	if err != nil {
		t.Fatalf("Models = %v, want a keyless catalog", err)
	}
	if len(models) != 1 || models[0].ID != "deepseek-v4-flash" {
		t.Fatalf("models = %+v", models)
	}
}

func TestModelsOpenCodeKeylessHonorsExplicitAuthHeader(t *testing.T) {
	srv := newModelServer(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "" {
			http.Error(w, "unexpected bearer auth", http.StatusUnauthorized)
			return
		}
		if got := r.Header.Get("X-Custom"); got != "Token invalid-key" {
			t.Errorf("X-Custom = %q, want the configured full override", got)
		}
		fmt.Fprint(w, `{"data":[{"id":"deepseek-v4-flash"}]}`)
	})
	p := mustProvider(t, config.Provider{
		Name: "opencode-go",
		Type: "opencode-go",
		Chat: srv.URL + "/v1",
		Keys: []config.ProviderKey{{Name: "key-1", Key: "invalid-key"}},
		Auth: config.Auth{Header: "X-Custom", Prefix: "Token "},
	})

	if _, err := p.Models(context.Background()); err != nil {
		t.Fatalf("Models = %v, want the explicit auth header honored", err)
	}
}

func TestNormalizeAPIsPreservesFirstSeenOrder(t *testing.T) {
	got := normalizeAPIs([]API{Anthropic, Chat, Anthropic, Responses})
	want := []API{Anthropic, Chat, Responses}
	if !slices.Equal(got, want) {
		t.Fatalf("normalizeAPIs = %v, want %v", got, want)
	}
}

func TestModelsOpenCodeMetadata(t *testing.T) {
	srv := newModelServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":[{
			"id":"deepseek-v4.1-flash",
			"name":"DeepSeek V4.1 Flash",
			"modalities":{"input":["text","image/jpeg","image/png"],"output":["text"]},
			"limit":{"context":1000000,"input":1000000,"output":384000},
			"cost":{"text":{"input":{"price":0.15},"output":{"price":0.6}}}
		}]}`)
	})
	p := mustProvider(t, config.Provider{
		Name: "opencode-go",
		Type: "opencode-go",
		Chat: srv.URL + "/v1",
	})

	models, err := p.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	m, ok := findModel(models, "deepseek-v4.1-flash")
	if !ok {
		t.Fatalf("models = %+v, want deepseek-v4.1-flash", models)
	}
	if m.Context != 1000000 || m.Input != 1000000 || m.Output != 384000 {
		t.Fatalf("limits = context %d, input %d, output %d, want 1000000/1000000/384000", m.Context, m.Input, m.Output)
	}
	if !slices.Equal(m.Inputs, []string{"text", "image"}) {
		t.Fatalf("Inputs = %v, want [text image]", m.Inputs)
	}
	if !slices.Equal(m.Outputs, []string{"text"}) {
		t.Fatalf("Outputs = %v, want [text]", m.Outputs)
	}
	if m.Price == nil || m.Price.Input != 0.15 || m.Price.Output != 0.6 {
		t.Fatalf("Price = %+v, want input 0.15 output 0.6", m.Price)
	}
}

func TestModelsMetadataLenient(t *testing.T) {
	srv := newModelServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":[
			{"id":"string-price","cost":{"text":{"input":{"price":"0.25"},"output":{"price":"0.5"}}}},
			{"id":"invalid-price","cost":{"text":{"input":{"price":"n/a"},"output":{"price":null}}}},
			{"id":"non-finite-price","cost":{"text":{"input":{"price":"NaN"},"output":{"price":"Inf"}}}},
			{"id":"absent"},
			{"id":"free","cost":{"text":{"input":{"price":0},"output":{"price":0}}}}
		]}`)
	})
	p := mustProvider(t, config.Provider{
		Name: "p",
		Type: "generic",
		Chat: srv.URL,
	})

	models, err := p.Models(context.Background())
	if err != nil {
		t.Fatalf("Models = %v, want lenient metadata decoding", err)
	}
	if len(models) != 5 {
		t.Fatalf("models = %+v, want five", models)
	}

	stringPrice, ok := findModel(models, "string-price")
	if !ok || stringPrice.Price == nil {
		t.Fatalf("string-price = %+v, want a parsed price", stringPrice)
	}
	if stringPrice.Price.Input != 0.25 || stringPrice.Price.Output != 0.5 {
		t.Fatalf("string-price = %+v, want input 0.25 output 0.5", stringPrice.Price)
	}

	invalid, ok := findModel(models, "invalid-price")
	if !ok {
		t.Fatalf("models = %+v, want invalid-price", models)
	}
	if invalid.Price != nil {
		t.Fatalf("invalid-price = %+v, want no price for invalid values", invalid.Price)
	}

	nonFinite, ok := findModel(models, "non-finite-price")
	if !ok {
		t.Fatalf("models = %+v, want non-finite-price", models)
	}
	if nonFinite.Price != nil {
		t.Fatalf("non-finite-price = %+v, want no price for NaN/Inf", nonFinite.Price)
	}
	if _, err := json.Marshal(models); err != nil {
		t.Fatalf("json.Marshal(models) = %v, want a serializable catalog", err)
	}

	absent, ok := findModel(models, "absent")
	if !ok {
		t.Fatalf("models = %+v, want absent", models)
	}
	if absent.Context != 0 || absent.Input != 0 || absent.Output != 0 ||
		absent.Inputs != nil || absent.Outputs != nil || absent.Price != nil {
		t.Fatalf("absent = %+v, want empty metadata", absent)
	}

	free, ok := findModel(models, "free")
	if !ok || free.Price == nil {
		t.Fatalf("free = %+v, want a zero price rather than none", free)
	}
	if free.Price.Input != 0 || free.Price.Output != 0 {
		t.Fatalf("free = %+v, want zero prices", free.Price)
	}
	raw, err := json.Marshal(free.Price)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(raw), `{"input":0,"output":0}`; got != want {
		t.Fatalf("marshaled price = %s, want %s", got, want)
	}
}

func TestModelsGeminiLimits(t *testing.T) {
	srv := newModelServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"models":[
			{"name":"models/gemini-2.5-pro","displayName":"Gemini 2.5 Pro","inputTokenLimit":1048576,"outputTokenLimit":65536},
			{"name":"models/gemini-2.5-flash"}
		]}`)
	})
	p := mustProvider(t, config.Provider{
		Name:   "p",
		Type:   "generic",
		Gemini: srv.URL + "/v1beta",
	})

	models, err := p.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	pro, ok := findModel(models, "gemini-2.5-pro")
	if !ok {
		t.Fatalf("models = %+v, want gemini-2.5-pro", models)
	}
	if pro.Context != 1048576 || pro.Output != 65536 {
		t.Fatalf("gemini limits = context %d, output %d, want 1048576/65536", pro.Context, pro.Output)
	}
	if pro.Input != 0 || pro.Inputs != nil || pro.Outputs != nil || pro.Price != nil {
		t.Fatalf("gemini metadata = %+v, want only context and output", pro)
	}

	flash, ok := findModel(models, "gemini-2.5-flash")
	if !ok {
		t.Fatalf("models = %+v, want gemini-2.5-flash", models)
	}
	if flash.Context != 0 || flash.Output != 0 {
		t.Fatalf("gemini-flash limits = context %d, output %d, want 0/0", flash.Context, flash.Output)
	}
}

func TestModelsMergeMetadataAcrossAPIs(t *testing.T) {
	chatSrv := newModelServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("chat path = %q, want /v1/models", r.URL.Path)
		}
		fmt.Fprint(w, `{"data":[{
			"id":"shared-model",
			"name":"Shared Model",
			"limit":{"context":200000,"input":150000,"output":50000},
			"cost":{"text":{"input":{"price":1.5},"output":{"price":2}}}
		}]}`)
	})
	anthropicSrv := newModelServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("anthropic path = %q, want /v1/models", r.URL.Path)
		}
		fmt.Fprint(w, `{"data":[{
			"id":"shared-model",
			"modalities":{"input":["text","image/png"],"output":["text","audio/wav"]},
			"limit":{"context":1000000,"input":1000000,"output":384000}
		}]}`)
	})
	p := mustProvider(t, config.Provider{
		Name:      "p",
		Type:      "generic",
		Chat:      chatSrv.URL + "/v1",
		Anthropic: anthropicSrv.URL,
	})

	models, err := p.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 {
		t.Fatalf("models = %+v, want one merged entry", models)
	}
	m := models[0]
	if !slices.Equal(m.APIs, []API{Chat, Anthropic}) {
		t.Fatalf("APIs = %v, want [chat anthropic]", m.APIs)
	}
	if m.Name != "Shared Model" {
		t.Fatalf("Name = %q, want the first non-empty name", m.Name)
	}
	if m.Context != 200000 || m.Input != 150000 || m.Output != 50000 {
		t.Fatalf("limits = context %d, input %d, output %d, want the first API's values", m.Context, m.Input, m.Output)
	}
	if !slices.Equal(m.Inputs, []string{"text", "image"}) {
		t.Fatalf("Inputs = %v, want [text image]", m.Inputs)
	}
	if !slices.Equal(m.Outputs, []string{"text", "audio"}) {
		t.Fatalf("Outputs = %v, want [text audio]", m.Outputs)
	}
	if m.Price == nil || m.Price.Input != 1.5 || m.Price.Output != 2 {
		t.Fatalf("Price = %+v, want input 1.5 output 2", m.Price)
	}
}

func TestNormalizeModalities(t *testing.T) {
	got := normalizeModalities([]string{" Text ", "image/jpeg", "", "IMAGE/PNG", "text", "audio/"})
	want := []string{"text", "image", "audio"}
	if !slices.Equal(got, want) {
		t.Fatalf("normalizeModalities = %v, want %v", got, want)
	}
	if got := normalizeModalities([]string{"application/pdf", "text", "image/png"}); !slices.Equal(got, []string{"file", "text", "image"}) {
		t.Fatalf("normalizeModalities with application type = %v, want [file text image]", got)
	}
	if got := normalizeModalities([]string{"image / jpeg"}); !slices.Equal(got, []string{"image"}) {
		t.Fatalf("normalizeModalities with spaced MIME = %v, want [image]", got)
	}
	if got := normalizeModalities(nil); got != nil {
		t.Fatalf("normalizeModalities(nil) = %v, want nil", got)
	}
}

func TestModelsPriceMaterialization(t *testing.T) {
	tests := []struct {
		name string
		cost string
		want *Price
	}{
		{name: "input-only zero", cost: `{"text":{"input":{"price":0}}}`, want: nil},
		{name: "output-only zero", cost: `{"text":{"output":{"price":0}}}`, want: nil},
		{name: "both zero", cost: `{"text":{"input":{"price":0},"output":{"price":0}}}`, want: &Price{}},
		{name: "input-only priced", cost: `{"text":{"input":{"price":1.25}}}`, want: &Price{Input: 1.25}},
		{name: "output-only priced", cost: `{"text":{"output":{"price":2.5}}}`, want: &Price{Output: 2.5}},
		{name: "zero input with priced output", cost: `{"text":{"input":{"price":0},"output":{"price":2.5}}}`, want: &Price{Output: 2.5}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newModelServer(t, func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprintf(w, `{"data":[{"id":"priced-model","cost":%s}]}`, tt.cost)
			})
			p := mustProvider(t, config.Provider{
				Name: "p",
				Type: "generic",
				Chat: srv.URL,
			})

			models, err := p.Models(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			got := models[0].Price
			if tt.want == nil {
				if got != nil {
					t.Fatalf("Price = %+v, want no price for a single-sided zero", got)
				}
				return
			}
			if got == nil || *got != *tt.want {
				t.Fatalf("Price = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestModelsWrongShapedMetadataContainers(t *testing.T) {
	srv := newModelServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":[{
			"id":"odd-metadata",
			"modalities":"text",
			"limit":[],
			"cost":[]
		}]}`)
	})
	p := mustProvider(t, config.Provider{
		Name: "p",
		Type: "generic",
		Chat: srv.URL,
	})

	models, err := p.Models(context.Background())
	if err != nil {
		t.Fatalf("Models = %v, want wrong-shaped optional containers to be ignored", err)
	}
	if len(models) != 1 {
		t.Fatalf("models = %+v, want one", models)
	}
	m := models[0]
	if m.Context != 0 || m.Input != 0 || m.Output != 0 ||
		m.Inputs != nil || m.Outputs != nil || m.Price != nil {
		t.Fatalf("model = %+v, want no metadata from wrong-shaped containers", m)
	}
}

func TestModelsCommandCodeContextLength(t *testing.T) {
	srv := newModelServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/provider/v1/models" {
			t.Errorf("path = %q, want /provider/v1/models", r.URL.Path)
		}
		fmt.Fprint(w, `{"object":"list","data":[{
			"id":"claude-sonnet-5",
			"name":"Claude Sonnet 5",
			"context_length":1000000,
			"supported_endpoints":["/messages"]
		}]}`)
	})
	p := mustProvider(t, config.Provider{
		Name: "commandcode",
		Type: "commandcode",
		Chat: srv.URL + "/provider/v1",
	})

	models, err := p.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	m, ok := findModel(models, "claude-sonnet-5")
	if !ok {
		t.Fatalf("models = %+v, want claude-sonnet-5", models)
	}
	if m.Context != 1000000 {
		t.Fatalf("context = %d, want 1000000", m.Context)
	}
}

func TestModelsDeepSeekMetadata(t *testing.T) {
	srv := newModelServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"object":"list","data":[{
			"id":"deepseek-v4.1-flash",
			"object":"model",
			"owned_by":"deepseek",
			"name":"DeepSeek V4.1 Flash",
			"context_window":1000000,
			"max_output_tokens":384000,
			"input_modalities":["text","image"],
			"output_modalities":["text"]
		}]}`)
	})
	p := mustProvider(t, config.Provider{
		Name: "deepseek",
		Type: "deepseek",
		Chat: srv.URL + "/v1",
	})

	models, err := p.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	m, ok := findModel(models, "deepseek-v4.1-flash")
	if !ok {
		t.Fatalf("models = %+v, want deepseek-v4.1-flash", models)
	}
	if m.Context != 1000000 || m.Output != 384000 {
		t.Fatalf("limits = context %d output %d, want 1000000/384000", m.Context, m.Output)
	}
	if !slices.Equal(m.Inputs, []string{"text", "image"}) || !slices.Equal(m.Outputs, []string{"text"}) {
		t.Fatalf("modalities = %v -> %v", m.Inputs, m.Outputs)
	}
	if m.Price != nil {
		t.Fatalf("price = %+v, want nil (DeepSeek list has no price)", m.Price)
	}
}
