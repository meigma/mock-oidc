package httpapi

import (
	"context"
	"net/http"
	"net/url"

	"github.com/danielgtaylor/huma/v2"

	"github.com/meigma/mock-oidc/internal/oidc"
)

// EndSessionInput is the /{issuer}/endsession input (GET and POST share it). Both
// post_logout_redirect_uri and state are read from the QUERY only (parity — never
// the body), and state is appended to the redirect only when present.
type EndSessionInput struct {
	Issuer                string `path:"issuer"`
	PostLogoutRedirectURI string `query:"post_logout_redirect_uri"`
	State                 string `query:"state"`
}

func (i *EndSessionInput) issuerID() string { return i.Issuer }

// registerEndSession mounts RP-initiated logout on BOTH GET and POST
// /{issuer}/endsession as two operations (distinct OperationIDs) sharing one
// handler. DefaultStatus is 302 (the redirect outcome); the handler overrides it
// to 200 for the direct logged-out page.
func (h *handlers) registerEndSession(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID:   "endsession-get",
		Method:        http.MethodGet,
		Path:          "/{issuer}/endsession",
		Summary:       "RP-initiated logout endpoint",
		Tags:          []string{tagOIDC},
		DefaultStatus: http.StatusFound,
	}, h.endSession)
	huma.Register(api, huma.Operation{
		OperationID:   "endsession-post",
		Method:        http.MethodPost,
		Path:          "/{issuer}/endsession",
		Summary:       "RP-initiated logout endpoint",
		Tags:          []string{tagOIDC},
		DefaultStatus: http.StatusFound,
	}, h.endSession)
}

// endSession computes the post-logout outcome (pure domain) and renders it: a 302
// to post_logout_redirect_uri (appending ?state=… only when state is present), or
// the plain "logged out" HTML page at 200 when no redirect URI was supplied. A
// malformed issuer renders the direct HTML error page. It NEVER returns a Go
// error, so the browser surface has one error contract.
func (h *handlers) endSession(ctx context.Context, in *EndSessionInput) (*BrowserOutput, error) {
	issuer, err := issuerOf(in)
	if err != nil {
		return h.endSessionError(err), nil
	}
	req := oidc.EndSessionRequest{
		Issuer:                issuer,
		PostLogoutRedirectURI: in.PostLogoutRedirectURI,
		State:                 in.State,
	}
	result, err := h.deps.Session.EndSession(ctx, req)
	if err != nil {
		return h.endSessionError(err), nil
	}
	if result.Redirect() {
		return &BrowserOutput{
			Status:   http.StatusFound,
			Location: appendLogoutState(result.RedirectURI, result.State),
		}, nil
	}
	return htmlOutput(http.StatusOK, tmplLoggedOut, nil), nil
}

// endSessionError renders a protocol error on the logout surface as the direct
// HTML error page (there is no redirect target to carry it into).
func (h *handlers) endSessionError(err error) *BrowserOutput {
	status, body := oauth2Error(err)
	return htmlOutput(status, tmplError, errorData{Error: body.Code, Description: body.Description})
}

// appendLogoutState appends ?state=… to the post-logout redirect URI, but only
// when state is present (an absent state leaves the URI untouched — no bare
// state= and no upstream NPE). It reuses the shared query-mode appender that owns
// url-encoding at the edge.
func appendLogoutState(uri, state string) string {
	if state == "" {
		return uri
	}
	params := url.Values{}
	params.Set("state", state)
	return appendParams(uri, oidc.ResponseModeQuery, params)
}
