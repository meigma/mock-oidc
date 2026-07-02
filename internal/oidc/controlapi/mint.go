package controlapi

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// registerMint mounts POST /_mock/mint.
func (h *handlers) registerMint(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "mock-mint",
		Method:      http.MethodPost,
		Path:        "/mint",
		Summary:     "Directly mint a token (issueToken / anyToken)",
		Description: "Signs a token without a grant flow, using the SAME signer/keys as /{issuer}/token, " +
			"so the result verifies against /{issuer}/jwks. Supply issuerUrl to sign for an arbitrary external iss.",
		Tags: []string{tagMockControl},
	}, h.mint)
}

// mint issues a token directly from the request body. The mint drives
// TokenService.Mint — the same application service /token uses — so the artifact is
// byte-identical to a granted one.
func (h *handlers) mint(ctx context.Context, in *MintTokenInput) (*MintTokenOutput, error) {
	spec, err := toMintSpec(in)
	if err != nil {
		return nil, toControlError(err)
	}
	signed, claims, err := h.deps.Tokens.Mint(ctx, spec)
	if err != nil {
		return nil, toControlError(err)
	}

	out := &MintTokenOutput{}
	out.Body.Token = string(signed)
	out.Body.Kid = string(spec.Issuer) // kid == issuer id
	out.Body.Algorithm = string(spec.Algorithm)
	out.Body.Issuer = spec.BaseURL.IssuerURL(spec.Issuer)
	out.Body.ExpiresAt = claims.Expiry.Time()
	out.Body.Claims = orderedClaimsMap(claims)
	return out, nil
}
