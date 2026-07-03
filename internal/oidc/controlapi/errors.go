package controlapi

import (
	"errors"

	"github.com/danielgtaylor/huma/v2"

	"github.com/meigma/mock-oidc/internal/oidc"
)

// toControlError translates a domain error into Huma's default
// application/problem+json (RFC 9457) — NEVER the OAuth2 error shape, which is
// reserved for the protocol surface (contract §6-B). A *oidc.ProtocolError carries
// its own closed ErrorCode + HTTPStatus (a reserved-issuer "_mock" surfaces here as
// the unified ParseIssuerID CodeNotFound/404 ProtocolError), so its status is
// mapped straight into a problem rather than matched per-condition; the remaining
// control-only sentinels map to 422, and anything unclassified is a 500.
func toControlError(err error) error {
	var perr *oidc.ProtocolError
	if errors.As(err, &perr) {
		return huma.NewError(perr.HTTPStatus, perr.Description, perr)
	}
	switch {
	case errors.Is(err, oidc.ErrUnsupportedAlgorithm):
		return huma.Error422UnprocessableEntity("unsupported signing algorithm", err)
	case errors.Is(err, oidc.ErrInvalidBaseURL), errors.Is(err, oidc.ErrInvalidScheme):
		return huma.Error422UnprocessableEntity("invalid issuer url", err)
	case errors.Is(err, oidc.ErrInvalidInstant), errors.Is(err, oidc.ErrInvalidDuration):
		return huma.Error422UnprocessableEntity("invalid time value", err)
	default:
		return huma.Error500InternalServerError("internal control error")
	}
}
