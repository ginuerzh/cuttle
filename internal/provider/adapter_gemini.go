package provider

import (
	"strings"

	"github.com/ginuerzh/cuttle/internal/config"
)

// geminiAdapter covers the Google AI Studio / Gemini API key surface.
// Static classification advertises the OpenAI-compatible Chat endpoint and the
// native Gemini endpoint. The compatible endpoint is the target for clients
// that cannot speak Gemini SSE; the native endpoint serves Gemini clients.
type geminiAdapter struct{}

func (geminiAdapter) classify(id string) []API {
	if strings.TrimSpace(id) == "" {
		return nil
	}
	return []API{Chat, Gemini}
}

func (geminiAdapter) auth(api API) config.Auth { return defaultAuth(api) }

func (geminiAdapter) listAll() bool { return false }

func (geminiAdapter) keylessCatalog() bool { return false }

func (geminiAdapter) catalogAPIs() []API { return []API{Gemini} }
