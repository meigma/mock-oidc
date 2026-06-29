# mock-oidc — Product Requirements Document

- **Status:** Draft (v0.1)
- **Date:** 2026-06-29
- **Owner:** jmgilman
- **Source material:** `.journal/001/mock-oauth2-server-feature-catalog.md` (parity catalog of `navikt/mock-oauth2-server`)

> This PRD defines the baseline product behavior `mock-oidc` must deliver to reach
> parity with the tool it replaces. It describes *what* the product does and *for whom*,
> not how it is built. Endpoint names, request formats, and configuration mechanics are
> intentionally omitted; they belong in the technical design that follows this document.

---

## 1. Summary

`mock-oidc` is a fake identity provider for software teams. It behaves enough like a
real OAuth2 / OpenID Connect provider that an application can sign users in, obtain
tokens, and validate them with no special code — but it is fully controllable, requires
no real accounts, and can be thrown away and recreated at will.

Its purpose is to let teams **build and test software that depends on identity without
depending on a real identity provider.** Instead of standing up (or sharing, or paying
for, or waiting on) a real provider during development and testing, a team runs
`mock-oidc` and scripts exactly the identities, tokens, and conditions each scenario
needs.

It is a reimplementation of the widely used `navikt/mock-oauth2-server`, rebuilt to a
higher engineering standard, with room for additional capabilities and a stronger,
more trustworthy distribution story.

---

## 2. Background & problem

Modern applications delegate "who is this user and what are they allowed to do" to an
external identity provider. That dependency is painful precisely where teams need speed
and determinism:

- **Tests** can't reliably call a real provider — it's slow, networked, shared, rate-limited, and impossible to force into specific states (this exact user, this missing claim, this expired token).
- **Local development** stalls when the app won't even start without a provider to talk to.
- **Provisioning real identities** for every test case is heavyweight and leaks test concerns into a system meant for real people.

Teams that consume identity need a stand-in that is **realistic enough to satisfy their
existing auth code**, yet **completely under their control.** That is the gap this product
fills.

---

## 3. Goals

**Primary goal — parity:** match the baseline product behavior of `navikt/mock-oauth2-server`
so that it is a credible, drop-in alternative for the jobs people use that tool for today.

Specifically, the product should let a user:

- G1. Stand up a working identity provider in seconds, with zero configuration for the common case.
- G2. Have an application authenticate and validate tokens against it using only standard, off-the-shelf identity libraries — no provider-specific glue.
- G3. Control exactly who is "signed in" and what their token contains, per scenario.
- G4. Exercise the full range of sign-in patterns real applications use.
- G5. Use it both embedded inside an automated test and as a standalone service for local and shared environments.
- G6. Inspect and script the interaction during tests (what was sent, what comes back next).

**Secondary goals — our differentiation (beyond parity):**

- G7. A noticeably better developer and operator experience than the tool it replaces.
- G8. Trustworthy distribution: artifacts users can verify and run with confidence in their pipelines.
- G9. Headroom for capabilities the original lacks (to be defined; not part of the parity baseline).

---

## 4. Non-goals

- N1. **Not a production identity provider.** It performs no real authentication or authorization and stores no real credentials. It must make this unmistakable.
- N2. **Not a user-management or account system.** Identities are conjured per scenario, not registered, stored, or administered.
- N3. **Not a security product.** It deliberately accepts whatever it is told; it is not a place to test that bad credentials are rejected.
- N4. **Not a general-purpose API gateway, proxy, or SSO portal.**
- N5. **Not chasing exhaustive standards conformance** beyond what relying applications actually need to be tested. Realism is scoped to "the app under test is satisfied."

---

## 5. Users & personas

| Persona | Who they are | What they need from the product |
|---|---|---|
| **Test author** (primary) | A developer writing automated tests for a service that consumes identity. | Deterministic, controllable identities and tokens inside the test, with no real provider and no flakiness; the ability to assert what their code sent. |
| **Local developer** | Someone running the app on their machine to build a feature. | A drop-in provider so the app boots and sign-in works end-to-end locally, including clicking through a login by hand. |
| **Platform / QA engineer** | Someone setting up shared dev/test/CI environments or scripted scenarios. | Configuration-driven behavior, reproducibility, and the ability to represent several providers or tenants at once. |

The *application under test* is not a persona but is the constant other party in every
scenario: whatever standard identity tooling it already uses must work against the mock
unchanged.

---

## 6. Product principles

- **P1. Real enough to be trusted, fake enough to be useful.** Tokens are genuine and verifiable; everything behind them is scriptable fiction.
- **P2. Works out of the box.** The default experience requires no configuration. Depth is available but never required.
- **P3. Determinism over realism.** When the two conflict, the product favors predictable, repeatable behavior (e.g. controllable time and identities) over faithfully simulating a real provider's nondeterminism.
- **P4. The user is in charge of identity.** Any sign-in succeeds as directed; the user decides who they are and what they carry.
- **P5. Obviously not for production.** The product is positioned, documented, and behaves so that no one mistakes it for a real provider.
- **P6. Meet developers where they are.** It fits into both an in-process test and a container in a compose file, with equal first-class support.

---

## 7. Core capabilities (parity baseline)

Priorities: **P0** = required for a credible parity release; **P1** = expected in parity but
secondary to the core loop; **P2** = valued, can trail.

### C1. Standards-based token issuance *(P0)*
The product acts as a compliant identity provider: it issues genuine, cryptographically
verifiable identity and access tokens, and publishes the provider information and signing
material that standard client libraries need to validate them automatically. The headline
promise is that **an application's existing identity code works against it with no
special configuration.**

### C2. Coverage of real-world sign-in patterns *(P0 for the common flows, P1 for the rest)*
The product can stand in for the provider regardless of how an application integrates,
supporting the authentication and authorization patterns applications actually use:

- Interactive user sign-in (a browser-based login) — *P0*
- Machine-to-machine access with no user present — *P0*
- Renewing access for long-lived sessions — *P1*
- Delegation / acting on behalf of a user, and exchanging one token for another (service-to-service identity propagation) — *P1*
- Direct username/password sign-in (legacy pattern still found in older apps) — *P1*

### C3. Scriptable, deterministic identity *(P0)*
This is the product's reason to exist over a real provider. For any scenario the user can
control:

- **Who is signed in** — the user/subject identity.
- **What the token says** — the attributes, roles, audience, and custom data it carries.
- **Validity and time** — how long tokens last, with the ability to freeze or set "now" so token lifetimes are exactly reproducible.
- **Edge and error conditions** — the ability to force specific failure or boundary states on demand.

Control is available **both programmatically** (from inside a test) **and through
configuration** (for standalone use). No real credentials are ever required.

### C4. Multiple providers from one instance *(P1)*
A single running instance can present as **several independent identity providers at
once**, so applications that integrate with more than one provider — or multi-tenant
setups — can be exercised against one mock without extra moving parts.

### C5. Full token lifecycle services *(P1)*
Beyond issuing tokens, a relying application can carry out the rest of the identity
lifecycle against the mock: look up the signed-in user's profile, check whether a token
is still active and inspect what it contains, invalidate a token, and sign a user out.

### C6. Test-harness integration & inspection *(P0)*
When used inside an automated test, the product provides a clean way to:

- Start and stop it and discover where it's listening.
- Mint tokens directly for test setup, without driving a full flow.
- Pre-program the next response so a scenario plays out a specific way.
- **Inspect and assert exactly what the application sent to the provider** — so tests can verify their own outbound behavior, not just the happy path.

### C7. Human-friendly local exploration *(P2)*
For local development and manual debugging, the product offers a simple interactive
sign-in screen (enter any user, optionally set their token's contents) and a built-in
playground that lets a developer drive a complete flow by hand and see the raw exchange.
This lowers the barrier to using it locally and to diagnosing integration problems.

### C8. Two deployment modes, low configuration *(P0)*
The product is delivered in two equally supported forms:

- **Embedded** — runs in-process inside a test, started and controlled from code.
- **Standalone** — runs as a service (container or local process), suitable for local compose setups and shared environments, configured simply.

Both have sensible zero-config defaults; richer behavior is available through
configuration when wanted.

### C9. Realistic operating conditions *(P1)*
The mock behaves correctly in the topologies teams actually run it in: behind proxies and
inside containers (so the identity it advertises matches the address clients actually
reach it on), over secured transport, and across browser-origin boundaries. Flows that
work in a production-like setup also work against the mock.

### C10. Safe positioning *(P0)*
The product is unambiguous, in both behavior and messaging, that it is a development and
testing tool only and must never be used as a real identity provider.

---

## 8. Key user scenarios

- **S1 — Test a protected API.** A developer writes an automated test for a service that requires a valid token. With a few lines they start the mock, mint a token for a specific user with specific roles, call their service, and assert the service accepted it — no real provider, no flakiness.
- **S2 — Test the unhappy paths.** The same developer forces an expired token, a missing attribute, or a wrong audience, and asserts their service rejects it correctly.
- **S3 — Run the app locally.** A developer brings up their stack locally; the mock stands in for the real provider so the app boots and a full browser sign-in works on their machine, signing in as any user they type.
- **S4 — Verify what the app sends.** A developer needs to confirm their service makes the right request to the provider; they drive the flow and inspect exactly what the service sent.
- **S5 — Multi-provider integration.** A team's product trusts two different providers; they point both integrations at one mock instance presenting as two providers and test them side by side.
- **S6 — Debug an integration by hand.** An engineer uses the built-in playground to walk through a sign-in step by step and read the raw request and response to find where their configuration is wrong.

---

## 9. Success criteria

The parity baseline is met when:

- **A1.** An application using standard, unmodified identity tooling can sign in, obtain tokens, validate them, renew them, inspect them, and sign out against `mock-oidc` with no provider-specific code.
- **A2.** A test author can express the common scenarios (a specific user, custom token contents, an expired token, multiple providers) in only a few lines.
- **A3.** The product runs both embedded in a test and as a standalone container with no configuration required for the default case.
- **A4.** A team currently using the tool we replace can adopt `mock-oidc` for the same jobs with minimal change *(pending the drop-in decision in §11)*.
- **A5.** The interactive sign-in and playground let a developer complete a flow by hand without reading documentation.

Beyond-parity success (G7–G9) is measured separately and defined in later work.

---

## 10. Differentiation (beyond parity)

Not part of the baseline, but the reasons to build our own rather than keep using the
original. Captured here so the baseline work doesn't foreclose them; each becomes its own
scoped effort later.

- **D1. Developer & operator experience** — clearer defaults, better documentation, friendlier failure messages, and a more approachable configuration story than the tool it replaces.
- **D2. Trustworthy distribution** — artifacts that operators can verify before running, with a transparent build and supply-chain story, since this tool runs inside other teams' pipelines.
- **D3. Additional capabilities** — features the original lacks. To be defined; explicitly out of scope for the parity baseline.

---

## 11. Open questions & decisions needed

- **Q1. Embedded mode's audience.** The original is embedded from JVM test suites. Embedding `mock-oidc` most naturally serves tests written in our implementation language, a different (and likely smaller) audience than the original's. Is embedded mode a first-class P0, or is the standalone service the primary product with embedding secondary? This materially affects scope and priorities.
- **Q2. Drop-in fidelity.** Do we target behavioral parity with the original *including* its quirks and known rough edges (so existing users can swap it in with no changes), or "parity on intent, cleaner where the original is buggy"? This is a product decision with real consequences for adoption and for success criterion A4.
- **Q3. Primary audience.** Internal use across our own projects, or a public open-source alternative for the original's user base? This shapes how much weight A4 and D1–D2 carry.
- **Q4. Which extra capabilities (D3) matter**, and when do they enter the roadmap relative to parity?
- **Q5. Interactive surfaces (C7) scope.** How faithfully must the by-hand login and playground match the original versus being reimagined for better UX?

---

## 12. Out of scope for this document

The technical design — architecture, language/package structure, the concrete protocol
surface (endpoints, request and response shapes, configuration format), and the
slice-by-slice implementation plan — is deliberately excluded and will be produced
separately, grounded in the parity catalog.
