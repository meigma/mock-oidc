package httpapi

import (
	"encoding/base64"
	"strings"

	"github.com/meigma/mock-oidc/internal/oidc"
)

// toDiscoveryDTO is a pure field copy from the domain discovery document to the
// fixed-order wire DTO. The advertised algorithm set is stringified from the
// domain SigningAlgorithm values (single-sourced from SupportedSigningAlgorithms).
func toDiscoveryDTO(d oidc.DiscoveryDocument) DiscoveryDTO {
	return DiscoveryDTO{
		Issuer:                 d.Issuer,
		AuthorizationEndpoint:  d.AuthorizationEndpoint,
		EndSessionEndpoint:     d.EndSessionEndpoint,
		RevocationEndpoint:     d.RevocationEndpoint,
		TokenEndpoint:          d.TokenEndpoint,
		UserinfoEndpoint:       d.UserinfoEndpoint,
		JwksURI:                d.JWKSURI,
		IntrospectionEndpoint:  d.IntrospectionEndpoint,
		ResponseTypesSupported: d.ResponseTypesSupported,
		ResponseModesSupported: d.ResponseModesSupported,
		SubjectTypesSupported:  d.SubjectTypesSupported,
		IDTokenSigningAlgs:     algsToStrings(d.IDTokenSigningAlgValuesSupported),
		CodeChallengeMethods:   d.CodeChallengeMethodsSupported,
	}
}

// algsToStrings stringifies the domain algorithm enum for the wire.
func algsToStrings(algs []oidc.SigningAlgorithm) []string {
	out := make([]string, len(algs))
	for i, a := range algs {
		out[i] = string(a)
	}
	return out
}

// toJWKSDTO is a pure field copy from the domain JWK set to the wire DTO. Only
// public parameters ever appear; no private material reaches the domain or wire.
func toJWKSDTO(set oidc.JWKS) JWKSDTO {
	dto := JWKSDTO{Keys: make([]JWKDTO, len(set.Keys))}
	for i, k := range set.Keys {
		dto.Keys[i] = toJWKDTO(k)
	}
	return dto
}

// toJWKDTO copies one public JWK, projecting the sealed PublicParams union onto
// the RSA (n,e) or EC (crv,x,y) wire fields.
func toJWKDTO(k oidc.JWK) JWKDTO {
	dto := JWKDTO{
		Kty: string(k.KeyType),
		Use: k.Use,
		Kid: string(k.KeyID),
		Alg: string(k.Algorithm),
	}
	switch p := k.Params.(type) {
	case oidc.RSAPublicParams:
		dto.N, dto.E = p.N, p.E
	case oidc.ECPublicParams:
		dto.Crv, dto.X, dto.Y = p.Crv, p.X, p.Y
	}
	return dto
}

// toTokenResponseDTO copies the domain token response onto the wire matrix.
// ExpiresIn is taken verbatim from the domain value (derived from the same Clock
// as exp), never recomputed from a live wall clock. IDToken and RefreshToken are
// omitempty on the DTO, so the client_credentials shape (which leaves them
// zero) still emits only the access-token fields.
func toTokenResponseDTO(r oidc.TokenResponse) TokenResponseDTO {
	return TokenResponseDTO{
		TokenType:       string(r.TokenType),
		IssuedTokenType: string(r.IssuedTokenType),
		IDToken:         string(r.IDToken),
		AccessToken:     string(r.AccessToken),
		RefreshToken:    string(r.RefreshToken),
		ExpiresIn:       int(r.ExpiresIn),
		Scope:           r.Scope.String(),
	}
}

// toUserInfoBody projects a verified claim set onto the /userinfo wire body: the
// ENTIRE claim set verbatim (registered claims + custom, no scoping). aud is
// echoed in its array form (no single-element collapse) so the response mirrors
// the token; the introspection edge is the only one that collapses aud.
func toUserInfoBody(claims oidc.ClaimSet) map[string]any {
	return claimsToMap(claims, false)
}

// toIntrospectionBody shapes the RFC 7662 introspection wire body. An inactive
// result is exactly {"active":false}; an active result carries the verified
// claims (aud collapsed to a scalar when single-valued) plus active=true and the
// token_type (default Bearer from the domain result).
func toIntrospectionBody(result oidc.IntrospectionResult) map[string]any {
	if !result.Active {
		return map[string]any{"active": false}
	}
	body := claimsToMap(result.Claims, true)
	body["active"] = true
	if result.TokenType != "" {
		body["token_type"] = string(result.TokenType)
	}
	return body
}

// claimsToMap renders a typed ClaimSet as the wire object every claim-bearing
// lifecycle response shares. Only present claims are emitted (a zero Instant or
// an empty/nil field is omitted). When collapseAud is set a single-element aud is
// emitted as a scalar string (RFC 7662 introspection); otherwise aud stays an
// array (userinfo verbatim). Custom claims are appended in their stored order.
func claimsToMap(c oidc.ClaimSet, collapseAud bool) map[string]any {
	m := make(map[string]any)
	if c.Issuer != "" {
		m["iss"] = c.Issuer
	}
	if c.Subject != "" {
		m["sub"] = string(c.Subject)
	}
	if c.Audience != nil {
		m["aud"] = audValue(c.Audience, collapseAud)
	}
	if t := c.IssuedAt; !t.Time().IsZero() {
		m["iat"] = t.Unix()
	}
	if t := c.NotBefore; !t.Time().IsZero() {
		m["nbf"] = t.Unix()
	}
	if t := c.Expiry; !t.Time().IsZero() {
		m["exp"] = t.Unix()
	}
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
	return m
}

// audValue renders the aud claim for the wire: a single-element audience becomes
// a scalar string when collapse is requested (introspection), otherwise it stays
// an array. A multi-element audience always stays an array.
func audValue(aud oidc.Audience, collapse bool) any {
	if collapse && len(aud) == 1 {
		return aud[0]
	}
	return []string(aud)
}

// decodeTokenRequest is the anti-corruption boundary for /token: it parses the
// closed grant set and only the fields that grant uses, producing a typed
// command. ParseGrantType yields the typed *ProtocolError for blank/unknown
// grant_type (no bare sentinel, no map[string]any), so the edge never 500s on the
// two most basic error cases. client_credentials, authorization_code, and
// refresh_token decode their grant-specific fields here; other (valid) grants
// decode to the base command and the token service reports them as unsupported
// until their slice lands.
func decodeTokenRequest(iss oidc.IssuerID, f FlatForm, authz string) (oidc.TokenRequest, error) {
	grant, err := oidc.ParseGrantType(f.Get("grant_type")) // "" → MissingParameter; junk → UnsupportedGrant
	if err != nil {
		return oidc.TokenRequest{}, err
	}
	client := decodeClientAuth(authz, f)
	base := oidc.NewTokenRequest(iss, grant, client).WithScopes(oidc.ParseScopes(f.Get("scope")))

	if grant == oidc.GrantAuthorizationCode {
		return base.WithAuthorizationCode(
			oidc.AuthorizationCode(f.Get("code")),
			f.Get("code_verifier"),
			f.Get("redirect_uri"),
		), nil
	}
	if grant == oidc.GrantRefreshToken {
		return base.WithRefreshToken(oidc.RefreshToken(f.Get("refresh_token"))), nil
	}
	if grant == oidc.GrantPassword {
		return base.WithPassword(f.Get("username"), oidc.ParseScopes(f.Get("scope"))), nil
	}
	if grant == oidc.GrantJWTBearer {
		return base.WithAssertion(f.Get("assertion"), oidc.ParseScopes(f.Get("scope")))
	}
	if grant == oidc.GrantTokenExchange {
		return base.WithSubjectToken(
			f.Get("subject_token"), f.Get("subject_token_type"), f.Get("audience"))
	}
	return base, nil
}

// clientAssertionTypeJWTBearer is the RFC 7523 client_assertion_type value that
// marks a private_key_jwt client authentication on the token endpoint.
//
//nolint:gosec // G101: an OAuth2 client-assertion-type URN, not a credential.
const clientAssertionTypeJWTBearer = "urn:ietf:params:oauth:client-assertion-type:jwt-bearer"

// decodeClientAuth reads the request's client identity off the Authorization
// header (client_secret_basic) or the form body (client_secret_post / none). No
// secret is ever validated — Auth records only how the client presented itself.
// A request with no client identity yields an empty ClientID (permitted; the
// client_credentials subject simply resolves empty).
func decodeClientAuth(authz string, f FlatForm) oidc.Client {
	if id, ok := basicAuthClientID(authz); ok {
		return oidc.Client{ID: oidc.ClientID(id), Auth: oidc.ClientAuthClientSecretBasic}
	}
	id := oidc.ClientID(f.Get("client_id"))
	// private_key_jwt: carry the raw client_assertion inward so the token-exchange
	// path can parse (unverified) and structurally validate it. The assertion's
	// own iss/sub carry the effective client_id, so a public client_id is optional.
	if f.Get("client_assertion_type") == clientAssertionTypeJWTBearer {
		return oidc.Client{
			ID:        id,
			Auth:      oidc.ClientAuthPrivateKeyJWT,
			Assertion: oidc.SignedToken(f.Get("client_assertion")),
		}
	}
	auth := oidc.ClientAuthNone
	if f.Has("client_secret") {
		auth = oidc.ClientAuthClientSecretPost
	}
	return oidc.Client{ID: id, Auth: auth}
}

// basicAuthClientID extracts the userid (client_id) from an HTTP Basic
// Authorization header, or reports ok=false when the header is absent or not
// Basic. The password (client_secret) is intentionally discarded — it is never
// validated.
func basicAuthClientID(authz string) (string, bool) {
	const prefix = "Basic "
	if len(authz) < len(prefix) || !strings.EqualFold(authz[:len(prefix)], prefix) {
		return "", false
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(authz[len(prefix):]))
	if err != nil {
		return "", false
	}
	id, _, found := strings.Cut(string(raw), ":")
	if !found {
		return "", false
	}
	return id, true
}
