package identity_test

import (
	"strings"
	"testing"

	"github.com/ginuerzh/cuttle/internal/identity"
)

func TestNewShapeAndPrefix(t *testing.T) {
	id, err := identity.New(identity.Provider)
	if err != nil {
		t.Fatal(err)
	}
	if !identity.Valid(identity.Provider, id) {
		t.Fatalf("invalid id %q", id)
	}
	if strings.HasPrefix(id, "pk_") {
		t.Fatalf("wrong prefix %q", id)
	}
}

func TestNewUsesKindPrefixes(t *testing.T) {
	for kind, prefix := range map[identity.Kind]string{
		identity.Provider:    "p_",
		identity.ProviderKey: "pk_",
		identity.Node:        "n_",
		identity.Route:       "r_",
		identity.Entrypoint:  "e_",
		identity.GatewayKey:  "gk_",
	} {
		id, err := identity.New(kind)
		if err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		if !strings.HasPrefix(id, prefix) {
			t.Fatalf("%s: id %q does not start with %q", kind, id, prefix)
		}
		if !identity.Valid(kind, id) {
			t.Fatalf("%s: invalid id %q", kind, id)
		}
	}
}

func TestNewIsRandom(t *testing.T) {
	a, err := identity.New(identity.Node)
	if err != nil {
		t.Fatal(err)
	}
	b, err := identity.New(identity.Node)
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatalf("two generated ids are equal: %q", a)
	}
}

func TestNewUnknownKind(t *testing.T) {
	if _, err := identity.New(identity.Kind("bogus")); err == nil {
		t.Fatal("unknown kind should not mint an id")
	}
}

func TestDeriveStableAndKindScoped(t *testing.T) {
	a := identity.Derive(identity.Provider, "DeepSeek 官方")
	b := identity.Derive(identity.Provider, "DeepSeek 官方")
	if a != b {
		t.Fatal("derive is not deterministic")
	}
	if a == identity.Derive(identity.Node, "DeepSeek 官方") {
		t.Fatal("prefix does not scope derivation")
	}
	if !identity.Valid(identity.Provider, a) {
		t.Fatalf("invalid derived id %q", a)
	}
	if a == identity.Derive(identity.Provider, "deepseek 官方") {
		t.Fatal("derivation ignores the seed")
	}
}

func TestValidRejectsWrongPrefixAndShape(t *testing.T) {
	for _, id := range []string{"", "p_", "p_short", "x_3k9x2m4q7a", "p_3K9X2M4Q7A", "p_3k9x2m4q7a_"} {
		if identity.Valid(identity.Provider, id) {
			t.Fatalf("accepted %q", id)
		}
	}
}

func TestShape(t *testing.T) {
	for _, id := range []string{"p_3k9x2m4q7a", "gk_9x2m4q7a3k", "pk_8m2xq7a4k9", "x_3k9x2m4q7a"} {
		if !identity.Shape(id) {
			t.Fatalf("rejected valid shape %q", id)
		}
	}
	for _, id := range []string{"", "_3k9x2m4q7a", "abc_3k9x2m4q7a", "p_3k9x2m4q7", "p_3k9x2m4q7a0"} {
		if identity.Shape(id) {
			t.Fatalf("accepted invalid shape %q", id)
		}
	}
}
