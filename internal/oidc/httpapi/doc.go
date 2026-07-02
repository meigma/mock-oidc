// Package httpapi is the driving adapter that maps inbound HTTP requests to the
// core's typed commands and renders domain results as protocol responses (the
// OAuth2/OIDC endpoints). It parses raw form/header/path bytes into typed domain
// commands at the edge (parse-don't-validate), orchestrates the provider and
// token services over their ports, and serializes results through a single
// success-shaped JSON envelope so the whole protocol surface shares one OAuth2
// error contract (never RFC 9457). Slice 1 covers discovery (+ RFC 8414 alias),
// the JWK set, and the client_credentials token endpoint; it also exports the
// OAuth2 error writer and protocol-path classifier the composition root installs
// as the generic router's protocol-family fallback.
package httpapi
