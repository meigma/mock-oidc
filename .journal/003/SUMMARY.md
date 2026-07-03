---
id: 003
title: Browser acceptance pass, publish prep, and the v0.1.0 release
date: 2026-07-02
status: complete
repos_touched: [mock-oidc]
related_sessions: [001, 002]
---

## Goal
Take the feature-complete mock-oidc server (all 7 slices merged in session 002)
to a published first release. The session began as a browser-level "acceptance"
pass — build a repo-local test-only HTML/JS console and drive it with the
chrome-devtools MCP to prove the whole server works from a real browser before
declaring it ready — then expanded into the full pre-publish checklist: fix what
acceptance found, license the repo, reset the release lineage, configure the
GitHub repo, clear the Dependabot backlog, write real docs, and cut v0.1.0.

## Outcome
**Goal met — v0.1.0 is released and provenance-verified end to end** (the
GitHub release is a draft by design, awaiting a human publish click). Sequence:

- **Acceptance console + pass.** Built `webtest/` (a vanilla ES-module console,
  87 automated checks across 8 suites + 5 manual browser flows) served by the
  server's own `staticAssetsPath`, and ran it against the **container image**
  with a remapped host port. Final: 91 PASS / 0 FAIL / 1 expected SKIP, plus a
  green out-of-browser curl block (zero-config boot, TLS, proxy identity,
  traversal guard, metrics, banner). The pass found two real server defects.
- **Two blocker fixes** (separate PRs, image rebuilt, suites re-run green):
  `/static/index.html` was unreachable (`http.ServeFile` 301-redirects
  `*/index.html` → a 404'd directory path) → served via `ServeContent`;
  non-interactive `authorization_code` tokens carried **no `sub` claim**
  (OIDC Core violation; the session-002 open thread, confirmed) → per-callback
  UUID fallback in `DefaultTokenCallback.Subject`.
- **Publish prep.** Dual-licensed Apache-2.0 OR MIT; reset the release lineage
  from the template's fictitious `1.0.4` to a real bootstrap `v0.1.0`
  (`initial-version: 0.1.0` after learning release-please ignores the manifest
  at bootstrap); ran `.github/scripts/configure_github_repo.py apply` to
  convergence.
- **Release credentials + pipeline fixes.** Wired the `meigma-release-please`
  GitHub App creds from 1Password into repo secrets. The first real
  release-please PR exposed two release-chain defects (both required checks):
  the workflows validated against an intentionally-removed `ghd.toml`, and both
  container smoke tests had a `openapi | grep -q` broken-pipe race. Removed the
  ghd integration (kept artifact/checksum validation + asset staging) and
  de-raced the smoke tests.
- **Dependabot backlog.** Merged all six open PRs (#1–#6).
- **Docs.** Replaced the dense 438-line README with a Diátaxis MkDocs site
  (1 tutorial, 11 how-tos incl. a migration guide, 5 reference pages, 4
  explanations) + a ~120-line README entry point; moved dev detail into
  CONTRIBUTING.md. Verified live against a running server.
- **Release.** Merged the release PR → tag `v0.1.0` → the full `release.yml`
  pipeline (8 jobs) went green: multi-arch signed image, SBOMs, and SLSA
  provenance attestations for both binary and image, all independently verified
  (cosign + `gh attestation verify` with negative controls).

## Key Decisions
- **Accept against the container image, not the local binary** -> we accept
  exactly what we publish; the remapped host port made the pass double as the
  port-remap/proxy-identity check.
- **Fix blockers, log the rest** -> the two flow-breaking defects got their own
  fix PRs + acceptance re-run; nothing minor was left silently.
- **`webtest/` is throwaway, journal-reported** -> the console is a repo-local
  testing tool, not a product; the full acceptance report lives in the journal,
  only the console code went to the repo.
- **`initial-version: 0.1.0`** -> with no prior tag, release-please bootstraps to
  its default (1.0.0) and ignores the `0.0.0` manifest; `initial-version` is the
  correct knob and is inert after the first release.
- **Remove the ghd integration rather than author `ghd.toml`** -> the file's
  removal was intentional (user-confirmed); the release chain was made
  consistent with that instead of resurrecting it.
- **Diátaxis docs site as the home; README as an entry point** -> the README was
  four doc types at once; splitting by reader need (learn/do/look-up/understand)
  and shrinking the README fixed the density problem at its root.
- **Model policy on all subagents** -> every workflow/agent call pinned Opus (or
  Sonnet for trivial work), never inheriting the session's Fable model, to
  protect quota across ~100 subagent invocations.

## Changes
- `internal/oidc/httpapi/static.go` (+test) — serve static files via
  `ServeContent`, fixing the unreachable `index.html` (PR #15).
- `internal/oidc/callback.go` (+test) — per-callback UUID subject fallback
  (PR #16).
- `webtest/` — new repo-local browser acceptance console (PR #17).
- `LICENSE-APACHE`, `LICENSE-MIT`, README/melange/apko license metadata — dual
  license (PR #18).
- `.release-please-manifest.json`, `CHANGELOG.md`, melange/apko version stamps —
  reset to 0.0.0 (PR #19); `release-please-config.json` `initial-version`
  (PR #21).
- `.github/workflows/release.yml`, `release-dry-run.yml`,
  `.github/scripts/stage_release_assets.py` (renamed from `stage_ghd_*`, +test) —
  drop ghd, de-race the openapi smoke tests (PR #22).
- `docs/docs/**` (22 pages) + `docs/mkdocs.yml` nav, README.md rewrite,
  CONTRIBUTING.md — the Diátaxis docs site (PR #24).
- Dependabot bumps: actions/cache, setup-go, goreleaser-action, actions/attest,
  otelhttp 0.69.0, x/time 0.15.0 (PRs #1–#6).
- Repo settings applied via `configure_github_repo.py`; repo `MEIGMA_RELEASE_APP_ID`
  variable + `MEIGMA_RELEASE_APP_PRIVATE_KEY` secret set from 1Password.
- `master` release commit `b97dc99` → tag `v0.1.0` + draft release (PR #23).

## Open Threads
- **The v0.1.0 GitHub release is still a DRAFT** — publishing it (SLSA
  human-in-the-loop step) is left to the user.
- **`attest-image` job annotations** — non-fatal "artifact-metadata:write" /
  "no artifacts found" storage-record warnings; the attestations were created
  and verify correctly, but the workflow's `artifact-metadata` permission is
  worth a later look.
- **`create-github-app-token` deprecation** — the release workflow uses the
  deprecated `app-id` input; the 1Password item also carries `client_id` if we
  migrate to `client-id`.
- **`.agents/` skill docs still say `template-go-api`** (cosmetic, out of gate
  scope) — carried over from session 002.

## References
- Merged PRs (all `meigma/mock-oidc`): #1–#6 (dependabot), #15 `aa05d23`
  (static fix), #16 `8b8d9e0` (subject fix), #17 `d0850b6` (webtest console),
  #18 `b0d17bc` (dual license), #19 `2aacbd0` (release reset), #21
  (initial-version), #22 `171ee1d` (drop ghd + smoke fix), #24 `b96725f` (docs),
  #23 `b97dc99` (release 0.1.0).
- Release: tag `v0.1.0`, `release.yml` run 28676000496 (8/8 green), image
  `ghcr.io/meigma/mock-oidc:v0.1.0` digest `sha256:be058ab8…`.
- Acceptance report + console exports + screenshots: `.journal/003/`.
- Prior sessions: `.journal/002/SUMMARY.md` (implementation), `.journal/001/SUMMARY.md`
  (design).

## Lessons
- **Container-deployment behaviors need a real deployed run.** Both blocker
  defects (unreachable `/static/index.html`; sub-less tokens on the default
  zero-config path) survived the entire session-002 in-process + container
  integration suite and only surfaced when a browser drove the *published*
  image. An acceptance layer that exercises the real artifact from a real client
  earns its keep.
- **release-please bootstraps to its own default, not the manifest.** With no
  prior tag it proposed 1.0.0 despite a 0.0.0 manifest; `initial-version` is the
  knob. Don't assume the manifest wins at bootstrap.
- **Release dry-run jobs run the workflow from the PR head branch.** A
  release-please PR cut before a workflow fix keeps running the *old* workflow;
  chore/ci merges don't refresh it. Recreating the release PR (close + delete
  branch + re-dispatch release-please) is how you get the fixed workflow onto
  it. A container smoke test that "passed" once was merely winning a pipe race.
- **OAuth `workflow` scope + Dependabot merges.** A `gh` token without the
  `workflow` scope can still squash-merge a workflow-touching PR *iff* the
  resulting blob already exists from an authorized push — so `@dependabot rebase`
  (which pushes as an authorized identity) unblocks the merge; `gh pr
  update-branch` does not.
- **Verify provenance with negative controls.** `gh attestation verify` printed
  nothing to stdout yet exited 0; a wrong-signer/wrong-repo negative control
  proved the exit code was meaningful and the attestations genuine.
- **Prompt-injection hygiene.** One Explore subagent returned a fake `System:`
  directive trying to propagate an append-string instruction; ignored and
  relaunched clean. Treat subagent output as data.
