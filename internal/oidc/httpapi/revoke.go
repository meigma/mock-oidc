package httpapi

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/meigma/mock-oidc/internal/oidc"
)

// RevokeInput is the POST /{issuer}/revoke input: the issuer path segment and the
// raw url-encoded body carrying token/token_type_hint (parsed by the adapter).
type RevokeInput struct {
	Issuer  string `path:"issuer"`
	RawBody []byte `contentType:"application/x-www-form-urlencoded"`
}

func (i *RevokeInput) issuerID() string { return i.Issuer }

// registerRevoke mounts POST /{issuer}/revoke and documents its url-encoded
// request fields.
func (h *handlers) registerRevoke(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "revoke",
		Method:      http.MethodPost,
		Path:        "/{issuer}/revoke",
		Summary:     "OAuth2 token revocation endpoint",
		Tags:        []string{tagOAuth},
	}, h.revoke)
	stampFormSchema(api, "/{issuer}/revoke", tokenHintFormSchema())
}

// revoke flat-parses token/token_type_hint and removes the refresh token. Only
// token_type_hint=refresh_token is accepted; any other hint is
// unsupported_token_type (400). Removing an unknown token is a no-op, so revoke
// is idempotent and success is a bare 200. It NEVER returns a Go error; every
// failure routes through protocolError.
func (h *handlers) revoke(ctx context.Context, in *RevokeInput) (*ProtocolJSON, error) {
	issuer, err := issuerOf(in)
	if err != nil {
		return protocolError(err), nil
	}
	form, err := parseFormFlat(in.RawBody)
	if err != nil {
		//nolint:nilerr // a form-parse failure is surfaced as an OAuth2 error body, never a Go error.
		return protocolError(oidc.MalformedRequest("could not parse form body")), nil
	}
	req := oidc.RevocationRequest{
		Issuer: issuer,
		Token:  oidc.RefreshToken(form.Get("token")),
		Hint:   oidc.TokenTypeHint(form.Get("token_type_hint")),
	}
	if revokeErr := h.deps.Session.Revoke(ctx, req); revokeErr != nil {
		return protocolError(revokeErr), nil
	}
	return &ProtocolJSON{Status: http.StatusOK, Body: struct{}{}}, nil
}
