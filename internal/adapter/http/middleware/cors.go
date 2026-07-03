// Package middleware holds the HTTP transport middleware composed by the router:
// client-IP resolution, panic recovery, CORS, and per-request timeouts. Each
// returns a func([http.Handler]) [http.Handler] so the router can order them.
package middleware

import (
	"net/http"
	"strconv"
)

// corsMaxAgeSeconds is how long browsers may cache a CORS preflight response.
const corsMaxAgeSeconds = 300

// corsAllowMethods is the method set advertised on a CORS preflight. The OIDC
// protocol surface serves only these verbs (Decision D-3).
const corsAllowMethods = "POST, GET, OPTIONS"

// CORS returns reflect-origin CORS middleware (Decision D-3, default ON). With an
// empty allowlist it reflects any request Origin back in
// Access-Control-Allow-Origin and sets Access-Control-Allow-Credentials: true, so
// a browser-based client works with zero configuration; because credentialled
// CORS forbids the "*" wildcard, the origin is always reflected verbatim, never
// starred. A non-empty allowlist tightens the reflection to exactly those
// origins. A preflight — ANY OPTIONS request, on ANY path — is answered 204 with
// the fixed method set and the echoed Access-Control-Request-Headers; upstream's
// CorsInterceptor treats the bare method as the preflight signal (a real browser
// preflight also carries Access-Control-Request-Method, but the 204 does not
// depend on it). Other requests fall through to the handler with the CORS
// response headers attached.
func CORS(allowedOrigins []string) func(http.Handler) http.Handler {
	allow := make(map[string]struct{}, len(allowedOrigins))
	for _, o := range allowedOrigins {
		allow[o] = struct{}{}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			preflight := r.Method == http.MethodOptions

			if origin == "" || !originAllowed(origin, allow) {
				// No Origin, or an origin outside the allowlist: emit no CORS
				// headers. A disallowed preflight still short-circuits so it is
				// not routed as a real request.
				if preflight {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				next.ServeHTTP(w, r)
				return
			}

			h := w.Header()
			h.Add("Vary", "Origin")
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Access-Control-Allow-Credentials", "true")

			if !preflight {
				next.ServeHTTP(w, r)
				return
			}

			h.Add("Vary", "Access-Control-Request-Method")
			h.Add("Vary", "Access-Control-Request-Headers")
			h.Set("Access-Control-Allow-Methods", corsAllowMethods)
			if reqHeaders := r.Header.Get("Access-Control-Request-Headers"); reqHeaders != "" {
				h.Set("Access-Control-Allow-Headers", reqHeaders)
			}
			h.Set("Access-Control-Max-Age", strconv.Itoa(corsMaxAgeSeconds))
			w.WriteHeader(http.StatusNoContent)
		})
	}
}

// originAllowed reports whether origin may receive CORS headers. An empty
// allowlist means default-ON reflect-any (Decision D-3); a non-empty allowlist
// admits only its exact members.
func originAllowed(origin string, allow map[string]struct{}) bool {
	if len(allow) == 0 {
		return true
	}
	_, ok := allow[origin]
	return ok
}
