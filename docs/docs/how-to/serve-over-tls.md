---
title: Serve over TLS
description: Run mock-oidc over HTTPS with a self-signed localhost certificate or your own cert/key pair.
---

# Serve over TLS

Run mock-oidc over HTTPS so your client talks to `https://` endpoints. There are
two ways to terminate TLS in-process: a throwaway self-signed localhost
certificate, or your own certificate and key. Pick one below.

Over HTTPS every advertised discovery URL — `issuer`, every `*_endpoint`, and
`jwks_uri` — is derived as `https://`, so a client that reads discovery follows
the correct scheme automatically.

## Method 1: self-signed localhost certificate

For local development, have mock-oidc generate a self-signed certificate in
process. Enable it through the JSON config `httpServer.ssl` object (an empty
object is enough; this matches upstream's `ssl: {}`):

```sh
JSON_CONFIG='{"httpServer":{"ssl":{}}}' ./bin/mock-oidc serve
```

Then hit discovery over HTTPS. The certificate is untrusted, so pass `-k`
(`--insecure`) to skip verification:

```sh
curl -k https://localhost:8080/default/.well-known/openid-configuration
#   => { "issuer": "https://localhost:8080/default",
#        "authorization_endpoint": "https://localhost:8080/default/authorize",
#        ...
#        "jwks_uri": "https://localhost:8080/default/jwks", ... }
```

Every URL in the body is `https://`.

The generated certificate carries Subject Alternative Names `localhost`,
`127.0.0.1`, and `::1`, so it validates for those hosts once trusted. In your
client, either disable verification (the `curl -k` / `--insecure` equivalent) or
add the certificate to that client's trust store.

!!! warning "Testing only"
    The in-process self-signed certificate exists to unblock local HTTPS
    testing. Do not add it to a shared or system trust store, and never rely on
    it for anything beyond a test client.

`JSON_CONFIG` is one of several config sources; you can equally point
`JSON_CONFIG_PATH` at a file or drop the `ssl` block into `./config.json`. See
[Configuration](../reference/configuration.md) for the full precedence and
schema.

## Method 2: your own certificate and key

To serve a certificate you already have, pass both `--tls-cert-file` and
`--tls-key-file`. They are required together — supplying one without the other
is an error.

```sh
./bin/mock-oidc serve \
  --tls-cert-file ./tls/server.crt \
  --tls-key-file  ./tls/server.key
```

```sh
curl https://localhost:8080/default/.well-known/openid-configuration
#   => { "issuer": "https://localhost:8080/default", ... }
```

If your certificate is issued by a CA the client already trusts, no `-k` is
needed. Each flag also has a `MOCK_OIDC_*` environment variable
(`MOCK_OIDC_TLS_CERT_FILE`, `MOCK_OIDC_TLS_KEY_FILE`), which is convenient for
containers.

## What TLS covers

TLS terminates on the **API listener only** — the issuer endpoints under
`/{issuer}/` and the `/static/*` mounts. The following stay plain HTTP by
design and are unaffected by either method above:

- `/metrics` on its dedicated listener (default `:9090`)
- the `/_mock` control plane

Point your metrics scraper and control-plane tooling at `http://`, and your
OAuth2/OIDC client at `https://`.

## Terminating TLS elsewhere

If you would rather present HTTPS at a reverse proxy (or an ingress / sidecar)
and keep mock-oidc itself on plain HTTP, terminate at the proxy and forward
`X-Forwarded-Proto: https` so the advertised URLs still come out `https://`.
That setup is covered in
[Run behind a proxy or in Docker](run-behind-a-proxy-or-in-docker.md).
