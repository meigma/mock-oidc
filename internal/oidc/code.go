package oidc

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"slices"
)

// CodeChallengeMethod is the closed PKCE transform (RFC 7636): plain echoes the
// verifier; S256 is base64url(sha256(verifier)). It is the ONLY crypto the core
// touches — a pure, keyless transform under the documented
// crypto/sha256+crypto/subtle+encoding/base64 carve-out (see doc.go), never
// key-bearing signing.
type CodeChallengeMethod string

// The two PKCE code-challenge methods this server understands.
const (
	ChallengePlain CodeChallengeMethod = "plain"
	ChallengeS256  CodeChallengeMethod = "S256"
)

// allCodeChallengeMethods is the authoritative membership list; Valid derives
// from it.
//
//nolint:gochecknoglobals // single source of truth for the closed PKCE method set (RFC 7636).
var allCodeChallengeMethods = []CodeChallengeMethod{ChallengePlain, ChallengeS256}

// Valid reports whether m is a member of the closed PKCE method set.
func (m CodeChallengeMethod) Valid() bool {
	return slices.Contains(allCodeChallengeMethods, m)
}

// Compute transforms verifier into the challenge value this method expects:
// plain returns the verifier unchanged; S256 returns
// base64url(sha256(verifier)) with no padding. An unrecognized method returns
// the empty string, which never equals a well-formed challenge, so verification
// fails closed.
func (m CodeChallengeMethod) Compute(verifier string) string {
	switch m {
	case ChallengePlain:
		return verifier
	case ChallengeS256:
		sum := sha256.Sum256([]byte(verifier))
		return base64.RawURLEncoding.EncodeToString(sum[:])
	default:
		return ""
	}
}

// AuthorizationCode is the opaque, single-use handle returned from /authorize
// and redeemed once at /token. The core treats it as a value it stores and
// compares; the ID source produces its bytes.
type AuthorizationCode string

// PKCEChallenge pairs a stored code_challenge with its method. Verification is a
// pure, keyless transform enforced only when a verifier is actually presented
// (a bare challenge need not be redeemed); a failed check is the caller's signal
// to invalidate the code.
type PKCEChallenge struct {
	Challenge string
	Method    CodeChallengeMethod
}

// CodeRecord is the request snapshot cached against an AuthorizationCode at
// /authorize and consumed once at /token. It is the authority for nonce, PKCE,
// and any interactive-login claims — none of which the token request can supply.
// RedirectURI is captured but intentionally NOT validated (documented parity
// behavior).
type CodeRecord struct {
	Issuer       IssuerID
	Client       Client
	RedirectURI  string // captured, intentionally NOT validated (parity)
	Scope        Scopes
	Nonce        *Nonce
	Challenge    *PKCEChallenge // nil when the request carried no PKCE
	ResponseMode ResponseMode
	Login        *LoginSubmission // present for interactive-login codes
	IssuedAt     Instant
}

// NewCodeRecord assembles the snapshot cached under a fresh AuthorizationCode.
// nonce and the PKCE challenge come from the authorize request; login is stored
// only when it carries a username (interactive codes), so a non-interactive
// direct-code path leaves Login nil.
func NewCodeRecord(
	snap AuthorizeSnapshot,
	nonce *Nonce,
	pkce *PKCEChallenge,
	login LoginSubmission,
	issuedAt Instant,
) CodeRecord {
	rec := CodeRecord{
		Issuer:       snap.Issuer,
		Client:       snap.Client,
		RedirectURI:  snap.RedirectURI,
		Scope:        snap.Scope,
		Nonce:        nonce,
		Challenge:    pkce,
		ResponseMode: snap.ResponseMode,
		Login:        nil,
		IssuedAt:     issuedAt,
	}
	if login.Username != "" {
		captured := login
		rec.Login = &captured
	}
	return rec
}

// VerifyPKCE checks a presented code_verifier against the cached challenge.
// The semantics are asymmetric: it is a no-op only when NEITHER a verifier nor a
// stored challenge is present; a challenge without a verifier (or a verifier
// without a challenge) is invalid_grant; a verifier that does not compute to the
// stored challenge is the invalid_pkce case. The compare is constant-time.
func (rec CodeRecord) VerifyPKCE(verifier string) error {
	switch {
	case rec.Challenge == nil && verifier == "":
		return nil // no PKCE in play on either side
	case rec.Challenge == nil:
		return InvalidGrant("code_verifier was not expected: no code_challenge was registered")
	case verifier == "":
		return InvalidGrant("code_verifier required: a code_challenge was registered")
	default:
		computed := rec.Challenge.Method.Compute(verifier)
		if subtle.ConstantTimeCompare([]byte(computed), []byte(rec.Challenge.Challenge)) != 1 {
			return PKCEFailed()
		}
		return nil
	}
}

// CallbackInput projects the cached record and the token request into the
// transport-free view a TokenCallback matches against. The subject is sourced
// from the CACHED login (not the token request); nonce and login claims are the
// record's, so the token request cannot forge them.
func (rec CodeRecord) CallbackInput(req TokenRequest) CallbackInput {
	in := CallbackInput{
		Grant:    req.Grant,
		Client:   req.Client,
		Scopes:   rec.Scope,
		Subject:  "",
		Params:   nil,
		Audience: nil,
	}
	if rec.Login != nil {
		in.Subject = rec.Login.Username
	}
	return in
}

// loginClaims returns the interactive-login claims cached with this code, or an
// empty set when the code was issued without a login (the direct-code path).
// They are merged add-only into the minted tokens at exchange time.
func (rec CodeRecord) loginClaims() CustomClaims {
	if rec.Login == nil {
		return CustomClaims{}
	}
	return rec.Login.Claims
}
