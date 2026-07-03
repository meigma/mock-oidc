// tokenContent.js — Token Content & Callbacks.
//
// Exercises the claim policy the mock stamps onto minted tokens: the default
// registered claims (sub/aud/iss/iat/nbf/exp/jti), tid, azp scoping, the id_token
// [client_id] audience, the 4-step access-token audience chain, nonce passthrough,
// the config-seeded `configured` issuer fixture, at+jwt self-verification, and the
// runtime control-plane surfaces that shape claims (one-shot scenarios,
// requestMapping templating, and typ gating). Follows the discovery.js reference:
// each check is independent, never throws, and returns ctx.pass/fail/skip with a
// meaningful expected/actual plus detail.req (request line) and detail.raw
// (truncated response) on failure paths. See README.md for the full ctx contract.

const SECRET = 'test-secret'; // client secrets are never validated by the mock.

// callbackUri is the redirect target authorizeFollow/authCode default to; reused
// verbatim on the token exchange (redirect_uri is never validated server-side).
function callbackUri(ctx) {
  return `${ctx.api.base}/static/callback.html`;
}

function tokenUrl(ctx, issuer) {
  return `${ctx.api.base}/${issuer}/token`;
}

// reqLine builds the detail.req request line for a form POST to /token.
function reqLine(ctx, issuer, form) {
  return { method: 'POST', url: tokenUrl(ctx, issuer), body: ctx.form.encode(form) };
}

// raw truncates a response body (or any value) to ~500 chars for detail.raw.
function raw(s) {
  if (s == null) return undefined;
  const t = typeof s === 'string' ? s : JSON.stringify(s, null, 2);
  return t.length > 500 ? t.slice(0, 500) + '…' : t;
}

function okStatus(r) {
  return r && r.status >= 200 && r.status < 300;
}

function arrEq(a, b) {
  if (!Array.isArray(a) || !Array.isArray(b) || a.length !== b.length) return false;
  return a.every((v, i) => v === b[i]);
}

// decode wraps ctx.jwt.decode so a malformed token yields null instead of throwing.
function decode(ctx, compact) {
  try {
    return ctx.jwt.decode(compact);
  } catch {
    return null;
  }
}

// ccToken runs a client_credentials grant and returns { r, req }. extra merges
// extra form fields (e.g. scope, or a nonce param to prove it is ignored).
async function ccToken(ctx, issuer, extra = {}) {
  const form = { grant_type: 'client_credentials', client_id: ctx.cfg.clientId, client_secret: SECRET, ...extra };
  const r = await ctx.api.token(issuer, form);
  return { r, req: reqLine(ctx, issuer, form) };
}

// authExchange drives a full authorization_code flow (authCode → /token) and
// returns { r, req } on success, or { err } when the authorize step yielded no
// code (ctx.authCode throws a ctx-fail-able Error carrying .detail).
async function authExchange(ctx, { scope = 'openid profile', nonce } = {}) {
  let cr;
  try {
    cr = await ctx.authCode(ctx.cfg.issuer, { scope, nonce });
  } catch (e) {
    return { err: e };
  }
  const form = {
    grant_type: 'authorization_code',
    code: cr.code,
    redirect_uri: callbackUri(ctx),
    client_id: ctx.cfg.clientId,
    client_secret: SECRET,
  };
  const r = await ctx.api.token(ctx.cfg.issuer, form);
  return { r, req: reqLine(ctx, ctx.cfg.issuer, form) };
}

// exchangeFail turns an authExchange { err } into a ctx.fail using the thrown
// Error's attached detail (finalUrl + parsed authorize params).
function exchangeFail(ctx, err) {
  return ctx.fail('authorization code issued', 'authorize returned no code', err.detail || { note: String(err && err.message) });
}

export default {
  id: 'tokenContent',
  title: 'Token Content & Callbacks',
  checks: [
    {
      name: 'default registered claims present and well-formed (client_credentials)',
      async run(ctx) {
        const { r, req } = await ccToken(ctx, ctx.cfg.issuer);
        if (!okStatus(r) || !r.json || !r.json.access_token) {
          return ctx.fail('200 + access_token', String(r.status || 'network error'), { req, raw: raw(r.text) });
        }
        const dec = decode(ctx, r.json.access_token);
        if (!dec) return ctx.fail('decodable access_token', 'undecodable JWT', { req, raw: raw(r.json.access_token) });
        const p = dec.payload;
        const required = ['sub', 'aud', 'iss', 'iat', 'nbf', 'exp', 'jti'];
        const missing = required.filter((k) => p[k] === undefined || p[k] === null);
        if (missing.length) {
          return ctx.fail('all present: ' + required.join(', '), 'missing: ' + missing.join(', '), { req, raw: raw(p) });
        }
        const wantIss = `${ctx.api.base}/${ctx.cfg.issuer}`;
        if (p.iss !== wantIss) return ctx.fail(`iss === ${wantIss}`, String(p.iss), { req, raw: raw(p) });
        if (p.exp - p.iat !== 3600) {
          return ctx.fail('exp - iat === 3600', String(p.exp - p.iat), { req, raw: raw({ iat: p.iat, exp: p.exp }) });
        }
        if (typeof p.jti !== 'string' || p.jti.length < 32) {
          return ctx.fail('jti uuid-ish (length >= 32)', String(p.jti), { req, raw: raw(p) });
        }
        return ctx.pass({ expected: 'sub,aud,iss,iat,nbf,exp,jti present; iss/exp/jti valid', actual: 'all valid', detail: { req } });
      },
    },
    {
      name: 'tid claim equals the issuer id',
      async run(ctx) {
        const { r, req } = await ccToken(ctx, ctx.cfg.issuer);
        if (!okStatus(r) || !r.json || !r.json.access_token) {
          return ctx.fail('200 + access_token', String(r.status || 'network error'), { req, raw: raw(r.text) });
        }
        const dec = decode(ctx, r.json.access_token);
        if (!dec) return ctx.fail('decodable access_token', 'undecodable JWT', { req, raw: raw(r.json.access_token) });
        if (dec.payload.tid !== ctx.cfg.issuer) {
          return ctx.fail(`tid === ${ctx.cfg.issuer}`, String(dec.payload.tid), { req, raw: raw(dec.payload) });
        }
        return ctx.pass({ expected: `tid === ${ctx.cfg.issuer}`, actual: String(dec.payload.tid), detail: { req } });
      },
    },
    {
      // Server truth (internal/oidc/token.go authorizationCode): azp is stamped ONLY
      // on the authorization_code id_token — the access token carries no azp, and
      // client_credentials carries no azp at all.
      name: 'azp scoping: authorization_code id_token only (never access / client_credentials)',
      async run(ctx) {
        const cc = await ccToken(ctx, ctx.cfg.issuer);
        if (!okStatus(cc.r) || !cc.r.json || !cc.r.json.access_token) {
          return ctx.fail('200 + access_token', String(cc.r.status || 'network error'), { req: cc.req, raw: raw(cc.r.text) });
        }
        const ccDec = decode(ctx, cc.r.json.access_token);
        if (!ccDec) return ctx.fail('decodable access_token', 'undecodable JWT', { req: cc.req, raw: raw(cc.r.json.access_token) });
        if (ccDec.payload.azp !== undefined) {
          return ctx.fail('client_credentials access token has no azp', 'azp=' + String(ccDec.payload.azp), { req: cc.req, raw: raw(ccDec.payload) });
        }

        const ex = await authExchange(ctx, { scope: 'openid profile' });
        if (ex.err) return exchangeFail(ctx, ex.err);
        if (!okStatus(ex.r) || !ex.r.json || !ex.r.json.id_token || !ex.r.json.access_token) {
          return ctx.fail('200 + id_token + access_token', String(ex.r.status || 'network error'), { req: [cc.req, ex.req], raw: raw(ex.r.text) });
        }
        const idDec = decode(ctx, ex.r.json.id_token);
        const acDec = decode(ctx, ex.r.json.access_token);
        if (!idDec || !acDec) return ctx.fail('decodable id + access tokens', 'undecodable JWT', { req: [cc.req, ex.req], raw: raw(ex.r.text) });
        if (idDec.payload.azp !== ctx.cfg.clientId) {
          return ctx.fail(`id_token azp === ${ctx.cfg.clientId}`, String(idDec.payload.azp), { req: [cc.req, ex.req], raw: raw(idDec.payload) });
        }
        if (acDec.payload.azp !== undefined) {
          return ctx.fail('authorization_code access token has no azp', 'azp=' + String(acDec.payload.azp), { req: [cc.req, ex.req], raw: raw(acDec.payload) });
        }
        return ctx.pass({
          expected: `id_token azp === ${ctx.cfg.clientId}; no azp on either access token`,
          actual: 'id_token azp set; access tokens carry none',
          detail: { req: [cc.req, ex.req] },
        });
      },
    },
    {
      name: 'id_token aud is exactly [client_id]',
      async run(ctx) {
        const ex = await authExchange(ctx, { scope: 'openid profile' });
        if (ex.err) return exchangeFail(ctx, ex.err);
        if (!okStatus(ex.r) || !ex.r.json || !ex.r.json.id_token) {
          return ctx.fail('200 + id_token', String(ex.r.status || 'network error'), { req: ex.req, raw: raw(ex.r.text) });
        }
        const dec = decode(ctx, ex.r.json.id_token);
        if (!dec) return ctx.fail('decodable id_token', 'undecodable JWT', { req: ex.req, raw: raw(ex.r.json.id_token) });
        const aud = dec.payload.aud;
        if (!arrEq(aud, [ctx.cfg.clientId])) {
          return ctx.fail(`[${ctx.cfg.clientId}]`, JSON.stringify(aud), { req: ex.req, raw: raw(dec.payload) });
        }
        return ctx.pass({ expected: `aud === [${ctx.cfg.clientId}]`, actual: JSON.stringify(aud), detail: { req: ex.req } });
      },
    },
    {
      name: 'access_token aud default chain (scope-derived vs ["default"])',
      async run(ctx) {
        const a = await ccToken(ctx, ctx.cfg.issuer, { scope: 'openid profile' });
        if (!okStatus(a.r) || !a.r.json || !a.r.json.access_token) {
          return ctx.fail('200 + access_token', String(a.r.status || 'network error'), { req: a.req, raw: raw(a.r.text) });
        }
        const aDec = decode(ctx, a.r.json.access_token);
        if (!aDec) return ctx.fail('decodable access_token', 'undecodable JWT', { req: a.req, raw: raw(a.r.json.access_token) });
        if (!arrEq(aDec.payload.aud, ['default'])) {
          return ctx.fail('scope "openid profile" -> aud ["default"]', JSON.stringify(aDec.payload.aud), { req: a.req, raw: raw(aDec.payload) });
        }

        const b = await ccToken(ctx, ctx.cfg.issuer, { scope: 'openid custom-api' });
        if (!okStatus(b.r) || !b.r.json || !b.r.json.access_token) {
          return ctx.fail('200 + access_token', String(b.r.status || 'network error'), { req: b.req, raw: raw(b.r.text) });
        }
        const bDec = decode(ctx, b.r.json.access_token);
        if (!bDec) return ctx.fail('decodable access_token', 'undecodable JWT', { req: b.req, raw: raw(b.r.json.access_token) });
        const bAud = bDec.payload.aud;
        if (!Array.isArray(bAud) || !bAud.includes('custom-api') || bAud.includes('openid')) {
          return ctx.fail('scope "openid custom-api" -> aud contains "custom-api", not "openid"', JSON.stringify(bAud), { req: [a.req, b.req], raw: raw(bDec.payload) });
        }
        return ctx.pass({
          expected: '["default"] for OIDC-only scopes; non-OIDC scope carried into aud',
          actual: `["default"] and ${JSON.stringify(bAud)}`,
          detail: { req: [a.req, b.req] },
        });
      },
    },
    {
      name: 'nonce passthrough on authorization_code, never injectable via /token',
      async run(ctx) {
        const nonce = ctx.newState();
        const ex = await authExchange(ctx, { scope: 'openid profile', nonce });
        if (ex.err) return exchangeFail(ctx, ex.err);
        if (!okStatus(ex.r) || !ex.r.json || !ex.r.json.id_token) {
          return ctx.fail('200 + id_token', String(ex.r.status || 'network error'), { req: ex.req, raw: raw(ex.r.text) });
        }
        const idDec = decode(ctx, ex.r.json.id_token);
        if (!idDec) return ctx.fail('decodable id_token', 'undecodable JWT', { req: ex.req, raw: raw(ex.r.json.id_token) });
        if (idDec.payload.nonce !== nonce) {
          return ctx.fail(`id_token nonce === ${nonce}`, String(idDec.payload.nonce), { req: ex.req, raw: raw(idDec.payload) });
        }

        // A nonce form param on a token request must never be stamped onto the token.
        const cc = await ccToken(ctx, ctx.cfg.issuer, { nonce: 'injected-should-be-ignored' });
        if (!okStatus(cc.r) || !cc.r.json || !cc.r.json.access_token) {
          return ctx.fail('200 + access_token', String(cc.r.status || 'network error'), { req: cc.req, raw: raw(cc.r.text) });
        }
        const ccDec = decode(ctx, cc.r.json.access_token);
        if (!ccDec) return ctx.fail('decodable access_token', 'undecodable JWT', { req: cc.req, raw: raw(cc.r.json.access_token) });
        if (ccDec.payload.nonce !== undefined) {
          return ctx.fail('client_credentials token carries no nonce', 'nonce=' + String(ccDec.payload.nonce), { req: [ex.req, cc.req], raw: raw(ccDec.payload) });
        }
        return ctx.pass({
          expected: 'cached nonce stamped into id_token; token-request nonce param ignored',
          actual: 'id_token nonce matches; client_credentials nonce absent',
          detail: { req: [ex.req, cc.req] },
        });
      },
    },
    {
      name: 'configured issuer fixture claims + at+jwt header',
      async run(ctx) {
        const { r, req } = await ccToken(ctx, ctx.cfg.configuredIssuer);
        if (!okStatus(r) || !r.json || !r.json.access_token) {
          return ctx.fail('200 + access_token', String(r.status || 'network error'), { req, raw: raw(r.text) });
        }
        const dec = decode(ctx, r.json.access_token);
        if (!dec) return ctx.fail('decodable access_token', 'undecodable JWT', { req, raw: raw(r.json.access_token) });
        const p = dec.payload;
        const problems = [];
        if (p.config_marker !== 'from-config') problems.push(`config_marker=${JSON.stringify(p.config_marker)}`);
        if (p.sub !== 'config-seeded-subject') problems.push(`sub=${JSON.stringify(p.sub)}`);
        if (!arrEq(p.aud, ['config-audience'])) problems.push(`aud=${JSON.stringify(p.aud)}`);
        if (dec.header.typ !== 'at+jwt') problems.push(`typ=${JSON.stringify(dec.header.typ)}`);
        if (problems.length) {
          return ctx.fail(
            'config_marker=from-config, sub=config-seeded-subject, aud=[config-audience], typ=at+jwt',
            problems.join(', '),
            { req, raw: raw({ header: dec.header, payload: p }) },
          );
        }
        return ctx.pass({ expected: 'config-seeded claims + at+jwt typ', actual: 'all match', detail: { req } });
      },
    },
    {
      name: 'at+jwt token self-verifies (jwks + userinfo + introspect)',
      async run(ctx) {
        const { r, req } = await ccToken(ctx, ctx.cfg.configuredIssuer);
        if (!okStatus(r) || !r.json || !r.json.access_token) {
          return ctx.fail('200 + access_token', String(r.status || 'network error'), { req, raw: raw(r.text) });
        }
        const token = r.json.access_token;

        const jwksRes = await ctx.api.jwks(ctx.cfg.configuredIssuer);
        const jwksReq = { method: 'GET', url: `${ctx.api.base}/${ctx.cfg.configuredIssuer}/jwks` };
        if (!jwksRes.json) return ctx.fail('jwks JSON', String(jwksRes.status || 'network error'), { req: jwksReq, raw: raw(jwksRes.text) });
        const verified = await ctx.jwt.verify(token, jwksRes.json);
        if (!verified) {
          return ctx.fail('signature verifies against configured jwks', 'verify=false', { req: [req, jwksReq], raw: raw(token) });
        }

        const ui = await ctx.api.userinfo(ctx.cfg.configuredIssuer, token);
        const uiReq = { method: 'GET', url: `${ctx.api.base}/${ctx.cfg.configuredIssuer}/userinfo`, note: 'Authorization: Bearer <token>' };
        if (ui.status !== 200) return ctx.fail('userinfo 200', String(ui.status || 'network error'), { req: uiReq, raw: raw(ui.text) });

        const intro = await ctx.api.introspect(ctx.cfg.configuredIssuer, token);
        const introReq = { method: 'POST', url: `${ctx.api.base}/${ctx.cfg.configuredIssuer}/introspect` };
        if (!intro.json || intro.json.active !== true) {
          return ctx.fail('introspect active:true', intro.json ? 'active=' + String(intro.json.active) : String(intro.status), { req: introReq, raw: raw(intro.text) });
        }
        return ctx.pass({ expected: 'verify + userinfo 200 + introspect active:true', actual: 'all pass', detail: { req: [req, jwksReq, uiReq, introReq] } });
      },
    },
    {
      name: 'scenario override is one-shot and reverts to default',
      async run(ctx) {
        await ctx.api.scenarioClear(); // isolate from any prior/leftover scenario.
        const enq = await ctx.api.scenarioEnqueue({ issuer: ctx.cfg.issuer, claims: { scenario_marker: 'once' }, expirySeconds: 1200 });
        const enqReq = { method: 'POST', url: `${ctx.api.base}/_mock/scenarios`, body: JSON.stringify({ issuer: ctx.cfg.issuer, claims: { scenario_marker: 'once' }, expirySeconds: 1200 }) };
        if (!okStatus(enq)) return ctx.fail('scenario enqueued (2xx)', String(enq.status || 'network error'), { req: enqReq, raw: raw(enq.text) });

        const first = await ccToken(ctx, ctx.cfg.issuer);
        if (!okStatus(first.r) || !first.r.json || !first.r.json.access_token) {
          return ctx.fail('200 + access_token', String(first.r.status || 'network error'), { req: first.req, raw: raw(first.r.text) });
        }
        const firstDec = decode(ctx, first.r.json.access_token);
        if (!firstDec) return ctx.fail('decodable access_token', 'undecodable JWT', { req: first.req, raw: raw(first.r.json.access_token) });
        if (firstDec.payload.scenario_marker !== 'once') {
          return ctx.fail('scenario token has scenario_marker=once', String(firstDec.payload.scenario_marker), { req: [enqReq, first.req], raw: raw(firstDec.payload) });
        }
        if (firstDec.payload.exp - firstDec.payload.iat !== 1200) {
          return ctx.fail('scenario token exp - iat === 1200', String(firstDec.payload.exp - firstDec.payload.iat), { req: [enqReq, first.req], raw: raw({ iat: firstDec.payload.iat, exp: firstDec.payload.exp }) });
        }

        const second = await ccToken(ctx, ctx.cfg.issuer);
        if (!okStatus(second.r) || !second.r.json || !second.r.json.access_token) {
          return ctx.fail('200 + access_token', String(second.r.status || 'network error'), { req: second.req, raw: raw(second.r.text) });
        }
        const secondDec = decode(ctx, second.r.json.access_token);
        if (!secondDec) return ctx.fail('decodable access_token', 'undecodable JWT', { req: second.req, raw: raw(second.r.json.access_token) });
        if (secondDec.payload.scenario_marker !== undefined) {
          return ctx.fail('next token reverts (no scenario_marker)', 'scenario_marker=' + String(secondDec.payload.scenario_marker), { req: second.req, raw: raw(secondDec.payload) });
        }
        return ctx.pass({
          expected: 'first token overridden (marker + 1200s exp), second reverted to default',
          actual: 'override applied once then reverted',
          detail: { req: [enqReq, first.req, second.req] },
        });
      },
    },
    {
      name: 'requestMapping templates ${username} into a claim (password grant)',
      async run(ctx) {
        await ctx.api.scenarioClear(); // isolate from any prior/leftover scenario.
        const scenario = { issuer: ctx.cfg.issuer, requestMappings: [{ param: 'username', match: '*', claims: { mapped_user: '${username}' } }] };
        const enq = await ctx.api.scenarioEnqueue(scenario);
        const enqReq = { method: 'POST', url: `${ctx.api.base}/_mock/scenarios`, body: JSON.stringify(scenario) };
        if (!okStatus(enq)) return ctx.fail('scenario enqueued (2xx)', String(enq.status || 'network error'), { req: enqReq, raw: raw(enq.text) });

        const form = { grant_type: 'password', username: 'carol', password: 'ignored', client_id: ctx.cfg.clientId, client_secret: SECRET, scope: 'openid profile' };
        const r = await ctx.api.token(ctx.cfg.issuer, form);
        const req = reqLine(ctx, ctx.cfg.issuer, form);
        if (!okStatus(r) || !r.json || !r.json.access_token) {
          await ctx.api.scenarioClear(); // best-effort cleanup if the grant did not consume it.
          return ctx.fail('200 + access_token', String(r.status || 'network error'), { req: [enqReq, req], raw: raw(r.text) });
        }
        const dec = decode(ctx, r.json.access_token);
        if (!dec) return ctx.fail('decodable access_token', 'undecodable JWT', { req, raw: raw(r.json.access_token) });
        if (dec.payload.mapped_user !== 'carol') {
          return ctx.fail("mapped_user === 'carol'", String(dec.payload.mapped_user), { req: [enqReq, req], raw: raw(dec.payload) });
        }
        return ctx.pass({ expected: "mapped_user === 'carol' (templated from username)", actual: String(dec.payload.mapped_user), detail: { req: [enqReq, req] } });
      },
    },
    {
      name: 'foreign typ token is rejected by userinfo and introspect',
      async run(ctx) {
        const mint = await ctx.api.mint({ issuer: ctx.cfg.issuer, typ: 'foo+jwt', subject: 's' });
        const mintReq = { method: 'POST', url: `${ctx.api.base}/_mock/mint`, body: JSON.stringify({ issuer: ctx.cfg.issuer, typ: 'foo+jwt', subject: 's' }) };
        if (!okStatus(mint) || !mint.json || !mint.json.token) {
          return ctx.fail('mint 200 + token', String(mint.status || 'network error'), { req: mintReq, raw: raw(mint.text) });
        }
        const token = mint.json.token;

        const ui = await ctx.api.userinfo(ctx.cfg.issuer, token);
        const uiReq = { method: 'GET', url: `${ctx.api.base}/${ctx.cfg.issuer}/userinfo`, note: 'Authorization: Bearer <foo+jwt token>' };
        if (ui.status !== 401) return ctx.fail('userinfo 401', String(ui.status || 'network error'), { req: [mintReq, uiReq], raw: raw(ui.text) });

        const intro = await ctx.api.introspect(ctx.cfg.issuer, token);
        const introReq = { method: 'POST', url: `${ctx.api.base}/${ctx.cfg.issuer}/introspect` };
        if (!intro.json || intro.json.active !== false) {
          return ctx.fail('introspect active:false', intro.json ? 'active=' + String(intro.json.active) : String(intro.status), { req: [mintReq, introReq], raw: raw(intro.text) });
        }
        return ctx.pass({ expected: 'userinfo 401 + introspect active:false', actual: 'both reject the foreign typ', detail: { req: [mintReq, uiReq, introReq] } });
      },
    },
  ],
};
