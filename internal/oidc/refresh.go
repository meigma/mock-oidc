package oidc

// RefreshToken is the opaque wire refresh-token string. The domain treats it as
// a value it stores and compares; it is *produced by the refresh-store adapter*
// (a bare UUID, or an unsigned alg=none PlainJWT when a nonce is present). The
// domain never hand-formats the JWT — it only selects the RefreshFormat.
type RefreshToken string

// RefreshFormat is the closed choice the domain makes; the adapter renders the
// bytes. The domain never serializes either form (§7 invariant).
type RefreshFormat int

// The two refresh-token wire forms.
const (
	RefreshBareUUID RefreshFormat = iota // grant had no nonce, or rotation
	RefreshPlainJWT                      // unsigned alg=none {jti, nonce} — nonce present
)

// ChooseRefreshFormat is the domain's form-selection rule: a bare UUID unless a
// nonce is present, in which case the Keycloak-JS accommodation form (an
// unsigned alg=none PlainJWT carrying the nonce) is chosen. The adapter reads
// this to decide how to materialize the token; the domain never serializes it.
func ChooseRefreshFormat(nonce *Nonce) RefreshFormat {
	if nonce != nil {
		return RefreshPlainJWT
	}
	return RefreshBareUUID
}

// RefreshRecord binds a refresh token to the issuer that minted it and the
// callback that reproduces its claims. Issuer is the field the strict (4.0.0+)
// cross-issuer check reads. Format records which wire form the adapter must
// render; rotation (rotateRefreshToken) re-issues as RefreshBareUUID and drops
// the nonce. Redemption of the record lands in Slice 3; Slice 2 only persists it.
type RefreshRecord struct {
	Issuer   IssuerID
	Subject  Subject
	Nonce    *Nonce
	Format   RefreshFormat
	Callback TokenCallback // claim/subject/aud policy to replay on refresh
}
