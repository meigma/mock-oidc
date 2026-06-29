package middleware

import (
	"net/http"

	chimiddleware "github.com/go-chi/chi/v5/middleware"
)

// ClientIP resolves the client IP and stores it on the request context for chi's
// [chimiddleware.GetClientIP]. When trustedProxyHeader is set it reads that
// proxy-overwritten header (for example X-Real-IP); otherwise it trusts only the
// direct TCP peer, which cannot be spoofed. It never reads X-Forwarded-For
// implicitly, and avoids the deprecated, spoofable [chimiddleware.RealIP].
func ClientIP(trustedProxyHeader string) func(http.Handler) http.Handler {
	if trustedProxyHeader != "" {
		return chimiddleware.ClientIPFromHeader(trustedProxyHeader)
	}

	return chimiddleware.ClientIPFromRemoteAddr
}
