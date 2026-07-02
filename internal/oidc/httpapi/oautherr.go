package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/meigma/mock-oidc/internal/oidc"
)

// oauth2Error maps a domain error to an HTTP status + RFC 6749 §5.2 body. A
// *oidc.ProtocolError carries the mapped status and the exact client-visible
// description the tests pin (correct case). Any other error is an internal fault
// → server_error / 500, never leaking Go error text to the client.
func oauth2Error(err error) (int, OAuth2Error) {
	var perr *oidc.ProtocolError
	if errors.As(err, &perr) {
		return perr.HTTPStatus, OAuth2Error{
			Code:        string(perr.Code),
			Description: perr.Description,
		}
	}
	return http.StatusInternalServerError, OAuth2Error{
		Code:        string(oidc.CodeServerError),
		Description: "unexpected error",
	}
}

// protocolError builds the success-shaped error envelope (§5.4) from a domain
// error. It is the single edge adapter for ANY error on a protocol JSON route,
// so the protocol surface has exactly one error contract. invalid_token also
// carries the RFC 6750 WWW-Authenticate challenge.
func protocolError(err error) *ProtocolJSON {
	status, body := oauth2Error(err)
	out := &ProtocolJSON{Status: status, Body: body}
	if body.Code == string(oidc.CodeInvalidToken) {
		out.WWWAuth = `Bearer error="invalid_token"`
	}
	return out
}

// WriteOAuth2Error writes an RFC 6749 §5.2 error body directly to w. It is
// exported so the composition root can install it (paired with IsProtocolPath) as
// the generic router's protocol-family fallback for non-Huma errors (for example,
// the 405 on GET /{issuer}/token), keeping internal/adapter/http free of any OIDC
// import.
func WriteOAuth2Error(w http.ResponseWriter, status int, code, desc string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(OAuth2Error{Code: code, Description: desc})
}

// IsProtocolPath reports whether p is an OIDC protocol-family route
// (/{issuer}/<known-endpoint>) — i.e. NOT a control (/_mock/*) or infra
// (/healthz, /metrics, …) route. The composition root uses it to decide whether a
// transport-level fallback should render the OAuth2 shape (protocol) or RFC 9457
// (control/infra).
func IsProtocolPath(p string) bool {
	// Issuer-scoped endpoint path suffixes that mark a protocol-family route.
	protocolSuffixes := []string{
		"/.well-known/openid-configuration",
		"/.well-known/oauth-authorization-server",
		"/jwks",
		"/authorize",
		"/token",
		"/userinfo",
		"/introspect",
		"/revoke",
		"/endsession",
	}

	if p == "" || p == "/" {
		return false
	}
	if p == "/_mock" || strings.HasPrefix(p, "/_mock/") {
		return false
	}
	// Must be issuer-scoped: /{issuer}/<endpoint> — a non-empty first segment
	// followed by at least one more.
	first, _, ok := strings.Cut(strings.TrimPrefix(p, "/"), "/")
	if !ok || first == "" {
		return false
	}
	for _, suffix := range protocolSuffixes {
		if strings.HasSuffix(p, suffix) && len(p) > len(suffix) {
			return true
		}
	}
	return false
}
