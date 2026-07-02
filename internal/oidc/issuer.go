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
// X-Forwarded-Port > Host port > scheme default. It returns (BaseURL, error)
// because forwarded headers are client-controlled and malformable; callers MUST
// handle the error.
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
	if o.FwdHost != "" {
		host = o.FwdHost
	}

	port := o.Port
	if o.FwdPort != "" {
		p, err := strconv.Atoi(o.FwdPort)
		if err != nil {
			return BaseURL{}, fmt.Errorf("%w: x-forwarded-port %q: %w", ErrInvalidBaseURL, o.FwdPort, err)
		}
		port = p
	}
	return NewBaseURL(scheme, host, port)
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
