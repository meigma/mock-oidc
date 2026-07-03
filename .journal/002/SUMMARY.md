---
id: 002
title: Full implementation of the mock-oidc design — plan + all 7 slices shipped
date: 2026-07-02
status: complete
repos_touched: [mock-oidc]
related_sessions: [001]
---

## Goal
Turn the decision-complete design package from session 001 (parity feature catalog,
PRD v0.2, normative technical design) into a working product. First author a
temporary, executable implementation plan in the session journal, then use it to
**fully** implement the currently specified design.

## Outcome
**Goal met — the entire specified design is shipped end-to-end.** Authored
`.journal/002/mock-oidc-implementation-plan.md` (a 7-slice, file-level, functionally
-gated build blueprint), then implemented all seven slices, each as its own reviewed,
DoD-verified, squash-merged PR:

- **Slice 0** (PR #8, `c275a16`) — rename `template-go-api`→`mock-oidc`, amputate
  Cedar/API-key authz + Postgres + todo, stand up the empty `internal/oidc` hexagon
  with depguard + arch-test layering gates. Walking skeleton, infra routes only.
- **Slice 1** (PR #9, `172b2cf`) — core token pipeline: pure domain, ports, services,
  RSA+EC stdlib signing, in-memory adapters, discovery/JWKS/`client_credentials` over
  the httpapi driving adapter. The tracer bullet — a stock client verifies a token.
- **Slice 2** (PR #10, `19fb9b0`) — `authorization_code` + PKCE + interactive login
  (HTML/form_post) + `id_token`/`refresh_token`.
- **Slice 3** (PR #11, `813d1ff`) — token lifecycle: refresh redemption, hardened
  stdlib `TokenVerifier`, `/userinfo`, `/introspect`, `/revoke`, `/endsession`.
- **Slice 4** (PR #12, `db5c92b`) — delegation/legacy grants: token-exchange,
  jwt-bearer, password (ROPC), structural-only `private_key_jwt`, `ParseUnverified`.
- **Slice 5** (PR #13, `bc6de51`) — multi-issuer key isolation, scenario queue +
  request-mapping callbacks, request capture, the `/_mock` control plane, debugger.
- **Slice 6** (PR #14, `ecaf84a`) — proxy-aware URLs, TLS + self-signed, static-asset
  guard, metric-cardinality bound, release-chain finish, README/docs reframe.

Every slice cleared its functional Definition of Done against a real container
(testcontainers-go), not just unit tests. The parity feature set from the catalog is
complete.

## Key Decisions
- **Plan-first, then execute slice-by-slice via multi-agent workflows** -> each slice
  ran as an Understand/Implement→Review→Repair→DoD workflow; the plan (a journal
  artifact) was the shared contract every agent read. Kept the work legible and each
  PR independently reviewable.
- **JOSE stays stdlib — no external library, for signing AND verification** -> the
  signing adapter hand-rolls compact JWS over `crypto/*`; verification (S3) is
  self-issued-token-only with known keys, so go-jose adds little. Re-assessed at S3
  as planned and confirmed, with six mandatory verifier-hardening rules (alg
  allowlist/never-none, alg-from-resolved-key, typ gate, iss match, injected-clock,
  constant-time). Contained behind ports; reversible in one package.
- **Model policy: never default the agent model** -> the session default was Fable;
  every workflow/agent call set `model` explicitly (opus for build/review/DoD, sonnet
  for mechanical lenses) to protect quota. Saved to agent memory.
- **Adversarial review on every slice, verified before acting** -> a security/protocol
  reviewer ran on each slice; it caught a real defect every single time (see Lessons).
- **Parity in intent, not in flaws** -> corrected upstream quirks (correct-case OAuth2
  errors, tolerated empty `state`, cross-issuer refresh text) rather than replicating
  them, per the locked D-1..D-5 decisions.

## Changes
- `.journal/002/mock-oidc-implementation-plan.md` — new; the 7-slice execution
  blueprint (now marked COMPLETE, all boxes ticked, per-slice DONE status lines).
- `mock-oidc` `master` — seven squash-merged feature PRs (#8–#14) taking the repo from
  the untouched template scaffold to a complete OIDC mock server: `internal/oidc`
  (pure core + `signing`/`memory`/`httpapi`/`controlapi` adapters), `internal/app`
  composition + TLS, `internal/config` seed/flags, integration suites, packaging
  (melange/apko/goreleaser identifiers), README/docs. Net effect of S0 alone:
  −7,936/+879 lines (amputation); S1–S6 built the OIDC surface on top.
- `.journal/TECH_NOTES.md` — added the plan pointer, the JOSE stdlib decision
  (resolved), and ops gotchas (gpg re-sign, mockery Homebrew-shadow).

## Open Threads
- **2 dependabot PRs open** (otelhttp 0.69.0, x/time 0.15.0) — not part of this
  session; left for a maintenance pass.
- **Zero-config tokens carry an empty `sub`** (default callback injects the login
  subject; non-interactive has none) — consider a default subject before parity docs.
- **Custom login page (`loginPagePath`)** deferred by the design as low-risk polish;
  only `staticAssetsPath` was implemented in S6.
- **`.agents/` skill docs still contain `template-go-api`** — intentionally untouched
  (skill-synced, out of the grep-gate scope); scrub if desired.
- **Version/CHANGELOG reset before first real release** — the repo inherited the
  template's `1.0.4` lineage; melange builds `1.0.4-r0`.
- **Promote design + plan into repo `docs/`** — still a product call now that
  implementation is complete.

## References
- Merged PRs: #8 `c275a16`, #9 `172b2cf`, #10 `19fb9b0`, #11 `813d1ff`, #12 `db5c92b`,
  #13 `bc6de51`, #14 `ecaf84a` (all in `meigma/mock-oidc`).
- Design package (session 001): `.journal/001/{mock-oauth2-server-feature-catalog,
  mock-oidc-prd,mock-oidc-technical-design}.md`.
- Execution blueprint: `.journal/002/mock-oidc-implementation-plan.md`.
- Prior session: `.journal/001/SUMMARY.md`.

## Lessons
- **Adversarial review earned its cost on every slice.** Each slice's review/verify
  pass caught a real defect functional testing alone would have missed: S1 ES-key
  production (would 500 + serve malformed JWKs), S2 clean, S3 exact-`exp`-instant
  acceptance (RFC 7519 §4.1.4), S4 jwt-bearer audience + injectable-jti bypass, S5
  debugger SSRF/panic + unconditional recording + nil-clock deref, S6 symlink-
  following static guard + embedded-port `X-Forwarded-Host` + dead `SERVER_PORT`
  alias. Default-reject verification kept false positives out.
- **In-process `httptest` can't catch container-deployment bugs.** S5's debugger
  passed R2 but failed under port remapping (back-channel dialed the front-channel
  origin) — only the R3/DoD container run exposed it. Self-calling/back-channel
  features need a remapped-port integration test.
- **Bound each agent's final message; assemble from pieces.** Carried from S001 —
  per-section synthesis + workflow returning bounded parts avoided the long-message
  truncation that bit the original design doc.
- **Pin the toolchain for drift gates.** A Homebrew `mockery` shadowed the mise pin
  on bare PATH (`interface{}` vs `any`), producing a CI-only mockery-check failure on
  S1. Always run gates via `mise x --`; verify drift by direct regeneration, never a
  cache-hit green.
- **Agent commits sign only while the gpg-agent cache is warm.** Check
  `git log --format='%G?'` before pushing agent-authored branches; re-sign with
  `git rebase --exec 'git commit --amend --no-edit -S' <base>`.
