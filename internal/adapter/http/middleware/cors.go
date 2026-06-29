// Package middleware holds the HTTP transport middleware composed by the router:
// client-IP resolution, panic recovery, CORS, and per-request timeouts. Each
// returns a func([http.Handler]) [http.Handler] so the router can order them.
package middleware

import (
	"net/http"

	"github.com/go-chi/cors"
)

// corsMaxAgeSeconds is how long browsers may cache a CORS preflight response.
const corsMaxAgeSeconds = 300

// CORS returns CORS middleware restricted to origins. The router installs it only
// when origins is non-empty, so the server emits no CORS headers by default.
// Methods, headers, and credentials use conservative defaults suited to a JSON
// API; widen them per deployment as needed.
func CORS(origins []string) func(http.Handler) http.Handler {
	return cors.Handler(cors.Options{
		AllowedOrigins: origins,
		AllowedMethods: []string{
			http.MethodGet, http.MethodPost, http.MethodPut,
			http.MethodPatch, http.MethodDelete, http.MethodOptions,
		},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type"},
		AllowCredentials: false,
		MaxAge:           corsMaxAgeSeconds,
	})
}
