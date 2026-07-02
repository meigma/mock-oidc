//go:build integration

package integration

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/url"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// TestContainerDelegationGrants is the Slice 4 R3 parity test: it boots the
// shipped mock-oidc:dev image with ZERO config, then drives the three delegation
// / legacy grants end-to-end the way a stock client would, verifying every minted
// signature against ONLY the served JWKS.
//
//   - token-exchange (RFC 8693): a client_credentials access token is exchanged
//     for a new access token bound to audience=https://rs.example. The response is
//     a Bearer token carrying issued_token_type, with NO id_token / refresh_token /
//     scope; the subject_token's claims are copied verbatim (same sub) but re-
//     stamped with a fresh iss/exp/jti and the requested audience. The subject
//     token is parsed but never signature-verified; the exchange nonetheless
//     requires SOME client authentication, so the request presents
//     client_secret_post.
//   - jwt-bearer OBO (RFC 7523): a self-minted assertion (signed with a throwaway
//     key the server never sees) mints an access token whose sub is copied from the
//     assertion and whose signature verifies against the published JWKS — proving
//     the assertion signature is not verified while the issued token is genuine.
//   - password (ROPC): username=alice with an arbitrary, never-validated password
//     mints an id_token AND an access_token (no refresh_token), both with sub=alice.
//
// The configured-callback-audience precedence case (a callback with its own
// configured audience overriding the request `audience` param) is covered at R2
// in-process (token_delegation_test.go) rather than here, because seeding a
// TokenCallback from config is a Slice 5 deliverable — a zero-config container
// has no configured-audience callback to exercise.
//
// It skips loudly when the mock-oidc:dev image is not present locally.
func TestContainerDelegationGrants(t *testing.T) {
	ctx := context.Background()

	skipIfImageMissing(ctx, t)

	req := testcontainers.ContainerRequest{
		Image:           imageTag,
		ExposedPorts:    []string{apiPort},
		AlwaysPullImage: false,
		WaitingFor: wait.ForHTTP("/isalive").
			WithPort(apiPort).
			WithStartupTimeout(60 * time.Second),
	}

	ctr, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	require.NoError(t, err, "boot %s with zero config", imageTag)
	t.Cleanup(func() {
		if terr := ctr.Terminate(context.Background()); terr != nil {
			t.Logf("terminate container: %v", terr)
		}
	})

	host, err := ctr.Host(ctx)
	require.NoError(t, err)
	mapped, err := ctr.MappedPort(ctx, "8080")
	require.NoError(t, err)
	base := "http://" + host + ":" + mapped.Port()

	// 1. Self-configure from discovery + JWKS.
	var disco struct {
		Issuer        string `json:"issuer"`
		TokenEndpoint string `json:"token_endpoint"`
		JWKSURI       string `json:"jwks_uri"`
	}
	getJSON(ctx, t, base+"/default/.well-known/openid-configuration", &disco)
	require.NotEmpty(t, disco.TokenEndpoint)
	keys := fetchJWKSKeys(ctx, t, disco.JWKSURI)
	require.NotEmpty(t, keys, "JWKS carries at least one key")

	// 2. Mint a client_credentials access token — the subject_token to be exchanged.
	var cc struct {
		AccessToken string `json:"access_token"`
	}
	postToken(ctx, t, disco.TokenEndpoint, url.Values{
		"grant_type": {"client_credentials"},
		"client_id":  {"svc"},
	}, &cc)
	require.NotEmpty(t, cc.AccessToken)
	ccClaims := verifiedClaims(t, keys, disco.Issuer, cc.AccessToken)
	require.Equal(t, "svc", ccClaims["sub"])

	// 3. token-exchange: audience-bind the client_credentials token. Client auth is
	//    mandatory (RFC 8693 / catalog line 109); client_secret_post satisfies it.
	status, body := postTokenForm(ctx, t, disco.TokenEndpoint, url.Values{
		"grant_type":         {"urn:ietf:params:oauth:grant-type:token-exchange"},
		"subject_token":      {cc.AccessToken},
		"subject_token_type": {"urn:ietf:params:oauth:token-type:access_token"},
		"audience":           {"https://rs.example"},
		"client_id":          {"svc"},
		"client_secret":      {"anything-never-validated"},
	})
	require.Equalf(t, 200, status, "token-exchange: %s", body)

	var exch map[string]any
	require.NoError(t, json.Unmarshal(body, &exch))
	assert.Equal(t, "Bearer", exch["token_type"])
	assert.Equal(t, "urn:ietf:params:oauth:token-type:access_token", exch["issued_token_type"],
		"token-exchange stamps issued_token_type")
	assert.NotContains(t, exch, "id_token", "token-exchange issues no id_token")
	assert.NotContains(t, exch, "refresh_token", "token-exchange issues no refresh_token")
	assert.NotContains(t, exch, "scope", "token-exchange never echoes scope")

	exchTok, _ := exch["access_token"].(string)
	require.NotEmpty(t, exchTok)
	exchClaims := verifiedClaims(t, keys, disco.Issuer, exchTok)
	assert.Equal(t, "svc", exchClaims["sub"], "subject_token claims are copied verbatim")
	assert.Equal(t, []any{"https://rs.example"}, exchClaims["aud"],
		"aud is re-bound to the requested audience (no configured callback audience)")
	assert.Equal(t, disco.Issuer, exchClaims["iss"], "iss is re-stamped to this issuer")
	assert.NotEqual(t, ccClaims["jti"], exchClaims["jti"], "a fresh jti is minted")
	assert.Contains(t, exchClaims, "exp", "a fresh exp is stamped")

	// 4. jwt-bearer OBO: a self-minted assertion (signed with a throwaway key the
	//    server never verifies) mints an access token whose sub is copied verbatim.
	assertion := mintAssertion(t, "alice", disco.TokenEndpoint)
	status, body = postTokenForm(ctx, t, disco.TokenEndpoint, url.Values{
		"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"},
		"assertion":  {assertion},
		"scope":      {"openid"},
		"client_id":  {"svc"},
	})
	require.Equalf(t, 200, status, "jwt-bearer: %s", body)

	var obo map[string]any
	require.NoError(t, json.Unmarshal(body, &obo))
	assert.Equal(t, "Bearer", obo["token_type"])
	assert.NotContains(t, obo, "id_token", "jwt-bearer issues no id_token")
	assert.NotContains(t, obo, "issued_token_type", "jwt-bearer stamps no issued_token_type")
	oboTok, _ := obo["access_token"].(string)
	require.NotEmpty(t, oboTok)
	oboClaims := verifiedClaims(t, keys, disco.Issuer, oboTok)
	assert.Equal(t, "alice", oboClaims["sub"],
		"the assertion's sub is copied verbatim (assertion signature never verified)")

	// 5. password (ROPC): any password is accepted; mints id_token + access_token
	//    (no refresh_token), both with sub = username.
	status, body = postTokenForm(ctx, t, disco.TokenEndpoint, url.Values{
		"grant_type": {"password"},
		"username":   {"alice"},
		"password":   {"any-password-never-validated"},
		"scope":      {"openid"},
		"client_id":  {"svc"},
	})
	require.Equalf(t, 200, status, "password: %s", body)

	var ropc map[string]any
	require.NoError(t, json.Unmarshal(body, &ropc))
	assert.NotContains(t, ropc, "refresh_token", "password grant issues no refresh_token")
	idTok, _ := ropc["id_token"].(string)
	accTok, _ := ropc["access_token"].(string)
	require.NotEmpty(t, idTok, "password grant mints an id_token")
	require.NotEmpty(t, accTok, "password grant mints an access_token")
	assert.Equal(t, "alice", verifiedClaims(t, keys, disco.Issuer, idTok)["sub"])
	assert.Equal(t, "alice", verifiedClaims(t, keys, disco.Issuer, accTok)["sub"])
}

// verifiedClaims verifies a token's signature against ONLY the published JWKS
// (plus issuer) and returns its full claim set, so a delegation-grant test can
// assert on the copied/re-stamped claims exactly as a stock verifier would read
// them.
func verifiedClaims(t *testing.T, keys map[string]*rsa.PublicKey, issuer, token string) jwt.MapClaims {
	t.Helper()

	claims := jwt.MapClaims{}
	parsed, err := jwt.ParseWithClaims(token, claims,
		func(tok *jwt.Token) (any, error) {
			kid, _ := tok.Header["kid"].(string)
			key, ok := keys[kid]
			if !ok {
				return nil, jwt.ErrTokenUnverifiable
			}
			return key, nil
		},
		jwt.WithValidMethods([]string{"RS256"}),
		jwt.WithIssuer(issuer),
	)
	require.NoError(t, err, "stock JWT client verifies the minted token against the published JWKS")
	require.True(t, parsed.Valid)
	return claims
}

// mintAssertion self-signs a jwt-bearer assertion with a throwaway RSA key the
// server never sees — the OBO grant PARSES the assertion but never verifies its
// signature, so the key is deliberately unrelated to the server's JWKS.
func mintAssertion(t *testing.T, sub, audience string) string {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	now := time.Now()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss": "obo-client",
		"sub": sub,
		"aud": audience,
		"iat": now.Unix(),
		"exp": now.Add(5 * time.Minute).Unix(),
	})
	signed, err := tok.SignedString(key)
	require.NoError(t, err)
	return signed
}
