package signing

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"slices"
	"sync"

	"github.com/meigma/mock-oidc/internal/oidc"
)

// rsaKeyBits is the modulus size for freshly generated issuer keys.
const rsaKeyBits = 2048

// Provider implements [oidc.Signer] and [oidc.KeyStore]. It is concurrency-safe:
// per-issuer key materialization and the seed deque are guarded by one mutex.
type Provider struct {
	mu   sync.Mutex
	alg  oidc.SigningAlgorithm
	seed []*rsa.PrivateKey            // remaining FIFO seed keys
	keys map[oidc.IssuerID]*issuerKey // materialized per-issuer keys
}

// issuerKey pairs an issuer's private key with its cached public metadata.
type issuerKey struct {
	priv *rsa.PrivateKey
	meta oidc.SigningKey
}

// NewProvider builds the signing adapter. alg is the configured signing
// algorithm (empty defaults to RS256); initialKeys are opaque private-RSA JWK
// JSON blobs from config, consumed before the embedded seed.
func NewProvider(alg oidc.SigningAlgorithm, initialKeys [][]byte) (*Provider, error) {
	if alg == "" {
		alg = oidc.DefaultSigningAlgorithm
	}
	if !supported(alg) {
		return nil, fmt.Errorf("signing: unsupported algorithm %q", alg)
	}
	seed, err := loadSeed(initialKeys)
	if err != nil {
		return nil, err
	}
	return &Provider{
		alg:  alg,
		seed: seed,
		keys: make(map[oidc.IssuerID]*issuerKey),
	}, nil
}

// SupportedAlgorithms reports the algorithms the adapter can produce, in
// discovery order. The constant-sync test pins this equal to
// [oidc.SupportedSigningAlgorithms] so discovery never advertises an algorithm
// the signer lacks a code path for.
func SupportedAlgorithms() []oidc.SigningAlgorithm {
	return []oidc.SigningAlgorithm{
		oidc.ES256, oidc.ES384,
		oidc.RS256, oidc.RS384, oidc.RS512, oidc.PS256, oidc.PS384, oidc.PS512,
	}
}

func supported(alg oidc.SigningAlgorithm) bool {
	return slices.Contains(SupportedAlgorithms(), alg)
}

// SigningKey returns id's public signing-key metadata, materializing the key on
// first reference.
func (p *Provider) SigningKey(_ context.Context, id oidc.IssuerID) (oidc.SigningKey, error) {
	key, err := p.keyFor(id)
	if err != nil {
		return oidc.SigningKey{}, err
	}
	return key.meta, nil
}

// PublicKeys returns id's JWK set (its single key), forcing materialization so
// the set is never empty.
func (p *Provider) PublicKeys(_ context.Context, id oidc.IssuerID) (oidc.JWKS, error) {
	key, err := p.keyFor(id)
	if err != nil {
		return oidc.JWKS{}, err
	}
	return oidc.JWKS{Keys: []oidc.JWK{key.meta.Public}}, nil
}

// Sign serializes and signs tok for issuer id, returning the compact JWS.
func (p *Provider) Sign(_ context.Context, id oidc.IssuerID, tok oidc.Token) (oidc.SignedToken, error) {
	key, err := p.keyFor(id)
	if err != nil {
		return "", err
	}
	return sign(key.priv, tok)
}

// keyFor returns id's materialized key, generating (or drawing from the seed) on
// first reference under the mutex.
func (p *Provider) keyFor(id oidc.IssuerID) (*issuerKey, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if key, ok := p.keys[id]; ok {
		return key, nil
	}
	priv, err := p.nextKey()
	if err != nil {
		return nil, err
	}
	key := &issuerKey{priv: priv, meta: signingKeyMeta(id, p.alg, &priv.PublicKey)}
	p.keys[id] = key
	return key, nil
}

// nextKey pops the head of the seed deque, or generates a fresh RSA key once the
// seed is exhausted.
func (p *Provider) nextKey() (*rsa.PrivateKey, error) {
	if len(p.seed) > 0 {
		key := p.seed[0]
		p.seed = p.seed[1:]
		return key, nil
	}
	key, err := rsa.GenerateKey(rand.Reader, rsaKeyBits)
	if err != nil {
		return nil, fmt.Errorf("signing: generate rsa key: %w", err)
	}
	return key, nil
}

// signingKeyMeta builds the public metadata (kid == issuer id, use=sig) for a
// key. No private material is copied.
func signingKeyMeta(id oidc.IssuerID, alg oidc.SigningAlgorithm, pub *rsa.PublicKey) oidc.SigningKey {
	kid := id.KeyID()
	return oidc.SigningKey{
		KeyID:     kid,
		Algorithm: alg,
		Public: oidc.JWK{
			KeyID:     kid,
			Algorithm: alg,
			KeyType:   oidc.KeyTypeRSA,
			Use:       "sig",
			Params: oidc.RSAPublicParams{
				N: base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
				E: base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
			},
		},
	}
}

// sign builds the compact JWS from the token's header and claims.
func sign(priv *rsa.PrivateKey, tok oidc.Token) (oidc.SignedToken, error) {
	headerJSON, err := json.Marshal(map[string]string{
		"alg": string(tok.Header.Algorithm),
		"typ": string(tok.Header.Type),
		"kid": string(tok.Header.KeyID),
	})
	if err != nil {
		return "", fmt.Errorf("signing: marshal header: %w", err)
	}
	payloadJSON, err := marshalClaims(tok.Claims)
	if err != nil {
		return "", err
	}
	signingInput := encodeSegment(headerJSON) + "." + encodeSegment(payloadJSON)
	sig, err := signBytes(priv, tok.Header.Algorithm, []byte(signingInput))
	if err != nil {
		return "", err
	}
	return oidc.SignedToken(signingInput + "." + encodeSegment(sig)), nil
}

// marshalClaims renders a ClaimSet as the JWT payload. This is the adapter edge,
// so it may use a map — the ordered emission the domain forbids inward.
func marshalClaims(c oidc.ClaimSet) ([]byte, error) {
	m := make(map[string]any)
	if c.Issuer != "" {
		m["iss"] = c.Issuer
	}
	if c.Subject != "" {
		m["sub"] = string(c.Subject)
	}
	if c.Audience != nil {
		m["aud"] = []string(c.Audience)
	}
	m["iat"] = c.IssuedAt.Unix()
	m["nbf"] = c.NotBefore.Unix()
	m["exp"] = c.Expiry.Unix()
	if c.JWTID != "" {
		m["jti"] = c.JWTID
	}
	if c.Nonce != nil {
		m["nonce"] = string(*c.Nonce)
	}
	if c.Azp != nil {
		m["azp"] = string(*c.Azp)
	}
	if c.Tenant != nil {
		m["tid"] = *c.Tenant
	}
	if len(c.Scope) > 0 {
		m["scope"] = c.Scope.String()
	}
	for _, e := range c.Custom.Entries() {
		m[e.Name] = e.Value
	}
	out, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("signing: marshal claims: %w", err)
	}
	return out, nil
}

// signBytes signs the JWS signing input for alg. RSA-family algorithms use the
// provided RSA key; EC-family algorithms need an EC key not provisioned in this
// slice.
func signBytes(priv *rsa.PrivateKey, alg oidc.SigningAlgorithm, input []byte) ([]byte, error) {
	switch alg {
	case oidc.RS256:
		return rsaPKCS1(priv, crypto.SHA256, sha256sum(input))
	case oidc.RS384:
		return rsaPKCS1(priv, crypto.SHA384, sha384sum(input))
	case oidc.RS512:
		return rsaPKCS1(priv, crypto.SHA512, sha512sum(input))
	case oidc.PS256:
		return rsaPSS(priv, crypto.SHA256, sha256sum(input))
	case oidc.PS384:
		return rsaPSS(priv, crypto.SHA384, sha384sum(input))
	case oidc.PS512:
		return rsaPSS(priv, crypto.SHA512, sha512sum(input))
	case oidc.ES256, oidc.ES384:
		return nil, fmt.Errorf("signing: %s requires an EC key not provisioned in this slice", alg)
	default:
		return nil, fmt.Errorf("signing: unsupported algorithm %q", alg)
	}
}

func rsaPKCS1(priv *rsa.PrivateKey, h crypto.Hash, digest []byte) ([]byte, error) {
	sig, err := rsa.SignPKCS1v15(rand.Reader, priv, h, digest)
	if err != nil {
		return nil, fmt.Errorf("signing: rsa pkcs1v15: %w", err)
	}
	return sig, nil
}

func rsaPSS(priv *rsa.PrivateKey, h crypto.Hash, digest []byte) ([]byte, error) {
	sig, err := rsa.SignPSS(rand.Reader, priv, h, digest, &rsa.PSSOptions{
		SaltLength: rsa.PSSSaltLengthEqualsHash,
		Hash:       h,
	})
	if err != nil {
		return nil, fmt.Errorf("signing: rsa pss: %w", err)
	}
	return sig, nil
}

func sha256sum(b []byte) []byte { s := sha256.Sum256(b); return s[:] }
func sha384sum(b []byte) []byte { s := sha512.Sum384(b); return s[:] }
func sha512sum(b []byte) []byte { s := sha512.Sum512(b); return s[:] }

func encodeSegment(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }
