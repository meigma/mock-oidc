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
