// corsSec.js — CORS, protocol/control error envelopes, and the static guard.
//
// Follows the discovery.js reference: default-export { id, title, checks, manual },
// every check returns ctx.pass/fail/skip with a useful expected/actual plus
// detail.req (the request line) and detail.raw (truncated response text) on the
// failure paths. See README.md for the frozen module contract.
//
// A few checks deviate from the literal task probes because the SERVER code and
// BROWSER fetch rules make the literal form untestable or wrong (each deviation
// is called out inline and in the returned notes):
//   * Reserved-prefix 404 uses GET /_mock/jwks, not GET /_mockzz/token.
//     ParseIssuerID only reserves the EXACT segment "_mock" (or "_mock/…"), so
//     "_mockzz" is a perfectly normal issuer and GET on its POST-only /token is a
//     plain 405 — it never exercises the reserved guard. "_mock" as the issuer is
//     the only value that yields not_found (404).
//   * Preflight Allow-Headers echo is a SKIP: Access-Control-Request-Headers is a
//     forbidden request-header name, so a browser silently drops it on fetch and
//     the server never has anything to echo. curl covers that leg.
//   * The 404-on-unknown-endpoint body is RFC-9457 problem+json (that path is not
//     a recognized protocol suffix, so it hits the router NotFound fallback, not
//     the OAuth2 fallback). The check accepts either JSON error dialect.

const RAW_MAX = 500;

// clip truncates response text for detail.raw so a huge body never floods a cell.
function clip(s, n = RAW_MAX) {
  const str = typeof s === 'string' ? s : String(s == null ? '' : s);
  return str.length <= n ? str : str.slice(0, n) + ' …';
}

// pageOrigin is the browser page origin — the value the browser stamps into the
// Origin header on every cross-origin (and every non-GET/HEAD) request, and thus
// exactly what the reflect-origin CORS middleware echoes back.
function pageOrigin() {
  return typeof location !== 'undefined' && location.origin ? location.origin : '';
}

// sameOrigin reports whether the configured base shares the page origin. Response
// headers (ACAO/ACAM/ACAC) are only readable from JS on a same-origin response;
// on a cross-origin one they are not CORS-exposed, so those checks SKIP and defer
// to the cross-origin manual entry.
function sameOrigin(base) {
  try {
    return new URL(base).origin === pageOrigin();
  } catch {
    return false;
  }
}

// correctCase reports whether an OAuth2 error code is the lowercase snake form the
// contract pins (e.g. invalid_request / not_found), catching upstream's all-caps
// defect this project deliberately does NOT replicate.
function correctCase(code) {
  return typeof code === 'string' && /^[a-z][a-z0-9_]*$/.test(code);
}

// normalizedPath returns the path the browser will actually request after WHATWG
// URL normalization (the same normalization fetch applies), so a traversal probe
// can detect up front whether the browser collapsed the "../" away — in which case
// the probe cannot be sent un-normalized and is SKIPped.
function normalizedPath(base, path) {
  try {
    return new URL(base + path).pathname;
  } catch {
    return path;
  }
}

// stillTraverses reports whether a normalized path still carries a dot-dot escape.
// If normalization stripped it, the browser flattened the probe and we cannot
// exercise the server guard from JS.
function stillTraverses(p) {
  return /\.\.|%2e%2e|%2e\.|\.%2e/i.test(p);
}

export default {
  id: 'corsSec',
  title: 'CORS, Errors & Static Guard',
  checks: [
    {
      name: 'CORS on POST reflects Origin (not *) + credentials',
      async run(ctx) {
        const req = { method: 'POST', url: `${ctx.api.base}/${ctx.cfg.issuer}/token` };
        if (!sameOrigin(ctx.api.base)) {
          return ctx.skip(
            'Base URL is cross-origin; the browser does not expose Access-Control-Allow-Origin / -Credentials ' +
              'to JS on a simple CORS POST. The reflect-origin + credentials behavior is proven by the ' +
              'cross-origin manual check (a readable credentialled response only works when they are set).',
          );
        }
        // A same-origin POST still carries an Origin header (every non-GET/HEAD
        // request does), so the reflect-origin middleware attaches CORS headers.
        const r = await ctx.api.token(ctx.cfg.issuer, {
          grant_type: 'client_credentials',
          client_id: ctx.cfg.clientId,
          scope: 'api',
        });
        if (r.networkError || r.status === 0) {
          return ctx.fail('a readable POST response', 'network error', { req, raw: clip(r.text) });
        }
        const acao = r.headers.get('Access-Control-Allow-Origin');
        const acac = r.headers.get('Access-Control-Allow-Credentials');
        const want = pageOrigin();
        if (acao !== want || acao === '*' || acac !== 'true') {
          return ctx.fail(
            `Access-Control-Allow-Origin=${want} (reflected, not *) + Access-Control-Allow-Credentials=true`,
            `Access-Control-Allow-Origin=${acao} / Access-Control-Allow-Credentials=${acac}`,
            { req, raw: clip(r.text) },
          );
        }
        return ctx.pass({
          expected: `ACAO=${want} + ACAC=true`,
          actual: `ACAO=${acao} + ACAC=${acac}`,
          detail: { req, note: `HTTP ${r.status}; origin reflected verbatim, never *` },
        });
      },
    },
    {
      name: 'OPTIONS preflight → 204 + Allow-Methods includes POST',
      async run(ctx) {
        const path = `/${ctx.cfg.issuer}/token`;
        const req = { method: 'OPTIONS', url: `${ctx.api.base}${path}` };
        // The browser drops the forbidden Access-Control-Request-* headers, but any
        // OPTIONS with an Origin still gets the fixed method set from the middleware.
        const r = await ctx.api.raw('OPTIONS', path, {
          headers: { 'Access-Control-Request-Method': 'POST', 'Access-Control-Request-Headers': 'content-type' },
        });
        if (r.status !== 204) {
          return ctx.fail('204', String(r.status || 'network error'), { req, raw: clip(r.text) });
        }
        if (!sameOrigin(ctx.api.base)) {
          return ctx.pass({
            expected: '204 (+ Allow-Methods incl POST, not JS-readable cross-origin)',
            actual: '204',
            detail: {
              req,
              note: 'Access-Control-Allow-Methods is not CORS-exposed on a cross-origin response; 204 confirmed.',
            },
          });
        }
        const acam = r.headers.get('Access-Control-Allow-Methods') || '';
        if (!/\bPOST\b/.test(acam)) {
          return ctx.fail('Access-Control-Allow-Methods contains POST', `Access-Control-Allow-Methods=${acam}`, { req });
        }
        return ctx.pass({
          expected: '204 + Allow-Methods incl POST',
          actual: `204 + Allow-Methods=${acam}`,
          detail: { req },
        });
      },
    },
    {
      name: 'OPTIONS preflight echoes request headers → Allow-Headers',
      async run(ctx) {
        const path = `/${ctx.cfg.issuer}/token`;
        const req = { method: 'OPTIONS', url: `${ctx.api.base}${path}` };
        const r = await ctx.api.raw('OPTIONS', path, {
          headers: { 'Access-Control-Request-Headers': 'content-type' },
        });
        const alh = r.headers.get('Access-Control-Allow-Headers');
        if (alh && /content-type/i.test(alh)) {
          // Unexpected from a browser, but honor it if the header did survive.
          return ctx.pass({
            expected: 'Access-Control-Allow-Headers echoes content-type',
            actual: `Access-Control-Allow-Headers=${alh}`,
            detail: { req },
          });
        }
        return ctx.skip(
          'Access-Control-Request-Headers is a forbidden request-header name — the browser strips it on fetch, ' +
            'so the server has nothing to echo into Access-Control-Allow-Headers. Verified out-of-band with curl.',
        );
      },
    },
    {
      name: '405 on protocol path → OAuth2 error envelope (correct-case)',
      async run(ctx) {
        const path = `/${ctx.cfg.issuer}/token`;
        const req = { method: 'GET', url: `${ctx.api.base}${path}` };
        const r = await ctx.api.raw('GET', path); // /token is POST-only → 405
        if (r.status !== 405) {
          return ctx.fail('405', String(r.status || 'network error'), { req, raw: clip(r.text) });
        }
        const j = r.json;
        if (!j || typeof j.error !== 'string' || typeof j.error_description !== 'string') {
          return ctx.fail('{ error, error_description } JSON', 'missing OAuth2 fields', { req, raw: clip(r.text) });
        }
        if (!correctCase(j.error)) {
          return ctx.fail('correct-case error code (e.g. invalid_request)', j.error, { req, raw: clip(r.text) });
        }
        return ctx.pass({
          expected: '405 + { error, error_description } (correct-case)',
          actual: `405 + error=${j.error}`,
          detail: { req, note: j.error_description },
        });
      },
    },
    {
      name: '404 on unknown protocol path → JSON error envelope',
      async run(ctx) {
        // An unrecognized endpoint under an issuer matches no route → the router
        // NotFound fallback (RFC-9457 problem+json), NOT the OAuth2 fallback; the
        // check accepts either JSON error dialect (task said "OAuth2 shape").
        const path = `/${ctx.cfg.issuer}/nonexistent-endpoint`;
        const req = { method: 'GET', url: `${ctx.api.base}${path}` };
        const r = await ctx.api.raw('GET', path);
        if (r.status !== 404) {
          return ctx.fail('404', String(r.status || 'network error'), { req, raw: clip(r.text) });
        }
        const j = r.json;
        const oauth = j && typeof j.error === 'string';
        const problem = j && (typeof j.title === 'string' || typeof j.detail === 'string' || typeof j.status === 'number');
        if (!oauth && !problem) {
          return ctx.fail('404 + JSON error envelope', 'non-JSON / unstructured body', { req, raw: clip(r.text) });
        }
        if (oauth && !correctCase(j.error)) {
          return ctx.fail('correct-case error code', j.error, { req, raw: clip(r.text) });
        }
        return ctx.pass({
          expected: '404 + JSON error envelope',
          actual: `404 + ${oauth ? 'OAuth2 { error:' + j.error + ' }' : 'problem+json { title,status,detail }'}`,
          detail: { req, note: oauth ? undefined : 'unknown-endpoint 404 renders RFC-9457 problem+json' },
        });
      },
    },
    {
      name: "reserved '_mock' prefix is not a usable issuer → 404",
      async run(ctx) {
        // Deviation: /_mockzz/token would be a plain 405 ("_mockzz" is a normal
        // issuer; only the exact segment "_mock" is reserved). GET /_mock/jwks
        // routes to the jwks handler with issuer "_mock", which ParseIssuerID
        // rejects as not_found (404). A real issuer (acme) would return 200 keys.
        const path = '/_mock/jwks';
        const req = { method: 'GET', url: `${ctx.api.base}${path}` };
        const r = await ctx.api.raw('GET', path);
        if (r.status === 200 && r.json && Array.isArray(r.json.keys)) {
          return ctx.fail('404 (reserved prefix rejected)', '200 JWKS — reserved "_mock" leaked as an issuer', {
            req,
            raw: clip(r.text),
          });
        }
        if (r.status !== 404) {
          return ctx.fail('404', String(r.status || 'network error'), { req, raw: clip(r.text) });
        }
        const j = r.json;
        if (j && j.error === 'not_found') {
          return ctx.pass({
            expected: "404 error 'not_found'",
            actual: "404 error 'not_found'",
            detail: { req, note: j.error_description },
          });
        }
        if (j && typeof j.error === 'string') {
          return ctx.fail("404 error 'not_found'", `404 error '${j.error}'`, { req, raw: clip(r.text) });
        }
        // problem+json 404 (no oauth error member) still proves the prefix is not
        // routable as an issuer; accept it and note the dialect.
        const problem = j && (typeof j.title === 'string' || typeof j.detail === 'string' || typeof j.status === 'number');
        if (problem) {
          return ctx.pass({
            expected: "404 (ideally error 'not_found')",
            actual: '404 problem+json (reserved prefix not routable as issuer)',
            detail: { req, note: 'reserved "_mock" surfaced via the RFC-9457 NotFound fallback, not the OAuth2 shape' },
          });
        }
        return ctx.fail('404 + JSON error envelope', 'non-JSON body', { req, raw: clip(r.text) });
      },
    },
    {
      name: 'control-plane error is problem+json (garbage clock advance)',
      async run(ctx) {
        const req = { method: 'POST', url: `${ctx.api.base}/_mock/clock/advance`, body: '{"duration":"not-a-duration"}' };
        const r = await ctx.api.clockAdvance('not-a-duration');
        const ct = r.headers.get('Content-Type') || '';
        if (r.status < 400) {
          return ctx.fail('status ≥ 400', String(r.status || 'network error'), { req, raw: clip(r.text) });
        }
        if (!/problem\+json/i.test(ct)) {
          return ctx.fail("Content-Type contains 'problem+json'", `Content-Type=${ct || '(none)'}`, {
            req,
            raw: clip(r.text),
          });
        }
        return ctx.pass({
          expected: 'status ≥ 400 + application/problem+json',
          actual: `${r.status} + ${ct}`,
          detail: { req, note: 'control/infra errors use RFC-9457 problem+json, never the OAuth2 shape' },
        });
      },
    },
    {
      name: "static traversal guard ('..%2f') → 404, no file leak",
      async run(ctx) {
        const path = '/static/..%2fgo.mod';
        const req = { method: 'GET', url: `${ctx.api.base}${path}` };
        const norm = normalizedPath(ctx.api.base, path);
        if (!stillTraverses(norm)) {
          return ctx.skip(
            `browser normalized the probe to ${norm}; it cannot be sent un-normalized from a page. curl covers this.`,
          );
        }
        const r = await ctx.api.raw('GET', path);
        const leaked = typeof r.text === 'string' && r.text.includes('module github.com');
        if (r.status !== 404 || leaked) {
          return ctx.fail('404 with no out-of-tree file contents', `${r.status}${leaked ? ' + go.mod leaked' : ''}`, {
            req,
            raw: clip(r.text),
          });
        }
        return ctx.pass({
          expected: "404 + body without 'module github.com'",
          actual: '404 + no leak',
          detail: { req, note: `sent as ${norm}` },
        });
      },
    },
    {
      name: "static traversal guard ('%2e%2e%2f') → 404",
      async run(ctx) {
        const path = '/static/%2e%2e%2fconfig.json';
        const req = { method: 'GET', url: `${ctx.api.base}${path}` };
        const norm = normalizedPath(ctx.api.base, path);
        if (!stillTraverses(norm)) {
          return ctx.skip(
            `browser normalized %2e%2e to a plain segment (${norm}); the probe cannot be sent un-normalized ` +
              'from a page. curl covers this variant.',
          );
        }
        const r = await ctx.api.raw('GET', path);
        const leaked = typeof r.text === 'string' && r.text.includes('staticAssetsPath');
        if (r.status !== 404 || leaked) {
          return ctx.fail('404 with no config.json contents', `${r.status}${leaked ? ' + config leaked' : ''}`, {
            req,
            raw: clip(r.text),
          });
        }
        return ctx.pass({
          expected: '404 + no config.json leak',
          actual: '404 + no leak',
          detail: { req, note: `sent as ${norm}` },
        });
      },
    },
    {
      name: 'static serves no directory index → 404',
      async run(ctx) {
        const path = '/static/';
        const req = { method: 'GET', url: `${ctx.api.base}${path}` };
        const r = await ctx.api.raw('GET', path);
        const looksLikeIndex = typeof r.text === 'string' && /<html|Index of|<title/i.test(r.text);
        if (r.status !== 404 || looksLikeIndex) {
          return ctx.fail('404 with no directory listing', `${r.status}${looksLikeIndex ? ' + HTML index' : ''}`, {
            req,
            raw: clip(r.text),
          });
        }
        return ctx.pass({ expected: '404 + no listing', actual: '404 + no listing', detail: { req } });
      },
    },
    {
      name: 'metrics endpoint not on this listener → 404 JSON',
      async run(ctx) {
        const path = '/metrics';
        const req = { method: 'GET', url: `${ctx.api.base}${path}` };
        const r = await ctx.api.raw('GET', path);
        // Best-effort cleanup of the capture buffer this section fills with its
        // protocol/static probes (every such request is recorded); never fails the
        // check. This is the last section, so clearing here leaves the server clean.
        try {
          await ctx.api.requestsClear();
        } catch {
          // ignore — control plane may be gated/absent; nothing to clean up then.
        }
        if (r.status === 200) {
          return ctx.skip(
            'GET /metrics returned 200 — metrics are inlined on this listener in this config. When a dedicated ' +
              'metrics listener is used (default), this listener 404s /metrics.',
          );
        }
        if (r.status !== 404) {
          return ctx.fail('404 (metrics on a separate listener) or 200 (inline)', String(r.status || 'network error'), {
            req,
            raw: clip(r.text),
          });
        }
        const j = r.json;
        const envelope = j && typeof j === 'object';
        if (!envelope) {
          return ctx.fail('404 + JSON envelope', 'non-JSON body', { req, raw: clip(r.text) });
        }
        return ctx.pass({
          expected: '404 + JSON envelope',
          actual: `404 + ${typeof j.error === 'string' ? 'OAuth2 { error }' : 'problem+json { title,status,detail }'}`,
          detail: { req },
        });
      },
    },
  ],
  manual: [
    {
      id: 'cross-origin-cors',
      name: 'Cross-origin CORS (reflect-origin + credentials)',
      instructions:
        'In the config panel, switch Base URL to a DIFFERENT origin that reaches this same server ' +
        '(e.g. http://127.0.0.1:<port> when the page is on http://localhost:<port>), then click the ' +
        'Discovery section Run button. Every discovery check must still PASS: a readable cross-origin ' +
        'response is only possible because the server reflects the Origin (never *) and sets ' +
        'Access-Control-Allow-Credentials: true. Switch Base URL back when done. Use Start to auto-probe ' +
        'the 127.0.0.1/localhost variant of this page origin.',
      async start(ctx) {
        if (typeof location === 'undefined') {
          return { note: 'no window.location — run this from the served console, not file://' };
        }
        const host = location.hostname;
        let altHost = null;
        if (host === 'localhost') altHost = '127.0.0.1';
        else if (host === '127.0.0.1') altHost = 'localhost';
        if (!altHost) {
          return {
            note:
              `page host is '${host}'; cannot auto-derive a same-server different-origin. Manually set Base URL ` +
              'to another origin that reaches this server and run the Discovery section.',
          };
        }
        const port = location.port ? ':' + location.port : '';
        const altOrigin = `${location.protocol}//${altHost}${port}`;
        const url = `${altOrigin}/${ctx.cfg.issuer}/.well-known/openid-configuration`;
        try {
          const res = await fetch(url, { method: 'GET', credentials: 'include' });
          const text = await res.text();
          let json;
          try {
            json = JSON.parse(text);
          } catch {
            json = undefined;
          }
          return {
            crossOriginRequest: url,
            pageOrigin: location.origin,
            status: res.status,
            bodyReadable: !!json,
            issuer: json && json.issuer,
            acaoHeader:
              res.headers.get('Access-Control-Allow-Origin') ||
              '(not JS-exposed on a cross-origin response; a readable credentialled body already proves it)',
            verdict:
              res.status === 200 && json && json.issuer
                ? 'PASS — cross-origin credentialled discovery read succeeded (reflect-origin + credentials CORS work)'
                : 'CHECK — no readable discovery doc came back; inspect status/body above',
          };
        } catch (e) {
          return {
            crossOriginRequest: url,
            error: String((e && e.message) || e),
            note: 'cross-origin fetch failed — reflect-origin/credentials CORS may be broken, or the variant host is unreachable',
          };
        }
      },
    },
  ],
};
