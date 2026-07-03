package app

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/mock-oidc/internal/config"
	"github.com/meigma/mock-oidc/internal/observability"
)

// parseCertFile reads a PEM cert file and returns the leaf certificate.
func parseCertFile(t *testing.T, path string) *x509.Certificate {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	block, _ := pem.Decode(raw)
	require.NotNil(t, block, "cert file is not PEM")
	cert, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)
	return cert
}

// TestGenerateSelfSignedLocalhost verifies the generated cert parses, names
// localhost, and carries the loopback SANs a test client dials.
func TestGenerateSelfSignedLocalhost(t *testing.T) {
	t.Parallel()

	certFile, keyFile, err := generateSelfSignedLocalhost()
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.Remove(certFile); _ = os.Remove(keyFile) })

	require.NotEmpty(t, certFile)
	require.NotEmpty(t, keyFile)

	// The pair must load as a usable TLS keypair.
	_, err = tls.LoadX509KeyPair(certFile, keyFile)
	require.NoError(t, err)

	cert := parseCertFile(t, certFile)
	assert.Equal(t, "localhost", cert.Subject.CommonName)
	assert.Contains(t, cert.DNSNames, "localhost")

	ips := make([]string, 0, len(cert.IPAddresses))
	for _, ip := range cert.IPAddresses {
		ips = append(ips, ip.String())
	}
	assert.Contains(t, ips, "127.0.0.1", "SAN must include the IPv4 loopback")
	assert.True(t, cert.NotAfter.After(time.Now()), "cert must not be already expired")
}

// TestResolveTLS covers the three branches: off, explicit files, and generate.
func TestResolveTLS(t *testing.T) {
	t.Parallel()

	t.Run("off yields empty", func(t *testing.T) {
		t.Parallel()
		cert, key, err := resolveTLS(config.Config{})
		require.NoError(t, err)
		assert.Empty(t, cert)
		assert.Empty(t, key)
	})

	t.Run("explicit files pass through", func(t *testing.T) {
		t.Parallel()
		cert, key, err := resolveTLS(config.Config{TLSCertFile: "/x/cert.pem", TLSKeyFile: "/x/key.pem"})
		require.NoError(t, err)
		assert.Equal(t, "/x/cert.pem", cert)
		assert.Equal(t, "/x/key.pem", key)
	})

	t.Run("enabled with no files generates a self-signed pair", func(t *testing.T) {
		t.Parallel()
		cert, key, err := resolveTLS(config.Config{TLSEnabled: true})
		require.NoError(t, err)
		t.Cleanup(func() { _ = os.Remove(cert); _ = os.Remove(key) })
		require.NotEmpty(t, cert)
		require.NotEmpty(t, key)
		leaf := parseCertFile(t, cert)
		assert.Equal(t, "localhost", leaf.Subject.CommonName)
	})
}

// TestNewGeneratesTLSWhenEnabled proves the composition root wires resolveTLS:
// TLSEnabled makes New fill the cert/key file fields; the default leaves them
// empty (plain HTTP).
func TestNewGeneratesTLSWhenEnabled(t *testing.T) {
	logger := observability.NewLogger(io.Discard, slog.LevelError, "json")

	plain, err := New(context.Background(), config.Config{
		Addr:           ":0",
		RequestTimeout: time.Second,
		ShutdownGrace:  time.Second,
		LogFormat:      "json",
	}, logger, "test")
	require.NoError(t, err)
	assert.Empty(t, plain.tlsCertFile, "no TLS by default")
	assert.Empty(t, plain.tlsKeyFile)

	secure, err := New(context.Background(), config.Config{
		Addr:           ":0",
		RequestTimeout: time.Second,
		ShutdownGrace:  time.Second,
		LogFormat:      "json",
		TLSEnabled:     true,
	}, logger, "test")
	require.NoError(t, err)
	require.NotEmpty(t, secure.tlsCertFile, "TLSEnabled must generate a cert")
	t.Cleanup(func() { _ = os.Remove(secure.tlsCertFile); _ = os.Remove(secure.tlsKeyFile) })
	assert.Equal(t, "localhost", parseCertFile(t, secure.tlsCertFile).Subject.CommonName)
}

// TestTLSSelfSignedServesHTTPSDiscovery proves the generated cert actually
// terminates TLS for the real app handler and that discovery served over HTTPS
// advertises https URLs (the ctx.TLS() scheme path).
func TestTLSSelfSignedServesHTTPSDiscovery(t *testing.T) {
	logger := observability.NewLogger(io.Discard, slog.LevelError, "json")

	application, err := New(context.Background(), config.Config{
		Addr:           ":0",
		MetricsAddr:    "",
		RequestTimeout: time.Second,
		ShutdownGrace:  time.Second,
		LogFormat:      "json",
		TLSEnabled:     true,
	}, logger, "test")
	require.NoError(t, err)
	require.NotEmpty(t, application.tlsCertFile)
	t.Cleanup(func() { _ = os.Remove(application.tlsCertFile); _ = os.Remove(application.tlsKeyFile) })

	cert, err := tls.LoadX509KeyPair(application.tlsCertFile, application.tlsKeyFile)
	require.NoError(t, err)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	srv := &http.Server{
		Handler:           application.Handler(),
		ReadHeaderTimeout: time.Second,
		TLSConfig:         &tls.Config{Certificates: []tls.Certificate{cert}},
	}
	go func() { _ = srv.ServeTLS(ln, "", "") }()
	t.Cleanup(func() { _ = srv.Close() })

	client := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}}
	url := "https://" + ln.Addr().String() + "/default/.well-known/openid-configuration"
	resp, err := client.Get(url)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	var doc struct {
		Issuer                string `json:"issuer"`
		JWKSURI               string `json:"jwks_uri"`
		AuthorizationEndpoint string `json:"authorization_endpoint"`
		TokenEndpoint         string `json:"token_endpoint"`
	}
	require.NoError(t, json.Unmarshal(body, &doc))
	assert.Equal(t, "https://"+ln.Addr().String()+"/default", doc.Issuer)
	for _, u := range []string{doc.JWKSURI, doc.AuthorizationEndpoint, doc.TokenEndpoint} {
		assert.Truef(t, len(u) >= 6 && u[:6] == "https:", "endpoint must be https: %q", u)
	}
}
