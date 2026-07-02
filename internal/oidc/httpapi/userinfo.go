package httpapi

import (
	"context"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/meigma/mock-oidc/internal/oidc"
)

// UserInfoInput is the GET /{issuer}/userinfo input: the issuer path segment and
// the Authorization header carrying the bearer access token. The token is parsed
// presence-only at the edge (the "Bearer " prefix is stripped when present); no
// validation happens here — the SessionService verifies it.
type UserInfoInput struct {
	Issuer        string `path:"issuer"`
	Authorization string `header:"Authorization"`
}

func (i *UserInfoInput) issuerID() string { return i.Issuer }

// registerUserinfo mounts GET /{issuer}/userinfo. The response body is the entire
// verified claim set (a dynamic object), so no fixed response schema is stamped.
func (h *handlers) registerUserinfo(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "userinfo",
		Method:      http.MethodGet,
		Path:        "/{issuer}/userinfo",
		Summary:     "OpenID Connect UserInfo endpoint",
		Tags:        []string{tagOIDC},
	}, h.userinfo)
}

// userinfo verifies the bearer access token and returns its ENTIRE claim set
// verbatim as JSON. A verification failure is invalid_token (401) with the RFC
// 6750 WWW-Authenticate challenge, emitted through the shared protocolError
// envelope. It NEVER returns a Go error — every failure routes through
// protocolError.
func (h *handlers) userinfo(ctx context.Context, in *UserInfoInput) (*ProtocolJSON, error) {
	issuer, err := issuerOf(in)
	if err != nil {
		return protocolError(err), nil
	}
	req := oidc.UserInfoRequest{Issuer: issuer, Token: bearerToken(in.Authorization)}
	claims, err := h.deps.Session.UserInfo(ctx, req)
	if err != nil {
		return protocolError(err), nil
	}
	return &ProtocolJSON{Status: http.StatusOK, Body: toUserInfoBody(claims)}, nil
}

// bearerToken extracts the access token from an Authorization header. It is
// presence-only: a "Bearer " prefix is stripped (case-insensitive) when present,
// otherwise the trimmed header value is used as-is. An absent header yields an
// empty token, which the verifier rejects as invalid_token.
func bearerToken(authz string) oidc.SignedToken {
	const prefix = "Bearer "
	if len(authz) >= len(prefix) && strings.EqualFold(authz[:len(prefix)], prefix) {
		return oidc.SignedToken(strings.TrimSpace(authz[len(prefix):]))
	}
	return oidc.SignedToken(strings.TrimSpace(authz))
}
