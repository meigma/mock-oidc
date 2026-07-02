package oidc

// Subject is the token `sub`. It is a distinct type from ClientID so the
// catalog's subject-resolution rules (cc -> client_id, password -> username,
// login -> username) are explicit conversions, never accidental assignments.
type Subject string

// ClientID is the request's client identifier. It is never credential-checked.
type ClientID string

// AsSubject converts a ClientID into a Subject — the client_credentials rule
// (sub defaults to client_id), written as one explicit boundary conversion.
func (c ClientID) AsSubject() Subject { return Subject(c) }

// ClientAuth records how a client presented itself. It is metadata for
// introspection/templating, never an authorization decision.
type ClientAuth string

// The client-authentication presentation methods. No secret is ever validated;
// these record only the shape of the presented credential.
const (
	ClientAuthNone              ClientAuth = "none"
	ClientAuthClientSecretBasic ClientAuth = "client_secret_basic"
	ClientAuthClientSecretPost  ClientAuth = "client_secret_post"
	ClientAuthPrivateKeyJWT     ClientAuth = "private_key_jwt"
)

// Client is the request's client identity. It is ephemeral — never stored,
// never credential-checked. Auth records only how the client presented itself,
// not whether it was authorized.
type Client struct {
	ID   ClientID
	Auth ClientAuth
}

// RequireClientID returns the effective client id or invalid_client when none
// was presented, mirroring upstream's "client_id cannot be null".
func (c Client) RequireClientID() (ClientID, error) {
	if c.ID == "" {
		return "", InvalidClient("client_id cannot be null")
	}
	return c.ID, nil
}
