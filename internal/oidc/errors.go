package oidc

import "errors"

// HTTP status codes the protocol constructors stamp. net/http is banned in the
// core (it is an adapter concern), so the statuses are named here as plain ints.
const (
	statusBadRequest   = 400
	statusUnauthorized = 401
	statusNotFound     = 404
)

// ErrorCode is the closed set of OAuth2/OIDC error codes the domain emits.
type ErrorCode string

// The OAuth2/OIDC error codes the domain raises. They are the client-visible
// `error` field values; the adapter renders them verbatim (correct case).
const (
	CodeInvalidRequest       ErrorCode = "invalid_request"
	CodeInvalidGrant         ErrorCode = "invalid_grant"
	CodeInvalidClient        ErrorCode = "invalid_client"
	CodeInvalidToken         ErrorCode = "invalid_token"
	CodeUnsupportedTokenType ErrorCode = "unsupported_token_type"
	CodeUnsupportedGrantType ErrorCode = "unsupported_grant_type"
	CodeServerError          ErrorCode = "server_error"
	CodeNotFound             ErrorCode = "not_found"
)

// ProtocolError is the typed OAuth2 protocol error every protocol service
// raises. Description is the client-visible text (correct case — upstream's
// full-body lowercasing is a defect this project does NOT replicate). HTTPStatus
// is explicit because the same code maps to different statuses depending on
// context (e.g. invalid_client is 400 at /introspect but 401 elsewhere); the
// constructor that knows the context sets it. cause is an optional wrapped
// sentinel so [errors.Is] (sentinel) and [errors.As] (*ProtocolError) both
// succeed on the same value.
type ProtocolError struct {
	Code        ErrorCode
	Description string
	HTTPStatus  int
	cause       error
}

// NewProtocolError is the generic constructor; the named constructors below are
// the preferred, intent-revealing entry points.
func NewProtocolError(code ErrorCode, desc string, status int) *ProtocolError {
	return &ProtocolError{Code: code, Description: desc, HTTPStatus: status, cause: nil}
}

// NewInvalidToken reports a token-verification failure as invalid_token (401),
// wrapping the underlying verifier cause so [errors.Is]/[errors.As] can reach it
// while the client-visible description stays a stable, non-leaking phrase. It is
// the /userinfo verify-fail constructor; the /introspect path reports
// {active:false} instead of raising this.
func NewInvalidToken(cause error) *ProtocolError {
	return &ProtocolError{
		Code:        CodeInvalidToken,
		Description: "the access token is invalid",
		HTTPStatus:  statusUnauthorized,
		cause:       cause,
	}
}

// Error renders the code and description, satisfying the error interface.
func (e *ProtocolError) Error() string { return string(e.Code) + ": " + e.Description }

// Unwrap exposes the wrapped parse sentinel (if any) for [errors.Is].
func (e *ProtocolError) Unwrap() error { return e.cause }

// Parse sentinels for [errors.Is] on parse failures. ErrInvalidIssuer and
// ErrReservedIssuer are wrapped as the cause of the *ProtocolError ParseIssuerID
// returns, so a caller can both [errors.As] (&ProtocolError) and
// [errors.Is] (ErrReservedIssuer).
var (
	ErrInvalidIssuer        = errors.New("invalid issuer id")
	ErrReservedIssuer       = errors.New("issuer collides with reserved control prefix")
	ErrInvalidBaseURL       = errors.New("invalid base url")
	ErrInvalidScheme        = errors.New("invalid url scheme")
	ErrUnsupportedGrantType = errors.New("unsupported grant_type")
	//nolint:staticcheck // ST1005: exact upstream wording "Unsupported algorithm" is asserted by parity tests.
	ErrUnsupportedAlgorithm = errors.New("Unsupported algorithm")
)

// MissingParameter reports a required protocol parameter as invalid_request
// "missing required parameter <name>" (400).
func MissingParameter(name string) *ProtocolError {
	return &ProtocolError{
		Code:        CodeInvalidRequest,
		Description: "missing required parameter " + name,
		HTTPStatus:  statusBadRequest,
		cause:       nil,
	}
}

// MalformedRequest reports structurally-bad protocol input as invalid_request
// <desc> (400) — the general-purpose 400 that is not a "missing parameter".
func MalformedRequest(desc string) *ProtocolError {
	return &ProtocolError{
		Code:        CodeInvalidRequest,
		Description: desc,
		HTTPStatus:  statusBadRequest,
		cause:       nil,
	}
}

// MissingClientAuthentication reports a token-exchange request that carried no
// client authentication at all as invalid_request (400) with upstream's exact
// text. Token-exchange is the only grant that requires some form of client
// authentication (catalog line 109); a public client_id alone does not satisfy it.
func MissingClientAuthentication() *ProtocolError {
	return MalformedRequest("request must contain some form of ClientAuthentication.")
}

// InvalidGrant is the base for every invalid_grant case (400); the specific
// throwers below delegate to it so their text lives in exactly one place.
func InvalidGrant(desc string) *ProtocolError {
	return &ProtocolError{
		Code:        CodeInvalidGrant,
		Description: desc,
		HTTPStatus:  statusBadRequest,
		cause:       nil,
	}
}

// UnsupportedGrant reports an unknown grant as invalid_grant
// "grant_type <x> not supported." Upstream reports it as invalid_grant (NOT
// unsupported_grant_type); this preserves that exact code and text and wraps
// ErrUnsupportedGrantType so a sentinel check still works.
func UnsupportedGrant(g string) *ProtocolError {
	e := InvalidGrant("grant_type " + g + " not supported.")
	e.cause = ErrUnsupportedGrantType
	return e
}

// UnknownAuthorizationCode reports a code cache miss as invalid_grant
// "unknown or already-used authorization code".
func UnknownAuthorizationCode() *ProtocolError {
	return InvalidGrant("unknown or already-used authorization code")
}

// PKCEFailed reports a failed PKCE check as invalid_grant with upstream's exact
// invalid_pkce description.
func PKCEFailed() *ProtocolError {
	return InvalidGrant("invalid_pkce: code_verifier does not compute to code_challenge from request")
}

// RefreshCrossIssuer reports a refresh token minted by another issuer. The text
// is the CORRECTED client-visible description, not the internal
// "refresh_token issuer mismatch" message (catalog correction).
func RefreshCrossIssuer() *ProtocolError {
	return InvalidGrant("refresh_token was issued by a different issuer")
}

// UnknownRefreshToken reports an unknown refresh token as invalid_grant.
func UnknownRefreshToken() *ProtocolError {
	return InvalidGrant("unknown refresh_token")
}

// InvalidClient reports a client-authentication failure as invalid_client (401).
func InvalidClient(desc string) *ProtocolError {
	return &ProtocolError{
		Code:        CodeInvalidClient,
		Description: desc,
		HTTPStatus:  statusUnauthorized,
		cause:       nil,
	}
}

// InvalidClientStatus reports invalid_client at an explicit status. The
// /introspect call site uses InvalidClientStatus(400, "...") for upstream's
// presence-only auth, where a missing/empty Authorization header yields 400.
func InvalidClientStatus(status int, desc string) *ProtocolError {
	return &ProtocolError{
		Code:        CodeInvalidClient,
		Description: desc,
		HTTPStatus:  status,
		cause:       nil,
	}
}

// InvalidToken reports a verification failure as invalid_token (401) — the
// /userinfo and /introspect verify path.
func InvalidToken(desc string) *ProtocolError {
	return &ProtocolError{
		Code:        CodeInvalidToken,
		Description: desc,
		HTTPStatus:  statusUnauthorized,
		cause:       nil,
	}
}

// UnsupportedTokenType reports a bad revoke hint as
// "unsupported token type: <hint>" (400). It takes the typed TokenTypeHint so the
// SessionService can hand its parsed hint straight through.
func UnsupportedTokenType(hint TokenTypeHint) *ProtocolError {
	return &ProtocolError{
		Code:        CodeUnsupportedTokenType,
		Description: "unsupported token type: " + string(hint),
		HTTPStatus:  statusBadRequest,
		cause:       nil,
	}
}

// NotFound reports not_found "Resource not found" (404).
func NotFound() *ProtocolError {
	return &ProtocolError{
		Code:        CodeNotFound,
		Description: "Resource not found",
		HTTPStatus:  statusNotFound,
		cause:       nil,
	}
}
