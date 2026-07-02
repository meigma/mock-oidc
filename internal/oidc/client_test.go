package oidc_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/mock-oidc/internal/oidc"
)

// TestValidatePrivateKeyJWT pins the six structural rules of the token-exchange
// private_key_jwt client assertion — each failure is invalid_request (400) — plus
// the passing rows. The assertion signature is never checked; only its decoded
// claims are inspected.
func TestValidatePrivateKeyJWT(t *testing.T) {
	t.Parallel()

	const (
		clientID   = oidc.ClientID("app")
		issuerURL  = "http://localhost:8080/default"
		tokenEPURL = "http://localhost:8080/default/token"
	)
	now := oidc.NewInstant(time.Unix(1_700_000_000, 0))
	iat := now
	exp := now.Add(60 * time.Second) // 60s lifetime — within the 120s cap

	// valid is the baseline structurally-sound assertion; each case mutates a copy.
	valid := oidc.ClaimSet{
		Issuer:   string(clientID),
		Subject:  oidc.Subject(clientID),
		Audience: oidc.Audience{issuerURL},
		IssuedAt: iat,
		Expiry:   exp,
	}

	tests := []struct {
		name    string
		mutate  func(c *oidc.ClaimSet)
		wantErr bool
	}{
		{"valid (aud = issuer url)", func(*oidc.ClaimSet) {}, false},
		{"valid (aud = token endpoint url)", func(c *oidc.ClaimSet) {
			c.Audience = oidc.Audience{tokenEPURL}
		}, false},
		{"lifetime exceeds 120s", func(c *oidc.ClaimSet) {
			c.Expiry = iat.Add(121 * time.Second)
		}, true},
		{"iss != client_id", func(c *oidc.ClaimSet) { c.Issuer = "someone-else" }, true},
		{"sub != client_id", func(c *oidc.ClaimSet) { c.Subject = "someone-else" }, true},
		{"empty audience", func(c *oidc.ClaimSet) { c.Audience = nil }, true},
		{"audience size > 1", func(c *oidc.ClaimSet) {
			c.Audience = oidc.Audience{issuerURL, tokenEPURL}
		}, true},
		{"audience not accepted", func(c *oidc.ClaimSet) {
			c.Audience = oidc.Audience{"https://evil.example"}
		}, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assertion := valid
			assertion.Audience = append(oidc.Audience(nil), valid.Audience...)
			tc.mutate(&assertion)

			err := oidc.ClientAuthPrivateKeyJWT.
				ValidatePrivateKeyJWT(assertion, clientID, issuerURL, tokenEPURL, now)

			if !tc.wantErr {
				assert.NoError(t, err)
				return
			}
			var perr *oidc.ProtocolError
			require.ErrorAs(t, err, &perr)
			assert.Equal(t, oidc.CodeInvalidRequest, perr.Code)
			assert.Equal(t, 400, perr.HTTPStatus)
		})
	}
}
