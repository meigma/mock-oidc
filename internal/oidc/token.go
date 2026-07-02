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

// MintKind is the closed kind of token a direct control-plane mint produces.
type MintKind string

// The two mintable token kinds. access_token follows the default access-token
// shape; id_token defaults its audience to [client_id] when none is supplied.
const (
	MintAccessToken MintKind = "access_token"
	MintIDToken     MintKind = "id_token"
)

// MintSpec is the fully-resolved command for TokenService.Mint — the direct
// "issue a token without a flow" use case behind POST /_mock/mint. Every field is
// a typed domain value the control edge parsed (parse-don't-validate): Issuer
// (ParseIssuerID rejects _mock), the resolved BaseURL for iss (proxy-aware, or an
// anyToken override), Subject/Audience/Scopes/ClientID, the custom Claims (the
// lone map[string]any → ClaimSet crossing, done at the edge), Kind, the open JOSE
// Typ, the advertised Algorithm, and the Expiry. Mint drives the SAME
// Signer/KeyStore/Clock/IssuerRegistry as /token, so a minted token is
// byte-identical to a granted one and verifies against the issuer's JWKS.
type MintSpec struct {
	Issuer    IssuerID
	BaseURL   BaseURL
	Subject   Subject
	Audience  Audience
	Scopes    Scopes
	Claims    ClaimSet
	ClientID  ClientID
	Kind      MintKind
	Typ       JOSEType
	Algorithm SigningAlgorithm
	Expiry    time.Duration
}

// TokenService orchestrates the token endpoint. It resolves the per-request
// issuer through the shared issuerResolver, dispatches on the closed GrantType,
// and mints the response over the Signer port. It holds no transport or crypto
// type: the Signer performs all JWS, the Clock supplies every time value, and
// the result is a typed TokenResponse the adapter maps to JSON.
type TokenService struct {
	issuers   issuerResolver
	signer    Signer
	clock     Clock
	codes     CodeStore         // authorization_code cache; nil until the grant is wired
	refresh   RefreshTokenStore // refresh-token persistence; nil until the grant is wired
	scenarios CallbackQueue     // one-shot enqueued scenarios; nil disables the queue branch
	rotate    bool              // rotateRefreshToken: replace the refresh token on redemption (default off)
	newID     func() string
	logger    *slog.Logger
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

// WithCallbackQueue wires the one-shot scenario queue every grant consults first
// during callback resolution (enqueued scenario > configured callback > default),
// including the refresh grant, so they all share one queue. Without it, the
// scenario branch is skipped and resolution falls straight to the configured and
// default callbacks.
func WithCallbackQueue(queue CallbackQueue) TokenOption {
	return func(s *TokenService) {
		if queue != nil {
			s.scenarios = queue
		}
	}
}

// WithRefreshRotation enables refresh-token rotation (rotateRefreshToken): on a
// successful refresh_token redemption the old token is removed and a fresh
// RefreshBareUUID token replaces it (the nonce is dropped). It defaults off, so
// the same refresh token keeps redeeming.
func WithRefreshRotation(rotate bool) TokenOption {
	return func(s *TokenService) {
		s.rotate = rotate
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
	case GrantRefreshToken:
		if s.refresh == nil {
			return TokenResponse{}, UnsupportedGrant(string(req.Grant))
		}
		return s.refreshToken(ctx, issuer, req)
	case GrantPassword:
		return s.password(ctx, issuer, req)
	case GrantJWTBearer:
		return s.jwtBearer(ctx, issuer, req)
	case GrantTokenExchange:
		return s.exchange(ctx, issuer, req)
	default:
		return TokenResponse{}, UnsupportedGrant(string(req.Grant))
	}
}

// Mint issues a token directly from a MintSpec, bypassing any grant flow (the
// control-plane issueToken/anyToken use case). It materializes the issuer and its
// signing key through the SAME IssuerRegistry/KeyStore the /token endpoint uses,
// stamps the default registered claims from the one Clock, folds in the spec's
// custom claims, and signs through the SAME Signer — so the token is
// indistinguishable from a granted one and verifies against the issuer's JWKS.
// The returned ClaimSet is the minted claim set (for the control response). The
// iss is the spec's already-resolved BaseURL (proxy-aware, or an anyToken
// override); the signing algorithm is the issuer key's, guaranteeing the same
// header shape as a grant.
func (s *TokenService) Mint(ctx context.Context, spec MintSpec) (SignedToken, ClaimSet, error) {
	if _, err := s.issuers.registry.Materialize(ctx, spec.Issuer); err != nil {
		return "", ClaimSet{}, fmt.Errorf("materialize issuer: %w", err)
	}
	key, err := s.issuers.keys.SigningKey(ctx, spec.Issuer)
	if err != nil {
		return "", ClaimSet{}, fmt.Errorf("issuer signing key: %w", err)
	}
	now := s.clock.Now()
	claims := s.mintClaims(spec, now)
	s.logger.DebugContext(ctx, "minting token", "issuer", string(spec.Issuer), "kind", string(spec.Kind))
	signed, err := s.signer.Sign(ctx, spec.Issuer, NewToken(spec.Issuer, key.Algorithm, spec.Typ, claims))
	if err != nil {
		return "", ClaimSet{}, fmt.Errorf("sign minted token: %w", err)
	}
	return signed, claims, nil
}

// mintClaims assembles the minted claim set from the spec and the one Clock:
// iss from the resolved BaseURL, iat/nbf from now, exp from now + the spec expiry,
// jti from the injected ID source, tid seeded to the issuer id, and the spec's
// custom claims folded in verbatim. An id_token defaults its audience to
// [client_id] when the spec leaves it unset (parity: id_token aud is [client_id]).
func (s *TokenService) mintClaims(spec MintSpec, now Instant) ClaimSet {
	aud := spec.Audience
	if spec.Kind == MintIDToken && aud == nil {
		aud = Audience{string(spec.ClientID)}
	}
	tenant := string(spec.Issuer)
	return ClaimSet{
		Subject:   spec.Subject,
		Audience:  aud,
		Issuer:    spec.BaseURL.IssuerURL(spec.Issuer),
		IssuedAt:  now,
		NotBefore: now,
		Expiry:    now.Add(spec.Expiry),
		JWTID:     s.newID(),
		Tenant:    &tenant,
		Scope:     spec.Scopes,
		Custom:    spec.Claims.Custom.Clone(),
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
	claims := s.defaultClaims(issuer, cb.Subject(in), cb.Audience(in), cb, in, now)
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

// password mints the ROPC (resource-owner password credentials) pair minus the
// refresh token: an id_token AND an access_token, both with sub = username. The
// PASSWORD IS NEVER VALIDATED — it is captured and discarded at the edge and never
// crosses inward, so any password is accepted (catalog line 96). The id_token
// audience is always [client_id], carries no nonce (nonce=null) and no azp; the
// access_token audience follows the callback's 4-step chain. Scope is echoed; no
// refresh token is issued.
func (s *TokenService) password(
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
	sub := cb.Subject(in) // DefaultTokenCallback: the username for a non-cc grant
	alg := issuer.Key.Algorithm
	typ := cb.TypeHeader(in)

	// id_token: aud is ALWAYS [client_id]; no nonce, no azp (only authcode adds azp).
	idClaims := s.defaultClaims(issuer, sub, Audience{string(req.Client.ID)}, cb, in, now)
	idToken, err := s.signer.Sign(ctx, issuer.ID, NewToken(issuer.ID, alg, typ, idClaims))
	if err != nil {
		return TokenResponse{}, fmt.Errorf("sign id token: %w", err)
	}

	// access_token: aud from the callback's 4-step chain.
	accClaims := s.defaultClaims(issuer, sub, cb.Audience(in), cb, in, now)
	accessToken, err := s.signer.Sign(ctx, issuer.ID, NewToken(issuer.ID, alg, typ, accClaims))
	if err != nil {
		return TokenResponse{}, fmt.Errorf("sign access token: %w", err)
	}

	return TokenResponse{
		TokenType:   TokenTypeBearer,
		IDToken:     idToken,
		AccessToken: accessToken,
		ExpiresIn:   expiresIn(now, cb.Expiry()),
		Scope:       req.Scopes,
	}, nil
}

// jwtBearer mints an On-Behalf-Of access token (RFC 7523) from the inbound
// assertion. The assertion is PARSED, NOT signature-verified (catalog line 97):
// its claims are copied verbatim then re-stamped via CopyWithOverrides. Only an
// access_token is issued (no id/refresh token, no issued_token_type). Scope
// resolves request scope ?? the assertion's own scope claim ?? invalid_request
// when neither is present.
func (s *TokenService) jwtBearer(
	ctx context.Context,
	issuer Issuer,
	req TokenRequest,
) (TokenResponse, error) {
	in := req.CallbackInput()
	cb, err := s.resolveCallback(ctx, issuer, in)
	if err != nil {
		return TokenResponse{}, err
	}
	inbound, err := s.signer.ParseUnverified(ctx, req.Assertion)
	if err != nil {
		return TokenResponse{}, err
	}
	scopes := req.Scopes
	if len(scopes) == 0 {
		scopes = inbound.Scope // the assertion's own scope claim
	}
	if len(scopes) == 0 {
		return TokenResponse{}, MalformedRequest("scope missing: neither the request nor the assertion carried a scope")
	}

	now := s.clock.Now()
	claims := inbound.CopyWithOverrides(issuer, cb, now, s.newID)
	claims.Audience = cb.Audience(in) // request-aware aud, mirroring exchange (catalog line 97)
	access, err := s.signer.Sign(ctx, issuer.ID, NewToken(issuer.ID, issuer.Key.Algorithm, cb.TypeHeader(in), claims))
	if err != nil {
		return TokenResponse{}, fmt.Errorf("sign access token: %w", err)
	}
	return TokenResponse{
		TokenType:   TokenTypeBearer,
		AccessToken: access,
		ExpiresIn:   expiresIn(now, cb.Expiry()),
		Scope:       scopes,
	}, nil
}

// exchange mints an RFC 8693 token-exchange access token from the inbound
// subject_token. The subject_token is PARSED, NOT signature-verified (catalog line
// 98): its claims are copied verbatim then re-stamped via CopyWithOverrides. Only
// an access_token is issued, carrying issued_token_type = the access-token URN and
// NO scope (token-exchange never echoes scope). Audience precedence: the request
// `audience` param wins only when the resolved callback has no configured
// audience; a configured callback audience stands — cb.Audience resolves exactly
// this when handed the request audience candidate.
func (s *TokenService) exchange(
	ctx context.Context,
	issuer Issuer,
	req TokenRequest,
) (TokenResponse, error) {
	if err := s.authenticateExchangeClient(ctx, issuer, req); err != nil {
		return TokenResponse{}, err
	}
	in := req.CallbackInput()
	cb, err := s.resolveCallback(ctx, issuer, in)
	if err != nil {
		return TokenResponse{}, err
	}
	inbound, err := s.signer.ParseUnverified(ctx, req.SubjectToken)
	if err != nil {
		return TokenResponse{}, err
	}
	now := s.clock.Now()
	claims := inbound.CopyWithOverrides(issuer, cb, now, s.newID)
	claims.Audience = cb.Audience(in) // audience precedence (catalog line 98)
	access, err := s.signer.Sign(ctx, issuer.ID, NewToken(issuer.ID, issuer.Key.Algorithm, cb.TypeHeader(in), claims))
	if err != nil {
		return TokenResponse{}, fmt.Errorf("sign access token: %w", err)
	}
	return TokenResponse{
		TokenType:       TokenTypeBearer,
		AccessToken:     access,
		IssuedTokenType: req.Grant.IssuedTokenType(),
		ExpiresIn:       expiresIn(now, cb.Expiry()),
	}, nil
}

// authenticateExchangeClient enforces RFC 8693's requirement that a
// token-exchange request carry SOME form of client authentication (catalog line
// 109). A request that presents none at all -> invalid_request. When the client
// authenticated with private_key_jwt the assertion is PARSED (never signature-
// verified) through the same Signer seam the subject_token uses, then run through
// the structural-only ClientAuth.ValidatePrivateKeyJWT rules with the issuer URL
// and the token-endpoint URL as the accepted audiences. client_secret_basic and
// client_secret_post carry no structural rules (no secret is ever validated). The
// httpapi edge always reports genuinely-absent auth as ClientAuthNone, so the
// no-auth branch is what the edge no-client-auth case surfaces as.
func (s *TokenService) authenticateExchangeClient(
	ctx context.Context,
	issuer Issuer,
	req TokenRequest,
) error {
	switch req.Client.Auth {
	case ClientAuthNone:
		return MissingClientAuthentication()
	case ClientAuthPrivateKeyJWT:
		assertion, err := s.signer.ParseUnverified(ctx, req.Client.Assertion)
		if err != nil {
			return err
		}
		clientID := req.Client.ID
		if clientID == "" {
			clientID = ClientID(assertion.Subject) // private_key_jwt client_id == the assertion sub
		}
		issuerURL := issuer.BaseURL.IssuerURL(issuer.ID)
		tokenEndpointURL := issuerURL + suffixToken
		return req.Client.Auth.ValidatePrivateKeyJWT(assertion, clientID, issuerURL, tokenEndpointURL, s.clock.Now())
	case ClientAuthClientSecretBasic, ClientAuthClientSecretPost:
		return nil
	default:
		return nil // unspecified (in-process construction) — no HTTP client-auth shape to enforce
	}
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

	// id_token: aud is ALWAYS [client_id]; nonce from the cache; azp added here
	// only, and only for the default callback (never a RequestMappingCallback).
	idClaims := s.defaultClaims(issuer, sub, Audience{string(req.Client.ID)}, cb, in, now).WithNonce(rec.Nonce)
	if addsDefaultRegisteredClaims(cb) {
		idClaims = idClaims.WithAZP(req.Client.ID)
	}
	idClaims = idClaims.WithLoginClaims(login)
	idToken, err := s.signer.Sign(ctx, issuer.ID, NewToken(issuer.ID, alg, typ, idClaims))
	if err != nil {
		return TokenResponse{}, fmt.Errorf("sign id token: %w", err)
	}

	// access_token: aud from the callback's 4-step chain; same nonce, no azp.
	accClaims := s.defaultClaims(issuer, sub, cb.Audience(in), cb, in, now).
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

// refreshToken redeems a refresh token: it looks up the stored record, enforces
// the strict cross-issuer binding (the CORRECTED client text), and re-mints a
// fresh access token — plus an id_token when the record carried a nonce — with a
// FRESH jti/iat/exp but the SAME subject. Claim policy is resolved by
// resolveRefreshCallback, which consults the SAME enqueued-scenario queue as the
// other grants FIRST (issuer-matched head, one-shot), then falls back to the
// callback stored on the refresh record, then the default — so an enqueued
// scenario steers refresh redemption too (the priority-parity invariant). When
// rotateRefreshToken is enabled the
// old token is removed and a new RefreshBareUUID token is returned; otherwise the
// same refresh token is echoed back so it keeps redeeming.
func (s *TokenService) refreshToken(
	ctx context.Context,
	issuer Issuer,
	req TokenRequest,
) (TokenResponse, error) {
	rec, err := s.refresh.Lookup(ctx, issuer.ID, req.RefreshToken)
	if err != nil {
		return TokenResponse{}, UnknownRefreshToken()
	}
	if rec.Issuer != issuer.ID {
		return TokenResponse{}, RefreshCrossIssuer()
	}

	now := s.clock.Now()
	sub := rec.Subject
	in := CallbackInput{Grant: GrantRefreshToken, Client: req.Client, Scopes: req.Scopes, Subject: sub}
	cb, err := s.resolveRefreshCallback(ctx, issuer.ID, rec)
	if err != nil {
		return TokenResponse{}, err
	}
	alg := issuer.Key.Algorithm
	typ := cb.TypeHeader(in)

	// access_token: aud from the callback's 4-step chain; the cached nonce replayed.
	accClaims := s.defaultClaims(issuer, sub, cb.Audience(in), cb, in, now).WithNonce(rec.Nonce)
	accessToken, err := s.signer.Sign(ctx, issuer.ID, NewToken(issuer.ID, alg, typ, accClaims))
	if err != nil {
		return TokenResponse{}, fmt.Errorf("sign access token: %w", err)
	}

	resp := TokenResponse{
		TokenType:   TokenTypeBearer,
		AccessToken: accessToken,
		ExpiresIn:   expiresIn(now, cb.Expiry()),
		Scope:       req.Scopes,
	}

	// id_token: minted only when the original grant carried a nonce (OIDC login);
	// aud is ALWAYS [client_id] and it carries azp, matching the authcode exchange.
	if rec.Nonce != nil {
		idClaims := s.defaultClaims(issuer, sub, Audience{string(req.Client.ID)}, cb, in, now).WithNonce(rec.Nonce)
		if addsDefaultRegisteredClaims(cb) {
			idClaims = idClaims.WithAZP(req.Client.ID)
		}
		idToken, signErr := s.signer.Sign(ctx, issuer.ID, NewToken(issuer.ID, alg, typ, idClaims))
		if signErr != nil {
			return TokenResponse{}, fmt.Errorf("sign id token: %w", signErr)
		}
		resp.IDToken = idToken
	}

	next, err := s.rotateRefreshToken(ctx, issuer, req.RefreshToken, rec, cb)
	if err != nil {
		return TokenResponse{}, err
	}
	resp.RefreshToken = next
	return resp, nil
}

// rotateRefreshToken applies the rotation policy. With rotation off it returns
// the presented token unchanged (it keeps redeeming). With rotation on it removes
// the old token and persists a fresh RefreshBareUUID record — rotation drops the
// nonce, so a rotated token never re-mints an id_token.
func (s *TokenService) rotateRefreshToken(
	ctx context.Context,
	issuer Issuer,
	old RefreshToken,
	rec RefreshRecord,
	cb TokenCallback,
) (RefreshToken, error) {
	if !s.rotate {
		return old, nil
	}
	if err := s.refresh.Remove(ctx, old); err != nil {
		return "", fmt.Errorf("remove rotated refresh token: %w", err)
	}
	next := RefreshToken(s.newID())
	rotated := RefreshRecord{
		Issuer:   issuer.ID,
		Subject:  rec.Subject,
		Nonce:    nil, // rotation drops the nonce
		Format:   RefreshBareUUID,
		Callback: cb,
	}
	if err := s.refresh.Save(ctx, issuer.ID, next, rotated); err != nil {
		return "", fmt.Errorf("persist rotated refresh token: %w", err)
	}
	return next, nil
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
	if err := s.refresh.Save(ctx, issuer.ID, token, rec); err != nil {
		return "", fmt.Errorf("persist refresh token: %w", err)
	}
	return token, nil
}

// resolveCallback applies the callback precedence: an issuer-matched enqueued
// Scenario wins (highest priority, one-shot); else the first configured callback
// that Matches; else the built-in DefaultTokenCallback. The refresh grant calls
// resolveRefreshCallback, which shares the SAME queue, so an enqueued scenario is
// consumed by whichever matching grant arrives first.
func (s *TokenService) resolveCallback(ctx context.Context, issuer Issuer, in CallbackInput) (TokenCallback, error) {
	if cb, ok, err := s.dequeueScenario(ctx, issuer.ID); err != nil {
		return nil, err
	} else if ok {
		return cb, nil
	}
	for _, cb := range issuer.Callbacks {
		if cb.Matches(in) {
			return cb, nil
		}
	}
	return NewDefaultTokenCallback(issuer.ID), nil
}

// resolveRefreshCallback applies the refresh grant's precedence: an issuer-matched
// enqueued Scenario wins (the same shared queue the other grants poll); else the
// callback bound to the refresh record when redeemed; else the default. It keeps
// the refresh grant on the one global queue (upstream: "the refresh grant
// consults the same queue").
func (s *TokenService) resolveRefreshCallback(
	ctx context.Context,
	issuer IssuerID,
	rec RefreshRecord,
) (TokenCallback, error) {
	if cb, ok, err := s.dequeueScenario(ctx, issuer); err != nil {
		return nil, err
	} else if ok {
		return cb, nil
	}
	if rec.Callback != nil {
		return rec.Callback, nil
	}
	return NewDefaultTokenCallback(issuer), nil
}

// dequeueScenario consults the enqueued-scenario queue (when wired), popping the
// head only if it targets issuer. It returns ok=false with no error when no
// queue is wired or the head belongs to another issuer.
func (s *TokenService) dequeueScenario(ctx context.Context, issuer IssuerID) (TokenCallback, bool, error) {
	if s.scenarios == nil {
		return nil, false, nil
	}
	sc, ok, err := s.scenarios.DequeueFor(ctx, issuer)
	if err != nil {
		return nil, false, fmt.Errorf("dequeue scenario: %w", err)
	}
	if !ok {
		return nil, false, nil
	}
	return sc.Callback, true, nil
}

// addsDefaultRegisteredClaims reports whether cb is the built-in default callback,
// the only callback that stamps the tid (and, on authorization_code, azp)
// registered claims — a RequestMappingCallback never adds them (upstream parity).
func addsDefaultRegisteredClaims(cb TokenCallback) bool {
	_, ok := cb.(DefaultTokenCallback)
	return ok
}

// defaultClaims assembles the registered claims into a typed ClaimSet: iss from
// the resolved BaseURL, iat/nbf from now, exp from now + the callback expiry, and
// jti from the injected ID source. tid is seeded to the issuer id ONLY for the
// default callback (a RequestMappingCallback never adds it). The resolved
// callback's extra (custom) claims are folded in add-only, skipping any
// registered claim name so a mapping cannot shadow a typed field. It never
// touches a map[string]any.
func (s *TokenService) defaultClaims(
	issuer Issuer,
	sub Subject,
	aud Audience,
	cb TokenCallback,
	in CallbackInput,
	now Instant,
) ClaimSet {
	claims := ClaimSet{
		Subject:   sub,
		Audience:  aud,
		Issuer:    issuer.BaseURL.IssuerURL(issuer.ID),
		IssuedAt:  now,
		NotBefore: now,
		Expiry:    now.Add(cb.Expiry()),
		JWTID:     s.newID(),
	}
	if addsDefaultRegisteredClaims(cb) {
		tenant := string(issuer.ID)
		claims.Tenant = &tenant
	}
	for _, e := range cb.ExtraClaims(in).Custom.Entries() {
		if _, reserved := registeredClaimNames[e.Name]; reserved {
			continue
		}
		claims.Custom.Set(e.Name, e.Value)
	}
	return claims
}

// expiresIn reports the token lifetime in whole seconds as exp - now, both taken
// from the same Clock instant so the advertised expires_in can never diverge
// from the exp claim (a deliberate correction of upstream's expires_in-from-real-
// now quirk).
func expiresIn(now Instant, lifetime time.Duration) int64 {
	return now.Add(lifetime).Unix() - now.Unix()
}
