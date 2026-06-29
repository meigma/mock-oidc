---
id: 001
title: Session kickoff
started: 2026-06-29
---

## 2026-06-29 14:18 — Kickoff
Goal for the session: not yet stated. Developer ran `session-new`; awaiting their
actual request before starting substantive work.
Current state of the world:
- Repo `meigma/mock-oidc` on `master` at `76a4b57` (Initial commit), clean tree.
- Repo is the `template-go-api` scaffold (cmd/template-go-api, melange/apko, moon,
  goreleaser, release-please, GitHub workflows) being repurposed into `mock-oidc`.
- A `DELETE_ME.md` placeholder is still tracked, suggesting template cleanup is pending.
- Journal system initialized; this is the first session (`001`).
- Required skills loaded: `git`, `worktrunk`. TECH_NOTES reviewed (hexagonal
  architecture, functional testing before "done", agile/iterative approach).
Plan: wait for the developer's stated goal, then plan from there.

## 2026-06-29 14:25 — Goal set: feature-parity examination of mock-oauth2-server
Developer's goal for this repo: build a Go reimplementation of
`navikt/mock-oauth2-server` with better architecture (hexagonal, strong typing,
quality linting), more features, and our stronger provenance/deployment model.
This first session is design/product focused — ignore the template scaffold for now.

First task: spawn a workflow doing a deep examination of `navikt/mock-oauth2-server`
to collect a FULL feature list, as input to planning the first implementation slice
toward feature parity.

Approach: cloned the upstream repo read-only into scratchpad
(`scratchpad/mock-oauth2-server`, Kotlin, 83 .kt files). Scouted structure —
clean subsystems: http/ (router, server, CORS, SSL), grant/ (6 grant handlers +
refresh manager), token/ (provider, callbacks, key gen/provider), login/, debugger/,
userinfo/, introspect/, templates/*.ftl, OAuth2Config + Standalone/MockOAuth2Server.
README is comprehensive (Supported Flows, Config Reference, API Reference, JSON_CONFIG).
Workflow design: 7 parallel examiners (docs, http-endpoints, grants, tokens, config,
interactive, test-api-deploy) → synthesize → completeness critic → finalize markdown
catalog.

## 2026-06-29 14:55 — Examination workflow complete; catalog saved
Workflow `wf_70149f7f-363` finished: 10 agents, ~918k tokens, 240 tool uses, ~34 min.
Raw features by dimension: docs 59, http-endpoints 34, grants 22, tokens 29, config 33,
interactive 25, test-api-deploy 31 (233 total) → deduped/synthesized → 8 critic gaps
folded in → final 393-line markdown catalog.
Artifact: `.journal/001/mock-oauth2-server-feature-catalog.md` (source of truth for
parity planning). Source refs point at navikt/mock-oauth2-server (re-clonable).

Key findings worth carrying forward:
- Suffix-based routing → zero-config multi-issuer; `iss` = baseURL + issuerId, proxy-aware
  (`x-forwarded-*`). Per-issuer lazy signing key, `kid = issuerId` (deterministic).
- Endpoints: discovery (OIDC + RFC8414, identical body), jwks, authorize, token,
  userinfo, introspect (RFC7662), revoke (RFC7009, refresh only), endsession, debugger.
- 6 grants: authorization_code (PKCE plain/S256, only-when-verifier-present), refresh
  (strict since 4.0.0, rotation optional), client_credentials, password (ROPC, password
  never checked), jwt-bearer (RFC7523, sig NOT verified), token-exchange (RFC8693, sig
  NOT verified). No client-secret validation anywhere.
- Tokens are real signed JWTs (RSA + EC, default RS256; ES256K/ES512/EdDSA rejected).
  Claim model: DefaultOAuth2TokenCallback (auto tid=issuerId, azp only for auth_code) vs
  RequestMappingTokenCallback (JSON-driven, ${...} templating). Pluggable frozen clock.
- Consumed embedded (test lib: issueToken/anyToken/enqueueCallback/takeRequest) or
  standalone (env: JSON_CONFIG, SERVER_PORT/PORT, SERVER_HOSTNAME; /isalive; Jib image).
- Many parity gotchas catalogued (dual form-body parsers, custom-typ fails own verify,
  form_post-without-state → 500, lowercased error JSON, expires_in uses real clock, etc.).

Our deltas vs upstream (intent): hexagonal Go, stronger typing/linting, MORE features,
stronger provenance/deployment (melange/apko/cosign/SLSA from the template).

Next: digest to developer + propose first implementation slice (likely the core OIDC
happy path: discovery + jwks + non-interactive authorization_code + token + default
claims, single issuer) and a phased parity roadmap.

## 2026-06-29 15:20 — Pivot: non-technical PRD first (not roadmap)
Developer redirected: before any technical roadmap, craft a typical non-technical PRD
from the catalog — establish baseline PRODUCT behavior for parity, no endpoints/methods/
config specifics. Wrote `.journal/001/mock-oidc-prd.md` (v0.1 draft).
PRD structure: summary, background/problem, goals (parity G1-G6 + differentiation
G7-G9), non-goals, personas (test author [primary], local dev, platform/QA), product
principles, 10 core capabilities C1-C10 with P0/P1/P2 priorities (C1 token issuance,
C2 sign-in patterns, C3 scriptable identity, C4 multi-provider, C5 lifecycle services,
C6 test-harness+inspection, C7 local exploration, C8 two deploy modes, C9 realistic
conditions, C10 safe positioning), user scenarios S1-S6, success criteria A1-A5,
differentiation D1-D3, and open questions Q1-Q5.
Notable open questions flagged for developer: Q1 embedded-mode audience (Go test suites
are a smaller audience than upstream's JVM users — is standalone the primary product?),
Q2 drop-in fidelity incl. quirks vs cleaner-where-buggy, Q3 internal vs public OSS,
Q4 which extra features, Q5 interactive-surface fidelity.
Next: developer reviews PRD; resolve open questions; then technical design / roadmap.
