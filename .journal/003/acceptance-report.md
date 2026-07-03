# mock-oidc Browser Acceptance Report — Session 003

Date: 2026-07-03 (run), authored 2026-07-02/03 local
Target: `mock-oidc:dev` container image (apko, arm64) built from `master`,
run with the `webtest/` console bind-mounted read-only and the host port
remapped (`-p 9080:8080`) so the whole pass doubles as the port-remap /
proxy-identity check. Driver: Chrome via chrome-devtools MCP.

```
docker run --rm -v "$REPO/webtest:/webtest:ro" \
  -e JSON_CONFIG_PATH=/webtest/config.container.json \
  -p 9080:8080 mock-oidc:dev
```

## Verdict

**ACCEPTED** (after two blocker fixes, both merged and re-verified against a
rebuilt image — see Findings). Final state: **91 PASS / 0 FAIL / 1 expected
SKIP of 92 checks** in-browser against the container, plus a fully green
out-of-browser curl block. The server is functionally ready for use; the one
remaining pre-publish item is the missing LICENSE (independent of this pass).

## Automated suite results (browser, container, port-remapped)

| Section | Pass | Fail | Skip |
|---|---|---|---|
| Discovery / .well-known | 8 | 0 | 0 |
| JWKS & Key Isolation | 6 | 0 | 0 |
| Authorization Endpoint | 6 | 0 | 0 |
| Token Grants (all six) | 19 | 0 | 0 |
| Token Content & Callbacks | 11 | 0 | 0 |
| Token Lifecycle | 14 | 0 | 0 |
| Control Plane (/_mock) | 12 | 0 | 0 |
| CORS, Errors & Static Guard | 10 | 0 | 1 |
| **Total automated** | **86** | **0** | **1** |

The single SKIP is `OPTIONS preflight echoes request headers → Allow-Headers`:
`Access-Control-Request-Headers` is a forbidden request header a browser
strips from `fetch`, so the echo cannot be exercised from a page. Covered
out-of-band: `curl -X OPTIONS -H 'Access-Control-Request-Headers: content-type'`
confirms the echo (and the integration stage verified it the same way).

## Manual browser flows (all PASS)

1. **Interactive login page** (`prompt=login` forcing it over
   `interactiveLogin:false`): username `alice` + claims JSON → callback →
   code exchange → id_token `sub=alice`, `email=alice@example.com`, nonce and
   state echoed.
2. **response_mode=fragment**: real popup; server auto-issued the code in the
   URL fragment; `callback.html` read `location.hash` and posted back
   code + matching state.
3. **response_mode=form_post**: self-submitting HTML POSTed `code`+`state` to
   `/{issuer}/userinfo`; recovered byte-exact from
   `POST /_mock/requests/take {endpoint:"userinfo"}` (capture-plane trick —
   static pages cannot read POST bodies).
4. **Debugger round trip under port remap**: `/acme/debugger` → authorize →
   callback → real back-channel token exchange against
   `http://localhost:9080/acme/token` (the externally reachable remapped
   address — validates the S5 back-channel fix) → "Token exchange complete"
   with all three tokens.
5. **Cross-origin CORS**: page on `http://localhost:9080`, server addressed as
   `http://127.0.0.1:9080` (different origin). Credentialled cross-origin
   discovery fetch readable (reflect-origin + credentials), and the full
   8-check Discovery suite re-ran green cross-origin.

## Out-of-browser curl block (all green)

- **Zero-config boot**: `/default/authorize` auto-302s with a code;
  `/static/*` 404s (tree unmounted without `staticAssetsPath`).
- **interactiveLogin:true seed**: `GET /authorize` without `prompt` renders
  the login page ("mock-oidc sign-in").
- **TLS** (`JSON_CONFIG='{"httpServer":{"ssl":{}}}'`): HTTPS serves with the
  self-signed localhost cert; every discovery URL is `https://`.
- **Proxy identity**: `X-Forwarded-Proto/Host` reflected into issuer +
  endpoints; port follows the documented precedence (Host-header port when no
  `X-Forwarded-Port`; explicit `X-Forwarded-Port: 443` elides the default →
  `https://idp.example.com/acme`).
- **Metrics**: dedicated `:9090` listener serves Prometheus exposition; 404 on
  the API listener.
- **Static traversal (un-normalized, `--path-as-is`)**: `..%2f`, `%2e%2e%2f`,
  and plain `../` probes all 404 with no file content leak.
- **Boot banner**: "FOR TESTING ONLY" warning logged at startup.
- **Image is shell-less** (apko/Wolfi minimal): `docker exec sh` fails — noted
  as a property, not a defect.

## Findings and resolutions

### F1 (blocker, fixed): `/static/index.html` was unreachable — PR #15 (`aa05d23`)
`http.ServeFile` unconditionally 301-redirects any path ending in
`/index.html` to `./`; the handler 404s directory paths, so the one file most
static trees need was dead. Found when the console's documented entry URL
failed. Fixed with `os.Open` + `http.ServeContent` (keeps conditional/range
handling, drops the URL-path opinions) + regression test. Re-verified in the
rebuilt container: console loads at `/static/index.html`.

### F2 (blocker, fixed): sub-less tokens on the non-interactive auth-code path — PR #16 (`8b8d9e0`)
Confirmed the session-002 open thread: with no login, no configured subject,
tokens minted **without any `sub` claim** — violating OIDC Core (id_token
`sub` REQUIRED; strict client libraries reject it) and upstream parity
(`DefaultOAuth2TokenCallback` defaults subject to a UUID). Fixed with a
per-callback UUID terminal fallback in `DefaultTokenCallback.Subject`;
precedence unchanged (configured > cc→client_id > ROPC/login > UUID).
Live-verified: auth-code tokens carry a stable UUID `sub`; ROPC/cc unchanged.

### F3 (console bug, fixed during integration)
The `introspect with no token param` check POSTed a fully empty body, which
Huma rejects (400) before the handler; corrected to a hint-only body. Server
behavior was correct all along (200 `{active:false}`).

### Corrections to expectations discovered while building
- `_mockx` is NOT a reserved issuer — only `_mock` exact or `_mock/` prefix
  is; the reserved-404 check targets `/_mock/jwks`.
- Discovery/JWKS/aud/azp exactness assertions were verified against source by
  the integration reviewer (field order, private-member absence, azp scoping,
  aud 4-step chain, single-use code burning, introspection scalar-aud
  collapse) — no wrong expectations shipped.

## Evidence

- `acceptance-results.json` — full machine-readable export (8 sections,
  87 checks, 5 manual states, config) from the first container run.
- `acceptance-results-rerun.json` — the post-fix re-run against the image
  rebuilt with PR #15 + PR #16 (87 checks, 0 fails, all manual PASS).
- `acceptance-results.md` — the console's own markdown export.
- `acceptance-tally.png`, `acceptance-final-tally.png` — full-page console
  screenshots (initial automated run; final state with manual PASSes).
- `acceptance-debugger.png` — debugger result page under port remap.

## Deliberately out of scope (per design/plan)

Nested multi-segment issuers; in-process embedded library API; signature
verification of jwt-bearer/token-exchange assertions and private_key_jwt
(parse-only by design); actor_token/act chains; hybrid/implicit response
types; custom `loginPagePath`.

## Flags for pre-publish

1. **LICENSE missing** — README §License explicitly says to add one before
   publishing. Blocker for "publish", independent of functionality.
2. `.agents/` skill docs still reference `template-go-api` (cosmetic).
3. Version/CHANGELOG lineage inherited from the template (`1.0.4`) — reset
   before the first real release.
