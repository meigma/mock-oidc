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
