# mock-oidc — Product Requirements Document

- **Status:** Draft (v0.2)
- **Date:** 2026-06-29
- **Owner:** jmgilman
- **Source material:** `.journal/001/mock-oauth2-server-feature-catalog.md` (parity catalog of `navikt/mock-oauth2-server`)
- **v0.2 note:** incorporates the product decisions recorded in §11 — container-first delivery, public open-source positioning, parity-only scope, and "parity in intent, cleaner where upstream is unclear."

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

It is a public, open-source reimplementation of the widely used
`navikt/mock-oauth2-server`, rebuilt to a higher engineering standard with a stronger,
more trustworthy distribution story. The initial product targets **behavioral parity**
with the original — matching what it does for the people who rely on it today — and adds
no new capabilities until that baseline is met.

---

## 2. Background & problem

Modern applications delegate "who is this user and what are they allowed to do" to an
external identity provider. That dependency is painful precisely where teams need speed
and determinism:

- **Tests** can't reliably call a real provider — it's slow, networked, shared, rate-limited, and impossible to force into specific states (this exact user, this missing claim, this expired token).
- **Local development** stalls when the app won't even start without a provider to talk to.
- **Provisioning real identities** for every test case is heavyweight and leaks test concerns into a system meant for real people.

Teams that consume identity need a stand-in that is **realistic enough to satisfy their
existing auth code**, yet **completely under their control.** `navikt/mock-oauth2-server`
established that this stand-in is valuable; `mock-oidc` aims to be the better-built,
better-distributed successor for that same job.

---

## 3. Goals

**Primary goal — parity:** match the baseline product behavior of `navikt/mock-oauth2-server`
so that it is a credible replacement for the jobs people use that tool for today.

Specifically, the product should let a user:

- G1. Stand up a working identity provider in seconds, with zero configuration for the common case.
- G2. Have an application authenticate and validate tokens against it using only standard, off-the-shelf identity libraries — no provider-specific glue.
- G3. Control exactly who is "signed in" and what their token contains, per scenario.
- G4. Exercise the full range of sign-in patterns real applications use.
- G5. Run it as a standalone service for local and shared environments, and use that same service inside automated tests by running it as a container alongside the test (container-based test orchestration, e.g. Testcontainers).
- G6. Inspect and script the interaction during tests (what was sent, what comes back next).

**Secondary goals — our differentiation:** these are about *how well* the product is
built and delivered, **not new features.**

- G7. A noticeably better developer and operator experience than the tool it replaces.
- G8. Trustworthy distribution: artifacts users can verify and run with confidence in their pipelines.
- G9. A clean, maintainable, extensible foundation — so post-parity capabilities are cheap to add later.

---

## 4. Non-goals

- N1. **Not a production identity provider.** It performs no real authentication or authorization and stores no real credentials. It must make this unmistakable.
- N2. **Not a user-management or account system.** Identities are conjured per scenario, not registered, stored, or administered.
- N3. **Not a security product.** It deliberately accepts whatever it is told; it is not a place to test that bad credentials are rejected.
- N4. **Not a general-purpose API gateway, proxy, or SSO portal.**
- N5. **Not chasing exhaustive standards conformance** beyond what relying applications actually need to be tested.
- N6. **Not an in-process embeddable library (for the parity release).** The product is delivered as a runnable service; testing uses that service as a container rather than a language-native embedded library. (Reconsiderable post-parity.)
- N7. **Not a feature superset of the original (for now).** Parity is the whole scope; new capabilities the original lacks are explicitly deferred.
- N8. **Not bound to reproduce the original's bugs or quirks.** Where upstream behavior is clearly a defect or is unclear, `mock-oidc` does the correct, clear thing rather than copying the flaw.

---

## 5. Users & personas

| Persona | Who they are | What they need from the product |
|---|---|---|
| **Test author** (primary) | A developer writing automated tests for a service that consumes identity. | Deterministic, controllable identities and tokens during the test — achieved by running `mock-oidc` as a container alongside the test — with no real provider and no flakiness, plus the ability to assert what their code sent. |
| **Local developer** | Someone running the app on their machine to build a feature. | A drop-in provider (a container in their local stack) so the app boots and sign-in works end-to-end locally, including clicking through a login by hand. |
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
- **P6. One product, run many ways.** The same standalone service serves local development, shared environments, and automated tests; it is not split into divergent variants.
- **P7. Faithful to intent, not to flaws.** Parity means matching what the original is *for*, while correcting what it gets wrong or leaves unclear.

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

Control is available **at startup through configuration** (the primary path for standalone
and test use) **and dynamically against a running instance** (for per-scenario scripting).
No real credentials are ever required.

### C4. Multiple providers from one instance *(P1)*
A single running instance can present as **several independent identity providers at
once**, so applications that integrate with more than one provider — or multi-tenant
setups — can be exercised against one mock without extra moving parts.

### C5. Full token lifecycle services *(P1)*
Beyond issuing tokens, a relying application can carry out the rest of the identity
lifecycle against the mock: look up the signed-in user's profile, check whether a token
is still active and inspect what it contains, invalidate a token, and sign a user out.

### C6. Test-time control & inspection *(P1)*
Because the product is used in automated tests by running it as a container, it must offer
the original's test-time capabilities in a way that works against a running instance, not
only via in-process calls:

- Obtain a token directly for test setup, without driving a full flow.
- Pre-program how the next interaction should respond, so a scenario plays out a specific way.
- **Inspect and assert exactly what the application sent to the provider** — so tests can verify their own outbound behavior, not just the happy path.

(The mechanism for delivering these against a running container is a technical-design
question; the *capability* is the parity target.)

### C7. Human-friendly local exploration *(P2)*
For local development and manual debugging, the product offers the **same conceptual
controllable-login experience** as the original: a simple interactive sign-in (enter any
user, optionally set what their token carries) and a built-in playground that lets a
developer drive a complete flow by hand and see the raw exchange. For parity, this matches
the original's *concept* and is faithful enough to be useful; a UX redesign of these
surfaces is explicitly **post-parity** work.

### C8. Container-first delivery, low configuration *(P0)*
The product is delivered as a **standalone service, container-first**, with sensible
zero-config defaults and richer behavior available through configuration when wanted. The
*same* service is the unit used everywhere: a container in a local stack, a service in a
shared environment, and a container spun up alongside an automated test. There is no
separate embedded library to learn or maintain.

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

- **S1 — Test a protected API.** A developer writes an automated test for a service that requires a valid token. The test brings up `mock-oidc` as a container, obtains a token for a specific user with specific roles, calls their service, and asserts the service accepted it — no real provider, no flakiness.
- **S2 — Test the unhappy paths.** The same developer forces an expired token, a missing attribute, or a wrong audience, and asserts their service rejects it correctly.
- **S3 — Run the app locally.** A developer brings up their stack locally; `mock-oidc` (a container in the stack) stands in for the real provider so the app boots and a full browser sign-in works on their machine, signing in as any user they type.
- **S4 — Verify what the app sends.** A developer needs to confirm their service makes the right request to the provider; they drive the flow and inspect exactly what the service sent.
- **S5 — Multi-provider integration.** A team's product trusts two different providers; they point both integrations at one mock instance presenting as two providers and test them side by side.
- **S6 — Debug an integration by hand.** An engineer uses the built-in playground to walk through a sign-in step by step and read the raw request and response to find where their configuration is wrong.

---

## 9. Success criteria

The parity baseline is met when:

- **A1.** An application using standard, unmodified identity tooling can sign in, obtain tokens, validate them, renew them, inspect them, and sign out against `mock-oidc` with no provider-specific code.
- **A2.** A test author can express the common scenarios (a specific user, custom token contents, an expired token, multiple providers) in only a few lines, using `mock-oidc` as a container alongside the test.
- **A3.** The product runs as a standalone container with no configuration required for the default case, and that same container is what tests use.
- **A4.** A team currently using the original can adopt `mock-oidc` for the same jobs by mapping the same concepts onto it — a behavioral replacement, not necessarily a byte-for-byte drop-in.
- **A5.** The interactive sign-in and playground let a developer complete a flow by hand without reading documentation.

Differentiation goals (G7–G9) are quality/delivery measures, tracked separately from the
parity baseline.

---

## 10. Differentiation (how, not what)

Not part of the baseline feature set, but the reasons to build and adopt this rather than
keep using the original. **None of these add new product features for the parity release**;
they are about engineering quality, experience, and trust.

- **D1. Developer & operator experience** — clearer defaults, better documentation, friendlier failure messages, and a more approachable configuration story than the tool it replaces.
- **D2. Trustworthy distribution** — artifacts that operators can verify before running, with a transparent build and supply-chain story, since this tool runs inside other teams' pipelines.
- **D3. A foundation for later** — a clean, well-structured implementation so that genuinely new capabilities (deliberately out of scope now) are inexpensive to add after parity.

---

## 11. Decisions (resolved 2026-06-29)

The open questions from v0.1 are now decided; recorded here with rationale so the baseline
work stays aligned.

- **D-1 — Delivery model: container-first.** The standalone service is the primary product. The original's in-process embedded library is **not** reproduced for parity; using `mock-oidc` as a container in tests (container-based test orchestration, e.g. Testcontainers) covers the "embedded testing" need. *(Shapes C6, C8, N6, the personas, and the scenarios.)*
- **D-2 — Fidelity: parity in intent, cleaner where unclear.** Match what the original is *for*, not its exact quirks; correct clear defects and ambiguous behavior rather than copying them. *(Drives P7, N8, and A4.)*
- **D-3 — Positioning: public open-source replacement.** The audience is the original's broad user base, so experience (D1) and trustworthy distribution (D2) carry real weight.
- **D-4 — Scope: parity only, no new features yet.** Differentiation is quality/DX/distribution, not capability. New features are deferred until parity is met. *(Drives N7, the reframed secondary goals, and §10.)*
- **D-5 — Interactive surfaces: same concept, UX later.** Reproduce the original's controllable-login *pattern* faithfully enough for parity; defer any UX redesign of the login and playground to post-parity work. *(Drives C7's P2 framing.)*

No open product questions remain. Remaining unknowns (e.g. exactly how test-time control
and inspection are exposed against a running container) are **technical-design** questions,
addressed in the document that follows this one.

---

## 12. Out of scope for this document

The technical design — architecture, language/package structure, the concrete protocol
surface (endpoints, request and response shapes, configuration format), the mechanism for
test-time control against a running container, and the slice-by-slice implementation plan
— is deliberately excluded and will be produced separately, grounded in the parity
catalog.
