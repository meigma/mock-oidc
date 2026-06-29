package ratelimit

import (
	"log/slog"
	"math"
	"net/http"
	"strconv"

	"github.com/danielgtaylor/huma/v2"
)

// genericTooManyRequests is the client-facing detail for a throttled request.
// The limited key is logged, not returned, so the response leaks nothing about
// other clients' traffic.
const genericTooManyRequests = "rate limit exceeded; retry later"

// KeyFunc extracts the client identity a request is limited by — by default the
// resolved client IP. Returning an error makes the middleware fail open (the
// request proceeds), so a key-extraction failure cannot deny all traffic.
type KeyFunc func(ctx huma.Context) (string, error)

// Middleware enforces a per-client rate limit as Huma middleware. Installed
// before the authentication middleware (see Install), it rejects an over-limit
// request with an RFC 9457 429 before authentication runs, so a flood never
// reaches the credential store. It is inert (pass-through) when disabled — the
// escape hatch matching the authz tier.
type Middleware struct {
	api     huma.API
	limiter Limiter
	key     KeyFunc
	logger  *slog.Logger
	enabled bool
}

// NewMiddleware builds the rate-limit middleware over limiter, keyed by key. api
// is the Huma API used to write the RFC 9457 problem response; logger records
// throttling and fail-open events. When enabled is false the middleware is a
// pass-through (Install is a no-op).
func NewMiddleware(
	api huma.API,
	limiter Limiter,
	key KeyFunc,
	logger *slog.Logger,
	enabled bool,
) *Middleware {
	if logger == nil {
		logger = slog.Default()
	}

	return &Middleware{
		api:     api,
		limiter: limiter,
		key:     key,
		logger:  logger,
		enabled: enabled,
	}
}

// Install registers the rate-limit middleware on the API. Like the authz
// middleware it must run before huma.Register (Huma snapshots the API's
// middleware stack into each operation at registration time, so middleware
// added afterward never runs), and it should be installed before the
// authentication middleware so limiting happens pre-auth. It is a no-op when
// disabled. The infrastructure routes (/healthz, /readyz, /metrics) bypass Huma,
// so they are never rate limited.
func (m *Middleware) Install() {
	if !m.enabled {
		return
	}

	m.api.UseMiddleware(m.limit)
}

// limit is the middleware function: derive the client key, consult the limiter,
// and either continue or reject with 429. It fails open on any key-extraction or
// limiter error so the limiter is never a single point of failure for the API.
func (m *Middleware) limit(ctx huma.Context, next func(huma.Context)) {
	key, err := m.key(ctx)
	if err != nil {
		m.logger.WarnContext(ctx.Context(), "rate-limit key extraction failed; allowing request",
			slog.Any("error", err))
		next(ctx)

		return
	}

	decision, err := m.limiter.Allow(ctx.Context(), key)
	if err != nil {
		m.logger.ErrorContext(ctx.Context(), "rate limiter unavailable; allowing request",
			slog.Any("error", err))
		next(ctx)

		return
	}

	if decision.Allowed {
		next(ctx)

		return
	}

	if decision.RetryAfter > 0 {
		// Retry-After is whole seconds (RFC 9110); round up so the client never
		// retries before a token is actually available.
		seconds := int(math.Ceil(decision.RetryAfter.Seconds()))
		ctx.SetHeader("Retry-After", strconv.Itoa(seconds))
	}

	m.logger.InfoContext(ctx.Context(), "rate limit exceeded", slog.String("key", key))
	_ = huma.WriteErr(m.api, ctx, http.StatusTooManyRequests, genericTooManyRequests)
}
