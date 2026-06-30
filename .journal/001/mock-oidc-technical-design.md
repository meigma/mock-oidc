# mock-oidc — Technical Design

`mock-oidc` is a zero-configuration, in-memory OpenID Connect / OAuth2 provider built **for testing** — a Go rebuild of the upstream mock OIDC server, distributed as a single static binary and a nonroot OCI image, and driven primarily from Testcontainers suites. This Technical Design is the implementation blueprint: it is the third leg of a three-document set and is **normative** for the build.

- The **parity catalog** is the behavioral ground truth — what upstream actually does, endpoint by endpoint, quirk by quirk. It answers *"what must this server do?"*
- The **PRD** scopes the product — goals, non-goals, the validation scenarios (S-series), and the capability requirements (C-series). It answers *"what are we building and why?"*
- This **Technical Design** answers *"how is it built, in Go, correctly?"* It maps the catalog's behavior and the PRD's requirements onto concrete packages, types, ports, adapters, and wiring, and it is bound by the separately-published **Binding Design Contract** (the TDD spine), whose decisions are settled and whose names are used verbatim here.

Throughout, the governing stance is **parity-in-intent** (PRD D-2 / P7 / N8): match what upstream is *for*, correct its defects, and do not replicate its quirks. Where a section preserves or deliberately diverges from upstream behavior, it says so.

Three principles are enforced — not aspirational — across every section:

1. **Hexagonal architecture (ports & adapters).** A pure application core (`internal/oidc`) depends only on consumer-declared interfaces; all transport, crypto, IO, and time live in adapters that the composition root wires inward. The dependency rule is enforced by a depguard rule, an architecture unit test, and the type system itself.
2. **Strong typing (parse-don't-validate).** Untyped external input is parsed into distinct named domain types *at the adapter edge*. Smart constructors return `(T, error)`; enums are closed `type X string` sets with `Valid()`; typed errors carry their own client-visible shape. No `map[string]any`, bare `string` grant types, or raw `url.Values` ever cross into the core.
3. **Clean code.** The build reuses the template's proven slice idioms — smart constructors, sentinel/typed errors, consumer-declared ports, DTO separation with explicit `mapping.go`, functional-Option seams for clock/ID — so adding mock-oidc is *"swap the slice, keep the chassis."*

---

## Foundations & Architecture

This section fixes the structural vocabulary the rest of the design uses: the module identity and rename surface, the package tree and per-package responsibility, the layering and dependency rule (and the concrete machinery that enforces it), the file-level KEEP/REMOVE/ADD outcome against the template, the single Huma content-type strategy, the dual error model, and the multi-issuer routing decision. Detailed wiring (flag/env precedence, JSON-config parsing, dual-server lifecycle) belongs to the Config/App section; this section establishes *where things live and which way they may point*.

---

### 1. Module, naming & rename ripples

The identifiers below are settled (Contract §1). Every tooling identifier is a mechanical substitution; section authors MUST use these values verbatim.

| Concern | Value |
|---|---|
| Go module path | `github.com/meigma/mock-oidc` |
| Binary / entrypoint dir | `cmd/mock-oidc` (`cmd/mock-oidc/main.go`) |
| OpenAPI title const (`api.go` `apiTitle`) | `mock-oidc` |
| OTel `service.name` (`app.go` `serviceName`) | `mock-oidc` |
| Viper env prefix | `MOCK_OIDC_` |
| goimports `local-prefixes` | `github.com/meigma/mock-oidc` |
| GHCR image | `ghcr.io/meigma/mock-oidc` |
| release-please / melange / apko `package-name` | `mock-oidc` |

**Env-var parity aliases.** In addition to the `MOCK_OIDC_*` prefix, the CLI MUST bind these *unprefixed* upstream names via explicit `viper.BindEnv` (the prefix replacer will not match them): `SERVER_HOSTNAME`, `SERVER_PORT`, `PORT`, `JSON_CONFIG`, `JSON_CONFIG_PATH`, `LOG_LEVEL`. Precedence parity: `SERVER_PORT` > `PORT` > `8080`; JSON config source `JSON_CONFIG` (inline) > `JSON_CONFIG_PATH` (file) > `./config.json` > built-in defaults. `LOGBACK_CONFIG` is accepted and ignored (no-op alias).

**Rename ripples (NOTE ONLY — do not redesign tooling).** The module/binary rename is a mechanical substitution across `go.mod`, ~43 import sites, the `cmd/` dir, `.mockery.yaml` package keys, `.golangci.yml` local-prefixes, `moon.yml` build/openapi paths, `melange.yaml`/`apko.yaml`/`.goreleaser.yaml`/`release-please-config.json` identifiers, and the workflow `IMAGE_NAME`/`binary_name` literals. `mise/moon/golangci/mockery/melange/apko/goreleaser/release-please/workflows` keep their mechanics. `.moon/workspace.yml`, `.moon/toolchains.yml`, `ci.yml`, `attest.yml`, `AGENTS.md`, `CLAUDE.md` contain no old identifier and MUST be left untouched. The pre-existing `ghd.toml` reference inconsistency in `release-dry-run.yml`/`moon.yml` is surfaced, not fixed here.

---

### 2. Vocabulary (used doc-wide)

| Term | Means | Concretely |
|---|---|---|
| **Application core** / **domain core** | The pure use-case + value layer. Zero transport/IO/crypto/framework imports. | `internal/oidc` (package root files only) |
| **Application service** | A use-case orchestrator that drives outbound ports over pure value types. Holds no wire/IO knowledge. | `ProviderService`, `AuthorizeService`, `TokenService`, `SessionService` |
| **Driving / inbound adapter** | Translates untyped external input into typed domain commands, calls a service, maps the result back to the wire. The *anti-corruption boundary*. | `internal/oidc/httpapi`, `internal/oidc/controlapi` |
| **Driven / outbound adapter** | A concrete implementation of a core-declared port: crypto, in-memory state. | `internal/oidc/signing`, `internal/oidc/memory` |
| **Outbound port** | A consumer-declared interface the core depends on. The only way the core reaches IO/crypto/time. | `KeyStore`, `Signer`, `TokenVerifier`, `CodeStore`, `RefreshTokenStore`, `CallbackQueue`, `IssuerRegistry`, `RequestRecorder`, `Clock` |
| **Generic transport** | The reusable, resource-agnostic chi+Huma substrate the inbound adapters mount onto. | `internal/adapter/http` |
| **Registrar seam** | `type Registrar func(huma.API)` — the one hook a transport substrate exposes for mounting operations. Both inbound adapters provide one. | `internal/adapter/http.Registrar` |
| **Pre-/post-register Huma invariant** | Huma snapshots the API middleware stack into each operation *at registration time*; document metadata can still be mutated after. Middleware installs **before** `Register`; OpenAPI security stamping runs **after**. | `RouterDeps` ordering in `router.go` |
| **Composition root** / **edges** | The only packages allowed to name concrete adapters and wire ports to them. | `internal/app`, `internal/cli`, `internal/config` |
| **Shared infra leaves** | Dependency-free cross-cutting utilities; no domain knowledge. | `internal/observability`, `internal/logctx`, `internal/ratelimit` |
| **Reserved control prefix** | The fixed path namespace the control plane and infra own; an `IssuerID` may not collide with it. | `/_mock/...` (control), root infra (`/healthz`, `/readyz`, `/metrics`, `/isalive`) |

The five lettered tiers used below are: **(D)** driving adapters, **(C)** core, **(P)** ports, **(A)** driven adapters, **(E)** edges.

---

### 3. The hexagon at a glance

```
                              DRIVING / INBOUND ADAPTERS  (D)
        parse untyped wire input -> typed domain commands (§7);  map results -> wire

   HTTP / browser ─▶  internal/oidc/httpapi        Huma ops on /{issuer}/...:
   OAuth2 clients     (OAuth2/OIDC transport)      authorize · token · userinfo ·
                                                   introspect · revoke · endsession ·
                                                   jwks · discovery · login · debugger
   Test harness  ─▶  internal/oidc/controlapi      Control plane on /_mock/... (RFC 9457):
   (Testcontainers)  (control / inspection)        enqueue scenario · capture inspect · mint
            │                       │
            │   DTOs + form parsers + mapping.go   (anti-corruption boundary)
            ▼                       ▼
   ┌────────────────────────────────────────────────────────────────────────────┐
   │            APPLICATION CORE — internal/oidc (root files only)   (C)         │
   │                                                                            │
   │  Application services (use-case orchestration; ports + pure values only):  │
   │     ProviderService  ·  AuthorizeService  ·  TokenService  ·  SessionSvc   │
   │                                                                            │
   │  Pure domain: smart constructors (T,error) · invariants-at-construction ·  │
   │  closed enums (GrantType, SigningAlgorithm, ResponseMode, …) ·             │
   │  typed ProtocolError/ErrorCode · ClaimSet (no map[string]any inward)       │
   │                                                                            │
   │  FORBIDDEN imports: huma, chi, humachi, net/http, net/url, viper, cobra,   │
   │  crypto signing (rsa/ecdsa/ed25519/tls/x509), go-jose/jwx, otel,           │
   │  prometheus, pgx, internal/adapter/*.  Permitted: crypto/sha256,           │
   │  crypto/subtle, encoding/base64 (keyless PKCE — the one carve-out).        │
   └────────────────────────────────────────────────────────────────────────────┘
            │   OUTBOUND PORTS (interfaces declared BY the core in ports.go / clock.go)   (P)
            ▼
     KeyStore · Signer · TokenVerifier · CodeStore · RefreshTokenStore ·
     CallbackQueue · IssuerRegistry · RequestRecorder · Clock
            │                       │
            ▼                       ▼
                              DRIVEN / OUTBOUND ADAPTERS  (A)
   internal/oidc/signing   crypto/* + JOSE:  Signer · TokenVerifier · KeyStore
                           (RSA-2048, EC P-256/P-384; embedded 5-key RSA JWKS seed)
   internal/oidc/memory    sync-guarded maps: IssuerRegistry · CodeStore ·
                           RefreshTokenStore · CallbackQueue · RequestRecorder
   internal/oidc/mocks     generated testify fakes of every port (tests only)

   ───────────────────────────────────────────────────────────────────────────────
   SUBSTRATE & EDGES  (E):  internal/adapter/http (chi+Huma router, infra routes,
   problem fallbacks) hosts the inbound adapters via the Registrar seam.
   internal/app wires concrete (A) adapters to (P) ports and injects them into (C);
   internal/cli + internal/config own process concerns (flags, env, JSON-config).
   Shared leaves: internal/observability · internal/logctx · internal/ratelimit.
```

Read the arrows as *compile-time dependency direction*: everything points **inward** to `internal/oidc`. The two inbound adapters call **into** the core; the core calls **out** only through the port interfaces it owns; the driven adapters depend **on** the core (to implement its ports), never the reverse; the edges depend on everything but nothing depends on the edges.

---

### 4. Package tree & per-package responsibility

The OIDC protocol is one tightly interconnected aggregate (tokens → claims → subjects → issuers), so the domain is **one** package with adapters as subpackages — mirroring the template's `internal/todo/{httpapi,postgres,todotest,mocks}` convention, not one-package-per-endpoint.

| Path | Tier | Responsibility |
|---|---|---|
| `cmd/mock-oidc/main.go` | E | Process entry: signal context, ldflag version vars, `cli.ExecuteContext`. |
| `internal/oidc/` (root) | C | **Core.** All value types, smart constructors, invariants, closed enums, typed errors, the outbound ports, and the four application services. Files per Contract §2 (`issuer.go`, `client.go`, `identity.go`, `grant.go`, `algorithm.go`, `claims.go`, `token.go`, `code.go`, `refresh.go`, `keys.go`, `discovery.go`, `introspection.go`, `requests.go`, `responses.go`, `callback.go`, `capture.go`, `errors.go`, `clock.go`, `ports.go`, `provider.go`, `authorize.go`, `token.go`, `session.go`). |
| `internal/oidc/doc.go` | C | Package doc that **states the forbidden-imports rule** as the human-readable companion to the depguard rule (see the dependency rule below). |
| `internal/oidc/httpapi/` | D | Inbound OAuth2/OIDC HTTP adapter (Huma). Wire DTOs (`dto.go`), domain↔DTO mapping (`mapping.go`), the two typed form parsers (`form.go`), the RFC 6749 error writer (`oautherr.go`), per-endpoint registrations, and embedded `html/` templates. Provides one `Registrar`. |
| `internal/oidc/controlapi/` | D | Inbound control plane (Huma, RFC 9457) delivering PRD **C6** against a running container: enqueue scenario, capture/replay inspection, direct token mint. Mounts under `/_mock/...`. Provides one `Registrar`. |
| `internal/oidc/signing/` | A | Outbound crypto adapter: implements `Signer`, `TokenVerifier`, `KeyStore`. RSA-2048 / EC P-256,P-384; embedded 5-key RSA JWKS seed; `kid = issuerID`. The *only* package importing a JOSE library. |
| `internal/oidc/memory/` | A | Outbound in-memory adapters (production, not test-only — parity: upstream uses in-memory maps): `IssuerRegistry`, `CodeStore`, `RefreshTokenStore`, `CallbackQueue`, `RequestRecorder`, and the mutable `Clock`. Concurrency-safe (`sync`). |
| `internal/oidc/mocks/` | A | Committed mockery-generated testify mocks of the outbound ports, regenerated by the `mockery` moon task. |
| `internal/adapter/http/` | E/sub | **Kept** generic chi+Huma transport: `NewRouter`/`RouterDeps`, `NewAPI`/`Registrar`/`SpecYAML`, health, metrics, `problem`, `middleware/*`, `ratelimit.go` seam — authz hooks removed. Stays free of any `internal/oidc` import. |
| `internal/app/` | E | Composition root: `New(ctx, cfg, logger, version, opts...)` + `Option`/lifecycle. Wires (A)→(P)→(C), composes both `Registrar`s, owns dual-server `Run`/shutdown. |
| `internal/cli/` | E | Cobra tree: root(=serve), serve, version, openapi. (`migrate` removed.) |
| `internal/config/` | E | Typed `Config` quad-pattern + the typed JSON-config-document parser producing a domain seed (issuers, tokenCallbacks, systemTime, initialKeys, interactiveLogin, rotateRefreshToken). |
| `internal/observability/` | E-leaf | **Kept:** slog logging, otel tracing, prometheus metrics, request log. No domain dependency. |
| `internal/logctx/` | E-leaf | **Kept:** dependency-free request-scoped logger context key. The *one* logging seam the core may import. |
| `internal/ratelimit/` | E-leaf | **Kept:** token-bucket limiter — **default disabled** (Contract §4). |

---

### 5. Layering & the dependency rule

Three concentric tiers, dependencies strictly inward (Contract §3 restated as the binding rule, not re-argued):

1. **Core — `internal/oidc` (root only).** MAY import the standard library, `log/slog`, and `internal/logctx`. MUST NOT import `huma`, `chi`, `humachi`, `net/http`, `net/url`, the crypto **signing**/JOSE packages (`crypto/rsa`, `crypto/ecdsa`, `crypto/ed25519`, `crypto/tls`, `crypto/x509`, `go-jose`/`jwx`), `viper`, `cobra`, `otel`, `prometheus`, `pgx`, or any `internal/adapter/*`. Crypto signing, HTTP, IO, and time are reached *only* through the port interfaces in `ports.go`/`clock.go`. **The sole crypto carve-out** is the keyless PKCE S256 transform (`crypto/sha256`, `crypto/subtle`, `encoding/base64`) — pure, keyless computation, permitted in the core and *not* denied by the lint/test gates below (this reconciles the contract's "no crypto" intent with the PKCE-in-domain decision so the build gate cannot fail the very code the domain mandates).

2. **Adapters — `internal/oidc/{httpapi,controlapi,signing,memory}`, `internal/adapter/http`.** Each imports `internal/oidc` (inward) plus exactly the one technology it adapts. **Adapters MUST NOT import each other** — no horizontal `adapter→adapter` coupling; they meet only at the composition root.

3. **Edges — `internal/app`, `internal/cli`, `internal/config`.** The only packages that name concrete adapters; `app` wires ports to adapters via functional `Option`s.

**Parse-don't-validate boundary.** All untyped input (form bytes, query strings, JSON config, headers) is parsed into the typed commands of Contract §7 *at the adapter edge*. No `map[string]any`, bare `string` grant types, or raw `url.Values` ever cross into `internal/oidc`. The same rule governs the *outbound* test-inspection direction: `capture.go`'s `NewCapturedRequest` takes only stdlib-primitive/domain types (`method string`, `rawURL string`, `header map[string][]string`, `query map[string][]string`, `body []byte`); the httpapi recording edge converts `r.URL.String()` and `map[string][]string(r.Header)` — never the `*url.URL`/`http.Header` values themselves — so `internal/oidc` never names `net/http`/`net/url`.

#### How the rule is enforced — four mechanisms, not vibes

**(a) Consumer-declared ports.** Every outbound dependency is an interface *the core owns*, in `ports.go`. The driven adapter depends on the core to satisfy it; the arrow is structurally inward. This is the same idiom the template uses for `todo.Repository`.

```go
// internal/oidc/ports.go  (representative subset — canonical signatures are
// frozen in the Domain Types section)
package oidc

import "context"

// Signer mints signed JWTs for an issuer. Declared here, by its consumer (the
// services), and implemented by an adapter (internal/oidc/signing). The core
// never sees a private key, a crypto.Signer, or a JOSE type — only this port.
type Signer interface {
	Sign(ctx context.Context, key KeyID, header JWTHeader, claims ClaimSet) (SignedToken, error)
}

// KeyStore materializes an issuer's signing key on first reference
// (computeIfAbsent / zero-config issuers, P2/C4) and exposes only public metadata.
type KeyStore interface {
	SigningKey(ctx context.Context, issuer IssuerID, alg SigningAlgorithm) (SigningKey, error)
	PublicKeys(ctx context.Context, issuer IssuerID) (JWKS, error)
}

// CodeStore caches single-use authorization codes with their request snapshot.
type CodeStore interface {
	Put(ctx context.Context, code AuthorizationCode, rec CodeRecord) error
	Take(ctx context.Context, code AuthorizationCode) (CodeRecord, error) // single-use: removes on read
}
```

**(b) `doc.go` as the human contract.** The forbidden-imports rule is documented at the package it governs, so it is visible at the point of edit:

```go
// Package oidc is the mock-oidc application core: the pure OIDC/OAuth2 domain
// (typed values, invariants, closed enums, typed errors) and the application
// services that orchestrate use-cases over outbound ports.
//
// Dependency rule (enforced by the depguard "oidc-core" rule in .golangci.yml
// and by TestCoreImportsAreClean): this package MAY import only the standard
// library, log/slog, and internal/logctx. It MUST NOT import huma, chi, humachi,
// net/http, net/url, the crypto signing/JOSE packages (crypto/rsa, crypto/ecdsa,
// crypto/ed25519, crypto/tls, crypto/x509, go-jose/jwx), viper, cobra, otel,
// prometheus, pgx, or any internal/adapter/*. Crypto signing, HTTP, IO, and time
// are reached solely through the ports declared in ports.go and clock.go.
//
// One carve-out: the keyless PKCE S256 transform may use crypto/sha256,
// crypto/subtle, and encoding/base64 — pure, keyless computation, not key-bearing
// signing — and these are deliberately NOT denied by the depguard rule.
package oidc
```

**(c) A depguard rule** in the already-present `.golangci.yml` (the repo already runs depguard with a `rules:` map; we add one scoped rule). `files` scopes it to the core root and excludes subpackage adapters via a path-prefix negation; `listMode: lax` + `deny` blocks the forbidden set with a reason. Note that blanket `crypto` is intentionally *not* denied — only key-bearing signing packages are:

```yaml
# .golangci.yml -> linters-settings.depguard.rules (added beside "deprecated")
"oidc-core":
  files:
    - "**/internal/oidc/*.go"          # root files only; subpackage adapters excluded
    - "!**/internal/oidc/*/**"
  deny:
    - pkg: github.com/danielgtaylor/huma
      desc: domain core is transport-free; map at the httpapi/controlapi edge
    - pkg: github.com/go-chi/chi
      desc: domain core is transport-free
    - pkg: net/http
      desc: HTTP is an adapter concern; use the inbound DTOs/commands instead
    - pkg: net/url
      desc: parse url.Values at the adapter edge into typed commands (§7)
    - pkg: crypto/rsa
      desc: RSA signing lives in internal/oidc/signing behind Signer/KeyStore
    - pkg: crypto/ecdsa
      desc: EC signing lives in internal/oidc/signing behind Signer/KeyStore
    - pkg: crypto/ed25519
      desc: rejected algorithm family; never produced by the core
    - pkg: crypto/tls
      desc: transport crypto is an adapter concern
    - pkg: crypto/x509
      desc: certificate handling is a signing-adapter detail
    - pkg: github.com/go-jose/go-jose
      desc: JOSE is a signing-adapter detail
    - pkg: github.com/spf13/viper
    - pkg: github.com/spf13/cobra
    - pkg: go.opentelemetry.io/otel
    - pkg: github.com/prometheus/client_golang
    - pkg: github.com/jackc/pgx
    - pkg: github.com/meigma/mock-oidc/internal/adapter
  # NOTE: blanket `crypto` is intentionally NOT denied — the keyless PKCE S256
  # transform (crypto/sha256, crypto/subtle, encoding/base64) is pure domain
  # computation and stays in the core; only key-bearing signing packages are denied.
```

**(d) An architecture unit test** as defense-in-depth (the template's constant-sync discipline applied to imports). It loads the core's *non-test* import graph and fails on any forbidden path — catching transitive leaks depguard's prefix globs can miss:

```go
// internal/oidc/arch_test.go
func TestCoreImportsAreClean(t *testing.T) {
	pkgs, err := packages.Load(&packages.Config{Mode: packages.NeedImports | packages.NeedName},
		"github.com/meigma/mock-oidc/internal/oidc")
	require.NoError(t, err)

	// Key-bearing signing crypto and all transport/framework packages are forbidden.
	// crypto/sha256, crypto/subtle, encoding/base64 are deliberately ABSENT from this
	// list: the keyless PKCE S256 transform is pure domain computation (Contract §3 carve-out).
	forbidden := []string{
		"net/http", "net/url",
		"crypto/rsa", "crypto/ecdsa", "crypto/ed25519", "crypto/tls", "crypto/x509",
		"github.com/danielgtaylor/huma", "github.com/go-chi/chi", "github.com/go-jose/",
		"github.com/spf13/", "go.opentelemetry.io/", "github.com/prometheus/",
		"github.com/meigma/mock-oidc/internal/adapter",
	}
	for _, p := range pkgs {
		for imp := range p.Imports {
			for _, bad := range forbidden {
				assert.NotEqual(t, bad, imp, "core imports %q (matched %q)", imp, bad)
				// (prefix variant for the trailing-slash entries elided)
			}
		}
	}
}
```

Together: the **ports** make the inward direction the path of least resistance, `doc.go` documents intent, depguard fails fast in-editor/CI, and the arch test closes transitive gaps. The depguard rule, the `arch_test.go` forbidden list, and `doc.go` are kept word-for-word consistent on the crypto carve-out, so no gate contradicts the PKCE-in-domain decision.

---

### 6. KEEP / REMOVE / ADD outcome vs the template

The decisions are settled in Contract §4; this is the *file-level outcome* grounded in the template's actual tree.

| Action | Template path(s) | Result |
|---|---|---|
| **KEEP** | `internal/adapter/http/{router,api,health,metrics,ratelimit}.go`, `internal/adapter/http/{problem,middleware}/*` | Retained; identifier/wording-only edits. Authz hooks (`InstallAuthz`/`FinalizeAuthz` fields + call sites) deleted from `RouterDeps`/`NewRouter`. A `RouterDeps.FallbackWriter` strategy is **added** (see Error model) so the substrate stays free of any `internal/oidc` import. |
| **KEEP** | `internal/observability/*`, `internal/logctx/*`, `internal/ratelimit/*` | Retained verbatim; re-word authz-referencing godoc. Rate limiting defaults **disabled**. |
| **KEEP** | `internal/config/config.go` quad-pattern + server fields | Retained; DB/authz fields removed (below), JSON-config seed parser added. |
| **KEEP** | `internal/app/{app,serve}.go` shape, `internal/cli/{root,serve,version,openapi}.go` | Retained shape; wiring re-pointed from todo+postgres+authz to oidc+memory+signing. |
| **REMOVE** | `internal/authz/**` (incl. `apikey/**`, `mocks/**`, `todo/authz/**`) | Deleted. `app.go` `authzInstaller`/`resolveAuthenticator`/`WithAuthenticator`, `RouterDeps.InstallAuthz`/`FinalizeAuthz`, `SpecYAML` finalize usage, per-op `authz.Require` metadata, apiKey OpenAPI stamping all removed. *(mock-oidc IS the auth provider; it never authenticates its own callers.)* |
| **REMOVE** | `internal/adapter/postgres/**`, `internal/todo/postgres/**`, `internal/todo/**` (whole slice), `internal/integration/*postgres*`/`*apikey*` | Deleted wholesale. `app.go` `resolveStore`/`pool`/`closePool`/`WithRepository(pg)`, `noopRepository`, the `/v1` `registerResources` group, `internal/cli/migrate.go` + its `AddCommand` removed. |
| **REMOVE** | Config fields `DatabaseURL`, `DBMaxConns`, `AuthzEnabled`, `AuthzPolicyDir`; `RateLimit*` required validation | Deleted, with their Validate clauses. |
| **REMOVE** | `sqlc.yaml`, goose migrations, `hack/sql/*`, compose `postgres`/`migrate`/`seed`, moon `sqlc*`/`migrate` tasks, `.mockery.yaml` authz/apikey entries | Deleted. `go mod tidy` drops `cedar-go`, `pgx`, `goose`, likely `uuid`. |
| **ADD** | — | `internal/oidc` core (§4), `internal/oidc/httpapi`, `internal/oidc/controlapi`, `internal/oidc/signing`, `internal/oidc/memory`, `internal/oidc/mocks`. `.mockery.yaml` `packages:` re-pointed to the new ports. |
| **ADD** | — | Proxy-aware `ResolveBaseURL` (core) + `RequestOrigin` extraction at the transport edge; reserved `/_mock` control prefix; `/isalive` parity alias; readiness slice empty (`/readyz` unconditionally ready — no DB). |

Net effect on the dependency graph: the template's *generic* outer ring (transport substrate + observability + edges) survives almost intact; the entire *resource-specific* inner content (todo + postgres + authz) is replaced by the OIDC hexagon. The substitution is possible precisely because the template already isolates the resource behind the `Registrar` seam and consumer-declared ports — adding mock-oidc is *"swap the slice, keep the chassis."*

---

### 7. Huma content-type strategy

**Single coherent strategy: the ENTIRE OAuth2/OIDC surface goes through Huma operations; raw chi is reserved for infra only (`/healthz`, `/readyz`, `/metrics`).** Every endpoint stays in the OpenAPI document and inherits Huma middleware. Section authors MUST use these four mechanisms and no others:

1. **Form-body endpoints (`/token`, `/introspect`, `/revoke`, login POST):** input struct field `RawBody []byte` with `` `contentType:"application/x-www-form-urlencoded"` ``. Huma stores raw bytes without parsing/validating; the adapter parses `url.Values` into the typed command (Contract §7) in `httpapi/form.go`. **Two typed parsers** are provided and not unified: a flat last-wins parser (grant dispatch, revoke, introspect, login) and a multi-valued parser (request-mapping templating, populating the domain-owned `FormParams` carrier — not `url.Values` — so nothing untyped crosses inward). Parsing intent is preserved; upstream's silent-truncation-on-second-`=` quirk is **not** replicated. After `huma.Register`, hand-inject the `application/x-www-form-urlencoded` request-body object schema onto the operation for OpenAPI fidelity. **Mechanism note (corrected):** Huma v2 exposes no `OpenAPI().Operations()` accessor — operations live on the path item. The post-register stamp walks `api.OpenAPI().Paths[path].Post` (the method field on the `*huma.PathItem`, the same Paths-based access the template's authz stamping used) and replaces the `RequestBody.Content["application/x-www-form-urlencoded"].Schema` that Huma already pre-populated for the `RawBody` field.

2. **302 redirects (`/authorize` success, `/endsession`):** output struct `{ Status int; Location string `+"`header:\"Location\"`"+` }`, no body; set `Operation.DefaultStatus = 302`. `/authorize` query params are taken as **permissive strings** (no Huma `required`/min/max) so the handler — not the framework — decides whether an error becomes a redirect-with-error vs a direct OAuth2 error. Multi-method endpoints (`/endsession` GET+POST, the debugger callback) are registered **once per method** with distinct OperationIDs bound to the same handler function — one handler, two `huma.Register` calls.

3. **HTML (login page, `form_post` auto-submit, debugger, error page, favicon, static):** output struct `{ ContentType string `+"`header:\"Content-Type\"`"+`; Body []byte }` set to `text/html; charset=utf-8`. `huma.StreamResponse` only where full header/status hand-control is needed. Multi-segment static asset trees are served by a raw chi wildcard route (`/static/*`), not a single-segment Huma path param.

4. **OAuth2 error body:** NEVER override the global `huma.NewError` (that would reshape RFC 9457 across the whole API). Model the OAuth2 error as a **per-operation typed, success-shaped output** `{ Status int; Body OAuth2Error }` returned (not as a Go `error`); the handler sets 400/401. Because form endpoints use `RawBody`, no framework 422 can pre-empt the handler. **Every protocol-family endpoint — including discovery and JWKS — uses this success-shaped envelope on failure** (never a Go `error` that would route through Huma's RFC 9457 path), so the protocol surface has exactly one stated error contract (see Error model). See §8.

**OpenAPI document hygiene.** `internal/adapter/http.NewAPI` starts from `huma.DefaultConfig` but **strips the `SchemaLinkTransformer`** (or serves discovery/JWKS as pre-serialized `Body []byte`). That default transformer prepends a `$schema` field and a `Link: …; rel="describedBy"` header to every concrete struct body — which would both break the `DiscoveryDocument` fixed-field-order invariant and inject non-standard fields into documents consumed by strict third-party OIDC clients. Declare `oauth2`/`openIdConnect` security schemes in `Components.SecuritySchemes` using the same post-register stamping pattern the template used for authz. Raw chi infra routes stay intentionally absent from the spec.

---

### 8. Error model

Two error contracts coexist, split by route family:

**A. OAuth2/OIDC protocol endpoints → RFC 6749 §5.2 JSON.** Shape: `{"error": "...", "error_description": "...", "error_uri"?: "..."}`, media type `application/json`, status `400`/`401` (and `WWW-Authenticate` where the spec calls for it). **Parity-in-intent correction: error JSON is NOT lowercased** (upstream lowercases the entire body, mangling `error_description`); we emit correct-case text. The `302→400` status-coercion upstream quirk is not replicated.

**B. Control API + infra → RFC 9457 problem+json.** The control plane (`controlapi`) is our own JSON API and keeps Huma's default `application/problem+json` error model; infra fallbacks (404/405/recover/timeout) on non-protocol routes keep `problem.Write`.

**Typed error → response mapping:**

- Domain raises a **pointer** `*oidc.ProtocolError{ Code ErrorCode; Description string; HTTPStatus int }` built via `NewProtocolError(code ErrorCode, desc string, status int) *ProtocolError` (the `Error()` method has a pointer receiver, so values are never returned). The `ErrorCode` set is closed, with Go constants prefixed `Code*`: `CodeInvalidRequest` (`invalid_request`), `CodeInvalidGrant` (`invalid_grant`), `CodeInvalidClient` (`invalid_client`), `CodeInvalidToken` (`invalid_token`), `CodeUnsupportedTokenType` (`unsupported_token_type`), `CodeUnsupportedGrantType` (`unsupported_grant_type`), `CodeServerError` (`server_error`), `CodeNotFound` (`not_found`). The `Code*` constants are the single naming used everywhere; the `Err*` identifiers are reserved for sentinel error *values*, never for `ErrorCode` members.
- Constructors mirror upstream throwers (`MissingParameter`, `InvalidGrant`, `InvalidClient`, `MalformedRequest`, etc.), each returning a `*ProtocolError` with the exact client-visible description tests assert — including the corrected refresh cross-issuer text `"refresh_token was issued by a different issuer"`. **Edge parse functions return typed `*ProtocolError`, never bare wrapped sentinels** (otherwise `errors.As` misses them and they coerce to `server_error`/500): e.g. blank `grant_type` → `MissingParameter("grant_type")` (`invalid_request`, 400) and unknown → `UnsupportedGrant(g)` (`invalid_grant`, exact text `"grant_type <x> not supported."`, 400). Issuer construction is unified on a single `ParseIssuerID` returning `*ProtocolError` (reserved-prefix collision → `CodeNotFound`/404 wrapping a distinguishable `ErrReservedIssuer`; empty or contains-`/` → `CodeInvalidRequest`/400), so both `errors.As(&*ProtocolError)` and `errors.Is(ErrReservedIssuer)` resolve consistently.
- `httpapi/oautherr.go` translates `*ProtocolError` (via `errors.As`) into the per-operation `OAuth2Error` output of §7(4) at the mapped status. Any non-`ProtocolError`/internal error on a protocol route becomes `server_error` (500). The `/authorize` path additionally routes errors **through a redirect** (error in `redirect_uri` query/fragment) when a usable `redirect_uri` is present, else a direct OAuth2 error response.
- The router's non-Huma fallbacks (404/405/recover/timeout) MUST emit the OAuth2 shape on protocol route families and the RFC 9457 shape on control/infra families. To keep the generic transport resource-agnostic, this is **not** an `internal/oidc` import inside `internal/adapter/http`: the composition root populates a `RouterDeps.FallbackWriter` strategy (an ordered `{prefixMatcher, writeFunc}` set) with the OIDC-shaped writer for protocol families and `problem.Write` for control/infra. The `problem` package is retained solely for the control/infra surface.

`OAuth2Error` MAY implement `ContentTypeFilter` to pin `application/json`. Domain `ErrorCode` and the advertised discovery/JWKS algorithm set MUST be cross-checked by a unit test (the template's constant-sync discipline): *algorithms advertised in discovery == algorithms the signer can actually produce*, sourced from the single `SupportedSigningAlgorithms()` accessor.

---

### 9. Multi-issuer routing & the two inbound seams

**Decision:** issuers are addressed by a **path parameter**, `/{issuer}/<endpoint>`, registered once per endpoint as a normal Huma operation with a `path:"issuer"` parameter. This **deliberately diverges** from upstream's `request.url.endsWith(path)` suffix matching (PRD D-2, parity-in-intent): suffix matching is an implementation quirk, not a contract clients depend on; `/{issuer}/...` is the clean, OpenAPI-expressible, equivalent-in-intent form.

Both inbound adapters plug into the **one** `Registrar` seam the kept transport exposes — the same mechanism the template's `internal/todo/httpapi.Register` used. The composition root composes them onto a single Huma API so they share one OpenAPI document, the middleware stack, and the pre-/post-register invariant:

```go
// internal/app — registrar composition (shape; see Config/App section for full wiring)
func registerOIDC(p httpapi.Deps, c controlapi.Deps) adapterhttp.Registrar {
	return func(api huma.API) {
		// OAuth2/OIDC surface on /{issuer}/...  (Huma path param; NOT huma.NewGroup —
		// issuers are dynamic, materialized on first reference. Contract §8.)
		httpapi.Register(api, p)
		// Control plane on the reserved /_mock prefix (RFC 9457). IssuerID's
		// constructor rejects "_mock", so issuer routing and control are unambiguous.
		controlapi.Register(api, c)
	}
}
```

Routing rules:

- **Zero-config, on-demand issuers preserved (P2/C4):** any non-reserved `{issuer}` value is accepted; the `IssuerRegistry`/`KeyStore` materialize the issuer and its lazily-generated key (`kid = issuer`) on first reference (`computeIfAbsent` semantics). No pre-registration. The issuer is a **runtime path parameter** validated into an `IssuerID` inside each handler via `ParseIssuerID` — so there is no per-issuer group; **do NOT use `huma.NewGroup` per issuer**.
- **`iss` and every endpoint URL derive from `ResolveBaseURL(RequestOrigin)`** (proxy-aware: `x-forwarded-proto` > scheme; `Host` host > original; port precedence `x-forwarded-port` > `Host` port > scheme default), joined with the issuer segment at host root. This is a NEW domain concern, separate from the kept IP-only `ClientIP` middleware. `ResolveBaseURL` returns `(BaseURL, error)` — forwarded headers are client-controlled and malformable, so callers handle the error.
- **Metric/trace cardinality:** labels use the chi route **template** (`/{issuer}/token`), never the raw `{issuer}` value — `{issuer}` is client-controlled and would explode time series.
- **Reserved control prefix:** the control plane (`controlapi`) and infra routes mount under reserved fixed paths that an issuer ID may not collide with. `ParseIssuerID` rejects values beginning with the reserved segment `_mock` (control plane lives at `/_mock/...`); infra stays at root (`/healthz`, `/readyz`, `/metrics`), and an `/isalive` parity alias is provided.
- **Request recording** is a mux-level chi middleware that buffers+restores `r.Body`, guarded by path prefix — it records the OIDC protocol families and early-returns for `/_mock/*`, `/healthz`, `/readyz`, `/metrics`, `/openapi*`, `/docs`. (In the co-located deployment there is one chi mux, so there is no separate "OIDC subtree"; the prefix guard, not a sub-router, scopes recording. The dedicated-listener case gives true physical separation.)

**Named parity gap — single-segment issuers.** Because `IssuerID` forbids `/`, only single-segment issuer IDs (`/acme/token`) are representable; upstream's suffix matching admits deeply-nested IDs (`/tenant/sub/default/token` → issuer `tenant/sub/default`). The `/{issuer}/...` form is therefore equivalent-in-intent for common single-segment usage but **not** for multi-segment / Azure-style nested issuers — this is recorded as a deliberate, justified limitation (not silent divergence), so adopters relying on nested issuer paths are warned rather than surprised.

---

### 10. Composition-root shape (high level)

`internal/app` keeps the template's `New(ctx, cfg, logger, version, opts...) (*App, error)` signature and functional-`Option` pattern. Its new responsibility is the hexagon assembly: build the driven adapters, declare them *as ports*, hand the ports to the four services, and expose both inbound adapters through the `Registrar`. Detailed flag/env/JSON-config resolution and the dual-server lifecycle are the Config/App section's job — this is only the structural skeleton that proves the layering closes.

```go
// internal/app/app.go  (SKELETON — wiring detail belongs to the Config/App section)
func New(ctx context.Context, cfg config.Config, logger *slog.Logger,
	version string, opts ...Option) (*App, error) {

	o := applyOptions(opts)             // test seams: WithClock, port overrides, etc.
	clock := o.clock                    // oidc.Clock port: the MUTABLE memory.Clock adapter
	                                    // (also satisfies controlapi.ClockController), seeded
	                                    // from systemTime and advanced via /_mock/clock.
	                                    // oidc.FixedClock/SystemClock are test-only seams.

	// ---- Driven / outbound adapters (A), each satisfying a core-declared port (P) ----
	keys := signing.NewKeyStore(cfg.Seed.InitialKeys) // KeyStore + Signer + TokenVerifier
	var (
		signer   oidc.Signer            = keys
		verifier oidc.TokenVerifier     = keys
		keyStore oidc.KeyStore          = keys
		issuers  oidc.IssuerRegistry    = memory.NewIssuerRegistry()
		codes    oidc.CodeStore         = memory.NewCodeStore(clock)
		refresh  oidc.RefreshTokenStore = memory.NewRefreshTokenStore(clock)
		queue    oidc.CallbackQueue     = memory.NewCallbackQueue()
		recorder oidc.RequestRecorder   = memory.NewRequestRecorder()
	)

	// ---- Application core (C): services depend ONLY on ports + values ----
	provider  := oidc.NewProviderService(issuers, keyStore, clock)
	authorize := oidc.NewAuthorizeService(codes, queue, issuers, clock)
	tokens    := oidc.NewTokenService(signer, verifier, codes, refresh, queue, issuers, clock)
	session   := oidc.NewSessionService(verifier, refresh, recorder, issuers, clock)

	// ---- Driving / inbound adapters (D), composed onto the kept transport (E) ----
	register := registerOIDC(
		httpapi.Deps{Provider: provider, Authorize: authorize, Tokens: tokens, Sessions: session,
			Login: cfg.Seed.InteractiveLogin},
		controlapi.Deps{Tokens: tokens, Scenarios: queue, Requests: recorder, Clock: clock},
	)

	handler := adapterhttp.NewRouter(adapterhttp.RouterDeps{
		Logger: logger, Metrics: observability.NewMetrics(), Version: version,
		RequestTimeout: cfg.RequestTimeout, CORSAllowedOrigins: cfg.CORSAllowedOrigins,
		TrustedProxyHeader: cfg.TrustedProxyHeader,
		Readiness:        nil,                 // no DB: /readyz is unconditionally ready
		Register:         register,
		Tracing:          cfg.TracingEnabled,
		InstallRateLimit: maybeRateLimit(cfg), // default nil (disabled)
		FallbackWriter:   protocolAwareFallback(), // OAuth2 shape on /{issuer}/* families,
		                                            // problem.Write on /_mock + infra — keeps
		                                            // adapter/http free of any oidc import
		// InstallAuthz / FinalizeAuthz fields deleted — mock-oidc never authenticates callers
	})
	// ... server construction + lifecycle: Config/App section.
}
```

Three structural facts this skeleton pins down, which the rest of the doc relies on:

- **`internal/app` is the only place where a `signing.*` or `memory.*` constructor name appears.** Re-typing each adapter to its port interface immediately at the assignment (`var signer oidc.Signer = keys`) makes the wiring read as port-shaped and lets a test swap any port for an `oidc/mocks` fake with no other change.
- **The services receive ports, never adapters.** `NewTokenService(signer, verifier, …)` cannot, by its signature, reach into crypto or HTTP — the type system carries the dependency rule into the constructor, not just the linter.
- **`Readiness: nil`, `InstallAuthz` absent, rate-limit default-off, and the injected `FallbackWriter`** are the visible residue of the REMOVE column and the resource-agnostic-substrate rule — the chassis is identical to the template; only the slice, three policy defaults, and the fallback strategy changed.

This establishes the structural frame; the Domain Types, Huma/Content-Type, Error-Model, Signing, Control-Plane, and Config/App sections fill each box in.

## Domain Model & Type System

> Scope: the value types, closed sets, smart constructors, and typed errors that live in the `internal/oidc` package **root** — the files `issuer.go`, `client.go`, `identity.go`, `grant.go`, `algorithm.go`, `claims.go`, `token.go`, `code.go`, `refresh.go`, `keys.go`, `requests.go`, `errors.go`, `clock.go`. The application services (`provider.go`/`authorize.go`/`token.go`/`session.go`), the outbound ports (`ports.go`), the control-time capture value (`capture.go`), the callbacks (`callback.go`), and the DTO/anti-corruption boundary (`httpapi/`) are covered by their own sections. This section establishes the vocabulary every other section consumes; the names here are the contract's §7 glossary names, used verbatim.

### 1. Typing doctrine

The domain core is the safety system. Three rules are mechanical, not aspirational:

1. **A distinct named type for every concept that has an invariant.** `IssuerID`, `Subject`, `ClientID`, `Scope`, `KeyID`, `Nonce`, `SignedToken` are all `type X string` — but they are not interchangeable, so the compiler rejects passing a `ClientID` where a `Subject` is wanted. This is the cheapest bug class to delete.
2. **Parse, don't validate.** Every type that can be malformed is unconstructible except through a `New*`/`Parse*` smart constructor returning `(T, error)`. There is no exported way to build an invalid value, so any value of the type in hand is, by construction, valid. Invariants are checked **once**, at the edge, and never re-checked inward.
3. **Closed sets are closed.** `GrantType`, `ResponseType`, `ResponseMode`, `TokenType`, `IssuedTokenType`, `SigningAlgorithm`, `ClaimName`, `CodeChallengeMethod`, `ClientAuth`, `RefreshFormat`, and `ErrorCode` are enumerated `type X string`/`int` values whose only legal members are package constants. `Parse*` is the *only* ingress; unknown text is a typed error, never a silently-tolerated string. No `map[string]any`, bare `string` grant type, or raw `url.Values` is permitted past the adapter boundary (contract §3).

The canonical constructor shape, shown on the most load-bearing parser:

```go
// ParseGrantType converts wire text into a GrantType. Because grant_type arrives
// on a protocol route, a parse failure must already carry the OAuth2 code/status
// the client will see — so this returns a typed *ProtocolError (never a bare
// sentinel the oautherr adapter would have to re-classify into server_error/500):
// blank -> invalid_request "missing required parameter grant_type"; unknown ->
// invalid_grant "grant_type <x> not supported." A returned GrantType is, by
// construction, one the token dispatcher can handle.
func ParseGrantType(s string) (GrantType, error) {
	if s == "" {
		return "", MissingParameter("grant_type")
	}
	g := GrantType(s)
	if !g.Valid() {
		return "", UnsupportedGrant(s)
	}
	return g, nil
}
```

**Two error contracts for constructors (coherence decision).** Parsers whose failure can reach a live protocol route — `ParseGrantType`, `ParseIssuerID` — return a typed `*ProtocolError` directly, so the `httpapi/oautherr.go` adapter's `errors.As(err, &protoErr)` recovers the exact code/status/text without a fallback to `server_error`. Parsers for *config-time / internal* values — `ParseURLScheme`, `ParseSigningAlgorithm`, `ParseInstant` — return a wrapped sentinel (`fmt.Errorf("%w: …", ErrX)` for `errors.Is`), because their failures surface at config load, not on a request. Where both audiences matter, a `*ProtocolError` additionally *wraps* its sentinel (via `Unwrap`) so `errors.Is` and `errors.As` both succeed (see §11).

### 2. Catalog concept → type map

Every behavioral concept in the parity catalog lands on exactly one domain type. This table is the audit that nothing in the catalog is left "stringly typed."

| Catalog concept | Domain type(s) | File |
|---|---|---|
| Zero-config on-demand issuer; `iss` = baseURL + issuerId; `{issuer}` path segment | `IssuerID`, `Issuer` | `issuer.go` |
| Proxy-aware URL resolution (`x-forwarded-*` / `Host`, port precedence, host-root join) | `RequestOrigin`, `BaseURL`, `URLScheme`, `ResolveBaseURL` | `issuer.go` |
| 6 grants dispatched from form `grant_type` | `GrantType` (closed set of 6) | `grant.go` |
| `response_type` (`code` impl; `none`/`id_token`/`token` advertised) | `ResponseType` | `grant.go` |
| `query`/`fragment`/`form_post` | `ResponseMode` | `grant.go` |
| `token_type` (`Bearer`) + token-exchange `issued_token_type` URN | `TokenType`, `IssuedTokenType` | `grant.go` |
| JWS `typ` header (default `JWT`, open — e.g. `at+jwt`) | `JOSEType` | `token.go` |
| RSA/EC alg family; `ES256K`/`ES512`/`EdDSA`/HMAC/`none` rejected; default `RS256` | `SigningAlgorithm` (supported/rejected sets) | `algorithm.go` |
| `kid = issuerId` | `KeyID` (+ `IssuerID.KeyID()`) | `algorithm.go` |
| PKCE `plain`/`S256`; advertised `code_challenge_methods_supported` | `CodeChallengeMethod`, `PKCEChallenge` | `code.go` |
| Default claims (`sub aud iss iat nbf exp jti`, `nonce` if present), `tid`=issuerId, `azp`=client_id (auth_code only) | `ClaimSet`, `ClaimName`, `CustomClaims`, `ClaimValue`, `Audience`, `Nonce` | `claims.go` |
| Subject resolution (cc→client_id, password→username, login→username) | `Subject` | `identity.go` |
| Scope echo + non-OIDC-scope audience derivation | `Scope`, `Scopes` | `identity.go` |
| Effective client_id; secrets never validated; auth methods | `ClientID`, `Client`, `ClientAuth` | `client.go` |
| Unsigned then signed JWT; `alg`/`kid`/`typ` header | `Token`, `SignedToken`, `JWTHeader` | `token.go` |
| Single-use auth code + cached request snapshot (redirect, nonce, PKCE, login) | `AuthorizationCode`, `CodeRecord` | `code.go` |
| Refresh token = bare UUID or `alg=none` PlainJWT `{jti,nonce}`; rotation; issuer binding | `RefreshToken`, `RefreshFormat`, `RefreshRecord` | `refresh.go` |
| Per-issuer public JWKS, public params only, `use=sig` | `SigningKey`, `JWK`, `KeyType`, `JWKS`/`KeySet` | `keys.go` |
| Interactive login POST (`username` required, `claims` optional JSON) | `LoginSubmission` | `requests.go` |
| Multi-valued form access for `${…}` request-mapping | `FormParams` | `requests.go` |
| Frozen/settable `systemTime` driving `iat`/`nbf`/`exp` | `Clock`, `Instant` | `clock.go` |
| OAuth2 error codes + exact upstream descriptions | `ErrorCode`, `ProtocolError` | `errors.go` |

### 3. Identity & issuer addressing

`IssuerID` is the first path segment and the most invariant-laden string in the system: it must be non-empty, contain no `/`, and **must not collide with the reserved control prefix** `_mock` (contract §8). Enforcing that at construction means no handler, registry, or key store ever has to re-check it.

```go
// reservedPrefix is the path segment owned by the control plane and infra
// routes; an IssuerID may never begin with it (see contract §8).
const reservedPrefix = "_mock"

// IssuerID identifies a zero-config, on-demand issuer. It is the first path
// segment of every issuer-scoped URL and, by construction, also the kid of that
// issuer's signing key.
type IssuerID string

// ParseIssuerID parses a path segment into an IssuerID. It is on the request
// path, so it returns a typed *ProtocolError (which also wraps a distinguishable
// sentinel for errors.Is): empty or containing '/' -> invalid_request (400);
// reserved-prefix collision -> not_found (404, wrapping ErrReservedIssuer).
// Materialization of the issuer and its key is the registry's job; this only
// guarantees the identifier is well-formed.
func ParseIssuerID(s string) (IssuerID, error) {
	switch {
	case s == "":
		return "", MissingParameter("issuer")
	case strings.ContainsRune(s, '/'):
		return "", &ProtocolError{
			Code: CodeInvalidRequest, HTTPStatus: 400,
			Description: fmt.Sprintf("issuer %q must not contain '/'", s),
			cause:       ErrInvalidIssuer,
		}
	case s == reservedPrefix || strings.HasPrefix(s, reservedPrefix+"/"):
		return "", &ProtocolError{
			Code: CodeNotFound, HTTPStatus: 404,
			Description: fmt.Sprintf("%q collides with reserved prefix %q", s, reservedPrefix),
			cause:       ErrReservedIssuer,
		}
	}
	return IssuerID(s), nil
}

// KeyID returns the JWS kid for this issuer. The mock keys every issuer
// deterministically by its id (not a thumbprint), so kid == IssuerID always.
func (id IssuerID) KeyID() KeyID { return KeyID(id) }
```

`ParseIssuerID` is the **single** issuer constructor across all sections (the inbound adapter, control plane, config seeds, and the roadmap slices all call it). It returns a `*ProtocolError` so the OAuth2 writer maps it correctly, and it wraps `ErrReservedIssuer`/`ErrInvalidIssuer` so the control plane's `errors.Is(err, oidc.ErrReservedIssuer)` branch still fires — one value, one status per condition, no three-way drift.

**Named parity gap — multi-segment issuer IDs.** Contract §8 binds the issuer to a single Huma `path:"issuer"` segment, and this constructor accordingly rejects any value containing `/`. Upstream's suffix matcher (`path.substringBefore(endpointSuffix)`) accepts a *multi-segment* issuer (e.g. `/tenant/sub/default/token` → issuer `tenant/sub/default`). The `/{issuer}/…` form is equivalent-in-intent for the common single-segment case but is **not** equivalent for nested issuers; deeply-nested Azure-style issuers like `aad/{tenant}/v2.0` are not representable. We record this as a conscious, justified parity gap (rationale: single-segment is the dominant real usage; the OpenAPI-expressible path param is worth the trade), not a silent divergence — adopters with nested issuers are warned here.

**`BaseURL` / `RequestOrigin` — deliberate net/url reconciliation.** The glossary describes `BaseURL` as "struct over `*url.URL`," but contract §3 forbids `net/url` in the domain core. We resolve the tension in favor of the stronger layering rule (the dependency rule is the hard requirement): `BaseURL` holds **typed scalar components** and renders by string formatting; `net/url` parsing of inbound headers happens only at the transport edge that *builds* the `RequestOrigin`. The type stays semantically identical ("host root only") while keeping the core import-clean. (If the contract owner intended `*url.URL` literally, this is the single point that needs ratification.)

```go
// URLScheme is the externally-visible transport scheme an issuer advertises.
type URLScheme string

const (
	SchemeHTTP  URLScheme = "http"
	SchemeHTTPS URLScheme = "https"
)

// ParseURLScheme is a config/edge-time parser: it returns a wrapped sentinel
// (errors.Is) rather than a *ProtocolError, since a bad scheme is a wiring fault.
func ParseURLScheme(s string) (URLScheme, error) {
	switch URLScheme(strings.ToLower(s)) {
	case SchemeHTTP:
		return SchemeHTTP, nil
	case SchemeHTTPS:
		return SchemeHTTPS, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrInvalidScheme, s)
	}
}

func (s URLScheme) defaultPort() int {
	if s == SchemeHTTPS {
		return 443
	}
	return 80
}

// BaseURL is the scheme://host[:port] root an issuer advertises. It carries no
// path: per-issuer endpoint URLs are formed by joining the IssuerID at the host
// root, mirroring upstream's baseUrl().resolve(joinPaths(issuerId, endpoint)).
type BaseURL struct {
	scheme URLScheme
	host   string // host only — no port, no path
	port   int    // 0 means "scheme default", omitted from String()
}

func NewBaseURL(scheme URLScheme, host string, port int) (BaseURL, error) {
	if host == "" {
		return BaseURL{}, fmt.Errorf("%w: host must not be empty", ErrInvalidBaseURL)
	}
	if port < 0 || port > 65535 {
		return BaseURL{}, fmt.Errorf("%w: port %d out of range", ErrInvalidBaseURL, port)
	}
	return BaseURL{scheme: scheme, host: host, port: port}, nil
}

// String renders the host root, omitting the port when it is the scheme default.
func (b BaseURL) String() string {
	if b.port == 0 || b.port == b.scheme.defaultPort() {
		return fmt.Sprintf("%s://%s", b.scheme, b.host)
	}
	return fmt.Sprintf("%s://%s:%d", b.scheme, b.host, b.port)
}

// IssuerURL returns the externally-visible issuer URL (the iss claim value):
// the host root with the issuer segment joined at root, regardless of any
// deeper request path. It returns a plain string — callers must NOT call
// .String() on the result.
func (b BaseURL) IssuerURL(id IssuerID) string {
	return b.String() + "/" + string(id)
}

// RequestOrigin carries the already-extracted candidate address components for
// base-URL resolution. The transport edge (httpapi) parses Host / X-Forwarded-*
// with net/url and fills this; the domain only applies precedence.
type RequestOrigin struct {
	Scheme   URLScheme // original request scheme
	Host     string    // Host-header host, else original host
	Port     int       // Host-header port, else original port (0 = unset)
	FwdProto string    // X-Forwarded-Proto (raw)
	FwdHost  string    // X-Forwarded-Host (raw)
	FwdPort  string    // X-Forwarded-Port (raw)
}

// ResolveBaseURL applies upstream's proxy-aware precedence
// (OAuth2HttpRequest.url): scheme = X-Forwarded-Proto ?: original; host =
// Host header ?: original; port = X-Forwarded-Port > Host port > scheme default.
// It returns (BaseURL, error) because forwarded headers are client-controlled
// and malformable; callers MUST handle the error.
func ResolveBaseURL(o RequestOrigin) (BaseURL, error) {
	scheme := o.Scheme
	if o.FwdProto != "" {
		s, err := ParseURLScheme(o.FwdProto)
		if err != nil {
			return BaseURL{}, err
		}
		scheme = s
	}

	host := o.Host
	if o.FwdHost != "" {
		host = o.FwdHost
	}

	port := o.Port
	if o.FwdPort != "" {
		p, err := strconv.Atoi(o.FwdPort)
		if err != nil {
			return BaseURL{}, fmt.Errorf("%w: x-forwarded-port %q: %v", ErrInvalidBaseURL, o.FwdPort, err)
		}
		port = p
	}
	return NewBaseURL(scheme, host, port)
}
```

`Issuer` is the materialized aggregate the registry hands services: identity + resolved address + public key metadata. It is a value, assembled by the registry/keystore, never parsed from the wire. Per the glossary it is exactly three fields — configured token-callbacks are resolved by the `TokenService`'s callback resolver against the registry, **not** stored on `Issuer`:

```go
// Issuer is a materialized issuer: its identity, the base URL it advertises for
// this request, and the public metadata of its (lazily generated) signing key.
type Issuer struct {
	ID      IssuerID
	BaseURL BaseURL
	Key     SigningKey
}
```

### 4. Closed enumerated sets and exhaustiveness discipline

Go has no exhaustive `enum`, so closure is enforced by a single idiom: a private `all*` slice is the **one source of truth**, `Valid()` is a `switch` over the constants, and a unit test ranges the slice asserting `Valid()` and `Parse` round-trip — so adding a constant without updating `Valid()`/`Parse` fails CI.

**`GrantType` — all six grants:**

```go
// GrantType is the closed set of OAuth2 grants the token endpoint dispatches on.
type GrantType string

const (
	GrantAuthorizationCode GrantType = "authorization_code"
	GrantClientCredentials GrantType = "client_credentials"
	GrantPassword          GrantType = "password"
	GrantRefreshToken      GrantType = "refresh_token"
	GrantJWTBearer         GrantType = "urn:ietf:params:oauth:grant-type:jwt-bearer"
	GrantTokenExchange     GrantType = "urn:ietf:params:oauth:grant-type:token-exchange"
)

// allGrantTypes is the authoritative membership list; Valid and the exhaustiveness
// test both derive from it.
var allGrantTypes = []GrantType{
	GrantAuthorizationCode, GrantClientCredentials, GrantPassword,
	GrantRefreshToken, GrantJWTBearer, GrantTokenExchange,
}

func (g GrantType) Valid() bool {
	switch g {
	case GrantAuthorizationCode, GrantClientCredentials, GrantPassword,
		GrantRefreshToken, GrantJWTBearer, GrantTokenExchange:
		return true
	default:
		return false
	}
}

// IssuesRefreshToken reports whether this grant returns a refresh_token
// (authorization_code and refresh_token only — the per-grant token matrix).
func (g GrantType) IssuesRefreshToken() bool {
	return g == GrantAuthorizationCode || g == GrantRefreshToken
}

// IssuesIDToken reports whether this grant mints an id_token (auth_code,
// refresh, password). client_credentials / jwt-bearer / token-exchange do not.
func (g GrantType) IssuesIDToken() bool {
	switch g {
	case GrantAuthorizationCode, GrantRefreshToken, GrantPassword:
		return true
	default:
		return false
	}
}
```

The per-grant token matrix from the catalog (id_token / refresh_token / issued_token_type / scope echo) is encoded as `GrantType` predicate methods (`IssuesIDToken`, `IssuesRefreshToken`, `EchoesScope`, `IssuedTokenType`) rather than re-discovered with `if grant == ...` scattered across the token service — the matrix lives in one place, next to the type. `IssuedTokenType()` returns the closed `IssuedTokenType` (the token-exchange URN) only for `GrantTokenExchange`.

**`ResponseType` — implemented vs advertised-only.** Discovery advertises `[code, none, id_token, token]`, but only `code` is implemented (hybrid/implicit → `invalid_grant`). The type carries that distinction so the authorize handler asks `rt.Implemented()` instead of hardcoding a string compare:

```go
type ResponseType string

const (
	ResponseTypeCode    ResponseType = "code"
	ResponseTypeNone    ResponseType = "none"
	ResponseTypeIDToken ResponseType = "id_token"
	ResponseTypeToken   ResponseType = "token"
)

// Implemented reports whether the mock actually issues this response type.
// Only the authorization-code flow is implemented; the rest are advertised in
// discovery for client-library compatibility but rejected at /authorize.
func (r ResponseType) Implemented() bool { return r == ResponseTypeCode }
```

**`ResponseMode`** (`query`, `fragment`, `form_post`, default `query`) is a closed enum of the same shape.

**`TokenType` / `IssuedTokenType` / `JOSEType` — one wire concept apiece (the overload is split).** Upstream conflates three disjoint values; we keep them as three distinct types so the closed ones stay closed and the open one stays open:

```go
// TokenType is the response `token_type` (closed: Bearer).
type TokenType string

const TokenTypeBearer TokenType = "Bearer"

// IssuedTokenType is the token-exchange `issued_token_type` (closed: the RFC 8693
// access_token URN).
type IssuedTokenType string

const IssuedTokenAccessToken IssuedTokenType = "urn:ietf:params:oauth:token-type:access_token"
```

The JWS `typ` header is **not** a closed set — see `JOSEType` in §7, which must accept `at+jwt` and any custom callback value, so it has no `Valid()` and `ParseJOSEType` never rejects.

**`SigningAlgorithm` — supported and explicitly-rejected sets.** This is where parse-don't-validate earns its keep: `ES256K`, `ES512`, `EdDSA`, all HMAC, and `none` are not merely "unknown" — they are *known-and-refused*, with the exact `"Unsupported algorithm: EdDSA"` text the upstream tests assert. Algorithm is a **config-time** value (configured per issuer/callback, never read off a token request), so this parser returns a wrapped sentinel. The supported slice is the single source of truth the discovery document and the signer must agree on (contract §6 constant-sync test).

```go
type SigningAlgorithm string

const (
	RS256 SigningAlgorithm = "RS256"
	RS384 SigningAlgorithm = "RS384"
	RS512 SigningAlgorithm = "RS512"
	PS256 SigningAlgorithm = "PS256"
	PS384 SigningAlgorithm = "PS384"
	PS512 SigningAlgorithm = "PS512"
	ES256 SigningAlgorithm = "ES256"
	ES384 SigningAlgorithm = "ES384"

	// DefaultSigningAlgorithm is used when no algorithm is configured.
	DefaultSigningAlgorithm = RS256
)

// supportedSigningAlgorithms is the single source of truth for what the signer
// can produce AND what discovery may advertise (the §6 constant-sync test pins
// these equal). EC family is listed first to match upstream discovery ordering.
var supportedSigningAlgorithms = []SigningAlgorithm{
	ES256, ES384,
	RS256, RS384, RS512, PS256, PS384, PS512,
}

// SupportedSigningAlgorithms returns the advertised/producible algorithm set in
// discovery order. Returns a copy so callers cannot mutate the source of truth.
// This is the single accessor discovery feeds into
// id_token_signing_alg_values_supported (no SupportedAlgorithms() alias exists).
func SupportedSigningAlgorithms() []SigningAlgorithm {
	return slices.Clone(supportedSigningAlgorithms)
}

// ParseSigningAlgorithm accepts a supported algorithm and rejects every other
// value with a typed sentinel. Known-but-refused algorithms (EdDSA, ES256K,
// ES512, HMAC, none) are reported with upstream's asserted wording.
func ParseSigningAlgorithm(s string) (SigningAlgorithm, error) {
	a := SigningAlgorithm(s)
	if slices.Contains(supportedSigningAlgorithms, a) {
		return a, nil
	}
	return "", fmt.Errorf("%w: %s", ErrUnsupportedAlgorithm, s) // -> "Unsupported algorithm: EdDSA"
}
```

`CodeChallengeMethod` (`plain`, `S256`) and `ClientAuth` (`none`, `client_secret_basic`, `client_secret_post`, `private_key_jwt`) are the remaining closed sets, same shape.

The exhaustiveness test that backs all of these — note it asserts the **typed-error** contract for `ParseGrantType` (the request-path parser), not a bare sentinel:

```go
func TestGrantTypeExhaustive(t *testing.T) {
	require.Len(t, allGrantTypes, 6) // tripwire: new grant must update this
	for _, g := range allGrantTypes {
		assert.True(t, g.Valid(), "%s missing from Valid()", g)
		got, err := ParseGrantType(string(g))
		require.NoError(t, err)
		assert.Equal(t, g, got) // round-trip
	}

	var pe *ProtocolError
	_, err := ParseGrantType("nonsense")
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, CodeInvalidGrant, pe.Code)          // unknown -> invalid_grant
	assert.ErrorIs(t, err, ErrUnsupportedGrantType)      // wrapped sentinel still matches

	_, err = ParseGrantType("")
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, CodeInvalidRequest, pe.Code)         // blank -> invalid_request
}
```

### 5. Subjects, scopes, clients, and form params

`Subject` and `ClientID` are distinct `string` types — the catalog's subject-resolution rules (cc → `client_id`, password → `username`, login → `username`) become explicit conversions, never accidental assignments. `Scopes` is an **ordered, deduped** set (insertion order preserved for echo; the catalog's audience step 3 strips OIDC scopes):

```go
// oidcScopes are excluded when deriving an access_token audience from scopes
// (DefaultOAuth2TokenCallback audience step 3).
var oidcScopes = map[Scope]struct{}{
	"openid": {}, "profile": {}, "email": {}, "address": {}, "phone": {}, "offline_access": {},
}

// Scopes is an ordered, de-duplicated scope set. Order is preserved so the
// echoed `scope` response reproduces the request order.
type Scopes []Scope

// ParseScopes splits a space-delimited scope string, dropping blanks and
// duplicates while preserving first-seen order.
func ParseScopes(raw string) Scopes {
	var out Scopes
	seen := make(map[Scope]struct{})
	for _, f := range strings.Fields(raw) {
		s := Scope(f)
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

// NonOIDC returns the scopes with the standard OIDC scopes removed — the
// fallback audience source when no audience is otherwise configured.
func (s Scopes) NonOIDC() Scopes {
	out := make(Scopes, 0, len(s))
	for _, sc := range s {
		if _, ok := oidcScopes[sc]; !ok {
			out = append(out, sc)
		}
	}
	return out
}

func (s Scopes) String() string { return strings.Join(scopeStrings(s), " ") }
```

`Client` encodes the catalog's hard rule that the client is **ephemeral and never authenticated** (N3, P4): there is no secret field, because no secret is ever validated. The only invariant is the effective-`client_id` rule (`invalid_client "client_id cannot be null"`):

```go
// Client is the request's client identity. It is ephemeral — never stored,
// never credential-checked. ClientAuth records only *how* the client presented
// itself (for introspection/templating), not whether it was authorized.
type Client struct {
	ID   ClientID
	Auth ClientAuth
}

// RequireClientID returns the effective client id or invalid_client when none
// was presented, mirroring NimbusExtensions' "client_id cannot be null".
func (c Client) RequireClientID() (ClientID, error) {
	if c.ID == "" {
		return "", InvalidClient("client_id cannot be null")
	}
	return c.ID, nil
}
```

`FormParams` is the **domain-owned** multi-valued form carrier — the typed replacement for `url.Values` that the request-mapping callback templates `${…}` against. It lives in the core (so `RequestMappingCallback` can consume it without an inward import); the `httpapi/form.go` multi-valued parser converts `url.Values → FormParams` at the edge, so `net/url` never crosses inward:

```go
// FormParams is a typed, multi-valued form accessor — the core's replacement for
// url.Values, which the dependency rule forbids inside internal/oidc. The httpapi
// edge populates it; the domain (RequestMappingCallback) only reads it.
type FormParams map[string][]string

func (f FormParams) Get(key string) string { /* first value or "" */ }
func (f FormParams) All(key string) []string { /* all values */ }
```

### 6. `ClaimSet`: the registered-fields vs extensible tradeoff

This is the single most consequential design choice in the domain, because the catalog's claim semantics are intricate (typed defaults, conditional `nonce`/`azp`, always-`tid`, custom merge order) and upstream models them as a bare `Map<String, Any>`. Three options:

| Option | Pros | Cons |
|---|---|---|
| **All claims as struct fields** | Maximum type safety; impossible to typo `sub`; self-documenting | Rigid: custom/user claims (the product's *entire point* per C3) have nowhere to go without an escape hatch anyway |
| **Bare `map[string]any`** (upstream) | Trivially flexible | Stringly-typed, no invariants, unordered, exactly what contract §2/§7 forbid in the domain |
| **Hybrid: typed registered fields + ordered custom container** ← chosen | Registered claims fully typed and invariant-bearing; custom claims flexible but behind a typed, *ordered* accessor — never an exposed `map[string]any` | One extra container type to maintain |

The hybrid is mandated by the glossary ("typed registered fields plus an *ordered* custom-claim accessor — **not** a bare `map[string]any` exposed to the domain"). Registered claims that carry invariants or participate in token logic (`sub`, `aud`, `iss`, `iat`, `nbf`, `exp`, `jti`, `nonce`, `tid`, `azp`, `scope`) are typed fields; everything a user scripts (`acr`, `auth_time`, roles, tenant data) lives in an ordered `CustomClaims`. Ordering matters twice: the catalog cares about deterministic emission, and the custom-claim merge is order-sensitive (`putIfAbsent`: mapping wins, login claims add-only).

```go
// ClaimName is the closed set of registered claims the domain models as typed
// fields. Custom claims use plain string keys via CustomClaims and are *not*
// members of this set.
type ClaimName string

const (
	ClaimSub   ClaimName = "sub"
	ClaimAud   ClaimName = "aud"
	ClaimIss   ClaimName = "iss"
	ClaimIat   ClaimName = "iat"
	ClaimNbf   ClaimName = "nbf"
	ClaimExp   ClaimName = "exp"
	ClaimJti   ClaimName = "jti"
	ClaimNonce ClaimName = "nonce"
	ClaimTid   ClaimName = "tid"
	ClaimAzp   ClaimName = "azp"
	ClaimScope ClaimName = "scope"
)

// Audience is the named audience type used everywhere (ClaimSet, the callback,
// MintSpec): an ordered list of opaque audience strings. id_token audience is
// built as Audience{string(req.ClientID)} (an explicit ClientID->string
// conversion at the boundary); access_token audience is the 4-step chain, whose
// values are arbitrary scope-derived strings, not client ids.
type Audience []string

// Nonce is the OIDC nonce. Its *presence* is semantic, so it is carried as
// *Nonce everywhere (nil == "no nonce"); the value is never a bare string.
type Nonce string

// ClaimSet is the typed claim container for one token. Registered claims are
// strongly-typed fields with invariants; arbitrary scripted claims live in
// Custom, an ordered accessor (never an exposed map). The optional pointer
// fields encode the catalog's "added only when non-null" rules precisely:
// Nonce and Azp are pointers because their *presence* is semantic.
//
// Ownership: a built ClaimSet is treated as immutable. Custom is owned by
// exactly one ClaimSet; mutation happens only during assembly (via the builder
// / Clone), so an order-sensitive putIfAbsent merge can never leak across a
// value copy of ClaimSet.
type ClaimSet struct {
	Subject   Subject
	Audience  Audience  // id_token => [client_id]; access_token => 4-step chain
	Issuer    string    // full issuer URL string (BaseURL.IssuerURL — already a string)
	IssuedAt  Instant
	NotBefore Instant
	Expiry    Instant
	JWTID     string    // jti — random UUID
	Nonce     *Nonce    // present only when the cached request carried a nonce
	Azp       *ClientID // present only for authorization_code (non-overridable)
	Tenant    *string   // tid — seeded to issuerId, but user-overridable (see below)
	Scope     Scopes
	Custom    CustomClaims
}
```

`CustomClaims` is the ordered, type-guarded accessor. Custom-claim *leaf* values genuinely originate as arbitrary JSON (config `claims`, login `claims`, copied assertion claims), so a leaf is a `ClaimValue` — a **defined** type (not a `= any` alias, which would carry no nominal identity) over the JSON value space — rather than a fully named type per leaf. The discipline the contract requires is that the **container** is typed and ordered and the domain never sees a naked `map[string]any`, not that every user-supplied JSON scalar gets its own Go type:

```go
// ClaimValue is one custom-claim leaf. It is a DEFINED type (note: no `=`), so
// map[string]ClaimValue is nominally distinct from map[string]any and the
// compiler can keep this one dynamic boundary named and contained. It ranges
// over the JSON value space (string | float64 | bool | []ClaimValue | nested
// CustomClaims).
type ClaimValue any

// ClaimEntry is one ordered (name, value) pair — the ordered-emission unit that
// replaces any "claims as map[string]any" return. Marshaling/inspection ranges
// Entries() in order; a Go map is never used to carry ordered claims out.
type ClaimEntry struct {
	Name  string
	Value ClaimValue
}

// CustomClaims is an insertion-ordered claim map. Ordering backs deterministic
// emission and the order-sensitive login/mapping merge (putIfAbsent semantics).
// It is the *only* container the domain exposes for dynamic claims; callers
// never touch a raw map.
type CustomClaims struct {
	order  []string
	values map[string]ClaimValue
}

// Set inserts or replaces a claim, preserving first-insertion order. Mutators
// operate on the builder-/ClaimSet-owned instance; obtain a Clone before
// mutating a CustomClaims read out of a built ClaimSet.
func (c *CustomClaims) Set(name string, v ClaimValue) { /* append to order if new */ }

// SetIfAbsent adds a claim only when absent — the login-claims merge rule
// (login claims ADD but never OVERWRITE a value the mapping already set).
func (c *CustomClaims) SetIfAbsent(name string, v ClaimValue) bool { /* ... */ }

// Entries returns the claims in insertion order — the ordered-emission accessor.
func (c CustomClaims) Entries() []ClaimEntry { /* ... */ }

// Clone returns a deep copy with its own order slice and values map, so a copy
// can be mutated without aliasing the source (the ownership rule above).
func (c CustomClaims) Clone() CustomClaims { /* ... */ }
```

The catalog's default-claim rules map onto `ClaimSet` construction without a single `map[string]any` literal: `iat`/`nbf`/`exp`/`jti` are always set from the `Clock`; `Nonce` is set iff the cached request carried one; `Azp` is set only on the `authorization_code` path and **after** custom claims (so it is non-overridable, matching upstream's post-`putAll` insertion).

`Tenant` (`tid`) is the one registered claim that is deliberately **overridable**: `DefaultOAuth2TokenCallback` seeds `Tenant = &issuerIdString` *before* the custom-claim merge, so a user-supplied `tid` in `Custom` takes precedence at emission (contrast `Azp`, set after the merge). It is modeled as `*string`, not `*IssuerID`, precisely because an overridden `tid` (e.g. `contoso/eu`) is arbitrary text that `IssuerID`'s constructor would reject — `IssuerID` stays reserved for routing and `kid`. `auth_time`/`acr` are absent from the registered set, so they can only arrive as `Custom` claims — exactly upstream's "custom only" behavior, enforced by the type.

### 7. Tokens & wire artifacts

The unsigned/signed split keeps the domain free of JOSE. `Token` is what a service hands the `Signer` port; `SignedToken` is the opaque compact-serialized result that comes back. **The domain never serializes or signs — it only *describes*.**

```go
// JOSEType is the JWS "typ" header. It is intentionally OPEN — no closed Valid():
// the default is "JWT", but a TokenCallback may set any value (e.g. RFC 9068
// "at+jwt"), so ParseJOSEType never rejects; it only supplies the default for
// empty input. This is distinct from TokenType (response token_type) and
// IssuedTokenType (token-exchange), which are closed.
type JOSEType string

const DefaultJOSEType JOSEType = "JWT"

func ParseJOSEType(s string) JOSEType {
	if s == "" {
		return DefaultJOSEType
	}
	return JOSEType(s)
}

// JWTHeader is the JWS header for a minted token: algorithm, key id (= issuer),
// and JOSE type (default "JWT"; e.g. "at+jwt" via a callback typeHeader).
type JWTHeader struct {
	Algorithm SigningAlgorithm
	KeyID     KeyID
	Type      JOSEType
}

// Token is an unsigned token model — header plus claims plus the key it must be
// signed with. It crosses to the Signer port; the domain performs no crypto.
type Token struct {
	Header JWTHeader
	Claims ClaimSet
}

// NewToken assembles an unsigned Token, defaulting the JOSE typ to "JWT". The
// kid is derived from the issuer (kid == IssuerID), the algorithm is explicit,
// and the typ is the open JOSEType (so at+jwt is representable).
func NewToken(issuer IssuerID, alg SigningAlgorithm, typ JOSEType, claims ClaimSet) (Token, error) {
	if typ == "" {
		typ = DefaultJOSEType
	}
	return Token{
		Header: JWTHeader{Algorithm: alg, KeyID: issuer.KeyID(), Type: typ},
		Claims: claims,
	}, nil
}

// SignedToken is the compact-serialized signed JWT: the wire artifact. It is
// opaque to the domain — produced by the Signer adapter, echoed in responses,
// and handed back to the Verifier adapter for /userinfo and /introspect.
type SignedToken string
```

### 8. Authorization codes & refresh tokens

`AuthorizationCode` is a single-use handle; the interesting state is the cached `CodeRecord` snapshot taken at `/authorize` time and consumed (and removed) at `/token` time. Modeling the snapshot as a typed value makes the catalog's subtle rules — `nonce` comes from the *cached* request not the token request, PKCE is enforced only when a verifier is presented, the code is removed before the PKCE check — checkable in one place.

```go
// CodeRecord is the request snapshot cached against an AuthorizationCode at
// /authorize and consumed once at /token. It is the authority for nonce, PKCE,
// and any interactive-login claims — none of which the token request can supply.
type CodeRecord struct {
	Issuer       IssuerID
	Client       Client
	RedirectURI  string // captured, intentionally NOT validated (parity)
	Scope        Scopes
	Nonce        *Nonce
	Challenge    *PKCEChallenge // nil when the request carried no PKCE
	ResponseMode ResponseMode
	Login        *LoginSubmission // present for interactive-login codes
	IssuedAt     Instant
}

// PKCEChallenge pairs a stored challenge with its method. Verification is a pure,
// keyless transform (SHA-256 + base64url) enforced only when a verifier is
// actually presented (a bare challenge need not be redeemed); a failed check is
// the caller's signal to invalidate the code. (Whether the transform lives in
// the core or behind a port is settled in the Architecture/Ports section's
// depguard carve-out; the type itself is transport-free either way.)
type PKCEChallenge struct {
	Challenge string
	Method    CodeChallengeMethod
}
```

`RefreshToken` captures upstream's dual format precisely with types rather than a stringly-typed flag — but the **domain only chooses which form, it never serializes one**. The wire string is either a bare UUID or an unsigned `alg=none` PlainJWT `{jti, nonce}` (the Keycloak-JS accommodation); the compact serialization of the PlainJWT form is JOSE wire-formatting and therefore belongs to the signing/refresh-store adapter, not `refresh.go`. The domain picks the form via the closed `RefreshFormat` enum on `RefreshRecord`; the adapter materializes the opaque `RefreshToken` string. This keeps §7's invariant ("the domain never serializes") intact.

```go
// RefreshToken is the opaque wire refresh-token string. The domain treats it as
// a value it stores and compares; it is *produced by the refresh-store adapter*
// (a bare UUID, or an unsigned alg=none PlainJWT when a nonce is present). The
// domain never hand-formats the JWT — it only selects the RefreshFormat.
type RefreshToken string

// RefreshFormat is the closed choice the domain makes; the adapter renders it.
type RefreshFormat int

const (
	RefreshBareUUID RefreshFormat = iota // grant had no nonce, or rotation
	RefreshPlainJWT                      // alg=none {jti, nonce} — nonce present
)

// RefreshRecord binds a refresh token to the issuer that minted it and the
// callback that reproduces its claims. Issuer is the field the strict (4.0.0+)
// cross-issuer check reads. Format records which wire form the adapter must
// render; rotation (rotateRefreshToken) re-issues as RefreshBareUUID and drops
// the nonce.
type RefreshRecord struct {
	Issuer   IssuerID
	Subject  Subject
	Nonce    *Nonce
	Format   RefreshFormat
	Callback TokenCallback // claim/subject/aud policy to replay on refresh
}
```

### 9. Signing keys & key sets

The domain holds **public metadata only** — never private key material (that lives behind the `Signer`/`KeyStore` ports in `signing/`). `SigningKey` is what `/jwks` and discovery describe; `kid == IssuerID` is baked in by construction. The public parameters are a **typed, sealed union** (RSA vs EC) rather than a stringly-typed `map[string]string` the core never reads:

```go
// KeyType is the closed JWK key family.
type KeyType string

const (
	KeyTypeRSA KeyType = "RSA"
	KeyTypeEC  KeyType = "EC"
)

// PublicParams is the sealed set of public-JWK parameter shapes (closed via the
// unexported marker method). It replaces the prior opaque map[string]string so
// the domain carries typed key parameters, not stringly-typed open keys.
type PublicParams interface{ isPublicParams() }

// RSAPublicParams carries the public RSA parameters (n, e).
type RSAPublicParams struct{ N, E string }

func (RSAPublicParams) isPublicParams() {}

// ECPublicParams carries the public EC parameters (crv, x, y).
type ECPublicParams struct{ Crv, X, Y string }

func (ECPublicParams) isPublicParams() {}

// SigningKey is the *public* metadata of an issuer's signing key. No private
// material ever enters the domain; the signing adapter owns that. kid equals the
// issuer id by construction.
type SigningKey struct {
	KeyID     KeyID
	Algorithm SigningAlgorithm
	Public    JWK // public parameters only (use=sig)
}

// JWK is a single public JSON Web Key (public parameters only). The signing
// adapter materializes it; the jwks adapter renders it. The domain holds typed
// parameters via the sealed PublicParams union, never an open string map.
type JWK struct {
	KeyID     KeyID
	Algorithm SigningAlgorithm
	KeyType   KeyType
	Use       string       // always "sig"
	Params    PublicParams // RSAPublicParams | ECPublicParams
}

// JWKS (alias KeySet) is the public key set served at /{issuer}/jwks.
type JWKS struct {
	Keys []JWK
}
type KeySet = JWKS
```

The contract §6 constant-sync invariant — *algorithms advertised in discovery == algorithms the signer can actually produce* — is anchored here: discovery's `id_token_signing_alg_values_supported` is built from `SupportedSigningAlgorithms()`, and a unit test asserts the signer adapter rejects exactly the complement. The domain owns the set; the adapter must conform to it, not the reverse.

### 10. Login submission

```go
// LoginSubmission is the parsed interactive-login POST: a required username
// (the subject) plus optional, free-form claims supplied as JSON. The adapter
// parses the form and the JSON before this value is constructed; invalid claims
// JSON is dropped at the edge (warned, not fatal) so the domain only ever sees
// a well-formed, possibly-empty claim set.
type LoginSubmission struct {
	Username Subject
	Claims   CustomClaims // empty when omitted; merged add-only at token time
}

// NewLoginSubmission validates the required username. MissingParameter returns a
// *ProtocolError (invalid_request, 400), so an empty username on the login POST
// is a correctly-typed protocol error, not a bare sentinel.
func NewLoginSubmission(username string, claims CustomClaims) (LoginSubmission, error) {
	if strings.TrimSpace(username) == "" {
		return LoginSubmission{}, MissingParameter("username")
	}
	return LoginSubmission{Username: Subject(username), Claims: claims}, nil
}
```

### 11. Typed domain errors

`ErrorCode` is a closed set; `ProtocolError` is the single typed error every protocol service raises, carrying the client-visible description and the HTTP status. **One representation everywhere:** services and constructors always return `*ProtocolError` (pointer — the `Error()`/`Unwrap()` receivers are pointer receivers), and codes are the `Code*` constants (never the `Err*` parse sentinels). Constructors mirror upstream's throwers one-for-one and bake in the **exact** descriptions the upstream tests assert — including the *corrected* texts mandated by parity-in-intent.

```go
// ErrorCode is the closed set of OAuth2/OIDC error codes the domain emits.
type ErrorCode string

const (
	CodeInvalidRequest       ErrorCode = "invalid_request"
	CodeInvalidGrant         ErrorCode = "invalid_grant"
	CodeInvalidClient        ErrorCode = "invalid_client"
	CodeInvalidToken         ErrorCode = "invalid_token"
	CodeUnsupportedTokenType ErrorCode = "unsupported_token_type"
	CodeUnsupportedGrantType ErrorCode = "unsupported_grant_type"
	CodeServerError          ErrorCode = "server_error"
	CodeNotFound             ErrorCode = "not_found"
)

// ProtocolError is the typed OAuth2 protocol error. Description is the
// client-visible text (correct-case — upstream's full-body lowercasing is a
// defect we do NOT replicate). HTTPStatus is explicit because the same code maps
// to different statuses upstream (e.g. invalid_client is 400 at /introspect but
// 401 elsewhere); the constructor that knows the context sets it. cause is an
// optional wrapped sentinel so errors.Is (sentinel) and errors.As(*ProtocolError)
// both succeed on the same value.
type ProtocolError struct {
	Code        ErrorCode
	Description string
	HTTPStatus  int
	cause       error
}

func (e *ProtocolError) Error() string { return string(e.Code) + ": " + e.Description }
func (e *ProtocolError) Unwrap() error { return e.cause }

// NewProtocolError is the generic constructor; the named constructors below are
// the preferred, intent-revealing entry points.
func NewProtocolError(code ErrorCode, desc string, status int) *ProtocolError {
	return &ProtocolError{Code: code, Description: desc, HTTPStatus: status}
}

// Sentinels for errors.Is on parse failures. ErrInvalidIssuer/ErrReservedIssuer
// are wrapped as the `cause` of the *ProtocolError ParseIssuerID returns, so a
// caller can both errors.As(&ProtocolError) and errors.Is(ErrReservedIssuer).
var (
	ErrInvalidIssuer        = errors.New("invalid issuer id")
	ErrReservedIssuer       = errors.New("issuer collides with reserved control prefix")
	ErrInvalidBaseURL       = errors.New("invalid base url")
	ErrInvalidScheme        = errors.New("invalid url scheme")
	ErrUnsupportedGrantType = errors.New("unsupported grant_type")
	ErrUnsupportedAlgorithm = errors.New("Unsupported algorithm") // exact upstream wording
)

// --- Constructors mirroring upstream throwers, with asserted descriptions ---

// MissingParameter -> invalid_request "missing required parameter <name>" (400).
func MissingParameter(name string) *ProtocolError {
	return &ProtocolError{Code: CodeInvalidRequest, Description: "missing required parameter " + name, HTTPStatus: 400}
}

// MalformedRequest -> invalid_request <desc> (400). General-purpose 400 for
// structurally-bad protocol input that is not a "missing parameter".
func MalformedRequest(desc string) *ProtocolError {
	return &ProtocolError{Code: CodeInvalidRequest, Description: desc, HTTPStatus: 400}
}

// InvalidGrant -> invalid_grant <desc> (400). The base for every invalid_grant
// case; the specific throwers below delegate to it so their text lives once.
func InvalidGrant(desc string) *ProtocolError {
	return &ProtocolError{Code: CodeInvalidGrant, Description: desc, HTTPStatus: 400}
}

// UnsupportedGrant -> invalid_grant "grant_type <x> not supported." Upstream
// reports an unknown grant as invalid_grant (NOT unsupported_grant_type); we
// preserve that exact code+text, and wrap ErrUnsupportedGrantType so a sentinel
// check still works.
func UnsupportedGrant(g string) *ProtocolError {
	e := InvalidGrant("grant_type " + g + " not supported.")
	e.cause = ErrUnsupportedGrantType
	return e
}

// UnknownAuthorizationCode -> invalid_grant "unknown or already-used authorization code".
func UnknownAuthorizationCode() *ProtocolError {
	return InvalidGrant("unknown or already-used authorization code")
}

// PKCEFailed -> invalid_grant with upstream's exact invalid_pkce description.
func PKCEFailed() *ProtocolError {
	return InvalidGrant("invalid_pkce: code_verifier does not compute to code_challenge from request")
}

// RefreshCrossIssuer -> invalid_grant "refresh_token was issued by a different
// issuer". CORRECTED text: the client-visible description, not the internal
// "refresh_token issuer mismatch" message (catalog correction).
func RefreshCrossIssuer() *ProtocolError {
	return InvalidGrant("refresh_token was issued by a different issuer")
}

// UnknownRefreshToken -> invalid_grant "unknown refresh_token".
func UnknownRefreshToken() *ProtocolError {
	return InvalidGrant("unknown refresh_token")
}

// InvalidClient -> invalid_client at 401 (the default elsewhere).
func InvalidClient(desc string) *ProtocolError {
	return &ProtocolError{Code: CodeInvalidClient, Description: desc, HTTPStatus: 401}
}

// InvalidClientStatus -> invalid_client at an explicit status. The /introspect
// call site uses InvalidClientStatus(400, "...") for upstream's presence-only
// auth, where a missing/empty Authorization header yields 400, not 401.
func InvalidClientStatus(status int, desc string) *ProtocolError {
	return &ProtocolError{Code: CodeInvalidClient, Description: desc, HTTPStatus: status}
}

// InvalidToken -> invalid_token at 401 (userinfo/introspect verify failure).
// This is the single InvalidToken shape — it takes a description string and
// returns *ProtocolError (no NewInvalidToken(err) variant).
func InvalidToken(desc string) *ProtocolError {
	return &ProtocolError{Code: CodeInvalidToken, Description: desc, HTTPStatus: 401}
}

// UnsupportedTokenType -> "unsupported token type: <hint>" (revoke) at 400.
func UnsupportedTokenType(hint string) *ProtocolError {
	return &ProtocolError{Code: CodeUnsupportedTokenType, Description: "unsupported token type: " + hint, HTTPStatus: 400}
}

// NotFound -> not_found "Resource not found" at 404.
func NotFound() *ProtocolError {
	return &ProtocolError{Code: CodeNotFound, Description: "Resource not found", HTTPStatus: 404}
}
```

The `httpapi/oautherr.go` adapter recovers these via `errors.As(err, &protoErr)` and renders the per-operation `OAuth2Error` output at `protoErr.HTTPStatus` (contract §5(4)/§6); any non-`ProtocolError` on a protocol route is coerced to `server_error` (500). The control plane recovers the **same** `*ProtocolError` via `errors.As` and maps `HTTPStatus` into its RFC 9457 problem document (and may additionally `errors.Is(err, ErrReservedIssuer)` for clarity). The domain never knows it is producing HTTP — it raises typed values; the adapters translate.

### 12. Clock & Instant

Time is a port so `systemTime` can freeze issuance deterministically (C3, P3). `Instant` is a thin wrapper that keeps `time.Time` from sprawling through the domain and gives token claims a single time type. There is **one** `Instant` definition, with the full accessor set the config and service layers need:

```go
// Instant is a point in time used for iat/nbf/exp. It wraps (UTC-normalized)
// time.Time so the domain has one time type and the frozen-clock seam is
// explicit.
type Instant struct{ t time.Time }

// NewInstant wraps a time.Time, normalizing to UTC.
func NewInstant(t time.Time) Instant { return Instant{t.UTC()} }

// ParseInstant parses an RFC 3339 timestamp (the config `systemTime` form). It
// is a config-time parser, so it returns a wrapped sentinel on failure.
func ParseInstant(s string) (Instant, error) {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return Instant{}, fmt.Errorf("invalid systemTime %q: %w", s, err)
	}
	return NewInstant(t), nil
}

func (i Instant) Time() time.Time             { return i.t }
func (i Instant) Unix() int64                 { return i.t.Unix() }
func (i Instant) Add(d time.Duration) Instant { return Instant{i.t.Add(d)} }

// Clock is the outbound time port. Now returns the configured instant — real
// wall-clock by default, or a frozen/advanced systemTime when one is set.
// iat/nbf/exp AND the reported expires_in are all derived from the same Clock,
// so they never diverge under a frozen clock (a deliberate correction of
// upstream's expires_in-from-real-now quirk; P3 determinism).
type Clock interface {
	Now() Instant
}
```

`Clock` is **only** the port here. Its concrete implementations are adapters, not domain values: the running server wires the mutable `memory.Clock` (which satisfies both `oidc.Clock` and the control plane's clock-control facet, so `/_mock/clock/advance` can push tokens past `exp`); test-only fixed/system clocks live alongside it. Keeping the domain to the port avoids baking an *immutable* clock value into the core that the control plane could not advance.

### 13. Deliberate divergences from upstream quirks (cleaner types)

Per parity-in-intent (PRD D-2/P7/N8), the type system *refuses to encode* several upstream defects:

| Upstream quirk | Why it is a defect | Domain treatment |
|---|---|---|
| Entire error JSON `.lowercase()`-d (mangles `error_description`) | Corrupts client-visible text | `ProtocolError.Description` is correct-case; no lowercasing exists in the type |
| Cross-issuer refresh surfaces internal `"...issuer mismatch"` | Leaks internal message | `RefreshCrossIssuer()` carries the corrected client text |
| `expires_in` from real `Instant.now()` while `exp` from frozen clock | Breaks determinism | Both derive from the single `Clock` (the httpapi token responder does NOT recompute from wall-clock) |
| `kid` discarded from bundled file, replaced by issuerId, but typed as opaque string | Loses the invariant | `KeyID` is produced *only* by `IssuerID.KeyID()`; the equality is a type rule |
| Claims modeled as `Map<String, Any>` | Stringly-typed, unordered, no invariants | `ClaimSet` (typed registered fields) + ordered `CustomClaims` (defined `ClaimValue` leaves, ordered `Entries()`); no exposed `map[string]any` |
| `ES256K`/`ES512`/`EdDSA` "unknown" | Silently mis-handled | Parsed-and-rejected with the asserted `"Unsupported algorithm"` wording |
| `token_type`/`issued_token_type`/JWS `typ` conflated | Closed `typ` rejects valid `at+jwt` | Split into closed `TokenType`/`IssuedTokenType` and **open** `JOSEType` |
| Suffix routing makes `issuerId` any leading path | Fragile, un-spec-able (single-segment limit recorded as a named parity gap, §3) | `IssuerID` is a validated path segment that rejects `/` and the reserved prefix |
| `302→400` status coercion; `form_post` NPE → 500 | Status confusion / crash | Not representable: `ProtocolError.HTTPStatus` is set explicitly per constructor; no 302 ever enters the error path |

Quirks that are **transport/parser** concerns (the dual form parsers' silent-`=`-truncation, `Files.probeContentType` MIME, the `anyToken` ~1000× expiry inflation, end-session reading query-only) are out of this section's scope and handled at the `httpapi` edge; the domain types are simply incapable of expressing them. The refresh-token PlainJWT *serialization* is likewise pushed to the signing/refresh-store adapter (§8), so the domain's "describe, never serialize" invariant holds.

---

Files this section defines (all absolute): `/Users/josh/code/meigma/mock-oidc/internal/oidc/{issuer.go, client.go, identity.go, grant.go, algorithm.go, claims.go, token.go, code.go, refresh.go, keys.go, requests.go, errors.go, clock.go}`. The one explicit contract tension — the glossary's "`BaseURL` struct over `*url.URL`" versus §3's net/url ban — is resolved in §3 in favor of the dependency rule (typed scalar components in the core; `net/url` confined to the edge that builds `RequestOrigin`); it is the single point that needs the contract owner's ratification if `*url.URL` was meant literally.

## Application Core & Ports

This section specifies `internal/oidc`'s **application services** (the inbound/driving use cases) and the **outbound/driven ports** they orchestrate. It realizes Contract §2 (`provider.go`, `authorize.go`, `token.go`, `session.go`, `ports.go`, `clock.go`) and §3 (the dependency rule). The governing idea: a service is a thin use-case orchestrator over (a) pure domain values and invariants and (b) a handful of narrow interfaces it *declares for itself*. No service imports `huma`, `net/http`, `crypto/*` signing wiring, or a JOSE library — those live behind the ports below and are supplied at the composition root.

### 1. Map of use cases → services → ports

Every endpoint in the parity catalog resolves to one method on one of four core services, plus one control-plane use case (`Mint`). The table is the contract between this section and the `httpapi`/`controlapi` adapters: adapters parse untyped input into the typed command, call the method, and map the typed result back out.

| Use case (catalog) | Service.method | Outbound ports consumed |
|---|---|---|
| Discovery / AS-metadata doc | `ProviderService.Discovery` | `IssuerRegistry`, `KeyStore` |
| Per-issuer JWKS | `ProviderService.JWKS` | `IssuerRegistry`, `KeyStore` |
| `GET /authorize` (code or login) | `AuthorizeService.Authorize` | `IssuerRegistry`, `KeyStore`, `Clock` |
| `POST /authorize` (login submit) | `AuthorizeService.SubmitLogin` | `CodeStore`, `Clock` |
| `POST /token` (6 grants) | `TokenService.Issue` | `IssuerRegistry`, `KeyStore`, `Signer`, `CodeStore`, `RefreshTokenStore`, `CallbackQueue`, `Clock` |
| `GET /userinfo` | `SessionService.UserInfo` | `IssuerRegistry`, `KeyStore`, `TokenVerifier`, `Clock` |
| `POST /introspect` | `SessionService.Introspect` | `IssuerRegistry`, `KeyStore`, `TokenVerifier`, `Clock` |
| `POST /revoke` | `SessionService.Revoke` | `RefreshTokenStore` |
| `* /endsession` | `SessionService.EndSession` | *(pure — none)* |
| Direct mint for test setup (PRD C6) | `TokenService.Mint` | `IssuerRegistry`, `KeyStore`, `Signer`, `Clock` |

`RequestRecorder` is the one port with **no core-service consumer**: it is written by the `httpapi` recording middleware (`Record`) and drained by `controlapi` through a *separate* consumer-declared `controlapi.RequestLog` port (PRD C6/S4). It is declared in `ports.go` only so the two adapters — which the dependency rule forbids from importing each other — share one neutral domain type; the dependency edge is adapter→port, not service→port. `CallbackQueue` is likewise split: the core consumes only its read side (`DequeueFor`, in `TokenService.resolveCallback`), while the control plane's `Enqueue`/`List`/`Clear` live on `controlapi.ScenarioStore`. One `memory.*` type structurally satisfies each port pair. This split is called out explicitly so the ports' placement is not mistaken for service coupling.

Two further seams the task names — `TemplateRenderer` and a debugger `SessionStore` — are deliberately **not** domain ports; they are declared by the `httpapi` adapter. §8 explains why putting them in the core would violate the dependency rule.

### 2. The freezable clock: `Clock` and `Instant`

PRD C3 ("freeze or set 'now' so token lifetimes are exactly reproducible") and the catalog's `systemTime` make time a first-class, injectable port. Rather than the template's bare `now func() time.Time`, the core uses a typed `Instant` so `iat`/`nbf`/`exp` are never confused with arbitrary `time.Time`, and so verification can be threaded with the *same* clock as issuance (the catalog's `verify()` reads `currentTime()` from the time provider). This is the canonical `Instant` definition for the whole design.

```go
// clock.go

// Instant is a single point in time used for every temporal claim the domain
// mints (iat, nbf, exp) and for token verification. It is a thin value over
// time.Time, normalized to UTC, so seconds-since-epoch math is unambiguous and
// a raw time.Time can never be passed where a domain instant is expected.
type Instant struct{ t time.Time }

// NewInstant wraps t as an Instant (normalized to UTC).
func NewInstant(t time.Time) Instant { return Instant{t: t.UTC()} }

// ParseInstant parses an RFC 3339 timestamp string into an Instant. The config
// edge uses it to turn the JSON systemTime string into a domain instant via a
// smart constructor (parse-don't-validate); time.Parse is a pure, keyless
// stdlib transform, so this stays inside the core's permitted std-lib surface.
func ParseInstant(s string) (Instant, error) {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return Instant{}, fmt.Errorf("parse instant %q: %w", s, err)
	}
	return NewInstant(t), nil
}

// Time returns the underlying time.Time.
func (i Instant) Time() time.Time { return i.t }

// Add returns the instant d later, used to compute exp = now + tokenExpiry.
func (i Instant) Add(d time.Duration) Instant { return Instant{t: i.t.Add(d)} }

// Unix returns seconds since the epoch, the wire form for iat/nbf/exp.
func (i Instant) Unix() int64 { return i.t.Unix() }

// Clock is the time source the domain reads "now" from. It is the seam that
// makes token lifetimes reproducible: production wires the mutable memory.Clock;
// unit tests wire SystemClock / FixedClock. Implementations must be safe for
// concurrent use.
type Clock interface {
	Now() Instant
}

// SystemClock reads the wall clock.
type SystemClock struct{}

// Now returns the current instant.
func (SystemClock) Now() Instant { return NewInstant(time.Now()) }

// FixedClock returns a pinned instant, freezing iat/nbf/exp for deterministic
// tests. It is constructed from the config seed's systemTime.
type FixedClock struct{ at Instant }

// NewFixedClock pins the clock at the given instant.
func NewFixedClock(at Instant) FixedClock { return FixedClock{at: at} }

// Now returns the pinned instant.
func (c FixedClock) Now() Instant { return c.at }
```

`Clock` and its two trivial implementations stay in the domain because they depend only on `time` (permitted std-lib). Each service takes a `Clock` through a functional option (mirroring the template's `WithClock`), defaulting to `SystemClock{}`:

```go
// WithClock overrides the time source (config systemTime / tests).
func WithClock(c Clock) Option { return func(s *serviceDeps) { s.clock = c } }
```

**Production wires a mutable clock adapter.** `SystemClock{}` and `FixedClock` are domain value seams used by unit tests. The *running server* instead wires the mutable `memory.Clock` adapter (seeded from the config seed's `systemTime`), which satisfies `oidc.Clock` for the core **and** the control plane's clock-controller interface for `/_mock/clock/{freeze,advance}` (PRD C6). A `FixedClock` cannot be advanced, so the clock-control capability requires the mutable adapter; the domain still only ever sees the `Clock` port, so `s.clock.Now()` transparently reflects any control-plane freeze/advance — and `TokenVerifier.Verify(..., now)` is threaded the same instant, so an advanced clock pushes previously-valid tokens past `exp`. `oidc.FixedClock`/`SystemClock` never appear on the running-server path.

**Parity-in-intent correction (PRD P7/N8):** upstream computes `expires_in` from real `Instant.now()` while `exp` uses the frozen clock, so the two diverge under a frozen clock (catalog "Parity Gotchas"). The core computes `expires_in` from the *same* `Clock` as `exp`, so a frozen-or-advanced clock yields a consistent pair — which is exactly what the control-plane "advance clock past exp" scenario relies on. This is the kind of defect the clock port exists to fix; the quirk is **not** preserved anywhere downstream.

### 3. Signing-key ports: `KeyStore`, `Signer`, `TokenVerifier`

The task's "SigningKeySource / KeyProvider" is split into three narrow ports per Contract §2 (`signing/` implements all three). The split keeps each consumer depending only on what it needs: `ProviderService` needs public material (`KeyStore`); `TokenService`/`Mint` need to sign (`Signer`); `SessionService` needs to verify (`TokenVerifier`). **No private key material is ever a domain type** — the domain holds only `SigningKey` (public metadata) and references a key by `KeyID` (== `IssuerID`); the adapter maps that id to its private key internally.

```go
// ports.go (signing cluster)

// KeyStore provides per-issuer PUBLIC signing-key metadata and the published JWK
// set. Keys are generated lazily on first reference (computeIfAbsent) with
// kid == issuerId, drawn from an embedded 5-key RSA JWKS deque before any key is
// generated (catalog: stable keys across restarts). The same id always yields
// the same key for the process lifetime; private material never crosses this
// port. Implementations must be concurrency-safe.
type KeyStore interface {
	// SigningKey returns the public metadata for id's signing key, materializing
	// it on first reference.
	SigningKey(ctx context.Context, id IssuerID) (SigningKey, error)
	// PublicKeys returns the JWK set served at the issuer's jwks_uri. It forces
	// materialization of id's key so the set is never empty (catalog: /jwks
	// always returns the requested issuer's key).
	PublicKeys(ctx context.Context, id IssuerID) (JWKS, error)
}

// Signer mints signed JWTs. The unsigned Token (header + claims + KeyID) is a
// pure domain value; the adapter holds the private key and performs the JWS,
// stamping alg/kid/typ from the Token's header. The same adapter also produces
// the unsigned alg=none PlainJWT form of a refresh token (see §7.3), so JOSE
// compact-serialization lives entirely in signing/, never in the domain.
type Signer interface {
	// Sign serializes and signs tok for issuer id, returning the compact JWS.
	Sign(ctx context.Context, id IssuerID, tok Token) (SignedToken, error)
}

// TokenVerifier verifies a signed JWT against an issuer and returns its claims.
// It backs /userinfo and /introspect. The now argument threads the same mutable
// Clock the control plane advances into verification so iat/exp are checked
// against the same time base issuance used. A verification failure is a typed
// error the domain maps to invalid_token (/userinfo) or {active:false}
// (/introspect).
type TokenVerifier interface {
	Verify(ctx context.Context, id IssuerID, token SignedToken, now Instant) (ClaimSet, error)
}
```

**Per-issuer lazy keys at the port level.** The catalog's `KeyProvider.signingKey(issuerId) = computeIfAbsent` is realized entirely inside the `signing` adapter behind `KeyStore.SigningKey`: the domain never asks "does this issuer exist yet?" — it asks for the key and the adapter creates-and-caches it deterministically (`kid = issuerId`, no thumbprint, no rotation; seeded deque consumed one-per-new-issuer). Because materialization is a side effect of `KeyStore` access, zero-config on-demand issuers (PRD P2/C4) need no pre-registration step in any service.

**Constant-sync invariant (Contract §6).** The algorithm set advertised in discovery (`SupportedSigningAlgorithms()`, a domain constant in `algorithm.go`) must equal the set the `Signer` can actually produce. `ProviderService.Discovery` reads the domain constant; a committed unit test cross-checks it against the `signing` adapter's exported capability list, the same drift discipline the template uses for enums. (`Signer` deliberately exposes no `Algorithms()` method — the single advertised source is the domain constant.)

### 4. `IssuerRegistry`: on-demand materialization + per-issuer config

`KeyStore` owns key material; `IssuerRegistry` owns the *set of issuers* and each issuer's **static configured callbacks** (the JSON-config `tokenCallbacks`, i.e. `RequestMappingTokenCallback`s). Splitting them this way (rather than overloading `KeyStore`) gives the config seed a clean home and keeps `TokenService` free of construction-time config wiring: configured callbacks flow in through the registry record. This registry record is the **single source of truth** for configured callbacks — there is no separately injected `CallbackResolver` object; resolution is the in-service composition of §6.

```go
// IssuerRecord is the static, per-issuer state the registry holds: the issuer's
// id and any callbacks seeded from JSON config (empty for zero-config, on-demand
// issuers). It carries no key material and no per-request base URL.
type IssuerRecord struct {
	ID        IssuerID
	Callbacks []TokenCallback // configured RequestMapping callbacks, first-match order
}

// IssuerRegistry records issuers on demand and exposes their static config.
// Any non-reserved IssuerID becomes live on first reference (computeIfAbsent);
// the config seed pre-populates records that carry configured callbacks.
// Implementations must be concurrency-safe.
type IssuerRegistry interface {
	// Materialize records id as a live issuer on first reference and returns its
	// record. Idempotent for the process lifetime.
	Materialize(ctx context.Context, id IssuerID) (IssuerRecord, error)
	// Known returns every materialized issuer id, for control-plane enumeration.
	Known(ctx context.Context) ([]IssuerID, error)
}
```

The per-request `Issuer` value (`IssuerID` + resolved `BaseURL` + `SigningKey` + configured callbacks) is assembled by a small **domain-internal** collaborator — not a port, not a service, just composition over two ports plus the pure `ResolveBaseURL`. (`Issuer` therefore carries a `Callbacks []TokenCallback` field, and `NewIssuer` is four-argument — see the cross-section note at the end.)

```go
// issuerResolver assembles the per-request Issuer aggregate from the registry
// (identity + configured callbacks), the key store (public key), and the
// proxy-aware base URL. It is shared by the services that need an Issuer, so
// none of them depend on another service.
type issuerResolver struct {
	registry IssuerRegistry
	keys     KeyStore
}

func (r issuerResolver) resolve(ctx context.Context, id IssuerID, origin RequestOrigin) (Issuer, error) {
	rec, err := r.registry.Materialize(ctx, id)
	if err != nil {
		return Issuer{}, fmt.Errorf("materialize issuer: %w", err)
	}
	key, err := r.keys.SigningKey(ctx, id)
	if err != nil {
		return Issuer{}, fmt.Errorf("issuer signing key: %w", err)
	}
	// ResolveBaseURL is pure domain (issuer.go): x-forwarded-proto > scheme,
	// Host host > origin, port precedence x-forwarded-port > Host port > default.
	// Forwarded headers are client-controlled, so it returns (BaseURL, error).
	base, err := ResolveBaseURL(origin)
	if err != nil {
		return Issuer{}, fmt.Errorf("resolve base url: %w", err)
	}
	return NewIssuer(id, base, key, rec.Callbacks), nil
}
```

`BaseURL` is intentionally **not** cached in the registry: it is a per-request function of `RequestOrigin`, so caching it would bake one proxy view into every issuer (a correctness bug behind PRD C9). Identity and key are stable; the address is resolved fresh each call.

### 5. State ports: `CodeStore`, `RefreshTokenStore`, `CallbackQueue`, `RequestRecorder`

These mirror upstream's in-memory maps; all are unbounded/no-TTL by parity (except the bounded capture ring), concurrency-safe, and implemented in `internal/oidc/memory`.

```go
// CodeStore caches single-use authorization codes between /authorize and /token.
// In-memory, unbounded, no TTL (parity); concurrency-safe.
type CodeStore interface {
	Save(ctx context.Context, code AuthorizationCode, rec CodeRecord) error
	// Take returns the record for code and removes it atomically (single-use).
	// The code is removed BEFORE any PKCE check, so a failed PKCE attempt also
	// invalidates the code (catalog). Returns ErrCodeNotFound when absent.
	Take(ctx context.Context, code AuthorizationCode) (CodeRecord, error)
}

// RefreshTokenStore binds the resolved callback (and its issuer, for the
// cross-issuer check) to each refresh token so a later refresh grant re-mints
// with the original policy. It persists only — token construction (bare UUID /
// PlainJWT bytes) stays in the domain service + signing adapter. In-memory, no
// expiry; concurrency-safe.
type RefreshTokenStore interface {
	Save(ctx context.Context, token RefreshToken, rec RefreshRecord) error
	Lookup(ctx context.Context, token RefreshToken) (RefreshRecord, error)
	// Remove deletes token (rotation + revocation); removing an absent token is
	// a no-op so revoke is idempotent.
	Remove(ctx context.Context, token RefreshToken) error
}

// CallbackQueue is the core's READ side over the one-shot, issuer-matched
// Scenario queue (PRD C6). Its sole consumer is TokenService.resolveCallback;
// the WRITE side (Enqueue/List/Clear) is the consumer-declared
// controlapi.ScenarioStore, satisfied by the same memory type. Consumption is
// HEAD-only and issuer-matched: the head is taken iff its issuer matches the
// request's issuer, otherwise it blocks consumption for other issuers (catalog
// "Parity Gotchas"). FIFO, single-use, mutex-guarded.
type CallbackQueue interface {
	// DequeueFor removes and returns the head iff its issuer == id; otherwise
	// ok is false and the queue is unchanged.
	DequeueFor(ctx context.Context, id IssuerID) (Scenario, bool, error)
}

// RequestRecorder is the core's WRITE side over the per-issuer capture log
// (PRD C6/S4). It has NO core-service consumer: it is written by the httpapi
// recording middleware. The drain/read side (Take/List/Clear) is the
// consumer-declared controlapi.RequestLog, satisfied by the same memory type.
// It is hosted in ports.go (not controlapi) so the two adapters — which must not
// import each other — share it through a neutral domain type. Raw bytes are
// preserved verbatim (param order matters) and never reparsed. Bounded ring per
// issuer to cap memory; concurrency-safe.
type RequestRecorder interface {
	Record(ctx context.Context, req CapturedRequest) error
}
```

`CallbackQueue`'s `DequeueFor` encodes the trickiest parity behavior in its *type*: it cannot accidentally consume another issuer's head, because the only consume operation is issuer-conditional. The peek-then-conditional-poll quirk is thus an invariant of the port, not a convention a caller must remember.

`CapturedRequest` and its `NewCapturedRequest` constructor (owned by `capture.go`) name **only** stdlib-primitive and domain types — `method string`, `rawURL string`, `header map[string][]string`, `query map[string][]string`, `body []byte` — never `*url.URL` or `http.Header`. The httpapi recording middleware does the conversion at the edge (`r.URL.String()`, an explicit `map[string][]string(r.Header)` copy), so `capture.go` keeps the core free of `net/http`/`net/url`. The arch test's forbidden set covers `capture.go` so this regresses loudly.

### 6. The `TokenCallback` model and the resolver (domain, not a port)

`TokenCallback` is the per-request claim/subject/audience/typ/expiry policy (Contract §7). It is a domain **interface with two value implementations**, never a port — it has no IO; it is pure policy. Its multi-valued input carrier (`FormParams`) and its `Audience` result type are **domain types**, declared in the core; the adapter only *populates* `FormParams` at the edge.

```go
// callback.go

// FormParams is the domain-owned, multi-valued typed view of url-encoded form
// data — the sanctioned replacement for net/url.Values inside the core. The
// httpapi adapter parses raw form bytes into it at the edge (httpapi/form.go);
// url.Values never crosses inward.
type FormParams map[string][]string

func (p FormParams) Get(key string) string        { /* last-wins, "" if absent */ }
func (p FormParams) All(key string) []string       { return p[key] } // request-mapping templating
func (p FormParams) SpaceJoined(key string) string { /* e.g. scope */ }

// Audience is the ordered audience list a token carries (aud). Declared once in
// the domain; ClaimSet.Audience, CallbackInput.Audience and TokenCallback.
// Audience() all use it. A nil Audience means "unset" (fall through the 4-step
// chain); a non-nil empty Audience means an explicitly configured empty audience
// (stop, emit no aud) — the two are distinct (catalog audience step 1).
type Audience []string

// CallbackInput is the typed, transport-free view a TokenCallback matches and
// templates against: the parsed grant kind, client, scope, form params, and any
// synthetic params (e.g. the login-injected "subject"). It replaces upstream's
// raw url.Values / Map<String,List<String>>, keeping map[string]any out of the
// domain.
type CallbackInput struct {
	Grant    GrantType
	Client   Client
	Scopes   Scopes
	Subject  Subject    // ROPC username / client_id / login username, pre-resolved
	Params   FormParams // domain type; httpapi/form.go populates it at the edge
	Audience Audience   // token-exchange / configured audience candidates
}

// TokenCallback decides a token's content for one request.
type TokenCallback interface {
	IssuerID() IssuerID
	Subject(in CallbackInput) Subject
	Audience(in CallbackInput) Audience
	// TypeHeader returns the JWS "typ" header value (open: default "JWT", may be
	// "at+jwt", etc.) — the open JOSEType, NOT the closed TokenType enum used for
	// the response token_type field.
	TypeHeader(in CallbackInput) JOSEType
	ExtraClaims(in CallbackInput) ClaimSet
	Expiry() time.Duration
	// Matches reports whether this (configured) callback applies to in; the
	// default callback always matches.
	Matches(in CallbackInput) bool
}
```

Resolution priority (enqueued one-shot > configured > default) is **domain composition over the `CallbackQueue` port plus the registry record**, not itself a port — exactly the "resolver" the task names, located correctly. Because configured callbacks live on `IssuerRecord` (the single source), `resolveCallback` is a `TokenService` method, not an injected object:

```go
// resolveCallback applies the catalog priority: an issuer-matched enqueued
// Scenario wins; else the first configured callback that Matches; else the
// built-in DefaultTokenCallback (tid = issuerId always; azp = client_id for
// authorization_code only). The refresh grant calls the same method, so it
// shares one queue with the other grants (catalog).
func (s *TokenService) resolveCallback(ctx context.Context, issuer Issuer, in CallbackInput) (TokenCallback, error) {
	if sc, ok, err := s.scenarios.DequeueFor(ctx, issuer.ID); err != nil {
		return nil, fmt.Errorf("dequeue scenario: %w", err)
	} else if ok {
		return sc.Callback, nil
	}
	for _, cb := range issuer.Callbacks { // configured, first-match order
		if cb.Matches(in) {
			return cb, nil
		}
	}
	return NewDefaultTokenCallback(issuer.ID), nil
}
```

### 7. The four application services

Each service follows the template's `NewService` shape: ports + logger + functional options; logger defaults to `slog.DiscardHandler`; request-scoped logging through `logctx.From(ctx)`.

#### 7.1 ProviderService

```go
// ProviderService serves provider metadata: discovery / AS-metadata and JWKS,
// with proxy-aware base-URL resolution. It mutates nothing.
type ProviderService struct {
	issuers issuerResolver
	logger  *slog.Logger
}

// Discovery builds the issuer's discovery document. Field order is fixed by the
// DiscoveryDocument struct declaration (Contract §7); the advertised algorithm
// set is the domain constant cross-checked against the Signer's capabilities.
func (s *ProviderService) Discovery(ctx context.Context, id IssuerID, origin RequestOrigin) (DiscoveryDocument, error) {
	issuer, err := s.issuers.resolve(ctx, id, origin)
	if err != nil {
		return DiscoveryDocument{}, err
	}
	return NewDiscoveryDocument(issuer.BaseURL, SupportedSigningAlgorithms()), nil
}

// JWKS returns the issuer's public JWK set, forcing key materialization so the
// set is never empty.
func (s *ProviderService) JWKS(ctx context.Context, id IssuerID) (JWKS, error) {
	return s.issuers.keys.PublicKeys(ctx, id)
}
```

#### 7.2 AuthorizeService

The domain decides *what* the authorize response is (issue a code, or require login) and returns a typed `AuthorizeResult`; the adapter renders it (302 / `form_post` HTML / login page). No HTML, no `redirect_uri` string-building leaks inward beyond the typed result. The kind enum is the single, three-valued contract the adapter switches on; the domain returns typed fields (`Code`/`State`/`RedirectURI`/`Mode`) and the adapter builds the redirect URL (url-encoding stays at the edge).

```go
// AuthorizeResultKind enumerates what /authorize decided. The adapter switches
// on it: ShowLogin -> render the login page; FormPost -> render the auto-submit
// HTML; Redirect -> 302 with the code in query|fragment (Mode distinguishes).
type AuthorizeResultKind int

const (
	AuthorizeShowLogin AuthorizeResultKind = iota // render the interactive login page
	AuthorizeFormPost                             // response_mode=form_post: auto-submit HTML
	AuthorizeRedirect                             // response_mode=query|fragment: 302 with code
)

// Authorize decides between issuing a code and requiring interactive login.
// interactiveLogin (server config) OR prompt ∈ {login,consent,select_account}
// forces the page; prompt=none never does (catalog). Only response_type=code is
// implemented; others are rejected as invalid_grant by the typed request parse.
func (s *AuthorizeService) Authorize(ctx context.Context, req AuthorizeRequest) (AuthorizeResult, error) {
	if s.interactiveLogin || req.Prompt.RequiresLogin() {
		return AuthorizeResult{Kind: AuthorizeShowLogin, Request: req}, nil
	}
	return s.issueCode(ctx, req, LoginSubmission{}) // non-interactive: subject from config/default
}

// SubmitLogin handles POST /authorize: it builds the CodeRecord (login snapshot,
// nonce from the request, PKCE challenge) and caches it under a fresh code for
// token-time use. Login claims are stored to be merged putIfAbsent at mint time
// (mapping wins) — the 5.0.0 contract.
func (s *AuthorizeService) SubmitLogin(ctx context.Context, req AuthorizeRequest, login LoginSubmission) (AuthorizeResult, error) {
	return s.issueCode(ctx, req, login)
}

func (s *AuthorizeService) issueCode(ctx context.Context, req AuthorizeRequest, login LoginSubmission) (AuthorizeResult, error) {
	code := s.newCode() // injected ID source (functional option), like the template
	rec := NewCodeRecord(req.Snapshot(), req.Nonce, req.PKCE, login, s.clock.Now())
	if err := s.codes.Save(ctx, code, rec); err != nil {
		return AuthorizeResult{}, fmt.Errorf("cache authorization code: %w", err)
	}
	kind := AuthorizeRedirect
	if req.ResponseMode == ResponseModeFormPost {
		kind = AuthorizeFormPost
	}
	return AuthorizeResult{
		Kind:        kind,
		Code:        code,
		State:       req.State,
		RedirectURI: req.RedirectURI,
		Mode:        req.ResponseMode, // query|fragment for the Redirect case
	}, nil
}
```

#### 7.3 TokenService — grant flow without transport/crypto leaking

The public entry is `Issue(ctx context.Context, origin RequestOrigin, req TokenRequest) (TokenResponse, error)`: it resolves the issuer through the shared `issuerResolver` (so `iss`/base-URL are proxy-correct), then dispatches on the closed `GrantType` enum to the per-grant method. The handler has already parsed `RawBody` into a typed `TokenRequest` (`httpapi/form.go`); the service consumes only typed values and ports; the `Signer` port performs all crypto; the result is a `TokenResponse` value the adapter maps to JSON. Below are the two flows the task asks for, end to end.

**client_credentials** (access token only; `sub` defaults to `client_id`; 4-step audience precedence):

```go
func (s *TokenService) clientCredentials(ctx context.Context, issuer Issuer, req TokenRequest) (TokenResponse, error) {
	in := req.CallbackInput()
	cb, err := s.resolveCallback(ctx, issuer, in)
	if err != nil {
		return TokenResponse{}, err
	}
	now := s.clock.Now()
	claims := s.defaultClaims(issuer, cb.Subject(in), cb.Audience(in), cb, now) // iss/sub/aud/iat/nbf/exp/jti (+tid)
	access, err := s.signer.Sign(ctx, issuer.ID, NewToken(issuer.Key.KeyID, issuer.Key.Algorithm, cb.TypeHeader(in), claims))
	if err != nil {
		return TokenResponse{}, fmt.Errorf("sign access token: %w", err)
	}
	return TokenResponse{
		TokenType:   TokenTypeBearer,
		AccessToken: access,
		Scope:       req.Scopes,                  // echoed
		ExpiresIn:   expiresIn(now, cb.Expiry()), // same clock as exp (parity correction)
	}, nil
}
```

**authorization_code** (id + access + refresh; single-use code; PKCE only when a verifier is presented; `azp` only here):

```go
func (s *TokenService) authorizationCode(ctx context.Context, issuer Issuer, req TokenRequest) (TokenResponse, error) {
	// Single-use: the code is removed here, BEFORE the PKCE check, so a failed
	// PKCE attempt also burns the code (catalog).
	rec, err := s.codes.Take(ctx, req.Code)
	if err != nil {
		return TokenResponse{}, InvalidGrant("unknown or already-used authorization code")
	}
	// PKCE is a pure domain invariant: S256 = base64url(sha256(verifier)).
	// Enforced only when a verifier is present (a bare challenge need not be
	// redeemed). This uses crypto/sha256 + crypto/subtle + encoding/base64 —
	// std-lib KEYLESS transforms with no key material or IO. They are the single
	// documented carve-out to the "core touches no crypto" rule (see §10): the
	// dependency gate denies the crypto SIGNING packages (crypto/rsa, crypto/
	// ecdsa, crypto/ed25519, crypto/tls, crypto/x509) and the JOSE lib, not these
	// hash/compare primitives. This is NOT signing wiring.
	if err := rec.VerifyPKCE(req.CodeVerifier); err != nil {
		return TokenResponse{}, err // InvalidGrant("invalid_pkce: ...")
	}

	in := rec.CallbackInput(req)       // nonce + login from the CACHED request, not the token request
	cb, err := s.resolveCallback(ctx, issuer, in)
	if err != nil {
		return TokenResponse{}, err
	}
	now := s.clock.Now()
	sub := cb.Subject(in)

	// id_token: aud is ALWAYS [client_id]; nonce from the cached request; azp added.
	idClaims := s.defaultClaims(issuer, sub, Audience{string(req.ClientID)}, cb, now).
		WithNonce(rec.Nonce).WithAZP(req.ClientID)
	idToken, err := s.signer.Sign(ctx, issuer.ID, NewToken(issuer.Key.KeyID, issuer.Key.Algorithm, cb.TypeHeader(in), idClaims))
	if err != nil {
		return TokenResponse{}, fmt.Errorf("sign id token: %w", err)
	}

	// access_token: aud from the callback's 4-step chain; same nonce.
	accClaims := s.defaultClaims(issuer, sub, cb.Audience(in), cb, now).WithNonce(rec.Nonce)
	accessToken, err := s.signer.Sign(ctx, issuer.ID, NewToken(issuer.Key.KeyID, issuer.Key.Algorithm, cb.TypeHeader(in), accClaims))
	if err != nil {
		return TokenResponse{}, fmt.Errorf("sign access token: %w", err)
	}

	refresh, err := s.issueRefresh(ctx, issuer, cb, rec.Nonce) // form chosen here; bytes from ID source / signing adapter
	if err != nil {
		return TokenResponse{}, err
	}
	return TokenResponse{
		TokenType:    TokenTypeBearer,
		IDToken:      idToken,
		AccessToken:  accessToken,
		RefreshToken: refresh,
		Scope:        req.Scopes,
		ExpiresIn:    expiresIn(now, cb.Expiry()),
	}, nil
}
```

`defaultClaims` is a pure domain helper assembling the registered claims into a `ClaimSet` (the typed container — never `map[string]any`), reading `iss` from `issuer.BaseURL`, and stamping `tid = issuerId` as an *overridable* claim (a custom-claim merge may replace it — catalog). Its `With*`/`Merge` methods are copy-on-write: each returns a new `ClaimSet` and never mutates the receiver's backing map, so the chained `defaultClaims(...).WithNonce(...).WithAZP(...)` cannot alias claims across the id-token and access-token copies. `issueRefresh` persists a `RefreshRecord{Callback, IssuerID, Subject, Claims, Nonce}` under the new token in `RefreshTokenStore` so the refresh grant can re-mint and enforce the cross-issuer rule (the corrected client text `"refresh_token was issued by a different issuer"`). The refresh token's *bytes* are produced **outside** the domain — a bare UUID from the injected ID source, or, when a nonce is present, an unsigned `alg=none` PlainJWT compact-serialized by the `signing` adapter through the `Signer` port. `refresh.go` only chooses the form (a typed constructor flag) and persists the record; it never JSON- or base64url-encodes a JWT, preserving "the domain never serializes." Nothing in either body touches HTTP or a private key.

**Mint** (control-plane test use case, PRD C6 — upstream `issueToken`/`anyToken`):

```go
// Mint issues a signed token directly, bypassing the grant flow, for test setup
// against a running container. The callback fully determines content; the Clock
// determines iat/nbf/exp. The upstream anyToken Duration->millis quirk (~1000x
// inflated expiry) is NOT replicated (PRD N8). It returns BOTH the wire token
// and its ClaimSet so the control plane can echo the minted claims.
func (s *TokenService) Mint(ctx context.Context, spec MintSpec) (SignedToken, ClaimSet, error) {
	issuer, err := s.issuers.resolve(ctx, spec.Issuer, spec.Origin)
	if err != nil {
		return "", ClaimSet{}, err
	}
	now := s.clock.Now()
	claims := s.defaultClaims(issuer, spec.Subject, spec.Audience, spec.Callback, now).Merge(spec.Claims)
	tok, err := s.signer.Sign(ctx, issuer.ID, NewToken(issuer.Key.KeyID, issuer.Key.Algorithm, spec.TypeHeader, claims))
	if err != nil {
		return "", ClaimSet{}, fmt.Errorf("mint token: %w", err)
	}
	return tok, claims, nil
}
```

#### 7.4 SessionService

```go
// UserInfo verifies the bearer token and returns its entire claim set verbatim
// (no scoping). Verification failure -> invalid_token (401). The presence-only
// "Bearer " parse happens at the adapter edge; the domain receives a typed
// SignedToken.
func (s *SessionService) UserInfo(ctx context.Context, req UserInfoRequest) (ClaimSet, error) {
	claims, err := s.verifier.Verify(ctx, req.Issuer, req.Token, s.clock.Now())
	if err != nil {
		return ClaimSet{}, NewInvalidToken(err)
	}
	return claims, nil
}

// Introspect verifies the token; an UNVERIFIABLE token is reported as
// {active:false}, NOT an error (catalog). The presence-only client-auth check
// (else invalid_client at 400) is enforced at the adapter edge.
func (s *SessionService) Introspect(ctx context.Context, req IntrospectionRequest) (IntrospectionResult, error) {
	claims, err := s.verifier.Verify(ctx, req.Issuer, req.Token, s.clock.Now())
	if err != nil {
		return InactiveIntrospection(), nil
	}
	return IntrospectionFrom(claims), nil
}

// Revoke removes a refresh token. Only token_type_hint=refresh_token is
// supported; any other hint -> unsupported_token_type (mapped at the edge).
func (s *SessionService) Revoke(ctx context.Context, req RevocationRequest) error {
	if req.Hint != TokenHintRefreshToken {
		return UnsupportedTokenType(req.Hint)
	}
	if err := s.refresh.Remove(ctx, req.Token); err != nil {
		return fmt.Errorf("revoke refresh token: %w", err)
	}
	return nil
}

// EndSession is pure: it computes the post-logout outcome from the typed request
// (post_logout_redirect_uri + state, query-only). No id_token_hint validation,
// no session termination (catalog) — there are no real sessions to end.
func (s *SessionService) EndSession(_ context.Context, req EndSessionRequest) (EndSessionResult, error) {
	return NewEndSessionResult(req.PostLogoutRedirectURI, req.State), nil
}
```

Because `Verify` is threaded `s.clock.Now()` from the same mutable `memory.Clock` the control plane advances, `/userinfo` and `/introspect` see expiry exactly as issuance did: an advanced clock turns a once-valid token into `invalid_token` (401) / `{active:false}` respectively, with no separate time source to drift.

### 8. Adapter-tier seams the core deliberately excludes

The task lists `SessionStore` and `TemplateRenderer`. Both are real seams, but putting them in `internal/oidc/ports.go` would drag HTML and cookies into the core and break Contract §3. They are therefore **consumer-declared ports of the `httpapi` adapter** — the same hexagonal pattern, one tier out:

| Seam | Declared in | Why not a domain port |
|---|---|---|
| `TemplateRenderer` | `httpapi` | Renders the login page, `form_post` auto-submit, debugger, and error HTML. The core returns typed `AuthorizeResult`/`EndSessionResult` values; turning them into `text/html` is presentation. Would force `html/template` into a core that must not import it. |
| `DebuggerSessionStore` | `httpapi` (debugger) | Upstream's "session" is the encrypted `debugger-session` *cookie* (JWE, per-process key) — a transport concern with no domain meaning. OIDC end-session does **no** server-side session termination (catalog), so there is no domain session to store. |

```go
// httpapi/render.go — declared by the adapter that consumes it.

// TemplateRenderer turns a domain AuthorizeResult / error into HTML. It is an
// httpapi seam, not a domain port: the core never produces HTML.
type TemplateRenderer interface {
	LoginPage(w io.Writer, req oidc.AuthorizeRequest) error
	FormPost(w io.Writer, res oidc.AuthorizeResult) error
	Debugger(w io.Writer, data DebuggerView) error
	ErrorPage(w io.Writer, e oidc.ProtocolError) error
}

// DebuggerSessionStore persists the debugger's encrypted form state across the
// redirect round-trip. An httpapi/debugger seam (cookie/JWE), never a domain
// port.
type DebuggerSessionStore interface {
	Put(values DebuggerForm) (Cookie, error)
	Get(c Cookie) (DebuggerForm, error)
}
```

This keeps the answer to "where do these live?" precise: they exist, they follow ports-and-adapters, but the consumer that declares them is the transport adapter, not a use case.

### 9. In-memory adapters (`internal/oidc/memory`)

Every driven port here is implemented in `internal/oidc/memory` as a `sync`-guarded map/deque — production adapters (not test-only), matching upstream's in-memory maps and the template's `todotest.Repository` idiom (mutex + map, returning typed sentinels). They may import only `sync` (Contract §3).

| Port | Backing structure | Notes |
|---|---|---|
| `IssuerRegistry` | `map[IssuerID]IssuerRecord` under `RWMutex` | computeIfAbsent on `Materialize`; seeded from config. |
| `CodeStore` | `map[AuthorizationCode]CodeRecord` | `Take` = load-and-delete under write lock (atomic single-use). |
| `RefreshTokenStore` | `map[RefreshToken]RefreshRecord` | `Remove` idempotent. |
| `CallbackQueue` | slice as FIFO deque under `Mutex` | `DequeueFor` peeks head, pops only on issuer match; the same type's `Enqueue`/`List`/`Clear` satisfy `controlapi.ScenarioStore`. |
| `RequestRecorder` | `map[IssuerID][]CapturedRequest` (bounded ring) | preserves raw bytes; `Record` appends; the same type's `Take`/`List`/`Clear` satisfy `controlapi.RequestLog`. |
| `Clock` | `Instant` + freeze flag under `Mutex` | mutable: `Freeze`/`Unfreeze`/`Advance`; satisfies `oidc.Clock` for the core and the control plane's clock-controller. |

`KeyStore`/`Signer`/`TokenVerifier` are **not** in `memory` — they hold private keys and JOSE wiring, so they live in `internal/oidc/signing`. Representative implementation (the one with a non-trivial invariant — atomic single-use):

```go
// memory/code_store.go

// CodeStore is an in-memory oidc.CodeStore safe for concurrent use.
type CodeStore struct {
	mu    sync.Mutex
	codes map[oidc.AuthorizationCode]oidc.CodeRecord
}

// NewCodeStore constructs an empty CodeStore.
func NewCodeStore() *CodeStore {
	return &CodeStore{codes: make(map[oidc.AuthorizationCode]oidc.CodeRecord)}
}

// Save stores rec under code.
func (s *CodeStore) Save(_ context.Context, code oidc.AuthorizationCode, rec oidc.CodeRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.codes[code] = rec
	return nil
}

// Take returns the record and deletes it in one critical section, so a code can
// never be redeemed twice even under concurrent /token calls.
func (s *CodeStore) Take(_ context.Context, code oidc.AuthorizationCode) (oidc.CodeRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.codes[code]
	if !ok {
		return oidc.CodeRecord{}, oidc.ErrCodeNotFound
	}
	delete(s.codes, code)
	return rec, nil
}
```

```go
// memory/callback_queue.go — issuer-matched HEAD-only consumption.

// DequeueFor removes and returns the head iff it targets id; a head for another
// issuer blocks consumption (parity), so the queue is unchanged on a miss. The
// same type also exposes Enqueue/List/Clear to satisfy controlapi.ScenarioStore.
func (q *CallbackQueue) DequeueFor(_ context.Context, id oidc.IssuerID) (oidc.Scenario, bool, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 0 || q.items[0].IssuerID() != id {
		return oidc.Scenario{}, false, nil
	}
	head := q.items[0]
	q.items = q.items[1:]
	return head, true, nil
}
```

### 10. Construction, mocks, and the layering proof

**Service construction** mirrors the template exactly. Each service has a `New*` taking its ports + logger + variadic `Option`s; a shared `serviceDeps` holds the `Clock`/ID seams so `WithClock`/`WithCodeGenerator` apply uniformly:

```go
// NewTokenService wires the token use cases. logger nil -> discard.
func NewTokenService(
	registry IssuerRegistry, keys KeyStore, signer Signer,
	codes CodeStore, refresh RefreshTokenStore, scenarios CallbackQueue,
	rotateRefresh bool, logger *slog.Logger, opts ...Option,
) *TokenService { /* defaults clock=SystemClock{}, then apply opts */ }
```

The composition root (`internal/app`) is the only place concrete adapters are named: it builds `signing.New(...)`, the five `memory.*` stores, the mutable `memory.Clock` (seeded from the config seed's `systemTime`, satisfying both `oidc.Clock` for the core and the control plane's clock-controller), seeds the `IssuerRegistry` from the typed JSON-config domain seed (including its configured callbacks), and injects them into the four services — then hands the services to `httpapi.Register` and `controlapi.Register`. `oidc.SystemClock`/`FixedClock` are reserved for unit tests.

**Mocks.** `.mockery.yaml` `packages:` is repointed from the removed `todo`/`authz` ports to the eight `internal/oidc` ports, generating `internal/oidc/mocks` (committed; guarded by the `mockery-check` moon task):

```yaml
packages:
  github.com/meigma/mock-oidc/internal/oidc:
    interfaces:
      Clock:
      KeyStore:
      Signer:
      TokenVerifier:
      IssuerRegistry:
      CodeStore:
      RefreshTokenStore:
      CallbackQueue:
      RequestRecorder:
```

**Layering proof.** `internal/oidc/doc.go` states the forbidden-imports rule, and a committed `internal/oidc/imports_test.go` asserts it the way the template asserts enum/constant sync: it walks the package's import set and fails if any of `huma`, `chi`, `net/http`, `net/url`, `crypto/{rsa,ecdsa,ed25519,tls,x509}`, JOSE libs, `viper`, `cobra`, `otel`, `prometheus`, or `internal/adapter/*` appears. The grant-flow bodies above pass this test by construction: the only non-std imports they touch are sibling domain files and the port interfaces. `crypto/sha256` + `crypto/subtle` + `encoding/base64` (PKCE) are the single, explicitly-whitelisted exception — pure, keyless transforms, justified inline where `VerifyPKCE` is defined. The dependency gate (depguard) denies the crypto **signing** packages enumerated above plus the JOSE library, not blanket `crypto/`, keeping doc.go, the arch test, and Contract §3's "no crypto signing/JOSE" intent in agreement.

---

Key cross-section dependencies the assembler should note: the **typed-command shapes** (`TokenRequest`, `AuthorizeRequest`, `CallbackInput`, `MintSpec`, etc.) are defined in the domain-types section (`requests.go`/`callback.go`); the open `JOSEType` (JWS `typ`, default `JWT`, accepts `at+jwt` — distinct from the closed `TokenType` enum used for the response `token_type`), the `Audience` and `FormParams` domain types, and the `Nonce` type this section consumes are likewise owned by domain-types; the `Issuer` aggregate carries a `Callbacks []TokenCallback` field (four-field struct, four-argument `NewIssuer`) per this section's `issuerResolver`; the **`ProtocolError` constructors** (`InvalidGrant`, `UnsupportedTokenType`, `NewInvalidToken`) referenced here are owned by the Error Model section (`errors.go`) and return `*ProtocolError`; the **`signing` adapter internals** (RSA-2048/EC, seeded deque, JOSE, the unsigned alg=none PlainJWT serialization) are owned by the signing-adapter section. This section owns only the service methods, the port interfaces (`KeyStore`, `Signer`, `TokenVerifier`, `IssuerRegistry`, `CodeStore`, `RefreshTokenStore`, `CallbackQueue`, `RequestRecorder`, `Clock`), the resolver/clock composition, and the `memory` adapter approach.

## HTTP / Protocol Adapter (`internal/oidc/httpapi`)

This is the package that turns HTTP into the domain's typed commands and turns domain results (and typed errors) back into wire bytes. It is the *only* place in the OIDC slice that knows Huma, `net/http`, `net/url`, and `html/template` exist. It depends inward on `internal/oidc` and plugs into the kept generic transport (`internal/adapter/http`) through the `Registrar` seam — exactly as `internal/todo/httpapi` plugs in today, but with a richer output vocabulary because OAuth2 is not a uniform JSON CRUD surface.

### N.1 Boundary discipline (anti-corruption)

The package obeys three rules that keep the domain pure:

1. **Parse, don't validate, at the edge.** Raw form bytes, query strings, and headers are parsed into the §7 typed commands (`oidc.TokenRequest`, `oidc.AuthorizeRequest`, `oidc.LoginSubmission`, …) in `form.go`/`mapping.go` *before* any application service is called. No `url.Values`, no `map[string]any`, and no bare `string` grant type ever crosses into `internal/oidc`.
2. **DTOs are transport types, not domain types.** `dto.go` holds the wire structs (`TokenResponseDTO`, `DiscoveryDTO`, `JWKSDTO`, …); `mapping.go` holds the pure `toXDTO` / `decodeXRequest` functions. The domain never imports a DTO and the wire never sees a domain value directly — the same split `internal/todo/httpapi/{dto.go,mapping.go}` already uses.
3. **Domain errors are translated, never leaked.** `oautherr.go` is the single chokepoint that turns `*oidc.ProtocolError` into the RFC 6749 body via `errors.As`; nothing else formats an error.

```go
// Package httpapi is the inbound HTTP transport adapter for the OIDC/OAuth2
// protocol surface: request/response DTOs, typed url-encoded form parsers,
// domain<->DTO mapping, the RFC 6749 error writer, and the Huma operation
// registrations. It depends inward on internal/oidc and mounts onto the generic
// transport (internal/adapter/http) through its Registrar seam. It is the only
// OIDC package that imports huma, net/http, net/url, or html/template.
package httpapi
```

### N.2 The `Registrar` seam and multi-issuer routing

The generic router exposes `type Registrar func(huma.API)` and calls it once, after the tracing/rate-limit middleware are installed and before infra routes are mounted (`router.go`). The composition root closes over the wired application services and the adapter's own setup into a single `Registrar`:

```go
// app wiring (internal/app): one Registrar mounts the whole protocol surface.
register := func(api huma.API) {
    httpapi.Register(api, httpapi.Deps{
        Provider:  providerSvc,  // *oidc.ProviderService
        Authorize: authorizeSvc, // *oidc.AuthorizeService
        Tokens:    tokenSvc,     // *oidc.TokenService
        Sessions:  sessionSvc,   // *oidc.SessionService
        Assets:    assetCfg,     // optional /static dir + custom login page
    })
    controlapi.Register(api, controlDeps) // the _mock/* control plane
}
deps := http.RouterDeps{ /* ... */ Register: register }
```

`Register` installs the request-origin middleware (must run *before* `huma.Register`, since Huma snapshots the middleware stack into each operation at registration), mounts every operation, then stamps the OpenAPI security schemes (metadata-only, safe post-register):

```go
// Deps carries the application services the operations call. All are inward
// (domain) types; no adapter is named here. Field names are frozen: Tokens and
// Sessions are plural (the composition root wires to these exact names). The
// request recorder is NOT a Deps field — it is installed as a router-level
// recording middleware (see N.11), not threaded through the inbound handlers.
type Deps struct {
    Provider  *oidc.ProviderService
    Authorize *oidc.AuthorizeService
    Tokens    *oidc.TokenService
    Sessions  *oidc.SessionService
    Assets    AssetConfig // zero value => /static unmounted, built-in login page
}

const (
    tagOIDC  = "OIDC"
    tagOAuth = "OAuth2"
    // issuerParam is the path parameter that carries the (zero-config, on-demand)
    // issuer id as the first path segment. Validated into oidc.IssuerID per call.
    issuerParam = "issuer"
)

func Register(api huma.API, deps Deps) {
    api.UseMiddleware(requestOriginMiddleware) // edge: derive RequestOrigin

    h := &handlers{deps: deps}
    h.registerDiscovery(api) // OIDC + RFC 8414 alias
    h.registerJWKS(api)
    h.registerAuthorize(api) // GET authorize + POST login submit
    h.registerToken(api)
    h.registerUserInfo(api)
    h.registerIntrospect(api)
    h.registerRevoke(api)
    h.registerEndSession(api)
    h.registerDebugger(api)
    h.registerFavicon(api)
    // /static is NOT a Huma op (see N.3): the composition root mounts it as a raw
    // chi wildcard from the http.Handler this package exposes when StaticDir is set.

    stampSecuritySchemes(api) // post-register Components.SecuritySchemes
}
```

**Routing decision realized (contract §8).** Issuers are a **path parameter**, `/{issuer}/<endpoint>`, registered **once per endpoint** as an ordinary Huma operation — *not* a `huma.NewGroup` per issuer (issuers are dynamic, materialized on first reference). The handler parses `{issuer}` into `oidc.IssuerID` and lets the `IssuerRegistry`/`KeyStore` lazily materialize it:

```go
// issuerOf parses the path parameter into the domain identity. A malformed or
// reserved issuer (e.g. one colliding with the _mock control prefix) is a typed
// *oidc.ProtocolError, surfaced through the endpoint's normal OAuth2 error path.
func issuerOf(in interface{ issuerID() string }) (oidc.IssuerID, error) {
    return oidc.ParseIssuerID(in.issuerID())
}
```

`oidc.ParseIssuerID` is the **single** issuer smart constructor used by every section (httpapi, controlapi, config, the roadmap slices); it returns a `*oidc.ProtocolError` (never a bare wrapped sentinel) so the edge always gets a code+status it can map: empty or `/`-containing → `invalid_request`/400, reserved `_mock` collision → `not_found`/404. Because chi matches static routes before wildcards, the root infra routes (`/healthz`, `/readyz`, `/metrics`, the `/isalive` alias) and the `/_mock/*` control plane never get shadowed by `/{issuer}/...`.

**Named parity gap — single-segment issuers.** The contract's `/{issuer}/...` path parameter and `IssuerID`'s "no `/`" rule realize **single-path-segment** issuers. Upstream's `endsWith(path)` suffix matching additionally admits *multi-segment* issuer ids (e.g. an Azure-style `/{tenant}/v2.0`), which a chi single-segment param cannot express. We record this as a **named, accepted parity gap** rather than claiming full equivalence: common usage is single-segment, the `/{issuer}/...` form is the clean OpenAPI-expressible shape, and adopters with nested issuer ids are warned in the parity catalog. (If nested issuers are later required, the route becomes a raw chi catch-all with an edge suffix-splitter and a relaxed `IssuerID` permitting interior `/` — still rejecting a reserved `_mock` first segment — but that is out of scope for this design.)

**Cardinality.** Metrics/trace labels use the **chi route template** (`/{issuer}/token`), never the resolved `{issuer}` value — the kept `observability.TraceSpanNamer` already names spans from `huma.Operation.Path` (the template), and the metrics middleware labels off the matched route pattern, so a client-controlled issuer cannot explode the time series.

### N.3 Endpoint inventory

Every protocol endpoint is a **typed Huma operation** (contract §5: the entire OAuth2/OIDC surface goes through Huma; raw chi is reserved for infra). The infra rows — and the conditional `/static/*` tree — are the only raw chi handlers; infra lives in `internal/adapter/http`, and the static wildcard is mounted by the composition root from the `http.Handler` this package exposes.

| Endpoint (issuer-scoped) | Method | File | Transport | Input type | Output type | Body mechanism (§5) | Error contract |
|---|---|---|---|---|---|---|---|
| `/{issuer}/.well-known/openid-configuration` | GET | `discovery.go` | Huma op | `DiscoveryInput` | `ProtocolJSON` (Body `DiscoveryDTO` \| `OAuth2Error`) | JSON §5(4) | OAuth2 |
| `/{issuer}/.well-known/oauth-authorization-server` | GET | `discovery.go` | Huma op (alias → same handler) | `DiscoveryInput` | `ProtocolJSON` | JSON §5(4) | OAuth2 |
| `/{issuer}/jwks` | GET | `jwks.go` | Huma op | `JWKSInput` | `ProtocolJSON` (Body `JWKSDTO` \| `OAuth2Error`) | JSON §5(4) | OAuth2 |
| `/{issuer}/authorize` | GET | `authorize.go` | Huma op | `AuthorizeInput` (permissive query strings) | `BrowserOutput` (302 \| HTML \| JSON-err) | §5(2)+(3) | OAuth2 via redirect/direct |
| `/{issuer}/authorize` | POST | `login.go` | Huma op | `LoginInput{RawBody}` | `BrowserOutput` | §5(1) in, §5(2)/(3) out | OAuth2 |
| `/{issuer}/token` | POST | `token.go` | Huma op | `TokenInput{RawBody,Authorization}` | `ProtocolJSON` (`Status`+`Body any`) | §5(1) in, §5(4) out | OAuth2 |
| `/{issuer}/userinfo` | GET | `userinfo.go` | Huma op | `UserInfoInput{Authorization}` | `ProtocolJSON` | JSON \| OAuth2 | OAuth2 (`401 invalid_token`) |
| `/{issuer}/introspect` | POST | `introspect.go` | Huma op | `IntrospectInput{RawBody,Authorization}` | `ProtocolJSON` | §5(1) in, §5(4) out | OAuth2 (`400 invalid_client`) |
| `/{issuer}/revoke` | POST | `revoke.go` | Huma op | `RevokeInput{RawBody}` | `ProtocolJSON` | §5(1) in, JSON/200 out | OAuth2 (`400 unsupported_token_type`) |
| `/{issuer}/endsession` | GET, POST | `endsession.go` | Huma ops (2 registrations, 1 handler) | `EndSessionInput` (query strings) | `BrowserOutput` (302 \| HTML) | §5(2)+(3) | OAuth2 |
| `/{issuer}/debugger` | GET | `debugger.go` | Huma op | `DebuggerInput` | `HTMLOutput` | §5(3) | HTML error page |
| `/{issuer}/debugger` | POST | `debugger.go` | Huma op | `DebuggerSubmitInput{RawBody}` | `BrowserOutput` + `Set-Cookie` | §5(1) in, §5(2) out | HTML error page |
| `/{issuer}/debugger/callback` | GET, POST | `debugger.go` | Huma ops (2 registrations, 1 handler) | `DebuggerCallbackInput` | `HTMLOutput` | §5(3) | HTML error page |
| `/favicon.ico` (root) | GET | `favicon.go` | Huma op | (none) | `HTMLOutput` (empty 200) | §5(3) | — |
| `/static/*` (root, conditional) | GET | `static.go` | **raw chi wildcard** (mounted iff `Assets.StaticDir`; out of spec) | — | `http.FileServer` tree | file bytes | 404 (infra shape) |
| `/healthz`, `/readyz`, `/metrics`, `/isalive` | GET | `adapter/http` | **raw chi** (out of spec) | — | — | — | RFC 9457 |

Notes carried from the parity catalog, applying parity-**in-intent**:

- **Discovery + RFC 8414 alias** are two `huma.Register` calls (Huma requires a unique method+path per operation) that share one handler and produce the **identical** body — matching upstream's `get(vararg paths)` registration.
- **Multi-method endpoints** (`/endsession`, `/debugger/callback`) are registered **once per method** with distinct `OperationID`s (`endsession-get`/`endsession-post`, …) bound to the *same* handler func — `huma.Operation.Method` is a scalar, so "one op, two methods" is two registrations sharing a Go function (the same pattern discovery uses for its two paths).
- **`GET /token`** upstream returns a distinct `405 "unsupported method"`; we register only `POST` and let the router's protocol-family 405 fallback emit a single OAuth2-shaped error. The bespoke body string is an upstream quirk we do not replicate (D-2) — the roadmap's Slice-1 DoD asserts a generic OAuth2-shaped 405, not the literal `"unsupported method"`.
- **`/endsession`** reads `post_logout_redirect_uri`/`state` from the **query only** (RP-initiated-logout convention); we register the real `GET`/`POST` methods rather than a catch-all, and append `state` only when present without the upstream NPE-on-pre-existing-query defect.
- **`userinfo`** adds a `WWW-Authenticate: Bearer error="invalid_token"` header on 401 (contract §6 "where the spec calls for it", RFC 6750) — correcting upstream's omission.
- **`favicon`** stays a flat Huma op (single coherent strategy) returning an empty `200`.
- **`/static`** serves a directory *tree* (upstream `staticAssetsPath`). A Huma single-segment `{file}` path param cannot match nested asset paths (`/static/css/app.css` would 404), so static is the **one presentation exception** to "Huma for everything": the composition root mounts a raw chi wildcard `mux.Handle("/static/*", http.StripPrefix(...))` over the `http.FileServer` this package builds from `Assets.StaticDir` — justified exactly like the infra raw-chi routes (multi-segment, out of the OpenAPI document). Because the built-in login/error pages inline their CSS (a DX correction over upstream's Google-Fonts dependency), the default deployment needs no static tree at all.

### N.4 The four Huma I/O envelopes

All operations are built from four reusable output shapes plus one form-input shape, realizing contract §5 mechanisms 1–4.

```go
// (§5.1) Form input: Huma stores the raw bytes; the adapter parses them.
type formBody struct {
    Issuer  string `path:"issuer"`
    RawBody []byte `contentType:"application/x-www-form-urlencoded"`
}
func (f *formBody) issuerID() string { return f.Issuer }

// (§5.2) Browser output: serves a 302 redirect OR an HTML page OR (for an
// /authorize error with no usable redirect_uri) a direct JSON OAuth2 error.
// One flexible struct because /authorize legitimately spans all three. The
// dynamic Status field (Huma reads an int field named "Status") drives the code;
// empty Location/Body fields are simply omitted.
type BrowserOutput struct {
    Status      int
    Location    string `header:"Location"`
    ContentType string `header:"Content-Type"`
    SetCookie   string `header:"Set-Cookie"`
    Body        []byte // raw HTML or pre-serialized JSON; Huma writes []byte as-is
}

// (§5.3) HTML output: login page, form_post auto-submit, debugger, error page.
type HTMLOutput struct {
    ContentType string `header:"Content-Type"` // text/html; charset=utf-8
    Body        []byte
}

// (§5.4) Protocol JSON output: a success-shaped envelope returned (never as a Go
// error) so it bypasses Huma's global RFC 9457 error path. Body is a success DTO
// on 2xx or an OAuth2Error on 4xx/5xx; both marshal as application/json, and the
// handler sets Status. Concrete per-status schemas are stamped post-register.
type ProtocolJSON struct {
    Status   int
    WWWAuth  string `header:"WWW-Authenticate"`
    Body     any
}
```

`Body []byte` is written verbatim by Huma (huma.go:1156), so `BrowserOutput`/`HTMLOutput` give us byte-exact control of HTML and redirect bodies while staying inside the Huma middleware stack and the OpenAPI document. `ProtocolJSON.Body any` lets a single operation return either a success DTO or the error DTO at a handler-chosen status **without ever returning a Go `error`** (which would hit the RFC 9457 path the contract forbids reshaping). Every JSON protocol endpoint — discovery and JWKS included (N.5/N.6) — uses this one envelope, so the protocol surface has exactly **one** error contract.

**Schema-link transformer neutralized (Huma config decision).** Because the OIDC JSON surface returns through `ProtocolJSON.Body any` and the HTML/redirect surface through `[]byte`, Huma's default `SchemaLinkTransformer` — which would otherwise prepend a `$schema` property (at field index 0) and a `Link: <…>; rel="describedBy"` response header to any **concrete-struct** JSON body whose schema is a named `$ref` — never fires on the protocol surface. The discovery document and JWKS therefore emit *exactly* the declared fields in declared order, with no `$schema` injection that strict third-party OIDC client libraries would choke on. As belt-and-suspenders, `internal/adapter/http.NewAPI` strips that transformer from the `huma.DefaultConfig` it builds, so the property holds even if a concrete-struct JSON output is added later.

After registration, the form endpoints get their request-body schema hand-injected so the OpenAPI document is honest about the `application/x-www-form-urlencoded` shape Huma otherwise records only as opaque bytes. The lookup walks `OpenAPI.Paths` (there is no `Operations()` accessor in Huma v2), selects the `Post` operation, and **replaces** the `{type:string,format:binary}` schema Huma pre-populated for the `RawBody` field:

```go
// stampFormSchema attaches a typed url-encoded request-body schema to the POST
// operation at path, whose handler consumed RawBody, so the committed spec
// documents the real fields. Located via Paths (Huma exposes no Operations()
// map); huma already created the content key for the RawBody field, so we
// overwrite its opaque schema rather than create the entry. Mirrors the
// template's post-register OpenAPI stamping discipline.
func stampFormSchema(api huma.API, path string, props map[string]*huma.Schema) {
    item := api.OpenAPI().Paths[path]
    if item == nil || item.Post == nil {
        return
    }
    const ct = "application/x-www-form-urlencoded"
    item.Post.RequestBody.Content[ct].Schema = &huma.Schema{
        Type:       huma.TypeObject,
        Properties: props,
    }
}
```

The same `Paths`/method-field discipline backs `stampJSONResponse`, which documents the `DiscoveryDTO`/`JWKSDTO` 2xx body shape that `Body any` would otherwise erase from the spec.

### N.5 Representative handler — discovery (JSON + proxy-aware base URL)

Discovery is the simplest JSON op and the natural place to show **proxy-aware issuer URL derivation**, which is a new domain concern (`oidc.ResolveBaseURL`) fed by a `RequestOrigin` extracted at the transport edge — distinct from the kept IP-only `ClientIP` middleware.

The edge extraction is a Huma middleware (it needs `huma.Context`, which the plain handler signature does not expose):

```go
type originCtxKey struct{}

// requestOriginMiddleware captures the externally visible origin (scheme, host,
// port, and x-forwarded-* overrides) once per request and stashes the typed
// oidc.RequestOrigin on the context. This is the ONLY place transport header
// names live; the domain resolver consumes the typed value.
func requestOriginMiddleware(ctx huma.Context, next func(huma.Context)) {
    scheme := "http"
    if ctx.TLS() != nil { // HTTPS termination at this process (contract §C9)
        scheme = "https"
    }
    host, port := splitHostPort(ctx.Host())
    origin := oidc.RequestOrigin{
        Scheme:   scheme,
        Host:     host,
        Port:     port,
        FwdProto: ctx.Header("X-Forwarded-Proto"),
        FwdHost:  ctx.Header("X-Forwarded-Host"),
        FwdPort:  ctx.Header("X-Forwarded-Port"),
    }
    next(huma.WithValue(ctx, originCtxKey{}, origin))
}

func originFrom(ctx context.Context) oidc.RequestOrigin {
    o, _ := ctx.Value(originCtxKey{}).(oidc.RequestOrigin)
    return o
}
```

The handler is a thin adapter: parse the issuer, ask the domain service (which calls `ResolveBaseURL(origin)` internally — that resolver returns `(BaseURL, error)` because forwarded headers are client-controlled and malformable — and returns the fixed-field-order `oidc.DiscoveryDocument`), map to a DTO, done. On any failure it returns the **same** success-shaped OAuth2 envelope as the rest of the protocol surface, never a Go `error`:

```go
type DiscoveryInput struct {
    Issuer string `path:"issuer" doc:"Issuer id; the first path segment."`
}
func (i *DiscoveryInput) issuerID() string { return i.Issuer }

func (h *handlers) registerDiscovery(api huma.API) {
    register := func(path, id string) {
        huma.Register(api, huma.Operation{
            OperationID: id, Method: http.MethodGet, Path: path,
            Summary: "OIDC/OAuth2 provider metadata", Tags: []string{tagOIDC},
        }, h.discovery)
        stampJSONResponse(api, path, http.MethodGet, discoveryResponseSchema()) // doc the DTO
    }
    register("/{issuer}/.well-known/openid-configuration", "discovery-openid")
    register("/{issuer}/.well-known/oauth-authorization-server", "discovery-oauth")
}

func (h *handlers) discovery(ctx context.Context, in *DiscoveryInput) (*ProtocolJSON, error) {
    issuer, err := issuerOf(in)
    if err != nil {
        return protocolError(err), nil // uniform OAuth2 shape — never RFC 9457
    }
    doc, err := h.deps.Provider.Discovery(ctx, issuer, originFrom(ctx))
    if err != nil {
        return protocolError(err), nil
    }
    return &ProtocolJSON{Status: http.StatusOK, Body: toDiscoveryDTO(doc)}, nil
}
```

> Earlier drafts let discovery/jwks surface failures through Huma's normal `error` return (RFC 9457). That is now corrected: returning `(*ProtocolJSON, nil)` keeps **one** error vocabulary across the whole protocol surface (contract §6 family split), and the `Body any` envelope is also what keeps the schema-link transformer from polluting the document (N.4). In practice a non-reserved issuer is always materializable, so these never fail at runtime — the only failure is a malformed/reserved issuer caught at the parse edge.

The DTO encodes the **fixed serialization order** (contract §7 / catalog correction — `token_endpoint` is 5th) purely by Go struct field declaration order:

```go
// DiscoveryDTO serializes in declaration order, which is the contract's fixed
// field order. A unit test asserts json field order == this declaration order
// (and is unaffected by transport: encoding/json honors declaration order even
// when the value travels through ProtocolJSON.Body any).
type DiscoveryDTO struct {
    Issuer                 string   `json:"issuer"`
    AuthorizationEndpoint  string   `json:"authorization_endpoint"`
    EndSessionEndpoint     string   `json:"end_session_endpoint"`
    RevocationEndpoint     string   `json:"revocation_endpoint"`
    TokenEndpoint          string   `json:"token_endpoint"`
    UserinfoEndpoint       string   `json:"userinfo_endpoint"`
    JwksURI                string   `json:"jwks_uri"`
    IntrospectionEndpoint  string   `json:"introspection_endpoint"`
    ResponseTypesSupported []string `json:"response_types_supported"`
    ResponseModesSupported []string `json:"response_modes_supported"`
    SubjectTypesSupported  []string `json:"subject_types_supported"`
    IDTokenSigningAlgs     []string `json:"id_token_signing_alg_values_supported"`
    CodeChallengeMethods   []string `json:"code_challenge_methods_supported"`
}
```

> Cross-check discipline (contract §6): a unit test asserts `IDTokenSigningAlgs` (advertised) == the algorithms `signing.Signer` can actually produce, both sourced from the single `oidc.SupportedSigningAlgorithms()` accessor — the template's constant-sync pattern, here pinning *discovery advertises exactly what we can sign*.

### N.6 Representative handler — JWKS

```go
type JWKSInput struct{ Issuer string `path:"issuer"` }
func (i *JWKSInput) issuerID() string { return i.Issuer }

type JWKSDTO struct {
    Keys []JWKDTO `json:"keys"`
}
type JWKDTO struct {
    Kty string `json:"kty"`
    Use string `json:"use"`           // "sig"
    Kid string `json:"kid"`           // == issuer id
    Alg string `json:"alg"`
    N   string `json:"n,omitempty"`   // RSA modulus
    E   string `json:"e,omitempty"`   // RSA exponent
    Crv string `json:"crv,omitempty"` // EC curve
    X   string `json:"x,omitempty"`   // EC x
    Y   string `json:"y,omitempty"`   // EC y
}

func (h *handlers) jwks(ctx context.Context, in *JWKSInput) (*ProtocolJSON, error) {
    issuer, err := issuerOf(in)
    if err != nil {
        return protocolError(err), nil
    }
    // Domain port materializes the issuer's key on first reference (kid = issuer).
    set, err := h.deps.Provider.JWKS(ctx, issuer)
    if err != nil {
        return protocolError(err), nil
    }
    return &ProtocolJSON{Status: http.StatusOK, Body: toJWKSDTO(set)}, nil
}
```

`oidc.JWKS` carries only public metadata (no private material ever reaches the domain or the wire-out); `toJWKSDTO` is a pure field copy in `mapping.go`. Requesting `/jwks` forces key materialization for that issuer, matching upstream.

### N.7 Representative handler — token (form → grant dispatch → JSON | OAuth2 error)

This is the canonical §5(1)-in / §5(4)-out endpoint. The handler: parses raw form bytes with the **flat last-wins** parser, decodes into the typed `oidc.TokenRequest` (grant-specific fields), reads client auth off the `Authorization` header, calls `TokenService.Issue`, and serializes either the token DTO (200) or an `OAuth2Error` (4xx/5xx) — **never** returning a Go `error` for a protocol failure.

```go
type TokenInput struct {
    Issuer        string `path:"issuer"`
    Authorization string `header:"Authorization"` // Basic / Bearer / private_key_jwt carrier
    RawBody       []byte `contentType:"application/x-www-form-urlencoded"`
}
func (i *TokenInput) issuerID() string { return i.Issuer }

func (h *handlers) registerToken(api huma.API) {
    huma.Register(api, huma.Operation{
        OperationID: "token", Method: http.MethodPost, Path: "/{issuer}/token",
        Summary: "OAuth2 token endpoint", Tags: []string{tagOAuth},
    }, h.token)
    stampFormSchema(api, "/{issuer}/token", tokenFormSchema()) // grant_type, code, refresh_token, ...
}

func (h *handlers) token(ctx context.Context, in *TokenInput) (*ProtocolJSON, error) {
    issuer, err := issuerOf(in)
    if err != nil {
        return protocolError(err), nil
    }
    form, err := parseFormFlat(in.RawBody) // §N.8 flat parser; url.ParseQuery semantics
    if err != nil {
        return protocolError(oidc.MalformedRequest("could not parse form body")), nil
    }
    // Anti-corruption mapping: typed url-encoded form + header -> typed command.
    cmd, err := decodeTokenRequest(issuer, form, in.Authorization)
    if err != nil {
        return protocolError(err), nil // typed *ProtocolError: MissingParameter("grant_type"), ...
    }
    resp, err := h.deps.Tokens.Issue(ctx, originFrom(ctx), cmd)
    if err != nil {
        return protocolError(err), nil // InvalidGrant, InvalidClient, cross-issuer, ...
    }
    return &ProtocolJSON{Status: http.StatusOK, Body: toTokenResponseDTO(resp)}, nil
}

// protocolError builds the success-shaped error envelope (§5.4) from a domain
// error. It is the single edge adapter for ANY error on a protocol JSON route.
func protocolError(err error) *ProtocolJSON {
    status, body := oauth2Error(err)
    out := &ProtocolJSON{Status: status, Body: body}
    if body.Code == string(oidc.CodeInvalidToken) {
        out.WWWAuth = `Bearer error="invalid_token"`
    }
    return out
}
```

The grant union lives entirely in `mapping.go`, keeping the handler flat. The closed `oidc.GrantType` enum makes the dispatch exhaustive and the "unknown grant" path a typed error, not a stringly-typed default. Critically, `oidc.ParseGrantType` itself returns a typed `*oidc.ProtocolError` — **blank** `grant_type` → `MissingParameter("grant_type")` (`invalid_request`/400, "missing required parameter grant_type"); **unknown** → `UnsupportedGrant(s)` (`invalid_grant`/400, "grant_type <x> not supported.") — so these two catalog-asserted cases reach `oauth2Error` as `*ProtocolError`s and map to **400**, never collapsing to a 500 `server_error`:

```go
// decodeTokenRequest is the anti-corruption boundary for /token. It parses the
// closed grant set and only the fields that grant uses, producing a typed
// command. ParseGrantType yields the typed *ProtocolError for blank/unknown
// grant_type (no bare sentinel, no map[string]any), so the edge never 500s on
// the two most basic error cases.
func decodeTokenRequest(iss oidc.IssuerID, f FlatForm, authz string) (oidc.TokenRequest, error) {
    grant, err := oidc.ParseGrantType(f.Get("grant_type")) // "" -> MissingParameter; junk -> UnsupportedGrant
    if err != nil {
        return oidc.TokenRequest{}, err
    }
    client, err := decodeClientAuth(authz, f) // Basic header | client_id/secret post | assertion
    if err != nil {
        return oidc.TokenRequest{}, err
    }
    base := oidc.TokenRequest{Issuer: iss, Grant: grant, Client: client}

    switch grant {
    case oidc.GrantAuthorizationCode:
        return base.WithAuthorizationCode(
            oidc.AuthorizationCode(f.Get("code")),
            f.Get("redirect_uri"),
            decodeCodeVerifier(f), // optional; PKCE enforced only when present
        ), nil
    case oidc.GrantClientCredentials:
        return base.WithScopes(oidc.ParseScopes(f.Get("scope"))), nil
    case oidc.GrantPassword:
        return base.WithPassword(f.Get("username"), oidc.ParseScopes(f.Get("scope"))), nil
    case oidc.GrantRefreshToken:
        return base.WithRefreshToken(oidc.RefreshToken(f.Get("refresh_token"))), nil
    case oidc.GrantJWTBearer:
        return base.WithAssertion(f.Get("assertion"), oidc.ParseScopes(f.Get("scope")))
    case oidc.GrantTokenExchange:
        return base.WithSubjectToken(
            f.Get("subject_token"), f.Get("subject_token_type"), f.Get("audience"))
    default:
        // Unreachable: ParseGrantType already rejected unknown values.
        return oidc.TokenRequest{}, oidc.UnsupportedGrant(string(grant))
    }
}
```

`toTokenResponseDTO` emits the per-grant token matrix with `omitempty` so absent fields (e.g. no `refresh_token` for `client_credentials`) drop out, matching upstream's `NON_NULL`. `ExpiresIn` is copied from the domain `TokenResponse` (derived from the **same** `oidc.Clock` as `exp`) — the responder does *not* recompute it from a live wall clock, correcting upstream's frozen-clock drift (PRD P3 determinism: an advanced clock yields a consistent `exp`/`expires_in` pair):

```go
type TokenResponseDTO struct {
    TokenType       string `json:"token_type"`                  // always "Bearer"
    IssuedTokenType string `json:"issued_token_type,omitempty"` // token-exchange only
    IDToken         string `json:"id_token,omitempty"`
    AccessToken     string `json:"access_token"`
    RefreshToken    string `json:"refresh_token,omitempty"`
    ExpiresIn       int    `json:"expires_in"`                  // present even when 0; domain-computed
    Scope           string `json:"scope,omitempty"`
}
```

### N.8 Form parsing — `form.go` (two typed parsers, not unified)

The catalog is explicit that upstream uses **two** body parsers and that conflating them diverges. We keep them separate and *correct* (parity-in-intent): both build on `url.ParseQuery`, whose first-`=` split and empty-value handling already preserve intent without upstream's silent-truncation/drop-on-missing-`=` quirks.

```go
// FlatForm is the last-wins flat view (upstream keyValuesToMap('&')): duplicate
// keys collapse to the LAST value. Drives grant dispatch, revoke, introspect,
// and login. A distinct named type so a multi-valued url.Values can never be
// passed where flat semantics are required.
type FlatForm map[string]string

func (f FlatForm) Get(k string) string { return f[k] }
func (f FlatForm) Has(k string) bool   { _, ok := f[k]; return ok }

// parseFormFlat parses x-www-form-urlencoded bytes into the last-wins flat map.
func parseFormFlat(raw []byte) (FlatForm, error) {
    vals, err := url.ParseQuery(string(raw))
    if err != nil {
        return nil, err
    }
    out := make(FlatForm, len(vals))
    for k, vs := range vals {
        out[k] = vs[len(vs)-1] // last wins
    }
    return out, nil
}

// parseFormMulti preserves all values in order; used ONLY by request-mapping
// templating (RequestMappingCallback). It converts to the DOMAIN-owned
// oidc.FormParams at the edge so net/url.Values never crosses inward.
func parseFormMulti(raw []byte) (oidc.FormParams, error) {
    vals, err := url.ParseQuery(string(raw))
    if err != nil {
        return nil, err
    }
    return oidc.FormParams(vals), nil // oidc.FormParams is map[string][]string (domain type)
}
```

The request-mapping multi-valued path is the only consumer of `parseFormMulti`; everything else takes `FlatForm`. Mixing them is structurally impossible because they return different types. Note the deliberate edge conversion: `url.Values` is forbidden inside `internal/oidc`, so the adapter hands the in-domain `RequestMappingCallback` the **domain-owned** `oidc.FormParams` (declared in `oidc/callback.go`, with `Get`/`All`/`SpaceJoined` accessors), populated here — `url.Values` never travels inward.

### N.9 OAuth2 error mapping — `oautherr.go`

One function translates the closed domain error into the RFC 6749 §5.2 body. It is the single place a `*oidc.ProtocolError` becomes wire bytes; non-protocol errors collapse to `server_error`/500.

```go
// OAuth2Error is the RFC 6749 §5.2 error body. Returned as a success-shaped
// output value (never a Go error), so it bypasses Huma's RFC 9457 path. Text is
// emitted correct-case — upstream lowercases the whole body, mangling
// error_description; we do not replicate that defect (contract §6, D-2).
type OAuth2Error struct {
    Code        string `json:"error"`
    Description string `json:"error_description,omitempty"`
    URI         string `json:"error_uri,omitempty"`
}

// ContentType pins the body to application/json regardless of the Accept header.
func (OAuth2Error) ContentType(string) string { return "application/json" }

// oauth2Error maps a domain error to an HTTP status + RFC 6749 body. A
// *oidc.ProtocolError carries the mapped status and the exact client-visible
// description tests assert (e.g. the corrected cross-issuer refresh text). Any
// other error is an internal fault -> server_error / 500.
func oauth2Error(err error) (int, OAuth2Error) {
    var perr *oidc.ProtocolError
    if errors.As(err, &perr) {
        return perr.HTTPStatus, OAuth2Error{
            Code:        string(perr.Code),
            Description: perr.Description,
        }
    }
    return http.StatusInternalServerError, OAuth2Error{
        Code:        string(oidc.CodeServerError),
        Description: "unexpected error",
    }
}
```

`errors.As(err, &perr)` (pointer-to-pointer) matches the canonical `*oidc.ProtocolError` whose `Error()` has a pointer receiver and whose codes are the `Code*` constants (`oidc.CodeInvalidToken`, `oidc.CodeServerError`, `oidc.CodeNotFound`, …). The domain side supplies the exact texts tests pin (catalog corrections), e.g. `oidc.InvalidGrant("refresh_token was issued by a different issuer")` and `oidc.MissingParameter("grant_type") → "missing required parameter grant_type"`; the adapter neither invents nor reshapes them.

**`/authorize` error routing (contract §6).** `/authorize` is special: an error becomes a **redirect carrying `error`/`error_description`** when a usable `redirect_uri` is present, else a direct OAuth2 JSON error. The browser handler decides:

```go
// authorizeError renders an /authorize failure: redirect-with-error when a usable
// redirect_uri is known, else a direct RFC 6749 JSON body.
func authorizeError(redirectURI string, mode oidc.ResponseMode, err error) *BrowserOutput {
    status, body := oauth2Error(err)
    if redirectURI == "" {
        return &BrowserOutput{
            Status:      status,
            ContentType: "application/json",
            Body:        mustJSON(body),
        }
    }
    loc := appendError(redirectURI, mode, body) // query or fragment per response_mode
    return &BrowserOutput{Status: http.StatusFound, Location: loc}
}
```

**Router fallbacks without an inward import (contract §6 + hexagonal purity).** The kept router's non-Huma fallbacks (404/405/recover/timeout) must emit the **OAuth2 shape on protocol route families** and the **RFC 9457 shape on control/infra families**. Earlier drafts had `internal/adapter/http` itself import `httpapi` (`WriteOAuth2Error`/`isProtocolPath`) — but that would make the *generic, resource-agnostic* transport substrate depend on the OIDC-specific adapter (and risk an import cycle through the `Registrar` seam). Instead, `httpapi` **exports** the writer and a classifier, and the **composition root** injects them as a strategy via a kept-`RouterDeps` field (`FallbackWriter`, a KEEP delta); the router stays free of any `internal/oidc`/`httpapi` import:

```go
// httpapi exports the OAuth2 error writer and a protocol-path classifier so the
// COMPOSITION ROOT — not the generic transport — installs the protocol-vs-infra
// fallback. internal/adapter/http remains resource-agnostic.
func WriteOAuth2Error(w http.ResponseWriter, status int, code, desc string) {
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(status)
    _ = json.NewEncoder(w).Encode(OAuth2Error{Code: code, Description: desc})
}

// IsProtocolPath reports whether p is an OIDC protocol family route
// (/{issuer}/<known-endpoint>) — i.e. NOT control (/_mock/*) or infra.
func IsProtocolPath(p string) bool { /* classify by segment/known-endpoint suffix */ }
```

```go
// internal/app composition root wires the strategy onto the kept RouterDeps. The
// router's NotFound/MethodNotAllowed/recover/timeout handlers call deps.FallbackWriter;
// the router imports neither oidc nor httpapi.
deps.FallbackWriter = func(w http.ResponseWriter, r *http.Request, status int, code, desc string) {
    if httpapi.IsProtocolPath(r.URL.Path) {
        httpapi.WriteOAuth2Error(w, status, code, desc) // RFC 6749 on protocol families
        return
    }
    problem.Write(w, status, desc) // control (/_mock) + infra keep RFC 9457
}
```

`problem` is retained **solely** for the control/infra surface; the protocol surface never emits problem+json.

### N.10 Representative handler — authorize (interactive HTML vs 302 vs `form_post`)

`/authorize` GET demonstrates §5(2)+(3) in one operation. Its query params are **permissive strings** (no Huma `required`/min/max) so the *handler* — not the framework — decides whether a defect becomes a redirect-with-error or a direct error. The domain returns a typed `oidc.AuthorizeResult` with a 3-value kind set (`AuthorizeShowLogin`, `AuthorizeFormPost`, `AuthorizeRedirect`) and the raw typed fields (`Code`, `State`, `RedirectURI`, `ResponseMode`); **url-encoding the redirect stays at the edge** (the adapter renders query/fragment/form_post), so the domain never builds a transport URL:

```go
type AuthorizeInput struct {
    Issuer       string `path:"issuer"`
    ResponseType string `query:"response_type"` // permissive: no required/enum
    ClientID     string `query:"client_id"`
    RedirectURI  string `query:"redirect_uri"`
    Scope        string `query:"scope"`
    State        string `query:"state"`
    Nonce        string `query:"nonce"`
    ResponseMode string `query:"response_mode"`
    Prompt       string `query:"prompt"`
    CodeChallenge       string `query:"code_challenge"`
    CodeChallengeMethod string `query:"code_challenge_method"`
}
func (i *AuthorizeInput) issuerID() string { return i.Issuer }

func (h *handlers) authorize(ctx context.Context, in *AuthorizeInput) (*BrowserOutput, error) {
    issuer, err := issuerOf(in)
    if err != nil {
        return authorizeError(in.RedirectURI, oidc.ResponseModeQuery, err), nil
    }
    req, err := decodeAuthorizeRequest(issuer, in) // typed AuthorizeRequest
    if err != nil {
        return authorizeError(in.RedirectURI, req.ResponseMode, err), nil
    }

    // The domain decides: interactive login page, or issue a code now.
    result, err := h.deps.Authorize.Authorize(ctx, req)
    if err != nil {
        return authorizeError(in.RedirectURI, req.ResponseMode, err), nil
    }

    switch result.Kind {
    case oidc.AuthorizeShowLogin:
        // Render the built-in (or configured) login page; form posts back to the
        // same /authorize URL preserving the query string.
        return renderHTML(h.loginPage(req)), nil

    case oidc.AuthorizeFormPost:
        // Self-submitting HTML form POSTing code & state to redirect_uri. State is
        // omitted when absent (we do NOT replicate upstream's missing-state 500).
        return renderHTML(h.formPostPage(result)), nil

    default: // AuthorizeRedirect: code & state placed in query or fragment per mode
        return &BrowserOutput{
            Status:   http.StatusFound,
            Location: appendCode(result.RedirectURI, result.ResponseMode, result.Code, result.State),
        }, nil
    }
}

func renderHTML(body []byte) *BrowserOutput {
    return &BrowserOutput{
        Status:      http.StatusOK,
        ContentType: "text/html; charset=utf-8",
        Body:        body,
    }
}
```

`appendCode` (success) and `appendError` (N.9) are the symmetric edge builders — the **only** owners of redirect-URL construction, so the domain never emits a pre-built `RedirectLocation`. HTML is rendered from embedded `html/template` assets in `httpapi/html/` (login, `form_post`, debugger, error). The templates are parsed once at package init from an `embed.FS`; CSS is inlined (no Google-Fonts network dependency — a DX correction over upstream). The `POST /authorize` login submit (`login.go`) shares `BrowserOutput`: it parses `username` (required → `MissingParameter`) and optional `claims` JSON into `oidc.LoginSubmission` with the **flat** parser, calls `AuthorizeService.SubmitLogin`, and returns the resulting redirect/form_post.

### N.11 Cross-cutting concerns (reusing template middleware)

| Concern | Mechanism | Reuse / new |
|---|---|---|
| **CORS** | Upstream's `CorsInterceptor` is zero-config and applies to **all** routes: with an `Origin` header it *reflects* that exact origin (not `*`), sets `Access-Control-Allow-Credentials: true`, answers preflight with `Allow-Methods: POST, GET, OPTIONS`, echoes `Access-Control-Request-Headers`, and returns `204` for any-path `OPTIONS`. To preserve zero-config browser/SPA support (PRD C9, scenario S3), CORS is **default ON** with those semantics: the kept `middleware.CORS` is configured with an `AllowOriginFunc` that returns true (reflect + credentials, which a static `*` allowlist cannot legally combine with credentials), the protocol verbs in `Allow-Methods`, and request-header echo. `CORSAllowedOrigins` becomes an *optional tightening override*, not the on/off switch. The adapter adds no CORS logic of its own. | **Reuse** `internal/adapter/http/middleware/cors.go`, configured for reflect-origin-with-credentials by default (not the as-is allowlist default). |
| **Proxy-aware issuer URL** | `requestOriginMiddleware` (N.5) builds the typed `oidc.RequestOrigin`; the domain `oidc.ResolveBaseURL` applies the precedence `x-forwarded-proto > scheme`, `Host` host > original, `x-forwarded-port > Host port > scheme default`, returning `(BaseURL, error)`. Every `iss` and endpoint URL in discovery derives from it. | **New** (adapter middleware + domain resolver). Distinct from the kept IP-only `ClientIP` middleware, which is unchanged. |
| **Request capture** | The request-recording middleware lives **in `httpapi`** (it references `oidc.RequestRecorder`), installed by the composition root as a *mux-level* chi middleware that buffers+restores `r.Body` (so the `RawBody` resolver still sees the stream) and **early-returns for `/_mock/*`, `/healthz`, `/readyz`, `/metrics`, `/openapi*`, `/docs`** — recording only the OIDC families. It converts transport types to stdlib primitives **at the edge** (`r.URL.String()`, an explicit `map[string][]string` copy of `r.Header`) before calling `oidc.NewCapturedRequest`, so the core never imports `net/http`/`net/url`. | **New** httpapi middleware (never in `adapter/http`). |
| **HTTPS / TLS** | TLS terminates at the listener (the kept `internal/app` dual-server). `ctx.TLS() != nil` flips the derived scheme to `https` so discovery advertises `https://…`. The adapter does not own certificates. | **Reuse** transport; adapter only reads `TLS()`. |
| **Client IP** | Unchanged `middleware.ClientIP`; protocol handlers never need it (no secret validation), but it keeps request logging/metrics consistent. | **Reuse**, untouched. |
| **Timeout / recover / request-id / access log / metrics** | Unchanged middleware order from `router.go`; protocol ops inherit them because they are Huma operations on the same mux. | **Reuse**, untouched. |

### N.12 OpenAPI security schemes (post-register stamping)

The contract removes the authz `FinalizeAuthz` hook but keeps its *pattern*: declare the `oauth2`/`openIdConnect` schemes in `Components.SecuritySchemes` after the operations exist (metadata-only mutation, safe post-register). This makes the committed spec advertise the flows the server actually serves, without implying the mock *enforces* them (it never authenticates its own callers):

```go
// stampSecuritySchemes documents the served flows in the OpenAPI Components.
// It is descriptive only: mock-oidc is the auth provider and never authenticates
// its own callers, so no per-operation security requirements are attached.
func stampSecuritySchemes(api huma.API) {
    comps := api.OpenAPI().Components
    if comps.SecuritySchemes == nil {
        comps.SecuritySchemes = map[string]*huma.SecurityScheme{}
    }
    comps.SecuritySchemes["oauth2"] = &huma.SecurityScheme{
        Type: "oauth2",
        Flows: &huma.OAuthFlows{
            AuthorizationCode: &huma.OAuthFlow{
                AuthorizationURL: "/{issuer}/authorize",
                TokenURL:         "/{issuer}/token",
                Scopes:           map[string]string{"openid": "OpenID Connect"},
            },
            ClientCredentials: &huma.OAuthFlow{TokenURL: "/{issuer}/token"},
        },
    }
    comps.SecuritySchemes["openIdConnect"] = &huma.SecurityScheme{
        Type:             "openIdConnect",
        OpenIDConnectURL: "/{issuer}/.well-known/openid-configuration",
    }
}
```

The same `Register`/`stampSecuritySchemes` pair feeds `SpecYAML` (the server-less OpenAPI export), so the committed spec and the running server stay in lock-step — the template's existing discipline, repurposed from authz to protocol-flow documentation.

---

**Files produced by this section:** `internal/oidc/httpapi/{register.go, discovery.go, jwks.go, authorize.go, login.go, token.go, userinfo.go, introspect.go, revoke.go, endsession.go, debugger.go, favicon.go, static.go, dto.go, mapping.go, form.go, oautherr.go}` plus embedded assets under `internal/oidc/httpapi/html/`. The package imports `internal/oidc`, `huma`, `net/http`, `net/url`, `html/template`, `encoding/json`, `embed` — and nothing from `signing`, `memory`, or any sibling adapter (they meet only at the `internal/app` composition root). It exports `WriteOAuth2Error`/`IsProtocolPath` and the request-recording middleware for the composition root to wire; `internal/adapter/http` imports none of it.

## Test-Time Control & Inspection Surface (`internal/oidc/controlapi`)

> Implements PRD **C6** (test-time control & inspection) and **G6/S4** under decision **D-1** (container-first, no in-process library) and **N6** (no embeddable library for parity). This is the novel piece: upstream gives tests these powers as JVM method calls on an in-process `MockOAuth2Server` (`issueToken`, `anyToken`, `enqueueCallback`, `takeRequest`, and `systemTime` for the clock). We have *no* in-process object to call — the container is the only thing a test can touch — so every one of those powers is re-exposed as a small, strongly-typed HTTP control API on the running container. Per the binding contract (§2, §4, §6, §8) it is the `internal/oidc/controlapi` package, a Huma RFC 9457 JSON surface mounted under the reserved `/_mock/` prefix.

### C.1 Mapping upstream's in-process powers to a control API

The catalog's "Test-Library API & Request Capture" section is the parity spec. Each in-process affordance maps to exactly one control capability, driving the same domain ports and use-cases the protocol surface uses — so a token minted out-of-band is byte-for-byte identical to one issued through `/token`, and an enqueued scenario alters *real* HTTP responses.

| Upstream in-process API (JVM) | What it does | Control capability | Drives (domain / port) |
|---|---|---|---|
| `issueToken(issuer, clientId, callback)` / `issueToken(issuer, subject, audience, claims, expiry)` | In-process mint, no HTTP, signs as-if-`authorization_code` | `POST /_mock/mint` | `TokenService.Mint` → `Signer`, `KeyStore`, `Clock`, `IssuerRegistry` |
| `anyToken(issuerUrl, claims, expiry)` | Sign a token for an **arbitrary external** `iss` with this server's keys | same `POST /_mock/mint` with `issuerUrl` override | `TokenService.Mint` (explicit `BaseURL`) |
| `enqueueCallback(callback)` | One-shot, issuer-matched, single-use callback consumed by the next matching token request (incl. refresh) | `POST /_mock/scenarios` (+ `GET`, `DELETE`) | `ScenarioStore.Enqueue` → same `oidc.CallbackQueue` the `TokenService` polls via `DequeueFor` |
| `takeRequest(timeout)` | Destructive FIFO pull of a recorded inbound request; **raw body bytes preserved** | `POST /_mock/requests/take` (+ non-destructive `GET /_mock/requests`, `DELETE`) | `RequestLog.Take/List/Clear` → same `oidc.RequestRecorder` the OIDC edge writes |
| `OAuth2TokenProvider(systemTime=…)` (startup only) | Freeze the global clock for deterministic `iat`/`nbf`/`exp` | `PUT /_mock/clock`, `POST /_mock/clock/advance`, `GET /_mock/clock` | `ClockController.Freeze/Advance` → the mutable `Clock` adapter |
| (none — JVM `@AfterEach`) | Reset state between cases | `POST /_mock/reset` | all of the above, atomically |

Two distinctions from upstream that the design makes explicit:

- **`mint` is out-of-band; `scenarios` is in-band.** `mint` returns a signed artifact directly and touches no queue (it is `issueToken`/`anyToken`). Enqueuing a `scenario` does *not* return a token — it changes how the *next real* `/{issuer}/token` (or refresh) request responds (it is `enqueueCallback`). Conflating them is the most common way to misread upstream; the API keeps them on separate routes with separate DTOs.
- **Capture is widened, parity-in-intent (D-2).** Upstream's `takeRequest` is a single destructive FIFO with a 2 s blocking timeout and a thrown exception on miss — fine for one assertion, awkward for "assert the app sent *these three* requests." We keep the destructive FIFO `take` (the literal parity path) but add a non-destructive `GET /_mock/requests` log with filters, because S4 ("verify what the app sends") is precisely the use-case upstream under-serves. Raw body bytes are preserved in both (the catalog calls this out — param order matters).

### C.2 Route, listener, and safe positioning

The control surface is strictly more powerful than the OIDC surface (it mints arbitrary identities and reads every client's captured traffic), so it is separated three ways and is opt-out-able:

1. **Reserved path prefix `/_mock/` (contract §8).** All control operations live under `/_mock/…`. `IssuerID`'s smart constructor (`ParseIssuerID`) rejects any value beginning with `_mock`, so no zero-config issuer can shadow the control plane, and chi's radix router matches the static `/_mock/*` segment ahead of the dynamic `/{issuer}/*` param — defense in depth on both the routing and the parsing side.
2. **Disable-able (default on).** `--control-enabled` (default `true`, env `MOCK_OIDC_CONTROL_ENABLED`). The product is a for-testing-only server (N1/N3/C10) and the primary persona is the test author (A2: "in a few lines"), so the control plane is *on by default* for the one-mapped-port Testcontainers case. A shared/long-lived deployment sets it `false`, and the `/_mock` group is simply never registered — the routes return the normal OIDC `404`.
3. **Relocatable to a dedicated listener (hardening).** `--control-addr` (default `""`, env `MOCK_OIDC_CONTROL_ADDR`). Empty co-locates `/_mock` on the main API listener (simplest; one exposed port). Non-empty binds a **third `http.Server`** — mirroring the existing dedicated `--metrics-addr` listener — which an operator can bind to loopback or a private network and firewall off, while the OIDC surface stays public.
4. **Optional shared-secret gate.** `--control-token` (default `""`). When set, every `/_mock/*` request must carry `X-Mock-Control-Token: <token>` or gets `401`. Default-off matches upstream (which had no auth — the powers were in-process); on is for shared environments. This is the *only* place mock-oidc ever authenticates a caller, and it guards the control plane, not the OIDC protocol.

The OIDC server already advertises "for testing only" loudly (C10); the control plane adds a `Warning`-level startup log line listing the enabled control routes and stamps `X-Mock-OIDC: testing-only` on control responses so a misdirected production probe is unmistakable.

### C.3 Package shape and the hexagonal seam

`controlapi` is an **inbound adapter** (same tier as `httpapi`): it depends inward on `internal/oidc` and on `huma`/`net/http`, never on another adapter (§3). It declares its *own* consumer-side ports — narrow facets of the same in-memory objects the OIDC core uses — so the control reader and the domain writer never share a fat interface (ISP + consumer-declared ports). One `memory.*` object structurally satisfies several narrow ports; the composition root wires them.

```
internal/oidc/controlapi/
  register.go    Register(api huma.API, deps Deps) — mounts the /_mock operations (relative paths under the group).
  deps.go        Deps + the consumer-declared control ports (ScenarioStore, RequestLog, ClockController).
  mint.go        POST /_mock/mint            -> TokenService.Mint
  scenarios.go   POST/GET/DELETE /_mock/scenarios
  requests.go    POST /_mock/requests/take, GET/DELETE /_mock/requests
  clock.go       GET/PUT /_mock/clock, POST /_mock/clock/advance
  reset.go       POST /_mock/reset
  dto.go         Wire DTOs (the only place map[string]any for claims is allowed).
  mapping.go     DTO<->domain mapping (anti-corruption boundary).
  errors.go      toControlError: typed domain error -> RFC 9457 (huma.Error*).
```

The narrow ports are intentionally split across the read/write directions, and they are hosted **in the domain** (`oidc`) only so two non-cooperating adapters can share one backing object without importing each other (§3 forbids adapter→adapter coupling) — not because the core consumes them all. Concretely: the same `memory.CallbackQueue` is the domain's read-side `oidc.CallbackQueue` (the *only* consumer is `TokenService` via `DequeueFor`) *and* the control's write-side `ScenarioStore` (the only enqueuer); the same `memory.RequestRecorder` is the OIDC edge's write-only `oidc.RequestRecorder` (`Record` only — no core service consumes it; it is written by the httpapi capture middleware) *and* the control's read-side `RequestLog`:

```go
// Package controlapi is the inbound test-time control plane: a small RFC 9457
// JSON API, mounted under the reserved /_mock prefix, that gives container-based
// tests the powers the upstream in-process library exposed as method calls —
// direct token mint, one-shot scenario enqueue, captured-request inspection, and
// clock control. It depends inward on internal/oidc and declares the narrow
// control-side ports it needs; the composition root satisfies them with the same
// in-memory adapters the OIDC core uses.
package controlapi

// Prefix is the reserved path prefix the control plane mounts under. Operations
// register RELATIVE paths (/mint, /scenarios, …) inside huma.NewGroup(api, Prefix),
// so the group yields /_mock/mint, /_mock/scenarios, … with no double-prefix.
const Prefix = "/_mock"

// ScenarioStore is the control-plane write/inspect view of the one-shot callback
// queue. The OIDC core consumes the same backing store through the read-side
// oidc.CallbackQueue port (DequeueFor only); this facet is the only enqueuer.
type ScenarioStore interface {
	Enqueue(s oidc.Scenario) (oidc.ScenarioID, error)
	List() []oidc.Scenario
	Clear()
}

// RequestLog is the control-plane read view of the recorder. The OIDC edge writes
// through the narrower oidc.RequestRecorder (Record only); this facet drains it.
type RequestLog interface {
	List(filter oidc.CaptureFilter) []oidc.CapturedRequest
	// Take dequeues the oldest matching request (FIFO), blocking up to timeout for
	// one to arrive; ok is false on timeout. This is the takeRequest equivalent.
	Take(ctx context.Context, filter oidc.CaptureFilter, timeout time.Duration) (oidc.CapturedRequest, bool)
	Clear()
}

// ClockController is the control-plane write view of the mutable clock. The OIDC
// core reads the same clock through the oidc.Clock port (Now only), and so does
// the /userinfo + /introspect verifier — one clock, both issuance and verification.
type ClockController interface {
	Freeze(at oidc.Instant)
	Unfreeze()
	Advance(d time.Duration)
	State() oidc.ClockState // { Frozen bool; Now oidc.Instant }
}

// Deps are the collaborators the control operations drive. Tokens are minted
// through the very same application service the /token endpoint uses, so a minted
// token is indistinguishable from a granted one and verifies against /jwks.
type Deps struct {
	Tokens    *oidc.TokenService // Mint(ctx, oidc.MintSpec) (SignedToken, ClaimSet, error)
	Scenarios ScenarioStore
	Requests  RequestLog
	Clock     ClockController
}
```

`memory.CallbackQueue`, `memory.RequestRecorder`, and `memory.Clock` each implement *both* their domain-facing `oidc.*` port and their control-facing facet here — Go's structural typing means `memory` imports neither `oidc`'s consumer nor `controlapi`; the composition root is the only place the concrete types are named (§3 edges).

### C.4 Endpoint: direct mint (`issueToken` + `anyToken`)

Folds both upstream overloads into one operation. If `issuerUrl` is supplied it is `anyToken` (sign for an arbitrary external `iss`); otherwise the `iss` is resolved proxy-aware from the control request via the same `ResolveBaseURL(RequestOrigin)` the OIDC edge uses (correct for the co-located Testcontainers case, where the test's `Host` header *is* the externally-visible address). The mint goes through `TokenService.Mint`, which materializes the issuer's signing key via `KeyStore` (`kid = issuer`) — so the minted token verifies at `/{issuer}/jwks`, `/userinfo`, and `/introspect`, which is the entire point of `issueToken`.

```go
// dto.go
type MintTokenInput struct {
	// Forwarding headers feed proxy-aware iss resolution (parity with the OIDC edge).
	Host     string `header:"Host"`
	FwdProto string `header:"X-Forwarded-Proto"`
	FwdHost  string `header:"X-Forwarded-Host"`
	FwdPort  string `header:"X-Forwarded-Port"`
	Body     MintRequestDTO
}

type MintRequestDTO struct {
	Issuer    string         `json:"issuer"            default:"default" doc:"Issuer id (first path segment). Reserved name '_mock' is rejected."`
	IssuerURL string         `json:"issuerUrl,omitempty"                 doc:"Override the iss with an arbitrary URL (the anyToken case). When set, proxy resolution is skipped."`
	Subject   string         `json:"subject,omitempty"                   doc:"sub claim; defaults to a random UUID."`
	Audience  []string       `json:"audience,omitempty"                  doc:"aud claim. Omitted -> default audience rules apply."`
	Scope     []string       `json:"scope,omitempty"`
	ClientID  string         `json:"clientId,omitempty" default:"default"`
	Kind      string         `json:"kind,omitempty"     enum:"access_token,id_token" default:"access_token"`
	Typ       string         `json:"typ,omitempty"      default:"JWT"     doc:"JWS typ header (an open JOSEType; e.g. at+jwt is accepted). Note: a non-JWT typ will fail this server's own /userinfo and /introspect verification (parity gotcha)."`
	Claims    map[string]any `json:"claims,omitempty"                    doc:"Additional/overriding claims. This DTO is the only place untyped claim data exists; it is parsed into a typed oidc.ClaimSet at the edge."`
	ExpirySec *int           `json:"expirySeconds,omitempty" default:"3600"`
}

type MintTokenOutput struct {
	Body struct {
		Token     string         `json:"token"     doc:"Compact signed JWT."`
		Kid       string         `json:"kid"`
		Algorithm string         `json:"algorithm"`
		Issuer    string         `json:"issuer"    doc:"Resolved iss."`
		ExpiresAt time.Time      `json:"expiresAt"`
		Claims    map[string]any `json:"claims"    doc:"Decoded claim set, for convenience (order-insignificant; the authoritative ordered claims live in the signed token)."`
	}
}
```

```go
// mint.go
func (h *handlers) mint(ctx context.Context, in *MintTokenInput) (*MintTokenOutput, error) {
	spec, err := toMintSpec(in) // mapping.go: parse-don't-validate into typed domain values
	if err != nil {
		return nil, toControlError(err)
	}

	signed, claims, err := h.deps.Tokens.Mint(ctx, spec)
	if err != nil {
		return nil, toControlError(err)
	}

	out := &MintTokenOutput{}
	out.Body.Token = string(signed)
	out.Body.Kid = string(spec.Issuer) // kid == issuer id
	out.Body.Algorithm = string(spec.Algorithm)
	out.Body.Issuer = spec.BaseURL.IssuerURL(spec.Issuer) // IssuerURL returns string; no .String()
	out.Body.ExpiresAt = claims.ExpiresAt().Time()
	out.Body.Claims = orderedClaimsMap(claims) // mapping.go: ranges ClaimSet's ordered accessor into the wire map
	return out, nil
}
```

`toMintSpec` is the anti-corruption boundary: `oidc.ParseIssuerID` (rejects `_mock`), `oidc.NewSubject`, `oidc.ParseScopes`, `oidc.NewClaimSet(in.Body.Claims)` (the lone `map[string]any` → typed `ClaimSet` crossing), `oidc.ParseJOSEType(in.Body.Typ)` (open type — never rejects `at+jwt`), and either `oidc.ResolveBaseURL(origin)` (which may error on malformed forwarded headers, so the mapping handles `(BaseURL, error)`) or `oidc.ParseBaseURL(in.Body.IssuerURL)`. There is **no** `ToOrderedMap()` domain method returning a map (a Go map cannot preserve insertion order, and `encoding/json` re-sorts its keys); `orderedClaimsMap` ranges `claims.Ordered()` (`[]oidc.ClaimEntry`) at the edge into the convenience wire map, while the authoritative ordered emission lives inside the signed `token`. The domain command and `MintKind` are additive value types in the `oidc` core consistent with the closed-enum idiom (§7):

```go
// internal/oidc — additive mint use-case input; reuses fixed glossary types.
type MintKind string
const (
	MintAccessToken MintKind = "access_token"
	MintIDToken     MintKind = "id_token"
)

type MintSpec struct {
	Issuer    IssuerID
	BaseURL   BaseURL          // resolved iss base (host root)
	Subject   Subject
	Audience  Audience         // named type (== []string); distinguishes unset vs explicitly-empty
	Scopes    Scopes
	Claims    ClaimSet
	ClientID  ClientID
	Kind      MintKind
	Typ       JOSEType         // open JWS typ header (default JWT; e.g. at+jwt), NOT the closed TokenType enum
	Algorithm SigningAlgorithm
	Expiry    time.Duration
}
```

### C.5 Endpoint: enqueue scenario (`enqueueCallback`)

The DTO is the declarative `TokenCallback` description — and it is **the same shape the JSON-config `tokenCallbacks` parser produces** (contract §4 ADD). `mapping.go` reuses the *same* domain callback constructors (`oidc.NewDefaultTokenCallback` / `oidc.NewRequestMappingCallback`) the config parser calls, so there is one anti-corruption mapping for "a callback described as JSON," whether it arrives at startup (config) or at runtime (control). When `requestMappings` is present the mapping yields a `RequestMappingTokenCallback`; otherwise a `DefaultTokenCallback`.

```go
type ScenarioDTO struct {
	Issuer          string              `json:"issuer" default:"default"`
	Subject         string              `json:"subject,omitempty"`
	Audience        []string            `json:"audience,omitempty"`
	Claims          map[string]any      `json:"claims,omitempty"`
	Typ             string              `json:"typ,omitempty"`
	ExpirySeconds   *int                `json:"expirySeconds,omitempty"`
	RequestMappings []RequestMappingDTO `json:"requestMappings,omitempty"`
}

func (h *handlers) enqueue(ctx context.Context, in *EnqueueScenarioInput) (*EnqueueScenarioOutput, error) {
	cb, err := toTokenCallback(in.Body) // shared with config parser
	if err != nil {
		return nil, toControlError(err)
	}
	scenario, err := oidc.NewScenario(cb) // one-shot, issuer-matched, single-use
	if err != nil {
		return nil, toControlError(err)
	}
	id, err := h.deps.Scenarios.Enqueue(scenario)
	if err != nil {
		return nil, toControlError(err)
	}
	out := &EnqueueScenarioOutput{}
	out.Body.ScenarioID = string(id)
	out.Body.QueueDepth = len(h.deps.Scenarios.List())
	return out, nil
}
```

`GET /_mock/scenarios` returns the pending queue (depth + decoded callbacks) for debugging; `DELETE /_mock/scenarios` flushes it. Critically, this is the *same* `oidc.CallbackQueue` the `TokenService` consults (via `DequeueFor`) during grant resolution (priority: enqueued issuer-matched head > configured `RequestMappingTokenCallback` > `DefaultTokenCallback`, and the refresh grant consults it too — catalog "Token-callback queue"), so an enqueued scenario changes the next real `/{issuer}/token` response. The queue is mutex-guarded and **peeks the head, polling only on issuer match** — preserving upstream's "a queued callback for issuer A blocks issuer B even if B arrives first" semantic.

### C.6 Endpoint: captured-request inspection (`takeRequest`) and the recording seam

**Recording seam.** Every inbound *OIDC* request is recorded through the write-only `oidc.RequestRecorder` port. Recording is a transport-edge concern, so it lives in `internal/oidc/httpapi` (it names `oidc.RequestRecorder` and `oidc.NewCapturedRequest`, so it must **not** sit in the generic, resource-agnostic `internal/adapter/http` substrate — §3). Because a co-located `huma.NewGroup(api, "/_mock")` is only a *logical* path-prefix over the **same** underlying chi mux (it is not a separate router), there is no physical OIDC subtree to attach the middleware to; instead `RecordRequests` is a single mux-level chi middleware that **path-guards** to the protocol families — it early-returns for `/_mock/*`, `/healthz`, `/readyz`, `/metrics`, `/openapi*`, and `/docs`, so the control plane never records itself. (Only the dedicated-listener deployment — `ControlAddr != ""` — gives true *physical* separation; co-located, this prefix guard is the separation.) It buffers the body (these are small form/JSON bodies), restores it for the handler, and records raw bytes + method + URL + headers + query — preserving param order exactly as the catalog requires.

Crucially, the middleware does the transport→domain conversion **at the edge**: it passes only stdlib-primitive/domain types to the core constructor (`r.URL.String()`, an explicit `map[string][]string(r.Header)` copy, `r.URL.Query()` as `map[string][]string`, and `body []byte`). `NewCapturedRequest` never names `*url.URL` or `http.Header`, so `capture.go` (in `internal/oidc`) stays free of `net/url`/`net/http` (contract §3; the arch test lists both as forbidden for that file):

```go
// internal/oidc — capture.go: domain constructor takes only stdlib primitives.
//   func NewCapturedRequest(method, rawURL string,
//       header, query map[string][]string, body []byte) CapturedRequest
```

```go
// internal/oidc/httpapi — recording middleware, mux-level, OIDC families only.
func RecordRequests(rec oidc.RequestRecorder) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !isOIDCPath(r.URL.Path) { // skips /_mock, /healthz, /readyz, /metrics, /openapi*, /docs
				next.ServeHTTP(w, r)
				return
			}
			body, _ := io.ReadAll(io.LimitReader(r.Body, maxCaptureBytes))
			_ = r.Body.Close()
			r.Body = io.NopCloser(bytes.NewReader(body)) // hand the handler an intact body

			// Convert transport types to stdlib primitives at the edge so the domain
			// constructor never imports net/url / net/http (contract §3).
			rec.Record(r.Context(), oidc.NewCapturedRequest(
				r.Method,
				r.URL.String(),
				map[string][]string(r.Header),
				map[string][]string(r.URL.Query()),
				body,
			))
			next.ServeHTTP(w, r)
		})
	}
}
```

**Read side.** `POST /_mock/requests/take` is the destructive FIFO `takeRequest` (long-polls up to `timeoutMs` for one to arrive, with optional `issuer`/`endpoint` filter); `GET /_mock/requests` is the non-destructive log; `DELETE /_mock/requests` clears it.

```go
type TakeRequestInput struct {
	Body struct {
		TimeoutMs int    `json:"timeoutMs,omitempty" default:"1000" doc:"Max time to wait for a matching request."`
		Issuer    string `json:"issuer,omitempty"`
		Endpoint  string `json:"endpoint,omitempty" enum:"authorize,token,userinfo,introspect,revoke,endsession,jwks"`
	}
}

type CapturedRequestDTO struct {
	ID         string              `json:"id"`
	ReceivedAt time.Time           `json:"receivedAt"`
	Issuer     string              `json:"issuer"`
	Method     string              `json:"method"`
	Path       string              `json:"path"`
	URL        string              `json:"url"`
	Query      map[string][]string `json:"query,omitempty"`
	Headers    map[string][]string `json:"headers,omitempty"`
	BodyBase64 string              `json:"bodyBase64,omitempty" doc:"Raw request body bytes, base64; preserves exact param order."`
	Body       string              `json:"body,omitempty"       doc:"Best-effort UTF-8 decode of the body, for convenience."`
}

func (h *handlers) take(ctx context.Context, in *TakeRequestInput) (*TakeRequestOutput, error) {
	filter := toCaptureFilter(in.Body.Issuer, in.Body.Endpoint)
	timeout := time.Duration(in.Body.TimeoutMs) * time.Millisecond
	rec, ok := h.deps.Requests.Take(ctx, filter, timeout)
	if !ok {
		// Upstream throws on timeout; we return a clean 404 (RFC 9457).
		return nil, huma.Error404NotFound("no captured request matched within the timeout")
	}
	return &TakeRequestOutput{Body: toCapturedRequestDTO(rec)}, nil
}
```

The blocking `Take` uses `ctx` (the Huma request context, already bounded by the server's request timeout) plus the recorder's internal signal so a missed request doesn't pin a goroutine.

### C.7 Endpoint: clock control (`systemTime`, made dynamic)

Upstream only freezes the clock at startup via `systemTime`. We keep that config seed (the `Clock` is seeded from `config`) and add runtime control. The clock is **global** (catalog: "A single global instant drives both issuance and verification"), so freezing affects every issuer's `iat`/`nbf`/`exp` and the `/userinfo`/`/introspect` verifier alike — exactly upstream's behavior, and the reason the `TokenVerifier` port threads the same `now Instant` the issuance path uses (f5: one clock drives both). The mutable `memory.Clock` satisfies both `oidc.Clock` (domain read) and `controlapi.ClockController` (control write); the immutable `oidc.FixedClock`/`SystemClock` value types are reserved for unit-test seams, never wired into the running server.

```go
type ClockStateDTO struct {
	Frozen bool      `json:"frozen"`
	Now    time.Time `json:"now"`
}
type SetClockInput struct {
	Body struct {
		Frozen  bool       `json:"frozen"`
		Instant *time.Time `json:"instant,omitempty" doc:"Required when frozen=true; the fixed 'now'."`
	}
}
type AdvanceClockInput struct {
	Body struct {
		Duration string `json:"duration" doc:"Go duration, e.g. '90s', '5m', '1h'. Advances the frozen clock."`
	}
}
```

`PUT /_mock/clock` with `frozen:true` calls `ClockController.Freeze(oidc.NewInstant(instant))`; `frozen:false` calls `Unfreeze()`. `POST /_mock/clock/advance` parses the duration and calls `Advance(d)` (the deterministic way to push a token past `exp` for S2's expiry tests). `GET /_mock/clock` returns the state. Per the design's parity-in-intent correction (PRD P3 determinism), `expires_in` in `/token` responses derives from the **same** authoritative `Clock` as `exp` — we do **not** replicate upstream's defect of recomputing `expires_in` from real `Instant.now()` — so freezing or advancing the clock yields a consistent `exp`/`expires_in` pair. That `expires_in`-vs-`exp` consistency lives in the `httpapi` token responder; the control clock here only moves the one authoritative `Clock` that both issuance and verification read.

### C.8 Reset and error model

`POST /_mock/reset` atomically `Clear()`s the scenario queue and request log and `Unfreeze()`s the clock — the per-test-case hygiene `@AfterEach` that lets a single reused container serve many cases (A3). It deliberately does **not** drop materialized issuer signing keys: keys are stable for the process lifetime (catalog: `kid=issuerId`, `computeIfAbsent`), and dropping them would invalidate JWKS a client already fetched.

Errors follow contract §6-B: the control plane is *our own* JSON API and keeps Huma's default `application/problem+json` (RFC 9457) — it does **not** use the OAuth2 error shape (that is reserved for the protocol surface). `toControlError` is the single translator. Because the domain unifies issuer/parse failures on one constructor that returns a `*oidc.ProtocolError` (per the cross-section error model — `ParseIssuerID("_mock")` yields a `CodeNotFound`/`404` `*ProtocolError` wrapping the reserved-issuer cause), the translator extracts it with `errors.As` and maps its `HTTPStatus` straight into a problem, rather than matching a per-condition sentinel:

```go
// errors.go
func toControlError(err error) error {
	// Domain protocol errors carry their own closed ErrorCode + HTTPStatus; map that
	// status into Huma's default problem+json (never the OAuth2 shape — §6-B).
	// A reserved issuer ("_mock") surfaces here as the unified ParseIssuerID
	// CodeNotFound/404 ProtocolError.
	var perr *oidc.ProtocolError
	if errors.As(err, &perr) {
		return huma.NewError(perr.HTTPStatus, perr.Description, perr)
	}
	switch {
	case errors.Is(err, oidc.ErrUnsupportedAlgorithm):
		return huma.Error422UnprocessableEntity("unsupported signing algorithm", err)
	case errors.Is(err, oidc.ErrInvalidInstant), errors.Is(err, oidc.ErrInvalidDuration):
		return huma.Error422UnprocessableEntity("invalid time value", err)
	default:
		return huma.Error500InternalServerError("internal control error")
	}
}
```

The router's non-Huma fallbacks (404/405) on the `/_mock/*` family emit RFC 9457 via the retained `problem` package (the OAuth2-shape fallbacks are for the protocol families only — §6). Note this stays consistent with the §6 architecture decision that `internal/adapter/http` never imports `httpapi`/`oidc`: the protocol-vs-control fallback writer is injected from the composition root, so the substrate keeps no OIDC route-family knowledge.

### C.9 Composition-root wiring and config

Two-plus new typed config fields (quad-pattern, `MOCK_OIDC_*` + the §1 parity aliases unaffected):

```go
// config.Config additions
ControlEnabled bool   // --control-enabled (default true)
ControlAddr    string // --control-addr  (default ""; "" co-locates /_mock on Addr)
ControlToken   string // --control-token (default ""; "" disables the gate)
```

`app.New` builds the in-memory adapters once and hands their narrow facets to both the OIDC services and the control Registrar. The runtime `Clock` is the **mutable** `memory.Clock` (satisfying `oidc.Clock` + `controlapi.ClockController`), seeded from config — *not* an immutable `oidc.FixedClock`, which `/_mock/clock/advance` could never move. Co-located vs. dedicated-listener mirrors the existing `metricsServer` pattern exactly (a third `namedServer` joins `App.servers()` and its graceful shutdown):

```go
recorder := memory.NewRequestRecorder()
queue := memory.NewCallbackQueue()
clock := memory.NewClock(cfg.SystemTime) // seeded from config; mutable at runtime

tokens := oidc.NewTokenService(signer, keyStore, queue, clock, issuers, logger)
// ... AuthorizeService, SessionService, ProviderService ...

controlDeps := controlapi.Deps{
	Tokens:    tokens,
	Scenarios: queue,    // *memory.CallbackQueue: ScenarioStore (write) + oidc.CallbackQueue (DequeueFor, read)
	Requests:  recorder, // *memory.RequestRecorder: RequestLog (drain) + oidc.RequestRecorder (Record)
	Clock:     clock,    // *memory.Clock: controlapi.ClockController (write) + oidc.Clock (read)
}

// OIDC capture is a mux-level chi middleware that path-guards to the protocol
// families; it lives in httpapi (it names oidc.RequestRecorder), never in the
// generic adapter/http substrate.
oidcMux.Use(httpapi.RecordRequests(recorder))

register := func(api huma.API) {
	httpapi.Register(api, oidcDeps) // /{issuer}/...
	if cfg.ControlEnabled && cfg.ControlAddr == "" {
		// huma.NewGroup applies the /_mock prefix; control ops register RELATIVE
		// paths (/mint, /scenarios, /requests/take, …) so the group yields
		// /_mock/mint etc. — exactly one prefix, never /_mock/_mock.
		controlapi.Register(huma.NewGroup(api, controlapi.Prefix), controlDeps)
	}
}
```

When `ControlAddr != ""`, the control plane is registered on its own chi mux + humachi API behind a dedicated `http.Server` (no recording middleware, optional token-gate chi middleware), added to `App.servers()`; the OIDC listener then carries no `/_mock` routes at all.

The optional token gate is a chi middleware scoped to the control router (or path-prefixed when co-located):

```go
func controlTokenGate(want string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if want != "" && subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Mock-Control-Token")), []byte(want)) != 1 {
				problem.Write(w, http.StatusUnauthorized, "missing or invalid control token")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
```

### C.10 OpenAPI positioning

Because the control operations are clean Huma JSON operations (§5 permits this for the control plane), they appear in the OpenAPI document tagged `Mock Control`, with their own group description that states, in the spec itself, that the surface is test-only and disabled by `--control-enabled=false`. The OAuth2/OpenID security schemes stamped onto the protocol operations do **not** apply to `/_mock`; when `--control-token` is set, the control group advertises an `apiKey`-in-header scheme for that token (the post-register stamping pattern the template used for authz, walking `api.OpenAPI().Paths[...]` and selecting the method field — there is no `Operations()` accessor in huma). Control routes are static (`/_mock/mint`, `/_mock/requests/take`, …), so they pose no metric/trace cardinality risk (unlike `/{issuer}/…`, whose labels use the route template).

### C.11 Worked end-to-end example (testcontainers-go)

This is the A2/S1/S2/S4 acceptance shape: a test brings up the container, mints a token for setup, drives a real flow under a frozen clock, asserts the issued token, and reads back what its code sent — with no in-process library. A tiny typed control client wraps the `/_mock` API; in practice this client ships as an importable helper so a test is genuinely "a few lines."

```go
func TestProtectedAPI(t *testing.T) {
	ctx := context.Background()

	// 1. Bring up the same container tests and prod use (C8, D-1).
	ctr, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "ghcr.io/meigma/mock-oidc:latest",
			ExposedPorts: []string{"8080/tcp"},
			Env:          map[string]string{"MOCK_OIDC_CONTROL_ENABLED": "true"},
			WaitingFor:   wait.ForHTTP("/healthz").WithPort("8080/tcp"),
		},
		Started: true,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = ctr.Terminate(ctx) })

	base, err := ctr.PortEndpoint(ctx, "8080/tcp", "http") // e.g. http://localhost:53124
	require.NoError(t, err)
	mock := controlclient.New(base) // thin typed wrapper over /_mock; base also feeds iss resolution

	// Clean slate, then freeze time so iat/nbf/exp (and S2's expiry math) are deterministic.
	require.NoError(t, mock.Reset(ctx))
	require.NoError(t, mock.FreezeClock(ctx, time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)))

	// 2. issueToken: mint a setup token for a specific user with specific roles (C3/S1).
	minted, err := mock.Mint(ctx, controlclient.MintRequest{
		Issuer:   "default",
		Subject:  "alice",
		Audience: []string{"my-api"},
		Claims:   map[string]any{"roles": []string{"admin"}},
	})
	require.NoError(t, err)

	// The minted token validates against the app's standard OIDC tooling with no glue (G2):
	// it verifies at <base>/default/jwks and <base>/default/userinfo because mint used the
	// same signer/kid as the protocol path.
	resp := callMyServiceUnderTest(t, minted.Token) // app fetches JWKS from the mock and verifies
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// 3. enqueueCallback: program the NEXT real /token to carry a custom claim (in-band).
	require.NoError(t, mock.EnqueueScenario(ctx, controlclient.Scenario{
		Issuer: "default",
		Claims: map[string]any{"acr": "Level4", "roles": []string{"teller"}},
	}))

	// Drive a real client-credentials request against the mock's protocol surface.
	tok := postForm(t, base+"/default/token", url.Values{
		"grant_type": {"client_credentials"}, "client_id": {"cashier"},
	})
	require.Equal(t, "Level4", decodeClaim(t, tok.AccessToken, "acr")) // the scenario applied

	// 4. takeRequest: assert exactly what our code sent to the provider (S4/G6).
	captured, err := mock.TakeRequest(ctx, controlclient.TakeFilter{Endpoint: "token", TimeoutMs: 2000})
	require.NoError(t, err)
	require.Equal(t, http.MethodPost, captured.Method)
	require.Equal(t, "/default/token", captured.Path)
	require.Contains(t, captured.Body, "grant_type=client_credentials") // raw bytes, order preserved

	// 5. S2 unhappy path: advance the frozen clock past exp and re-validate -> rejected.
	require.NoError(t, mock.AdvanceClock(ctx, time.Hour+time.Minute))
	require.Equal(t, http.StatusUnauthorized, callMyServiceUnderTest(t, minted.Token).StatusCode)
}
```

For the container-to-container + browser topology (catalog Scenario 2, the classic `iss` mismatch), the test passes the network-internal URL as the mint/`issuerUrl` override (the `anyToken` path) or relies on proxy-aware resolution from the `Host` header — so the `iss` the app validates matches the address it actually reaches the mock on (C9).

### C.12 Invariants and cross-checks (tests to write)

- **Mint ≡ grant.** A token from `POST /_mock/mint` and a token from `/{issuer}/token` for the same `MintSpec` are structurally identical (same `kid`, `alg`, default claims) and both verify at `/{issuer}/jwks`, `/userinfo`, `/introspect`. A unit test asserts the mint path and the grant path share the `Signer`/`KeyStore`.
- **Scenario priority parity.** An enqueued scenario for issuer A is consumed by the next A token request (incl. refresh) and is *not* consumed by an interleaved issuer-B request (head-of-queue, issuer-matched) — the catalog's queue semantic.
- **Raw-body fidelity.** A captured request's `bodyBase64` round-trips the exact bytes the client sent (duplicate keys, order, `+`/`%` encoding intact) — `takeRequest` preserves raw bytes, not a reparsed map.
- **Reserved-prefix safety.** `ParseIssuerID("_mock")` fails with the unified `CodeNotFound`/`404` `*ProtocolError`; `mint`/`scenario` with `issuer:"_mock"` therefore returns `404` problem+json (the same error mapped through `toControlError`'s `errors.As`); a request to `/_mock/mint` is never routed to the OIDC `{issuer}` handler.
- **Isolation.** With `--control-enabled=false`, no `/_mock/*` route exists (returns the OIDC `404`); with `--control-token` set, an un-tokened `/_mock/*` request returns `401`; control traffic never appears in the request log (the path-guarded `RecordRequests` skips `/_mock/*`).
- **Clock globality.** Freezing the clock changes `iat`/`exp` on every issuer's tokens and the verifier's "now" (`/userinfo`, `/introspect`) — one `Clock`, both issuance and verification — and `expires_in` stays consistent with `exp` under a frozen/advanced clock.

---

**Files this section defines or touches (absolute paths):**
- New package: `/Users/josh/code/meigma/mock-oidc/internal/oidc/controlapi/` (`register.go`, `deps.go`, `mint.go`, `scenarios.go`, `requests.go`, `clock.go`, `reset.go`, `dto.go`, `mapping.go`, `errors.go`)
- Domain additions in `/Users/josh/code/meigma/mock-oidc/internal/oidc/`: `MintSpec`/`MintKind` and `TokenService.Mint` (`token.go`), `Scenario`/`ScenarioID` (`callback.go`), `CapturedRequest`/`CaptureFilter` + `NewCapturedRequest(method, rawURL string, header, query map[string][]string, body []byte)` (stdlib-primitive params only — no `net/url`/`net/http` in `capture.go`), `ClockState` + mutable-clock contract (`clock.go`), reserved-prefix rejection in `ParseIssuerID` returning a `*ProtocolError` (`issuer.go`), control-relevant sentinels (`errors.go`)
- Outbound adapter additions in `/Users/josh/code/meigma/mock-oidc/internal/oidc/memory/`: `CallbackQueue` (also `controlapi.ScenarioStore`), `RequestRecorder` (also `controlapi.RequestLog`), `Clock` (mutable; also `controlapi.ClockController`)
- Recording middleware: `/Users/josh/code/meigma/mock-oidc/internal/oidc/httpapi/` (NOT `internal/adapter/http`) — mux-level, path-guarded to OIDC families
- Edge wiring: `/Users/josh/code/meigma/mock-oidc/internal/app/app.go` (control Registrar + optional third listener + mutable `memory.Clock`) and `internal/app/serve.go` (named control server); config fields in `/Users/josh/code/meigma/mock-oidc/internal/config/config.go` (`ControlEnabled`, `ControlAddr`, `ControlToken`)

## Configuration, Composition Root & Entrypoint

This section specifies how `mock-oidc` is configured, how the composition root (`internal/app`) assembles the in-memory + signing adapters behind the domain services, and how the process is launched. It is the *edge* tier of the dependency rule (Contract §3): `internal/config`, `internal/app`, and `internal/cli` are the only packages that name concrete adapters and own process concerns (flags, env, JSON-config parsing, signals, lifecycle). The DB/authz machinery the template carried here is removed wholesale.

### 1. Two configuration layers

The template had a single flat `config.Config`. `mock-oidc` keeps that flat *process* config but adds a second, strongly-typed *domain seed* parsed from the upstream-parity JSON config. They are deliberately distinct: one configures the **server** (where it listens, how it logs), the other seeds the **OIDC behavior** (clocks, keys, callbacks, login mode).

| Layer | Type | Source | Owns | Consumed by |
|---|---|---|---|---|
| Process config | `config.Config` | flags + `MOCK_OIDC_*` env + parity env aliases | listen addr/host/port, TLS, timeouts, log, CORS, proxy header, rate-limit (off), tracing, JSON-config source pointers | `app.New(cfg, …)`, transport |
| Domain seed | `config.Seed` | JSON config document (`JSON_CONFIG` / file / `./config.json`) | interactive-login mode, refresh rotation, frozen clock, signing algorithm + initial keys, per-issuer token callbacks, login/static asset paths, TLS-from-`ssl` flag | `app.WithSeed(seed)` → in-memory + signing adapters + services |

The split keeps `config.Config` viper-bound and stringly-flat where that is harmless (server tuning), while the OIDC seed is **parsed-not-validated** into closed domain types at the config edge — no `map[string]any`, no bare `string` algorithms or grant types, and (per the parse-don't-validate fix below) no `json.RawMessage` crossing into `internal/oidc`.

```
flags ─┐
env  ──┼─► viper ──► config.Load ─────► config.Config ─────────────┐
       │                                                            ├─► app.New ─► App
JSON_CONFIG / JSON_CONFIG_PATH / ./config.json ─► config.LoadSeed ─┘    (composition root)
                                    │                              app.WithSeed(config.Seed)
                                    └─ parse-don't-validate ──► oidc.* typed values
```

### 2. Process config: `config.Config`

`config.Config` keeps the template's quad-pattern (`RegisterFlags` / `setDefaults` / `Load` / `Validate`) and every *server* field (Contract §4 KEEP). The DB and authz fields are deleted; OIDC-server fields are added.

```go
// Package config defines the mock-oidc server's runtime configuration, loaded
// from flags and MOCK_OIDC_* environment variables (plus upstream-parity env
// aliases) via Viper, and the typed JSON config document that seeds OIDC
// behavior.
package config

const (
	defaultAddr              = ":8080"
	defaultServerPort        = 8080
	defaultMetricsAddr       = ":9090"
	defaultReadTimeout       = 5 * time.Second
	defaultReadHeaderTimeout = 5 * time.Second
	defaultWriteTimeout      = 10 * time.Second
	defaultIdleTimeout       = 120 * time.Second
	defaultRequestTimeout    = 15 * time.Second
	defaultShutdownGrace     = 15 * time.Second
	defaultLogLevel          = "info"
	defaultLogFormat         = "json"
	// defaultRateLimitEnabled is FALSE: a for-testing OIDC server is hammered by
	// Testcontainers suites, so throttling legitimate test traffic is a parity
	// defect (Contract §4 RATE LIMITING). Opt in via config when wanted.
	defaultRateLimitEnabled = false
	defaultRateLimitRPS     = 50.0
	defaultRateLimitBurst   = 100
	defaultTracingEnabled   = false
	// defaultConfigFilePath is the working-directory file consulted when neither
	// JSON_CONFIG nor JSON_CONFIG_PATH is set (upstream parity).
	defaultConfigFilePath = "config.json"
)

// Config holds the mock-oidc server's transport and process settings. OIDC
// behavior (clocks, keys, token callbacks, login mode) is carried by Seed, not
// here.
type Config struct {
	// Addr is the resolved host:port the HTTP server listens on. It is derived in
	// Load: an explicit --addr / MOCK_OIDC_ADDR wins; otherwise it is composed
	// from ServerHostname and ServerPort (upstream SERVER_HOSTNAME/SERVER_PORT).
	Addr string
	// ServerHostname is the bind interface. Empty (the default) binds the
	// wildcard address, matching upstream's InetSocketAddress(0) behavior.
	ServerHostname string
	// ServerPort is the listen port. Precedence SERVER_PORT > PORT > 8080 is
	// applied by the env binding in initializeConfig.
	ServerPort int

	// MetricsAddr is the dedicated /metrics listener; empty co-locates it on Addr.
	MetricsAddr string

	ReadTimeout       time.Duration
	ReadHeaderTimeout time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	RequestTimeout    time.Duration
	ShutdownGrace     time.Duration

	LogLevel  string
	LogFormat string

	// CORSAllowedOrigins optionally TIGHTENS CORS to an explicit allowlist. Empty
	// (the default) preserves upstream's zero-config browser behavior (parity
	// C9/S3): the request Origin is reflected (not "*"),
	// Access-Control-Allow-Credentials: true, methods GET/POST/OPTIONS, and
	// preflight echoes Access-Control-Request-Headers with a 204. A non-empty
	// list restricts to those origins. The reflect-with-credentials default lives
	// in the kept CORS middleware (http-adapter); config only carries the
	// optional override.
	CORSAllowedOrigins []string
	// TrustedProxyHeader names a proxy header to read the client IP from (kept
	// IP-only ClientIP middleware). This is distinct from the proxy-aware
	// RequestOrigin extraction that drives base-URL/iss resolution (Contract §8).
	TrustedProxyHeader string

	// TLS configures HTTPS. Presence of an `ssl` object in the JSON config (even
	// empty) maps onto TLSEnabled via Seed.TLSFromHTTPServer (set in serve),
	// matching upstream. When enabled with empty cert/key, app generates a
	// self-signed localhost certificate in-process (see §6 resolveTLS).
	TLSEnabled  bool
	TLSCertFile string
	TLSKeyFile  string

	// RateLimit* default OFF (see defaultRateLimitEnabled).
	RateLimitEnabled bool
	RateLimitRPS     float64
	RateLimitBurst   int

	TracingEnabled bool

	// JSONConfig (inline) and JSONConfigPath point at the OIDC seed source. They
	// are resolved by LoadSeed, not used by the server directly. Held here so the
	// one viper instance is the single source of truth for both layers.
	JSONConfig     string
	JSONConfigPath string
}
```

`RegisterFlags` / `setDefaults` / `Load` mirror the template; the deltas are: drop `database-url`, `db-max-conns`, `authz-enabled`, `authz-policy-dir`; add `server-hostname`, `server-port`, `tls-cert-file`, `tls-key-file`, `json-config-path`; flip `rate-limit-enabled` default to `false`.

```go
func RegisterFlags(flags *pflag.FlagSet) {
	flags.String("addr", "", "host:port to listen on; overrides --server-hostname/--server-port when set")
	flags.String("server-hostname", "", "bind interface; empty binds the wildcard address (env SERVER_HOSTNAME)")
	flags.Int("server-port", defaultServerPort, "listen port (env SERVER_PORT > PORT > 8080)")
	flags.String("metrics-addr", defaultMetricsAddr, "dedicated /metrics listener; empty serves /metrics on --addr")
	flags.String("log-level", defaultLogLevel, "log level: debug, info, warn, or error (env LOG_LEVEL)")
	flags.String("log-format", defaultLogFormat, "log format: json or text")
	// …timeouts, shutdown-grace, cors-allowed-origins, trusted-proxy-header unchanged…
	flags.String("tls-cert-file", "", "PEM certificate for HTTPS; empty with TLS enabled generates a self-signed localhost cert")
	flags.String("tls-key-file", "", "PEM private key for HTTPS; paired with --tls-cert-file")
	flags.Bool("rate-limit-enabled", defaultRateLimitEnabled, "enable per-client (IP) rate limiting; OFF by default for test traffic")
	flags.Float64("rate-limit-rps", defaultRateLimitRPS, "sustained per-client request rate when rate limiting is enabled")
	flags.Int("rate-limit-burst", defaultRateLimitBurst, "per-client burst size when rate limiting is enabled")
	flags.Bool("tracing-enabled", defaultTracingEnabled, "enable OpenTelemetry tracing; configure the exporter via OTEL_* env vars")
	flags.String("json-config-path", "", "path to a JSON config document (env JSON_CONFIG_PATH; default ./config.json)")
}
```

`Load` resolves the effective `Addr` and reads the rest:

```go
// Load reads server configuration from vp, applying defaults and resolving the
// effective listen address.
func Load(vp *viper.Viper) Config {
	setDefaults(vp)
	return Config{
		Addr:               resolveListenAddr(vp),
		ServerHostname:     vp.GetString("server-hostname"),
		ServerPort:         vp.GetInt("server-port"),
		MetricsAddr:        vp.GetString("metrics-addr"),
		ReadTimeout:        vp.GetDuration("read-timeout"),
		// …other timeouts…
		ShutdownGrace:      vp.GetDuration("shutdown-grace"),
		LogLevel:           vp.GetString("log-level"),
		LogFormat:          vp.GetString("log-format"),
		CORSAllowedOrigins: vp.GetStringSlice("cors-allowed-origins"),
		TrustedProxyHeader: vp.GetString("trusted-proxy-header"),
		TLSCertFile:        vp.GetString("tls-cert-file"),
		TLSKeyFile:         vp.GetString("tls-key-file"),
		RateLimitEnabled:   vp.GetBool("rate-limit-enabled"),
		RateLimitRPS:       vp.GetFloat64("rate-limit-rps"),
		RateLimitBurst:     vp.GetInt("rate-limit-burst"),
		TracingEnabled:     vp.GetBool("tracing-enabled"),
		JSONConfig:         vp.GetString("json-config"),
		JSONConfigPath:     vp.GetString("json-config-path"),
	}
}

// resolveListenAddr applies the address precedence: an explicit --addr /
// MOCK_OIDC_ADDR wins; otherwise compose SERVER_HOSTNAME:SERVER_PORT. viper's
// IsSet is false for defaults, so an unset --addr falls through to composition,
// yielding ":8080" with the stock defaults.
func resolveListenAddr(vp *viper.Viper) string {
	if vp.IsSet("addr") {
		return vp.GetString("addr")
	}
	return net.JoinHostPort(vp.GetString("server-hostname"), strconv.Itoa(vp.GetInt("server-port")))
}
```

`Validate` drops the `database-url` clause and the always-required rate-limit clause; rate-limit fields are only checked when enabled:

```go
func (c Config) Validate() error {
	if strings.TrimSpace(c.Addr) == "" {
		return errors.New("addr must not be empty")
	}
	if c.MetricsAddr != "" && c.MetricsAddr == c.Addr {
		return errors.New("metrics-addr must differ from addr")
	}
	if c.RequestTimeout <= 0 {
		return errors.New("request-timeout must be positive")
	}
	if c.ShutdownGrace <= 0 {
		return errors.New("shutdown-grace must be positive")
	}
	if c.LogFormat != "json" && c.LogFormat != "text" {
		return fmt.Errorf("log-format must be %q or %q, got %q", "json", "text", c.LogFormat)
	}
	if c.TLSCertFile != "" && c.TLSKeyFile == "" || c.TLSCertFile == "" && c.TLSKeyFile != "" {
		return errors.New("tls-cert-file and tls-key-file must be set together")
	}
	if c.RateLimitEnabled {
		if c.RateLimitRPS <= 0 {
			return errors.New("rate-limit-rps must be positive when rate limiting is enabled")
		}
		if c.RateLimitBurst <= 0 {
			return errors.New("rate-limit-burst must be positive when rate limiting is enabled")
		}
	}
	return nil
}
```

`Validate` runs on the flag/env-derived `Config` only; `TLSEnabled` (derived from `ssl:{}` in JSON) is set *after* validation in `runServe`, so the "cert and key together" rule never trips on a JSON-enabled, no-cert TLS run (both files empty → self-signed generation in §6).

### 3. Env binding & upstream-parity aliases

`initializeConfig` keeps the template's prefix+replacer+`AutomaticEnv`, switching the prefix to `MOCK_OIDC`, and adds explicit `BindEnv` calls for the unprefixed upstream names (Contract §1). The replacer maps `server-port` → `MOCK_OIDC_SERVER_PORT`; `AutomaticEnv` never matches the bare `SERVER_PORT`, so each parity alias is bound explicitly. Listing the prefixed name *first* keeps the meigma-native variable authoritative while the bare upstream name remains a working alias.

```go
func initializeConfig(cmd *cobra.Command, vp *viper.Viper) error {
	vp.SetEnvPrefix("MOCK_OIDC")
	vp.SetEnvKeyReplacer(strings.NewReplacer("-", "_", ".", "_"))
	vp.AutomaticEnv()

	// Upstream-parity env aliases. First match wins, so MOCK_OIDC_* takes
	// precedence over the bare upstream name where both are bound.
	_ = vp.BindEnv("server-hostname", "MOCK_OIDC_SERVER_HOSTNAME", "SERVER_HOSTNAME")
	_ = vp.BindEnv("server-port", "MOCK_OIDC_SERVER_PORT", "SERVER_PORT", "PORT") // SERVER_PORT > PORT > default 8080
	_ = vp.BindEnv("log-level", "MOCK_OIDC_LOG_LEVEL", "LOG_LEVEL")
	_ = vp.BindEnv("json-config", "MOCK_OIDC_JSON_CONFIG", "JSON_CONFIG")
	_ = vp.BindEnv("json-config-path", "MOCK_OIDC_JSON_CONFIG_PATH", "JSON_CONFIG_PATH")
	// LOGBACK_CONFIG is a JVM/Logback concept with no Go analog: accepted and
	// ignored (no-op alias, Contract §1). It is intentionally not bound.

	if err := vp.BindPFlags(cmd.Root().PersistentFlags()); err != nil {
		return fmt.Errorf("bind persistent flags: %w", err)
	}
	return vp.BindPFlags(cmd.Flags())
}
```

Precedence summary (highest first):

| Concern | Resolution |
|---|---|
| Listen port | `--server-port` / `MOCK_OIDC_SERVER_PORT` > `SERVER_PORT` > `PORT` > `8080` |
| Listen address | `--addr` / `MOCK_OIDC_ADDR` (if set) > composed `hostname:port` |
| Bind host | `--server-hostname` / `MOCK_OIDC_SERVER_HOSTNAME` > `SERVER_HOSTNAME` > wildcard |
| Log level | `--log-level` / `MOCK_OIDC_LOG_LEVEL` > `LOG_LEVEL` > `info` |
| OIDC JSON source | `JSON_CONFIG` (inline) > `JSON_CONFIG_PATH` (file) > `./config.json` > built-in defaults |

### 4. JSON config document → typed domain seed

`config.Seed` is the parsed, closed-typed OIDC seed. The JSON DTOs live in `config/jsonconfig.go`; mapping to domain types happens at the config edge so the domain only ever sees `oidc.*` values — including the `${…}` claim template, which is **ordered-decoded at this edge** (the fix below) rather than handed raw bytes into `internal/oidc`.

```go
// Seed is the strongly-typed OIDC behavior parsed from the JSON config document
// (or built-in defaults). Every field is a closed domain type or a primitive
// flag — never map[string]any. The composition root distributes it: SystemTime
// → Clock, Algorithm/InitialKeys → signing adapter, TokenCallbacks → callback
// resolver, the flags → services and httpapi.Assets.
type Seed struct {
	// InteractiveLogin defaults true (standalone parity: upstream's standalone
	// main builds OAuth2Config(interactiveLogin=true) when no JSON is found).
	InteractiveLogin   bool
	RotateRefreshToken bool

	// SystemTime, when present, freezes the clock for deterministic iat/nbf/exp
	// AND verification (the same instant drives both — see §6 of the error/clock
	// model). Absent → the live, runtime-advanceable clock.
	SystemTime      oidc.Instant
	SystemTimeFixed bool

	Algorithm      oidc.SigningAlgorithm           // default RS256
	TokenCallbacks []oidc.RequestMappingCallback

	// InitialKeys carries opaque JWK JSON blobs (private material). They are NOT
	// parsed here: crypto lives only in the signing adapter, which parses and
	// validates them (and the algorithm) at construction. Empty → the embedded
	// 5-key RSA seed. We accept an ARRAY, dropping upstream's single-key JSON
	// limitation; and proper JSON objects, dropping its string-escaped-JWK quirk.
	InitialKeys [][]byte

	LoginPagePath    string // custom login HTML served verbatim; empty → built-in template
	StaticAssetsPath string

	// TLSFromHTTPServer is true when the JSON config carried an `ssl` object
	// (even empty). serve ORs it into Config.TLSEnabled so ssl:{} turns HTTPS on
	// (upstream parity). Declared here so toSeed has a home for the flag.
	TLSFromHTTPServer bool
}

// DefaultSeed is the zero-config seed used when no JSON config is present.
func DefaultSeed() Seed {
	return Seed{InteractiveLogin: true, Algorithm: oidc.AlgorithmRS256}
}
```

The wire DTOs mirror upstream's `OAuth2Config` field names 1:1 and are lenient on unknown keys (parity); the cleanups (single-key → array, escaped-JWK → object, no body-lowercasing of errors) are applied here. The `${…}` claim object is decoded by a config-edge helper into an *ordered, typed* domain template, so neither config nor domain holds a `map[string]any` and `encoding/json` never runs inside `internal/oidc`.

```go
// document is the JSON config wire shape (upstream OAuth2Config). Unknown keys
// are ignored (lenient parity); only the fields mock-oidc honors are declared.
type document struct {
	InteractiveLogin   *bool              `json:"interactiveLogin"`
	LoginPagePath      string             `json:"loginPagePath"`
	StaticAssetsPath   string             `json:"staticAssetsPath"`
	RotateRefreshToken bool               `json:"rotateRefreshToken"`
	TokenProvider      tokenProviderDoc   `json:"tokenProvider"`
	TokenCallbacks     []tokenCallbackDoc `json:"tokenCallbacks"`
	HTTPServer         httpServerDoc      `json:"httpServer"` // string | { type, ssl }
}

type tokenProviderDoc struct {
	SystemTime  string         `json:"systemTime"` // RFC3339; "" → live clock
	KeyProvider keyProviderDoc `json:"keyProvider"`
}

type keyProviderDoc struct {
	Algorithm   string            `json:"algorithm"`   // "" → RS256
	InitialKeys []json.RawMessage `json:"initialKeys"` // array of JWK objects (cleanup)
}

type tokenCallbackDoc struct {
	IssuerID        string              `json:"issuerId"`
	TokenExpiry     int64               `json:"tokenExpiry"` // seconds; 0 → default 3600
	RequestMappings []requestMappingDoc `json:"requestMappings"`
}

type requestMappingDoc struct {
	RequestParam string          `json:"requestParam"`
	Match        string          `json:"match"`
	TypeHeader   string          `json:"typeHeader"`
	Claims       json.RawMessage `json:"claims"` // ${…} template; ordered-decoded at this edge
}

// httpServerDoc accepts either a bare string ("autoStart" etc.) or an object
// carrying an optional `ssl` block. tlsRequested reports whether an `ssl` object
// was present (even {}), which serve maps onto TLS.
type httpServerDoc struct { /* custom UnmarshalJSON: string | { type, ssl } */ }

func (h httpServerDoc) tlsRequested() bool { /* true iff an ssl object was present */ }
```

`LoadSeed` resolves the source (precedence above) and parses; mapping is **parse-don't-validate**, surfacing the first typed error:

```go
// LoadSeed resolves the JSON config source and parses it into a typed Seed. No
// source present → DefaultSeed(). Malformed JSON or any value the domain rejects
// (bad algorithm, bad issuer id, unparseable systemTime, bad claim template) is
// a hard error.
func LoadSeed(vp *viper.Viper) (Seed, error) {
	raw, ok, err := resolveJSONSource(vp)
	if err != nil {
		return Seed{}, err
	}
	if !ok {
		return DefaultSeed(), nil
	}

	var doc document
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&doc); err != nil {
		return Seed{}, fmt.Errorf("parse JSON config: %w", err)
	}
	return doc.toSeed()
}

// resolveJSONSource implements: JSON_CONFIG (inline) > JSON_CONFIG_PATH (file) >
// ./config.json > none. A missing JSON_CONFIG_PATH file is an error (it was
// named explicitly); a missing ./config.json is not (it is the implicit
// fallback) — upstream falls back to defaults in both cases, but we fail loudly
// when an operator names a file that is not there.
func resolveJSONSource(vp *viper.Viper) (data []byte, found bool, err error) {
	if inline := strings.TrimSpace(vp.GetString("json-config")); inline != "" {
		return []byte(inline), true, nil
	}
	if path := vp.GetString("json-config-path"); path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, false, fmt.Errorf("read json-config-path %q: %w", path, err)
		}
		return b, true, nil
	}
	if b, err := os.ReadFile(defaultConfigFilePath); err == nil {
		return b, true, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, false, fmt.Errorf("read %s: %w", defaultConfigFilePath, err)
	}
	return nil, false, nil
}

func (d document) toSeed() (Seed, error) {
	seed := DefaultSeed()
	if d.InteractiveLogin != nil {
		seed.InteractiveLogin = *d.InteractiveLogin
	}
	seed.RotateRefreshToken = d.RotateRefreshToken
	seed.LoginPagePath = d.LoginPagePath
	seed.StaticAssetsPath = d.StaticAssetsPath
	seed.TLSFromHTTPServer = d.HTTPServer.tlsRequested() // ssl:{} → TLS on (wired by serve)

	if t := strings.TrimSpace(d.TokenProvider.SystemTime); t != "" {
		inst, err := oidc.ParseInstant(t) // RFC3339; pins issuance + verification
		if err != nil {
			return Seed{}, fmt.Errorf("tokenProvider.systemTime: %w", err)
		}
		seed.SystemTime, seed.SystemTimeFixed = inst, true
	}

	if a := strings.TrimSpace(d.TokenProvider.KeyProvider.Algorithm); a != "" {
		alg, err := oidc.ParseSigningAlgorithm(a) // rejects ES256K/ES512/EdDSA/HMAC/none typed
		if err != nil {
			return Seed{}, fmt.Errorf("tokenProvider.keyProvider.algorithm: %w", err)
		}
		seed.Algorithm = alg
	}
	for _, k := range d.TokenProvider.KeyProvider.InitialKeys {
		seed.InitialKeys = append(seed.InitialKeys, []byte(k)) // opaque; parsed by signing
	}

	for i, cb := range d.TokenCallbacks {
		mapped, err := cb.toCallback()
		if err != nil {
			return Seed{}, fmt.Errorf("tokenCallbacks[%d]: %w", i, err)
		}
		seed.TokenCallbacks = append(seed.TokenCallbacks, mapped)
	}
	return seed, nil
}

func (c tokenCallbackDoc) toCallback() (oidc.RequestMappingCallback, error) {
	issuer, err := oidc.ParseIssuerID(c.IssuerID) // canonical constructor: rejects "", "/", reserved _mock
	if err != nil {
		return oidc.RequestMappingCallback{}, err
	}
	expiry := oidc.DefaultTokenExpiry
	if c.TokenExpiry > 0 {
		expiry = time.Duration(c.TokenExpiry) * time.Second
	}
	mappings := make([]oidc.RequestMapping, 0, len(c.RequestMappings))
	for _, m := range c.RequestMappings {
		// The ${…} claim object is ordered-decoded HERE (config edge), not inside
		// the domain: no json.RawMessage crosses into internal/oidc.
		tmpl, err := decodeClaimTemplate(m.Claims)
		if err != nil {
			return oidc.RequestMappingCallback{}, err
		}
		typ, err := oidc.ParseTypeHeader(m.TypeHeader) // open JOSEType; "" → JWT, NEVER rejects at+jwt
		if err != nil {
			return oidc.RequestMappingCallback{}, err
		}
		mappings = append(mappings, oidc.RequestMapping{Param: m.RequestParam, Match: m.Match, TypeHeader: typ, Claims: tmpl})
	}
	return oidc.NewRequestMappingCallback(issuer, expiry, mappings)
}
```

**Parse-don't-validate fix — claim template decoded at the edge.** The earlier draft called `oidc.ParseClaimTemplate(m.Claims)` with a `json.RawMessage`, which would force `internal/oidc` to run `encoding/json` on untyped bytes — a silent leak past the Contract §3 boundary (the depguard list does not deny `encoding/json`, so nothing would catch it). Instead the config edge ordered-decodes into a typed `[]oidc.ClaimTemplateEntry` and hands the *already-decoded* structure to a domain constructor. The decode preserves key order (the whole point of the ordered template) and `ClaimValue` is the domain's defined JSON-leaf type:

```go
// decodeClaimTemplate ordered-decodes the ${…} claim object at the config edge,
// so encoding/json never runs inside internal/oidc (Contract §3). It yields an
// ordered []oidc.ClaimTemplateEntry the domain assembles via oidc.NewClaimTemplate
// — no json.RawMessage or map[string]any crosses the boundary.
func decodeClaimTemplate(raw json.RawMessage) (oidc.ClaimTemplate, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return oidc.ClaimTemplate{}, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber() // preserve numeric fidelity; no float coercion
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return oidc.ClaimTemplate{}, errors.New("claims must be a JSON object")
	}
	var entries []oidc.ClaimTemplateEntry
	for dec.More() {
		keyTok, err := dec.Token() // object key, in source order
		if err != nil {
			return oidc.ClaimTemplate{}, err
		}
		var val oidc.ClaimValue // oidc.ClaimValue is a DEFINED type (no = alias)
		if err := dec.Decode(&val); err != nil {
			return oidc.ClaimTemplate{}, err
		}
		entries = append(entries, oidc.ClaimTemplateEntry{Name: keyTok.(string), Value: val})
	}
	return oidc.NewClaimTemplate(entries) // domain validates ${…} refs; order preserved
}
```

**Dropped quirks (parity-in-intent):** (a) `initialKeys` is an array of JWK *objects*, not a single string-escaped JWK; (b) the seed merges with the embedded 5-key RSA bundle rather than replacing it with one key; (c) parse errors are reported with field context instead of being swallowed; (d) the claim `typeHeader` is the OPEN `oidc.JOSEType` (default `JWT`), so a standards-compliant `at+jwt` is honored rather than rejected by the closed `TokenType` enum. **Preserved intent:** lenient unknown keys, `httpServer.ssl` presence (even `{}`) turning TLS on, `systemTime` freezing the clock.

### 5. Composition root: `internal/app`

`app.New` keeps its signature and lifecycle shape but replaces the store/authz wiring with in-memory + signing adapter construction. The `App` struct loses `pool` and `closePool`; it keeps `rateLimiter` (nil when disabled), `traceShutdown`, the resolved TLS file paths, and the registrar (retained so the `openapi` command can render the spec).

```go
// Package app is the composition root: it wires the OIDC domain services to the
// in-memory state adapters and the crypto signing adapter, mounts the HTTP and
// control-plane inbound adapters, and assembles the runnable App. It holds no
// persistence: the mock keeps all state in memory (parity with upstream's maps).
package app

const serviceName = "mock-oidc" // OTel service.name; OTEL_SERVICE_NAME overrides.

type App struct {
	server        *http.Server
	metricsServer *http.Server
	logger        *slog.Logger
	grace         time.Duration
	tlsCertFile   string // empty → plain HTTP; non-empty → ListenAndServeTLS
	tlsKeyFile    string
	rateLimiter   *ratelimit.InMemory     // nil when rate limiting is disabled (the default)
	traceShutdown func(context.Context) error
	registrar     adapterhttp.Registrar   // retained for OpenAPIYAML / the openapi command
}
```

The Option seams replace `WithRepository`/`WithAuthenticator` with OIDC-shaped injection points (inject the clock, a fixed key set, preloaded scenarios):

```go
// Option configures how New wires the application.
type Option func(*options)

type options struct {
	seed      config.Seed
	clock     oidc.Clock              // nil → mutable memory.Clock derived from the seed
	signing   signingProvider         // nil → signing.NewProvider(seed)
	scenarios []oidc.Scenario         // preloaded one-shot callbacks (control-plane parity)
	recorder  *memory.RequestRecorder // nil → memory.NewRequestRecorder()
}

// signingProvider is the trio the crypto adapter satisfies. Declared by the
// consumer (composition root), per the dependency rule.
type signingProvider interface {
	oidc.Signer
	oidc.TokenVerifier
	oidc.KeyStore
}

// WithSeed supplies the parsed OIDC seed. serve passes config.LoadSeed's result;
// tests pass a hand-built Seed.
func WithSeed(s config.Seed) Option { return func(o *options) { o.seed = s } }

// WithClock injects a clock, overriding the seed. Unit tests pass
// oidc.NewFixedClock(instant) (immutable) to pin iat/nbf/exp; control-plane
// tests pass memory.NewClock(...) for a runtime-mutable seam. The production
// path (no WithClock) always uses the mutable memory.Clock (see resolveClock).
func WithClock(c oidc.Clock) Option { return func(o *options) { o.clock = c } }

// WithSigning injects a signing provider with a fixed key set, bypassing key
// generation. Tests use signing.NewProviderFromJWKS(fixedJWKS) for stable kids.
func WithSigning(p signingProvider) Option { return func(o *options) { o.signing = p } }

// WithScenarios preloads one-shot, issuer-matched token callbacks into the
// CallbackQueue, the same seam the control plane uses at runtime (PRD C6).
func WithScenarios(s ...oidc.Scenario) Option { return func(o *options) { o.scenarios = append(o.scenarios, s...) } }
```

`New` constructs adapters, derives the clock, builds the four services, applies the seed, resolves TLS, and registers both inbound adapters. There is no error path for store connection any more; the fallible steps are signing-provider construction (it parses the seed's JWKs and validates the algorithm), tracing init, and TLS cert resolution.

```go
func New(ctx context.Context, cfg config.Config, logger *slog.Logger, version string, opts ...Option) (*App, error) {
	o := options{seed: config.DefaultSeed()}
	for _, opt := range opts {
		opt(&o)
	}

	clock := resolveClock(o)
	sign, err := resolveSigning(o)
	if err != nil {
		return nil, fmt.Errorf("init signing: %w", err)
	}

	// In-memory outbound adapters (production, not test-only): concurrency-safe.
	issuers := memory.NewIssuerRegistry(sign, clock) // computeIfAbsent per {issuer}
	codes := memory.NewCodeStore()
	refresh := memory.NewRefreshTokenStore()
	callbacks := memory.NewCallbackQueue()
	recorder := o.recorder
	if recorder == nil {
		recorder = memory.NewRequestRecorder()
	}

	// Seed the callback resolver: configured per-issuer RequestMappingCallbacks
	// plus any preloaded one-shot scenarios (queue head wins at request time).
	resolver := oidc.NewCallbackResolver(callbacks, o.seed.TokenCallbacks)
	for _, sc := range o.scenarios {
		callbacks.Enqueue(sc)
	}

	// Application services orchestrate the ports + value types. NO service takes
	// the RequestRecorder: capture is a transport-edge concern (the recording
	// middleware writes; the control plane reads). The login mode and refresh-
	// rotation flag come from the seed; LoginPagePath/StaticAssetsPath are
	// presentation, owned by httpapi.Assets — NOT a domain LoginPolicy field.
	provider := oidc.NewProviderService(issuers, sign, clock)
	authorize := oidc.NewAuthorizeService(issuers, codes, clock, oidc.LoginPolicy{
		Interactive: o.seed.InteractiveLogin,
	})
	tokens := oidc.NewTokenService(issuers, sign, codes, refresh, resolver, clock, oidc.TokenPolicy{
		RotateRefreshToken: o.seed.RotateRefreshToken,
	})
	sessions := oidc.NewSessionService(issuers, sign, refresh, clock)

	metrics := observability.NewMetrics()
	traceShutdown, err := observability.NewTracerProvider(ctx, observability.TracingConfig{
		Enabled: cfg.TracingEnabled, ServiceName: serviceName, ServiceVersion: version,
	})
	if err != nil {
		return nil, fmt.Errorf("init tracing: %w", err)
	}

	tlsCert, tlsKey, err := resolveTLS(cfg)
	if err != nil {
		return nil, fmt.Errorf("init tls: %w", err)
	}

	rateLimiter, installRateLimit := buildRateLimiter(cfg, logger) // both nil when disabled (default)

	serveMetricsInline := cfg.MetricsAddr == ""
	registrar := registerOIDC(provider, authorize, tokens, sessions, callbacks, recorder, clock, staticAssets(o.seed))
	handler := adapterhttp.NewRouter(adapterhttp.RouterDeps{
		Logger:               logger,
		Metrics:              metrics,
		ServeMetricsEndpoint: serveMetricsInline,
		Version:              version,
		RequestTimeout:       cfg.RequestTimeout,
		CORSAllowedOrigins:   cfg.CORSAllowedOrigins,
		TrustedProxyHeader:   cfg.TrustedProxyHeader,
		// No DB ⇒ no readiness checks ⇒ /readyz is unconditionally ready. The kept
		// router itself mounts the /isalive parity alias alongside /healthz at the
		// root (per the http-adapter section); no RouterDeps flag is invented here.
		Readiness:        nil,
		Register:         registrar,
		Tracing:          cfg.TracingEnabled,
		InstallRateLimit: installRateLimit,
		// Recording is the OIDC-owned, path-guarded capture middleware (writes to
		// recorder; early-returns for /_mock, /healthz, /readyz, /metrics,
		// /openapi*, /docs). It lives in httpapi so the generic substrate never
		// imports internal/oidc. Fed the recorder here, not via httpapi.Deps.
		Recording: httpapi.RecordRequests(recorder),
		// FallbackWriter shapes the router's 404/405/recover/timeout responses per
		// route family WITHOUT adapter/http importing internal/oidc: protocol
		// families get the OAuth2 (RFC 6749) writer, control/infra keep
		// problem.Write (RFC 9457). Injected from the edge as a strategy.
		FallbackWriter: httpapi.FallbackWriter(),
		// InstallAuthz / FinalizeAuthz removed: mock-oidc is the auth provider; it
		// never authenticates its own callers.
	})

	server := &http.Server{
		Addr: cfg.Addr, Handler: handler,
		ReadTimeout: cfg.ReadTimeout, ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		WriteTimeout: cfg.WriteTimeout, IdleTimeout: cfg.IdleTimeout,
	}
	var metricsServer *http.Server
	if !serveMetricsInline {
		metricsServer = &http.Server{Addr: cfg.MetricsAddr, Handler: adapterhttp.NewMetricsHandler(metrics) /* …timeouts… */}
	}

	return &App{
		server: server, metricsServer: metricsServer, logger: logger, grace: cfg.ShutdownGrace,
		tlsCertFile: tlsCert, tlsKeyFile: tlsKey,
		rateLimiter: rateLimiter, traceShutdown: traceShutdown, registrar: registrar,
	}, nil
}

// resolveClock returns the runtime clock. WithClock (a test seam) wins; the
// PRODUCTION path always builds the MUTABLE memory.Clock adapter, frozen at the
// seed's systemTime when set (else tracking the wall clock until the control
// plane freezes it). memory.Clock satisfies BOTH oidc.Clock (for the domain) and
// controlapi.ClockController (so /_mock/clock/advance can push tokens past exp);
// the immutable oidc.FixedClock / oidc.SystemClock value types are reserved for
// unit tests only, never the running server.
func resolveClock(o options) oidc.Clock {
	if o.clock != nil {
		return o.clock
	}
	return memory.NewClock(o.seed.SystemTime, o.seed.SystemTimeFixed)
}

// resolveSigning: WithSigning wins; else build the crypto adapter from the seed
// (embedded 5-key RSA bundle merged with any JSON-supplied initialKeys; kid =
// issuerId; per-issuer lazy key materialization).
func resolveSigning(o options) (signingProvider, error) {
	if o.signing != nil {
		return o.signing, nil
	}
	return signing.NewProvider(signing.Config{Algorithm: o.seed.Algorithm, InitialKeys: o.seed.InitialKeys})
}

// staticAssets builds the httpapi asset config from the seed. httpapi.Assets is
// the SINGLE owner of login/static presentation paths (the domain LoginPolicy
// holds only the Interactive flag), resolving the earlier AssetConfig/Assets
// name split.
func staticAssets(s config.Seed) httpapi.Assets {
	return httpapi.Assets{LoginPagePath: s.LoginPagePath, StaticAssetsPath: s.StaticAssetsPath}
}
```

**Clock-port placement fix.** The earlier `resolveClock` returned the *immutable* `oidc.NewFixedClock`/`oidc.SystemClock` value types, which `/_mock/clock/advance` cannot mutate — making the PRD-C6 clock-control capability inoperable on a running server, and colliding with the `SystemClock{}` vs `SystemClock()` form. The running server now wires the mutable `memory.Clock` adapter (one instance satisfying `oidc.Clock` and `controlapi.ClockController`), seeded from `seed.SystemTime`. The same instance reaches the control plane through `registerOIDC` (below). `oidc.FixedClock`/`SystemClock` survive only as unit-test seams via `WithClock`.

`registerOIDC` is the parity of the template's `registerResources`, but with no `/v1` version group — the OIDC surface is the contract, not a versioned resource API (Contract §4 removes the version group). It mounts the HTTP adapter and the reserved-prefix control plane, threading the *same* mutable clock, the scenario queue, and the recorder into the control plane:

```go
// registerOIDC mounts every OIDC operation and the control plane. There is no
// version prefix: the OIDC endpoint paths ARE the public contract. Issuers are a
// runtime path parameter (Contract §8), never a static huma.NewGroup.
func registerOIDC(p *oidc.ProviderService, a *oidc.AuthorizeService, t *oidc.TokenService,
	s *oidc.SessionService, scenarios *memory.CallbackQueue, rec *memory.RequestRecorder,
	clock oidc.Clock, assets httpapi.Assets) adapterhttp.Registrar {
	return func(api huma.API) {
		// Field names are FROZEN to the http-adapter section: Tokens/Sessions
		// plural; the recorder is NOT a Deps field (it flows through the recording
		// middleware above).
		httpapi.Register(api, httpapi.Deps{Provider: p, Authorize: a, Tokens: t, Sessions: s, Assets: assets})

		// Control plane under the reserved /_mock prefix (Contract §8): huma.NewGroup
		// applies controlapi.Prefix ("/_mock") ONCE; controlapi registers RELATIVE
		// paths beneath it (no double-prefix). Clock control is live only when clock
		// is the mutable memory.Clock; a unit-test FixedClock yields a nil
		// ClockController and an inert /_mock/clock — by design.
		ctl, _ := clock.(controlapi.ClockController)
		controlapi.Register(huma.NewGroup(api, controlapi.Prefix), controlapi.Deps{
			Tokens:    t,
			Scenarios: scenarios, // memory.CallbackQueue satisfies controlapi.ScenarioStore (write side)
			Requests:  rec,       // memory.RequestRecorder satisfies controlapi.RequestLog (read side)
			Clock:     ctl,
		})
	}
}
```

This freezes the cross-section disagreements: `httpapi.Deps` is `{Provider, Authorize, Tokens, Sessions, Assets}` (recorder removed — it reaches capture via the middleware, not Deps); `controlapi.Deps` is `{Tokens, Scenarios, Requests, Clock}`; and the prefix is applied exactly once via `huma.NewGroup(api, controlapi.Prefix)` with relative control routes (issuer in the request body, per the control-plane section), never absolute `/_mock/...` paths on the bare api.

`OpenAPIYAML` drops the authz `DocumentSecurity` finalizer and the noop repository, building services over the in-memory adapters with a fixed-key signing provider so the spec is deterministic:

```go
// OpenAPIYAML builds the API without binding a listener and returns the spec as
// YAML. No DB, no authz finalizer; a deterministic signing provider keeps the
// committed spec stable. OAuth2/openIdConnect SecuritySchemes are stamped post-
// register via httpapi.DocumentSecurity (the same Paths-walk stamping pattern the
// template used for authz, not a nonexistent Operations() accessor).
func OpenAPIYAML(version string) ([]byte, error) {
	a, err := New(context.Background(), config.Config{Addr: ":0"}, slog.New(slog.DiscardHandler), version,
		WithSeed(config.DefaultSeed()), WithSigning(signing.NewDeterministicProvider()))
	if err != nil {
		return nil, fmt.Errorf("build app for spec: %w", err)
	}
	return adapterhttp.SpecYAML(version, a.registrar, httpapi.DocumentSecurity)
}
```

### 6. Graceful shutdown & TLS reuse

`Run`/`shutdown` are reused verbatim except: `closePool` is **deleted** (no DB), and the listen call honors TLS. `stopRateLimiter` and `shutdownTracing` stay (both no-op when their feature is off).

```go
func (a *App) Run(ctx context.Context) error {
	// No pool to close; only the rate limiter (if any) and the tracer provider.
	defer a.stopRateLimiter(ctx)
	defer a.shutdownTracing(ctx)

	servers := a.servers()
	serveErr := make(chan error, len(servers))
	for _, s := range servers {
		go func() {
			a.logger.InfoContext(ctx, s.name+" listening", slog.String("addr", s.server.Addr))
			err := a.listen(s.server)
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				serveErr <- fmt.Errorf("%s: %w", s.name, err)
				return
			}
			serveErr <- nil
		}()
	}

	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
		return a.shutdown(ctx)
	}
}

// listen serves TLS when a certificate is configured. An empty cert/key with TLS
// requested is filled by a generated self-signed localhost certificate before
// New returns (resolveTLS), so by here the files always exist when tlsCertFile
// is set.
func (a *App) listen(s *http.Server) error {
	if a.tlsCertFile != "" {
		return s.ListenAndServeTLS(a.tlsCertFile, a.tlsKeyFile)
	}
	return s.ListenAndServe()
}

// resolveTLS returns the cert/key paths the listener will use. TLS off → both
// empty (plain HTTP). TLS on with explicit files → those files. TLS on with no
// files (upstream ssl:{} parity) → a freshly generated self-signed localhost
// certificate (RSA-2048, CN=localhost, SAN localhost+127.0.0.1) written to a
// temp PEM pair, so listen() always finds files when tlsCertFile is non-empty.
func resolveTLS(cfg config.Config) (certFile, keyFile string, err error) {
	if !cfg.TLSEnabled && cfg.TLSCertFile == "" {
		return "", "", nil
	}
	if cfg.TLSCertFile != "" {
		return cfg.TLSCertFile, cfg.TLSKeyFile, nil
	}
	return generateSelfSignedLocalhost() // writes a temp cert/key pair, returns their paths
}
```

The metrics server stays plain HTTP regardless of TLS (it is an operational surface, not part of the OIDC contract).

### 7. CLI & entrypoint

The Cobra tree keeps root=serve, `serve`, `version`, `openapi`; the `migrate` command and its `AddCommand` are **removed** (no DB). `serve` now loads both config layers and passes the seed; it ORs the JSON `ssl:{}` flag into `Config.TLSEnabled` *after* `Validate`, so a JSON-enabled, no-cert TLS run is honored and the self-signed cert is generated in `New`.

```go
// runServe loads server config and the OIDC seed, builds the logger, and runs
// the server until the command context is cancelled (SIGINT/SIGTERM).
func runServe(cmd *cobra.Command, options Options) error {
	cfg := config.Load(options.Viper)
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}

	seed, err := config.LoadSeed(options.Viper)
	if err != nil {
		return fmt.Errorf("load OIDC config: %w", err)
	}
	cfg.TLSEnabled = cfg.TLSEnabled || seed.TLSFromHTTPServer // ssl:{} in JSON turns TLS on

	logger := observability.NewLogger(options.Err, observability.ParseLevel(cfg.LogLevel), cfg.LogFormat)
	application, err := app.New(cmd.Context(), cfg, logger, options.Build.Version, app.WithSeed(seed))
	if err != nil {
		return fmt.Errorf("build application: %w", err)
	}
	if err := application.Run(cmd.Context()); err != nil {
		return fmt.Errorf("run server: %w", err)
	}
	return nil
}
```

Root-command deltas (`internal/cli/root.go`): `Use: "mock-oidc"`, env prefix `MOCK_OIDC`, and the `newMigrateCommand` registration deleted:

```go
root := &cobra.Command{
	Use:           "mock-oidc",
	Short:         "Mock OAuth2/OIDC authorization server for testing",
	Long:          "mock-oidc runs a for-testing-only OIDC server that mints real, signed JWTs.",
	Version:       options.Build.Version,
	SilenceUsage:  true,
	SilenceErrors: true,
	PersistentPreRunE: func(cmd *cobra.Command, _ []string) error { return initializeConfig(cmd, options.Viper) },
	RunE:              func(cmd *cobra.Command, _ []string) error { return runServe(cmd, options) },
}
config.RegisterFlags(root.PersistentFlags())
root.AddCommand(newServeCommand(options))
root.AddCommand(newVersionCommand(options))
root.AddCommand(newOpenAPICommand(options))
// newMigrateCommand removed: mock-oidc has no database.
```

**Standalone defaults** (zero-config, PRD P2/C8): with no flags, no env, and no `config.json`, the server listens on `:8080`, logs JSON at `info`, rate limiting OFF, tracing OFF, **CORS in zero-config reflect-origin mode** (request Origin reflected, credentials allowed — upstream browser parity), and `DefaultSeed()` gives **interactive login ON** (standalone parity) with the embedded 5-key RSA signing bundle and the live, runtime-advanceable `memory.Clock`. `/isalive`, `/healthz`, `/readyz` (always ready), and `/metrics` are the root infra routes; the control plane lives under `/_mock`. Issuers are materialized on demand on first reference — no pre-registration.

The entrypoint moves to `cmd/mock-oidc/main.go`; its body is unchanged from the template except for the package import path and the binary name in `cli.NewRootCommand`. Signal handling (`signal.NotifyContext(SIGINT, SIGTERM)`), ldflag `version/commit/date` injection, and `ExecuteContext` are reused verbatim.

### 8. Removals & ripples (this section's surface)

| Removed | Replacement / note |
|---|---|
| `config.DatabaseURL`, `DBMaxConns`, `AuthzEnabled`, `AuthzPolicyDir` + their flags/defaults/validation | gone; OIDC seed + server-only fields |
| `app.resolveStore` / `pool` / `closePool` / `WithRepository` | in-memory adapters; no pool to close |
| `app.authzInstaller` / `resolveAuthenticator` / `WithAuthenticator` / authz `DocumentSecurity` | removed; no caller authentication |
| `app` `/v1` version group (`registerResources`) | `registerOIDC` mounts at root + `/_mock` (via `huma.NewGroup(api, controlapi.Prefix)`) |
| `cli/migrate.go` + `root.AddCommand(newMigrateCommand)` | removed; DB-less |
| `rate-limit-enabled` default `true`, required validation | default `false`, validated only when enabled |
| env prefix `TEMPLATE_GO_API`, binary `template-go-api` | `MOCK_OIDC`, `mock-oidc` (Contract §1) |
| immutable `oidc.NewFixedClock`/`SystemClock()` in the run path; `Recorder` in `httpapi.Deps`; `RouterDeps.LivenessAlias`; `oidc.ParseClaimTemplate(rawJSON)` | mutable `memory.Clock`; recorder via recording middleware; `/isalive` mounted by the kept router; claim template ordered-decoded at the config edge |

Mechanical rename ripples (Contract §1, **note only**): `.mockery.yaml` package keys repoint from the authz/todo/postgres ports to `internal/oidc` ports (§4 ADD `internal/oidc/mocks`); `cmd/` dir, `go.mod` module path, `.golangci.yml` local-prefixes, `moon.yml` build/openapi paths, and the release-chain identifiers follow the global substitution. `go mod tidy` drops `pgx`, `goose`, `cedar-go`, and likely `uuid`.

---

**Key files this section governs (absolute paths):**
- `/Users/josh/code/meigma/mock-oidc/internal/config/config.go` — process `Config`, quad-pattern, address/TLS resolution, CORS-override semantics.
- `/Users/josh/code/meigma/mock-oidc/internal/config/jsonconfig.go` *(new)* — JSON DTOs, `Seed` (incl. `TLSFromHTTPServer`), `LoadSeed`, source precedence, edge-decoded claim templates, domain mapping.
- `/Users/josh/code/meigma/mock-oidc/internal/app/app.go` — composition root, Option seams, adapter wiring, mutable `memory.Clock`, `registerOIDC` (frozen Deps + `/_mock` group), `staticAssets`, `resolveTLS`, `OpenAPIYAML`.
- `/Users/josh/code/meigma/mock-oidc/internal/app/serve.go` — `Run`/`shutdown` (closePool deleted, TLS `listen` added).
- `/Users/josh/code/meigma/mock-oidc/internal/cli/root.go` — `mock-oidc` tree, `MOCK_OIDC` prefix + parity `BindEnv`, migrate removed.
- `/Users/josh/code/meigma/mock-oidc/internal/cli/serve.go` — loads both config layers, ORs `ssl:{}` into TLS, passes `WithSeed`.
- `/Users/josh/code/meigma/mock-oidc/internal/cli/migrate.go` — **deleted**.
- `/Users/josh/code/meigma/mock-oidc/cmd/mock-oidc/main.go` *(moved)* — entrypoint, signals, ldflag version.

## Implementation Roadmap to Parity

> This section sequences the build as a series of **vertical hexagonal slices**. Each slice is end-to-end (typed core → ports → adapters → composition root → container), independently shippable, and provably done against the parity catalog. The ordering is driven by one rule: **establish every architectural seam in the first functional slice, then widen the surface through those same seams** — no slice introduces a new layer, only new use-cases over the layers Slice 1 fixes in place. Type names, package paths, ports, and the error model are those of the Design Contract (§2, §6, §7); this section does not re-derive them, it schedules them.

### 8.1 Capability → Slice Map

| Cap (PRD §7) | Pri | S0 | S1 | S2 | S3 | S4 | S5 | S6 |
|---|---|:--:|:--:|:--:|:--:|:--:|:--:|:--:|
| C1 Standards-based token issuance | P0 | | ●seed | ◐id_token | | | | |
| C2 Sign-in patterns | P0/P1 | | ●client_credentials (m2m, P0) | ●authorization_code (interactive, P0) | ◐refresh (P1) | ◐jwt-bearer / token-exchange / password (P1) | | |
| C3 Scriptable deterministic identity | P0 | | ◐clock + default claims + config seed | ◐login claims + PKCE | | | ●dynamic scenarios/callbacks | |
| C4 Multiple providers per instance | P1 | | ◐routing seam (`/{issuer}`) | | | | ●concurrent issuers + key isolation | |
| C5 Full token lifecycle | P1 | | | | ●userinfo/introspect/revoke/endsession | | | |
| C6 Test-time control & inspection | P1 | | | | | | ●control API (mint/enqueue/capture) | |
| C7 Human-friendly local exploration | P2 | | | ◐login page (concept) | | | ●debugger playground | |
| C8 Container-first, low config | P0 | ◐walking skeleton | ●zero-config `default` issuer | | | | | ●packaging polish |
| C9 Realistic operating conditions | P1 | | ◐`ResolveBaseURL` seam | | | | | ●proxy / TLS / CORS hardening |
| C10 Safe positioning | P0 | ◐banner + labels | ◐discovery/notice | | | | | ●final docs/UX |

`●` = capability substantially delivered in this slice; `◐` = seam or partial increment. P0 capabilities (C1, C2-common, C3, C8, C10) are all reachable by the end of **Slice 2**; that is the first "credible parity" waypoint. P1/P2 fan out in Slices 3–6.

### 8.2 Sequencing Rationale

1. **Slice 0 first** because the rename + amputation (Contract §1, §4) must land before any new package compiles cleanly; it produces a *walking skeleton* (infra routes only) that proves the kept transport/observability/CLI still boot under the new module path.
2. **Slice 1 is the tracer bullet.** `client_credentials` is chosen as the first grant (not `authorization_code`) precisely because it is the *minimal* path that still touches **every seam**: routing → parse-don't-validate edge → `TokenService` over `Signer`/`KeyStore`/`IssuerRegistry`/`Clock` ports → DTO mapping → OAuth2 error writer → discovery/JWKS so a real client library can self-configure and verify. It needs no code cache, no login HTML, no refresh store — so the architecture is proven before the protocol breadth arrives.
3. **Slice 2** adds the only other P0 sign-in pattern (interactive `authorization_code`) plus `id_token`, completing the C1/C2/C3 P0 core. It introduces exactly one new outbound port (`CodeStore`) and the HTML adapter, reusing every Slice 1 seam.
4. **Slices 3–4** widen grants and lifecycle endpoints (C5, C2-P1) — additive handlers and two stores (`RefreshTokenStore`, `TokenVerifier`), no new architecture.
5. **Slice 5** turns on the *scriptable* dimension (C3 dynamic, C4, C6, C7): the callback-resolution priority chain, multi-issuer key isolation, the control plane, and the debugger. It is deliberately late because it depends on the token-minting pipeline being correct first.
6. **Slice 6** hardens the edges teams actually deploy into (C9) and finishes distribution (C8/C10). Pure adapter/edge work — the domain is frozen by then.

### 8.3 The Definition-of-Done Ladder

Every slice clears the same four rungs before it is "done". Functional and container rungs are non-negotiable per TECH_NOTES (functional tests before *complete*; container-based where it proves real behavior):

| Rung | Altitude | Mechanism | Proves |
|---|---|---|---|
| **R1 Unit** | typed core (`internal/oidc`) | `go test`, table-driven, ports replaced by `internal/oidc/mocks` | Constructors reject bad input; services orchestrate ports correctly; invariants hold. |
| **R2 Functional** | wired adapter | `httptest.Server` over the real `httpapi` Registrar + real `signing`/`memory` adapters | The HTTP contract (status, headers, JSON/JWT bytes) is correct end-to-end in-process. |
| **R3 Container** | built image | `testcontainers-go` runs `ghcr.io/meigma/mock-oidc` and drives it with an off-the-shelf client lib | The *shipped artifact* behaves; zero-config boot works; real verification against published JWKS. |
| **R4 Gates green** | repo | `moon run check` (lint, `openapi-check`, `mockery-check`, build) | No spec/mocks drift; layering rule holds (an import-restriction rung, see §8.11). |

Slice 0 must clear R3/R4 (skeleton boots in a container, gates green). Slices 1–6 must clear **all four**, and each slice adds a **container-level parity assertion** taken from the catalog (a "golden" the upstream container also satisfies, except where parity-in-intent deliberately diverges — those divergences get an explicit *negative* parity test asserting the corrected behavior).

---

### 8.4 Slice 0 — Skeleton & Amputation

**Goal.** Rename the module to `github.com/meigma/mock-oidc`, remove the auth/authz and Postgres layers, and ship a *walking skeleton*: the kept chi+Huma transport, observability, CLI, and config boot in a container exposing only infra routes. No OIDC behavior yet — this slice de-risks the mechanical churn so Slice 1 starts from a clean, green tree.

**Delivered.** C8 (partial — image boots, `/healthz`/`/readyz`/`/metrics` respond, `/isalive` parity alias), C10 (partial — "for testing only" boot banner + OCI image labels).

**Deferred.** All OIDC endpoints and the `internal/oidc` tree (Slice 1+).

**New / changed.**
- *Removed* (Contract §4): `internal/authz/**`, `internal/todo/**`, `internal/adapter/postgres/**`, `internal/cli/migrate.go`, sqlc/goose/compose-pg, the `/v1` resource group, authz Option/Registrar finalize hooks. Config loses `DatabaseURL`, `DBMaxConns`, `AuthzEnabled`, `AuthzPolicyDir`. `go mod tidy` drops cedar-go, pgx, goose, uuid.
- *Trimmed edges*: `app.options` shrinks to OIDC seams (no `repo`/`authenticator`); `resolveStore`/`closePool`/`shutdownPool` deleted; `Readiness` is empty so `/readyz` is unconditionally ready (Contract §4).
- `cmd/mock-oidc/main.go`, env prefix `MOCK_OIDC_`, parity env aliases (`SERVER_PORT` > `PORT` > 8080; `JSON_CONFIG` > `JSON_CONFIG_PATH` > `./config.json`) wired via explicit `viper.BindEnv` in `internal/cli`.

```go
// internal/oidc/doc.go — created empty-of-behavior in S0, the layering contract is stated up front.
//
// Package oidc is the domain core of mock-oidc: value types, invariants, typed
// errors, outbound ports, and application services for an OAuth2/OIDC provider.
//
// Dependency rule (Contract §3): this package MAY import only the standard
// library, log/slog, and internal/logctx. It MUST NOT import huma, chi, net/http,
// net/url, the crypto signing/JOSE packages (crypto/rsa, crypto/ecdsa,
// crypto/ed25519, crypto/tls, crypto/x509, go-jose), viper, cobra, otel,
// prometheus, or any internal/adapter/*. The keyless crypto/sha256, crypto/subtle,
// and encoding/base64 transforms used by PKCE (code.go) are a documented carve-out.
// Crypto, HTTP, and IO are reached only through the outbound ports declared in
// ports.go and clock.go.
package oidc
```

**Definition of Done.**
- R3: `testcontainers-go` brings up the renamed image; asserts `GET /isalive` → 200 and `/healthz`,`/readyz`,`/metrics` respond; container logs the positioning banner.
- R4: `moon run check` green under the new module path; `openapi-check` regenerates with title `mock-oidc`; `mockery-check` passes with the repointed (currently empty) `packages:`.
- A grep gate: no occurrence of the old module path, `authz`, or `postgres` remains outside the untouched tooling files enumerated in Contract §1.

---

### 8.5 Slice 1 — Core Token Pipeline (the Tracer Bullet)

**Goal.** A zero-config `default` issuer that publishes Discovery + JWKS and mints a real, signed `client_credentials` access token with default claims — verifiable by an unmodified client library. This slice **stands up every hexagonal seam** the rest of the build reuses.

**Delivered.** C1 (token issuance + discovery + JWKS), C2 machine-to-machine (`client_credentials`, P0), C3 partial (frozen `Clock`, default-claim policy, `systemTime`/`initialKeys`/`algorithm` config seed), C8 (zero-config default issuer; `iss` derived from the request), C10 (discovery served, "test-only" surfaced), C4 routing seam (`/{issuer}/...` path param accepted, only `default` exercised).

**Deferred.** All other grants; `id_token`/`refresh_token`; interactive login; userinfo/introspect/revoke/endsession; multi-issuer concurrency & per-issuer key isolation (one key path proven, isolation hardened in S5); proxy header precedence & TLS/CORS (S6); scenarios/callbacks beyond `DefaultTokenCallback` (S5).

**New types** (Contract §7, the subset Slice 1 needs): `IssuerID`, `Issuer`, `BaseURL`, `RequestOrigin` + `ResolveBaseURL`, `ClientID`, `ClientAuth`, `Client`, `Subject`, `Scope`/`Scopes`, `GrantType` (full closed set declared; only `client_credentials` dispatched), `TokenType`, `SigningAlgorithm` (+supported/rejected sets), `KeyID`, `ClaimSet`, `Token`, `SignedToken`, `JWTHeader`, `SigningKey`, `JWK`/`JWKS`, `DiscoveryDocument` (fixed field order), `TokenRequest`/`TokenResponse` (cc shape), `ProtocolError`/`ErrorCode`, `Instant`, `DefaultTokenCallback`.

**New ports** (`ports.go`, `clock.go`): `KeyStore`, `Signer`, `IssuerRegistry`, `Clock`.

**New adapters**: `internal/oidc/signing` (`Signer`+`KeyStore`; RSA-2048; embedded 5-key RSA JWKS seed; `kid = issuerId`), `internal/oidc/memory` (`IssuerRegistry`, mutable `Clock`), `internal/oidc/httpapi` (`register.go`, `discovery.go`, `jwks.go`, `token.go`, `dto.go`, `mapping.go`, the flat `form.go` parser, `oautherr.go`). Services: `ProviderService`, `TokenService`. Edge: `internal/oidc/mocks` for the four ports; `app` gains functional Options wiring the adapters; `config` parses the first three JSON-config fields into a typed seed.

The seams, in code:

```go
// internal/oidc/issuer.go — parse-don't-validate at construction; reserved-prefix rule (Contract §8).
type IssuerID string

const reservedPrefix = "_mock"

// ParseIssuerID validates the first path segment as an issuer identity. It is the
// SINGLE issuer constructor used by every adapter (httpapi, controlapi, config),
// and it ALWAYS returns *ProtocolError (never a bare sentinel) so the OAuth2 error
// writer's errors.As(&*ProtocolError) reaches it and the corrected status/text wins.
func ParseIssuerID(s string) (IssuerID, error) {
    switch {
    case s == "":
        return "", MissingParameter("issuer") // *ProtocolError{CodeInvalidRequest, 400}
    case strings.ContainsRune(s, '/'):
        return "", MalformedRequest("issuer must not contain '/'") // *ProtocolError{CodeInvalidRequest, 400}
    case s == reservedPrefix || strings.HasPrefix(s, reservedPrefix+"/"):
        // *ProtocolError{CodeNotFound, 404} that ALSO wraps the ErrReservedIssuer
        // sentinel, so control-surface's errors.Is(err, oidc.ErrReservedIssuer) and the
        // OAuth2 writer's errors.As(&*ProtocolError) both fire on the same value.
        return "", ReservedIssuer(s)
    }
    return IssuerID(s), nil
}
```

> **Named parity gap — single-segment issuers.** `/{issuer}` is a single chi path segment and `ParseIssuerID` rejects values containing `/` (Contract §7/§8). Upstream derived `issuerId` as everything *before* the matched endpoint suffix, so it accepted multi-segment / deeply-nested IDs (e.g. `tenant/sub/default`). We do **not** replicate that: nested issuer IDs are a **named, accepted gap**, not a silent divergence — common usage is single-segment and the single-segment form is what is OpenAPI-expressible. Adopters with Azure-style nested issuers (`aad/{tenant}/v2.0`) are warned in the docs (Slice 6, C10). This is exercised/documented when multi-issuer lands (§8.9).

```go
// internal/oidc/ports.go — outbound ports, declared by their consumer (the services).
type KeyStore interface {
    // SigningKey returns the issuer's public key metadata, materializing the key
    // (kid = issuer) on first reference — computeIfAbsent semantics (catalog).
    SigningKey(ctx context.Context, issuer IssuerID) (SigningKey, error)
    // PublicKeys returns the issuer's JWKS (public params only).
    PublicKeys(ctx context.Context, issuer IssuerID) (JWKS, error)
}

type Signer interface {
    // Sign serializes and signs an unsigned Token, producing the compact JWT.
    Sign(ctx context.Context, issuer IssuerID, tok Token) (SignedToken, error)
    // Algorithms reports the algorithms this signer can actually produce — discovery
    // (built from SupportedSigningAlgorithms()) is cross-checked against this set so
    // the build fails on drift (Contract §6).
    Algorithms() []SigningAlgorithm
}

type IssuerRegistry interface {
    // Resolve returns (materializing on demand) the issuer for id at origin.
    Resolve(ctx context.Context, id IssuerID, origin RequestOrigin) (Issuer, error)
}
```

```go
// internal/oidc/token.go (service) — orchestrates ports + pure values, nothing else.
func (s *TokenService) IssueClientCredentials(ctx context.Context, req TokenRequest) (TokenResponse, error) {
    // req.Issuer is the Issuer resolved by Issue() via IssuerRegistry from the RequestOrigin.
    cb, err := s.callbacks.Resolve(ctx, req.Issuer.ID, callbackInput(req)) // S1: always DefaultTokenCallback; S5 widens this.
    if err != nil {
        return TokenResponse{}, err
    }
    now := s.clock.Now()

    sub := req.Client.ID.AsSubject() // cc: sub defaults to client_id (catalog)
    claims := cb.AccessTokenClaims(req, sub, now)
    tok, err := NewToken(req.Issuer.ID, s.alg, claims) // builds JWTHeader{alg, kid=issuer, typ=JWT}
    if err != nil {
        return TokenResponse{}, err
    }
    signed, err := s.signer.Sign(ctx, req.Issuer.ID, tok)
    if err != nil {
        return TokenResponse{}, fmt.Errorf("sign access token: %w", err)
    }
    return TokenResponse{
        TokenType:   TokenTypeBearer,
        AccessToken: signed,
        ExpiresIn:   claims.ExpiresIn(now), // derived from exp via the SAME Clock as iat/nbf/exp —
                                            // frozen-clock determinism (PRD P3), correcting upstream's real-time recompute
        Scope:       req.Scopes,            // echoed
    }, nil // no id_token / refresh_token for cc
}
```

```go
// internal/oidc/httpapi/token.go — the parse-don't-validate edge + success-shaped OAuth2 error (Contract §5.4, §6).
type tokenInput struct {
    Issuer  string `path:"issuer"`
    RawBody []byte `contentType:"application/x-www-form-urlencoded"` // Huma stores bytes; we parse.
}
type tokenOutput struct {
    Status int
    Body   any // TokenResponseDTO on success; OAuth2Error on protocol failure — both success-shaped (§5.4), never a Go error.
}

func (h *handlers) token(ctx context.Context, in *tokenInput) (*tokenOutput, error) {
    issuer, err := oidc.ParseIssuerID(in.Issuer)
    if err != nil {
        return h.oauth2(err), nil // ProtocolError → per-op OAuth2Error output; never a Go error / RFC 9457
    }
    cmd, err := parseTokenRequest(issuer, in.RawBody) // flat form.go parser → typed TokenRequest
    if err != nil {
        return h.oauth2(err), nil
    }
    resp, err := h.tokens.Issue(ctx, h.origin(ctx), cmd) // resolves Issuer from origin; dispatches by GrantType
    if err != nil {
        return h.oauth2(err), nil
    }
    return &tokenOutput{Status: 200, Body: toTokenResponseDTO(resp)}, nil
}

// oauth2 translates any error (ProtocolError via errors.As; otherwise server_error/500)
// into the success-shaped OAuth2 output — correct-case JSON, NOT lowercased (Contract §6).
func (h *handlers) oauth2(err error) *tokenOutput {
    st, body := h.oautherr.Translate(err)
    return &tokenOutput{Status: st, Body: body}
}
```

```go
// internal/app — the wiring seam: ports → adapters via functional Options (mirrors the kept template).
func New(ctx context.Context, cfg config.Config, logger *slog.Logger, version string, opts ...Option) (*App, error) {
    o := defaults(cfg) // signing.NewKeyStore(seed), signing.NewSigner, memory.NewIssuerRegistry,
                       // memory.NewClock(cfg.SystemTime) — the MUTABLE clock adapter (oidc.Clock +
                       // controlapi.ClockController) so S5's control plane can advance time.
    for _, opt := range opts { opt(&o) }

    provider := oidc.NewProviderService(o.issuers, o.keys, o.clock)
    tokens   := oidc.NewTokenService(o.signer, o.keys, o.callbacks, o.clock)
    register := httpapi.Register(httpapi.Deps{Provider: provider, Tokens: tokens /* … */})
    // …NewRouter(RouterDeps{…, Register: register}) — authz hooks gone (Contract §4).
}

// WithClock injects a clock for tests (the immutable oidc.FixedClock froze iat/nbf/exp);
// the running server defaults to the mutable memory.Clock — the systemTime seam (catalog).
func WithClock(c oidc.Clock) Option { return func(o *options) { o.clock = c } }
```

**Definition of Done.**
- R1: `ParseIssuerID`/`ParseSigningAlgorithm` reject the catalog's rejected algs (`ES256K`, `ES512`, `EdDSA`, HMAC, `none`) with typed `*ProtocolError`s; `TokenService.IssueClientCredentials` table tests over mocked `Signer`/`KeyStore` assert default claims (`sub`,`aud`,`iss`,`iat`,`nbf`,`exp`,`jti`, `tid=issuer`) and the cc matrix (access-token only, `sub=client_id`, `aud` 4-step precedence → `["default"]`).
- **Constant-sync test** (Contract §6): `discovery.id_token_signing_alg_values_supported` (built from `SupportedSigningAlgorithms()`) `== signer.Algorithms()` — fails the build if discovery advertises an algorithm the signer cannot mint.
- R2: `httptest` server: `GET /default/.well-known/openid-configuration` returns the **fixed field order** (catalog §Discovery: `issuer, authorization_endpoint, end_session_endpoint, revocation_endpoint, token_endpoint, userinfo_endpoint, jwks_uri, introspection_endpoint`, then `*_supported`); `/default/jwks` returns the issuer key with `kid=default`,`use=sig`; `POST /default/token` with `grant_type=client_credentials` yields a JWT whose signature verifies against the served JWKS. OAuth2 errors are correct-case JSON (`error`,`error_description`) — **not lowercased** (parity-in-intent correction); `GET /default/token` → **405 with the uniform OAuth2-shaped error** (the bespoke `"unsupported method"` body is an upstream quirk we do **not** replicate — D-2).
- R3 (container parity): `testcontainers-go` boots the image with no config; a stock Go OIDC verifier (`coreos/go-oidc`-style: fetch discovery → fetch JWKS → verify) accepts a `client_credentials` token. This is the headline C1 promise proven against the shipped artifact.
- R4: `openapi-check`/`mockery-check` green; the four new ports appear in `mocks`.

---

### 8.6 Slice 2 — Authorization Code + Interactive Login + ID Token

**Goal.** Browser-based interactive sign-in: `GET /authorize` renders a login page (or issues a code directly), `POST /authorize` submits a login, and `authorization_code` exchange mints `id_token` + `access_token` + `refresh_token`. Completes the P0 core (C1/C2/C3).

**Delivered.** C2 interactive sign-in (P0), C1 `id_token`, C3 login-injected subject/claims + PKCE + `azp`/`nonce` semantics, C7 login *mechanism* (the controllable-login concept — UX deferred per PRD D-5).

**Deferred.** Refresh *redemption* (token is issued here but the refresh grant lands in S3); the debugger playground (S5); `RequestMappingTokenCallback` templating of login (`subject` synthetic param) — the login→callback merge contract is wired but mapping-driven login is exercised in S5; `loginPagePath` custom page (S6 config polish, low risk).

**New types**: `ResponseType`, `ResponseMode`, `AuthorizationCode` + `CodeRecord` (request snapshot: nonce, PKCE challenge, redirect_uri, scopes, client), `AuthorizeRequest`, `LoginSubmission`, `AuthorizeResult`, the `id_token`/`refresh_token` fields of `TokenResponse`. **New port**: `CodeStore`. **New service**: `AuthorizeService`. **New adapters**: `httpapi/authorize.go`, `httpapi/login.go`, `httpapi/html/{login.html,form_post.html,error.html}`, `memory.CodeStore`. PKCE compute lives in the pure core (`code.go`) under the documented `crypto/sha256`+`crypto/subtle`+`encoding/base64` carve-out (§8.11).

```go
// internal/oidc/ports.go (added) — single-use code cache, consumer-declared.
type CodeStore interface {
    // Save stores a single-use record under code.
    Save(ctx context.Context, code AuthorizationCode, rec CodeRecord) error
    // Take atomically returns and REMOVES the record — single-use, removed
    // before the PKCE check so a failed attempt also invalidates the code (catalog).
    Take(ctx context.Context, code AuthorizationCode) (CodeRecord, error)
}
```

```go
// internal/oidc/authorize.go — interactive-vs-code decision is a domain concern, not the handler's.
func (s *AuthorizeService) Authorize(ctx context.Context, req AuthorizeRequest) (AuthorizeResult, error) {
    if req.ResponseType != ResponseTypeCode { // hybrid/implicit advertised-only
        return AuthorizeResult{}, InvalidGrantf("response_type %q not supported", req.ResponseType)
    }
    if s.interactiveLogin || req.Prompt.RequiresLogin() { // prompt ∈ {login,consent,select_account}; none → false
        return AuthorizeResult{Kind: AuthorizeShowLogin, Request: req}, nil
    }
    return s.issueCode(ctx, req, Subject(req.LoginHint)) // direct code, no page
}

// PKCE enforced ONLY when a verifier is presented; code already removed (catalog gotcha).
func (rec CodeRecord) VerifyPKCE(verifier string) error {
    if verifier == "" || rec.Challenge == "" {
        return nil
    }
    if !rec.ChallengeMethod.Compute(verifier).Equal(rec.Challenge) {
        return InvalidGrant("invalid_pkce: code_verifier does not compute to code_challenge from request")
    }
    return nil
}
```

`AuthorizeResult` carries one of three kinds — `AuthorizeShowLogin` / `AuthorizeFormPost` / `AuthorizeRedirect` — plus typed fields (`Code`, `State`, `RedirectURI`, `Mode`); the **adapter** renders query/fragment/`form_post` and owns url-encoding at the edge (the domain never builds the redirect string). The 302 vs `form_post` split is the Contract §5.2 / §5.3 mechanics; the `form_post`-without-`state` upstream 500 is **not** replicated (parity-in-intent — empty `state` is tolerated).

```go
// internal/oidc/httpapi/authorize.go — 302 success output; errors route THROUGH the redirect when a usable redirect_uri exists (Contract §6).
type authorizeRedirect struct {
    Status   int
    Location string `header:"Location"`
}
// Operation.DefaultStatus = 302; query params taken as permissive strings so the
// HANDLER decides redirect-with-error vs direct OAuth2 error. The adapter renders
// Location from the typed AuthorizeResult fields (Code/State/RedirectURI/Mode).
```

**Definition of Done.**
- R1: `AuthorizeService.Authorize` table tests for the interactive trigger matrix (config flag OR `prompt`, `prompt=none` ⇒ no page); `CodeRecord.VerifyPKCE` plain & S256, and the single-use invariant (second `Take` → `invalid_grant "unknown or already-used authorization code"`); login-claims merge uses `putIfAbsent` (mapping/`sub` wins, login claims add-only — the 5.0.0 contract).
- R2: `httptest`: `GET /default/authorize` (interactive on) returns the login HTML with a `username`/`claims` form posting to the same URL; `POST /default/authorize` → 302 carrying `code`+`state`; `form_post` mode → 200 self-submitting HTML POSTing only `code`+`state`; exchange yields `id_token aud=[client_id]` + `azp=client_id` (auth_code only) + `nonce` from the **cached** request; `refresh_token` present (bare UUID, or unsigned `alg=none` PlainJWT when nonce present).
- R3 (container parity): drive the full auth-code flow against the container with a real client (authorize redirect → code → token), verify the `id_token` against published JWKS; assert PKCE S256 round-trips and a tampered verifier → `invalid_grant`.
- R4: spec carries `/{issuer}/authorize` with permissive string params; `CodeStore` mocked.

---

### 8.7 Slice 3 — Token Lifecycle Services

**Goal.** Everything a relying app does *after* it has a token: `refresh_token` redemption, `userinfo`, `introspect`, `revoke`, `endsession`. Closes C5 and the C2 refresh pattern.

**Delivered.** C5 full lifecycle, C2 renewal (`refresh_token`, P1).

**Deferred.** `rotateRefreshToken` config exists but defaults `false` (rotation behavior tested here, config plumbed); cross-issuer/scenario-driven refresh resolution priority that *also* consults the callback queue is wired but the queue itself arrives in S5 (S3 resolves stored-callback → default only).

**New types**: `RefreshToken` + `RefreshRecord` (bound callback + `Nonce` — the named `type Nonce string` used consistently across `ClaimSet`/`CodeRecord`/`RefreshRecord`), `IntrospectionRequest`/`IntrospectionResult`, `UserInfoRequest`, `RevocationRequest`, `EndSessionRequest`/`EndSessionResult`. **New ports**: `RefreshTokenStore`, `TokenVerifier`. **New service**: `SessionService`. **New adapters**: `httpapi/{userinfo,introspect,revoke,endsession}.go`, `signing.TokenVerifier`, `memory.RefreshTokenStore`.

```go
// internal/oidc/ports.go (added) — RefreshTokenStore is PERSIST-ONLY; the domain service
// mints the token value and decides its FORM, the adapter produces any compact bytes.
type RefreshTokenStore interface {
    // Save persists the record (bound callback + nonce) under tok for issuer. The
    // SERVICE chooses the form (bare UUID, or the unsigned alg=none PlainJWT form when
    // a nonce is present); the alg=none compact serialization is produced by the
    // signing adapter via the Signer port — the domain never serializes (Contract §7).
    Save(ctx context.Context, issuer IssuerID, tok RefreshToken, rec RefreshRecord) error
    // Lookup returns the record bound to tok. The service enforces issuer binding — a
    // token from issuer A presented to B is rejected with the CORRECTED client text.
    Lookup(ctx context.Context, issuer IssuerID, tok RefreshToken) (RefreshRecord, error)
    // Remove invalidates tok (revoke / rotation).
    Remove(ctx context.Context, tok RefreshToken) error
}

type TokenVerifier interface {
    // Verify checks signature, issuer, iat, exp and pins typ=JWT. now is threaded so
    // verification reads the SAME freezable Clock as issuance (catalog: one global
    // instant drives issuance AND verification — a control-plane clock advance moves
    // both). CONSCIOUS parity call (not a quirk we are forced to keep, PRD N8): a token
    // minted with a custom typ (e.g. RFC 9068 "at+jwt") therefore fails the server's own
    // /userinfo and /introspect. We preserve typ=JWT pinning for drop-in test
    // compatibility and document the at+jwt consequence; relaxing it is a post-parity option.
    Verify(ctx context.Context, issuer IssuerID, token SignedToken, now Instant) (ClaimSet, error)
}
```

```go
// internal/oidc/session.go — introspect: invalid signature is NOT an error (catalog).
func (s *SessionService) Introspect(ctx context.Context, req IntrospectionRequest) (IntrospectionResult, error) {
    claims, err := s.verifier.Verify(ctx, req.Issuer, req.Token, s.clock.Now())
    if err != nil {
        return IntrospectionResult{Active: false}, nil // {active:false}, 200 — not an error
    }
    return introspectionFromClaims(claims), nil
}
```

The refresh cross-issuer error MUST carry the corrected client text `"refresh_token was issued by a different issuer"` (Contract §6 / catalog correction), not the internal "issuer mismatch" — enforced by `SessionService`/`TokenService` after `Lookup`. `endsession` reads `post_logout_redirect_uri`/`state` from the **query only**; `revoke` accepts only `token_type_hint=refresh_token` (else `400 unsupported_token_type`).

**Definition of Done.**
- R1: `SessionService` tests over mocked `TokenVerifier`/`RefreshTokenStore` for: introspect invalid-sig → `{active:false}`; userinfo verify-fail → `401 invalid_token`; refresh re-mint (fresh `jti`/`iat`/`exp`, same subject); cross-issuer refresh → `invalid_grant` with the corrected text; revoke unknown hint → `unsupported_token_type`.
- R2: `httptest`: userinfo returns the **entire** claim set verbatim; introspect serializes a single-element `aud` as a string and defaults `token_type=Bearer`; missing `Authorization` on introspect → `400 invalid_client`; endsession with redirect → 302 `?state=…`, without → 200 "logged out"; a **custom-`typ`** token (minted via callback) fails userinfo (401) and introspect (`{active:false}`) — the documented self-verification caveat, consciously preserved.
- R3 (container parity): obtain a token, then refresh it, introspect both, hit userinfo, revoke the refresh token and assert reuse → `invalid_grant` — the full lifecycle against the container; with the clock advanced past `exp` (via the control plane, S5) the same Clock drives verification so an expired token introspects `{active:false}`.
- R4: `RefreshTokenStore`/`TokenVerifier` mocked; spec carries all four endpoints.

---

### 8.8 Slice 4 — Delegation & Legacy Grants

**Goal.** The remaining grant types: `urn:…:jwt-bearer` (OBO), `urn:…:token-exchange` (RFC 8693), and `password` (ROPC). Completes C2's P1 sign-in patterns.

**Delivered.** C2 delegation/exchange/legacy (P1).

**Deferred.** Nothing new architecturally — this is pure handler/minting breadth. (`actor_token`/`act` chains remain unimplemented, matching upstream.)

**New types/paths**: `IssuedTokenType` exchange issued-token-type URN; the `exchangeAccessToken` minting path (copies inbound claims, overrides `iss/exp/nbf/iat/jti/aud` + adds claims); `ClientAuth` `private_key_jwt` with **structural-only** validation. No new ports; reuses `Signer`/`Clock`/callbacks.

```go
// internal/oidc/token.go (service) — exchange/jwt-bearer copy inbound claims; signatures NOT verified, only parsed (catalog).
func (s *TokenService) exchange(ctx context.Context, req TokenRequest, inbound ClaimSet) (TokenResponse, error) {
    now := s.clock.Now()
    cb, err := s.callbacks.Resolve(ctx, req.Issuer.ID, callbackInput(req))
    if err != nil {
        return TokenResponse{}, err
    }
    claims := inbound.CopyWithOverrides(req.Issuer, cb, now) // iss/exp/nbf/iat/jti/aud + addClaims
    signed, err := s.signAccess(ctx, req.Issuer, claims)
    if err != nil { return TokenResponse{}, err }
    return TokenResponse{
        TokenType:        TokenTypeBearer,
        AccessToken:      signed,
        IssuedTokenType:  req.IssuedTokenType(), // exchange: ...:access_token ; jwt-bearer: omitted
        ExpiresIn:        claims.ExpiresIn(now),
        // token-exchange: scope is null (not echoed);
        // jwt-bearer: scope = req ?? assertion.scope ?? invalid_request (neither present → error)
    }, nil
}
```

`private_key_jwt` structural validation (lifetime ≤ 120s; `iss==sub==client_id`; exactly one accepted audience ∈ {issuerURL, tokenEndpointURL}) is a pure-domain `ClientAuth.ValidatePrivateKeyJWT` returning `invalid_request` on each failure — the assertion signature is deliberately *not* cryptographically verified.

**Definition of Done.**
- R1: per-grant token matrix (catalog §Token response): `jwt-bearer`/`token-exchange` → access-token only; `password` → access+id, no refresh, `sub=username`, password never validated; `private_key_jwt` structural checks each map to `invalid_request`; jwt-bearer with neither request nor assertion scope → `invalid_request`.
- R2: `httptest`: token-exchange copies subject_token claims and sets `issued_token_type`, `scope` absent; missing `assertion` → `invalid_request "missing required parameter assertion"`; `password` issues id+access.
- R3 (container parity): exchange a token and OBO-assert against the container; confirm copied claims and audience precedence.
- R4: grant dispatch map covers all six `GrantType`s; unknown → `invalid_grant "grant_type <x> not supported."`, blank → `invalid_request "missing required parameter grant_type"` (both produced as typed `*ProtocolError` at the parse edge so they surface as 400s, not server_error/500).

---

### 8.9 Slice 5 — Multi-Issuer, Scenarios & Test-Time Control

**Goal.** Turn on the *scriptable* dimension: concurrent independent issuers with isolated keys, the callback-resolution priority chain (enqueued scenario > config mapping > default), the **control plane** (PRD C6 against a running container), and the debugger playground.

**Delivered.** C4 multiple providers per instance (P1), C6 test-time control & inspection (P1), C3 dynamic per-scenario scripting (the second half of "at startup *and* against a running instance"), C7 debugger playground (P2).

**Deferred.** Nothing material toward parity remains after this slice except edge hardening (S6).

**New types**: `Scenario`, `RequestMappingCallback` (+ `RequestMapping` matching/`${…}` templating), `CapturedRequest`, the core-owned `FormParams` (`map[string][]string` with `Get`/`All`/`SpaceJoined` accessors — the typed, multi-valued replacement for `url.Values`, lives in `callback.go`/`requests.go`). **New ports**: `CallbackQueue`, `RequestRecorder` (both read-side narrow ports in the domain — see below). **New adapters**: `internal/oidc/controlapi` (RFC 9457; mounted under reserved `/_mock`), `httpapi/debugger.go` + debugger HTML, `memory.{CallbackQueue,RequestRecorder}`, the **multi-valued** `form.go` parser (converts `url.Values` → `oidc.FormParams` at the edge so `url.Values` never crosses inward), `config` `tokenCallbacks` parser. `ProviderService`/`memory.IssuerRegistry` harden to per-issuer key isolation + concurrency.

```go
// internal/oidc/ports.go (added) — the domain hosts only the READ-SIDE of each port (its
// sole core consumer). The write/drain surfaces (Enqueue / Take / List / Clear) live on the
// consumer-declared controlapi.ScenarioStore / controlapi.RequestLog. One memory.* type
// structurally satisfies both narrow ports; the composition root wires them. These ports are
// hosted in oidc for NEUTRAL sharing between two adapters (which must not import each other),
// NOT because the core consumes the write side.
type CallbackQueue interface {
    // DequeueFor pops the head scenario ONLY if it matches issuer — issuer-matched-head,
    // single-use, FIFO; a queued scenario for A blocks B (catalog gotcha, preserved).
    DequeueFor(ctx context.Context, issuer IssuerID) (Scenario, bool, error)
}

type RequestRecorder interface {
    // Record stores a captured request; write-only on the core side. The read/drain side
    // (Take/List/Clear) is the consumer-declared controlapi.RequestLog facet.
    Record(ctx context.Context, cap CapturedRequest) error // raw bytes preserved
}
```

```go
// internal/oidc/callback.go — the resolution priority chain (catalog §customization model).
func (r *callbackResolver) Resolve(ctx context.Context, issuer IssuerID, in CallbackInput) (TokenCallback, error) {
    if sc, ok, err := r.queue.DequeueFor(ctx, issuer); err != nil {
        return nil, err
    } else if ok {                                    // 1. enqueued one-shot scenario
        return sc.Callback, nil
    }
    if cb, ok := r.config.Match(issuer, in); ok {     // 2. config RequestMappingTokenCallback (request-matched)
        return cb, nil
    }
    return NewDefaultTokenCallback(issuer), nil       // 3. default
}
```

```go
// internal/oidc/controlapi/register.go — C6 over a running container; RFC 9457 (Contract §6 B), reserved prefix (Contract §8).
// Mounted via huma.NewGroup(api, "/_mock") with RELATIVE paths (single prefix application — no double /_mock).
// Issuer is carried in the request BODY, never the path:
//   POST /_mock/scenarios       enqueue a one-shot scenario (pre-program next response)
//   POST /_mock/mint            direct mint for test setup (no full flow)
//   POST /_mock/requests/take   take/inspect what the app sent (assert outbound behavior)
//   POST /_mock/clock/advance   advance the freezable clock (control-plane time travel)
// This is the parity replacement for the JVM in-process enqueueCallback/issueToken/takeRequest,
// delivered against the container per PRD D-1 / C6.
```

Request capture is an **`httpapi`-level** concern (never `internal/adapter/http`, which stays resource-agnostic): the edge converts `r.URL.String()` and an explicit `map[string][]string` copy of the headers/query into `oidc.NewCapturedRequest(method string, rawURL string, header, query map[string][]string, body []byte)` — stdlib primitives only, never a `*url.URL`/`http.Header` value — guarded to record only the OIDC protocol families and skip `/_mock/*`, `/healthz`, `/readyz`, `/metrics`, `/openapi*`, `/docs`. Multi-issuer key isolation: distinct `IssuerID`s get distinct lazily-generated keys (`kid=issuerId`); the embedded 5-key seed is consumed FIFO before generation. The control API and `httpapi` recorder share the `RequestRecorder` so captures cover every inbound protocol request with **raw body bytes** preserved (param order matters — the catalog's `takeRequest` contract).

**Definition of Done.**
- R1: resolution priority table (enqueued > config > default; refresh grant consults the same queue); issuer-matched-head blocking; `RequestMapping` matching (`*` wildcard, regex-or-exact with silently-swallowed invalid regex, `client_id` un-shadowable) and `${…}` templating (string leaves only, space-joined multi-values via `FormParams.SpaceJoined`); `RequestMappingCallback` does **not** add `tid`/`azp`.
- R2: `httptest`: two issuers serve distinct JWKS (distinct `kid`); an enqueued scenario alters the *next* token for its issuer only; a config `tokenCallbacks` mapping templates `aud` from a request param; control `POST /_mock/mint` (issuer in body) mints a token without a flow; `POST /_mock/requests/take` returns the raw bytes of a prior `/token` POST; debugger `GET /default/debugger` renders the pre-filled form and the callback performs a real back-channel code exchange.
- R3 (container parity, the C6 headline): a `testcontainers-go` suite (the realistic test-author workflow) brings up the container, **enqueues a scenario over the control API** (`POST /_mock/scenarios`), drives the app-under-test, then **asserts the captured outbound request** (`POST /_mock/requests/take`) — proving test-time control + inspection work against a running container, not in-process.
- R4: `CallbackQueue`/`RequestRecorder` mocked; control routes carry RFC 9457 problem responses in the spec; `IssuerID` reserved-prefix rejection covers `/_mock` (and `errors.Is(err, oidc.ErrReservedIssuer)` holds alongside `errors.As(&*ProtocolError)`).

---

### 8.10 Slice 6 — Realistic Operating Conditions & Distribution

**Goal.** Make the mock behave in the topologies teams deploy it into, and finish the trustworthy-distribution story. Pure adapter/edge/packaging work — the domain is frozen.

**Delivered.** C9 (proxy-aware base URL with full header precedence, TLS, CORS reflection, container/`host.docker.internal` parity), C8 packaging polish (multi-arch image via the kept melange/apko/goreleaser chain, zero-config defaults documented), C10 final positioning (README, image labels, login/discovery notices).

**Deferred.** Post-parity only (UX redesign of login/debugger per PRD D-5; new features per N7).

**New / changed.** No new domain types or ports. `RequestOrigin` extraction at the transport edge (a NEW concern distinct from the kept IP-only `ClientIP` middleware); `ResolveBaseURL` precedence fully exercised; TLS termination + self-signed cert generation in the server bootstrap; a **default-ON** CORS interceptor (zero-config upstream semantics) reflecting `Origin` (not `*`) with `Access-Control-Allow-Credentials: true`, `Allow-Methods POST,GET,OPTIONS`, echoing `Access-Control-Request-Headers`, OPTIONS preflight → 204 on any path — `CORSAllowedOrigins` remains an optional *tightening* override, not the on/off switch; `loginPagePath`/`staticAssetsPath` config (static-asset traversal guard + non-contractual MIME via `mime.TypeByExtension`); melange/apko/goreleaser/release-please identifier substitution (the rename ripple noted in Contract §1).

```go
// internal/oidc/issuer.go — ResolveBaseURL is PURE domain: precedence per catalog §proxy resolution.
// Forwarded headers are client-controlled and malformable, so it returns (BaseURL, error).
// scheme: x-forwarded-proto > origin scheme
// host:   Host header host > origin host
// port:   x-forwarded-port > Host header port > scheme default(443/80) > origin port
func ResolveBaseURL(o RequestOrigin) (BaseURL, error) { /* host-root only; path dropped */ }
```

```go
// internal/oidc/httpapi — the EDGE extracts RequestOrigin from headers; the domain decides the URL.
func requestOrigin(r *http.Request) oidc.RequestOrigin {
    return oidc.RequestOrigin{
        Scheme:   schemeOf(r), Host: r.Host,
        FwdProto: r.Header.Get("X-Forwarded-Proto"),
        FwdHost:  r.Header.Get("X-Forwarded-Host"),
        FwdPort:  r.Header.Get("X-Forwarded-Port"),
    }
}
```

Metric/trace labels MUST use the chi route **template** (`/{issuer}/token`), never the raw `{issuer}` value, to bound cardinality (Contract §8).

**Definition of Done.**
- R1: `ResolveBaseURL` precedence table (forwarded-proto/host/port wins; defaults; deeply nested issuer paths resolve to host root; malformed `x-forwarded-port`/scheme → error) — a pure unit test, no HTTP.
- R2: `httptest` with `X-Forwarded-*` headers: `iss` and every discovery endpoint reflect the external address; CORS reflects `Origin` + credentials (default-on, no config); OPTIONS preflight → 204; static-asset traversal escape → `404 "not found"`.
- R3 (container parity, C9): a `docker-compose`-style two-container test (app + mock) where the browser and the app reach the mock under the **same** `iss` (the `host.docker.internal` scenario from the catalog), proving the advertised identity matches the reachable address; a TLS variant boots with `ssl:{}` and serves `https` discovery whose URLs are `https`.
- R4: `goreleaser`/melange/apko produce a multi-arch signed image under `ghcr.io/meigma/mock-oidc`; `openapi-check` final spec stable; metric cardinality test asserts templated labels.

---

### 8.11 Testing Strategy (cross-slice)

The R1–R4 ladder (§8.3) is applied per slice; this is how the three altitudes are realized and what each is *for*.

**Unit — the typed core (`internal/oidc`, R1).** The bulk of correctness lives here because the type system is the safety system. Table-driven tests (per the go-testing convention) cover: every `Parse*`/`New*` smart constructor's accept/reject set (especially the rejected algorithm set and the reserved-prefix rule, all returning typed `*ProtocolError`); each application service orchestrated over **`internal/oidc/mocks`** (generated, committed, drift-gated by `mockery-check`) so services are tested with zero IO; the pure decision functions (`Authorize` interactive trigger, `VerifyPKCE`, `ResolveBaseURL`, the callback resolution chain, audience/subject precedence). No `httptest`, no crypto signing, no container — fast and deterministic. The **constant-sync test** (advertised algs from `SupportedSigningAlgorithms()` == signer's `Algorithms()`) is a permanent R1 gate from Slice 1 on.

**Functional — the wired adapter (R2).** An `httptest.Server` mounts the real `httpapi`/`controlapi` Registrar over the **real** `signing` and `memory` adapters (no mocks) — the seam the container also uses, minus the network image. This altitude owns the HTTP *contract*: exact status codes, headers, the fixed discovery field order, correct-case (non-lowercased) OAuth2 error bodies, the 302/`form_post`/HTML output shapes, and real signature verification of minted JWTs against the served JWKS. Every parity-in-intent **divergence** gets an explicit negative test here asserting the *corrected* behavior (no body-lowercasing, no `form_post`-without-`state` 500, no `302→400` coercion, corrected cross-issuer refresh text, uniform OAuth2-shaped 405 instead of the bespoke `"unsupported method"` body, `/{issuer}` path routing instead of suffix matching).

**Container — the shipped artifact (R3).** Gated by the `integration` build tag in `internal/integration` (the kept convention; Docker required, excluded from default `go test ./...`, run via `moon run test-integration`). `testcontainers-go` runs the actual `ghcr.io/meigma/mock-oidc` image and drives it exactly as a test author would (PRD D-1: the container *is* the test fixture). Two flavors:
- **Self-parity**: a stock off-the-shelf OIDC client library performs the real flows against the container (discovery → JWKS → verify; auth-code; refresh; introspect; userinfo; exchange; the control-API scenario+capture loop). This proves the headline promise — *unmodified identity tooling works against the artifact*.
- **Golden parity (opt-in)**: where a byte-level behavior is contractual, the same request is issued against both the `mock-oidc` container and the upstream `ghcr.io/navikt/mock-oauth2-server` container and compared — **except** on the catalogued parity-in-intent divergences, where the comparison is inverted into an assertion that the two *differ in the intended way*. This keeps "parity in intent, not in flaws" (PRD P7/N8) honest and machine-checked rather than aspirational.

**Always-green gates (R4, `moon run check`).** Lint (golangci) includes a layering/import-restriction rung that fails:
- (a) any `internal/oidc` import of `huma`/`chi`/`net/http`/`net/url`/the JOSE library/the crypto **signing** packages (`crypto/rsa`, `crypto/ecdsa`, `crypto/ed25519`, `crypto/tls`, `crypto/x509`)/`viper`/`cobra`/`otel`/`prometheus`/`internal/adapter/*` — **with a documented carve-out** for the keyless `crypto/sha256` + `crypto/subtle` + `encoding/base64` PKCE transforms in `code.go` (Contract §3 as amended; depguard denies the named signing packages rather than blanket `crypto`);
- (b) any `internal/adapter/http` import of `internal/oidc` or `internal/oidc/httpapi` — the generic transport stays resource-agnostic; the protocol-vs-infra fallback writer (OAuth2 shape on protocol families, `problem.Write` on control/infra) is injected via `RouterDeps.FallbackWriter` from the composition root, and the recording middleware lives in `httpapi`, never in `adapter/http`.

Plus `build`, `openapi-check` (committed spec vs regenerated — no drift), and `mockery-check` (committed mocks vs regenerated). These run on every slice; a slice is not "done" until they are green and its R3 container assertion passes.

## Open Questions / Risks

These are the items the critics flagged that the binding contract and the rest of the design do **not** yet settle. Each needs an explicit call before implementation; none is a blocker that reopens a `DECISION`.

- **Domain-purity carve-out for keyless crypto and JSON (HIGH, unresolved).** Contract §3 forbids `internal/oidc` from importing `crypto/*`, and the depguard `oidc-core` rule + `arch_test.go` enforce a blanket `crypto/` prefix denial. Yet the design keeps two pure, keyless transforms in the core: PKCE `S256` verification (`crypto/sha256` + `crypto/subtle` + `encoding/base64`) and the `${…}` claim-template decode (`ParseClaimTemplate` over `encoding/json`, which depguard does not even deny — a silent leak). As written, the build gate would fail the very code the domain mandates. **Undecided:** amend §3 wording (from "`crypto/*`" to "crypto signing/JOSE packages") and narrow depguard/`arch_test.go` to deny only `crypto/rsa|ecdsa|ed25519|tls|x509` + the JOSE lib, with a *documented* carve-out for `crypto/sha256`/`crypto/subtle`/`encoding/base64`/`encoding/json` in `doc.go`; **or** relocate both behind ports/adapters and keep the core crypto- and json-free. All three artifacts (Contract §3, depguard config, `arch_test.go`) must end up saying the same thing.

- **Multi-segment / nested issuer IDs (MEDIUM, parity gap).** Contract §8's `DECISION` fixes single-segment `/{issuer}/<endpoint>`, and `IssuerID`'s constructor rejects any value containing `/`. Upstream's `issuerId` is everything before the endpoint suffix, so deeply-nested issuers (e.g. Azure-style `/aad/{tenant}/v2.0`; parity scenario S5) are fully supported there but cannot be represented here. The `/{issuer}/…` form is **not** equivalent-in-intent for multi-segment issuers. **Undecided:** either accept single-segment as a *named, justified* parity gap (rationale: common usage is single-segment) and warn adopters, **or** support a catch-all chi route + edge suffix-splitter and relax `IssuerID` to permit internal `/` segments (still rejecting the reserved `_mock` first segment). Do not leave it as silent divergence.

- **CORS default posture (MEDIUM, parity).** The contract KEEPs `CORSAllowedOrigins` but, unlike rate-limiting (explicitly default-off), does not decide CORS's default state. Upstream's `CorsInterceptor` is zero-config on *all* routes: it reflects the request `Origin` (not `*`), sets `Access-Control-Allow-Credentials: true`, answers preflight with `Allow-Methods POST,GET,OPTIONS` + echoed request headers, and returns 204. A default-empty allowlist blocks a SPA's cross-origin `fetch()` to `/{issuer}/jwks`, `/{issuer}/token`, `/{issuer}/userinfo` out of the box (regresses C9/S3). **Undecided:** default CORS **ON** with upstream reflect-origin-with-credentials semantics (keeping `CORSAllowedOrigins` as an optional tightening override), **or** keep the generic opt-in allowlist and record the zero-config SPA regression as an accepted gap. A plain allowlist with `AllowCredentials` cannot legally use `*`, so the reflect-with-credentials case must be specified concretely, not "reuse go-chi/cors as-is."

- **Custom JOSE `typ` self-verification (LOW, parity-in-intent call).** Upstream's verifier pins `typ=JWT`, so a token minted with a custom `JOSEType` (e.g. RFC 9068 `at+jwt`) fails the server's own `/userinfo` (401) and `/introspect` (`active:false`) — the mock rejects its own standards-compliant access tokens. This sits beside the many quirks the design deliberately drops (N8). **Undecided, must be a conscious documented call:** preserve `typ=JWT` pinning for drop-in test compatibility (noting the `at+jwt`/RFC 9068 consequence), **or** relax the verifier to accept `at+jwt`/`JWT` (cleaner per intent). It must not be asserted as unambiguously "correct" without the trade-off recorded.

- **Huma `SchemaLinkTransformer` pollution of discovery/JWKS (MEDIUM, feasibility risk to close).** `huma.DefaultConfig` (used verbatim by the kept `internal/adapter/http.NewAPI`) installs the link transformer, which wraps concrete struct bodies so `$schema` serializes **first** and appends a `Link: …; rel="describedBy"` header. Served as concrete structs, the `DiscoveryDocument` and `JWKS` responses would emit `{"$schema":…,"issuer":…}` — breaking the §7 fixed-field-order invariant (and its order-asserting unit test) and injecting non-standard fields/headers into documents strict third-party OIDC clients consume. The design never neutralizes this default. **Decision to confirm:** strip the link transformer in `NewAPI` (clear `cfg.Transformers` + the link `OnAddOperation`/CreateHook) **or** serve discovery and JWKS as pre-serialized `Body []byte` (the `[]byte` fast-path bypasses the transformer); state the chosen config decision in the TDD.