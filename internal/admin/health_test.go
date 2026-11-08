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
