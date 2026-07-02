package controlapi

import (
	"encoding/base64"
	"net"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/meigma/mock-oidc/internal/oidc"
)

// defaultMintExpiry is the fallback token lifetime when a mint/scenario body omits
// expirySeconds (upstream's 3600s default).
const defaultMintExpiry = 3600 * time.Second

// toMintSpec is the mint anti-corruption boundary: it parse-don't-validates the
// wire DTO into typed domain values. ParseIssuerID rejects the reserved "_mock";
// the iss BaseURL is either the explicit anyToken override (ParseBaseURL) or the
// proxy-aware resolution of the control request's own headers; the lone
// map[string]any claim payload crosses into a typed ClaimSet here. A blank subject
// defaults to a random UUID.
func toMintSpec(in *MintTokenInput) (oidc.MintSpec, error) {
	b := in.Body
	issuer, err := oidc.ParseIssuerID(b.Issuer)
	if err != nil {
		return oidc.MintSpec{}, err
	}
	base, err := resolveMintBaseURL(in)
	if err != nil {
		return oidc.MintSpec{}, err
	}
	claims, err := oidc.NewClaimSet(b.Claims)
	if err != nil {
		return oidc.MintSpec{}, err
	}

	subject := oidc.Subject(b.Subject)
	if subject == "" {
		subject = oidc.Subject(uuid.NewString())
	}
	kind := oidc.MintKind(b.Kind)
	if kind == "" {
		kind = oidc.MintAccessToken
	}
	expiry := defaultMintExpiry
	if b.ExpirySec != nil {
		expiry = time.Duration(*b.ExpirySec) * time.Second
	}

	return oidc.MintSpec{
		Issuer:    issuer,
		BaseURL:   base,
		Subject:   subject,
		Audience:  oidc.Audience(b.Audience), // nil when omitted (unset)
		Scopes:    oidc.ParseScopes(strings.Join(b.Scope, " ")),
		Claims:    claims,
		ClientID:  oidc.ClientID(b.ClientID),
		Kind:      kind,
		Typ:       oidc.ParseJOSEType(b.Typ),
		Algorithm: oidc.DefaultSigningAlgorithm, // echoed in the response; Mint signs with the key's own alg
		Expiry:    expiry,
	}, nil
}

// resolveMintBaseURL yields the iss base: the explicit issuerUrl override (the
// anyToken case, host-root parsed) or the proxy-aware resolution of the control
// request's Host / X-Forwarded-* (correct for the co-located Testcontainers case,
// where the request Host IS the externally-visible address). in.Host is the request's
// own host, backfilled by MintTokenInput.Resolve (Go hides it from header binding),
// so a bare mint with no override still resolves an iss; precedence stays issuerUrl >
// X-Forwarded-Host > request Host.
func resolveMintBaseURL(in *MintTokenInput) (oidc.BaseURL, error) {
	if in.Body.IssuerURL != "" {
		return oidc.ParseBaseURL(in.Body.IssuerURL)
	}
	host, port := splitHostPort(in.Host)
	return oidc.ResolveBaseURL(oidc.RequestOrigin{
		Scheme:   oidc.SchemeHTTP, // the control edge sees no TLS terminator; X-Forwarded-Proto overrides
		Host:     host,
		Port:     port,
		FwdProto: in.FwdProto,
		FwdHost:  in.FwdHost,
		FwdPort:  in.FwdPort,
	})
}

// splitHostPort splits an authority (host[:port]) into host and numeric port,
// returning port 0 when none is present or it is unparseable.
func splitHostPort(hostport string) (string, int) {
	if hostport == "" {
		return "", 0
	}
	host, portStr, err := net.SplitHostPort(hostport)
	if err != nil {
		return hostport, 0
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return host, 0
	}
	return host, port
}

// orderedClaimsMap ranges the claim set's ordered accessor into the mint response's
// convenience map. There is no map-returning domain method (a Go map cannot
// preserve insertion order and encoding/json re-sorts keys); the authoritative
// ordered emission lives in the signed token.
func orderedClaimsMap(c oidc.ClaimSet) map[string]any {
	entries := c.Ordered()
	m := make(map[string]any, len(entries))
	for _, e := range entries {
		m[e.Name] = e.Value
	}
	return m
}

// toTokenCallback maps a declarative scenario/config description onto the SAME
// domain callback constructors the config `tokenCallbacks` parser calls, so there
// is one anti-corruption path for "a callback described as JSON" whether it arrives
// at startup (config) or at runtime (control). requestMappings present ->
// RequestMappingCallback; otherwise a DefaultTokenCallback carrying the scenario's
// subject/audience/typ/claims/expiry. Both constructors live in internal/oidc, so
// the config edge reuses them without importing this adapter.
func toTokenCallback(dto ScenarioDTO) (oidc.TokenCallback, error) {
	issuer, err := oidc.ParseIssuerID(dto.Issuer)
	if err != nil {
		return nil, err
	}
	var expiry time.Duration
	if dto.ExpirySeconds != nil {
		expiry = time.Duration(*dto.ExpirySeconds) * time.Second
	}

	if len(dto.RequestMappings) > 0 {
		mappings, mErr := toRequestMappings(dto.RequestMappings)
		if mErr != nil {
			return nil, mErr
		}
		return oidc.NewRequestMappingCallback(issuer, expiry, mappings)
	}

	claims, err := oidc.NewClaimSet(dto.Claims)
	if err != nil {
		return nil, err
	}
	return oidc.NewDefaultTokenCallbackWith(
		issuer,
		oidc.Subject(dto.Subject),
		oidc.Audience(dto.Audience),
		oidc.JOSEType(dto.Typ), // "" stays unset -> TypeHeader defaults to JWT
		claims.Custom,
		expiry,
	), nil
}

// toRequestMappings converts the wire mapping rules into domain RequestMappings,
// parsing each rule's templated claim body into typed CustomClaims.
func toRequestMappings(dtos []RequestMappingDTO) ([]oidc.RequestMapping, error) {
	out := make([]oidc.RequestMapping, 0, len(dtos))
	for _, d := range dtos {
		claims, err := oidc.NewClaimSet(d.Claims)
		if err != nil {
			return nil, err
		}
		out = append(out, oidc.RequestMapping{
			Param:      d.Param,
			Match:      d.Match,
			TypeHeader: oidc.JOSEType(d.TypeHeader),
			Claims:     claims.Custom,
		})
	}
	return out, nil
}

// scenarioKind reports whether a pending scenario's callback is the default or a
// request-mapping shape, for the GET /_mock/scenarios summary.
func scenarioKind(s oidc.Scenario) string {
	switch s.Callback.(type) {
	case oidc.RequestMappingCallback:
		return "requestMapping"
	default:
		return "default"
	}
}

// toCaptureFilter builds the domain filter from the take/list query dimensions; an
// empty field is a wildcard for that dimension.
func toCaptureFilter(issuer, endpoint string) oidc.CaptureFilter {
	return oidc.CaptureFilter{Issuer: oidc.IssuerID(issuer), Endpoint: endpoint}
}

// toCapturedRequestDTO renders a captured request for the wire, preserving the raw
// bytes as base64 and adding a best-effort UTF-8 decode for convenience.
func toCapturedRequestDTO(r oidc.CapturedRequest) CapturedRequestDTO {
	dto := CapturedRequestDTO{
		ID:         r.ID,
		ReceivedAt: r.ReceivedAt.Time(),
		Issuer:     string(r.Issuer),
		Method:     r.Method,
		Path:       r.Path,
		URL:        r.URL,
		Query:      r.Query,
		Headers:    r.Header,
	}
	if len(r.Body) > 0 {
		dto.BodyBase64 = base64.StdEncoding.EncodeToString(r.Body)
		if utf8.Valid(r.Body) {
			dto.Body = string(r.Body)
		}
	}
	return dto
}
