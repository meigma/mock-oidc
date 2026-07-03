---
title: The security model
description: Why mock-oidc is safe as a test tool and dangerous anywhere else, and the design choices that keep the two apart.
---

# The security model

mock-oidc has an unusual security posture: it is safe precisely because it is
insecure, and it is insecure on purpose. It will mint a valid, cryptographically
signed token for any identity you ask for, and it will never check a client
secret or a password before doing so. That behaviour is the whole product. It is
also exactly what would make it catastrophic anywhere near production. This page
explains why those two facts are the same fact, and what the server does to keep
the line between "test fixture" and "open oracle" from being crossed by accident.

!!! warning "FOR TESTING ONLY"
    mock-oidc is a test tool. It authenticates nobody and authorizes everything.
    Never place it in front of production traffic, and never expose it on a
    network where an untrusted party can reach it.

## Credentials are never checked, on purpose

A real identity provider exists to answer one question: *is this caller who they
claim to be?* mock-oidc exists to answer the opposite need: *let this test
pretend to be anyone, instantly, without standing up an identity provider.* Those
goals are irreconcilable. A test that had to present a valid client secret or a
correct password to obtain a token would need a real credential store, real user
provisioning, and real secret management — the very machinery the mock is meant
to let you skip.

So the mock skips the check. Client authentication is accepted in every documented
form (`client_secret_basic`, `client_secret_post`, `private_key_jwt`) and then
never validated — the `client_secret_basic` and `client_secret_post` secrets are
discarded outright. The password grant accepts any password. The `private_key_jwt`
and `jwt-bearer` and `token-exchange` assertions are *parsed* for their structure
and claims but their signatures are never verified — a dummy signature works. This is not an oversight to be hardened later; verifying any of
it would defeat the purpose. The value of the tool is that a test can drive any
identity and any scenario by simply asking, and asking is the only credential.

It helps to be precise about *which* checks are dropped. The server is blind to
the inputs that prove **authorization** — secrets, passwords, assertion
signatures — because gatekeeping is not its job. It remains strict about the one
thing that makes a token a token: **cryptographic integrity**. Tokens it issues
are properly signed, `userinfo` refuses a Bearer token whose signature it cannot
verify, introspection reports an unverifiable token as inactive, and `alg=none`
is rejected on the verifying side. The mock drops the checks that would get in a
test's way and keeps the checks that make its tokens real.

## The tokens are real, and that is the danger

The tokens are not stubs or fixtures with a recognizable "fake" shape. They are
ordinary JWTs signed with an ordinary key, and they verify against the server's
published JWKS using any standard OIDC library — the same code path your client
uses against your real provider. That realism is the entire reason an *unmodified*
client can complete a sign-in against the mock: nothing in the client has to be
told it is talking to a test double, because at the protocol level it is not
talking to anything unusual. It fetches discovery, fetches JWKS, validates the
signature and the `iss`, and is satisfied — exactly as it would be with a
production IdP.

That same realism is why the server belongs only in a closed environment. A
mock-oidc instance is, functionally, a machine that hands out genuine, signed
bearer tokens for the identity "admin" (or any other) to whoever asks. Any
service configured to trust its JWKS will honour those tokens. There is no
weaker, sandboxed variant of a "real signed token" — a token strong enough to
satisfy an unmodified client is strong enough to be dangerous if the audience for
it is not confined to your test suite. The feature and the hazard are the same
property viewed from two sides.

## Why it announces itself so loudly

Because the danger is invisible at the protocol level — a mock token looks like a
real one — the server makes its nature visible out of band. On **every** startup
it logs a "FOR TESTING ONLY" banner, so a mock instance that has drifted into an
environment where someone forgot what it is announces itself in the logs rather
than blending in. Every response from the `/_mock` control plane carries the
header `X-Mock-Oidc: testing-only`, so any traffic capture or proxy that sees a
control-plane response can recognise what it is dealing with. These are not
security controls — they stop no attacker — but they are honesty about identity,
which is the most useful thing a component this dangerous can offer to the humans
operating it.

## Safe by default, despite the "mock anything" stance

The permissiveness is deliberately confined to the OAuth2 semantics — identities,
secrets, passwords. Around that core, the boundary behaviours lean *toward* safe
defaults rather than maximal openness, because there is no test-ergonomics reason
for them to be loose.

- **CORS reflects, but never wildcards.** With no allowlist configured the server
  reflects each request's `Origin` back verbatim (with credentials allowed), so a
  browser-based suite from any origin just works. But it never emits
  `Access-Control-Allow-Credentials: true` alongside a `*` wildcard — a
  combination browsers reject and a habit worth not forming. Echoing the exact
  origin keeps the response correct rather than blanket-open, and an allowlist
  tightens it further when you want that.
- **Client IP comes from the TCP peer, not from a header.** The advertised issuer
  URLs *do* follow `X-Forwarded-*`, because presenting the right identity behind a
  proxy is core to how the mock is deployed. But the client IP used for logging is
  read from the actual TCP peer and does **not** implicitly trust
  `X-Forwarded-For`; honouring a forwarded client header is opt-in, via a named
  trusted-proxy header. Identity-of-the-server is derived from forwarded headers;
  trust-of-the-caller is not handed to them for free.
- **Rate limiting is off.** A test suite firing thousands of token requests should
  never be throttled, so the limiter is disabled by default. This is a
  test-first default, not a security stance — it exists precisely because the
  server is not meant to face hostile load. It can be turned on, but the honest
  mitigation for abuse is network isolation, not a rate limiter.
- **The control plane can be closed.** `/_mock` — which can mint arbitrary tokens
  outright — is on by default for zero-config convenience, but it can be gated
  behind a bearer token (compared in constant time) or disabled entirely so it
  `404`s. In a shared or CI environment, closing or tokening it is the difference
  between "a test helper" and "an open token-minting endpoint."

None of these turn mock-oidc into something safe to expose. They reduce the
number of ways a *correctly isolated* deployment can still surprise you, and they
avoid teaching bad habits (wildcard CORS, blind header trust) that might migrate
into real code.

## Where the line actually is

The load-bearing control is not any single flag; it is the network boundary. The
right place to run mock-oidc is a closed test environment — a CI job, a developer
laptop, a container network your suite owns — where the only clients that can
reach it are the ones you are testing. Every choice above assumes that boundary
exists. The banner and the `testing-only` header help you notice when it does
not; the CORS, client-IP, and control-plane defaults reduce the blast radius if
something slips; but nothing substitutes for keeping the server unreachable by
anyone you would not hand an "admin" token.

For the concrete steps to gate or disable the control plane, see
[Lock down the control plane](../how-to/lock-down-the-control-plane.md). For the
full set of flags and their defaults — CORS allowlists, the trusted-proxy header,
rate-limit and control-plane settings — see the
[Configuration reference](../reference/configuration.md).
