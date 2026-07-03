package oidc

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// AuthorizeService orchestrates the /authorize endpoint: it decides between
// rendering the interactive login page and issuing an authorization code, then
// caches the request snapshot under a freshly minted code for the token
// exchange. It holds no transport type — the httpapi adapter renders the login
// page, the form_post HTML, and the redirect URL; the domain returns only typed
// AuthorizeResult fields and never builds a redirect URL.
type AuthorizeService struct {
	codes            CodeStore
	clock            Clock
	interactiveLogin bool
	templates        LoginTemplates
	newCode          func() string
}

// AuthorizeOption customizes an AuthorizeService at construction.
type AuthorizeOption func(*AuthorizeService)

// WithAuthorizeCodeID overrides the authorization-code ID source. The default is
// a random UUID; tests pin it for deterministic assertions.
func WithAuthorizeCodeID(newCode func() string) AuthorizeOption {
	return func(s *AuthorizeService) {
		if newCode != nil {
			s.newCode = newCode
		}
	}
}

// WithLoginTemplates installs the config-declared login templates. A non-empty
// collection arms the login_hint branch of Authorize; the default (empty)
// leaves login_hint ignored.
func WithLoginTemplates(templates LoginTemplates) AuthorizeOption {
	return func(s *AuthorizeService) {
		s.templates = templates
	}
}

// NewAuthorizeService wires the /authorize use cases over the CodeStore and
// Clock. interactiveLogin is the server-config flag that forces the login page
// even when the request does not ask for it. The code ID source defaults to a
// random UUID.
//
// The design's constructor also names the IssuerRegistry; the authorize
// orchestration (TDD §7.2) resolves no issuer — it never builds a URL, reads a
// signing key, nor materializes an issuer — so, like ProviderService omitting
// the Clock, the unused registry dependency is intentionally not stored here.
func NewAuthorizeService(
	codes CodeStore,
	clock Clock,
	interactiveLogin bool,
	opts ...AuthorizeOption,
) *AuthorizeService {
	s := &AuthorizeService{
		codes:            codes,
		clock:            clock,
		interactiveLogin: interactiveLogin,
		newCode:          uuid.NewString,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Authorize decides between issuing a code and requiring interactive login.
// When login templates are configured and the request carries a login_hint, the
// hint wins outright: a matching template resolves headlessly to a code (even
// over interactiveLogin and prompt=login), and an unknown name is a hard
// invalid_request — never a silent fallthrough. Otherwise the server
// interactiveLogin flag OR prompt ∈ {login,consent,select_account} forces the
// login page; prompt=none never does. Only response_type=code is dispatched —
// the hybrid/implicit members are advertised in discovery but rejected here as
// invalid_grant.
func (s *AuthorizeService) Authorize(ctx context.Context, req AuthorizeRequest) (AuthorizeResult, error) {
	if req.ResponseType != ResponseTypeCode {
		return AuthorizeResult{}, InvalidGrant("response_type " + string(req.ResponseType) + " not supported.")
	}
	if s.templates.Len() > 0 && req.LoginHint != "" {
		tmpl, ok := s.templates.Lookup(LoginTemplateName(req.LoginHint))
		if !ok {
			return AuthorizeResult{}, UnknownLoginTemplate(req.LoginHint)
		}
		return s.issueCode(ctx, req, tmpl.Submission()) // template wins: headless login
	}
	if s.interactiveLogin || req.Prompt.RequiresLogin() {
		return AuthorizeResult{Kind: AuthorizeShowLogin, Request: req}, nil
	}
	return s.issueCode(ctx, req, LoginSubmission{}) // non-interactive: subject from config/default
}

// SubmitLogin handles POST /authorize: it caches the CodeRecord (login snapshot,
// nonce, PKCE challenge) under a fresh code and returns the redirect/form_post
// outcome. Login claims are stored to be merged putIfAbsent at mint time
// (mapping wins).
func (s *AuthorizeService) SubmitLogin(
	ctx context.Context,
	req AuthorizeRequest,
	login LoginSubmission,
) (AuthorizeResult, error) {
	return s.issueCode(ctx, req, login)
}

// issueCode mints a code, caches the request snapshot (nonce, PKCE, and any
// interactive login) under it, and returns AuthorizeFormPost when the request
// asked for response_mode=form_post, else AuthorizeRedirect (Mode carries
// query|fragment).
func (s *AuthorizeService) issueCode(
	ctx context.Context,
	req AuthorizeRequest,
	login LoginSubmission,
) (AuthorizeResult, error) {
	code := AuthorizationCode(s.newCode())
	rec := NewCodeRecord(req.Snapshot(), req.Nonce, req.PKCE, login, s.clock.Now())
	if err := s.codes.Save(ctx, code, rec); err != nil {
		return AuthorizeResult{}, fmt.Errorf("cache authorization code: %w", err)
	}
	kind := AuthorizeRedirect
	if req.ResponseMode == ResponseModeFormPost {
		kind = AuthorizeFormPost
	}
	return AuthorizeResult{
		Kind:        kind,
		Code:        code,
		State:       req.State,
		RedirectURI: req.RedirectURI,
		Mode:        req.ResponseMode, // query|fragment for the Redirect case
	}, nil
}
