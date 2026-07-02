package httpapi

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// JWKSInput is the issuer-scoped input for the JWK set endpoint.
type JWKSInput struct {
	Issuer string `path:"issuer" doc:"Issuer id; the first path segment."`
}

func (i *JWKSInput) issuerID() string { return i.Issuer }

// registerJWKS mounts GET /{issuer}/jwks.
func (h *handlers) registerJWKS(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "jwks",
		Method:      http.MethodGet,
		Path:        "/{issuer}/jwks",
		Summary:     "JSON Web Key Set",
		Tags:        []string{tagOIDC},
	}, h.jwks)
	stampJSONResponse(api, "/{issuer}/jwks", http.MethodGet, schemaOf(api, JWKSDTO{}))
}

// jwks parses the issuer and returns its public JWK set. Requesting /jwks forces
// key materialization for that issuer (kid == issuer), so the set is never empty.
func (h *handlers) jwks(ctx context.Context, in *JWKSInput) (*ProtocolJSON, error) {
	issuer, err := issuerOf(in)
	if err != nil {
		return protocolError(err), nil
	}
	set, err := h.deps.Provider.JWKS(ctx, issuer)
	if err != nil {
		return protocolError(err), nil
	}
	return &ProtocolJSON{Status: http.StatusOK, Body: toJWKSDTO(set)}, nil
}
