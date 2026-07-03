package oidc_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/mock-oidc/internal/oidc"
)

// TestParseIssuerIDAccepts confirms a well-formed single-segment id parses and
// that kid == id by construction.
func TestParseIssuerIDAccepts(t *testing.T) {
	t.Parallel()

	id, err := oidc.ParseIssuerID("default")
	require.NoError(t, err)
	assert.Equal(t, oidc.IssuerID("default"), id)
	assert.Equal(t, oidc.KeyID("default"), id.KeyID())
}

// TestParseIssuerIDRejectionMatrix is the rejection matrix. Each case asserts
// both [errors.As] (*ProtocolError) (so the OAuth2 writer maps code/status) and,
// where applicable, [errors.Is] (ErrReservedIssuer)/ErrInvalidIssuer (so the
// control plane's sentinel branch still fires) on the same value.
func TestParseIssuerIDRejectionMatrix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		wantCode oidc.ErrorCode
		wantHTTP int
		sentinel error
	}{
		{"empty", "", oidc.CodeInvalidRequest, 400, nil},
		{"contains slash", "tenant/sub", oidc.CodeInvalidRequest, 400, oidc.ErrInvalidIssuer},
		{"reserved exact", "_mock", oidc.CodeNotFound, 404, oidc.ErrReservedIssuer},
		// The slash check precedes the reserved-prefix check, so a reserved value
		// carrying a slash is rejected as invalid_request (a single path segment
		// can never contain a slash under single-segment routing anyway).
		{"reserved with slash", "_mock/clock", oidc.CodeInvalidRequest, 400, oidc.ErrInvalidIssuer},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := oidc.ParseIssuerID(tc.input)
			require.Error(t, err)

			var pe *oidc.ProtocolError
			require.ErrorAs(t, err, &pe, "must be recoverable as *ProtocolError")
			assert.Equal(t, tc.wantCode, pe.Code)
			assert.Equal(t, tc.wantHTTP, pe.HTTPStatus)

			if tc.sentinel != nil {
				assert.ErrorIs(t, err, tc.sentinel, "must also match its parse sentinel")
			}
		})
	}
}

// TestResolveBaseURL exercises the proxy-aware precedence and the joined issuer
// URL, plus the default-port omission in String().
func TestResolveBaseURL(t *testing.T) {
	t.Parallel()

	t.Run("host with non-default port", func(t *testing.T) {
		t.Parallel()

		base, err := oidc.ResolveBaseURL(oidc.RequestOrigin{
			Scheme: oidc.SchemeHTTP, Host: "localhost", Port: 8080,
		})
		require.NoError(t, err)
		assert.Equal(t, "http://localhost:8080", base.String())
		assert.Equal(t, "http://localhost:8080/default", base.IssuerURL("default"))
	})

	t.Run("forwarded headers win and default port omitted", func(t *testing.T) {
		t.Parallel()

		base, err := oidc.ResolveBaseURL(oidc.RequestOrigin{
			Scheme:   oidc.SchemeHTTP,
			Host:     "internal",
			Port:     8080,
			FwdProto: "https",
			FwdHost:  "auth.example.com",
			FwdPort:  "443",
		})
		require.NoError(t, err)
		assert.Equal(t, "https://auth.example.com", base.String())
	})

	t.Run("forwarded port overrides host port", func(t *testing.T) {
		t.Parallel()

		base, err := oidc.ResolveBaseURL(oidc.RequestOrigin{
			Scheme: oidc.SchemeHTTPS, Host: "h", Port: 8080, FwdPort: "9443",
		})
		require.NoError(t, err)
		assert.Equal(t, "https://h:9443", base.String())
	})

	t.Run("forwarded host with embedded port does not double the authority", func(t *testing.T) {
		t.Parallel()

		base, err := oidc.ResolveBaseURL(oidc.RequestOrigin{
			Scheme: oidc.SchemeHTTPS, Host: "internal", Port: 8080, FwdHost: "idp.example.com:8443",
		})
		require.NoError(t, err)
		assert.Equal(t, "https://idp.example.com:8443", base.String())
		assert.Equal(t, "https://idp.example.com:8443/default", base.IssuerURL("default"))
	})

	t.Run("explicit forwarded port outranks the forwarded host port", func(t *testing.T) {
		t.Parallel()

		base, err := oidc.ResolveBaseURL(oidc.RequestOrigin{
			Scheme: oidc.SchemeHTTPS, Host: "internal", FwdHost: "idp.example.com:8443", FwdPort: "9443",
		})
		require.NoError(t, err)
		assert.Equal(t, "https://idp.example.com:9443", base.String())
	})

	t.Run("scheme default port is omitted when no port is present", func(t *testing.T) {
		t.Parallel()

		https, err := oidc.ResolveBaseURL(oidc.RequestOrigin{Scheme: oidc.SchemeHTTPS, Host: "h"})
		require.NoError(t, err)
		assert.Equal(t, "https://h", https.String())

		httpBase, err := oidc.ResolveBaseURL(oidc.RequestOrigin{Scheme: oidc.SchemeHTTP, Host: "h"})
		require.NoError(t, err)
		assert.Equal(t, "http://h", httpBase.String())
	})

	t.Run("issuer url is host-rooted regardless of request depth", func(t *testing.T) {
		t.Parallel()

		// The origin carries no path, and IssuerURL joins the issuer segment at the
		// host root — a request to a deep path still advertises a host-root iss.
		base, err := oidc.ResolveBaseURL(oidc.RequestOrigin{
			Scheme: oidc.SchemeHTTPS, Host: "idp.example.com", FwdPort: "443",
		})
		require.NoError(t, err)
		assert.Equal(t, "https://idp.example.com/default", base.IssuerURL("default"))
	})

	t.Run("malformed forwarded port is an error", func(t *testing.T) {
		t.Parallel()

		_, err := oidc.ResolveBaseURL(oidc.RequestOrigin{
			Scheme: oidc.SchemeHTTPS, Host: "h", FwdPort: "not-a-port",
		})
		require.Error(t, err)
		assert.ErrorIs(t, err, oidc.ErrInvalidBaseURL)
	})

	t.Run("bad forwarded scheme is an error", func(t *testing.T) {
		t.Parallel()

		_, err := oidc.ResolveBaseURL(oidc.RequestOrigin{
			Scheme: oidc.SchemeHTTPS, Host: "h", FwdProto: "gopher",
		})
		require.Error(t, err)
		assert.ErrorIs(t, err, oidc.ErrInvalidScheme)
	})
}
