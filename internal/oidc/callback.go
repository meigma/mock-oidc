package oidc

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
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

// claimTemplateRe matches an upstream ${key} placeholder (Template.kt: \$\{(\w+)\}).
var claimTemplateRe = regexp.MustCompile(`\$\{(\w+)\}`)

// resolveParam returns the request-derived value for name, applying upstream's
// precedence (highest wins): client_id/clientId is always authoritative and can
// NOT be shadowed by a same-named form param; then the multi-valued form params
// (space-joined); then the synthetic "subject" match param injected at login. A
// present-but-empty value still reports ok=true so a "*" mapping can match it.
func (in CallbackInput) resolveParam(name string) (string, bool) {
	switch name {
	case "client_id", "clientId":
		if in.Client.ID != "" {
			return string(in.Client.ID), true
		}
		return "", false
	}
	if _, ok := in.Params[name]; ok {
		return in.Params.SpaceJoined(name), true // multi-values space-joined
	}
	if name == "subject" && in.Subject != "" {
		return string(in.Subject), true
	}
	return "", false
}

// renderTemplate substitutes every ${key} in s from resolveParam; an unknown key
// is left literal (upstream Template.kt).
func (in CallbackInput) renderTemplate(s string) string {
	return claimTemplateRe.ReplaceAllStringFunc(s, func(match string) string {
		key := claimTemplateRe.FindStringSubmatch(match)[1]
		if v, ok := in.resolveParam(key); ok {
			return v
		}
		return match
	})
}

// renderValue applies ${...} templating over a claim value, recursing into
// nested lists and objects; only STRING leaves are templated — numbers and bools
// pass through verbatim (upstream: non-String leaf values are not templated).
func (in CallbackInput) renderValue(v ClaimValue) ClaimValue {
	switch t := v.(type) {
	case string:
		return in.renderTemplate(t)
	case []ClaimValue:
		out := make([]ClaimValue, len(t))
		for i, e := range t {
			out[i] = in.renderValue(e)
		}
		return out
	case CustomClaims:
		var out CustomClaims
		for _, e := range t.Entries() {
			out.Set(e.Name, in.renderValue(e.Value))
		}
		return out
	default:
		return v
	}
}

// RequestMapping is one request-param → templated-claims rule inside a
// RequestMappingCallback. Param is the form/synthetic param name to test; Match
// is "*" (any present value), an exact string, or a full-match regex; TypeHeader
// overrides the JWS typ; Claims is the ordered ${...} template applied when the
// mapping matches. It is a domain value populated at the config/control edge.
type RequestMapping struct {
	Param      string
	Match      string
	TypeHeader JOSEType
	Claims     CustomClaims
}

// matches reports whether in satisfies this mapping. The resolved param must be
// present; then match == "*" wins, else an exact-string match wins, else the
// value must FULL-match the pattern compiled as a regex. An invalid regex is
// swallowed silently (treated as exact-only, never a panic) so a bad config
// pattern degrades to non-matching rather than crashing token issuance.
func (m RequestMapping) matches(in CallbackInput) bool {
	value, ok := in.resolveParam(m.Param)
	if !ok {
		return false
	}
	if m.Match == "*" {
		return true
	}
	if value == m.Match {
		return true
	}
	re, err := regexp.Compile("^(?:" + m.Match + ")$")
	if err != nil {
		return false // invalid regex swallowed → exact-only (already failed above)
	}
	return re.MatchString(value)
}

// RequestMappingCallback is the JSON-driven TokenCallback (config tokenCallbacks
// and requestMappings-bearing scenarios). It reads the request's form params,
// finds the FIRST matching RequestMapping, and derives subject = claims['sub'],
// audience = claims['aud'] (coerced to a list), typ = the mapping's typeHeader,
// and addClaims = the templated claims. It deliberately does NOT add tid or azp —
// those are stamped only by DefaultTokenCallback (upstream parity).
type RequestMappingCallback struct {
	issuer   IssuerID
	expiry   time.Duration
	mappings []RequestMapping
}

// NewRequestMappingCallback builds a request-mapping callback for issuer. A
// non-positive expiry defaults to the built-in 3600s lifetime.
func NewRequestMappingCallback(
	issuer IssuerID,
	expiry time.Duration,
	mappings []RequestMapping,
) (RequestMappingCallback, error) {
	if expiry <= 0 {
		expiry = defaultTokenExpiry
	}
	return RequestMappingCallback{issuer: issuer, expiry: expiry, mappings: slices.Clone(mappings)}, nil
}

// IssuerID returns the issuer this callback mints for.
func (c RequestMappingCallback) IssuerID() IssuerID { return c.issuer }

// match returns the first mapping that applies to in.
func (c RequestMappingCallback) match(in CallbackInput) (RequestMapping, bool) {
	for _, m := range c.mappings {
		if m.matches(in) {
			return m, true
		}
	}
	return RequestMapping{}, false
}

// rendered returns the templated claims of the first matching mapping, or an
// empty set when nothing matches.
func (c RequestMappingCallback) rendered(in CallbackInput) CustomClaims {
	m, ok := c.match(in)
	if !ok {
		return CustomClaims{}
	}
	var out CustomClaims
	for _, e := range m.Claims.Entries() {
		out.Set(e.Name, in.renderValue(e.Value))
	}
	return out
}

// Matches reports whether any mapping applies to in; when none does, the resolver
// falls through to the next configured callback (or the default).
func (c RequestMappingCallback) Matches(in CallbackInput) bool {
	_, ok := c.match(in)
	return ok
}

// Subject resolves to the templated claims['sub'] when present, else the
// pre-resolved input subject (upstream: nullable when the mapping omits sub).
func (c RequestMappingCallback) Subject(in CallbackInput) Subject {
	if v, ok := c.rendered(in).Get("sub"); ok {
		if s, ok := v.(string); ok {
			return Subject(s)
		}
	}
	return in.Subject
}

// Audience resolves to the templated claims['aud'] coerced to a string list, or
// nil (unset) when the mapping carries no aud.
func (c RequestMappingCallback) Audience(in CallbackInput) Audience {
	if v, ok := c.rendered(in).Get("aud"); ok {
		return coerceAudience(v)
	}
	return nil
}

// TypeHeader returns the matched mapping's typeHeader, defaulting to "JWT".
func (c RequestMappingCallback) TypeHeader(in CallbackInput) JOSEType {
	if m, ok := c.match(in); ok && m.TypeHeader != "" {
		return m.TypeHeader
	}
	return DefaultJOSEType
}

// ExtraClaims returns the templated claims minus any registered claim name, so a
// mapping can never shadow a typed field (sub/aud/iss/... are handled elsewhere).
func (c RequestMappingCallback) ExtraClaims(in CallbackInput) ClaimSet {
	var out CustomClaims
	for _, e := range c.rendered(in).Entries() {
		if _, reserved := registeredClaimNames[e.Name]; reserved {
			continue
		}
		out.Set(e.Name, e.Value)
	}
	return ClaimSet{Custom: out}
}

// Expiry returns the configured token lifetime.
func (c RequestMappingCallback) Expiry() time.Duration { return c.expiry }

// coerceAudience turns a templated aud claim value into an Audience: a string
// becomes a single-element list; a list is flattened to its string elements;
// anything else stringifies. A nil/absent value never reaches here.
func coerceAudience(v ClaimValue) Audience {
	switch t := v.(type) {
	case string:
		return Audience{t}
	case []string:
		return Audience(slices.Clone(t))
	case []ClaimValue:
		aud := make(Audience, 0, len(t))
		for _, e := range t {
			if s, ok := e.(string); ok {
				aud = append(aud, s)
				continue
			}
			aud = append(aud, fmt.Sprint(e))
		}
		return aud
	default:
		return Audience{fmt.Sprint(v)}
	}
}

// ScenarioID identifies one enqueued scenario in the control-plane queue. The
// memory queue mints it on Enqueue; the domain only names the type.
type ScenarioID string

// ErrNilScenarioCallback is returned by NewScenario when its callback is nil.
var ErrNilScenarioCallback = errors.New("scenario callback must not be nil")

// Scenario is a one-shot, issuer-matched, single-use callback enqueued through
// the control plane and consumed by the next matching token request (including
// refresh). It wraps a fully-resolved TokenCallback; its issuer is that
// callback's issuer, so the queue can match the head against a request's issuer.
type Scenario struct {
	Callback TokenCallback
}

// NewScenario wraps a resolved TokenCallback as a one-shot scenario.
func NewScenario(cb TokenCallback) (Scenario, error) {
	if cb == nil {
		return Scenario{}, ErrNilScenarioCallback
	}
	return Scenario{Callback: cb}, nil
}

// IssuerID returns the issuer the scenario mints for (its callback's issuer).
func (s Scenario) IssuerID() IssuerID {
	if s.Callback == nil {
		return ""
	}
	return s.Callback.IssuerID()
}
