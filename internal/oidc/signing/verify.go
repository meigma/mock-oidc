package signing

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"

	"github.com/meigma/mock-oidc/internal/oidc"
)

// ErrTokenVerification is the sentinel every verification failure wraps, so a
// caller (or the domain's NewInvalidToken cause chain) can match it with
// [errors.Is] while the client-visible description stays a stable phrase.
var ErrTokenVerification = errors.New("signing: token verification failed")

// compactSegments is the number of dot-separated segments in a signed compact
// JWS (header.payload.signature).
const compactSegments = 3

// Verify implements [oidc.TokenVerifier]. It is deliberately strict — the server
// verifies only its OWN tokens against known keys (kid == issuerID) — and applies
// the resolved-JOSE hardening rules:
//
//  1. The verification algorithm is derived from the RESOLVED key, never trusted
//     from the token header. alg=none is always rejected (the resolved key is a
//     real signing key), and the header alg MUST match the key algorithm
//     (alg-confusion guard).
//  2. typ is gated to "JWT" and "at+jwt" (RFC 9068, Decision D-4); every other
//     value is rejected.
//  3. iss must name the resolved issuer.
//  4. iat/exp (via nbf/exp) are checked against the PASSED now Instant — the same
//     freezable Clock issuance uses — never [time.Now].
//
// Signature verification uses the stdlib asymmetric primitives (constant-time by
// construction; no manual byte comparison). A lazily-created issuer verifies
// because keyFor materializes its key deterministically on first reference.
func (p *Provider) Verify(
	_ context.Context,
	id oidc.IssuerID,
	token oidc.SignedToken,
	now oidc.Instant,
) (oidc.ClaimSet, error) {
	key, err := p.keyFor(id)
	if err != nil {
		return oidc.ClaimSet{}, err
	}
	keyAlg := key.meta.Algorithm
	if keyAlg == oidc.AlgNone || !supported(keyAlg) {
		return oidc.ClaimSet{}, verifyErr("resolved key algorithm is not verifiable")
	}

	parts := strings.Split(string(token), ".")
	if len(parts) != compactSegments {
		return oidc.ClaimSet{}, verifyErr("malformed compact JWS")
	}

	if headerErr := verifyHeader(parts[0], keyAlg); headerErr != nil {
		return oidc.ClaimSet{}, headerErr
	}

	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return oidc.ClaimSet{}, verifyErr("undecodable signature segment")
	}
	signingInput := []byte(parts[0] + "." + parts[1])
	if sigErr := key.priv.verify(keyAlg, signingInput, sig); sigErr != nil {
		return oidc.ClaimSet{}, verifyErr("signature does not verify")
	}

	claims, err := parseClaims(parts[1])
	if err != nil {
		return oidc.ClaimSet{}, err
	}
	if !issuerMatches(claims.Issuer, id) {
		return oidc.ClaimSet{}, verifyErr("iss does not match the resolved issuer")
	}
	if timeErr := checkTimes(claims, now); timeErr != nil {
		return oidc.ClaimSet{}, timeErr
	}
	return claims, nil
}

// verifyHeader decodes the protected header, gates typ to JWT/at+jwt, and pins
// the header alg to the resolved key algorithm (alg-confusion guard; alg=none is
// rejected because keyAlg is a real signing algorithm).
func verifyHeader(segment string, keyAlg oidc.SigningAlgorithm) error {
	raw, err := base64.RawURLEncoding.DecodeString(segment)
	if err != nil {
		return verifyErr("undecodable header segment")
	}
	var hdr struct {
		Alg string `json:"alg"`
		Typ string `json:"typ"`
	}
	if err := json.Unmarshal(raw, &hdr); err != nil {
		return verifyErr("undecodable header JSON")
	}
	if !acceptedJOSEType(hdr.Typ) {
		return verifyErr(fmt.Sprintf("unacceptable typ %q", hdr.Typ))
	}
	if oidc.SigningAlgorithm(hdr.Alg) != keyAlg {
		return verifyErr(fmt.Sprintf("header alg %q does not match key alg %q", hdr.Alg, keyAlg))
	}
	return nil
}

// acceptedJOSEType reports whether typ is an access-token type the server
// self-verifies: "JWT" or the RFC 9068 "at+jwt" (Decision D-4). Every other value
// — including the empty string and a foreign "foo+jwt" — is rejected.
func acceptedJOSEType(typ string) bool {
	return typ == string(oidc.DefaultJOSEType) || typ == "at+jwt"
}

// issuerMatches reports whether the iss claim names id. The iss URL is
// scheme://host/{id}, so the final path segment must equal the resolved issuer
// id. (Signature verification against id's key already binds the token to the
// issuer; this is the additional claim-level guard.)
func issuerMatches(iss string, id oidc.IssuerID) bool {
	idx := strings.LastIndex(iss, "/")
	if idx < 0 {
		return false
	}
	return iss[idx+1:] == string(id)
}

// checkTimes enforces the temporal claims against the injected now: a token whose
// exp is at/after now-passed... i.e. now after exp is expired, and now before nbf
// is not-yet-valid. nbf equals iat at issuance, so this also covers iat.
func checkTimes(claims oidc.ClaimSet, now oidc.Instant) error {
	if exp := claims.Expiry.Time(); !exp.IsZero() && now.Time().After(exp) {
		return verifyErr("token is expired")
	}
	if nbf := claims.NotBefore.Time(); !nbf.IsZero() && now.Time().Before(nbf) {
		return verifyErr("token is not yet valid")
	}
	return nil
}

// verify checks the RSA-keyed families (RS*/PS*) against the public key.
//
//nolint:exhaustive // rsaKey verifies only the RS*/PS* families; ES* fall to default.
func (k rsaKey) verify(alg oidc.SigningAlgorithm, input, sig []byte) error {
	pub := &k.priv.PublicKey
	switch alg {
	case oidc.RS256:
		return verifyPKCS1(pub, crypto.SHA256, sha256sum(input), sig)
	case oidc.RS384:
		return verifyPKCS1(pub, crypto.SHA384, sha384sum(input), sig)
	case oidc.RS512:
		return verifyPKCS1(pub, crypto.SHA512, sha512sum(input), sig)
	case oidc.PS256:
		return verifyPSS(pub, crypto.SHA256, sha256sum(input), sig)
	case oidc.PS384:
		return verifyPSS(pub, crypto.SHA384, sha384sum(input), sig)
	case oidc.PS512:
		return verifyPSS(pub, crypto.SHA512, sha512sum(input), sig)
	default:
		return fmt.Errorf("signing: algorithm %q is not RSA-keyed", alg)
	}
}

func verifyPKCS1(pub *rsa.PublicKey, h crypto.Hash, digest, sig []byte) error {
	if err := rsa.VerifyPKCS1v15(pub, h, digest, sig); err != nil {
		return fmt.Errorf("signing: rsa pkcs1v15 verify: %w", err)
	}
	return nil
}

func verifyPSS(pub *rsa.PublicKey, h crypto.Hash, digest, sig []byte) error {
	if err := rsa.VerifyPSS(pub, h, digest, sig, &rsa.PSSOptions{
		SaltLength: rsa.PSSSaltLengthEqualsHash,
		Hash:       h,
	}); err != nil {
		return fmt.Errorf("signing: rsa pss verify: %w", err)
	}
	return nil
}

// verify checks the EC-keyed family (ES*) against the public key. The JWS
// signature is fixed-width R || S (RFC 7518 §3.4), not ASN.1 DER.
//
//nolint:exhaustive // ecKey verifies only the ES* family; RS*/PS* fall to default.
func (k ecKey) verify(alg oidc.SigningAlgorithm, input, sig []byte) error {
	var digest []byte
	switch alg {
	case oidc.ES256:
		digest = sha256sum(input)
	case oidc.ES384:
		digest = sha384sum(input)
	default:
		return fmt.Errorf("signing: algorithm %q is not EC-keyed", alg)
	}
	size := coordinateSize(k.priv.Curve)
	if len(sig) != 2*size {
		return errors.New("signing: ec signature is not fixed-width R || S")
	}
	r := new(big.Int).SetBytes(sig[:size])
	s := new(big.Int).SetBytes(sig[size:])
	if !ecdsa.Verify(&k.priv.PublicKey, digest, r, s) {
		return errors.New("signing: ecdsa signature does not verify")
	}
	return nil
}

// registeredPayloadClaims are the JWT payload keys parseClaims maps to typed
// ClaimSet fields; every other key becomes a Custom claim.
//
//nolint:gochecknoglobals // fixed set of registered claim keys for the payload parser.
var registeredPayloadClaims = map[string]struct{}{
	"iss": {}, "sub": {}, "aud": {}, "iat": {}, "nbf": {}, "exp": {},
	"jti": {}, "nonce": {}, "azp": {}, "tid": {}, "scope": {},
}

// parseClaims parses the JWT payload segment into a typed ClaimSet: registered
// claims populate the strongly-typed fields (aud accepts both the scalar and
// array JSON forms) and every other key is folded into Custom in sorted order for
// deterministic emission.
func parseClaims(segment string) (oidc.ClaimSet, error) {
	payload, err := base64.RawURLEncoding.DecodeString(segment)
	if err != nil {
		return oidc.ClaimSet{}, verifyErr("undecodable payload segment")
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(payload, &raw); err != nil {
		return oidc.ClaimSet{}, verifyErr("undecodable payload JSON")
	}

	var cs oidc.ClaimSet
	if s, ok := decodeString(raw, "iss"); ok {
		cs.Issuer = s
	}
	if s, ok := decodeString(raw, "sub"); ok {
		cs.Subject = oidc.Subject(s)
	}
	if aud, ok := decodeAudience(raw); ok {
		cs.Audience = aud
	}
	if t, ok := decodeInstant(raw, "iat"); ok {
		cs.IssuedAt = t
	}
	if t, ok := decodeInstant(raw, "nbf"); ok {
		cs.NotBefore = t
	}
	if t, ok := decodeInstant(raw, "exp"); ok {
		cs.Expiry = t
	}
	if s, ok := decodeString(raw, "jti"); ok {
		cs.JWTID = s
	}
	if s, ok := decodeString(raw, "nonce"); ok {
		n := oidc.Nonce(s)
		cs.Nonce = &n
	}
	if s, ok := decodeString(raw, "azp"); ok {
		azp := oidc.ClientID(s)
		cs.Azp = &azp
	}
	if s, ok := decodeString(raw, "tid"); ok {
		tid := s
		cs.Tenant = &tid
	}
	if s, ok := decodeString(raw, "scope"); ok {
		cs.Scope = oidc.ParseScopes(s)
	}

	custom := make([]string, 0, len(raw))
	for name := range raw {
		if _, reserved := registeredPayloadClaims[name]; !reserved {
			custom = append(custom, name)
		}
	}
	sort.Strings(custom)
	for _, name := range custom {
		var v any
		if err := json.Unmarshal(raw[name], &v); err != nil {
			return oidc.ClaimSet{}, verifyErr(fmt.Sprintf("undecodable custom claim %q", name))
		}
		cs.Custom.Set(name, v)
	}
	return cs, nil
}

func decodeString(raw map[string]json.RawMessage, key string) (string, bool) {
	v, ok := raw[key]
	if !ok {
		return "", false
	}
	var s string
	if err := json.Unmarshal(v, &s); err != nil {
		return "", false
	}
	return s, true
}

func decodeInstant(raw map[string]json.RawMessage, key string) (oidc.Instant, bool) {
	v, ok := raw[key]
	if !ok {
		return oidc.Instant{}, false
	}
	var secs int64
	if err := json.Unmarshal(v, &secs); err != nil {
		return oidc.Instant{}, false
	}
	return oidc.NewInstant(time.Unix(secs, 0)), true
}

// decodeAudience accepts both the scalar ("aud":"x") and array ("aud":["x","y"])
// JSON forms, matching what the signer emits and what a stock client sends back.
func decodeAudience(raw map[string]json.RawMessage) (oidc.Audience, bool) {
	v, ok := raw["aud"]
	if !ok {
		return nil, false
	}
	var arr []string
	if err := json.Unmarshal(v, &arr); err == nil {
		return oidc.Audience(arr), true
	}
	var single string
	if err := json.Unmarshal(v, &single); err == nil {
		return oidc.Audience{single}, true
	}
	return nil, false
}

func verifyErr(reason string) error {
	return fmt.Errorf("%w: %s", ErrTokenVerification, reason)
}
