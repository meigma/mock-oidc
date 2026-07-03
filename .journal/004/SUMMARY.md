---
id: 004
title: Login templates — first beyond-parity feature, shipped in v0.1.1
date: 2026-07-03
status: complete
repos_touched: [mock-oidc]
related_sessions: [001, 002, 003]
---

## Goal
Investigate adding a "template system" to mock-oidc and deliver it. Scope as
clarified by Josh: **named login principals** (`{name, subject, claims}`) —
selectable from a dropdown on the interactive login page, and usable headlessly
by automated test systems that initiate a login flow naming a template and skip
form-filling entirely. Explicitly NOT HTML styling (upstream's `loginPagePath`
static-file override stays unimplemented).

## Outcome
**Goal met and shipped.** The feature went from investigation → locked design →
approved plan → implementation (PR #25, squash-merged `7c976bd`) → release
**v0.1.1** (release PR #26 → `877166c`, tag `v0.1.1`, image
`ghcr.io/meigma/mock-oidc:v0.1.1` @ `sha256:4e9a8649…`, provenance verified with
a negative control, published image smoke-tested with a mounted config). The
GitHub release is a **draft awaiting Josh's publish click** (v0.1.0 has been
published). Container-level DoD passed: headless `login_hint` curl flow minted
`{sub: alice, email, roles}`; unknown hint errored loudly; a real browser
(chrome-devtools) confirmed the dropdown pre-fills, stays editable, and
"— none —" leaves edits untouched (screenshot in `.journal/004/`).

## Key Decisions
- **Template = named principal, config-only** -> `loginTemplates` top-level key
  in the existing JSON config (Docker-mountable via `JSON_CONFIG_PATH`), global
  scope, shape exactly `{name, subject, claims}`, no runtime CRUD, no token
  overrides (those stay in token callbacks).
- **Headless selection via standard `login_hint`** -> off-the-shelf OIDC client
  libraries can send it with zero custom HTTP code; upstream assigns the param
  no meaning, so there is no conflict. Bare template name, no prefix.
- **Template wins over `interactiveLogin`/`prompt=login`; unknown name is a
  hard `invalid_request`** -> fail-loud beats silent fallthrough for automated
  suites; the hint is only interpreted while templates are configured, so
  real-world `login_hint` values are untouched otherwise.
- **Dropdown pre-fills, never auto-submits** -> human keeps editing power; the
  domain never sees the dropdown (adapter rendering concern), while headless
  resolution lives in `AuthorizeService` — both funnel into the existing
  `LoginSubmission`, so template claims inherit login-claim merge semantics
  (putIfAbsent at mint) for free.
- **Escaping strategy: data attributes + attribute auto-escaping, no
  `template.JS`** -> claims JSON rides `data-claims` (html/template-escaped),
  browser un-escapes on read; the inline script is static (no `{{}}` actions).
  No new XSS surface.
- **Complementary to the scenario queue, not a replacement** -> templates are
  client-selected at flow time, reusable, parallel-safe; the `/_mock` queue
  remains the server-side one-shot pre-arrangement tool.

## Changes
- `internal/oidc/logintemplate.go` (+test) — `LoginTemplate`, ordered
  unique-name `LoginTemplates`, config-time sentinels, `UnknownLoginTemplate`.
- `internal/oidc/authorize.go` — `WithLoginTemplates` option + the hint branch
  (after the `response_type` check, before interactive/prompt).
- `internal/oidc/httpapi/{authorize,login,registrar,html}.go` +
  `html/login.html` — `login_hint` on both authorize ops, `Deps.LoginTemplates`,
  `claimsToJSON`, `{{if .Templates}}`-gated dropdown + pre-fill script.
- `internal/config/jsonconfig.go` (+tests) — `loginTemplates` parsing with
  index-tagged fail-fast validation; `internal/app/app.go` wiring.
- `docs/docs/openapi.yaml` — regenerated (login_hint on both ops).
- `internal/integration/logintemplate_flow_test.go` — container e2e;
  `webtest/` — two automated checks + dropdown in the manual login check.
- Docs: new `docs/docs/how-to/log-in-with-login-templates.md`, configuration
  reference entry, "Beyond parity" section in `explanation/parity.md`, nav,
  README bullet + doc link.

## Open Threads
- **The v0.1.1 GitHub release is a DRAFT** — human publish pending (v0.1.0 is
  published).
- **release-please maps `feat`→patch pre-1.0** (`bump-patch-for-minor-pre-major:
  true`), which is why this shipped as 0.1.1 rather than 0.2.0. Deliberate
  config, but revisit if features should bump minor before 1.0.
- **`attest-image` annotations persist** (artifact-metadata:write / storage
  record; non-fatal, carried from session 003) — attestations verify fine.
- Carried from earlier sessions: `create-github-app-token` `app-id` deprecation;
  `.agents/` skill docs still say `template-go-api` (cosmetic).

## References
- PRs (both `meigma/mock-oidc`, squash): #25 `7c976bd` (feature), #26 `877166c`
  (release 0.1.1).
- Release: tag `v0.1.1`, run 28680269710 (green), image digest
  `sha256:4e9a864903ed…02a096`, 9 release assets (4 binaries, 4 SBOMs,
  checksums).
- Plan file: `~/.claude/plans/1-agreed-2-agreed-graceful-giraffe.md` (approved
  verbatim); design decisions recorded in `.journal/004/NOTES.md`.
- Browser evidence: `.journal/004/login-page-template-dropdown.png`.
- Prior sessions: `.journal/003/SUMMARY.md` (release chain facts),
  `.journal/002/SUMMARY.md` (implementation layout).

## Lessons
- **An orphaned chrome-devtools automation browser blocks the MCP** with "browser
  already running" for its profile; even `list_pages` fails. Fix:
  `pkill -f 'chrome-devtools-mcp/chrome-profile'` (it is the MCP-owned
  automation profile, not the user's browser), then reconnect.
- **Run `mise x -- moon run check` before assuming lint-clean**: this repo's
  gate stack (funcorder, godoclint, golines, testifylint, modernize/mapsloop)
  flags style that compiles fine — six findings on a first pass that unit tests
  never see.
