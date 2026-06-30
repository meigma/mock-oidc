---
id: 001
title: Design foundation — parity catalog, PRD, technical design
date: 2026-06-29
status: complete
repos_touched: [mock-oidc (journal branch only — no implementation code)]
related_sessions: []
---

## Goal
Kick off the `meigma/mock-oidc` project — a Go reimplementation of
`navikt/mock-oauth2-server` (a for-testing-only OAuth2/OIDC server that mints real
signed JWTs). This first session was deliberately design/product-focused: examine
upstream to establish full feature parity, then produce the product and technical
design needed to plan the first implementation slice. No production code by design.

## Outcome
**Goal met.** Produced a complete, internally consistent, decision-complete design
package — three journal artifacts under `.journal/001/`:
- `mock-oauth2-server-feature-catalog.md` — parity ground truth (233 raw features
  distilled, deduped, critic-corrected; endpoints, 6 grants, token/claim model, config,
  test-lib + standalone surfaces, ~25 Go-port gotchas).
- `mock-oidc-prd.md` (v0.2) — non-technical PRD: personas, 10 capabilities (P0/P1/P2),
  scenarios, success criteria; decisions D-1…D-5 recorded.
- `mock-oidc-technical-design.md` — normative build blueprint (hexagonal, strongly-typed,
  clean Go reusing the template's Huma/chi base); Slice 1 defined; all 5 open questions
  resolved (now a "Decisions (resolved 2026-06-29)" section).
No implementation code was written and the template scaffold is untouched (intentional).

## Key Decisions
- **Container-first delivery; no in-process embedded library** -> the standalone container
  is the product; tests run it Testcontainers-style. Smaller surface, language-agnostic
  reach, matches a public-OSS audience.
- **Parity in intent, not in quirks** -> match what upstream is *for*; correct its defects
  (don't replicate the ~25 catalogued bugs). Frees the design from upstream's flaws.
- **Public OSS replacement; parity only, no new features yet** -> differentiation is
  engineering quality / DX / trustworthy distribution, not capability.
- **Reuse the template chassis, swap the slice** -> keep Huma/chi transport, observability,
  CLI, config; **remove** the Cedar+API-key authz layer and Postgres persistence (the mock
  is in-memory). The authz layer plugs in via nil-able router hooks, so excision is clean.
- **Hexagonal layout** -> pure core `internal/oidc`; driven adapters `internal/oidc/{signing,
  memory}`; driving adapters `internal/oidc/{httpapi,controlapi}`; enforced by depguard +
  an `arch_test.go` import gate.
- **Strong typing = safety system** -> closed enums with `Valid()` (GrantType/ResponseType/
  SigningAlgorithm/TokenType), parse-don't-validate smart constructors at the edge,
  `IssuerID` a single path segment, `KeyID` only via `IssuerID.KeyID()`, typed `ClaimSet`
  (no `map[string]any` in the core).
- **Huma content-type strategy** -> JSON endpoints as typed Huma ops; form bodies via
  `RawBody`; 302/HTML via raw chi/`BrowserOutput`; the OAuth2 error envelope kept separate
  from the template's RFC 9457 problem+json (control + infra keep problem+json).
- **Multi-issuer via `/{issuer}/` path-param groups** -> a clean, documented divergence from
  upstream's suffix routing; container-first control plane under reserved `/_mock/`.
- **Five resolved design decisions** -> (1) narrow the core's crypto ban to key-bearing
  signing/JOSE only, carving out keyless PKCE primitives (already implemented in the body);
  (2) accept single-segment issuers as a named parity gap; (3) CORS default ON with
  reflect-origin + credentials, `CORSAllowedOrigins` tightens; (4) verifier accepts `JWT`
  and `at+jwt` (RFC 9068); (5) strip Huma's `SchemaLinkTransformer` in `NewAPI` so protocol
  JSON stays clean and the discovery field order holds.
- **Naming** -> module `github.com/meigma/mock-oidc`, binary `cmd/mock-oidc`, env prefix
  `MOCK_OIDC_` plus unprefixed upstream parity aliases. Not yet applied to the repo.

## Changes
- `.journal/001/mock-oauth2-server-feature-catalog.md` — new (parity catalog).
- `.journal/001/mock-oidc-prd.md` — new (PRD v0.1 -> v0.2 after decisions).
- `.journal/001/mock-oidc-technical-design.md` — new (technical design; decision-complete).
- `.journal/TECH_NOTES.md` — added the mock-oidc project block + pointers to all three
  artifacts and the locked product/technical decisions.
- `.journal/INDEX.md` — session 001 row (in-progress -> complete).
- No changes to the implementation tree (`internal/`, `cmd/`, tooling) — design-only session.

## Open Threads
- **Slice 1 not started.** Core token pipeline (routing + discovery + JWKS +
  `client_credentials` + signed-JWT issuance with default claims) is defined in the TDD but
  unimplemented. Start it on a fresh implementation worktree off the default branch.
- **Module/binary rename not applied.** `template-go-api` -> `mock-oidc` is a mechanical
  substitution (~43 import sites + tooling identifiers), to be done as the first step of
  implementation.
- **authz + Postgres removal not applied.** Excision is specified in the TDD; not yet done.
- **Artifact placement.** The three design docs live on the journal branch; they may later
  be promoted into the repo as `docs/` once implementation begins (a product call).

## References
- Upstream: https://github.com/navikt/mock-oauth2-server (cloned read-only during the session).
- Artifacts: `.journal/001/{mock-oauth2-server-feature-catalog,mock-oidc-prd,mock-oidc-technical-design}.md`.
- No PRs — design-only session; all work on `journal/jmgilman`.

## Lessons
- **Workflow synthesis truncates from the front when one agent emits a very long final
  message.** A single agent assembling a ~1000-line doc (and even a two-pass front/back
  split) had its long final message tail-captured — the *beginning* was lost, not the end.
  Fix that worked: **per-section synthesis** (one bounded agent per `##` section, each
  emitting only its section), then concatenate. Resume-from-cache made the re-runs cheap.
  General rule: keep any single agent's final output bounded; split assembly by section.
