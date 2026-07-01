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
- Execution blueprint: `.journal/002/mock-oidc-implementation-plan.md` — the working,
  slice-by-slice implementation plan (Slices 0–6 + cross-cutting testing) that turns the
  technical design into an ordered, file-level, functionally-gated task list. Start here to
  implement; it defers to the technical design on any conflict. Read before beginning a slice.
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
