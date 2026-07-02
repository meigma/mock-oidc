package oidc

import (
	"strings"
	"time"
)

// defaultTokenExpiry is the built-in token lifetime (upstream default 3600s).
const defaultTokenExpiry = 3600 * time.Second

// defaultAudienceValue is the terminal fallback of the 4-step audience chain.
const defaultAudienceValue = "default"

// FormParams is the domain-owned, multi-valued typed view of url-encoded form
// data — the sanctioned replacement for [net/url.Values] inside the core. The
// httpapi adapter parses raw form bytes into it at the edge (httpapi/form.go);
// url.Values never crosses inward.
type FormParams map[string][]string

// Get returns the last value for key (last-wins), or "" when absent.
func (p FormParams) Get(key string) string {
	vs := p[key]
	if len(vs) == 0 {
		return ""
	}
	return vs[len(vs)-1]
}

// All returns every value for key (request-mapping templating).
func (p FormParams) All(key string) []string { return p[key] }

// SpaceJoined returns all values for key joined by a single space (e.g. scope).
func (p FormParams) SpaceJoined(key string) string { return strings.Join(p[key], " ") }

// CallbackInput is the typed, transport-free view a TokenCallback matches and
// templates against: the parsed grant kind, client, scope, form params, and any
// synthetic params (e.g. the login-injected subject). It replaces upstream's raw
// url.Values, keeping map[string]any out of the domain.
type CallbackInput struct {
	Grant    GrantType
	Client   Client
	Scopes   Scopes
	Subject  Subject    // ROPC username / client_id / login username, pre-resolved
	Params   FormParams // domain type; httpapi/form.go populates it at the edge
	Audience Audience   // token-exchange / configured audience candidates
}

// TokenCallback decides a token's content for one request. It is pure policy
// (no IO), never a port; its FormParams input and Audience result are domain
// types populated at the edge.
type TokenCallback interface {
	IssuerID() IssuerID
	Subject(in CallbackInput) Subject
	Audience(in CallbackInput) Audience
	// TypeHeader returns the JWS "typ" header value (open: default "JWT", may be
	// "at+jwt", etc.) — the open JOSEType, NOT the closed TokenType enum.
	TypeHeader(in CallbackInput) JOSEType
	ExtraClaims(in CallbackInput) ClaimSet
	Expiry() time.Duration
	// Matches reports whether this (configured) callback applies to in; the
	// default callback always matches.
	Matches(in CallbackInput) bool
}

// DefaultTokenCallback is the built-in policy applied when no configured or
// enqueued callback matches. Subject defaults to client_id for
// client_credentials; the access_token audience follows the 4-step precedence
// chain, resolving to ["default"] when nothing is configured; the JWS typ is
// "JWT". The tid/azp registered claims are stamped by the token service's
// default-claim assembly, not here.
type DefaultTokenCallback struct {
	issuer   IssuerID
	audience Audience // nil == unset (fall through the 4-step chain)
	expiry   time.Duration
}

// NewDefaultTokenCallback builds the default callback for issuer, with the
// unset audience and the built-in 3600s expiry.
func NewDefaultTokenCallback(issuer IssuerID) DefaultTokenCallback {
	return DefaultTokenCallback{issuer: issuer, audience: nil, expiry: defaultTokenExpiry}
}

// IssuerID returns the issuer this callback mints for.
func (c DefaultTokenCallback) IssuerID() IssuerID { return c.issuer }

// Subject resolves the token subject: client_credentials uses the client_id;
// otherwise the pre-resolved input subject (ROPC/login username).
func (c DefaultTokenCallback) Subject(in CallbackInput) Subject {
	if in.Grant == GrantClientCredentials {
		return in.Client.ID.AsSubject()
	}
	return in.Subject
}

// Audience applies the 4-step access_token audience precedence: (1) an
// explicitly configured audience (including an explicitly empty one); (2) the
// token-exchange audience request params; (3) the request's non-OIDC scopes;
// (4) ["default"]. id_token audience is [client_id] and does NOT use this chain
// (the service sets it directly).
func (c DefaultTokenCallback) Audience(in CallbackInput) Audience {
	if c.audience != nil {
		return c.audience
	}
	if len(in.Audience) > 0 {
		return in.Audience
	}
	if nonOIDC := in.Scopes.NonOIDC(); len(nonOIDC) > 0 {
		aud := make(Audience, len(nonOIDC))
		for i, sc := range nonOIDC {
			aud[i] = string(sc)
		}
		return aud
	}
	return Audience{defaultAudienceValue}
}

// TypeHeader returns the default JWS typ ("JWT").
func (c DefaultTokenCallback) TypeHeader(_ CallbackInput) JOSEType { return DefaultJOSEType }

// ExtraClaims returns no custom claims; the default callback adds none.
func (c DefaultTokenCallback) ExtraClaims(_ CallbackInput) ClaimSet { return ClaimSet{} }

// Expiry returns the configured token lifetime.
func (c DefaultTokenCallback) Expiry() time.Duration { return c.expiry }

// Matches always reports true — the default callback is the terminal fallback.
func (c DefaultTokenCallback) Matches(_ CallbackInput) bool { return true }
