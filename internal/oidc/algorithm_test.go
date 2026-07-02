package oidc_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/mock-oidc/internal/oidc"
)

// TestParseSigningAlgorithmAccepts confirms every supported algorithm parses and
// round-trips, and that RS256 is the default.
func TestParseSigningAlgorithmAccepts(t *testing.T) {
	t.Parallel()

	assert.Equal(t, oidc.RS256, oidc.DefaultSigningAlgorithm)

	for _, a := range oidc.SupportedSigningAlgorithms() {
		got, err := oidc.ParseSigningAlgorithm(string(a))
		require.NoErrorf(t, err, "ParseSigningAlgorithm(%q)", a)
		assert.Equal(t, a, got)
	}
}

// TestParseSigningAlgorithmRejects is the rejection table: known-but-refused
// algorithms (EdDSA, ES256K, ES512, HMAC family, none) all fail with the exact
// upstream wording and wrap ErrUnsupportedAlgorithm.
func TestParseSigningAlgorithmRejects(t *testing.T) {
	t.Parallel()

	rejected := []string{"EdDSA", "ES256K", "ES512", "HS256", "HS384", "HS512", "none", "", "RSA1_5"}
	for _, s := range rejected {
		got, err := oidc.ParseSigningAlgorithm(s)
		require.Errorf(t, err, "expected %q to be rejected", s)
		assert.Empty(t, string(got))
		require.ErrorIsf(t, err, oidc.ErrUnsupportedAlgorithm, "%q should wrap ErrUnsupportedAlgorithm", s)
		assert.Equal(t, "Unsupported algorithm: "+s, err.Error())
	}
}

// TestSupportedSigningAlgorithmsIsCloned proves the accessor returns a copy so
// callers cannot mutate the single source of truth.
func TestSupportedSigningAlgorithmsIsCloned(t *testing.T) {
	t.Parallel()

	first := oidc.SupportedSigningAlgorithms()
	require.NotEmpty(t, first)
	assert.Equal(t, oidc.ES256, first[0], "EC family is advertised first")

	first[0] = oidc.SigningAlgorithm("MUTATED")
	second := oidc.SupportedSigningAlgorithms()
	assert.Equal(t, oidc.ES256, second[0], "source of truth must be unaffected by caller mutation")
}
