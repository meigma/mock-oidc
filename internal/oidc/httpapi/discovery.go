package httpapi

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// DiscoveryInput is the issuer-scoped input for the two discovery paths.
type DiscoveryInput struct {
	Issuer string `path:"issuer" doc:"Issuer id; the first path segment."`
}

func (i *DiscoveryInput) issuerID() string { return i.Issuer }

// registerDiscovery mounts both the OIDC and the RFC 8414 discovery paths as two
// Huma operations sharing one handler and producing the identical body (Huma
// requires a unique method+path per operation).
func (h *handlers) registerDiscovery(api huma.API) {
	register := func(path, id string) {
		huma.Register(api, huma.Operation{
			OperationID: id,
			Method:      http.MethodGet,
			Path:        path,
			Summary:     "OIDC/OAuth2 provider metadata",
			Tags:        []string{tagOIDC},
		}, h.discovery)
		stampJSONResponse(api, path, http.MethodGet, schemaOf(api, DiscoveryDTO{}))
	}
	register("/{issuer}/.well-known/openid-configuration", "discovery-openid")
	register("/{issuer}/.well-known/oauth-authorization-server", "discovery-oauth")
}

// discovery parses the issuer, asks the provider service for the fixed-field-order
// document (proxy-aware base URL resolved inside the domain), and maps it to the
// DTO. Any failure returns the uniform OAuth2 envelope — never a Go error, so the
// protocol surface keeps one error contract.
func (h *handlers) discovery(ctx context.Context, in *DiscoveryInput) (*ProtocolJSON, error) {
	issuer, err := issuerOf(in)
	if err != nil {
		return protocolError(err), nil
	}
	doc, err := h.deps.Provider.Discovery(ctx, issuer, originFrom(ctx))
	if err != nil {
		return protocolError(err), nil
	}
	return &ProtocolJSON{Status: http.StatusOK, Body: toDiscoveryDTO(doc)}, nil
}
