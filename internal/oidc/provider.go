package oidc

import (
	"context"
	"log/slog"
)

// ProviderService serves provider metadata — the discovery / RFC 8414
// authorization-server document and the JWK set — with proxy-aware base-URL
// resolution. It mutates nothing; every call is a pure read over the KeyStore
// and IssuerRegistry ports.
type ProviderService struct {
	issuers issuerResolver
	logger  *slog.Logger
}

// ProviderOption customizes a ProviderService at construction.
type ProviderOption func(*ProviderService)

// WithProviderLogger sets the service logger. The default discards all records.
func WithProviderLogger(logger *slog.Logger) ProviderOption {
	return func(s *ProviderService) {
		if logger != nil {
			s.logger = logger
		}
	}
}

// NewProviderService wires the provider metadata use cases over the registry and
// key-store ports. The logger defaults to a discard handler.
//
// The design's constructor also names the Clock; ProviderService carries no time
// dependency (discovery/JWKS are time-independent), so it is intentionally
// omitted here rather than stored as an unused field.
func NewProviderService(registry IssuerRegistry, keys KeyStore, opts ...ProviderOption) *ProviderService {
	s := &ProviderService{
		issuers: issuerResolver{registry: registry, keys: keys},
		logger:  slog.New(slog.DiscardHandler),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Discovery builds the issuer's discovery document. Field order is fixed by the
// DiscoveryDocument struct declaration; the advertised algorithm set is the
// domain constant SupportedSigningAlgorithms(), cross-checked against the
// signing adapter by the constant-sync test.
func (s *ProviderService) Discovery(
	ctx context.Context,
	id IssuerID,
	origin RequestOrigin,
) (DiscoveryDocument, error) {
	issuer, err := s.issuers.resolve(ctx, id, origin)
	if err != nil {
		return DiscoveryDocument{}, err
	}
	s.logger.DebugContext(ctx, "serving discovery", "issuer", string(id))
	return NewDiscoveryDocument(issuer.BaseURL, id, SupportedSigningAlgorithms()), nil
}

// JWKS returns the issuer's public JWK set, forcing key materialization through
// the KeyStore so the set is never empty (every issuer's /jwks always serves its
// own key).
func (s *ProviderService) JWKS(ctx context.Context, id IssuerID) (JWKS, error) {
	s.logger.DebugContext(ctx, "serving jwks", "issuer", string(id))
	return s.issuers.keys.PublicKeys(ctx, id)
}
