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
