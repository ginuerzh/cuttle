package gateway_test

import (
	"testing"

	"github.com/ginuerzh/cuttle/internal/config"
)

// withIDs runs the identity migration config.Load performs on every config it
// loads, so a fixture that names its entities exercises the id-based build path
// with the derived ids production would use. Fixtures stay readable: only the
// migration spells out the ids.
func withIDs(t *testing.T, cfg *config.Config) *config.Config {
	t.Helper()

	if _, err := config.MigrateIdentities(cfg); err != nil {
		t.Fatalf("migrate identities: %v", err)
	}
	return cfg
}
