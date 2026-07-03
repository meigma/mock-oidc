---
title: Issuers and advertised identity
description: Why one server can impersonate many issuers on demand, and how it decides which identity to advertise on every request.
---

# Issuers and advertised identity

An OIDC provider has an identity: a URL that names it. Clients pin that URL —
they fetch discovery and JWKS from it, and they reject any token whose `iss`
does not match it. A mock that stands in for real providers therefore has to
answer two questions convincingly. *Who* is minting this token — which issuer's
key signed it, and which trust domain does it belong to? And *where* does that
issuer live — what URL should the token claim, so the client under test
actually believes it?

mock-oidc answers the first question with a namespace and the second by
deriving the URL fresh on every request. The two mechanisms are independent,
and understanding why they are separate is the key to understanding the whole
identity model.

## One server, many issuers

Everything the server does is namespaced under a single path segment: the
issuer. Discovery, JWKS, `authorize`, `token`, `userinfo` — all of it lives
under `/{issuer}/...`. With zero configuration the server answers as one issuer
named `default`, reachable at `http://localhost:8080/default`, but that name is
not special. It is simply the issuer you get when you do not ask for another.

Issuers are not registered. They **materialize on first touch**: the first time
any request hits `/{some-id}/...`, that issuer springs into existence with a
lazily generated signing key, and every subsequent request under the same
segment shares it. There is no create step, no config entry, no restart.

This is a deliberate inversion of how a real provider works, and it is chosen
for what a test needs. A test suite wants to drive a sign-in without first
provisioning a tenant; picking an issuer name and using it *is* the
provisioning. The same property lets a single running container impersonate an
arbitrary number of independent IdPs at once — one for each name a test cares
to invent — which is exactly what you want when exercising multi-tenant
isolation, federation across several providers, or an audience matrix. The
alternative, an explicit registration API or a config block listing every
issuer up front, would buy nothing here except setup: it trades away the
zero-friction property that makes the mock worth using.

Each materialized issuer is its own trust domain. Its signing key carries
`kid` equal to the issuer id, so a verifier can route its trust off the issuer
name alone, and — more importantly — verification is isolated. A token minted
under one issuer is worthless to another: the second issuer's JWKS never
advertises the first one's key, so its `userinfo` rejects the foreign token and
its `introspect` reports it inactive. That isolation is not an add-on; it falls out of every
issuer having a distinct key and a distinct discovery document. (For the wider
consequences of "any string becomes a trusted, key-bearing issuer," see
[The security model](security-model.md).)

The cost of materialize-on-touch is that there is no such thing as a typo. Ask
for `/defalt/token` and you have not hit an error — you have created and used a
brand-new issuer named `defalt`, with its own key and its own `iss`. That is a
reasonable price for a testing tool, but it is why the model belongs to a
process you run and throw away, never one that fronts real traffic.

## The advertised-identity problem

The harder question is *where* an issuer lives. A real provider is reached at
one canonical URL and advertises exactly that. A mock is not so lucky: the
same running server is reached under several different names *at the same
time*. A developer curls it at `http://localhost:8080`. An app inside a
container reaches it through a Docker network alias or the host gateway. A
reverse proxy answers for it at `https://idp.example.com` on port 443 and
forwards inward. Each caller sees a different address — and OIDC gives the
server no room to fudge, because the client compares the `issuer` in discovery
and the `iss` in the token against *the URL it actually fetched discovery from*.
If those disagree, verification fails.

A fixed, configured base URL cannot solve this. Whatever single value you bake
in at startup is correct for exactly one of those callers and wrong for the
rest. The proxy topology wants `https://idp.example.com`; the localhost
developer wants `http://localhost:8080`; the container wants the host-gateway
name. No constant is right for all three simultaneously.

So mock-oidc does not use a constant. It derives every advertised URL — the
discovery `issuer`, every `*_endpoint`, `jwks_uri`, and the `iss` stamped into
tokens — **per request**, from the address the request appears to have arrived
at. It reads `X-Forwarded-Proto`, `X-Forwarded-Host`, and `X-Forwarded-Port`
when a proxy set them, and falls back to the request's own `Host` header when it
did not. It keeps only the host root — scheme plus authority — discarding the
request path, and then appends the issuer segment. The advertised identity, in
other words, is a function of *how you reached the server*, computed anew every
time.

That single decision is why the awkward topologies "just work" without any
matching configuration:

- **Behind a proxy**, the proxy forwards `X-Forwarded-*` describing the external
  address it answers on. A discovery request that entered as
  `X-Forwarded-Proto: https`, `X-Forwarded-Host: idp.example.com` comes back
  advertising `https://idp.example.com/default`, and tokens minted on that
  request claim the same `iss` — even though the mock's own listener is plain
  HTTP on `:8080`.
- **Across two containers**, the app under test and the browser must agree on
  one issuer URL, because the app fetches discovery and JWKS from it while the
  browser is redirected to `authorize` on it. Give both sides the same
  reachable name — `host.docker.internal:8080` is the usual one on Docker
  Desktop — and because the mock derives `iss` from the incoming `Host`, it
  advertises exactly that name back to both. Remapping the published port needs
  no configuration either: the mock sees the new port in `Host` and advertises
  it.

The mirror image of this is that the mock only advertises what it is told. If a
proxy strips the forwarded headers, the mock falls back to the `Host` it sees
and will advertise the internal address — correct behavior for a wrong input.
The identity is honest about the address the request carried, which is the most
a per-request derivation can promise. What exactly ends up in a token, `iss`
included, is catalogued in [Tokens and claims](../reference/tokens-and-claims.md);
the practical recipes live in
[Run behind a proxy or in Docker](../how-to/run-behind-a-proxy-or-in-docker.md).

## The single-segment constraint

An issuer id is one path segment. It cannot contain a `/`. This is a real
limitation with a real consequence, and it is documented here rather than
papered over, because papering over it would be worse.

Some real providers publish *nested* issuer URLs. Azure AD is the canonical
example: its issuer looks like `https://login.microsoftonline.com/{tenant}/v2.0`
— several path segments deep. mock-oidc cannot represent that shape. A request
to `/tenant/v2.0/token` does not create a nested issuer called `tenant/v2.0`;
it is read as the issuer `tenant` with `v2.0/token` as a path beneath it. There
is no configuration that changes this.

The reason is routing. Single-segment issuers map cleanly onto path-parameter
routing — `/{issuer}/...` — where the router extracts exactly one segment and
everything after it is a known, fixed endpoint. Allowing multi-segment issuers
would make the boundary between "issuer" and "endpoint" ambiguous: the router
could not tell where the issuer name ends and the OAuth path begins without
some escaping convention or greedy-matching rule, and that ambiguity would leak
into every route. The project chose the clean routing model and accepted that
Azure-style nested issuers fall outside it.

What makes this a *named* gap rather than a silent bug is that it is a
deliberate, stated boundary of intent-parity with the upstream
`mock-oauth2-server`, not an accident of implementation. The mock aims to match
what a provider is *for*, and for the vast majority of clients the issuer is an
opaque URL to compare byte-for-byte — a single flat segment serves that
perfectly. Nested issuer paths are the corner it does not reproduce, and saying
so plainly is more useful than pretending a request under a nested path did
something sensible. The full inventory of what is and isn't reproduced, and the
reasoning behind each choice, lives in
[Parity with mock-oauth2-server](parity.md).

One related reservation follows the same spirit: the `_mock` segment is taken by
the control plane, so it cannot be used as an issuer id. A protocol request
under that prefix is refused rather than quietly treated as an issuer — again,
an explicit boundary in place of a silent surprise.

## Two questions, two mechanisms

The namespace answers *who* — an isolated, key-bearing trust domain that exists
the moment you name it. The per-request derivation answers *where* — a URL that
follows the address each caller actually used. Keeping them separate is what
lets one process behave as many issuers and, for each of them, present the
right address to a proxy, a container network, and `localhost` all at once. When
you are ready to put either mechanism to work, see
[Use multiple issuers](../how-to/use-multiple-issuers.md) and
[Run behind a proxy or in Docker](../how-to/run-behind-a-proxy-or-in-docker.md).
