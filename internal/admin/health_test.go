package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ginuerzh/cuttle/internal/version"
)

func TestHealthReportsBuildIdentity(t *testing.T) {
	oldV, oldC, oldB := version.Version, version.Commit, version.BuildDate
	t.Cleanup(func() { version.Version, version.Commit, version.BuildDate = oldV, oldC, oldB })
	version.Version = "v9.9.9-test"
	version.Commit = "deadbeefcafe"
	version.BuildDate = "2026-11-06T10:00:00Z"

	rec := httptest.NewRecorder()
	(&Server{}).handleHealth(rec, httptest.NewRequest(http.MethodGet, "/api/health", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var got struct {
		OK      bool   `json:"ok"`
		Version string `json:"version"`
		Commit  string `json:"commit"`
		BuiltAt string `json:"builtAt"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.OK || got.Version != "v9.9.9-test" || got.Commit != "deadbeefcafe" || got.BuiltAt != "2026-11-06T10:00:00Z" {
		t.Fatalf("health = %+v", got)
	}
}

// TestHealthReportsStatsMigrationError keeps the health response additive: the
// field is absent when the one-time stats rewrite is fine and carries the
// failure when it is not.
func TestHealthReportsStatsMigrationError(t *testing.T) {
	decode := func(t *testing.T, s *Server) map[string]any {
		t.Helper()
		rec := httptest.NewRecorder()
		s.handleHealth(rec, httptest.NewRequest(http.MethodGet, "/api/health", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d", rec.Code)
		}
		var got map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		return got
	}

	fine := decode(t, &Server{})
	if _, ok := fine["statsMigrationError"]; ok {
		t.Fatalf("health = %+v, want no statsMigrationError when the migration is clean", fine)
	}
	if fine["ok"] != true {
		t.Fatalf("health = %+v, want ok", fine)
	}

	failed := decode(t, &Server{migrationError: func() string {
		return "stats: back up /data/stats.sqlite: disk full"
	}})
	if got, _ := failed["statsMigrationError"].(string); got != "stats: back up /data/stats.sqlite: disk full" {
		t.Fatalf("statsMigrationError = %v, want the migration failure", failed["statsMigrationError"])
	}
}
