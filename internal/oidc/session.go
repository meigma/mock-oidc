package oidc

import (
	"context"
	"fmt"
)

// SessionService orchestrates the post-issuance lifecycle endpoints: /userinfo,
// /introspect, /revoke, and /endsession (RP-initiated logout). It reaches crypto
// only through the TokenVerifier port and persistence only through the
// RefreshTokenStore port, and reads every time value from the same Clock issuance
// uses — so a control-plane clock advance moves verification in lockstep with
// issuance. It holds no transport type; the httpapi adapter parses the wire into
// the typed requests and maps the typed results back to the wire.
type SessionService struct {
	verifier TokenVerifier
	refresh  RefreshTokenStore
	clock    Clock
}

// NewSessionService wires the lifecycle use cases over the verifier and
// refresh-store ports plus the Clock. The Clock MUST be the same instance
// issuance uses so iat/exp are checked against one time base.
func NewSessionService(verifier TokenVerifier, refresh RefreshTokenStore, clock Clock) *SessionService {
	return &SessionService{verifier: verifier, refresh: refresh, clock: clock}
}

// UserInfo verifies the bearer access token and returns its ENTIRE claim set
// verbatim (the edge emits it unscoped). A verification failure is reported as
// invalid_token (401), wrapping the verifier cause so [errors.Is] can reach it.
func (s *SessionService) UserInfo(ctx context.Context, req UserInfoRequest) (ClaimSet, error) {
	claims, err := s.verifier.Verify(ctx, req.Issuer, req.Token, s.clock.Now())
	if err != nil {
		return ClaimSet{}, NewInvalidToken(err)
	}
	return claims, nil
}

// Introspect verifies the token; an UNVERIFIABLE token is reported as
// {active:false} at 200, NEVER a Go error (RFC 7662 / parity: introspecting a bad
// token is not a failure). The presence-only client-auth check (else
// invalid_client at 400) is enforced at the adapter edge, not here.
func (s *SessionService) Introspect(ctx context.Context, req IntrospectionRequest) (IntrospectionResult, error) {
	claims, err := s.verifier.Verify(ctx, req.Issuer, req.Token, s.clock.Now())
	if err != nil {
		//nolint:nilerr // RFC 7662 / parity: an unverifiable token is reported as {active:false} at 200, never a Go error.
		return InactiveIntrospection(), nil
	}
	return IntrospectionFrom(claims), nil
}

// Revoke removes a refresh token. Only token_type_hint=refresh_token is
// supported; any other hint -> unsupported_token_type (400, mapped at the edge).
// Removing an absent token is a no-op, so revoke is idempotent.
func (s *SessionService) Revoke(ctx context.Context, req RevocationRequest) error {
	if req.Hint != TokenHintRefreshToken {
		return UnsupportedTokenType(req.Hint)
	}
	if err := s.refresh.Remove(ctx, req.Token); err != nil {
		return fmt.Errorf("revoke refresh token: %w", err)
	}
	return nil
}

// EndSession is pure: it computes the post-logout outcome from the typed request
// (post_logout_redirect_uri + state, read from the query only). There is no
// id_token_hint validation and no server-side session to terminate (parity) — the
// edge issues a 302 to the redirect (appending ?state=… only when present) or
// renders the plain "logged out" page.
func (s *SessionService) EndSession(_ context.Context, req EndSessionRequest) (EndSessionResult, error) {
	return NewEndSessionResult(req.PostLogoutRedirectURI, req.State), nil
}
