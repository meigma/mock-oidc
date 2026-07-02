package signing

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
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
	seed []*rsa.PrivateKey            // remaining FIFO seed keys (RSA families only)
	keys map[oidc.IssuerID]*issuerKey // materialized per-issuer keys
}

// issuerKey pairs an issuer's private key with its cached public metadata.
type issuerKey struct {
	priv keyMaterial
	meta oidc.SigningKey
}

// keyMaterial is a materialized private signing key of a specific family. It can
// sign the JWS signing input for the configured algorithm and expose its public
// JWK parameters. Two implementations exist — [rsaKey] and [ecKey] — so the
// adapter can actually produce every algorithm discovery advertises.
type keyMaterial interface {
	sign(alg oidc.SigningAlgorithm, input []byte) ([]byte, error)
	publicParams() (oidc.KeyType, oidc.PublicParams, error)
}

// NewProvider builds the signing adapter. alg is the configured signing
// algorithm (empty defaults to RS256); initialKeys are opaque private-RSA JWK
// JSON blobs from config, consumed before the embedded seed. The seed is an
// RSA-only optimization; EC algorithms always generate fresh keys.
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

// SupportedAlgorithms reports the algorithms the adapter can actually produce, in
// discovery order. The constant-sync test pins this equal to
// [oidc.SupportedSigningAlgorithms] AND signs a probe token per algorithm, so
// discovery never advertises an algorithm the signer lacks a code path for.
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
	meta, err := signingKeyMeta(id, p.alg, priv)
	if err != nil {
		return nil, err
	}
	key := &issuerKey{priv: priv, meta: meta}
	p.keys[id] = key
	return key, nil
}

// nextKey materializes the next private key for the configured algorithm. EC
// algorithms always generate a fresh key of the matching curve; RSA algorithms
// pop the head of the seed deque, or generate a fresh RSA key once the seed is
// exhausted.
func (p *Provider) nextKey() (keyMaterial, error) {
	if curve, ok := ecCurve(p.alg); ok {
		priv, err := ecdsa.GenerateKey(curve, rand.Reader)
		if err != nil {
			return nil, fmt.Errorf("signing: generate ec key: %w", err)
		}
		return ecKey{priv: priv}, nil
	}
	if len(p.seed) > 0 {
		key := p.seed[0]
		p.seed = p.seed[1:]
		return rsaKey{priv: key}, nil
	}
	key, err := rsa.GenerateKey(rand.Reader, rsaKeyBits)
	if err != nil {
		return nil, fmt.Errorf("signing: generate rsa key: %w", err)
	}
	return rsaKey{priv: key}, nil
}

// ecCurve reports the elliptic curve for an EC signing algorithm, and false for
// the RSA families.
//
//nolint:exhaustive // only the ES* family maps to a curve; RS*/PS* return false.
func ecCurve(alg oidc.SigningAlgorithm) (elliptic.Curve, bool) {
	switch alg {
	case oidc.ES256:
		return elliptic.P256(), true
	case oidc.ES384:
		return elliptic.P384(), true
	default:
		return nil, false
	}
}

// signingKeyMeta builds the public metadata (kid == issuer id, use=sig) for a
// key. No private material is copied.
func signingKeyMeta(id oidc.IssuerID, alg oidc.SigningAlgorithm, priv keyMaterial) (oidc.SigningKey, error) {
	kid := id.KeyID()
	kty, params, err := priv.publicParams()
	if err != nil {
		return oidc.SigningKey{}, err
	}
	return oidc.SigningKey{
		KeyID:     kid,
		Algorithm: alg,
		Public: oidc.JWK{
			KeyID:     kid,
			Algorithm: alg,
			KeyType:   kty,
			Use:       "sig",
			Params:    params,
		},
	}, nil
}

// sign builds the compact JWS from the token's header and claims.
func sign(priv keyMaterial, tok oidc.Token) (oidc.SignedToken, error) {
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
	sig, err := priv.sign(tok.Header.Algorithm, []byte(signingInput))
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

// rsaKey is RSA-keyed key material producing the RS* and PS* families.
type rsaKey struct{ priv *rsa.PrivateKey }

func (k rsaKey) publicParams() (oidc.KeyType, oidc.PublicParams, error) {
	pub := &k.priv.PublicKey
	return oidc.KeyTypeRSA, oidc.RSAPublicParams{
		N: base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		E: base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
	}, nil
}

// sign dispatches the RSA-keyed families; EC algorithms are rejected via the
// default arm since they never reach an rsaKey (nextKey pairs family to key).
//
//nolint:exhaustive // rsaKey handles only the RS*/PS* families; ES* fall to default.
func (k rsaKey) sign(alg oidc.SigningAlgorithm, input []byte) ([]byte, error) {
	switch alg {
	case oidc.RS256:
		return rsaPKCS1(k.priv, crypto.SHA256, sha256sum(input))
	case oidc.RS384:
		return rsaPKCS1(k.priv, crypto.SHA384, sha384sum(input))
	case oidc.RS512:
		return rsaPKCS1(k.priv, crypto.SHA512, sha512sum(input))
	case oidc.PS256:
		return rsaPSS(k.priv, crypto.SHA256, sha256sum(input))
	case oidc.PS384:
		return rsaPSS(k.priv, crypto.SHA384, sha384sum(input))
	case oidc.PS512:
		return rsaPSS(k.priv, crypto.SHA512, sha512sum(input))
	default:
		return nil, fmt.Errorf("signing: algorithm %q is not RSA-keyed", alg)
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

// ecKey is EC-keyed key material producing the ES* family.
type ecKey struct{ priv *ecdsa.PrivateKey }

func (k ecKey) publicParams() (oidc.KeyType, oidc.PublicParams, error) {
	// Derive the affine (x, y) from the SEC1 uncompressed encoding via crypto/ecdh
	// (0x04 || X || Y), avoiding the deprecated ecdsa.PublicKey.X/Y fields. ECDH
	// only errors for unsupported curves; P-256/P-384 are always supported.
	pub, err := k.priv.PublicKey.ECDH()
	if err != nil {
		return "", nil, fmt.Errorf("signing: ec public key: %w", err)
	}
	raw := pub.Bytes() // 0x04 || X || Y
	size := coordinateSize(k.priv.Curve)
	return oidc.KeyTypeEC, oidc.ECPublicParams{
		Crv: k.priv.Curve.Params().Name, // "P-256" / "P-384"
		X:   base64.RawURLEncoding.EncodeToString(raw[1 : 1+size]),
		Y:   base64.RawURLEncoding.EncodeToString(raw[1+size:]),
	}, nil
}

// sign dispatches the EC-keyed family; RSA algorithms are rejected via the
// default arm since they never reach an ecKey (nextKey pairs family to key).
//
//nolint:exhaustive // ecKey handles only the ES* family; RS*/PS* fall to default.
func (k ecKey) sign(alg oidc.SigningAlgorithm, input []byte) ([]byte, error) {
	var digest []byte
	switch alg {
	case oidc.ES256:
		digest = sha256sum(input)
	case oidc.ES384:
		digest = sha384sum(input)
	default:
		return nil, fmt.Errorf("signing: algorithm %q is not EC-keyed", alg)
	}
	r, s, err := ecdsa.Sign(rand.Reader, k.priv, digest)
	if err != nil {
		return nil, fmt.Errorf("signing: ecdsa sign: %w", err)
	}
	// JWS (RFC 7518 §3.4): fixed-width R || S, each left-padded to the curve's
	// coordinate byte length — not the ASN.1 DER encoding ecdsa.Sign implies.
	size := coordinateSize(k.priv.Curve)
	rb := r.FillBytes(make([]byte, size))
	sb := s.FillBytes(make([]byte, size))
	return append(rb, sb...), nil
}

// coordinateSize is the byte length of a curve coordinate (32 for P-256, 48 for
// P-384), used to left-pad EC coordinates and signature halves to fixed width.
//
//nolint:mnd // ceiling division converting the curve's bit size to whole bytes.
func coordinateSize(c elliptic.Curve) int {
	return (c.Params().BitSize + 7) / 8
}

func sha256sum(b []byte) []byte { s := sha256.Sum256(b); return s[:] }
func sha384sum(b []byte) []byte { s := sha512.Sum384(b); return s[:] }
func sha512sum(b []byte) []byte { s := sha512.Sum512(b); return s[:] }

func encodeSegment(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }
