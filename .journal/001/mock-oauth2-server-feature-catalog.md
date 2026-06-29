# mock-oauth2-server — Feature Catalog (for Go parity)

## What it is

`navikt/mock-oauth2-server` is a scriptable OAuth2/OpenID Connect authorization server built for JVM tests and for local/Docker use. It mints **real, signed, verifiable JWTs** and exposes standard OIDC Discovery, OAuth2 Authorization-Server Metadata (RFC 8414), and JWKS endpoints, so an application under test needs no special configuration to validate the tokens. It is explicitly **for testing only** — never run it in production (README.md:94–101,115).

This document is the source of truth for reimplementing it in Go (`meigma/mock-oidc`). It folds in all corrections from the critic pass (discovery field order, refresh cross-issuer error text, the dual form-body parsers, custom-`typ` self-verification, `form_post`-without-`state`, end-session query-only params, static-asset MIME behavior, and previously uncatalogued provider methods).

## Architecture overview

The core is the immutable `OAuth2Config` (7 fields: `interactiveLogin`, `loginPagePath`, `staticAssetsPath`, `rotateRefreshToken`, `tokenProvider`, `tokenCallbacks`, `httpServer`). `MockOAuth2Server` turns that config into an `OAuth2HttpRequestHandler`, which exposes a single composed `Route` tree handed to a pluggable `OAuth2HttpServer`. Two backends exist: `MockWebServerWrapper` (default for the embedded test library; supports `takeRequest`) and `NettyWrapper` (default for standalone; required for HTTPS).

A custom `PathRouter` matches endpoints by URL path **SUFFIX** (`request.url.endsWith(path)`), so any leading path segment(s) before a known endpoint suffix become the `issuerId`. Multi-issuer is therefore **zero-config**: issuers are materialized on demand, and the `iss` claim = base URL + `issuerId`, derived through proxy-aware URL resolution (`x-forwarded-*` / `Host`). Each issuer gets its own lazily-created signing key (`kid = issuerId`) from `KeyProvider`, seeded from a bundled 5-key RSA JWKS (or generated; RSA-2048 / EC by curve), and exposes per-issuer discovery docs and JWKS.

`POST /token` dispatches by `grant_type` to six `GrantHandler`s. Tokens are signed JWTs minted by `OAuth2TokenProvider`; claim content is governed by an `OAuth2TokenCallback` resolved per request with priority **one-shot enqueued queue (issuer-matched head) > configured `RequestMappingTokenCallback` (issuerId match) > `DefaultOAuth2TokenCallback`**. A pluggable `TimeProvider`/`systemTime` freezes the clock for deterministic `iat`/`nbf`/`exp`. Cross-cutting HTTP concerns include a CORS response interceptor, a fully-lowercased JSON OAuth2 error shape, a global exception handler, a FreeMarker-rendered interactive login page, and an OAuth2 client debugger playground.

The library is consumed two ways: **embedded** (`MockOAuth2Server`: start/shutdown, URL helpers, `issueToken`/`anyToken`, `enqueueCallback`, `takeRequest`, `withMockOAuth2Server`) and **standalone** `main()` configured by env vars (`JSON_CONFIG`/`JSON_CONFIG_PATH`, `SERVER_HOSTNAME`, `SERVER_PORT`/`PORT`, `LOG_LEVEL`/`LOGBACK_CONFIG`) plus JSON, with an `/isalive` route, packaged via the Gradle application plugin and Google Jib (no Dockerfile) and published to GHCR + Maven Central.

---

## Endpoints & Discovery

All endpoints are issuer-scoped unless noted. The router matches by path **suffix**: everything before a known suffix is the `issuerId` (conventionally `default`). The composed route tree order is: exception handler → CORS interceptor → well-known → jwks → authorization → token → endSession → revocation → userInfo → introspect → OPTIONS preflight → static assets → favicon → debugger. User-supplied `additionalRoutes` are **prepended** (so they can override built-ins, e.g. `/isalive`). First match wins (OAuth2HttpRequestHandler.kt:92–108; MockOAuth2Server.kt:51–57).

| Path | Method | Purpose | Notes |
|---|---|---|---|
| `/{issuerId}/.well-known/openid-configuration` | GET | OIDC Discovery document | Same handler/body as the RFC 8414 doc below; `200 application/json;charset=UTF-8`, pretty-printed. Field order below. |
| `/{issuerId}/.well-known/oauth-authorization-server` | GET | RFC 8414 AS Metadata | **Identical body** to the OIDC discovery doc (registered on the same handler via `get(vararg paths)`). |
| `/{issuerId}/jwks` | GET | Per-issuer public JWK set | Path is `/{issuerId}/jwks` (not `/jwks.json`). `{"keys":[...]}` (Nimbus), public params only, `kid=issuerId`, `use=sig`. Forces key materialization for that issuer. (OAuth2HttpRequestHandler.kt:118–124) |
| `/{issuerId}/authorize` | GET | Authorization endpoint | If `interactiveLogin==true` OR `prompt` ∈ {login, consent, select_account} → interactive login HTML; else issues an authorization code. `prompt=none` does **not** trigger the page. Only `code` response_type implemented; hybrid/implicit → `invalid_grant`. (OAuth2HttpRequestHandler.kt:126–136; NimbusExtensions.kt:39–42) |
| `/{issuerId}/authorize` | POST | Login submit | POST always treats the body as a login submit (reads form `username` [required] + `claims` [optional JSON]). |
| `/{issuerId}/token` | POST | Token endpoint (grant dispatch) | Reads `grant_type` from the **POST body**. See Grant Types table. |
| `/{issuerId}/token` | GET | — | `405` with body `unsupported method` (distinct from the generic `method not allowed`). |
| `/{issuerId}/userinfo` | GET | OIDC UserInfo | Verifies `Authorization: Bearer <token>`; returns the token's **entire claim set** verbatim. Failure → `401 invalid_token`. No POST, no `WWW-Authenticate` header. (UserInfo.kt:18–49) |
| `/{issuerId}/introspect` | POST | RFC 7662 introspection | Requires a **presence-only** `Authorization` header (Bearer or Basic) else `400 invalid_client`. Reads token from form field `token`. Invalid signature → `{active:false}` (not an error). (Introspect.kt:22–104) |
| `/{issuerId}/revoke` | POST | RFC 7009 revocation (refresh tokens only) | `token_type_hint=refresh_token` → remove from `RefreshTokenManager`, `200 "ok"`. Any other/absent hint → `400 unsupported_token_type` `unsupported token type: <hint>`. (OAuth2HttpRequestHandler.kt:154–171) |
| `/{issuerId}/endsession` | GET/POST/ANY | RP-initiated logout | Reads `post_logout_redirect_uri` and `state` from the **URL query only** (`url.queryParameter`). With redirect uri → `302` (`?state=` appended naively if present); else `html("logged out")` 200. No `id_token_hint` validation. (OAuth2HttpRequestHandler.kt:144–152) |
| `/{issuerId}/debugger` | GET | Client debugger playground form | Pre-fills an authorize request against this server. Default `client_id=debugger`, `client_secret=someSecret`, `scope=openid somescope`, `state=1234`, `nonce=5678`. (DebuggerRequestHandler.kt:28–68) |
| `/{issuerId}/debugger` | POST | Debugger submit | `302` to the entered `authorize_url`; stashes all params (incl. secrets) in an encrypted `debugger-session` cookie. |
| `/{issuerId}/debugger/callback` | ANY | Debugger token exchange | Reads `token_url` + params from the session cookie, performs a real back-channel `authorization_code` token request, renders request/response as HTML. |
| `/static/{file}` | GET | Static assets (conditional) | **Only registered when `staticAssetsPath` set.** Not issuer-scoped. Path-traversal guard via canonical-path prefix check; `404 "not found"` if outside dir or missing. Content-Type via `Files.probeContentType` (see gotchas). (OAuth2HttpRequestHandler.kt:196–215) |
| `/favicon.ico` | GET | Quiet favicon | Empty `200` (no headers/content-type). |
| `*` (any path) | OPTIONS | CORS preflight catch-all | Empty-path OPTIONS route matches every path; returns `204`. CORS headers added by the interceptor. |
| (no match) | any | Fallthrough | Path matched but wrong method → `405 "method not allowed"`; no path match → `404 "no routes found"`. |

### Discovery document (`WellKnown`) field shape

Fields are emitted **in Kotlin data-class declaration order** (= Jackson serialization order). **CORRECTED ORDER** (the draft catalog and the README example both had this wrong):

1. `issuer`
2. `authorization_endpoint`
3. `end_session_endpoint`
4. `revocation_endpoint`
5. `token_endpoint`
6. `userinfo_endpoint`
7. `jwks_uri`
8. `introspection_endpoint`
9. `response_types_supported` = `[code, none, id_token, token]`
10. `response_modes_supported` = `[query, fragment, form_post]`
11. `subject_types_supported` = `[public]`
12. `id_token_signing_alg_values_supported` = EC family first (`ES256, ES384`; `ES256K` & `ES512` filtered out) **then** RSA family (`RS256, RS384, RS512, PS256, PS384, PS512`)
13. `code_challenge_methods_supported` = `[plain, S256]`

Not emitted: `registration_endpoint`, `scopes_supported`, `grant_types_supported`, `claims_supported`, `token_endpoint_auth_methods_supported`. (OAuth2HttpResponse.kt:53–79 declared order; KeyGenerator.kt:66–73 for the alg list.)

### CORS behavior (`CorsInterceptor`, applied to all routes)

- No `Origin` header → response unchanged.
- With `Origin`: `Access-Control-Allow-Origin` = the request Origin (**reflected, not `*`**); `Access-Control-Allow-Credentials: true`.
- On `OPTIONS`: `Access-Control-Allow-Headers` echoes `Access-Control-Request-Headers`; `Access-Control-Allow-Methods` = `POST, GET, OPTIONS`. (CorsInterceptor.kt:7–42)

### Error shape

Errors render as **indented JSON of the Nimbus `ErrorObject`, then fully `.lowercase()`-d** (this lowercases `error_description` text too). HTTP status = `error.httpStatusCode` unless it is `302` (coerced to `400`) or unset (→`400`). Keys: `error`, `error_description`. (OAuth2HttpResponse.kt:172–187.) Exception → error mapping: `OAuth2Exception`/`GeneralException` → their `errorObject`; `ParseException` → its `errorObject` ?: `invalid_request "failed to parse request: <urlencoded msg>"`; otherwise `server_error "unexpected exception with message: <urlencoded msg>"` (OAuth2HttpRequestHandler.kt:79–90). Helper throwers: `missingParameter`→`invalid_request "missing required parameter <name>"`; `invalidGrant`→`invalid_grant "grant_type <x> not supported."`; `invalidRequest`→`invalid_request`; `notFound`→`("not_found","Resource not found",404)` (OAuth2Exception.kt:1–34).

### Proxy-aware URL resolution (`OAuth2HttpRequest.url`)

Drives every discovery/issuer/`iss` URL so they reflect the externally visible address (OAuth2HttpRequest.kt:83–132):
- `scheme` = `x-forwarded-proto` ?: original scheme
- `host` = `Host` header host ?: original host
- `port` precedence: `x-forwarded-port` > `Host` header port > (`https`?443:80 by `x-forwarded-proto`) > original port
- path/query preserved from the original URL

Per-issuer endpoint URLs are built as `baseUrl().resolve(joinPaths(issuerId, endpointPath))`, where `baseUrl()` keeps only `scheme://host:port` (drops any path), so deeply nested issuer paths still resolve relative to host root (HttpUrlExtensions.kt:65–119).

---

## Grant Types & Flows

`POST /{issuerId}/token` reads `grant_type` from the **form body** and dispatches via a static map. Blank/missing → `400 invalid_request "missing required parameter grant_type"`; unknown → `400 invalid_grant "grant_type <x> not supported."`. Client secrets are **never validated** for any grant (only token-exchange `private_key_jwt` is structurally validated). The effective `client_id` = `clientAuthentication.clientID` ?: public `clientID` else `invalid_client "client_id cannot be null"` (NimbusExtensions.kt:77,104).

| `grant_type` | Key params | Behavior | Source |
|---|---|---|---|
| `authorization_code` | `code`, `redirect_uri`, `client_id`, `client_secret`, `code_verifier?` | Looks up & **removes** the cached `AuthenticationRequest` by `code` (single-use; removed before PKCE check). Mints `id_token` + `access_token` + `refresh_token`, `token_type=Bearer`. `nonce` comes from the **cached** request (not the token request) and is embedded in id/access/refresh-JWT. `id_token aud=[client_id]`; `access_token aud` from callback. `redirect_uri` not validated. **Only grant that auto-adds `azp`.** Cache miss → `400 invalid_grant "unknown or already-used authorization code"`. | AuthorizationCodeHandler.kt:71; OAuth2TokenProvider.kt:49 |
| `client_credentials` | `scope?` (+ client auth) | Issues **only** `access_token`. `sub` defaults to `client_id`. `aud`: configured audience, else non-OIDC scopes, else `["default"]`. `scope` echoed. | ClientCredentialsGrantHandler.kt:13; OAuth2TokenCallback.kt:45,53 |
| `password` (ROPC) | `username`, `password`, `scope?` (+ client auth) | Issues `access_token` **and** `id_token` (no refresh). `sub` = `username`. **Password never validated** (any accepted). `id_token nonce=null, aud=[client_id]`. Flagged insecure / removed from OAuth 2.1. | PasswordGrantHandler.kt:13,36 |
| `urn:ietf:params:oauth:grant-type:jwt-bearer` | `assertion` (JWT), `scope?` | On-Behalf-Of (RFC 7523). Copies **all** assertion claims, overrides `iss`/`exp`/`nbf`/`iat`/`jti`/`aud` + `addClaims`. Only `access_token`. `issued_token_type` omitted. **Assertion signature NOT verified** (only parsed). Missing assertion → `invalid_request "missing required parameter assertion"`. `scope` = request ?? assertion `scope` claim ?? `invalidRequest`. | JwtBearerGrantHandler.kt:16,41; OAuth2TokenProvider.kt:77 |
| `urn:ietf:params:oauth:grant-type:token-exchange` | `subject_token` (JWT), `subject_token_type`, `audience` | Token exchange (RFC 8693). Copies `subject_token` claims (**signature NOT verified**), sets `issued_token_type=urn:ietf:params:oauth:token-type:access_token`. Only `access_token`; `scope` is **null** (not echoed). `audience` param → `aud` when none configured. `actor_token`/`requested_token_type`/`resource` not consumed (no `act` chain). `subject_token_type` accepted, not enforced. | TokenExchangeGrantHandler.kt:14,40 |
| `refresh_token` | `refresh_token`, `client_id`, `client_secret` | **Strict (since 4.0.0).** Resolution: issuer-binding check on stored callback → enqueued callback (issuer-matched head) → stored callback → else `400 invalid_grant "unknown refresh_token"`. Re-mints `id_token` + `access_token` (same subject/claims, fresh `jti`/`iat`/`exp`) + returns `refresh_token`. Unknown/expired/revoked/cross-issuer → `400 invalid_grant`. | RefreshTokenGrantHandler.kt:20,61 |

### Authorization-code flow details

- **PKCE** (plain & S256): enforced **only when a `code_verifier` is presented**. `CodeChallenge.compute(method, verifier)` must equal the stored `code_challenge`, else `400 invalid_grant "invalid_pkce: code_verifier does not compute to code_challenge from request"`. A challenge alone need not be redeemed. A failed PKCE attempt also invalidates the code (login-cache entry removed). `S256 = BASE64URL(SHA256(verifier))`; default method = plain (Nimbus). (NimbusExtensions.kt:44; AuthorizationCodeHandler.kt:87)
- **Response modes**: `query`/`fragment` → `302` to `redirect_uri` carrying `code` & `state`; `form_post` → `200` self-submitting HTML form POSTing `code` & `state` to `redirect_uri`. Only `code` & `state` are posted (no `id_token`; hybrid/implicit unsupported). (OAuth2HttpResponse.kt:150–170)
- **Code cache**: `ConcurrentHashMap`, in-memory, unbounded, no TTL; single-use.

### Token-exchange `private_key_jwt` client-assertion validation

When token-exchange is authenticated with `client_assertion` (`client_assertion_type=urn:ietf:params:oauth:client-assertion-type:jwt-bearer`), extra checks apply (`requirePrivateKeyJwt(requiredAudience=issuerUrl, maxLifetimeSeconds=120, additionalAcceptedAudience=tokenEndpointUrl)`). All → `400 invalid_request`: `expiresIn > 120s`; `jwt.iss != client_id`; `jwt.sub != client_id`; audience empty; audience size > 1; `audience[0]` not in {issuerUrl, tokenEndpointUrl}. Non-private-key-jwt auth bypasses this. No client auth at all → `"request must contain some form of ClientAuthentication."`. The assertion signature is **not** cryptographically verified. (NimbusExtensions.kt:104,121)

### Refresh-token semantics

- **Rotation** (`rotateRefreshToken`): when `true`, the grant issues a **new** refresh token and removes the old (rotated token generated **without** nonce → loses the PlainJWT form); when `false` (default), the same token is echoed and remains reusable indefinitely. A rotated-out token then fails with `400 invalid_grant`. No reuse-detection chain. (RefreshTokenManager.kt:30)
- **Issuer binding**: a refresh token issued under issuer A, presented to issuer B's token endpoint → `400 invalid_grant`. The **client-visible `error_description` is `"refresh_token was issued by a different issuer"`** (CORRECTED — the internal exception *message* is `"refresh_token issuer mismatch"`, but `setDescription(...)` carries the client text, which `oauth2Error` lowercases). (RefreshTokenGrantHandler.kt:37–39)
- **Enqueued override**: an enqueued callback whose `issuerId` matches takes priority over the stored callback (priority: enqueued > stored). Same shared queue as non-refresh grants. (RefreshTokenGrantHandler.kt:40)

---

## Token Issuance, Signing & Claims

### Token response JSON (`OAuth2TokenResponse`, `NON_NULL`)

Keys (snake_case, null fields omitted): `token_type` (always `Bearer`), `issued_token_type?`, `id_token?`, `access_token`, `refresh_token?`, `expires_in`, `scope?`. `expires_in` is an `Int` **recomputed live** from the signed token's `exp` via `SignedJWT.expiresIn()` using **real `Instant.now()`** — so under a frozen `systemTime`, the token's `exp` and the reported `expires_in` diverge. Per-grant matrix:

| Grant | id_token | access_token | refresh_token | issued_token_type | scope |
|---|---|---|---|---|---|
| `authorization_code` | ✓ | ✓ | ✓ | — | echoed |
| `refresh_token` | ✓ | ✓ | ✓ | — | echoed |
| `password` | ✓ | ✓ | — | — | echoed |
| `client_credentials` | — | ✓ | — | — | echoed |
| `jwt-bearer` | — | ✓ | — | — | echoed |
| `token-exchange` | — | ✓ | — | `...:access_token` | **null** |

(OAuth2HttpResponse.kt:81–97; NimbusExtensions.kt:81.)

### Signing keys & JWKS

- **Algorithms** (`KeyGenerator`): full Nimbus RSA family (`RS256/384/512`, `PS256/384/512`) and EC **except `ES256K` ("not a public used algorithm") and `ES512` ("legacy")** → so `ES256`, `ES384`. Default `RS256`. `EdDSA`/HMAC/`none` unsupported (`EdDSA` throws `"Unsupported algorithm: EdDSA"` — asserted in tests). (KeyGenerator.kt:66–82; OAuth2TokenProvider.kt:119–151)
- **Key generation**: RSA fixed 2048 bits (`RSAKeyGenerator.MIN_KEY_SIZE_BITS`); EC curve derived from 3 chars after `ES` (`ES256`→256, `ES384`→384) via `Curve.forJWSAlgorithm`. Each JWK: `use=sig`, chosen `alg`, `kid=issuerId`. (KeyGenerator.kt:22–107)
- **Per-issuer lazy keys**: `KeyProvider.signingKey(issuerId)` = `computeIfAbsent`. A given `issuerId` always signs with the same key for the server's lifetime; distinct issuers get distinct keys. **`kid` = `issuerId`** (deterministic, not a thumbprint). No automatic rotation. (KeyProvider.kt:33–50)
- **Bundled initial keys**: on startup, `KeyProvider` loads initial JWKs from a classpath file (default `/mock-oauth2-server-keys.json`, **5 RSA keys**, kids `initialkey-1..5`) into a FIFO deque consumed one-per-new-issuer before any generation. An EC variant (`mock-oauth2-server-keys-ec.json`, `issuer0`/`issuer1`, P-256/ES256) is also bundled. **The file's `kid` is discarded and replaced by `issuerId` on use.** JSON config supports only a **single** `initialKeys` JWK. Ship an equivalent embedded RSA JWKS for stable keys across restarts. (KeyProvider.kt:16–69)
- **JWKS exposure**: `publicJwkSet(issuerId)` first calls `signingKey(issuerId)` (materializing it), so `/jwks` always returns the requested issuer's key; public params only. The internal `get()` used for verification may be empty until first sign. (OAuth2TokenProvider.kt:44–47; KeyProvider.kt:72–75)
- **JWS header**: every token sets `alg` (provider algorithm), `kid` (= `issuerId`), and `typ` (callback's `typeHeader`, default `JWT`; e.g. `at+jwt`). (OAuth2TokenProvider.kt:119–162)
- **Provider/key introspection methods** (public, embeddable API — previously uncatalogued): `OAuth2TokenProvider.getAlgorithm()` exposes the configured `JWSAlgorithm`; `KeyProvider.algorithm()`/`keyType()`/`generate(algorithm)` — the last swaps the `KeyGenerator` at runtime, allowing the signing algorithm/key type to be changed. (OAuth2TokenProvider.kt:47; KeyProvider.kt:52–58)

### Pluggable clock (`TimeProvider` / `systemTime`)

`OAuth2TokenProvider` accepts a `TimeProvider (()->Instant?)` or fixed `systemTime` `Instant`. `Instant?.orNow() = this ?: Instant.now()`. All `iat`/`nbf`/`exp` and the verifier's `currentTime()` use it. JSON: `tokenProvider.systemTime` is an ISO-8601/RFC3339 instant string (`Instant.parse`). A single global instant drives both issuance and verification. (OAuth2TokenProvider.kt:27–42,192,204)

### Claims

**`defaultClaims()`** (id_token & access_token) always sets: `sub`, `aud`, `iss` (full issuer URL string), `iat` (= now), `nbf` (= now), `exp` (= now + `tokenExpiry`), `jti` (random UUID). `nonce` added **only when non-null**. `auth_time`/`acr` are **not** auto-added (custom only). (OAuth2TokenProvider.kt:169–190)

Auto-added by `DefaultOAuth2TokenCallback` (not by `RequestMappingTokenCallback`):
- **`tid` = `issuerId`** — always (NAV/Azure-AD convention); user-overridable via `claims` (added before `putAll(claims)`). (OAuth2TokenCallback.kt:63–71)
- **`azp` = `client_id`** — **only for `authorization_code`**, added **after** your claims (non-overridable). (OAuth2TokenCallback.kt:68–70)

**Subject resolution** (`subject(req)`):
- `client_credentials` → `client_id`
- `password` → ROPC `username`
- interactive login → login `username` (or mapping `sub` falling back to login username)
- otherwise → configured `subject` (default random UUID)
- `RequestMappingTokenCallback.subject` = `claims['sub']` (**nullable** when the mapping omits `sub`)

**Audience resolution** for `access_token` (`DefaultOAuth2TokenCallback.audience`, 4-step precedence):
1. explicit configured `audience` (including an explicitly **empty** list — meaningful because the field is nullable)
2. token-exchange `audience` request param(s) (`audienceOrEmpty()`)
3. non-OIDC scopes (`scopesWithoutOidcScopes`, stripping `openid`/`profile`/`email`/`address`/`phone`/`offline_access`)
4. `["default"]`

`id_token` **ignores** this chain — `id_token aud` is always `[client_id]`. `RequestMappingTokenCallback.audience` = `claims['aud']` coerced to a String list. (OAuth2TokenCallback.kt:53–61,207–212)

**Token lifetime**: `exp = now + tokenExpiry()` seconds; default 3600 for both built-in callbacks; configurable per `tokenCallback` in JSON (`tokenExpiry`, seconds). The generic `jwt()` helper defaults to 1h.

**`typ` header**: per-request customizable via callback (default `JWT`); `RequestMapping` carries its own `typeHeader`. **Caveat:** see Parity Gotchas — custom `typ` tokens fail this server's own `/userinfo` and `/introspect` verification.

### Token-minting paths (`OAuth2TokenProvider`)

| Method | Used by | Auto-added | aud |
|---|---|---|---|
| `idToken(...)` | auth_code, refresh, password | default claims (+ nonce if non-null) | `[client_id]` |
| `accessToken(...)` | auth_code, cc, password, refresh | default claims | from callback |
| `exchangeAccessToken(...)` | token-exchange, jwt-bearer, `anyToken()` | copies inbound claims, overrides `iss`/`exp`/`nbf`/`iat`/`jti`/`aud` + `addClaims` | from callback |
| `jwt(map, dur=1h, issuerId="default")` | generic helper | **only** `iat`/`nbf`/`exp`; caller supplies `sub`/`aud`/`iss` | caller |

`access_token` and `id_token` are structurally identical signed JWTs (not opaque), differing only in `aud` resolution and (for auth_code) nonce.

### Refresh-token format (`RefreshTokenManager`)

`refreshToken(callback, nonce?)`: `jti = UUID`. Token = `nonce?.let { PlainJWT({jti, nonce}).serialize() } ?: jti` — i.e. a bare UUID **or an UNSIGNED (`alg=none`) PlainJWT** `{jti, nonce}` when a nonce is present (for the Keycloak JS client). Cache `ConcurrentHashMap<RefreshToken, OAuth2TokenCallback>`, no expiry. Only `authorization_code` and `refresh_token` grants return a refresh token. (RefreshTokenManager.kt:12,19)

### Internal verification helpers

- `verify(HttpUrl, String)`: `DefaultJWTProcessor` with `jwsTypeVerifier` pinned to `typ="JWT"`, `JWSVerificationKeySelector(algorithm, keyProvider)`, requires **exact issuer + `iat` + `exp`**; current time from `TimeProvider`. Backs `/userinfo` and `/introspect`.
- `SignedJWT.verifySignatureAndIssuer(Issuer, JWKSet, JWSAlgorithm)` (default RS256): requires **`sub` + `iat` + `exp`**, pins issuer, accepts only `typ=JWT`.

(Two different required-claim sets. Both reject non-`JWT` `typ`.) (OAuth2TokenProvider.kt:114–117,194–208; NimbusExtensions.kt:83–102)

### The `OAuth2TokenCallback` customization model

`interface OAuth2TokenCallback` methods: `issuerId()`, `subject(req)`, `typeHeader(req)`, `audience(req)`, `addClaims(req)`, `tokenExpiry()`. Selection priority (keyed strictly by `issuerId`): **enqueued one-shot (issuer-matched head, polled) > `config.tokenCallbacks` first matching `issuerId` > `DefaultOAuth2TokenCallback(issuerId)`**. The refresh grant consults the same queue.

**Built-ins:**
- `DefaultOAuth2TokenCallback(issuerId="default", subject=UUID, typeHeader="JWT", audience=null, claims=empty, expiry=3600)` — `open` for subclassing; `audience` nullable to distinguish "unset" from explicit empty.
- `RequestMappingTokenCallback{issuerId, requestMappings, tokenExpiry=3600}` — the type used when callbacks come from JSON (`@JsonDeserialize(contentAs=RequestMappingTokenCallback)`). `resolve()` reads form params (multi-valued, see gotchas), finds the **first** matching `RequestMapping`, applies `${...}` templating; `subject=claims['sub']`, `audience=claims['aud']` coerced to list, `addClaims=claims`, `typeHeader` from the mapping. **Does not** add `tid`/`azp`.

**`RequestMapping{requestParam, match, claims, typeHeader}`** matching:
- `match == "*"` → matches any present value (literal wildcard, not regex)
- otherwise: compiled as `Regex.matchEntire` **AND** accepted on exact-string equality; an **invalid regex is swallowed silently** (warns, falls back to exact match only)
- `requestParam` resolution: form param of that name; else `client_id` → `clientAuthentication.clientID`/`clientID`; else `extraMatchParams` (e.g. synthetic `subject` injected at interactive login, `SUBJECT_PARAM="subject"`)
- **first matching mapping wins** (order-sensitive)

**`${key}` templating** (`Template.kt`): regex `\$\{(\w+)\}` replaced from request-derived params; recurses into nested `List`/`Map`/`String`; **non-String leaf values (numbers/bools) NOT templated**; unknown keys left literal. Multiple form-param values are space-joined. **Precedence (highest wins): `client_id`/`clientId` (always authoritative, cannot be shadowed) > token POST body form params > built-ins like `${subject}`.** (Template.kt:9–30; OAuth2TokenCallback.kt:132–154)

**Login-injected subject & claim merge (5.0.0 contract):** for interactive-login auth codes, `LoginOAuth2TokenCallback` wraps the resolved callback. `withExtraMatchParams(subject=login.username)` lets mappings match `requestParam="subject"`; subject resolves to mapping `sub` else `login.username`. `addClaims` starts from delegate claims, then **`putIfAbsent`** for each field of the login `claims` JSON — **login claims ADD but never OVERWRITE** (mapping wins, including `sub`). Invalid login `claims` JSON is caught/warned/ignored. (Pre-5.0.0 login claims could overwrite, causing subject mismatch.) (AuthorizationCodeHandler.kt:111–165; MIGRATION.md:5–15)

---

## Configuration Reference

### Environment variables / system properties (standalone)

`StandaloneConfig.oauth2Config()` resolves config from JSON env/file or builds a default. Precedence for the JSON config source: **`JSON_CONFIG` > `JSON_CONFIG_PATH` file > `config.json` in CWD > built-in defaults**.

| Var | Default | Purpose | Notes |
|---|---|---|---|
| `JSON_CONFIG` | — | Full `OAuth2Config` JSON inline | Highest-precedence config source. (StandaloneMockOAuth2Server.kt:16,41) |
| `JSON_CONFIG_PATH` | `config.json` | Path to JSON config file | Used when `JSON_CONFIG` unset; `FileNotFoundException` → fall back to defaults. docker-compose sets `/app/config.json`. |
| `SERVER_HOSTNAME` | wildcard | Bind interface/host | Unset → `InetSocketAddress(0).address` (wildcard, e.g. `0.0.0.0`/`::`). No lowercase variant. |
| `SERVER_PORT` | `8080` | Listen port | Takes precedence over `PORT`. |
| `PORT` | `8080` | Listen port (Heroku compat) | `SERVER_PORT` > `PORT` > 8080; both parsed as Int. |
| `LOG_LEVEL` | `INFO` | Root log level | `logback-standalone.xml` reads `${LOG_LEVEL:-INFO}`; pins `io.netty` to `info`. Go: map to `slog` level. |
| `LOGBACK_CONFIG` | `logback-standalone.xml` | Custom logback XML path | JVM/Logback-specific; in Go likely a no-op alias. |

**Standalone vs library defaults diverge**: standalone builds `OAuth2Config(interactiveLogin=true, httpServer=NettyWrapper())` when no JSON found; the library default is `interactiveLogin=false` + `MockWebServerWrapper`. (StandaloneMockOAuth2Server.kt:29–48)

### JSON_CONFIG schema (`OAuth2Config.fromJson`)

Parsed by `jacksonObjectMapper().readValue(json)`. Keys map 1:1 to property names; **lenient on extra keys** (no `FAIL_ON_UNKNOWN`). Three fields carry custom deserializers (`tokenProvider`, `tokenCallbacks` element type, `httpServer` string|object union). Unknown server type or unsupported algorithm → `JsonMappingException` wrapping `OAuth2Exception`.

| JSON key | Type | Default | Description |
|---|---|---|---|
| `interactiveLogin` | boolean | `false` (lib) / `true` (standalone) | Show login page on `/authorize` (or when `prompt` forces it). Enables `requestParam="subject"` matching and `${subject}`. |
| `loginPagePath` | string\|null | `null` | Filesystem path to a **custom HTML login page** served verbatim (`File(path).readText()`; no templating). Invalid/missing/dir/empty → `404` `"The configured loginPagePath ... is invalid ..."`. Custom page **must POST fields named exactly `username` (required) and `claims`** back to `/authorize`. |
| `staticAssetsPath` | string\|null | `null` | Directory served under `/static/*` (route only registered when non-null). |
| `rotateRefreshToken` | boolean | `false` | Passthrough to the refresh grant (rotate vs reuse). |
| `tokenProvider` | object | `OAuth2TokenProvider()` | See below. Non-object node → default. |
| `tokenCallbacks` | array | `[]` (empty set) | Per-issuer `RequestMappingTokenCallback` list. See below. |
| `httpServer` | string\|object | `MockWebServerWrapper` (lib) / `NettyWrapper` (standalone) | See below. |

**`tokenProvider`** (`OAuth2TokenProviderDeserializer`):
- `systemTime` — ISO-8601/RFC3339 instant string (`Instant.parse`); pins the global clock for issuance & verification.
- `keyProvider` — `{ initialKeys, algorithm }`:
  - `initialKeys` — a **single JWK** as a JSON **string** (`JWK.parse`, wrapped in a one-element list). Note: the embedded JWK JSON must be string-escaped. The JSON path supports only ONE key (vs the 5-key bundled file).
  - `algorithm` — JWS algorithm string; defaults `RS256` (example uses `ES256`). RSA & EC only; `EdDSA`/`ES256K`/`ES512` rejected. `kid` is always overwritten to `issuerId`.

**`tokenCallbacks[]`** — each entry:
- `issuerId` (string)
- `requestMappings` (ordered array) of `{ requestParam, match, claims, typeHeader }`:
  - `claims` values may be scalars or arrays (`aud` commonly an array, e.g. `["audByCode"]`) and support `${...}` templates.
  - matching/templating semantics as described above.
- `tokenExpiry` (seconds, default `3600`)

Lookup at request time: enqueued > `config.tokenCallbacks.firstOrNull{issuerId match}` > `DefaultOAuth2TokenCallback`. Example `config.json` shows `issuer1` (`tokenExpiry` 120, code-based mapping) and `issuer2` (`someparam` mapping).

**`httpServer`** (`OAuth2HttpServerDeserializer`, string|object union):
- String form: `"MockWebServerWrapper"` (supports `takeRequest()`) or `"NettyWrapper"` (required for HTTPS). Unknown → `JsonMappingException`.
- Object form: `{ type, ssl }`.
- `ssl` (presence of an `ssl` object — **even empty `{}`** — turns TLS on): `SslConfig { keyPassword="", keystoreFile=null, keystoreType="PKCS12" (PKCS12|JKS), keystorePassword="" }`. `keystoreFile=null` → generated self-signed **RSA-2048, SHA256withRSA, CN=localhost, SAN dNSName localhost + 127.0.0.1, 365-day, PKCS12** keystore. `config-ssl.json` uses `NettyWrapper` + `ssl:{}`.

(OAuth2Config.kt:24–123; Ssl.kt:38–154.)

### `OAuth2Exception` / error codes to replicate

`class OAuth2Exception(errorObject?, message, throwable?)` plus helpers (`missingParameter`, `invalidGrant`, `invalidRequest`, `notFound`). Replicate the standard codes and exact descriptions tests assert: `invalid_request`, `invalid_grant`, `invalid_client`, `not_found` (`"Resource not found"`, 404), `server_error`, `unsupported_token_type`, `invalid_token`.

---

## Interactive Login, Debugger, UserInfo, Introspection & Logout

### Interactive login

- **Triggers** (OR'd): `config.interactiveLogin == true` **or** `AuthenticationRequest.isPrompt()` (prompt ∈ {LOGIN, CONSENT, SELECT_ACCOUNT}; `none`/`null` → false). Page rendered only for **GET** `/authorize`; **POST** always treats the body as a login submit. (OAuth2HttpRequestHandler.kt:126–142)
- **Built-in page** (`login.ftl`): title `Mock OAuth2 Server Sign-in`; a `<form method="post">` with **no `action`** (posts back to the same `/authorize` URL preserving the query string); inputs `username` (text, required, autofocus, placeholder "Enter any user/subject") and `claims` (15-row textarea, optional JSON, e.g. `{"acr":"reference"}`); a `Sign-in` submit. Below the form, an "Authorization Request" section lists every query parameter (`<strong>${name}</strong> = ${value}`). `request_url` is supplied to the template but unused. (login.ftl:6–39; TemplateMapper.kt:18–32)
- **Submit handler** (`LoginRequestHandler`): reads `username` (missing → `missingParameter("username")` / 400) and optional `claims` → `Login(username, claims?)`; `authorizationCodeResponse` caches `code → Login` (`codeToLoginCache`) for token-time use. Only the auth-code path consumes login. (LoginRequestHandler.kt:26–36)
- **Custom page** (`loginPagePath`): raw `File(path).readText()`; no templating; invalid path → `404`. Must POST `username`/`claims` back to `/authorize`.

### `form_post` auto-submit page

When `response_mode=form_post`, the server returns `200` with `<body onload="document.callback.submit()">` and a hidden form `action=${redirect_uri}` method=post, hidden inputs `code`/`state`. (authorization_code_response.ftl:1–13)

### OAuth2 client debugger / playground

- **`GET /debugger`** (`debugger.ftl`): editable form pre-filled to drive a round-trip against this server. Default `client_id=debugger`, `client_secret=someSecret` (baked into the template), `scope=openid somescope`, `response_type=code`, `response_mode=query`, `state=1234`, `nonce=5678`, `redirect_uri={issuer}/debugger/callback`, default `client_auth_method=CLIENT_SECRET_BASIC`; submit "Get a token".
- **`POST /debugger`**: rebuilds the request from the submitted form, strips helper-only params (`authorize_url`, `token_url`, `client_secret`, `client_auth_method`) from the redirect query, stores the **whole form map** in an encrypted session, returns `302` to the entered `authorize_url` (can be an external IdP) with `Set-Cookie`.
- **`ANY /debugger/callback`** (`debugger_callback.ftl`): reads `token_url` + params from the session cookie; `code` from query or form (`else invalid_request "no code parameter present"`); builds `ClientAuthentication` from the session + a Nimbus `authorization_code` `TokenRequest`; **POSTs to `token_url` via OkHttp** (`withSsl(ssl)` when TLS); renders raw "Token Request" / "Token Response" in `<pre><code>`. The server acts as its own OAuth client (back-channel call to its own token endpoint).
- **Session cookie** (`SessionManager`): `debugger-session`; `HttpOnly; Path=/`; param map serialized to JSON and **JWE-encrypted** (`JWEAlgorithm.DIR` + `EncryptionMethod.A128GCM`, `DirectEncrypter`) with a **per-process AES-128 key regenerated each start** (sessions don't survive restart or span nodes). Decrypt failures → empty session; `get(key)` throws `"could not get <key> from session"` if missing.
- **Client auth methods**: enum `{CLIENT_SECRET_POST, CLIENT_SECRET_BASIC}` only (no `private_key_jwt`/`none`). `Method.valueOf` is **case-sensitive**. BASIC → `Authorization: Basic base64(id:secret)`; POST → url-encoded creds appended to the form body.
- **SSL trust**: when TLS is on, the back-channel call trusts the server's own keystore (`OkHttpClient.withSsl(ssl)`, `followRedirects=false`).
- **Error page** (`error.ftl`): the debugger sub-router has its **own** exception handler rendering `500 text/html` with the full stack trace + a link back (distinct from the main JSON OAuth error handler). Typical trigger: expired/missing session cookie.

(DebuggerRequestHandler.kt:28–117; SessionManager.kt:19–78; Client.kt:19–114.)

### UserInfo (`GET /{issuer}/userinfo`)

`bearerToken()` splits `Authorization` on `"Bearer "` (must yield 2 parts) else `invalidToken("missing bearer token")`. `tokenProvider.verify(toIssuerUrl, token)` → `json(claims.claims)` — the **whole** `JWTClaimsSet`, no filtering/scoping. Verify failure → `invalidToken(e.message ?: "could not verify bearer token")` = `OAuth2Exception(ErrorObject("invalid_token", msg, 401))`. Only GET; no `WWW-Authenticate` header. Tokens from a different issuer/algorithm are rejected (401). (UserInfo.kt:18–49)

### Introspection (`POST /{issuer}/introspect`, RFC 7662)

`authenticated()`: `Authorization` split on `"Bearer "` or `"Basic "` must yield a non-empty value, else `OAuth2Exception(OAuth2Error.INVALID_CLIENT "The client authentication was invalid")` (400). **No actual credential check.** Verifies form param `token` via `tokenProvider.verify`; failure (or no `token` param) → `IntrospectResponse(active=false)` (not an error). Response (`NON_NULL`): `active`, `scope`, `client_id`, `username`, `token_type` (claim or default `"Bearer"`), `exp`/`iat`/`nbf` (epoch seconds), `sub`, `aud` (`List<String>` with `WRITE_SINGLE_ELEM_ARRAYS_UNWRAPPED` → single `aud` serialized as a **string**), `iss`, `jti`. (Introspect.kt:22–104)

### End-session / logout (`ANY /{issuer}/endsession`)

Accepts any method. With `post_logout_redirect_uri` (read from the **URL query only**): if `state` present → `302` to `<uri>?state=<state>` (naive `?state=` concat, assumes no existing query), else `302` to the uri. Without it → `html("logged out")` 200. **No `id_token_hint` validation, no session termination.** Path literal is `endsession` (one word). (OAuth2HttpRequestHandler.kt:144–152)

### Shared HTML layout

All built-in pages share `main.ftl`'s `mainLayout(title, description)` macro, inlining three CSS files (`normalize` ~8KB, `skeleton` ~12KB, `custom` ~1.8KB) via `<#include 'css/...'>` and loading the **Raleway** web font from Google Fonts (a network dependency). Templates load from classpath `templates/` via `ClassTemplateLoader`. Go: `html/template` with inlined CSS. (main.ftl:1–25; TemplateMapper.kt:95–110)

---

## Test-Library API & Request Capture

Consumed as a test-scope JVM dependency: Gradle `testImplementation("no.nav.security:mock-oauth2-server:$version")`; Maven `groupId no.nav.security`, `artifactId mock-oauth2-server`, `<scope>test</scope>`. Lifecycle: `new MockOAuth2Server(); server.start(); ...; server.shutdown()`.

| API | Signature / behavior | Notes |
|---|---|---|
| `MockOAuth2Server` | `MockOAuth2Server(config=OAuth2Config(), vararg additionalRoutes: Route)` (+ config-only, routes-only ctors) | `additionalRoutes` **prepended** before the default route (take precedence). `open` class. |
| `start` | `start(port=0)`; `start(InetAddress, port)` | `port=0` → ephemeral port on localhost (read back via `url()`/`baseUrl()`). `IOException` → `OAuth2Exception("unable to start server: ...")`. |
| `shutdown` | `shutdown()` | `IOException` → `OAuth2Exception("unable to shutdown server: ...")`. |
| `withMockOAuth2Server` | `<R> withMockOAuth2Server(config=OAuth2Config(), test: MockOAuth2Server.()->R): R` | Random port; runs the lambda with the server as receiver; **guarantees shutdown in `finally`**. |
| URL accessors | `url(path)`, `baseUrl()`, `issuerUrl(id)`, `wellKnownUrl(id)`, `oauth2AuthorizationServerMetadataUrl(id)`, `tokenEndpointUrl(id)`, `jwksUrl(id)`, `authorizationEndpointUrl(id)`, `endSessionEndpointUrl(id)`, `revocationEndpointUrl(id)`, `userInfoUrl(id)` | `url(path)`: first path segment = `issuerId`. `baseUrl().toString()` ends with `/`. **No `introspectUrl()` method exists** despite README:642 documenting one (the `/introspect` endpoint itself exists) — decide whether to add it for docs-parity. |
| `issueToken` (full) | `issueToken(issuerId, clientId, OAuth2TokenCallback): SignedJWT` | In-process mint (no HTTP) as if from `authorization_code`. Synthesizes a Nimbus `TokenRequest` with `ClientSecretBasic(clientId, "secret")`, `AuthorizationCodeGrant(code="123", redirect="http://localhost")` and calls `accessToken(...)`. |
| `issueToken` (convenience) | `issueToken(issuerId="default", subject=UUID, audience="default", claims=emptyMap, expiry=3600): SignedJWT` | Forces `clientId="default"`; `audience` wrapped to single-element list (`null` → no aud). Most-used helper in examples. |
| `anyToken` | `anyToken(issuerUrl: HttpUrl, claims, expiry=Duration.ofHours(1)): SignedJWT` | Signs a token for an arbitrary external `issuerUrl` with this server's keys. **Quirk:** `expiry.toMillis()` is passed where seconds are expected → effective expiry inflated ~1000×. Replicate only to match exact behavior. |
| `enqueueCallback` | `enqueueCallback(OAuth2TokenCallback)` | One-shot, issuer-matched (head-only), single-use; consumed by the next matching token request (incl. refresh grant). Affects **real HTTP** responses (vs `issueToken`'s in-process mint). |
| `takeRequest` | `takeRequest(timeout=2, unit=SECONDS): RecordedRequest` | **MockWebServer-only.** Casts `httpServer as? MockWebServerWrapper`. Null/timeout → `RuntimeException "no request found in queue within timeout"`. `NettyWrapper` → `UnsupportedOperationException "can only takeRequest when httpServer is of type MockWebServer"`. Exposes `requestUrl`/`path`/`method`/`headers`/`body`. Preserve **raw body bytes** (param order matters), not a reparsed map. |
| `enqueueResponse` (deprecated) | `@Deprecated enqueueResponse(MockResponse)` always throws `UnsupportedOperationException` | The internal `responseQueue` is peeked by the dispatcher, but the public API isn't externally fillable. Go may choose to expose a working `EnqueueResponse`. |
| okio guard | `companion object init { Buffer().copy() }` | Fails fast at class-load if mockwebserver/okio < 4.9.2. JVM-classpath-specific; no Go equivalent. |

**Test-source helpers** (not shipped API, but a useful parity spec): `testutils/Http.kt` (OkHttp wrappers, `Response.authorizationCode` extracting `code` from `Location`), `testutils/Token.kt` (`shouldBeValidFor(GrantType)` encodes the grant→token-shape matrix; `verifyWith` against the live `jwksUrl`, RS256, required claims `sub/iss/iat/exp/aud`), `testutils/Grant.kt` (`authenticationRequest(...)` defaults: `response_type=code`, `response_mode=query`, `scope=[openid]`, `state=1234`, `nonce=5678`; `Pkce(verifier, method=S256)`).

**Spring Boot embedding constraint**: `MockOAuth2ServerInitializer` (`ApplicationContextInitializer`) must start the server **before** the application context so eager discovery/JWKS fetches succeed; it injects `mock-oauth2-server.baseUrl` (trailing slash stripped) and wires `jwt.issuer-uri=${baseUrl}/issuer1`. `AbstractExampleApp` is the integration contract: fetch `OIDCProviderMetadata` from discovery, retrieve JWKS, verify bearer JWTs with Nimbus.

---

## Deployment & Packaging

- **Standalone JAR / Docker**: `docker run -p 8080:8080 ghcr.io/navikt/mock-oauth2-server:$VERSION`. Default port 8080. Token endpoint `http://localhost:8080/default/token`; discovery `http://localhost:8080/default/.well-known/openid-configuration`. On Windows add `-h localhost`. Entrypoint `no.nav.security.mock.oauth2.StandaloneMockOAuth2ServerKt` mounts `/isalive` (an `additionalRoute`, standalone-only) → `200 "alive and well"` and starts on `hostname()`/`port()`.
- **Pluggable HTTP server** (`OAuth2HttpServer`: `start`/`stop`/`close`/`port`/`url`/`sslConfig`):
  - `NettyWrapper` (production/standalone, required for HTTPS): scheme from `SslHandler` presence; host/port prefer `Host` header (host:port) else socket address; aggregator max content length `Int.MAX_VALUE`; keep-alive; chunked write; `SSLHandshakeException` containing `certificate_unknown` is **intentionally swallowed** (logged debug).
  - `MockWebServerWrapper` (library default): custom `Dispatcher` returns an enqueued `MockResponse` if present else dispatches to the handler; `ssl != null` → `useHttps(socketFactory, false)` (no client auth); supports `takeRequest`/enqueue.
  - No-handler default response: `404 "no requesthandler configured"`.
- **HTTPS/TLS** (`Ssl`/`SslKeystore`): by default generates an in-memory self-signed cert (BouncyCastle X509, CN=localhost, SHA256withRSA, 365-day, extensions subjectKeyIdentifier/authorityKeyIdentifier/basicConstraints(CA true)/SAN(dNSName localhost + 127.0.0.1)/keyUsage(digitalSignature)/extendedKeyUsage(serverAuth)); or loads PKCS12/JKS. `sslEngine()`: `useClientMode=false`, `needClientAuth=false`. Ready-to-use `docker-compose-ssl.yaml`. When TLS is on, `scheme=https` and discovery/issuer URLs reflect it.
- **Image build**: Google **Jib** (no Dockerfile). `from.image=cgr.dev/chainguard/jre:latest-dev`, multi-arch `linux/amd64` + `linux/arm64`, `ports=[8080]`, `mainClass=...StandaloneMockOAuth2ServerKt`, `jvmFlags=[--sun-misc-unsafe-memory-access=allow]`. Local: `./gradlew -Djib.from.platforms=linux/amd64 jibDockerBuild`. Go reimpl authors its own Dockerfile/ko (match chainguard-style nonroot base, port 8080, multi-arch).
- **Application plugin**: `run`/`installDist`/`distZip`/`distTar`; Java 17 bytecode, built on JDK 21 in CI, Kotlin consumer min 1.9.
- **Publishing**: library `no.nav.security:mock-oauth2-server` → Maven Central (Vanniktech plugin, GPG signing) + GitHub Packages (`maven.pkg.github.com/navikt/mock-oauth2-server`); MIT license. Image → GHCR with **SemVer tags, no `v` prefix**, plus moving `MAJOR` and `MAJOR.MINOR` tags (`docker buildx imagetools create`) and `latest`. Docker Hub wired but disabled. Version is **never stored in the repo** — injected via `-Pversion` from the GitHub Release tag.
- **CI/Release**: `test-pr` (PR build, JDK 21, ignores `*.md`), `build-master` (build + dependency-submission + release-drafter drafting `$NEXT_PATCH_VERSION`), `publish-release` (Maven Central + GitHub Packages + Jib), `dokka` (deploys API docs to gh-pages `/docs`). `release-drafter` is the version source of truth.
- **Docker Compose recipes** (plain + SSL): both `image :latest`, `8080:8080`, `LOG_LEVEL=debug`, `SERVER_PORT=8080`, `JSON_CONFIG_PATH=/app/config.json`, healthcheck `wget --spider http://localhost:8080/isalive`. Plain mounts `config.json` + `login.example.html` (`/app/login/`) + `static/` (`/app/static/`); SSL mounts `config-ssl.json` (NettyWrapper + `ssl:{}`). **Scenario 1** (container-to-container): app uses `http://mock-oauth2-server:8080/default/jwks`. **Scenario 2** (+browser, e.g. auth-code): add `127.0.0.1 host.docker.internal` to `/etc/hosts` (Linux) and set `hostname: host.docker.internal` so the `iss` (derived from the request host) resolves identically for both the app and the browser. Each service needs a distinct host port.
- **Migration contracts (advertised surface, match for drop-in)**: **5.0.0** — matching `requestMapping` claims take priority over login-page claims (login claims ADD only, never overwrite, incl. `sub`). **4.0.0** — strict refresh-token validation; unknown/expired/revoked and cross-issuer refresh tokens fail `400 invalid_grant`; must use a real refresh token from a prior request.

---

## Parity Gotchas for a Go Port

Subtle behaviors to preserve (several are critic corrections to the draft catalog):

- **Routing is by SUFFIX, not exact/prefix.** Match the endpoint suffix and treat the remainder as `issuerId`. Replicate the full endpoint-suffix list (`/.well-known/oauth-authorization-server`, `/.well-known/openid-configuration`, `/authorize`, `/token`, `/endsession`, `/revoke`, `/jwks`, `/userinfo`, `/introspect`, `/debugger`, `/debugger/callback`) and the `*` → `.*` wildcard compilation. Empty-path route = wildcard match on method only (powers the OPTIONS preflight). Don't forget `/favicon.ico` and the OPTIONS catch-all.
- **Discovery field order** (CORRECTED): `issuer, authorization_endpoint, end_session_endpoint, revocation_endpoint, token_endpoint, userinfo_endpoint, jwks_uri, introspection_endpoint`, then the `*_supported` arrays. `token_endpoint` is **5th**, not 3rd. Neither the README example nor the draft catalog matched the code.
- **Refresh cross-issuer `error_description`** (CORRECTED): emit `"refresh_token was issued by a different issuer"` (the client-visible `setDescription` text), **not** the internal exception message `"refresh_token issuer mismatch"`.
- **Two distinct form-body parsers** (UNDER-SPECIFIED in draft — do not unify them):
  1. `OAuth2HttpRequest.formParameters` = `keyValuesToMap('&')` → a **flat `Map<String,String>`**: duplicate keys collapse to the **last** value; each key/value URL-decoded **and trimmed**; entries without `=` are dropped; a value with a second `=` is truncated (split destructured into exactly key/value). Drives `grant_type` detection, `/revoke` `token`+`token_type_hint`, login `username`/`claims`, debugger params, introspect `token`.
  2. `RequestMapping` matching and `${...}` templating use Nimbus's `tokenRequest.toHTTPRequest().bodyAsFormParameters` → **`Map<String,List<String>>`** (multi-valued, space-joined). Mixing these (or applying multi-valued semantics where the flat map is used, or vice-versa) will diverge.
- **Custom `typ` tokens fail this server's own verification** (UNDER-SPECIFIED): `verify()` pins the JOSE type verifier to `typ="JWT"`. A token minted with a callback `typeHeader` other than `JWT` (e.g. `at+jwt`) will **not** verify → `/userinfo` returns `401 invalid_token` and `/introspect` returns `{active:false}`. If you relax `typ` enforcement on verify, you'll diverge.
- **`form_post` without `state` throws → HTTP 500** (UNDER-SPECIFIED): the FORM_POST branch reads `authenticationSuccessResponse.state.value` unconditionally; a missing `state` NPEs → mapped to `server_error` (500). The `query`/`fragment` branch uses `toURI()` and tolerates a null `state`.
- **End-session reads params from the URL QUERY only** (UNDER-SPECIFIED): even though the route is `any()` (GET/POST/any), `post_logout_redirect_uri`/`state` come from `url.queryParameter(...)`. A POST carrying them in the form body is ignored (always returns the "logged out" page). The naive `?state=` append also breaks if the redirect URI already has a query string.
- **Static-asset Content-Type is `Files.probeContentType(...) ?: "application/octet-stream"`** (CORRECTED): there is **no extension table**. The `.txt→text/plain`, `.css→text/css`, `.js→text/javascript` mappings in the draft are observed JVM/OS-dependent outputs, not guaranteed. A Go port (`mime.TypeByExtension` / `http.DetectContentType`) will legitimately differ — match the **traversal guard** (canonical-path prefix check) and the `404 "not found"` behavior, but treat exact MIME strings as non-contractual.
- **Two distinct `405` bodies**: generic `"method not allowed"` vs `GET /token`'s explicit `"unsupported method"`. `404` default body is `"no routes found"`.
- **Entire error JSON body is lowercased** (affects `error_description` text); HTTP `302` coerced to `400`.
- **Token-callback queue** is global, peek-then-conditional-poll, **issuer-matched HEAD only**: a queued callback for issuer A blocks consumption for issuer B even if B's request arrives first. Single-use, FIFO; also consulted by the refresh grant. Use a mutex-guarded deque.
- **`expires_in` uses real `Instant.now()`**, while `exp` uses the (possibly frozen) `systemTime` — they diverge under a frozen clock. Include `expires_in` even when `0`.
- **`kid` = `issuerId`** (deterministic), not a thumbprint. Bundled file kids (`initialkey-1..5`) are **discarded** and replaced by `issuerId` on use. Ship an equivalent embedded 5-key RSA JWKS for stable keys across restarts.
- **Reject `ES256K` and `ES512`** explicitly (and `EdDSA`/HMAC/`none`); the `"Unsupported algorithm: EdDSA"` error text is asserted by tests. RSA fixed at 2048; EC curve inferred from the 3 chars after `ES`.
- **Refresh token is a bare UUID, or an UNSIGNED (`alg=none`) PlainJWT `{jti, nonce}`** when a nonce is present. Rotation removes the old token (which then fails) and the rotated token loses the nonce/PlainJWT form.
- **PKCE enforced only when a `code_verifier` is presented**; a challenge alone need not be redeemed. Auth code is single-use and removed **before** the PKCE check (a failed attempt invalidates the code).
- **`azp` only for `authorization_code`** (added post-merge, non-overridable); **`tid` = `issuerId`** always (overridable) — both only via `DefaultOAuth2TokenCallback`, never `RequestMappingTokenCallback`.
- **`id_token aud` is always `[client_id]`**, independent of the 4-step `access_token aud` chain (configured → token-exchange `audience` param → non-OIDC scopes → `["default"]`).
- **No secret validation** for any grant; **assertion/`subject_token` signatures are not verified** (only parsed) for jwt-bearer/token-exchange. Token-exchange `private_key_jwt` is only **structurally** validated (lifetime ≤120s, `iss==sub==client_id`, single accepted audience).
- **Login claims merge with `putIfAbsent`** (mapping wins; login claims ADD only). `subject` is a **synthetic** `requestParam`, not an actual token-request field. `${client_id}`/`${clientId}` cannot be shadowed by a same-named form param.
- **Debugger session AES key is regenerated per process** — sessions don't survive restart or span nodes; `client_auth_method.valueOf` is case-sensitive; only `CLIENT_SECRET_BASIC`/`CLIENT_SECRET_POST` supported.
- **`takeRequest` is backend-dependent** (MockWebServer only; Netty/HTTPS/standalone can't record). Preserve **raw body bytes** (param order), not a reparsed map.
- **`anyToken` expiry quirk**: `Duration.toMillis()` is treated as seconds → ~1000× inflation. Replicate only if matching exact behavior.
- **UserInfo returns the entire claim set verbatim** (no scoping/filtering); tokens from a different issuer/algorithm/`typ` are rejected with `401`. Introspection serializes a single-element `aud` as a **string** (`WRITE_SINGLE_ELEM_ARRAYS_UNWRAPPED`) and defaults `token_type` to `"Bearer"`; missing/empty `Authorization` → `400 invalid_client`, but an invalid signature is **not** an error (→ `{active:false}`).
- **Uncatalogued public methods to expose for parity**: `OAuth2TokenProvider.getAlgorithm()`, `KeyProvider.algorithm()`/`keyType()`/`generate(algorithm)` (runtime algorithm/key-type swap).