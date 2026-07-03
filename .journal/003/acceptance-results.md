# webtest acceptance results

Started: 2026-07-03T06:10:05.388Z

Config: `{"base":"http://localhost:9080","issuer":"acme","issuer2":"beta","configuredIssuer":"configured","clientId":"web-client","controlToken":""}`

## Discovery / .well-known

| Check | Status | Expected | Actual |
| --- | --- | --- | --- |
| openid-configuration returns 200 JSON | PASS | 200 + JSON | 200 + JSON |
| field order matches the 13-key contract | PASS | issuer, authorization_endpoint, end_session_endpoint, revocation_endpoint, token_endpoint, userinfo_endpoint, jwks_uri, introspection_endpoint, response_types_supported, response_modes_supported, subject_types_supported, id_token_signing_alg_values_supported, code_challenge_methods_supported | issuer, authorization_endpoint, end_session_endpoint, revocation_endpoint, token_endpoint, userinfo_endpoint, jwks_uri, introspection_endpoint, response_types_supported, response_modes_supported, subject_types_supported, id_token_signing_alg_values_supported, code_challenge_methods_supported |
| forbidden keys are absent | PASS | none present | none present |
| both .well-known paths are byte-identical | PASS | identical bodies | identical |
| no Link response header | PASS | absent | absent |
| issuer field equals base + / + issuer | PASS | http://127.0.0.1:9080/acme | http://127.0.0.1:9080/acme |
| enum contents exactly match the contract | PASS | all 5 enums match | all match |
| every endpoint/jwks_uri starts with the issuer URL | PASS | all start with http://127.0.0.1:9080/acme | all prefixed |

Tally: 8 PASS / 0 FAIL / 0 SKIP

## JWKS & Key Isolation

| Check | Status | Expected | Actual |
| --- | --- | --- | --- |
| jwks keys are well-formed (sig / RS256 / RSA, kid == issuer) | PASS | sig/RS256/RSA, kid=acme | 1 key(s), all sig/RS256/RSA, kid=acme |
| no private JWK members (d, p, q, dp, dq, qi) | PASS | no private members | none present |
| fresh issuer materializes a key set on first hit | PASS | non-empty, kid=mat-af0080aff905 | 1 key(s), kid=mat-af0080aff905 |
| distinct issuers → distinct kid and distinct modulus n | PASS | distinct kid and n | kid "acme" vs "beta"; moduli differ |
| cross-issuer isolation: minted token verifies only under its issuer | PASS | true under acme, false under beta | verified only under its own issuer |
| reserved prefix: GET /_mock/jwks → 404 not_found (OAuth2 envelope) | PASS | 404 + { error: 'not_found' } | 404 + error='not_found' |

Tally: 6 PASS / 0 FAIL / 0 SKIP

## Authorization Endpoint

| Check | Status | Expected | Actual |
| --- | --- | --- | --- |
| response_type=code with state → code + echoed state on callback | PASS | code + state=1f88db5850b1ac8440c43dca7f066ae3 | code=38397c01… state=1f88db5850b1ac8440c43dca7f066ae3 |
| no state param → code present, state omitted from callback | PASS | code present, state absent | code=36e40d4d…, no state |
| response_type token/id_token/garbage → error at redirect_uri, no code | PASS | token/id_token/garbage → error, no code | all 3 errored to redirect_uri, no code |
| POST /authorize login form without username → 400 invalid_request | PASS | 400 + invalid_request | 400 + invalid_request |
| prompt=none → code without rendering a login page | PASS | code + callback.html (no login page) | code issued, landed on callback.html |
| GET /favicon.ico → 200 | PASS | 200 | 200 |

Tally: 6 PASS / 0 FAIL / 0 SKIP

## Token Grants (all six)

| Check | Status | Expected | Actual |
| --- | --- | --- | --- |
| client_credentials → access-only, sub == client_id | PASS | 200 access-only, sub==clientId | 200 sub=web-client |
| authorization_code → id + access + refresh | PASS | 200 + all three tokens | access + id + refresh present |
| authorization code is single-use | PASS | re-use → 400 invalid_grant | 400 invalid_grant |
| PKCE S256 happy path | PASS | S256 verifier → 200 | 200 with tokens |
| PKCE mismatch → invalid_grant (pkce), then code is burned | PASS | wrong→400 invalid_grant/pkce; then burned | 400 invalid_grant (invalid_pkce: code_verifier does not compute to code_challenge from request); retry 400 |
| PKCE asymmetry: challenge without verifier → invalid_grant | PASS | missing verifier → 400 invalid_grant | 400 invalid_grant |
| refresh_token → fresh access_token, same sub, different jti | PASS | 200, same sub, new jti | sub=undefined jti≠orig |
| refresh_token unknown → invalid_grant | PASS | unknown refresh → 400 invalid_grant | 400 invalid_grant |
| cross-issuer refresh → invalid_grant (different issuer) | PASS | 400 invalid_grant / different issuer | 400 invalid_grant (refresh_token was issued by a different issuer) |
| no rotation: same refresh redeems twice | PASS | same token redeems twice → 200,200 | 200, 200 |
| password → id + access, NO refresh, sub == username | PASS | 200 id+access, no refresh, sub=bob; any pw | sub=bob, no refresh, 2nd pw 200 |
| jwt-bearer → access-only, claims kept, iss re-stamped | PASS | access-only, sub/custom kept, iss re-stamped | sub=obo-user iss=http://localhost:9080/acme |
| jwt-bearer missing assertion → invalid_request | PASS | no assertion → 400 invalid_request | 400 invalid_request |
| jwt-bearer no scope anywhere → invalid_request | PASS | no scope → 400 invalid_request | 400 invalid_request |
| token-exchange → access-only, issued_token_type, aud, no scope | PASS | 200 access-only, issued_token_type, aud=exchanged-aud, no scope | all satisfied |
| token-exchange without client auth → invalid_request (ClientAuthentication) | PASS | 400 invalid_request / ClientAuthentication | 400 invalid_request (request must contain some form of ClientAuthentication.) |
| private_key_jwt structural rules (token-exchange) | PASS | valid→200; lifetime/iss/sub/empty-aud/wrong-aud → 400 invalid_request | all 6 structural cases as expected |
| grant_type: blank → invalid_request, unknown → invalid_grant | PASS | blank→invalid_request; foo→invalid_grant | both as expected |
| client_secret_basic accepted (client_credentials) | PASS | Basic auth → 200, sub==clientId | 200 sub=web-client |

Tally: 19 PASS / 0 FAIL / 0 SKIP

## Token Content & Callbacks

| Check | Status | Expected | Actual |
| --- | --- | --- | --- |
| default registered claims present and well-formed (client_credentials) | PASS | sub,aud,iss,iat,nbf,exp,jti present; iss/exp/jti valid | all valid |
| tid claim equals the issuer id | PASS | tid === acme | acme |
| azp scoping: authorization_code id_token only (never access / client_credentials) | PASS | id_token azp === web-client; no azp on either access token | id_token azp set; access tokens carry none |
| id_token aud is exactly [client_id] | PASS | aud === [web-client] | ["web-client"] |
| access_token aud default chain (scope-derived vs ["default"]) | PASS | ["default"] for OIDC-only scopes; non-OIDC scope carried into aud | ["default"] and ["custom-api"] |
| nonce passthrough on authorization_code, never injectable via /token | PASS | cached nonce stamped into id_token; token-request nonce param ignored | id_token nonce matches; client_credentials nonce absent |
| configured issuer fixture claims + at+jwt header | PASS | config-seeded claims + at+jwt typ | all match |
| at+jwt token self-verifies (jwks + userinfo + introspect) | PASS | verify + userinfo 200 + introspect active:true | all pass |
| scenario override is one-shot and reverts to default | PASS | first token overridden (marker + 1200s exp), second reverted to default | override applied once then reverted |
| requestMapping templates ${username} into a claim (password grant) | PASS | mapped_user === 'carol' (templated from username) | carol |
| foreign typ token is rejected by userinfo and introspect | PASS | userinfo 401 + introspect active:false | both reject the foreign typ |

Tally: 11 PASS / 0 FAIL / 0 SKIP

## Token Lifecycle

| Check | Status | Expected | Actual |
| --- | --- | --- | --- |
| userinfo returns the full claim set for a minted access token | PASS | 200 sub=u1 email=u1@x | 200 sub=u1 email=u1@x |
| userinfo with a garbage bearer → 401 invalid_token + WWW-Authenticate | PASS | 401 invalid_token + WWW-Authenticate | 401 invalid_token; Bearer error="invalid_token" |
| userinfo with no Authorization header → 401 | PASS | 401 | 401 (invalid_token) |
| introspect a valid access token → active:true with typed claims | PASS | active:true; Bearer; sub/iss; numeric exp/iat; aud string | active:true; sub=introspect-subject; aud="api-one" (string) |
| introspect a garbage token → 200 {active:false} | PASS | 200 {active:false} | 200 {active:false} |
| introspect with no token param → 200 {active:false} | PASS | 200 {active:false} | 200 {active:false} |
| introspect with no Authorization header → 400 invalid_client | PASS | 400 invalid_client | 400 invalid_client |
| revoke a refresh token then re-redeem it → 400 invalid_grant | PASS | revoke 200, then redeem 400 invalid_grant | revoke 200; redeem 400 invalid_grant |
| revoke with token_type_hint=access_token → 400 unsupported_token_type | PASS | 400 unsupported_token_type | 400 unsupported_token_type |
| revoke an unknown refresh token (correct hint) → 200 idempotent no-op | PASS | 200 (idempotent no-op) | 200 |
| endsession redirects to post_logout_redirect_uri carrying state | PASS | callback.html?state=bye | http://localhost:9080/static/callback.html?state=bye |
| endsession redirect without state lands on callback with no state param | PASS | callback.html, no state | http://localhost:9080/static/callback.html |
| endsession without a redirect URI → 200 "logged out" HTML | PASS | 200 HTML containing "logged out" | 200 logged-out page |
| POST endsession honors query params, ignoring a conflicting body | PASS | query wins: callback.html?state=q | http://localhost:9080/static/callback.html?state=q |

Tally: 14 PASS / 0 FAIL / 0 SKIP

## Control Plane (/_mock)

| Check | Status | Expected | Actual |
| --- | --- | --- | --- |
| mint ≡ grant: minted token verifies and userinfo echoes sub | PASS | verifies + userinfo sub m1 | kid=acme alg=RS256 sub=m1 |
| mint reserved issuer ('_mock') → 4xx problem | PASS | status >= 400 problem | 404 (no token) |
| scenario is one-shot AND issuer-matched | PASS | acme skips, beta consumes; depth 1→0 | issuer-matched, single-use as expected |
| scenario list/clear: depth 2 with issuer fields, DELETE → 0 | PASS | depth 2 (issuer fields) → 0 | [acme, beta] → 0 |
| capture list: token request appears with method/path | PASS | count>=1, POST /acme/token | count=1, POST /acme/token |
| capture take: bodyBase64 is byte-exact | PASS | atob(bodyBase64) === exact body | byte-exact match |
| capture take on empty → 404 | PASS | 404 on empty match | 404 |
| self-isolation: control-plane calls are never captured | PASS | no /_mock entries | 0 entries, 0 under /_mock |
| clock freeze: token iat pins to the frozen instant | PASS | iat === 1893456000 | iat === 1893456000 |
| clock advance flips token expiry (introspect + userinfo) | PASS | active true→false, userinfo 401 | expiry flipped as expected |
| clock unfreeze: frozen=false and a fresh token verifies | PASS | frozen false + token verifies | frozen=false, verified |
| reset: clears scenarios+captures, unfreezes, KEEPS keys | PASS | scenarios 0, requests 0, unfrozen, keys kept | reset cleared state and preserved keys |

Tally: 12 PASS / 0 FAIL / 0 SKIP

## CORS, Errors & Static Guard

| Check | Status | Expected | Actual |
| --- | --- | --- | --- |
| CORS on POST reflects Origin (not *) + credentials | PASS | ACAO=http://localhost:9080 + ACAC=true | ACAO=http://localhost:9080 + ACAC=true |
| OPTIONS preflight → 204 + Allow-Methods includes POST | PASS | 204 + Allow-Methods incl POST | 204 + Allow-Methods=POST, GET, OPTIONS |
| OPTIONS preflight echoes request headers → Allow-Headers | SKIP |  |  |
| 405 on protocol path → OAuth2 error envelope (correct-case) | PASS | 405 + { error, error_description } (correct-case) | 405 + error=invalid_request |
| 404 on unknown protocol path → JSON error envelope | PASS | 404 + JSON error envelope | 404 + problem+json { title,status,detail } |
| reserved '_mock' prefix is not a usable issuer → 404 | PASS | 404 error 'not_found' | 404 error 'not_found' |
| control-plane error is problem+json (garbage clock advance) | PASS | status ≥ 400 + application/problem+json | 422 + application/problem+json |
| static traversal guard ('..%2f') → 404, no file leak | PASS | 404 + body without 'module github.com' | 404 + no leak |
| static traversal guard ('%2e%2e%2f') → 404 | PASS | 404 + no config.json leak | 404 + no leak |
| static serves no directory index → 404 | PASS | 404 + no listing | 404 + no listing |
| metrics endpoint not on this listener → 404 JSON | PASS | 404 + JSON envelope | 404 + problem+json { title,status,detail } |

Tally: 10 PASS / 0 FAIL / 1 SKIP

## Manual checks

| Section | Check | Status |
| --- | --- | --- |
| authorize | Interactive login page mints an id_token for the entered identity | PASS |
| authorize | response_mode=fragment returns code+state in the URL fragment | PASS |
| authorize | response_mode=form_post self-submits code+state to redirect_uri | PASS |
| authorize | Debugger completes a full authorize→token round-trip | PASS |
| corsSec | Cross-origin CORS (reflect-origin + credentials) | PASS |

## Overall

91 PASS / 0 FAIL / 1 SKIP (of 92)
