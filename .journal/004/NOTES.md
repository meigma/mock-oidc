---
id: 004
title: Investigate adding a template system
started: 2026-07-03
---

## 2026-07-03 11:40 — Kickoff
Goal for the session: investigate adding a template system to the current
mock-oidc implementation. This is an investigation session — scope, options,
and design come before any code.

Current state of the world: v0.1.0 is released (tag `v0.1.0`, image
`ghcr.io/meigma/mock-oidc:v0.1.0`, provenance-verified; the GitHub release is
still a draft awaiting a human publish). The full parity design from sessions
001–002 is shipped: all 6 grants, token lifecycle, multi-issuer, `/_mock`
control plane, proxy/TLS, `staticAssetsPath`. Known related open thread from
session 002: the custom login page (`loginPagePath`) was deferred as low-risk
polish — likely adjacent to any template-system work, since upstream's login
page templating is the nearest existing concept.

Plan: await the user's direction on what "template system" means here (e.g.
templated login/interaction pages, templated token/claim responses, or
something else), then survey the current implementation and upstream parity
catalog before proposing a design.

## 2026-07-03 11:55 — Scope clarified: template = named principal, not HTML styling
Josh defined the target use case: (1) a template selection box on the login
page listing config-provided "template principals" to log in with; (2) the
same system usable headlessly — an automated test initiates a login flow
naming a template and skips form-filling entirely. Not interested in styling
(upstream's `loginPagePath` static-file override) right now.

Grounding: interactive login funnels through `Authorize.SubmitLogin(req,
LoginSubmission)` (httpapi/login.go → oidc/identity.go); a template resolves
to a LoginSubmission, so GUI dropdown + headless param are two front doors to
one domain concept. Distinct from the slice-5 scenario queue / request-mapping
callbacks (server-side pre-arrangement); templates are client-selected, named,
reusable at flow time — better for parallel suites.

Open design questions posed to Josh: headless selection mechanism
(`login_hint` vs mock-specific param vs both — load-bearing), GUI auto-submit
vs pre-fill, template scope (global/per-issuer; just subject+claims or token
overrides too), config-only vs runtime CRUD via `/_mock`.

## 2026-07-03 12:10 — Design decisions locked (Josh) + code survey
Decisions: (1) headless selection via standard `login_hint` param; (2) GUI
dropdown PRE-FILLS username/claims, human can edit before submit; (3) template
shape is just `{name, subject, claims}`, global scope, no token overrides;
(4) config-only — must work via Docker-mounted config, no runtime CRUD.

Survey findings: JSON config precedence JSON_CONFIG > JSON_CONFIG_PATH >
./config.json (internal/config/jsonconfig.go) — Docker mount flow already
exists; templates = new top-level key in the same document (unknown keys are
lenient, upstream configs unaffected). `login_hint` is read nowhere today —
zero conflict. AuthorizeService.Authorize() decides ShowLogin vs auto-issue;
SubmitLogin(req, LoginSubmission) is the single funnel — a template resolves
to a LoginSubmission for both paths. Login page is httpapi/html/login.html
(inline CSS, no static tree needed).

Remaining micro-decisions to settle with Josh: unknown-template login_hint
behavior (error vs fall-through), login_hint vs prompt=login precedence,
whether the hint value is the bare template name or prefixed.

## 2026-07-03 12:45 — Implemented end to end; PR #25 open
Plan approved (plan file: 1-agreed-2-agreed-graceful-giraffe.md) and executed on
worktree `feat/login-templates` (six signed commits, ba35c9a..9cb7268):
domain LoginTemplate/LoginTemplates + WithLoginTemplates hint branch →
httpapi login_hint + Deps + dropdown/pre-fill JS → config loginTemplates
parsing + wiring + OpenAPI regen → integration/webtest coverage → docs
(new how-to log-in-with-login-templates, config reference, Beyond parity,
README) → lint fixes.

Verification: `mise x -- moon run check` green (12 tasks; caught funcorder/
godoclint/golines/testifylint/mapsloop first pass — fixed). Image built
(`mise run image-local`), full integration suite green incl. new
TestContainerLoginTemplates. Manual container DoD with Docker-MOUNTED
config.json: headless login_hint flow minted id_token {sub: alice, email,
roles:[admin]}; unknown hint → invalid_request with clear description;
dropdown HTML escaped correctly. Browser check (chrome-devtools): pre-fill
works, fields stay editable, "— none —" leaves edits untouched — screenshot
at .journal/004/login-page-template-dropdown.png. Gotcha hit: an orphaned
MCP automation Chrome held the chrome-devtools profile lock; pkill -f
'chrome-devtools-mcp/chrome-profile' cleared it.

PR: https://github.com/meigma/mock-oidc/pull/25 (awaiting review/merge).

## 2026-07-03 12:55 — PR #25 merged
All CI checks green (release dry-runs skipped as expected on a non-release
branch). Squash-merged as `7c976bd` "feat(oidc): add login templates for
interactive and headless login (#25)"; remote branch deleted, local master
pulled, worktree removed via `wt remove`. Feature is on master. Session goal
(investigate + deliver a template system) is complete pending session close.

## 2026-07-03 13:10 — v0.1.1 released and verified
release-please PR #26 proposed 0.1.1 (NOT 0.2.0 — correct per config:
`bump-patch-for-minor-pre-major: true` maps feat→patch pre-1.0). All dry-run
gates green (melange amd64/arm64, binary + container dry-runs) → squash-merged
as `877166c` → release.yml run 28680269710 fully green. Same two non-fatal
attest-image annotations as v0.1.0 (artifact-metadata:write / storage record)
— known open thread, attestations verify regardless.

Verified: tag `v0.1.1` exists; GitHub release is a DRAFT (by design, human
publish pending) with 9 assets (4 binaries + 4 SBOMs + checksums). Image
ghcr.io/meigma/mock-oidc:v0.1.1
(sha256:4e9a864903ed6cde4a9f41d945f09ff735de2b2a1d554a81355698c4ed02a096):
`gh attestation verify` OK with wrong-repo negative control rejected.
Published-image smoke test with mounted config: /isalive OK, headless
login_hint flow OK, dropdown rendered OK, binary stamps 0.1.1/877166c.
