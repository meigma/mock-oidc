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

## 2026-06-29 15:45 — PRD decisions resolved → v0.2
Developer answered all 5 open questions; folded into PRD v0.2:
- D-1 Container-first delivery. Standalone service is the primary product; NO in-process
  embedded library for parity — tests use the container (Testcontainers-style) for the
  "embedded" need. (Reshaped C6 test-time control, C8 delivery, added N6.)
- D-2 Parity in intent, cleaner where upstream unclear/buggy — we do NOT replicate
  upstream bugs/quirks (added principle P7, non-goal N8).
- D-3 Public open-source replacement (audience = upstream's user base) → DX + trustworthy
  distribution carry real weight.
- D-4 Parity ONLY, no new features now. Differentiation = quality/DX/distribution, not
  capability (reframed §10, added N7).
- D-5 Interactive login/playground: same conceptual controllable-login pattern, faithful
  enough for parity; UX redesign deferred to post-parity (C7 stays P2).
No open product questions remain; remaining unknowns are technical-design (e.g. how
test-time control/inspection is exposed against a running container).
Next: technical design — architecture (hexagonal), Go package layout, concrete protocol
surface, and the slice-by-slice parity roadmap, grounded in the catalog + PRD.

## 2026-06-29 16:30 — Effort=ultracode; technical design workflow launched
Developer set /effort ultracode and asked for a technical design doc enforcing (1)
hexagonal, (2) strong typing (type system = safety system), (3) clean code; REUSE the
Huma/Chi base; auth/authz layer goes away; dev tooling/release left untouched.
Scouted the template base myself first: it's a full hexagonal Go API template —
internal/adapter/http (humachi.New on chi, Registrar func(huma.API) seam, RouterDeps,
RFC9457 problem fallbacks, raw infra routes alongside Huma, huma.NewGroup version
groups, SpecYAML serverless OpenAPI), internal/todo (canonical vertical slice:
todo.go/service.go/ports.go + httpapi/postgres/mocks), internal/app composition root
(New + Option seams WithRepository/WithAuthenticator, resolveStore), cobra cli, viper
config, observability (slog/otel/prom). Auth/authz = internal/authz (Cedar+apikey) wired
via RouterDeps.InstallAuthz/FinalizeAuthz hooks (already nil-able) — clean to excise.
Postgres persistence also recommended for removal (mock = in-memory, parity).
Central design tension: OAuth2 needs form-encoded bodies + 302 redirects + HTML +
OAuth2-error-JSON, which fight Huma's JSON-first model → workflow includes a dedicated
Huma-feasibility researcher (context7 + go doc + template usage).
Workflow `wf_a442cb8b-c5d` (~21 agents, xhigh): Survey (6 base + huma research) →
Contract (binding package tree/type glossary/Huma strategy/error model/routing) →
Design (7 sections: architecture, domain-types, app-ports, http-adapter, config-app,
control-surface [container-first test API], roadmap) → Critique (5 lenses: hexagonal
purity, type-safety, huma-feasibility, parity-completeness, cleancode-coherence) →
Synthesize final TDD. Running in background.

## 2026-06-29 17:40 — Technical design complete (after 2 synth-truncation recoveries)
Workflow `wf_a442cb8b-c5d`: 6 base surveys + huma research → binding contract → 7 design
sections → 5 critic lenses (55 findings, 20 high) → synthesis. Synthesis truncated TWICE
(single-agent, then 2-pass front/back) — agents' very long final message gets tail-captured,
losing the BEGINNING. Fixed by per-section synthesis (9 bounded agents, resume from cache).
Final TDD: `.journal/001/mock-oidc-technical-design.md` (~4.8k lines after removing a
duplicate Architecture section that overlapped Foundations).

Key technical decisions locked (binding contract):
- Module github.com/meigma/mock-oidc; binary cmd/mock-oidc; env prefix MOCK_OIDC_ + parity
  aliases (SERVER_PORT>PORT>8080, JSON_CONFIG>JSON_CONFIG_PATH>./config.json).
- Core domain pkg `internal/oidc` (pure); driven adapters `internal/oidc/{signing,memory}`;
  driving adapters `internal/oidc/{httpapi,controlapi}`; reuse `internal/adapter/http`
  (Huma/chi) + observability/cli/config; REMOVE authz + postgres.
- Strong typing: closed enums (GrantType/ResponseType/SigningAlgorithm/TokenType) with
  Valid(); parse-don't-validate smart constructors at the edge; IssuerID single path
  segment; KeyID only from IssuerID.KeyID(); ClaimSet (no map[string]any in core).
- Huma strategy: JSON endpoints via typed Huma ops; form bodies via RawBody parse;
  302/HTML via raw chi/BrowserOutput; OAuth2 error envelope ({error,error_description})
  kept SEPARATE from RFC9457 problem (control+infra keep problem+json).
- Multi-issuer via /{issuer}/ path-param groups (clean replacement for upstream suffix
  routing); control plane under reserved /_mock/ prefix (container-first test API).
- Parity-in-intent divergence table: no body-lowercasing, no form_post-without-state 500,
  no 302→400, corrected cross-issuer text, expires_in from same Clock as exp, accept at+jwt.
- Roadmap: Slice 1 = core token pipeline (routing + discovery + jwks + client_credentials
  + signed JWT + default claims). 3-tier testing (unit / httptest functional / Testcontainers).

5 OPEN QUESTIONS need a call before implementation (in the TDD's Open Questions section):
1. (HIGH) depguard `crypto/*` ban contradicts core PKCE/template code (crypto/sha256,
   encoding/json) — must narrow the rule + carve-out, reconcile contract §3 + arch_test.
2. (MED) multi-segment/nested issuer IDs unsupported by single-segment routing (parity gap).
3. (MED) CORS default posture (default-on reflect-origin vs opt-in allowlist).
4. (LOW) custom JOSE typ self-verification (keep typ=JWT pin vs accept at+jwt).
5. (MED) Huma DefaultConfig SchemaLinkTransformer injects $schema + Link header → breaks
   discovery/jwks fixed field order; must strip transformer or serve pre-serialized []byte.
Next: developer reviews TDD + decides the 5 open questions; then begin Slice 1 (or close session).
