package admin

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ginuerzh/cuttle/internal/config"
	"github.com/ginuerzh/cuttle/internal/health"
)

// entrypointHealthPayload is the wire shape the console consumes. It is
// declared apart from health.Entrypoint on purpose, so the test pins the JSON
// names and not the Go field names.
type entrypointHealthPayload struct {
	ID     string `json:"entrypointId"`
	Name   string `json:"entrypointName"`
	Active struct {
		RouteID  string `json:"routeId"`
		Fallback bool   `json:"fallback"`
	} `json:"active"`
	Bindings []struct {
		Index     int    `json:"index"`
		RouteID   string `json:"routeId"`
		RouteName string `json:"routeName"`
		Eligible  bool   `json:"eligible"`
		Last      *struct {
			At        time.Time `json:"at"`
			OK        bool      `json:"ok"`
			ExitCode  int       `json:"exitCode"`
			LatencyMS int64     `json:"latencyMs"`
			Error     string    `json:"error"`
		} `json:"last"`
	} `json:"bindings"`
}

func TestEntrypointHealthEndpointRequiresAuth(t *testing.T) {
	_, _, _, ts := newTestAdmin(t)

	resp := doJSON(t, http.DefaultClient, http.MethodGet, ts.URL+"/api/entrypoints/health", "", "")
	if resp.StatusCode != http.StatusUnauthorized {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d body = %s, want 401", resp.StatusCode, b)
	}
}

func TestEntrypointHealthShape(t *testing.T) {
	_, rel, _, ts := newTestAdmin(t)
	probedAt := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	rel.entrypoints = []health.Entrypoint{{
		ID:     "e_0000000001",
		Name:   "fast",
		Active: health.ActiveBinding{RouteID: "r_0000000001", Fallback: true},
		Bindings: []health.Binding{
			{
				Index:     0,
				RouteID:   "r_0000000001",
				RouteName: "primary",
				Eligible:  false,
				Last: &health.Probe{
					At:        probedAt,
					OK:        false,
					ExitCode:  3,
					LatencyMS: 12,
					Error:     "probe: dial refused",
				},
			},
			{Index: 1, RouteID: "r_0000000002", RouteName: "backup", Eligible: true},
		},
	}}
	client, _ := login(t, ts.URL)

	resp := doJSON(t, client, http.MethodGet, ts.URL+"/api/entrypoints/health", "", "")
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d body = %s, want 200", resp.StatusCode, b)
	}
	var got []entrypointHealthPayload
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("entrypoints = %+v, want 1", got)
	}
	ep := got[0]
	if ep.ID != "e_0000000001" || ep.Name != "fast" {
		t.Fatalf("entrypoint identity = %+v", ep)
	}
	if ep.Active.RouteID != "r_0000000001" || !ep.Active.Fallback {
		t.Fatalf("active = %+v, want first binding as fallback", ep.Active)
	}
	if len(ep.Bindings) != 2 {
		t.Fatalf("bindings = %+v, want 2", ep.Bindings)
	}
	first, second := ep.Bindings[0], ep.Bindings[1]
	if first.Index != 0 || first.RouteID != "r_0000000001" || first.RouteName != "primary" {
		t.Fatalf("first binding = %+v", first)
	}
	if first.Eligible {
		t.Fatalf("first binding eligible = true, want false")
	}
	if first.Last == nil {
		t.Fatalf("first binding has no probe result")
	}
	if first.Last.ExitCode != 3 || first.Last.OK || first.Last.LatencyMS != 12 ||
		first.Last.Error != "probe: dial refused" || !first.Last.At.Equal(probedAt) {
		t.Fatalf("first binding last probe = %+v", first.Last)
	}
	if second.Index != 1 || second.RouteID != "r_0000000002" || second.RouteName != "backup" {
		t.Fatalf("second binding = %+v, want preserved configuration order", second)
	}
	if !second.Eligible || second.Last != nil {
		t.Fatalf("second binding = %+v", second)
	}
}

func TestEntrypointHealthDraftEntrypoint(t *testing.T) {
	_, rel, _, ts := newTestAdmin(t)
	// A draft entrypoint has no bindings and no probe runner, so the data plane
	// reports nothing for it.
	rel.entrypoints = nil
	client, _ := login(t, ts.URL)

	resp := doJSON(t, client, http.MethodGet, ts.URL+"/api/entrypoints/health", "", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(body)); got != "[]" {
		t.Fatalf("body = %s, want []", got)
	}
}

// TestEntrypointWhenRoundTrip pins the config path: `when` survives PUT then
// GET, and a second PUT that omits it makes every binding unconditional.
func TestEntrypointWhenRoundTrip(t *testing.T) {
	base, client, csrf := setupRouteFixtures(t)
	fastID := routeID(t, client, base, "fast")
	defaultID := routeID(t, client, base, "default")

	body := `[{"name":"claude","match":["*"],"routes":[{"route":"` + fastID + `","when":"subagent"},{"route":"` + defaultID + `"}]}]`
	if resp := doJSON(t, client, http.MethodPut, base+"/api/entrypoints", body, csrf); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("put entrypoints = %d", resp.StatusCode)
	}

	list := doJSON(t, client, http.MethodGet, base+"/api/entrypoints", "", "")
	var got []config.Entrypoint
	if err := jsonDecode(list.Body, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || len(got[0].Routes) != 2 {
		t.Fatalf("entrypoints = %+v, want one entry with two bindings", got)
	}
	if got[0].Routes[0].When != config.BindingWhenSubagent {
		t.Fatalf("routes[0].When = %q, want %q", got[0].Routes[0].When, config.BindingWhenSubagent)
	}
	if got[0].Routes[1].When != "" {
		t.Fatalf("routes[1].When = %q, want empty", got[0].Routes[1].When)
	}

	plain := `[{"name":"claude","match":["*"],"routes":[{"route":"` + fastID + `"},{"route":"` + defaultID + `"}]}]`
	if resp := doJSON(t, client, http.MethodPut, base+"/api/entrypoints", plain, csrf); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("put entrypoints (plain) = %d", resp.StatusCode)
	}
	list = doJSON(t, client, http.MethodGet, base+"/api/entrypoints", "", "")
	got = nil
	if err := jsonDecode(list.Body, &got); err != nil {
		t.Fatal(err)
	}
	for i, b := range got[0].Routes {
		if b.When != "" {
			t.Fatalf("routes[%d].When = %q after an omitting PUT, want empty", i, b.When)
		}
	}
}

// TestEntrypointWhenJSONKey pins the wire name: the console reads `when`, so a
// struct tag regression to `When` would silently break the binding editor.
func TestEntrypointWhenJSONKey(t *testing.T) {
	base, client, csrf := setupRouteFixtures(t)
	fastID := routeID(t, client, base, "fast")
	defaultID := routeID(t, client, base, "default")

	body := `[{"name":"claude","match":["*"],"routes":[{"route":"` + fastID + `","when":"subagent"},{"route":"` + defaultID + `"}]}]`
	if resp := doJSON(t, client, http.MethodPut, base+"/api/entrypoints", body, csrf); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("put entrypoints = %d", resp.StatusCode)
	}

	list := doJSON(t, client, http.MethodGet, base+"/api/entrypoints", "", "")
	raw, err := io.ReadAll(list.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"when":"subagent"`) {
		t.Fatalf("payload = %s, want a lowercase when key", raw)
	}
}
