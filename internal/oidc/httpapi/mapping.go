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
		TokenType:    string(r.TokenType),
		IDToken:      string(r.IDToken),
		AccessToken:  string(r.AccessToken),
		RefreshToken: string(r.RefreshToken),
		ExpiresIn:    int(r.ExpiresIn),
		Scope:        r.Scope.String(),
	}
}

// decodeTokenRequest is the anti-corruption boundary for /token: it parses the
// closed grant set and only the fields that grant uses, producing a typed
// command. ParseGrantType yields the typed *ProtocolError for blank/unknown
// grant_type (no bare sentinel, no map[string]any), so the edge never 500s on the
// two most basic error cases. Only client_credentials is wired this slice; other
// (valid) grants decode to the base command and the token service reports them as
// unsupported until their slice lands.
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
	return base, nil
}

// decodeClientAuth reads the request's client identity off the Authorization
// header (client_secret_basic) or the form body (client_secret_post / none). No
// secret is ever validated — Auth records only how the client presented itself.
// A request with no client identity yields an empty ClientID (permitted; the
// client_credentials subject simply resolves empty).
func decodeClientAuth(authz string, f FlatForm) oidc.Client {
	if id, ok := basicAuthClientID(authz); ok {
		return oidc.Client{ID: oidc.ClientID(id), Auth: oidc.ClientAuthClientSecretBasic}
	}
	id := f.Get("client_id")
	auth := oidc.ClientAuthNone
	if f.Has("client_secret") {
		auth = oidc.ClientAuthClientSecretPost
	}
	return oidc.Client{ID: oidc.ClientID(id), Auth: auth}
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
