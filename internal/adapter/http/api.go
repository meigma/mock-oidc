// Package http assembles the generic, resource-agnostic HTTP transport: the chi
// router and middleware, the infrastructure routes (/isalive, /healthz, /readyz,
// /metrics), the Huma API, and server-less OpenAPI export. Resource operations
// are mounted by their own adapter packages (for example, internal/oidc/httpapi)
// through the Registrar seam, keeping this package free of any internal/oidc
// import.
package http

import (
	"fmt"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
)

// apiTitle is the OpenAPI document title for this service.
const apiTitle = "mock-oidc"

// Registrar mounts resource operations onto a Huma API. Each resource's HTTP
// adapter package provides one, and the composition root composes them.
type Registrar func(huma.API)

// NewAPI wraps mux with Huma and returns the API. It registers no operations;
// callers register resource handlers onto the returned API via a Registrar.
//
// It strips Huma's SchemaLinkTransformer from the default config (Decision D-5):
// that transformer's create hook injects a $schema field (at field index 0) and
// a Link: rel="describedBy" response header into every concrete-struct JSON body.
// On the OIDC protocol surface that would break the discovery/JWKS fixed
// field-order invariant and inject non-standard fields that strict third-party
// OIDC clients reject. Clearing the create hook (and the transformer slots it
// populates) keeps the protocol JSON clean.
func NewAPI(mux chi.Router, version string) huma.API {
	cfg := huma.DefaultConfig(apiTitle, version)
	cfg.CreateHooks = nil
	cfg.Transformers = nil
	cfg.OpenAPI.OnAddOperation = nil

	return humachi.New(mux, cfg)
}

// SpecYAML builds the API on a throwaway router, applies register, and returns the
// OpenAPI 3.0.3 specification as YAML, without binding a network listener.
//
// finalize, when non-nil, runs after the operations are registered and before
// the document is serialized. It is the post-register stamping seam: the
// composition root passes a hook that declares protocol security schemes (for
// example, oauth2/openIdConnect) on the document, so the server-less export
// matches the running server. It is nil when nothing stamps the spec.
func SpecYAML(version string, register Registrar, finalize func(huma.API)) ([]byte, error) {
	api := NewAPI(chi.NewMux(), version)
	if register != nil {
		register(api)
	}
	if finalize != nil {
		finalize(api)
	}

	spec, err := api.OpenAPI().DowngradeYAML()
	if err != nil {
		return nil, fmt.Errorf("downgrade openapi spec to yaml: %w", err)
	}

	return spec, nil
}
