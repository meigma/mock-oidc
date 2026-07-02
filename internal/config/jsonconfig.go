package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/spf13/viper"

	"github.com/meigma/mock-oidc/internal/oidc"
)

// defaultConfigFilePath is the implicit JSON config source consulted when
// neither JSON_CONFIG nor JSON_CONFIG_PATH is set. A missing file here is not an
// error (it is the zero-config path); a missing JSON_CONFIG_PATH file IS an error
// (the operator named it explicitly).
const defaultConfigFilePath = "config.json"

// Seed is the strongly-typed OIDC behavior parsed from the JSON config document
// (or built-in defaults). Every field is a closed domain type or a primitive
// flag — never map[string]any. The composition root distributes it: SystemTime
// freezes the Clock; Algorithm and InitialKeys drive the signing adapter.
//
// This slice parses the first three JSON-config fields (systemTime, algorithm,
// initialKeys). The remaining upstream OAuth2Config fields (token callbacks,
// login page, TLS) are decoded in later slices.
type Seed struct {
	// SystemTime, when present, freezes the clock at a fixed instant so iat/nbf/
	// exp (and later, verification) are deterministic. Absent → the live,
	// runtime-advanceable clock. SystemTimeFixed reports whether it was set.
	SystemTime      oidc.Instant
	SystemTimeFixed bool

	// Algorithm is the issuer signing algorithm; it defaults to RS256.
	Algorithm oidc.SigningAlgorithm

	// InitialKeys carries opaque private-RSA JWK JSON blobs from config. They are
	// NOT parsed here: crypto lives only in the signing adapter, which parses and
	// validates them (and the algorithm) at construction. Empty → the embedded
	// 5-key RSA seed. An ARRAY is accepted, dropping upstream's single-key JSON
	// limitation.
	InitialKeys [][]byte

	// InteractiveLogin forces GET /authorize to render the interactive login page
	// even when the request does not ask for it (via prompt). Absent → false: the
	// zero-config default auto-issues a code so a stock client's authorize→code→
	// token flow works without a browser, matching upstream.
	InteractiveLogin bool
}

// DefaultSeed is the zero-config seed used when no JSON config is present.
func DefaultSeed() Seed {
	return Seed{Algorithm: oidc.DefaultSigningAlgorithm}
}

// document is the JSON config wire shape (upstream OAuth2Config). Unknown keys
// are ignored (lenient parity); only the fields mock-oidc honors this slice are
// declared.
type document struct {
	TokenProvider    tokenProviderDoc `json:"tokenProvider"`
	InteractiveLogin bool             `json:"interactiveLogin"`
}

type tokenProviderDoc struct {
	SystemTime  string         `json:"systemTime"` // RFC3339; "" → live clock
	KeyProvider keyProviderDoc `json:"keyProvider"`
}

type keyProviderDoc struct {
	Algorithm   string            `json:"algorithm"`   // "" → RS256
	InitialKeys []json.RawMessage `json:"initialKeys"` // array of JWK objects
}

// LoadSeed resolves the JSON config source and parses it into a typed Seed. No
// source present → DefaultSeed(). Malformed JSON or any value the domain rejects
// (bad algorithm, unparseable systemTime) is a hard error.
//
// Source precedence (highest first): JSON_CONFIG (inline) > JSON_CONFIG_PATH
// (file) > ./config.json > built-in defaults.
func LoadSeed(vp *viper.Viper) (Seed, error) {
	raw, ok, err := resolveJSONSource(vp)
	if err != nil {
		return Seed{}, err
	}
	if !ok {
		return DefaultSeed(), nil
	}

	var doc document
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&doc); err != nil {
		return Seed{}, fmt.Errorf("parse JSON config: %w", err)
	}
	return doc.toSeed()
}

// resolveJSONSource implements the source precedence: JSON_CONFIG (inline) >
// JSON_CONFIG_PATH (file) > ./config.json > none. A missing JSON_CONFIG_PATH file
// is an error (it was named explicitly); a missing ./config.json is not (it is
// the implicit zero-config fallback).
func resolveJSONSource(vp *viper.Viper) ([]byte, bool, error) {
	if inline := strings.TrimSpace(vp.GetString("json-config")); inline != "" {
		return []byte(inline), true, nil
	}
	if path := strings.TrimSpace(vp.GetString("json-config-path")); path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, false, fmt.Errorf("read json-config-path %q: %w", path, err)
		}
		return b, true, nil
	}
	b, err := os.ReadFile(defaultConfigFilePath)
	switch {
	case err == nil:
		return b, true, nil
	case errors.Is(err, fs.ErrNotExist):
		return nil, false, nil
	default:
		return nil, false, fmt.Errorf("read %s: %w", defaultConfigFilePath, err)
	}
}

// toSeed maps the wire document to the typed Seed at the config edge, so the
// domain only ever sees oidc.* values (parse-don't-validate).
func (d document) toSeed() (Seed, error) {
	seed := DefaultSeed()

	if t := strings.TrimSpace(d.TokenProvider.SystemTime); t != "" {
		inst, err := oidc.ParseInstant(t)
		if err != nil {
			return Seed{}, fmt.Errorf("tokenProvider.systemTime: %w", err)
		}
		seed.SystemTime, seed.SystemTimeFixed = inst, true
	}

	if a := strings.TrimSpace(d.TokenProvider.KeyProvider.Algorithm); a != "" {
		alg, err := oidc.ParseSigningAlgorithm(a)
		if err != nil {
			return Seed{}, fmt.Errorf("tokenProvider.keyProvider.algorithm: %w", err)
		}
		seed.Algorithm = alg
	}

	for _, k := range d.TokenProvider.KeyProvider.InitialKeys {
		seed.InitialKeys = append(seed.InitialKeys, []byte(k)) // opaque; parsed by the signing adapter
	}

	seed.InteractiveLogin = d.InteractiveLogin

	return seed, nil
}
