package signing

import (
	"crypto/rsa"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
)

// seedKeysJSON is the committed 5-key RSA-2048 JWKS seed. It is consumed FIFO,
// one key per new issuer, so issuer keys are stable across process restarts
// (deterministic kid == issuer id keying). Generated once and committed; never
// regenerated at build time.
//
//go:embed seed_keys.json
var seedKeysJSON []byte

// privateJWK is the private RSA JWK shape shared by the embedded seed and the
// config `initialKeys` blobs. Only the parameters needed to reconstruct an
// [rsa.PrivateKey] are read; the CRT values are recomputed via Precompute.
type privateJWK struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	N   string `json:"n"`
	E   string `json:"e"`
	D   string `json:"d"`
	P   string `json:"p"`
	Q   string `json:"q"`
}

// jwkSet is the seed file envelope.
type jwkSet struct {
	Keys []privateJWK `json:"keys"`
}

// seedKeyCount is the number of keys in the embedded seed (a capacity hint).
const seedKeyCount = 5

// rsaKey reconstructs the private RSA key from the JWK's n/e/d/p/q parameters.
func (j privateJWK) rsaKey() (*rsa.PrivateKey, error) {
	if j.Kty != "" && j.Kty != "RSA" {
		return nil, fmt.Errorf("signing: unsupported seed key type %q (only RSA is supported)", j.Kty)
	}
	n, err := decodeBigInt(j.N)
	if err != nil {
		return nil, fmt.Errorf("signing: seed key n: %w", err)
	}
	e, err := decodeBigInt(j.E)
	if err != nil {
		return nil, fmt.Errorf("signing: seed key e: %w", err)
	}
	d, err := decodeBigInt(j.D)
	if err != nil {
		return nil, fmt.Errorf("signing: seed key d: %w", err)
	}
	p, err := decodeBigInt(j.P)
	if err != nil {
		return nil, fmt.Errorf("signing: seed key p: %w", err)
	}
	q, err := decodeBigInt(j.Q)
	if err != nil {
		return nil, fmt.Errorf("signing: seed key q: %w", err)
	}
	key := &rsa.PrivateKey{
		PublicKey: rsa.PublicKey{N: n, E: int(e.Int64())},
		D:         d,
		Primes:    []*big.Int{p, q},
	}
	key.Precompute()
	if err := key.Validate(); err != nil {
		return nil, fmt.Errorf("signing: invalid seed key: %w", err)
	}
	return key, nil
}

// decodeBigInt decodes a base64url (no padding) JWK integer parameter.
func decodeBigInt(s string) (*big.Int, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("base64url: %w", err)
	}
	return new(big.Int).SetBytes(raw), nil
}

// loadSeed builds the FIFO key deque: configured initialKeys first (consumed
// before the embedded seed so config-provided keys win), then the embedded
// 5-key seed. Each entry is an opaque private-RSA JWK JSON object.
func loadSeed(initialKeys [][]byte) ([]*rsa.PrivateKey, error) {
	out := make([]*rsa.PrivateKey, 0, len(initialKeys)+seedKeyCount)
	for i, blob := range initialKeys {
		var j privateJWK
		if err := json.Unmarshal(blob, &j); err != nil {
			return nil, fmt.Errorf("signing: initialKeys[%d]: %w", i, err)
		}
		key, err := j.rsaKey()
		if err != nil {
			return nil, fmt.Errorf("signing: initialKeys[%d]: %w", i, err)
		}
		out = append(out, key)
	}
	var set jwkSet
	if err := json.Unmarshal(seedKeysJSON, &set); err != nil {
		return nil, fmt.Errorf("signing: parse embedded seed: %w", err)
	}
	for i, j := range set.Keys {
		key, err := j.rsaKey()
		if err != nil {
			return nil, fmt.Errorf("signing: embedded seed key %d: %w", i, err)
		}
		out = append(out, key)
	}
	return out, nil
}
