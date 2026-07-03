---
title: Parity with mock-oauth2-server
description: Why mock-oidc matches what navikt/mock-oauth2-server is FOR while correcting its defects, and which upstream behaviours it deliberately does not reproduce.
---

# Parity with mock-oauth2-server

mock-oidc began life as a reimplementation of [navikt/mock-oauth2-server](https://github.com/navikt/mock-oauth2-server), the JVM-based mock identity provider that many teams already reach for when they need a test suite to drive a real OAuth2/OIDC sign-in. Because so many projects already know that tool's shape, an obvious temptation is to reproduce it byte for byte — to treat every response it emits, correct or not, as a specification. mock-oidc deliberately does not do that. Its guiding principle is **parity in intent, not parity in quirks**.

The distinction matters, and this page exists to explain it: what "intent" we chose to match, which upstream behaviours we treat as defects and correct, and — just as importantly — which capabilities we consciously chose not to carry over at all. If you are here to actually perform the switch, the mechanics live in [Migrate from mock-oauth2-server](../how-to/migrate-from-mock-oauth2-server.md); this page is about *why* the switch is safe and where it isn't.

## What upstream is FOR

Before deciding what to keep, it helps to name what the upstream tool is genuinely good at — its purpose, stripped of implementation accidents. mock-oauth2-server exists so that an automated test can perform a *complete, honest* sign-in against an **unmodified** OAuth2/OIDC client. It is not a stub that returns canned strings; it mints real, cryptographically signed tokens for arbitrary identities, publishes a JWKS, serves discovery, and honours the standard grant flows. The value is that your application code under test never has to know it is talking to a fake. You point it at a mock issuer instead of the real one, and everything downstream — signature verification, claim extraction, expiry checks — runs for real.

That purpose is what mock-oidc sets out to preserve exactly. A token minted here verifies against the advertised JWKS; a full authorization-code round trip completes against a real browser client; the clock that stamps a token is the same clock that later validates it. Anywhere the two tools would produce a *materially different testing outcome* for a correct client, mock-oidc treats that as a bug to be reconciled — usually in upstream's favour, sometimes in ours where upstream is simply wrong.

!!! warning "For testing only"
    This inherited purpose carries an inherited constraint. A server whose entire job is to mint valid tokens for *any* identity with *no* secret validation is a catastrophic thing to expose to real traffic. mock-oidc, like the tool it replaces, must never front production.

## Correcting defects rather than copying them

The interesting cases are where upstream's *observable* behaviour diverges from what the specifications — or plain good sense — call for. Faithfully reproducing those would mean importing bugs into a fresh codebase and asking every future user to work around them forever. Instead, each was examined on its merits, and where upstream is demonstrably wrong, mock-oidc corrects it. The corrections are small in number and each has a concrete rationale.

**OAuth2 error codes keep their correct case.** RFC 6749 defines error codes such as `invalid_request`, `invalid_grant`, and `invalid_client` as exact tokens. Upstream lowercases error bodies in a way that can mangle them; mock-oidc emits them verbatim. A client that switches on the error code — the entire reason the field exists — should not have to normalise case first, and a test asserting on the spec-defined value should pass.

**A `form_post` response with no `state` is tolerated.** The `form_post` response mode is legitimately used without a `state` parameter; `state` is optional. Upstream could fault on that combination and return a 500. A missing optional parameter is not a server error, so mock-oidc renders the self-submitting form regardless. The absence of `state` simply means it is omitted from the posted body, which is exactly what the spec implies.

**There is no 302-to-400 status coercion.** In some error situations upstream rewrites what should be a redirect-carried error into a flat `400`. That defeats the purpose of the OAuth2 redirect error channel, where the error is meant to travel back to the client's `redirect_uri` so the client's own handling runs. mock-oidc preserves the redirect semantics the flow prescribes rather than collapsing them to a bare status code.

**`at+jwt` access tokens self-verify.** RFC 9068 blesses `at+jwt` as the media type for JWT access tokens, set in the JWS `typ` header. Upstream's verification path could reject a token whose `typ` was anything other than the default, so a token it had itself been asked to issue with a custom `typ` would fail its own `userinfo` and introspection checks. mock-oidc treats a token it minted as valid at its own endpoints — an `at+jwt` token introspects `active:true` and is accepted at `userinfo`. A genuinely foreign `typ` still fails, which is the behaviour you actually want: the server trusts the shapes it produces and rejects the ones it does not.

**The login page has no network dependency.** Upstream's interactive login page pulls a web font (Raleway, via Google Fonts) at render time. In a test environment — frequently air-gapped, network-policied, or simply offline in CI — an external font request is at best latency and at worst a hang or a failed render. mock-oidc's login page inlines its CSS and depends on nothing beyond the response itself. A mock server used precisely because you want to avoid the real network should not reach out to a third party to draw a form.

**Issuers are addressed by a path parameter, not a suffix.** Upstream distinguishes issuers by matching a suffix on the path. mock-oidc routes every issuer under a single leading path segment, `/{issuer}/`, so `default` lives at `/default` and its endpoints hang beneath it. Path-parameter routing composes cleanly with a standard router, makes the reserved `_mock` control namespace unambiguous, and gives every issuer a predictable, greppable prefix. It is the same conceptual model — many issuers behind one server — expressed in a form that is easier to reason about and to put behind a proxy. The consequences of this model for identity resolution are explored in [Issuers and identity](issuers-and-identity.md).

None of these corrections require a well-behaved client to change anything. A client that already followed the specifications was, in effect, coding against the corrected behaviour all along; the divergences only ever bit code that had adapted to a bug.

## Deliberate non-goals

Parity in intent also means being honest about intent *not* shared. Several upstream capabilities were left out on purpose. These are design decisions, not unfinished work, and each reflects a judgement about what a container-first testing IdP should be.

**No in-process embedded library.** Upstream ships as a JVM library you can start inside a test process and address through Kotlin/Java APIs. mock-oidc is container-first: you run it as a process — usually a container — and talk to it over HTTP. The trade-off is deliberate. An embedded library ties you to one language runtime; a server on a port serves a Go suite, a Node suite, a browser end-to-end run, and a shell script equally well. The dynamic control that the embedded API gave you in-process is provided instead by the `/_mock` control plane, which lets any client mint tokens, queue one-shot scenarios, capture requests, and drive the clock over plain JSON. That plane *is* the equivalent surface, reached over the network rather than through a class. See the [control-plane reference](../reference/control-plane.md) for its shape, and [Architecture and distribution](architecture-and-distribution.md) for why container-first was the organising choice.

**No nested, multi-segment issuers.** An issuer id must be a single path segment. Azure-style issuers whose paths carry several segments (a tenant, then more) are unsupported. This is a *named, documented gap* rather than an oversight — single-segment routing is what keeps the model simple and the `_mock` namespace unambiguous, and multi-segment issuers would complicate both for a case most test suites do not need. Where it does matter, it is called out plainly so you are never surprised; the reasoning and its boundaries live in [Issuers and identity](issuers-and-identity.md).

**Assertion signatures are parsed, not verified.** For the `jwt-bearer` and `token-exchange` grants, the incoming assertion or subject token is decoded for its claims but its signature is *not* checked — a dummy or empty signature works. This looks like a shortcut and is instead the point. A test needs to hand the server arbitrary assertions to exercise its own downstream handling: expired ones, ones with unusual claims, ones from an issuer that does not exist. Requiring a valid upstream signature would force every test to stand up a second signing authority just to feed the first, which defeats the purpose of a mock. Verifying here would buy security theatre in a component that already mints tokens for anyone.

**No `actor_token` / `act` delegation chains.** Token exchange in mock-oidc handles the subject token and audience, but it does not model delegation via `actor_token` or stamp `act` claims to represent an acting party. Delegation chains are a rich corner of RFC 8693 that most test suites never touch; supporting them would add surface and claim-shaping complexity for a scenario better served, when genuinely needed, by minting a token with exactly the `act` claim you want through the control plane.

**No arbitrary raw-response injection.** There is no facility to make the server return a hand-crafted, non-conforming raw HTTP response. mock-oidc's job is to behave like a *correct* identity provider, and every response it emits goes through the same token and error machinery so that what your client receives is internally consistent — a token that verifies, an error in the right envelope with the right status. An escape hatch for injecting arbitrary bytes would undermine that guarantee, and the legitimate need behind it — shaping what a specific token or callback contains — is already met by scenarios and minting.

## The shape of the trade

Taken together, these choices describe a tool that is faithful to upstream's *reason for existing* and unsentimental about its *implementation history*. You get the behaviour a correct OAuth2/OIDC client expects, the defects quietly fixed, and a smaller, more honest feature set with the sharp edges labelled rather than hidden. For most suites the practical result is that swapping the servers changes almost nothing in your application code; where it does, the difference is a bug you no longer have to work around, or a gap you were told about in advance.

When you are ready to make the change concretely — the endpoint mapping, the configuration that carries over, and the handful of behaviours to re-check — follow [Migrate from mock-oauth2-server](../how-to/migrate-from-mock-oauth2-server.md).
