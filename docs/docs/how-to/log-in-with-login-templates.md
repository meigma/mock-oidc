---
title: Log in with login templates
description: Define named login principals in config, pick them from the login page, or complete a login headlessly with login_hint — no browser required.
---

# Log in with login templates

Login templates are named principals — `{name, subject, claims}` — declared in
the JSON config. Once configured, the same template works two ways: a human
picks it from a dropdown on the interactive login page, and an automated test
names it with `login_hint` on the `authorize` request to complete the login
**headlessly** — no form, no browser, no pre-arrangement call.

Templates are a mock-oidc extension (upstream `mock-oauth2-server` has no
equivalent). For the key's exact shape see the
[configuration reference](../reference/configuration.md); for the surrounding
flow see [Drive the authorization-code flow](drive-the-authorization-code-flow.md).

## Define templates in the config

Add a top-level `loginTemplates` array. Names must be unique and non-empty;
`subject` becomes the token `sub`; `claims` is an optional JSON object merged
into the minted tokens:

```json
{
  "interactiveLogin": true,
  "loginTemplates": [
    {
      "name": "admin-alice",
      "subject": "alice",
      "claims": { "email": "alice@example.com", "roles": ["admin"] }
    },
    { "name": "basic-bob", "subject": "bob" }
  ]
}
```

For the standard container setup, mount the file and point `JSON_CONFIG_PATH`
at it:

```sh
docker run --rm -p 8080:8080 \
  -e JSON_CONFIG_PATH=/config.json \
  -v "$(pwd)/config.json:/config.json:ro" \
  ghcr.io/meigma/mock-oidc
```

A bad template — blank name or subject, or a duplicate name — fails startup
with an error naming the offending entry, so a typo never ships silently.

## Pick a template on the login page

When templates are configured, the interactive login page grows a **Template**
dropdown listing them by name. Selecting one pre-fills the username and claims
fields; both stay **editable**, so you can tweak the identity before signing
in. The form submit is unchanged — the dropdown is purely a pre-fill.

With no templates configured, the page renders exactly as before.

## Log in headlessly with login_hint

Name a template in the standard `login_hint` parameter on `GET /authorize` and
the server resolves it immediately — the code comes back in the `302` without a
login page, **even when `interactiveLogin: true` or `prompt=login` would
otherwise force one**:

```sh
curl -i "http://localhost:8080/default/authorize?\
response_type=code&client_id=test-client&\
redirect_uri=http://localhost:3000/callback&scope=openid&state=xyz&\
login_hint=admin-alice"
#   => HTTP/1.1 302 Found
#   => Location: http://localhost:3000/callback?code=<code>&state=xyz
```

Exchange the code as usual; the tokens carry the template identity:

```sh
curl -s http://localhost:8080/default/token \
  -d grant_type=authorization_code \
  -d code="$code" \
  -d client_id=test-client
#   => id_token payload: {"sub":"alice","email":"alice@example.com","roles":["admin"],...}
```

A `login_hint` that names no configured template fails **loudly** as
`invalid_request` rather than falling through to a login page or a default
identity — an automated suite sees the typo immediately:

```sh
curl -si "http://localhost:8080/default/authorize?\
response_type=code&client_id=test-client&\
redirect_uri=http://localhost:3000/callback&login_hint=nobody" \
  | grep -i '^location:'
#   => location: http://localhost:3000/callback?error=invalid_request&error_description=...
```

## Use it from an automated test

`login_hint` is a standard OIDC parameter, so any off-the-shelf client library
can send it without custom HTTP code — configure the library's authorize
request with `login_hint=<template-name>` and run the flow normally. Because
the template is selected *by the client at flow time*, parallel tests never
contend over shared server state; contrast the one-shot
[scenario queue](shape-token-claims.md), which pre-arranges the *next* token
server-side.

!!! note "Semantics to know"
    - **Templates are global**, not per-issuer: the same names resolve on every
      issuer.
    - **The hint only acts while templates are configured.** With an empty or
      absent `loginTemplates`, `login_hint` is ignored entirely.
    - **Template claims merge like login claims**: they are added only where a
      token callback or registered claim (like `sub`) has not already set the
      value. See [Shape token claims](shape-token-claims.md) for the resolution
      order.
