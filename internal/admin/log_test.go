package admin

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/go-gost/core/logger"
	xlogger "github.com/go-gost/x/logger"

	"github.com/ginuerzh/cuttle/internal/config"
)

type trackingLogger struct {
	logger.Logger
	level  string
	format string
}

func (l *trackingLogger) SetLevel(level string) error { l.level = level; return nil }
func (l *trackingLogger) Format() string              { return l.format }

func TestPutConfigAppliesLogLevel(t *testing.T) {
	srv, _, _, ts := newTestAdmin(t)
	client, csrf := login(t, ts.URL)
	srv.log = &trackingLogger{Logger: xlogger.Nop(), level: "info", format: "json"}

	body := `{"gateway":{"listen":"127.0.0.1:0"},"log":{"level":"debug","format":"json"}}`
	resp := doJSON(t, client, http.MethodPut, ts.URL+"/api/config", body, csrf)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if got := srv.log.(*trackingLogger).level; got != "debug" {
		t.Fatalf("level = %q, want debug", got)
	}

	resp = doJSON(t, client, http.MethodGet, ts.URL+"/api/config", "", "")
	var got struct {
		Log config.Log `json:"log"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Log.Level != "debug" || got.Log.Format != "json" {
		t.Fatalf("config log = %+v", got.Log)
	}
}

func TestSetConfigKeepsExternalLogLevel(t *testing.T) {
	srv, _, _, ts := newTestAdmin(t)
	client, csrf := login(t, ts.URL)
	log := &trackingLogger{Logger: xlogger.Nop(), level: "info", format: "json"}
	srv.log = log

	next := *srv.cfg
	next.Log = config.Log{Level: "debug", Format: "json"}
	srv.SetConfig(&next)
	log.level = "debug" // main applies the level after SIGHUP

	resp := doJSON(t, client, http.MethodPut, ts.URL+"/api/entrypoints", `[]`, csrf)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("put entrypoints status = %d", resp.StatusCode)
	}
	if log.level != "debug" {
		t.Fatalf("level after admin save = %q, want debug", log.level)
	}

	resp = doJSON(t, client, http.MethodGet, ts.URL+"/api/config", "", "")
	var got struct {
		Log config.Log `json:"log"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Log.Level != "debug" {
		t.Fatalf("config log = %+v", got.Log)
	}
}
