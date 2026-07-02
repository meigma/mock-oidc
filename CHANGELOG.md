# Changelog

## [1.0.4](https://github.com/meigma/mock-oidc/compare/v1.0.3...v1.0.4) (2026-06-28)


### Bug Fixes

* **release:** log in to GHCR in the reusable attest workflow before pushing image provenance ([#33](https://github.com/meigma/mock-oidc/issues/33)) ([dcc4a6c](https://github.com/meigma/mock-oidc/commit/dcc4a6cd313b868ade08be345189d414bca78f4d))

## [1.0.3](https://github.com/meigma/mock-oidc/compare/v1.0.2...v1.0.3) (2026-06-28)


### Bug Fixes

* **release:** create the apko --sbom-path directory before publishing ([#31](https://github.com/meigma/mock-oidc/issues/31)) ([4287c53](https://github.com/meigma/mock-oidc/commit/4287c533b0fc07297dfb932d419ec8f3165d01be))

## [1.0.2](https://github.com/meigma/mock-oidc/compare/v1.0.1...v1.0.2) (2026-06-28)


### Bug Fixes

* **release:** grant the binary attestation caller the packages scope its reusable workflow needs ([#29](https://github.com/meigma/mock-oidc/issues/29)) ([a8e1fc5](https://github.com/meigma/mock-oidc/commit/a8e1fc5ec7f6ca0115254700dde2d0d1e22758c8))

## [1.0.1](https://github.com/meigma/mock-oidc/compare/v1.0.0...v1.0.1) (2026-06-28)


### Chores

* release 1.0.1 ([c071914](https://github.com/meigma/mock-oidc/commit/c07191459342fa6ac7bf24bc7e2f029c4974c26d))

## 1.0.0 (2026-06-26)


### Features

* add todo vertical slice (chi + Huma API server) ([745a9ed](https://github.com/meigma/mock-oidc/commit/745a9ed31cee7a9721598e751fc7cf83cf4fc664))
* **api:** add OpenTelemetry tracing (HTTP + DB spans) ([#18](https://github.com/meigma/mock-oidc/issues/18)) ([6625ab1](https://github.com/meigma/mock-oidc/commit/6625ab1fa91695eec83b5a5bab38b87756328f02))
* **api:** add per-client IP rate limiting ([#17](https://github.com/meigma/mock-oidc/issues/17)) ([867662f](https://github.com/meigma/mock-oidc/commit/867662fd37e03c4eb02c79eb263dd7f44272e54f))
* **api:** finish template — docs pipeline, CORS/client-IP, request-scoped logs, named readiness ([05f5446](https://github.com/meigma/mock-oidc/commit/05f54464c22f504dec5e3327b2b2e8d65a183a26))
* **api:** serve resource routes under a /v1 version prefix ([#16](https://github.com/meigma/mock-oidc/issues/16)) ([a485f7e](https://github.com/meigma/mock-oidc/commit/a485f7e0d1d848442a6ae306cc9d59daf6e0f866))
* **authz:** add Cedar-based authorization tier with deferred API-key authn ([#10](https://github.com/meigma/mock-oidc/issues/10)) ([13a1fe5](https://github.com/meigma/mock-oidc/commit/13a1fe5945919525dd6974d9e2dd153ab8031c69))
* **compose:** add day-one Docker Compose stack (API + PostgreSQL + SQL seeding) ([#7](https://github.com/meigma/mock-oidc/issues/7)) ([8b68bd4](https://github.com/meigma/mock-oidc/commit/8b68bd4dac8ac22f14170653f519a6c00a6dafa8))
* **postgres:** add PostgreSQL persistence tier (sqlc + pgx + goose) ([#6](https://github.com/meigma/mock-oidc/issues/6)) ([18b56e7](https://github.com/meigma/mock-oidc/commit/18b56e72dd1b0859e820e68d71d8852a4913de44))
* **todo:** paginate the list endpoint with keyset cursors ([#14](https://github.com/meigma/mock-oidc/issues/14)) ([879e2be](https://github.com/meigma/mock-oidc/commit/879e2beb956a9bb1f72f6869dc602d1cb8462ba0))


### Bug Fixes

* **authz:** store API keys as SHA-256 hashes at rest ([#13](https://github.com/meigma/mock-oidc/issues/13)) ([ff55a2e](https://github.com/meigma/mock-oidc/commit/ff55a2ef24ef1aa9b306765382da68918c654325))

## Changelog

All notable changes to this project will be documented in this file. This
project follows [Conventional Commits](https://www.conventionalcommits.org) and
releases are managed by Release Please. No releases have been cut yet.
