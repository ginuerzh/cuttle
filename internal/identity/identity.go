// Package identity mints and validates the immutable ids that name cuttle
// entities. An id carries the entity kind in its prefix and a random or
// derived suffix; the entity's name is only a display label.
package identity

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"fmt"
	"regexp"
	"strings"
)

// Kind is the entity kind an id belongs to. The value is the kind string shared
// with the config, the stats store and the admin API.
type Kind string

// Entity kinds.
const (
	Provider    Kind = "provider"
	ProviderKey Kind = "provider_key"
	Node        Kind = "node"
	Route       Kind = "route"
	Entrypoint  Kind = "entrypoint"
	GatewayKey  Kind = "gateway_key"
)

// prefixes maps a kind to its id prefix.
var prefixes = map[Kind]string{
	Provider:    "p",
	ProviderKey: "pk",
	Node:        "n",
	Route:       "r",
	Entrypoint:  "e",
	GatewayKey:  "gk",
}

// encoding is unpadded base32, lowercased by the callers: six bytes encode to
// exactly ten characters.
var encoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// shape matches <prefix>_<10 lowercase base32 characters>.
var shape = regexp.MustCompile(`^[a-z]{1,2}_[0-9a-z]{10}$`)

// New returns a new random id for the kind.
func New(k Kind) (string, error) {
	prefix, ok := prefixes[k]
	if !ok {
		return "", fmt.Errorf("identity: unknown kind %q", k)
	}
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return prefix + "_" + suffix(b), nil
}

// Derive returns the id for seed: sha256(seed) truncated to six bytes, so the
// same seed and kind always produce the same id. The kind only selects the
// prefix and does not salt the hash.
func Derive(k Kind, seed string) string {
	sum := sha256.Sum256([]byte(seed))
	return prefixes[k] + "_" + suffix(sum[:6])
}

// Valid reports whether id has the id shape and the kind's prefix.
func Valid(k Kind, id string) bool {
	prefix, ok := prefixes[k]
	if !ok {
		return false
	}
	return Shape(id) && strings.HasPrefix(id, prefix+"_")
}

// Shape reports whether id has the generic id shape, ignoring its kind.
func Shape(id string) bool {
	return shape.MatchString(id)
}

func suffix(b []byte) string {
	return strings.ToLower(encoding.EncodeToString(b))
}
