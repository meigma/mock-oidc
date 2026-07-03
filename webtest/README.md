# webtest — browser acceptance console

A repo-local, browser-based acceptance console for the mock OIDC server.

**FOR TESTING ONLY. This is not a product and not a supported UI.** It is a
plain, dependency-free page a developer opens against a running mock server to
exercise the OIDC/OAuth2 surface and the `/_mock` control plane from a real
browser (so browser-only behaviors — CORS, redirect following, WebCrypto JWT
verification — are exercised the way a real client sees them).

The console is vanilla ES-module JavaScript + HTML + one CSS file. No frameworks,
no build step, no external network dependencies (no CDN, no fonts, no remote
URLs). It must be **served by the mock server**, never opened via `file://`
(ES modules, `fetch`, and same-origin control-plane calls all require an HTTP
origin).

## How to run

Build the server binary and run it with the webtest config, which points
`staticAssetsPath` at this directory:

```sh
# from the repo root
go build -o bin/mock-oidc ./cmd/mock-oidc
# (use `mise x -- go build ...` if a bare `go` is missing or the wrong version)

JSON_CONFIG_PATH=webtest/config.json ./bin/mock-oidc serve --addr :18080
```

Then open the console:

```
http://localhost:18080/static/index.html
```

The metrics listener (`:9090`) is separate and unrelated; ignore it.

### Container variant

When running the server in a container, bind-mount this directory to an absolute
path inside the container and use `config.container.json` (identical to
`config.json` except `staticAssetsPath` is the absolute `/webtest`):

```sh
docker run --rm -p 18080:18080 \
  -v "$PWD/webtest:/webtest:ro" \
  -e JSON_CONFIG_PATH=/webtest/config.container.json \
  <image> serve --addr :18080
```

The console is then at `http://localhost:18080/static/index.html`.

## Configuration

The header config panel (persisted in `localStorage`) drives every check:

| Field | Default | Meaning |
| --- | --- | --- |
| Base URL | page origin | Server base the console talks to |
| Issuer | `acme` | Default working issuer |
| Issuer 2 | `beta` | Isolation counterpart issuer |
| Configured issuer | `configured` | Config-seeded issuer (fixture checks) |
| Client ID | `web-client` | Default OAuth client id |
| Control token | *(empty)* | Sent as `X-Mock-Control-Token` when set |

## Layout

```
webtest/
├── index.html            # console page (status banner, config, run-all, exports)
├── callback.html         # OAuth authorize/logout landing page
├── css/console.css       # single stylesheet (light + dark via color-scheme)
├── lib/                  # framework-free modules (contract below)
│   ├── store.js  api.js  jwt.js  jose.js  pkce.js  form.js
│   ├── runner.js manual.js app.js modules.js
├── sections/             # one module per check group (discovery.js is the reference)
├── config.json           # server config (staticAssetsPath: "webtest")
└── config.container.json # same, staticAssetsPath: "/webtest"
```

## Module contract (frozen — implement exactly this)

Every `sections/*.js` default-exports:

```js
{ id, title,
  mount?(el, ctx),                    // optional extra controls
  checks: [{ name, async run(ctx) }], // run returns a CheckResult
  manual?: [{ id, name, instructions, async start?(ctx) }] }
```

`CheckResult = { status: 'PASS'|'FAIL'|'SKIP', expected?, actual?, detail?: {req?, raw?, note?} }`
— build via `ctx.pass(extra?)`, `ctx.fail(expected, actual, detail?)`,
`ctx.skip(reason)`. `run()` must never throw uncaught (the runner catches and
renders FAIL, but prefer explicit fails with useful expected/actual).

```js
ctx = {
  cfg: { base, issuer, issuer2, configuredIssuer, clientId, controlToken },   // from lib/store.js (localStorage-backed)
  api: {  // all base-aware; JSON control calls add X-Mock-Control-Token when set; every helper returns a normalized envelope {status, headers(Headers), json?, text?, finalUrl?, params?}
    discovery(issuer, {oauth=false}={}), jwks(issuer),
    authorizeFollow(issuer, params),   // GET /authorize with redirect:'follow'; default redirect_uri = base+'/static/callback.html'; returns {status, finalUrl, params:{code,state,error,error_description,...parsed from finalUrl query}}
    token(issuer, formObj, {basicAuth}={}),  // POST form-encoded; basicAuth:{user,pass} sets Authorization: Basic
    userinfo(issuer, bearer), introspect(issuer, token, {hint, auth='Bearer x'}={}), revoke(issuer, token, {hint}={}),
    endsessionFollow(issuer, params),  // same follow trick for the logout redirect
    mint(body), scenarioEnqueue(body), scenarioList(), scenarioClear(),
    requestsTake(body), requestsList(query), requestsClear(),
    clockGet(), clockSet(body), clockAdvance(duration), reset(),
    raw(method, path, {headers, body}={}),  // absolute-path escape hatch on base
  },
  jwt: { decode(compact) -> {header,payload}, async verify(compact, jwksJson) -> boolean, typ(compact) },
  jose: { craft({header={alg:'RS256',typ:'JWT'}, payload}) -> dummy-signature compact JWT,
          craftPrivateKeyJwt({clientId, aud, iatOffset=0, lifetime=60, iss, sub}) -> compact JWT (iss/sub default clientId) },
  pkce: { verifier() -> random string, async challengeS256(v) -> base64url(sha256(v)) },
  form: { encode(obj) -> urlencoded string },
  pass, fail, skip, newState() -> random string,
  authCode: async (issuer, {scope='openid profile', nonce, state, pkce=false, extra={}}={}) -> {code, state, verifier?}   // convenience: full authorizeFollow returning a fresh code (throws ctx-fail-able Error on missing code)
}
```

`sections/discovery.js` is the fully-working reference — copy its structure when
adding a section. `sections/*.js` files are registered (in order) in
`lib/modules.js`.

## Golden rule

**Never open the console via `file://`.** Always serve it from the mock server
(`/static/index.html`). Browser fetch cannot read a `302 Location`
(`opaqueredirect`), so authorize/logout outcomes are read by following the
redirect to `callback.html` and parsing `response.url`.
