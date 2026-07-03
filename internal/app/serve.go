package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
)

// namedServer pairs an [http.Server] with a label used in log lines. tls marks
// the listener that terminates HTTPS: only the API server does, so the metrics
// and control listeners stay plain HTTP (operational surfaces, not the OIDC
// contract).
type namedServer struct {
	server *http.Server
	name   string
	tls    bool
}

// servers returns the servers to run: the API server and, when a metrics-addr is
// configured, the dedicated metrics server. Only the API server terminates TLS.
func (a *App) servers() []namedServer {
	servers := []namedServer{{server: a.server, name: "http server", tls: a.tlsCertFile != ""}}
	if a.metricsServer != nil {
		servers = append(servers, namedServer{server: a.metricsServer, name: "metrics server"})
	}
	if a.controlServer != nil {
		servers = append(servers, namedServer{server: a.controlServer, name: "control server"})
	}

	return servers
}

// listen serves the given server over TLS when a certificate is configured, else
// plain HTTP. resolveTLS ensures the cert/key files exist before New returns, so
// by here they are always present when tls is set.
func (a *App) listen(s namedServer) error {
	if s.tls {
		return s.server.ListenAndServeTLS(a.tlsCertFile, a.tlsKeyFile)
	}
	return s.server.ListenAndServe()
}

// Run starts the configured HTTP servers and blocks until ctx is cancelled or a
// server fails, then shuts every server down within the configured grace period.
func (a *App) Run(ctx context.Context) error {
	// Stop the in-process rate limiter's janitor goroutine on every exit path.
	defer a.stopRateLimiter(ctx)
	// Flush and shut down the tracer provider on every exit path.
	defer a.shutdownTracing(ctx)

	servers := a.servers()
	serveErr := make(chan error, len(servers))
	for _, s := range servers {
		go func() {
			a.logger.InfoContext(ctx, s.name+" listening",
				slog.String("addr", s.server.Addr), slog.Bool("tls", s.tls))

			err := a.listen(s)
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				serveErr <- fmt.Errorf("%s: %w", s.name, err)

				return
			}

			serveErr <- nil
		}()
	}

	select {
	case err := <-serveErr:
		if err != nil {
			return err
		}

		return nil
	case <-ctx.Done():
		return a.shutdown(ctx)
	}
}

func (a *App) shutdown(ctx context.Context) error {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), a.grace)
	defer cancel()

	var errs []error
	for _, s := range a.servers() {
		a.logger.InfoContext(ctx, "shutting down "+s.name)
		if err := s.server.Shutdown(shutdownCtx); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", s.name, err))
		}
	}

	a.logger.InfoContext(shutdownCtx, "servers stopped")

	return errors.Join(errs...)
}

// stopRateLimiter stops the in-process rate limiter's janitor goroutine when one
// is configured. It is deferred in Run so it executes on every exit path. It is
// a no-op when rate limiting is disabled (no limiter was built).
func (a *App) stopRateLimiter(ctx context.Context) {
	if a.rateLimiter == nil {
		return
	}

	a.logger.InfoContext(ctx, "stopping rate limiter")
	a.rateLimiter.Stop()
}

// shutdownTracing flushes and shuts down the tracer provider when tracing is
// enabled. It is deferred in Run so it executes on every exit path. The flush
// runs on a fresh, grace-bounded context because Run's context is already
// cancelled by the time shutdown begins, and an already-cancelled context would
// abandon the final span export. It is a no-op when tracing is disabled.
func (a *App) shutdownTracing(ctx context.Context) {
	if a.traceShutdown == nil {
		return
	}

	a.logger.InfoContext(ctx, "shutting down tracer provider")

	flushCtx, cancel := context.WithTimeout(context.Background(), a.grace)
	defer cancel()

	if err := a.traceShutdown(flushCtx); err != nil {
		a.logger.ErrorContext(ctx, "tracer provider shutdown failed", slog.Any("error", err))
	}
}
