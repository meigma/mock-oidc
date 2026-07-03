# Technical Notes

- Use hexagonal architecture at all times. Keep business logic isolated from CLI, filesystem, network, storage, and other external adapters.
- Prefer functional testing before calling any feature complete. Unit tests are useful, but they do not prove the tool works the way the design intends.
- Take an agile approach to development. Avoid waterfall: underspecify when useful, prototype early, learn from the result, and refine from working behavior.

## Project: mock-oidc

- Goal: a Go reimplementation of `navikt/mock-oauth2-server` with better architecture
  (hexagonal, strong typing, quality linting), MORE features, and our stronger
  provenance/deployment model (melange/apko/cosign/SLSA from the template scaffold).
  For-testing-only OAuth2/OIDC server that mints real signed JWTs.
- Parity source of truth: `.journal/001/mock-oauth2-server-feature-catalog.md` — full
  feature catalog of the upstream Kotlin server (endpoints, 6 grants, token/claim model,
  config, test-lib + standalone surfaces, and Go-port gotchas). Read it before scoping
  any parity work.
- Product baseline: `.journal/001/mock-oidc-prd.md` — non-technical PRD (v0.2). Locked
  product decisions: **container-first** (standalone service is the product; no in-process
  embedded library — tests run the container, Testcontainers-style); **parity in intent,
  cleaner where upstream is unclear/buggy** (do not copy upstream quirks); **public OSS
  replacement**; **parity only, no new features yet** (differentiation = quality/DX/
  distribution); interactive login/playground match upstream's *concept*, UX redesign is
  post-parity.
- Decision (2026-07-02, Josh): the signing adapter uses **stdlib crypto only — no external
  JOSE library** (deviation from the TDD's go-jose assumption). Rationale: zero third-party
  code in the key-holding package, emission-only JOSE scope, byte-level control over the
  typed ClaimSet wire format; independent verification in tests via golang-jwt/jwt/v5.
  **RESOLVED at Slice 3 (2026-07-02): verification also stays stdlib.** Scope is
  self-issued tokens with known keys (kid==issuerID via own KeyStore; no foreign JWKS/JWE;
  refresh redemption = store lookup, not JWS parse). Hardening mandated in the verifier:
  alg allowlist (never none), algorithm from resolved key not token header, typ gate
  JWT|at+jwt, iss match, injected-Clock time checks. Full rule list in the plan's Slice 3
  note. Revisit only if verification of foreign tokens ever enters scope.
- Ops gotchas (2026-07-02): agent-authored commits sign only while the gpg-agent cache is
  warm — check `git log --format='%G?'` before pushing, re-sign via
  `git rebase --exec 'git commit --amend --no-edit -S' <base>`. Drift-gate gotcha (root
  cause found in session 002/slice 2, correcting the earlier moon-cache hypothesis): a
  Homebrew mockery v3.7.0 at /opt/homebrew/bin shadows the mise-pinned v3.7.1 on bare
  PATH (3.7.0 emits `interface{}`, 3.7.1 emits `any`). Always run gates through the pinned
  toolchain: `mise x -- moon run check` / `mise x -- mockery`. Consider `brew uninstall
  mockery` on this host. CI uses mise, so it always matches the pin.
- **IMPLEMENTATION COMPLETE (2026-07-02, session 002):** all 7 slices (0–6) are merged to
  `master` (PRs #8–#14). The full specified design is shipped — parity feature set complete:
  all 6 grants, id/refresh tokens, lifecycle (userinfo/introspect/revoke/endsession),
  multi-issuer key isolation, the `/_mock` control plane + debugger, proxy-aware URLs, TLS.
  Every slice cleared a container-level functional DoD. Package layout as designed:
  `internal/oidc` (pure core) + `internal/oidc/{signing,memory,httpapi,controlapi}` adapters
  + `internal/app` composition. Signing/verification are stdlib-only (no go-jose).
- Execution blueprint (now the build record): `.journal/002/mock-oidc-implementation-plan.md`
  — the slice-by-slice plan, all boxes ticked, per-slice DONE status lines with merge shas.
  Read it to understand how a given behavior was built and which PR shipped it.
- **ACCEPTANCE COMPLETE (2026-07-03, session 003):** browser acceptance gate run
  against the container image (chrome-devtools, port-remapped) — 91/92 checks PASS
  (1 expected skip). Two blockers found+fixed: PR #15 (static index.html unreachable
  via http.ServeFile redirect) and PR #16 (sub-less tokens on non-interactive
  auth-code; now UUID fallback, upstream parity). The reusable test console lives in
  `webtest/` (PR #17; see webtest/README.md — serve via staticAssetsPath, never
  file://). Full report: `.journal/003/acceptance-report.md`.
- **v0.1.0 RELEASED (2026-07-03, session 003):** `master` at `b97dc99`, tag `v0.1.0`,
  image `ghcr.io/meigma/mock-oidc:v0.1.0` (multi-arch, cosign-signed, SLSA provenance
  for binary + image, verified). The GitHub release is still a DRAFT pending a human
  publish. Repo is dual-licensed **Apache-2.0 OR MIT** (LICENSE-APACHE/LICENSE-MIT).
  Docs are now a Diátaxis MkDocs site under `docs/docs/` (tutorial/how-to/reference/
  explanation) with a slim ~120-line README; contributor detail lives in CONTRIBUTING.md.
- **Release-chain facts for future maintainers:** there is intentionally NO `ghd.toml`
  / ghd distribution (removed PR #22 — do not re-add). The bootstrap version is pinned
  by `initial-version: 0.1.0` in `release-please-config.json` (release-please ignores
  the manifest version when no tag exists — that knob, not the manifest, sets the first
  release). Release dry-run jobs (`release-dry-run.yml`) execute the workflow from the
  PR's HEAD branch and only run on `release-please--*` branches, so a release PR cut
  before a workflow fix keeps running the OLD workflow — recreate the release PR
  (close + delete branch + re-dispatch release-please) to pick up workflow fixes.
  Release App creds are repo-level (`MEIGMA_RELEASE_APP_ID` var +
  `MEIGMA_RELEASE_APP_PRIVATE_KEY` secret), sourced from 1Password
  `op://Homelab/meigma-release-please` (fields app_id, client_id, key.pem attachment).
- **Ops gotcha (2026-07-03):** a `gh` token missing the `workflow` scope cannot
  `gh pr merge`/`update-branch` a PR that touches `.github/workflows/*` unless the
  resulting file content was already pushed by an authorized identity — for Dependabot
  PRs, comment `@dependabot rebase` then merge. golangci-lint's cache goes stale when a
  worktree it indexed is removed (`root:lint` fails with "no such file or directory");
  fix with `mise x -- golangci-lint cache clean`. `wt remove` deletes in the background
  and races `mise run image-local` (melange snapshots the repo incl. `.git/wt/trash/`);
  let removal settle before building the image.
- Ops gotchas (2026-07-03): golangci-lint's cache goes stale when a worktree it
  indexed is removed — `root:lint` fails with "no such file or directory" noise;
  fix with `mise x -- golangci-lint cache clean`. `wt remove` deletes in the
  background via `.git/wt/trash/` and races `mise run image-local` (melange
  snapshots the repo incl. trash) — wait for `.git/wt/trash/` to empty first.
- Technical design: `.journal/001/mock-oidc-technical-design.md` — normative build
  blueprint. Hexagonal Go reusing the template's Huma/chi base; core domain pkg
  `internal/oidc` (pure) + driven adapters `internal/oidc/{signing,memory}` + driving
  adapters `internal/oidc/{httpapi,controlapi}`; authz + postgres removed; strong typing
  (closed enums, parse-don't-validate, no map[string]any in core); /{issuer}/ path-param
  routing; container-first control plane under `/_mock/`. Slice 1 = core token pipeline.
  Has 5 OPEN QUESTIONS to decide before implementation (see its Open Questions section) —
  notably the depguard `crypto/*` ban vs core PKCE/template code, and Huma's default
  SchemaLinkTransformer polluting discovery/JWKS. Module path will be github.com/meigma/
  mock-oidc; binary cmd/mock-oidc (not yet applied to the repo).
