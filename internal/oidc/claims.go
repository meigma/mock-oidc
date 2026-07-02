package oidc

import (
	"maps"
	"slices"
	"strings"
)

// Scope is a single OAuth2 scope token.
type Scope string

// Scopes is an ordered, de-duplicated scope set. Order is preserved so the
// echoed `scope` response reproduces the request order.
type Scopes []Scope

// oidcScopes are excluded when deriving an access_token audience from scopes
// (DefaultTokenCallback audience step 3).
//
//nolint:gochecknoglobals // fixed membership set for the OIDC scope filter (TDD §5).
var oidcScopes = map[Scope]struct{}{
	"openid": {}, "profile": {}, "email": {}, "address": {}, "phone": {}, "offline_access": {},
}

// ParseScopes splits a space-delimited scope string, dropping blanks and
// duplicates while preserving first-seen order.
func ParseScopes(raw string) Scopes {
	var out Scopes
	seen := make(map[Scope]struct{})
	for f := range strings.FieldsSeq(raw) {
		s := Scope(f)
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

// NonOIDC returns the scopes with the standard OIDC scopes removed — the
// fallback audience source when no audience is otherwise configured.
func (s Scopes) NonOIDC() Scopes {
	out := make(Scopes, 0, len(s))
	for _, sc := range s {
		if _, ok := oidcScopes[sc]; !ok {
			out = append(out, sc)
		}
	}
	return out
}

// String renders the scopes space-delimited in order.
func (s Scopes) String() string {
	parts := make([]string, len(s))
	for i, sc := range s {
		parts[i] = string(sc)
	}
	return strings.Join(parts, " ")
}

// ClaimName is the closed set of registered claims the domain models as typed
// fields. Custom claims use plain string keys via CustomClaims and are NOT
// members of this set.
type ClaimName string

// The registered claim names carried as typed ClaimSet fields.
const (
	ClaimSub   ClaimName = "sub"
	ClaimAud   ClaimName = "aud"
	ClaimIss   ClaimName = "iss"
	ClaimIat   ClaimName = "iat"
	ClaimNbf   ClaimName = "nbf"
	ClaimExp   ClaimName = "exp"
	ClaimJti   ClaimName = "jti"
	ClaimNonce ClaimName = "nonce"
	ClaimTid   ClaimName = "tid"
	ClaimAzp   ClaimName = "azp"
	ClaimScope ClaimName = "scope"
)

// Audience is the ordered audience list a token carries (aud). A nil Audience
// means "unset" (fall through the 4-step chain); a non-nil empty Audience means
// an explicitly configured empty audience (stop, emit no aud) — the two are
// distinct (catalog audience step 1).
type Audience []string

// Nonce is the OIDC nonce. Its presence is semantic, so it is carried as *Nonce
// everywhere (nil == "no nonce"); the value is never a bare string.
type Nonce string

// ClaimValue is one custom-claim leaf. It is a DEFINED type (note: no `=`), so
// map[string]ClaimValue is nominally distinct from map[string]any and the
// compiler keeps this one dynamic boundary named and contained. It ranges over
// the JSON value space (string | float64 | bool | []ClaimValue | nested
// CustomClaims).
type ClaimValue any

// ClaimEntry is one ordered (name, value) pair — the ordered-emission unit that
// replaces any "claims as map[string]any" return.
type ClaimEntry struct {
	Name  string
	Value ClaimValue
}

// CustomClaims is an insertion-ordered claim map. Ordering backs deterministic
// emission and the order-sensitive login/mapping merge (putIfAbsent semantics).
// It is the only container the domain exposes for dynamic claims; callers never
// touch a raw map. The zero value is ready to use.
//
//nolint:recvcheck // mutators take a pointer receiver; read-only accessors take a value receiver (intentional).
type CustomClaims struct {
	order  []string
	values map[string]ClaimValue
}

// Set inserts or replaces a claim, preserving first-insertion order.
func (c *CustomClaims) Set(name string, v ClaimValue) {
	if c.values == nil {
		c.values = make(map[string]ClaimValue)
	}
	if _, ok := c.values[name]; !ok {
		c.order = append(c.order, name)
	}
	c.values[name] = v
}

// SetIfAbsent adds a claim only when absent — the login-claims merge rule (login
// claims ADD but never OVERWRITE a value the mapping already set). It reports
// whether the value was inserted.
func (c *CustomClaims) SetIfAbsent(name string, v ClaimValue) bool {
	if c.values != nil {
		if _, ok := c.values[name]; ok {
			return false
		}
	}
	c.Set(name, v)
	return true
}

// Get returns the value for name and whether it is present.
func (c CustomClaims) Get(name string) (ClaimValue, bool) {
	v, ok := c.values[name]
	return v, ok
}

// Len returns the number of custom claims.
func (c CustomClaims) Len() int { return len(c.order) }

// Entries returns the claims in insertion order — the ordered-emission accessor.
func (c CustomClaims) Entries() []ClaimEntry {
	out := make([]ClaimEntry, len(c.order))
	for i, name := range c.order {
		out[i] = ClaimEntry{Name: name, Value: c.values[name]}
	}
	return out
}

// Clone returns a deep copy with its own order slice and values map, so a copy
// can be mutated without aliasing the source (the ClaimSet ownership rule).
func (c CustomClaims) Clone() CustomClaims {
	if c.values == nil {
		return CustomClaims{order: nil, values: nil}
	}
	values := make(map[string]ClaimValue, len(c.values))
	maps.Copy(values, c.values)
	return CustomClaims{order: slices.Clone(c.order), values: values}
}

// ClaimSet is the typed claim container for one token. Registered claims are
// strongly-typed fields with invariants; arbitrary scripted claims live in
// Custom, an ordered accessor (never an exposed map). The optional pointer
// fields encode the catalog's "added only when non-null" rules: Nonce and Azp
// are pointers because their presence is semantic. A built ClaimSet is treated
// as immutable — Custom is owned by exactly one ClaimSet.
type ClaimSet struct {
	Subject   Subject
	Audience  Audience // id_token => [client_id]; access_token => 4-step chain
	Issuer    string   // full issuer URL string (BaseURL.IssuerURL — already a string)
	IssuedAt  Instant
	NotBefore Instant
	Expiry    Instant
	JWTID     string    // jti — random UUID
	Nonce     *Nonce    // present only when the cached request carried a nonce
	Azp       *ClientID // present only for authorization_code (non-overridable)
	Tenant    *string   // tid — seeded to issuerId, but user-overridable
	Scope     Scopes
	Custom    CustomClaims
}
