// Package http assembles the generic, resource-agnostic HTTP transport: the chi
// router and middleware, the infrastructure routes (/healthz, /readyz, /metrics),
// the Huma API, and server-less OpenAPI export. Resource operations are mounted by
// their own adapter packages (for example, internal/todo/httpapi) through
// the Registrar seam.
package http

import (
	"fmt"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
)

// apiTitle is the OpenAPI document title for this service.
const apiTitle = "template-go-api"

// Registrar mounts resource operations onto a Huma API. Each resource's HTTP
// adapter package provides one, and the composition root composes them.
type Registrar func(huma.API)

// NewAPI wraps mux with Huma and returns the API. It registers no operations;
// callers register resource handlers onto the returned API via a Registrar.
func NewAPI(mux chi.Router, version string) huma.API {
	return humachi.New(mux, huma.DefaultConfig(apiTitle, version))
}

// SpecYAML builds the API on a throwaway router, applies register, and returns the
// OpenAPI 3.0.3 specification as YAML, without binding a network listener.
//
// finalize, when non-nil, runs after the operations are registered and before
// the document is serialized. The composition root passes the authz hook here so
// the server-less export carries the same security scheme and per-operation
// requirements the running server installs — keeping the committed spec in step
// with the enforced protection. It is nil when authorization is disabled.
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
