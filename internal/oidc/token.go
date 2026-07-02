package oidc

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

// TokenType is the response `token_type` (closed: Bearer).
type TokenType string

// TokenTypeBearer is the only response token_type this server issues.
const TokenTypeBearer TokenType = "Bearer"

// IssuedTokenType is the token-exchange `issued_token_type` (closed: the RFC
// 8693 access_token URN).
type IssuedTokenType string

// IssuedTokenAccessToken is the RFC 8693 access-token type URN (a protocol
// identifier, not a credential).
//
//nolint:gosec // G101: OAuth2 token-type URN, not a credential.
const IssuedTokenAccessToken IssuedTokenType = "urn:ietf:params:oauth:token-type:access_token"

// JOSEType is the JWS "typ" header. It is intentionally OPEN — no closed Valid():
// the default is "JWT", but a TokenCallback may set any value (e.g. RFC 9068
// "at+jwt"), so ParseJOSEType never rejects; it only supplies the default for
// empty input. This is distinct from TokenType (response token_type) and
// IssuedTokenType (token-exchange), which are closed.
type JOSEType string

// DefaultJOSEType is the JWS typ used when a callback specifies none.
const DefaultJOSEType JOSEType = "JWT"

// ParseJOSEType returns the JOSE typ for s, defaulting empty input to "JWT". It
// never rejects — any custom callback value is representable.
func ParseJOSEType(s string) JOSEType {
	if s == "" {
		return DefaultJOSEType
	}
	return JOSEType(s)
}

// JWTHeader is the JWS header for a minted token: algorithm, key id (= issuer),
// and JOSE type (default "JWT"; e.g. "at+jwt" via a callback typeHeader).
type JWTHeader struct {
	Algorithm SigningAlgorithm
	KeyID     KeyID
	Type      JOSEType
}

// Token is an unsigned token model — header plus claims. It crosses to the
// Signer port; the domain performs no crypto and never serializes.
type Token struct {
	Header JWTHeader
	Claims ClaimSet
}

// NewToken assembles an unsigned Token, defaulting the JOSE typ to "JWT". The
// kid is derived from the issuer (kid == IssuerID), the algorithm is explicit,
// and the typ is the open JOSEType (so at+jwt is representable).
func NewToken(issuer IssuerID, alg SigningAlgorithm, typ JOSEType, claims ClaimSet) Token {
	return Token{
		Header: JWTHeader{Algorithm: alg, KeyID: issuer.KeyID(), Type: ParseJOSEType(string(typ))},
		Claims: claims,
	}
}

// SignedToken is the compact-serialized signed JWT: the wire artifact. It is
// opaque to the domain — produced by the Signer adapter, echoed in responses,
// and handed back to the Verifier adapter for /userinfo and /introspect.
type SignedToken string

// TokenService orchestrates the token endpoint. It resolves the per-request
// issuer through the shared issuerResolver, dispatches on the closed GrantType,
// and mints the response over the Signer port. It holds no transport or crypto
// type: the Signer performs all JWS, the Clock supplies every time value, and
// the result is a typed TokenResponse the adapter maps to JSON.
type TokenService struct {
	issuers issuerResolver
	signer  Signer
	clock   Clock
	codes   CodeStore         // authorization_code cache; nil until the grant is wired
	refresh RefreshTokenStore // refresh-token persistence; nil until the grant is wired
	newID   func() string
	logger  *slog.Logger
}

// TokenOption customizes a TokenService at construction.
type TokenOption func(*TokenService)

// WithTokenLogger sets the service logger. The default discards all records.
func WithTokenLogger(logger *slog.Logger) TokenOption {
	return func(s *TokenService) {
		if logger != nil {
			s.logger = logger
		}
	}
}

// WithTokenID overrides the jti (JWT ID) source. The default is a random UUID;
// tests pin it for deterministic claim assertions.
func WithTokenID(newID func() string) TokenOption {
	return func(s *TokenService) {
		if newID != nil {
			s.newID = newID
		}
	}
}

// WithCodeStore wires the single-use authorization-code cache. It is paired with
// WithRefreshStore to enable the authorization_code grant; without both, that
// grant is reported as an unsupported grant.
func WithCodeStore(codes CodeStore) TokenOption {
	return func(s *TokenService) {
		if codes != nil {
			s.codes = codes
		}
	}
}

// WithRefreshStore wires the refresh-token persistence used by the
// authorization_code grant. See WithCodeStore.
func WithRefreshStore(refresh RefreshTokenStore) TokenOption {
	return func(s *TokenService) {
		if refresh != nil {
			s.refresh = refresh
		}
	}
}

// NewTokenService wires the token use cases over the registry, key-store, and
// signer ports plus the Clock. The jti source defaults to a random UUID and the
// logger to a discard handler.
func NewTokenService(
	registry IssuerRegistry,
	keys KeyStore,
	signer Signer,
	clock Clock,
	opts ...TokenOption,
) *TokenService {
	s := &TokenService{
		issuers: issuerResolver{registry: registry, keys: keys},
		signer:  signer,
		clock:   clock,
		newID:   uuid.NewString,
		logger:  slog.New(slog.DiscardHandler),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Issue resolves the issuer (so iss/base-URL are proxy-correct) and dispatches
// on the grant. Only client_credentials is wired in this slice; every other
// (valid) grant is reported as an unsupported grant until its slice lands.
func (s *TokenService) Issue(
	ctx context.Context,
	origin RequestOrigin,
	req TokenRequest,
) (TokenResponse, error) {
	issuer, err := s.issuers.resolve(ctx, req.Issuer, origin)
	if err != nil {
		return TokenResponse{}, err
	}
	s.logger.DebugContext(ctx, "issuing token", "issuer", string(req.Issuer), "grant", string(req.Grant))
	switch req.Grant {
	case GrantClientCredentials:
		return s.clientCredentials(ctx, issuer, req)
	case GrantAuthorizationCode:
		if s.codes == nil || s.refresh == nil {
			return TokenResponse{}, UnsupportedGrant(string(req.Grant))
		}
		return s.authorizationCode(ctx, issuer, req)
	case GrantPassword, GrantRefreshToken, GrantJWTBearer, GrantTokenExchange:
		return TokenResponse{}, UnsupportedGrant(string(req.Grant))
	default:
		return TokenResponse{}, UnsupportedGrant(string(req.Grant))
	}
}

// clientCredentials mints an access token only: sub defaults to client_id and
// aud follows the callback's 4-step precedence (→ ["default"] when nothing is
// configured). iss/iat/nbf/exp/jti/tid come from defaultClaims off the one Clock;
// expires_in is derived from that same Clock so it never diverges from exp.
func (s *TokenService) clientCredentials(
	ctx context.Context,
	issuer Issuer,
	req TokenRequest,
) (TokenResponse, error) {
	in := req.CallbackInput()
	cb, err := s.resolveCallback(ctx, issuer, in)
	if err != nil {
		return TokenResponse{}, err
	}
	now := s.clock.Now()
	claims := s.defaultClaims(issuer, cb.Subject(in), cb.Audience(in), cb, now)
	access, err := s.signer.Sign(ctx, issuer.ID, NewToken(issuer.ID, issuer.Key.Algorithm, cb.TypeHeader(in), claims))
	if err != nil {
		return TokenResponse{}, fmt.Errorf("sign access token: %w", err)
	}
	return TokenResponse{
		TokenType:   TokenTypeBearer,
		AccessToken: access,
		ExpiresIn:   expiresIn(now, cb.Expiry()),
		Scope:       req.Scopes,
	}, nil
}

// authorizationCode redeems a single-use code for the id/access/refresh triple.
// The code is burned FIRST (codes.Take, before the PKCE check) so a failed
// exchange still invalidates it. The nonce, login subject, and login claims come
// from the CACHED record — never the token request — so the client cannot forge
// them. The id_token audience is always [client_id] and carries azp; the
// access_token audience follows the callback's 4-step chain; both carry the
// cached nonce, and both merge the login claims add-only.
func (s *TokenService) authorizationCode(
	ctx context.Context,
	issuer Issuer,
	req TokenRequest,
) (TokenResponse, error) {
	rec, err := s.codes.Take(ctx, req.Code)
	if err != nil {
		return TokenResponse{}, UnknownAuthorizationCode()
	}
	if err = rec.VerifyPKCE(req.CodeVerifier); err != nil {
		return TokenResponse{}, err // InvalidGrant("invalid_pkce: ...") or the asymmetric cases
	}

	in := rec.CallbackInput(req) // nonce + login from the cache, not the token request
	cb, err := s.resolveCallback(ctx, issuer, in)
	if err != nil {
		return TokenResponse{}, err
	}
	now := s.clock.Now()
	sub := cb.Subject(in)
	login := rec.loginClaims()

	alg := issuer.Key.Algorithm
	typ := cb.TypeHeader(in)

	// id_token: aud is ALWAYS [client_id]; nonce from the cache; azp added here only.
	idClaims := s.defaultClaims(issuer, sub, Audience{string(req.Client.ID)}, cb, now).
		WithNonce(rec.Nonce).WithAZP(req.Client.ID).WithLoginClaims(login)
	idToken, err := s.signer.Sign(ctx, issuer.ID, NewToken(issuer.ID, alg, typ, idClaims))
	if err != nil {
		return TokenResponse{}, fmt.Errorf("sign id token: %w", err)
	}

	// access_token: aud from the callback's 4-step chain; same nonce, no azp.
	accClaims := s.defaultClaims(issuer, sub, cb.Audience(in), cb, now).
		WithNonce(rec.Nonce).WithLoginClaims(login)
	accessToken, err := s.signer.Sign(ctx, issuer.ID, NewToken(issuer.ID, alg, typ, accClaims))
	if err != nil {
		return TokenResponse{}, fmt.Errorf("sign access token: %w", err)
	}

	refresh, err := s.issueRefresh(ctx, issuer, sub, cb, rec.Nonce)
	if err != nil {
		return TokenResponse{}, err
	}
	return TokenResponse{
		TokenType:    TokenTypeBearer,
		IDToken:      idToken,
		AccessToken:  accessToken,
		RefreshToken: refresh,
		Scope:        req.Scopes,
		ExpiresIn:    expiresIn(now, cb.Expiry()),
	}, nil
}

// issueRefresh mints and persists the refresh token for a grant. The domain
// chooses the wire form (ChooseRefreshFormat): a bare UUID from the injected ID
// source, or — when a nonce is present — an unsigned alg=none PlainJWT that the
// signing adapter compact-serializes through the Signer port (the Keycloak-JS
// accommodation carrying {jti, nonce}). The RefreshRecord is persisted for the
// Slice 3 redemption path; the domain never serializes the token itself.
func (s *TokenService) issueRefresh(
	ctx context.Context,
	issuer Issuer,
	sub Subject,
	cb TokenCallback,
	nonce *Nonce,
) (RefreshToken, error) {
	format := ChooseRefreshFormat(nonce)
	var token RefreshToken
	switch format {
	case RefreshBareUUID:
		token = RefreshToken(s.newID())
	case RefreshPlainJWT:
		claims := ClaimSet{JWTID: s.newID(), Nonce: nonce}
		signed, err := s.signer.Sign(ctx, issuer.ID, NewToken(issuer.ID, AlgNone, DefaultJOSEType, claims))
		if err != nil {
			return "", fmt.Errorf("sign refresh token: %w", err)
		}
		token = RefreshToken(signed)
	}
	rec := RefreshRecord{
		Issuer:   issuer.ID,
		Subject:  sub,
		Nonce:    nonce,
		Format:   format,
		Callback: cb,
	}
	if err := s.refresh.Save(ctx, token, rec); err != nil {
		return "", fmt.Errorf("persist refresh token: %w", err)
	}
	return token, nil
}

// resolveCallback applies the callback precedence: the first configured callback
// that Matches, else the built-in DefaultTokenCallback. The enqueued one-shot
// Scenario branch (highest priority) lands with the CallbackQueue port in a
// later slice; the signature already carries ctx and returns an error so that
// widening does not touch callers.
//
//nolint:unparam // ctx and the error are reserved for the S5 CallbackQueue branch; the signature is kept stable.
func (s *TokenService) resolveCallback(_ context.Context, issuer Issuer, in CallbackInput) (TokenCallback, error) {
	for _, cb := range issuer.Callbacks {
		if cb.Matches(in) {
			return cb, nil
		}
	}
	return NewDefaultTokenCallback(issuer.ID), nil
}

// defaultClaims assembles the registered claims into a typed ClaimSet: iss from
// the resolved BaseURL, iat/nbf from now, exp from now + the callback expiry,
// jti from the injected ID source, and tid seeded to the issuer id (an
// overridable claim). It never touches a map[string]any.
func (s *TokenService) defaultClaims(
	issuer Issuer,
	sub Subject,
	aud Audience,
	cb TokenCallback,
	now Instant,
) ClaimSet {
	tenant := string(issuer.ID)
	return ClaimSet{
		Subject:   sub,
		Audience:  aud,
		Issuer:    issuer.BaseURL.IssuerURL(issuer.ID),
		IssuedAt:  now,
		NotBefore: now,
		Expiry:    now.Add(cb.Expiry()),
		JWTID:     s.newID(),
		Tenant:    &tenant,
	}
}

// expiresIn reports the token lifetime in whole seconds as exp - now, both taken
// from the same Clock instant so the advertised expires_in can never diverge
// from the exp claim (a deliberate correction of upstream's expires_in-from-real-
// now quirk).
func expiresIn(now Instant, lifetime time.Duration) int64 {
	return now.Add(lifetime).Unix() - now.Unix()
}
