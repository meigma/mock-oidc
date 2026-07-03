//go:build integration

package integration

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestContainerDebuggerPortRemap is the Slice 5 port-remap regression: it drives the
// interactive debugger's full authorization-code round trip against the shipped
// image THROUGH its random testcontainers-mapped host port. The callback's real
// back-channel /token exchange must dial the server's OWN loopback listener — the
// front-channel origin names the host-side mapped port, which is NOT listening inside
// the container, so an origin-derived dial would be refused. Success is proven by the
// callback page rendering the exchanged tokens instead of the connection-refused
// error page. The dialed exchange must still mint a token whose iss is the
// browser-facing (mapped-port) issuer. It skips loudly when the image is absent.
func TestContainerDebuggerPortRemap(t *testing.T) {
	ctx := context.Background()
	// Zero config: authorize is non-interactive, so the front-channel POST auto-drives
	// the redirect into /authorize and on into the callback (no login page).
	base := startControlContainer(ctx, t, map[string]string{})

	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	var redirects []string
	client := &http.Client{
		Jar: jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			redirects = append(redirects, req.URL.String())
			if len(via) >= 10 {
				return errors.New("stopped after 10 redirects")
			}
			return nil
		},
	}

	// 1. GET the pre-filled debugger form through the mapped port.
	getReq, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/default/debugger", nil)
	require.NoError(t, err)
	getResp, err := client.Do(getReq)
	require.NoError(t, err)
	formBody, err := io.ReadAll(getResp.Body)
	require.NoError(t, err)
	require.NoError(t, getResp.Body.Close())
	require.Equal(t, http.StatusOK, getResp.StatusCode)
	assert.Contains(t, string(formBody), `action="/default/debugger"`,
		"the form posts back to the issuer's debugger through the mapped port")

	// 2. POST the form; the client follows the 302 into /authorize and on into the
	//    callback, which runs the REAL back-channel /token exchange over loopback.
	postReq, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/default/debugger",
		strings.NewReader("client_id=web-app&scope=openid+profile"))
	require.NoError(t, err)
	postReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	postResp, err := client.Do(postReq)
	require.NoError(t, err)
	resultBody, err := io.ReadAll(postResp.Body)
	require.NoError(t, err)
	require.NoError(t, postResp.Body.Close())

	joined := strings.Join(redirects, "\n")
	assert.Contains(t, joined, "/default/authorize", "POST /debugger 302s into /authorize via the mapped port")
	assert.Contains(t, joined, "code_challenge_method=S256", "PKCE challenge rides the authorize redirect")

	// 3. The browser lands on the callback, which rendered the exchange result: tokens,
	//    NOT the connection-refused error page an origin-derived dial would produce.
	require.Equalf(t, http.StatusOK, postResp.StatusCode, "callback: %s", resultBody)
	assert.Equal(t, "/default/debugger/callback", postResp.Request.URL.Path)
	result := string(resultBody)
	assert.Contains(t, result, "Token exchange complete",
		"the loopback back-channel exchange succeeded against the container's own listener")
	assert.Contains(t, result, "grant_type=authorization_code", "the real code exchange was performed")
	assert.NotContains(t, result, "back-channel token exchange failed",
		"the callback must not render the connection-refused error page (the pre-fix failure mode)")

	// 4. The result page shows the browser-facing (mapped-port) token endpoint, not the
	//    127.0.0.1 loopback address the exchange was actually dialed on — the front
	//    channel the user sees is preserved even though the dial went over loopback.
	assert.Contains(t, result, base+"/default",
		"the page shows the mapped-port front-channel token endpoint, not the loopback dial address")
	assert.NotContains(t, result, "127.0.0.1:8080",
		"the internal loopback dial address never leaks onto the result page")
}
