// lifecycle.js — Token Lifecycle section.
//
// Exercises the post-issuance surface: /userinfo, /introspect, /revoke, and
// RP-initiated logout (/endsession). Follows the discovery.js reference pattern —
// every check returns ctx.pass / ctx.fail / ctx.skip with a useful expected/actual,
// a request line in detail.req, and truncated raw response text in detail.raw on
// failure paths. Checks are independent and use ctx.cfg.issuer; the destructive
// revoke flow runs on a throwaway `<issuer>-revoke` id (issuers materialize on
// demand) so the working issuer stays clean. No scenarios, captures, or clock
// state are created, so no teardown is needed.

// trunc caps raw response text at ~500 chars for the detail panel.
function trunc(s, n = 500) {
  if (typeof s !== 'string') return s;
  return s.length <= n ? s : s.slice(0, n) + ' …';
}

// reqLine builds the { method, url } request line (plus an optional body summary)
// carried in detail.req, mirroring discovery.js.
function reqLine(ctx, method, path, body) {
  const req = { method, url: `${ctx.api.base}${path}` };
  if (body !== undefined) req.body = body;
  return req;
}

// paramFromUrl reads a single query param off a followed-redirect URL. Used where
// the raw escape hatch (api.raw) returns finalUrl without a parsed params map.
function paramFromUrl(u, key) {
  try {
    return new URL(u).searchParams.get(key);
  } catch {
    return null;
  }
}

// mintAccessToken mints a signed access token through the control plane and
// returns { r, req, token } so callers can fail with the raw mint body.
async function mintAccessToken(ctx, body) {
  const r = await ctx.api.mint(body);
  const req = reqLine(ctx, 'POST', '/_mock/mint', body);
  return { r, req, token: r.json && r.json.token };
}

export default {
  id: 'lifecycle',
  title: 'Token Lifecycle',
  checks: [
    {
      name: 'userinfo returns the full claim set for a minted access token',
      async run(ctx) {
        const issuer = ctx.cfg.issuer;
        const m = await mintAccessToken(ctx, { issuer, subject: 'u1', claims: { email: 'u1@x' } });
        if (m.r.status !== 200 || !m.token) {
          return ctx.fail('mint 200 with token', `mint ${m.r.status || 'network error'}`, { req: m.req, raw: trunc(m.r.text) });
        }
        const r = await ctx.api.userinfo(issuer, m.token);
        const req = reqLine(ctx, 'GET', `/${issuer}/userinfo`, 'Authorization: Bearer <minted>');
        if (r.status !== 200) return ctx.fail('200', String(r.status || 'network error'), { req, raw: trunc(r.text) });
        if (!r.json || typeof r.json !== 'object') return ctx.fail('JSON body', 'unparseable body', { req, raw: trunc(r.text) });
        if (r.json.sub !== 'u1' || r.json.email !== 'u1@x') {
          return ctx.fail('sub=u1, email=u1@x', `sub=${r.json.sub}, email=${r.json.email}`, { req, raw: trunc(r.text) });
        }
        return ctx.pass({ expected: '200 sub=u1 email=u1@x', actual: `200 sub=${r.json.sub} email=${r.json.email}`, detail: { req, raw: trunc(r.text) } });
      },
    },
    {
      name: 'userinfo with a garbage bearer → 401 invalid_token + WWW-Authenticate',
      async run(ctx) {
        const issuer = ctx.cfg.issuer;
        const r = await ctx.api.userinfo(issuer, 'not-a-real-token');
        const req = reqLine(ctx, 'GET', `/${issuer}/userinfo`, 'Authorization: Bearer not-a-real-token');
        if (r.status !== 401) return ctx.fail('401', String(r.status || 'network error'), { req, raw: trunc(r.text) });
        const wa = r.headers.get('WWW-Authenticate');
        if (!wa || !wa.includes('error="invalid_token"')) {
          return ctx.fail('WWW-Authenticate contains error="invalid_token"', String(wa), { req, raw: trunc(r.text) });
        }
        if (!r.json || r.json.error !== 'invalid_token') {
          return ctx.fail('body error=invalid_token', r.json ? String(r.json.error) : 'no JSON body', { req, raw: trunc(r.text) });
        }
        return ctx.pass({ expected: '401 invalid_token + WWW-Authenticate', actual: `401 ${r.json.error}; ${wa}`, detail: { req, raw: trunc(r.text) } });
      },
    },
    {
      name: 'userinfo with no Authorization header → 401',
      async run(ctx) {
        const issuer = ctx.cfg.issuer;
        const r = await ctx.api.userinfo(issuer, '');
        const req = reqLine(ctx, 'GET', `/${issuer}/userinfo`, '(no Authorization header)');
        if (r.status !== 401) return ctx.fail('401', String(r.status || 'network error'), { req, raw: trunc(r.text) });
        return ctx.pass({ expected: '401', actual: `401 (${r.json && r.json.error})`, detail: { req, raw: trunc(r.text) } });
      },
    },
    {
      name: 'introspect a valid access token → active:true with typed claims',
      async run(ctx) {
        const issuer = ctx.cfg.issuer;
        // A single-element audience so the introspection edge collapses aud to a
        // scalar string (RFC 7662). A minted access token with no audience omits
        // aud entirely, which would make the aud-as-string assertion meaningless.
        const m = await mintAccessToken(ctx, { issuer, subject: 'introspect-subject', audience: ['api-one'] });
        if (m.r.status !== 200 || !m.token) {
          return ctx.fail('mint 200 with token', `mint ${m.r.status || 'network error'}`, { req: m.req, raw: trunc(m.r.text) });
        }
        const r = await ctx.api.introspect(issuer, m.token);
        const req = reqLine(ctx, 'POST', `/${issuer}/introspect`, { token: '<minted>' });
        if (r.status !== 200) return ctx.fail('200', String(r.status || 'network error'), { req, raw: trunc(r.text) });
        const b = r.json || {};
        const problems = [];
        if (b.active !== true) problems.push(`active=${b.active}`);
        if (b.token_type !== 'Bearer') problems.push(`token_type=${b.token_type}`);
        if (typeof b.sub !== 'string' || !b.sub) problems.push(`sub=${b.sub}`);
        if (typeof b.iss !== 'string' || !b.iss) problems.push(`iss=${b.iss}`);
        if (typeof b.exp !== 'number') problems.push(`exp not numeric (${typeof b.exp})`);
        if (typeof b.iat !== 'number') problems.push(`iat not numeric (${typeof b.iat})`);
        if (typeof b.aud !== 'string') problems.push(`aud not a string (${typeof b.aud}: ${JSON.stringify(b.aud)})`);
        if (problems.length) {
          return ctx.fail('active:true; token_type=Bearer; sub/iss strings; exp/iat numeric; aud scalar string', problems.join('; '), { req, raw: trunc(r.text) });
        }
        return ctx.pass({ expected: 'active:true; Bearer; sub/iss; numeric exp/iat; aud string', actual: `active:true; sub=${b.sub}; aud="${b.aud}" (string)`, detail: { req, raw: trunc(r.text) } });
      },
    },
    {
      name: 'introspect a garbage token → 200 {active:false}',
      async run(ctx) {
        const issuer = ctx.cfg.issuer;
        const r = await ctx.api.introspect(issuer, 'garbage-token');
        const req = reqLine(ctx, 'POST', `/${issuer}/introspect`, { token: 'garbage-token' });
        if (r.status !== 200) return ctx.fail('200', String(r.status || 'network error'), { req, raw: trunc(r.text) });
        if (!r.json || r.json.active !== false) return ctx.fail('{active:false}', JSON.stringify(r.json), { req, raw: trunc(r.text) });
        return ctx.pass({ expected: '200 {active:false}', actual: '200 {active:false}', detail: { req, raw: trunc(r.text) } });
      },
    },
    {
      name: 'introspect with no token param → 200 {active:false}',
      async run(ctx) {
        const issuer = ctx.cfg.issuer;
        // No `token` field, but a NON-EMPTY body: the framework rejects a wholly
        // empty request body with 400 "request body is required" before the handler
        // runs, whereas the introspection handler itself treats an absent token as
        // {active:false}. api.introspect(issuer, undefined) would drop the token and
        // send an empty body, so drive api.raw with token_type_hint alone (still no
        // token param) plus presence-only client auth.
        const body = ctx.form.encode({ token_type_hint: 'access_token' });
        const r = await ctx.api.raw('POST', `/${issuer}/introspect`, {
          headers: { 'Content-Type': 'application/x-www-form-urlencoded', Authorization: 'Bearer x' },
          body,
        });
        const req = reqLine(ctx, 'POST', `/${issuer}/introspect`, 'token_type_hint=access_token (no token param)');
        if (r.status !== 200) return ctx.fail('200', String(r.status || 'network error'), { req, raw: trunc(r.text) });
        if (!r.json || r.json.active !== false) return ctx.fail('{active:false}', JSON.stringify(r.json), { req, raw: trunc(r.text) });
        return ctx.pass({ expected: '200 {active:false}', actual: '200 {active:false}', detail: { req, raw: trunc(r.text) } });
      },
    },
    {
      name: 'introspect with no Authorization header → 400 invalid_client',
      async run(ctx) {
        const issuer = ctx.cfg.issuer;
        // api.introspect always attaches presence-only auth, so drop to api.raw to
        // omit the Authorization header entirely.
        const body = ctx.form.encode({ token: 'anything' });
        const r = await ctx.api.raw('POST', `/${issuer}/introspect`, {
          headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
          body,
        });
        const req = reqLine(ctx, 'POST', `/${issuer}/introspect`, 'token=anything (no Authorization)');
        if (r.status !== 400) return ctx.fail('400', String(r.status || 'network error'), { req, raw: trunc(r.text) });
        if (!r.json || r.json.error !== 'invalid_client') {
          return ctx.fail('error=invalid_client', r.json ? String(r.json.error) : 'no JSON body', { req, raw: trunc(r.text) });
        }
        return ctx.pass({ expected: '400 invalid_client', actual: `400 ${r.json.error}`, detail: { req, raw: trunc(r.text) } });
      },
    },
    {
      name: 'revoke a refresh token then re-redeem it → 400 invalid_grant',
      async run(ctx) {
        // Isolate the destructive redeem on a throwaway issuer (materializes on demand).
        const issuer = `${ctx.cfg.issuer}-revoke`;
        let code;
        try {
          const ac = await ctx.authCode(issuer, { scope: 'openid profile' });
          code = ac.code;
        } catch (e) {
          return ctx.fail('authorization code from /authorize', 'no code returned', e.detail || { note: String((e && e.message) || e) });
        }
        const redirectUri = `${ctx.api.base}/static/callback.html`;
        const ex = await ctx.api.token(issuer, { grant_type: 'authorization_code', code, redirect_uri: redirectUri, client_id: ctx.cfg.clientId });
        const exReq = reqLine(ctx, 'POST', `/${issuer}/token`, { grant_type: 'authorization_code', code: '<code>' });
        if (ex.status !== 200 || !ex.json || !ex.json.refresh_token) {
          return ctx.fail('200 with refresh_token', `${ex.status} refresh=${ex.json && ex.json.refresh_token}`, { req: exReq, raw: trunc(ex.text) });
        }
        const refresh = ex.json.refresh_token;
        const rv = await ctx.api.revoke(issuer, refresh, { hint: 'refresh_token' });
        const rvReq = reqLine(ctx, 'POST', `/${issuer}/revoke`, { token: '<refresh>', token_type_hint: 'refresh_token' });
        if (rv.status !== 200) return ctx.fail('revoke → 200', String(rv.status || 'network error'), { req: rvReq, raw: trunc(rv.text) });
        const re = await ctx.api.token(issuer, { grant_type: 'refresh_token', refresh_token: refresh, client_id: ctx.cfg.clientId });
        const reReq = reqLine(ctx, 'POST', `/${issuer}/token`, { grant_type: 'refresh_token', refresh_token: '<refresh>' });
        if (re.status !== 400 || !re.json || re.json.error !== 'invalid_grant') {
          return ctx.fail('redeem revoked → 400 invalid_grant', `${re.status} ${re.json && re.json.error}`, { req: reReq, raw: trunc(re.text) });
        }
        return ctx.pass({ expected: 'revoke 200, then redeem 400 invalid_grant', actual: `revoke ${rv.status}; redeem ${re.status} ${re.json.error}`, detail: { req: [exReq, rvReq, reReq] } });
      },
    },
    {
      name: 'revoke with token_type_hint=access_token → 400 unsupported_token_type',
      async run(ctx) {
        const issuer = ctx.cfg.issuer;
        const r = await ctx.api.revoke(issuer, 'some-token', { hint: 'access_token' });
        const req = reqLine(ctx, 'POST', `/${issuer}/revoke`, { token: 'some-token', token_type_hint: 'access_token' });
        if (r.status !== 400) return ctx.fail('400', String(r.status || 'network error'), { req, raw: trunc(r.text) });
        if (!r.json || r.json.error !== 'unsupported_token_type') {
          return ctx.fail('error=unsupported_token_type', r.json ? String(r.json.error) : 'no JSON body', { req, raw: trunc(r.text) });
        }
        return ctx.pass({ expected: '400 unsupported_token_type', actual: `400 ${r.json.error}`, detail: { req, raw: trunc(r.text) } });
      },
    },
    {
      name: 'revoke an unknown refresh token (correct hint) → 200 idempotent no-op',
      async run(ctx) {
        const issuer = ctx.cfg.issuer;
        const r = await ctx.api.revoke(issuer, `unknown-${ctx.newState()}`, { hint: 'refresh_token' });
        const req = reqLine(ctx, 'POST', `/${issuer}/revoke`, { token: '<unknown>', token_type_hint: 'refresh_token' });
        if (r.status !== 200) return ctx.fail('200', String(r.status || 'network error'), { req, raw: trunc(r.text) });
        return ctx.pass({ expected: '200 (idempotent no-op)', actual: '200', detail: { req, raw: trunc(r.text) } });
      },
    },
    {
      name: 'endsession redirects to post_logout_redirect_uri carrying state',
      async run(ctx) {
        const issuer = ctx.cfg.issuer;
        const cb = `${ctx.api.base}/static/callback.html`;
        const r = await ctx.api.endsessionFollow(issuer, { post_logout_redirect_uri: cb, state: 'bye' });
        const req = reqLine(ctx, 'GET', `/${issuer}/endsession?post_logout_redirect_uri=${encodeURIComponent(cb)}&state=bye`);
        const onCallback = typeof r.finalUrl === 'string' && r.finalUrl.includes('/static/callback.html');
        if (!onCallback || !r.params || r.params.state !== 'bye') {
          return ctx.fail('finalUrl on callback.html with state=bye', `finalUrl=${r.finalUrl}; state=${r.params && r.params.state}`, { req, raw: trunc(r.text) });
        }
        return ctx.pass({ expected: 'callback.html?state=bye', actual: r.finalUrl, detail: { req } });
      },
    },
    {
      name: 'endsession redirect without state lands on callback with no state param',
      async run(ctx) {
        const issuer = ctx.cfg.issuer;
        const cb = `${ctx.api.base}/static/callback.html`;
        const r = await ctx.api.endsessionFollow(issuer, { post_logout_redirect_uri: cb });
        const req = reqLine(ctx, 'GET', `/${issuer}/endsession?post_logout_redirect_uri=${encodeURIComponent(cb)}`);
        const onCallback = typeof r.finalUrl === 'string' && r.finalUrl.includes('/static/callback.html');
        const hasState = r.params && Object.prototype.hasOwnProperty.call(r.params, 'state');
        if (!onCallback || hasState) {
          return ctx.fail('callback.html with no state param', `finalUrl=${r.finalUrl}; state=${r.params && r.params.state}`, { req, raw: trunc(r.text) });
        }
        return ctx.pass({ expected: 'callback.html, no state', actual: r.finalUrl, detail: { req } });
      },
    },
    {
      name: 'endsession without a redirect URI → 200 "logged out" HTML',
      async run(ctx) {
        const issuer = ctx.cfg.issuer;
        const r = await ctx.api.endsessionFollow(issuer, {});
        const req = reqLine(ctx, 'GET', `/${issuer}/endsession`);
        if (r.status !== 200) return ctx.fail('200', String(r.status || 'network error'), { req, raw: trunc(r.text) });
        if (!/logged out/i.test(r.text || '')) {
          return ctx.fail('HTML containing "logged out"', trunc(r.text) || '(empty body)', { req, raw: trunc(r.text) });
        }
        return ctx.pass({ expected: '200 HTML containing "logged out"', actual: '200 logged-out page', detail: { req, raw: trunc(r.text) } });
      },
    },
    {
      name: 'POST endsession honors query params, ignoring a conflicting body',
      async run(ctx) {
        const issuer = ctx.cfg.issuer;
        const cb = `${ctx.api.base}/static/callback.html`;
        const decoy = `${ctx.api.base}/static/decoy.html`;
        const path = `/${issuer}/endsession?post_logout_redirect_uri=${encodeURIComponent(cb)}&state=q`;
        // Conflicting body values the server must ignore (endsession reads QUERY only).
        // api.raw follows redirects (fetch default), so finalUrl is the landing URL.
        const body = ctx.form.encode({ post_logout_redirect_uri: decoy, state: 'body-state' });
        const r = await ctx.api.raw('POST', path, { headers: { 'Content-Type': 'application/x-www-form-urlencoded' }, body });
        const req = reqLine(ctx, 'POST', path, 'body: post_logout_redirect_uri=<decoy>&state=body-state');
        const onCallback = typeof r.finalUrl === 'string' && r.finalUrl.includes('/static/callback.html');
        const state = paramFromUrl(r.finalUrl, 'state');
        if (!onCallback || state !== 'q') {
          return ctx.fail('redirect to query callback.html?state=q (body ignored)', `finalUrl=${r.finalUrl}; state=${state}`, { req, raw: trunc(r.text) });
        }
        return ctx.pass({ expected: 'query wins: callback.html?state=q', actual: r.finalUrl, detail: { req } });
      },
    },
  ],
};
