package http

import (
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
)

// ClientIPKeyFunc keys the rate limiter by the resolved client IP. It reads the
// IP the ClientIP middleware stored on the request context — spoof-safe, since
// that middleware honors --trusted-proxy-header and otherwise trusts only the
// TCP peer — so the limiter and the access log agree on who the client is.
//
// It has the signature of ratelimit.KeyFunc and is the default key for the
// rate-limit middleware. To limit authenticated callers instead, swap in a key
// function that reads the principal (see internal/authz) from the context;
// keying lives here, in the transport, so the limiter core stays
// router-agnostic. It never errors: an unresolved IP yields the empty key, which
// simply shares one bucket rather than failing the request.
func ClientIPKeyFunc(ctx huma.Context) (string, error) {
	r, _ := humachi.Unwrap(ctx)

	return chimiddleware.GetClientIP(r.Context()), nil
}
