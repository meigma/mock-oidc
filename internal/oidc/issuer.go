// This file also records a named parity gap: issuer IDs are single-segment
// only. Upstream's suffix matcher accepts a multi-segment issuer (e.g.
// tenant/sub/default); ParseIssuerID rejects any value containing '/'. The
// /{issuer}/… form is equivalent-in-intent for the common single-segment case
// but cannot represent deeply-nested Azure-style issuers. This is a conscious,
// documented parity gap (Decision D-2), not a silent divergence.

package oidc

import (
	"fmt"
	"strconv"
	"strings"
)

// reservedPrefix is the path segment owned by the control plane and infra
// routes; an IssuerID may never begin with it (contract §8).
const reservedPrefix = "_mock"

// Default scheme ports, and the maximum TCP port, named to keep the port
// precedence readable (and to keep magic numbers out of the logic).
const (
	httpPort  = 80
	httpsPort = 443
	maxPort   = 65535
)

// IssuerID identifies a zero-config, on-demand issuer. It is the first path
// segment of every issuer-scoped URL and, by construction, also the kid of that
// issuer's signing key.
type IssuerID string

// ParseIssuerID parses a path segment into an IssuerID. It is on the request
// path, so it returns a typed *ProtocolError (which also wraps a distinguishable
// sentinel for [errors.Is]): empty -> invalid_request (400); containing '/' ->
// invalid_request (400, wrapping ErrInvalidIssuer); reserved-prefix collision ->
// not_found (404, wrapping ErrReservedIssuer). It is the single issuer
// constructor across every section.
//
// Named parity gap (Decision D-2): issuer IDs are SINGLE-SEGMENT only. Rejecting
// any value containing '/' means an Azure-style nested issuer (tenant/sub/default)
// cannot be represented — the '/{issuer}/…' form is equivalent-in-intent for the
// common single-segment case but not for deeply-nested issuers. This is a
// conscious, documented divergence, surfaced (400 invalid_request) rather than
// silently mishandled.
func ParseIssuerID(s string) (IssuerID, error) {
	switch {
	case s == "":
		return "", MissingParameter("issuer")
	case strings.ContainsRune(s, '/'):
		return "", &ProtocolError{
			Code:        CodeInvalidRequest,
			HTTPStatus:  statusBadRequest,
			Description: fmt.Sprintf("issuer %q must not contain '/'", s),
			cause:       ErrInvalidIssuer,
		}
	case s == reservedPrefix || strings.HasPrefix(s, reservedPrefix+"/"):
		return "", &ProtocolError{
			Code:        CodeNotFound,
			HTTPStatus:  statusNotFound,
			Description: fmt.Sprintf("%q collides with reserved prefix %q", s, reservedPrefix),
			cause:       ErrReservedIssuer,
		}
	}
	return IssuerID(s), nil
}

// KeyID returns the JWS kid for this issuer. The mock keys every issuer
// deterministically by its id (not a thumbprint), so kid == IssuerID always.
func (id IssuerID) KeyID() KeyID { return KeyID(id) }

// URLScheme is the externally-visible transport scheme an issuer advertises.
type URLScheme string

// The supported URL schemes.
const (
	SchemeHTTP  URLScheme = "http"
	SchemeHTTPS URLScheme = "https"
)

// ParseURLScheme is a config/edge-time parser: it returns a wrapped sentinel
// (errors.Is) rather than a *ProtocolError, since a bad scheme is a wiring fault.
func ParseURLScheme(s string) (URLScheme, error) {
	switch URLScheme(strings.ToLower(s)) {
	case SchemeHTTP:
		return SchemeHTTP, nil
	case SchemeHTTPS:
		return SchemeHTTPS, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrInvalidScheme, s)
	}
}

func (s URLScheme) defaultPort() int {
	if s == SchemeHTTPS {
		return httpsPort
	}
	return httpPort
}

// BaseURL is the scheme://host[:port] root an issuer advertises. It carries no
// path: per-issuer endpoint URLs are formed by joining the IssuerID at the host
// root. It holds typed scalar components and renders by string formatting so the
// core stays free of net/url (Decision D-1 / TDD §3).
type BaseURL struct {
	scheme URLScheme
	host   string // host only — no port, no path
	port   int    // 0 means "scheme default", omitted from String()
}

// NewBaseURL validates and constructs a BaseURL. host must be non-empty and port
// in range; a zero port means "scheme default".
func NewBaseURL(scheme URLScheme, host string, port int) (BaseURL, error) {
	if host == "" {
		return BaseURL{}, fmt.Errorf("%w: host must not be empty", ErrInvalidBaseURL)
	}
	if port < 0 || port > maxPort {
		return BaseURL{}, fmt.Errorf("%w: port %d out of range", ErrInvalidBaseURL, port)
	}
	return BaseURL{scheme: scheme, host: host, port: port}, nil
}

// String renders the host root, omitting the port when it is the scheme default.
func (b BaseURL) String() string {
	if b.port == 0 || b.port == b.scheme.defaultPort() {
		return fmt.Sprintf("%s://%s", b.scheme, b.host)
	}
	return fmt.Sprintf("%s://%s:%d", b.scheme, b.host, b.port)
}

// IssuerURL returns the externally-visible issuer URL (the iss claim value): the
// host root with the issuer segment joined at root, regardless of any deeper
// request path. It returns a plain string — callers must NOT call String() on
// the result.
func (b BaseURL) IssuerURL(id IssuerID) string {
	return b.String() + "/" + string(id)
}

// ParseBaseURL parses a full URL string into a host-root BaseURL — the mint
// `anyToken` override, where a test signs a token for an arbitrary external `iss`
// with this server's keys. It accepts scheme://host[:port] with an optional
// trailing path (dropped — BaseURL is host-root only) and parses with string ops
// only (net/url is banned in the core; IPv6 literals are the documented parity
// gap, not supported). A missing scheme, empty host, or non-numeric port is a
// wrapped ErrInvalidBaseURL / ErrInvalidScheme so the control edge maps it to a
// 422 rather than crashing.
func ParseBaseURL(raw string) (BaseURL, error) {
	schemePart, rest, found := strings.Cut(raw, "://")
	if !found {
		return BaseURL{}, fmt.Errorf("%w: %q is missing a scheme", ErrInvalidBaseURL, raw)
	}
	scheme, err := ParseURLScheme(schemePart)
	if err != nil {
		return BaseURL{}, err
	}
	authority := rest
	if i := strings.IndexByte(authority, '/'); i >= 0 {
		authority = authority[:i] // drop any path/query/fragment — host root only
	}
	host := authority
	port := 0
	if i := strings.LastIndexByte(authority, ':'); i >= 0 {
		p, convErr := strconv.Atoi(authority[i+1:])
		if convErr != nil {
			return BaseURL{}, fmt.Errorf("%w: port in %q: %w", ErrInvalidBaseURL, raw, convErr)
		}
		host = authority[:i]
		port = p
	}
	return NewBaseURL(scheme, host, port)
}

// RequestOrigin carries the already-extracted candidate address components for
// base-URL resolution. The transport edge (httpapi) parses Host / X-Forwarded-*
// with net/url and fills this; the domain only applies precedence.
type RequestOrigin struct {
	Scheme   URLScheme // original request scheme
	Host     string    // Host-header host, else original host
	Port     int       // Host-header port, else original port (0 = unset)
	FwdProto string    // X-Forwarded-Proto (raw)
	FwdHost  string    // X-Forwarded-Host (raw)
	FwdPort  string    // X-Forwarded-Port (raw)
}

// ResolveBaseURL applies upstream's proxy-aware precedence: scheme =
// X-Forwarded-Proto ?: original; host = X-Forwarded-Host ?: original; port =
// X-Forwarded-Port > X-Forwarded-Host port > Host port > scheme default. A real
// reverse proxy may forward X-Forwarded-Host with an embedded port (e.g.
// "idp.example.com:8443"); that port is split out so it participates in the
// precedence chain rather than being left embedded in the host (which would emit
// a double-port authority). It returns (BaseURL, error) because forwarded headers
// are client-controlled and malformable; callers MUST handle the error.
func ResolveBaseURL(o RequestOrigin) (BaseURL, error) {
	scheme := o.Scheme
	if o.FwdProto != "" {
		s, err := ParseURLScheme(o.FwdProto)
		if err != nil {
			return BaseURL{}, err
		}
		scheme = s
	}

	host := o.Host
	port := o.Port
	if o.FwdHost != "" {
		h, p, ok := splitHostPort(o.FwdHost)
		host = h
		if ok {
			// An embedded X-Forwarded-Host port outranks the inbound Host port
			// but is still overridable by an explicit X-Forwarded-Port below.
			port = p
		}
	}

	if o.FwdPort != "" {
		p, err := strconv.Atoi(o.FwdPort)
		if err != nil {
			return BaseURL{}, fmt.Errorf("%w: x-forwarded-port %q: %w", ErrInvalidBaseURL, o.FwdPort, err)
		}
		port = p
	}
	return NewBaseURL(scheme, host, port)
}

// splitHostPort separates a trailing :port from a host[:port] authority using
// string ops only (the core bans net; see ParseBaseURL). It reports ok=true only
// when a numeric :port suffix is present — a bare host, or a non-numeric suffix,
// is returned verbatim with ok=false so the caller keeps its existing port. IPv6
// literals are the documented parity gap (Decision D-2): a bracketless "::1"
// mis-splits exactly as ParseBaseURL already does.
func splitHostPort(authority string) (string, int, bool) {
	i := strings.LastIndexByte(authority, ':')
	if i < 0 {
		return authority, 0, false
	}
	port, err := strconv.Atoi(authority[i+1:])
	if err != nil {
		return authority, 0, false
	}
	return authority[:i], port, true
}

// Issuer is a materialized issuer: its identity, the base URL it advertises for
// this request, the public metadata of its (lazily generated) signing key, and
// any statically configured token callbacks. It is a value assembled by the
// issuerResolver, never parsed from the wire.
type Issuer struct {
	ID        IssuerID
	BaseURL   BaseURL
	Key       SigningKey
	Callbacks []TokenCallback
}

// NewIssuer assembles a materialized Issuer value from its resolved parts.
func NewIssuer(id IssuerID, base BaseURL, key SigningKey, callbacks []TokenCallback) Issuer {
	return Issuer{ID: id, BaseURL: base, Key: key, Callbacks: callbacks}
}
