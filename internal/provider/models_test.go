package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/go-gost/llm-gw/internal/config"
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
			Keys: []string{"sk-test"},
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
			Keys:      []string{"sk-test"},
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
		Keys:   []string{"sk-test"},
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
		Keys: []string{"sk-test"},
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

func TestNormalizeAPIsPreservesFirstSeenOrder(t *testing.T) {
	got := normalizeAPIs([]API{Anthropic, Chat, Anthropic, Responses})
	want := []API{Anthropic, Chat, Responses}
	if !slices.Equal(got, want) {
		t.Fatalf("normalizeAPIs = %v, want %v", got, want)
	}
}
