package cli

import (
	"testing"
	"time"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"

	"github.com/meigma/mock-oidc/internal/config"
)

// TestRunServeSSLConfigOrder mirrors runServe's config assembly to pin the
// ssl:{}-turns-TLS-on ordering invariant: Validate runs on the flag/env Config
// (where a no-cert TLS request would trip the cert-and-key clause), and only
// AFTER Validate is the JSON ssl:{} flag ORed into TLSEnabled — so a JSON-enabled,
// no-cert TLS run validates cleanly and then self-signs in app.New (the
// generation itself is proven by the internal/app TLS tests).
func TestRunServeSSLConfigOrder(t *testing.T) {
	t.Parallel()

	vp := viper.New()
	vp.Set("json-config", `{"httpServer":{"ssl":{}}}`)
	vp.Set("request-timeout", time.Second)
	vp.Set("shutdown-grace", time.Second)

	cfg := config.Load(vp)
	require.False(t, cfg.TLSEnabled, "ssl:{} is not on the flag/env Config yet")
	require.NoError(t, cfg.Validate(), "no-cert TLS must not trip the cert+key clause before the OR")

	seed, err := config.LoadSeed(vp)
	require.NoError(t, err)
	require.True(t, seed.TLSFromHTTPServer, "ssl:{} sets the seed TLS flag")

	cfg.TLSEnabled = cfg.TLSEnabled || seed.TLSFromHTTPServer
	require.True(t, cfg.TLSEnabled, "ssl:{} turns TLS on after the OR")
}
