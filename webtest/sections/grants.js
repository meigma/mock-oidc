// grants.js — Token Grants (all six).
//
// Exercises every /token grant_type the mock server dispatches:
// authorization_code (+ PKCE), client_credentials, password, refresh_token,
// jwt-bearer, and token-exchange (incl. private_key_jwt client auth). Follows the
// discovery.js reference conventions: each check independently drives the server
// and returns ctx.pass / ctx.fail / ctx.skip with detail.req (the request line)
// and detail.raw (truncated raw body) on failure paths. See README.md.

const GT_CODE = 'authorization_code';
const GT_CC = 'client_credentials';
const GT_PW = 'password';
const GT_REFRESH = 'refresh_token';
const GT_JWT_BEARER = 'urn:ietf:params:oauth:grant-type:jwt-bearer';
const GT_EXCHANGE = 'urn:ietf:params:oauth:grant-type:token-exchange';
const CLIENT_ASSERTION_TYPE = 'urn:ietf:params:oauth:client-assertion-type:jwt-bearer';
const TT_JWT = 'urn:ietf:params:oauth:token-type:jwt';
const TT_ACCESS = 'urn:ietf:params:oauth:token-type:access_token';

// clip truncates a raw response body for the detail pane (~500 chars).
function clip(text, n = 500) {
  const s = typeof text === 'string' ? text : String(text ?? '');
  return s.length <= n ? s : s.slice(0, n) + ' …';
}

// summarizeForm renders a form body for the request line, shortening long values
// (JWT assertions/subject_tokens) so the detail stays readable.
function summarizeForm(form) {
  const out = {};
  for (const [k, v] of Object.entries(form)) {
    if (v === undefined || v === null) continue;
    const s = String(v);
    out[k] = s.length > 60 ? s.slice(0, 60) + `…(+${s.length - 60})` : s;
  }
  return out;
}

// tokenReq builds the request-line descriptor surfaced in detail.req.
function tokenReq(ctx, issuer, form, basicAuth) {
  const req = { method: 'POST', url: `${ctx.api.base}/${issuer}/token`, form: summarizeForm(form) };
  if (basicAuth) req.authorization = `Basic (user=${basicAuth.user})`;
  return req;
}

// postToken POSTs a form-encoded token request and returns { r, req } so every
// check can re-run alone (mirrors discovery.js getDoc).
async function postToken(ctx, issuer, form, opts = {}) {
  const r = await ctx.api.token(issuer, form, opts);
  return { r, req: tokenReq(ctx, issuer, form, opts.basicAuth) };
}

// decodeSafe decodes a compact JWT without throwing (returns null on garbage).
function decodeSafe(ctx, compact) {
  try {
    return ctx.jwt.decode(compact);
  } catch {
    return null;
  }
}

// freshCode drives a full authorize→code follow, converting the throw from
// ctx.authCode (no code returned) into a fail-able shape.
async function freshCode(ctx, issuer, opts = {}) {
  try {
    const got = await ctx.authCode(issuer, opts);
    return { ok: true, ...got };
  } catch (e) {
    return { ok: false, error: e };
  }
}

// audList normalizes an `aud` claim (string or array) to an array for contains
// checks.
function audList(aud) {
  if (Array.isArray(aud)) return aud;
  if (aud === undefined || aud === null) return [];
  return [aud];
}

export default {
  id: 'grants',
  title: 'Token Grants (all six)',
  checks: [
    {
      name: 'client_credentials → access-only, sub == client_id',
      async run(ctx) {
        const issuer = ctx.cfg.issuer;
        const form = { grant_type: GT_CC, client_id: ctx.cfg.clientId, scope: 'openid' };
        const { r, req } = await postToken(ctx, issuer, form);
        if (r.status !== 200) return ctx.fail('200', String(r.status || 'network error'), { req, raw: clip(r.text) });
        const j = r.json || {};
        if (!j.access_token) return ctx.fail('access_token present', 'absent', { req, raw: clip(r.text) });
        if (j.id_token || j.refresh_token) {
          return ctx.fail('no id_token / no refresh_token', `id_token:${!!j.id_token} refresh_token:${!!j.refresh_token}`, { req, raw: clip(r.text) });
        }
        if (j.token_type !== 'Bearer') return ctx.fail("token_type 'Bearer'", String(j.token_type), { req, raw: clip(r.text) });
        const dec = decodeSafe(ctx, j.access_token);
        if (!dec) return ctx.fail('decodable access_token', 'undecodable', { req, raw: clip(j.access_token) });
        if (dec.payload.sub !== ctx.cfg.clientId) return ctx.fail(`sub == ${ctx.cfg.clientId}`, String(dec.payload.sub), { req });
        return ctx.pass({ expected: '200 access-only, sub==clientId', actual: `200 sub=${dec.payload.sub}`, detail: { req } });
      },
    },
    {
      name: 'authorization_code → id + access + refresh',
      async run(ctx) {
        const issuer = ctx.cfg.issuer;
        const got = await freshCode(ctx, issuer, {});
        if (!got.ok) return ctx.fail('a fresh authorization code', 'none', got.error.detail);
        const form = { grant_type: GT_CODE, code: got.code, client_id: ctx.cfg.clientId };
        const { r, req } = await postToken(ctx, issuer, form);
        if (r.status !== 200) return ctx.fail('200', String(r.status || 'network error'), { req, raw: clip(r.text) });
        const j = r.json || {};
        const missing = ['access_token', 'id_token', 'refresh_token'].filter((k) => !j[k]);
        if (missing.length) return ctx.fail('access + id + refresh tokens', 'missing: ' + missing.join(', '), { req, raw: clip(r.text) });
        return ctx.pass({ expected: '200 + all three tokens', actual: 'access + id + refresh present', detail: { req } });
      },
    },
    {
      name: 'authorization code is single-use',
      async run(ctx) {
        const issuer = ctx.cfg.issuer;
        const got = await freshCode(ctx, issuer, {});
        if (!got.ok) return ctx.fail('a fresh authorization code', 'none', got.error.detail);
        const form = { grant_type: GT_CODE, code: got.code, client_id: ctx.cfg.clientId };
        const first = await postToken(ctx, issuer, form);
        if (first.r.status !== 200) return ctx.fail('first exchange 200', String(first.r.status), { req: first.req, raw: clip(first.r.text) });
        const { r, req } = await postToken(ctx, issuer, form);
        if (r.status !== 400) return ctx.fail('400 on re-use', String(r.status), { req, raw: clip(r.text) });
        const err = (r.json || {}).error;
        if (err !== 'invalid_grant') return ctx.fail('invalid_grant', String(err), { req, raw: clip(r.text) });
        return ctx.pass({ expected: 're-use → 400 invalid_grant', actual: '400 invalid_grant', detail: { req } });
      },
    },
    {
      name: 'PKCE S256 happy path',
      async run(ctx) {
        const issuer = ctx.cfg.issuer;
        const got = await freshCode(ctx, issuer, { pkce: true });
        if (!got.ok) return ctx.fail('a fresh PKCE authorization code', 'none', got.error.detail);
        const form = { grant_type: GT_CODE, code: got.code, client_id: ctx.cfg.clientId, code_verifier: got.verifier };
        const { r, req } = await postToken(ctx, issuer, form);
        if (r.status !== 200) return ctx.fail('200', String(r.status || 'network error'), { req, raw: clip(r.text) });
        if (!(r.json || {}).access_token) return ctx.fail('access_token present', 'absent', { req, raw: clip(r.text) });
        return ctx.pass({ expected: 'S256 verifier → 200', actual: '200 with tokens', detail: { req } });
      },
    },
    {
      name: 'PKCE mismatch → invalid_grant (pkce), then code is burned',
      async run(ctx) {
        const issuer = ctx.cfg.issuer;
        const got = await freshCode(ctx, issuer, { pkce: true });
        if (!got.ok) return ctx.fail('a fresh PKCE authorization code', 'none', got.error.detail);
        const wrong = ctx.pkce.verifier(); // fresh random verifier, ≠ the challenge's
        const bad = await postToken(ctx, issuer, { grant_type: GT_CODE, code: got.code, client_id: ctx.cfg.clientId, code_verifier: wrong });
        if (bad.r.status !== 400) return ctx.fail('400 on wrong verifier', String(bad.r.status), { req: bad.req, raw: clip(bad.r.text) });
        const err = (bad.r.json || {}).error;
        if (err !== 'invalid_grant') return ctx.fail('invalid_grant', String(err), { req: bad.req, raw: clip(bad.r.text) });
        const desc = String((bad.r.json || {}).error_description || '');
        if (!/pkce/i.test(desc)) return ctx.fail("error_description contains 'pkce'", desc, { req: bad.req, raw: clip(bad.r.text) });
        // The code is burned even on the failed PKCE check: the correct verifier now fails too.
        const retry = await postToken(ctx, issuer, { grant_type: GT_CODE, code: got.code, client_id: ctx.cfg.clientId, code_verifier: got.verifier });
        if (retry.r.status !== 400) return ctx.fail('correct verifier also 400 (code burned)', String(retry.r.status), { req: retry.req, raw: clip(retry.r.text) });
        return ctx.pass({ expected: 'wrong→400 invalid_grant/pkce; then burned', actual: `400 ${err} (${desc}); retry ${retry.r.status}`, detail: { req: bad.req } });
      },
    },
    {
      name: 'PKCE asymmetry: challenge without verifier → invalid_grant',
      async run(ctx) {
        const issuer = ctx.cfg.issuer;
        const got = await freshCode(ctx, issuer, { pkce: true });
        if (!got.ok) return ctx.fail('a fresh PKCE authorization code', 'none', got.error.detail);
        const form = { grant_type: GT_CODE, code: got.code, client_id: ctx.cfg.clientId }; // no code_verifier
        const { r, req } = await postToken(ctx, issuer, form);
        if (r.status !== 400) return ctx.fail('400', String(r.status), { req, raw: clip(r.text) });
        const err = (r.json || {}).error;
        if (err !== 'invalid_grant') return ctx.fail('invalid_grant', String(err), { req, raw: clip(r.text) });
        return ctx.pass({ expected: 'missing verifier → 400 invalid_grant', actual: '400 invalid_grant', detail: { req } });
      },
    },
    {
      name: 'refresh_token → fresh access_token, same sub, different jti',
      async run(ctx) {
        const issuer = ctx.cfg.issuer;
        const got = await freshCode(ctx, issuer, {});
        if (!got.ok) return ctx.fail('a fresh authorization code', 'none', got.error.detail);
        const ex = await postToken(ctx, issuer, { grant_type: GT_CODE, code: got.code, client_id: ctx.cfg.clientId });
        if (ex.r.status !== 200) return ctx.fail('code exchange 200', String(ex.r.status), { req: ex.req, raw: clip(ex.r.text) });
        const refresh = (ex.r.json || {}).refresh_token;
        if (!refresh) return ctx.fail('refresh_token from exchange', 'absent', { req: ex.req, raw: clip(ex.r.text) });
        const orig = decodeSafe(ctx, (ex.r.json || {}).access_token);
        const form = { grant_type: GT_REFRESH, refresh_token: refresh, client_id: ctx.cfg.clientId };
        const { r, req } = await postToken(ctx, issuer, form);
        if (r.status !== 200) return ctx.fail('200', String(r.status || 'network error'), { req, raw: clip(r.text) });
        const dec = decodeSafe(ctx, (r.json || {}).access_token);
        if (!dec) return ctx.fail('decodable fresh access_token', 'absent/undecodable', { req, raw: clip(r.text) });
        if (orig && dec.payload.sub !== orig.payload.sub) return ctx.fail(`sub == ${orig.payload.sub}`, String(dec.payload.sub), { req });
        if (orig && dec.payload.jti === orig.payload.jti) return ctx.fail('jti differs from original', `both ${dec.payload.jti}`, { req });
        return ctx.pass({ expected: '200, same sub, new jti', actual: `sub=${dec.payload.sub} jti≠orig`, detail: { req } });
      },
    },
    {
      name: 'refresh_token unknown → invalid_grant',
      async run(ctx) {
        const issuer = ctx.cfg.issuer;
        const form = { grant_type: GT_REFRESH, refresh_token: 'nope', client_id: ctx.cfg.clientId };
        const { r, req } = await postToken(ctx, issuer, form);
        if (r.status !== 400) return ctx.fail('400', String(r.status), { req, raw: clip(r.text) });
        const err = (r.json || {}).error;
        if (err !== 'invalid_grant') return ctx.fail('invalid_grant', String(err), { req, raw: clip(r.text) });
        return ctx.pass({ expected: 'unknown refresh → 400 invalid_grant', actual: '400 invalid_grant', detail: { req } });
      },
    },
    {
      name: 'cross-issuer refresh → invalid_grant (different issuer)',
      async run(ctx) {
        const issuer = ctx.cfg.issuer;
        const issuer2 = ctx.cfg.issuer2;
        if (!issuer2 || issuer2 === issuer) return ctx.skip('issuer2 not configured or equal to issuer');
        const got = await freshCode(ctx, issuer, {});
        if (!got.ok) return ctx.fail('a fresh authorization code', 'none', got.error.detail);
        const ex = await postToken(ctx, issuer, { grant_type: GT_CODE, code: got.code, client_id: ctx.cfg.clientId });
        if (ex.r.status !== 200) return ctx.fail('code exchange 200', String(ex.r.status), { req: ex.req, raw: clip(ex.r.text) });
        const refresh = (ex.r.json || {}).refresh_token;
        if (!refresh) return ctx.fail('refresh_token from exchange', 'absent', { req: ex.req, raw: clip(ex.r.text) });
        const form = { grant_type: GT_REFRESH, refresh_token: refresh, client_id: ctx.cfg.clientId };
        const { r, req } = await postToken(ctx, issuer2, form); // redeem under the OTHER issuer
        if (r.status !== 400) return ctx.fail('400', String(r.status), { req, raw: clip(r.text) });
        const err = (r.json || {}).error;
        const desc = String((r.json || {}).error_description || '');
        if (err !== 'invalid_grant') return ctx.fail('invalid_grant', String(err), { req, raw: clip(r.text) });
        if (!/different issuer/i.test(desc)) return ctx.fail("error_description contains 'different issuer'", desc, { req, raw: clip(r.text) });
        return ctx.pass({ expected: '400 invalid_grant / different issuer', actual: `400 ${err} (${desc})`, detail: { req } });
      },
    },
    {
      name: 'no rotation: same refresh redeems twice',
      async run(ctx) {
        const issuer = ctx.cfg.issuer;
        const got = await freshCode(ctx, issuer, {});
        if (!got.ok) return ctx.fail('a fresh authorization code', 'none', got.error.detail);
        const ex = await postToken(ctx, issuer, { grant_type: GT_CODE, code: got.code, client_id: ctx.cfg.clientId });
        if (ex.r.status !== 200) return ctx.fail('code exchange 200', String(ex.r.status), { req: ex.req, raw: clip(ex.r.text) });
        const refresh = (ex.r.json || {}).refresh_token;
        if (!refresh) return ctx.fail('refresh_token from exchange', 'absent', { req: ex.req, raw: clip(ex.r.text) });
        const form = { grant_type: GT_REFRESH, refresh_token: refresh, client_id: ctx.cfg.clientId };
        const first = await postToken(ctx, issuer, form);
        if (first.r.status !== 200) return ctx.fail('first redemption 200', String(first.r.status), { req: first.req, raw: clip(first.r.text) });
        const second = await postToken(ctx, issuer, form);
        if (second.r.status !== 200) return ctx.fail('second redemption 200 (no rotation)', String(second.r.status), { req: second.req, raw: clip(second.r.text) });
        if (!(second.r.json || {}).access_token) return ctx.fail('second access_token present', 'absent', { req: second.req, raw: clip(second.r.text) });
        return ctx.pass({ expected: 'same token redeems twice → 200,200', actual: '200, 200', detail: { req: second.req } });
      },
    },
    {
      name: 'password → id + access, NO refresh, sub == username',
      async run(ctx) {
        const issuer = ctx.cfg.issuer;
        const form = { grant_type: GT_PW, username: 'bob', password: 'anything', scope: 'openid' };
        const { r, req } = await postToken(ctx, issuer, form);
        if (r.status !== 200) return ctx.fail('200', String(r.status || 'network error'), { req, raw: clip(r.text) });
        const j = r.json || {};
        if (!j.access_token || !j.id_token) return ctx.fail('id_token + access_token', `access:${!!j.access_token} id:${!!j.id_token}`, { req, raw: clip(r.text) });
        if (j.refresh_token) return ctx.fail('no refresh_token', 'present', { req, raw: clip(r.text) });
        const dec = decodeSafe(ctx, j.access_token);
        if (!dec) return ctx.fail('decodable access_token', 'undecodable', { req, raw: clip(j.access_token) });
        if (dec.payload.sub !== 'bob') return ctx.fail("sub == 'bob'", String(dec.payload.sub), { req });
        // Password is never validated: a different password also succeeds.
        const other = await postToken(ctx, issuer, { grant_type: GT_PW, username: 'bob', password: 'totally-different', scope: 'openid' });
        if (other.r.status !== 200) return ctx.fail('any password → 200', String(other.r.status), { req: other.req, raw: clip(other.r.text) });
        return ctx.pass({ expected: '200 id+access, no refresh, sub=bob; any pw', actual: `sub=${dec.payload.sub}, no refresh, 2nd pw 200`, detail: { req } });
      },
    },
    {
      name: 'jwt-bearer → access-only, claims kept, iss re-stamped',
      async run(ctx) {
        const issuer = ctx.cfg.issuer;
        const assertion = ctx.jose.craft({ payload: { sub: 'obo-user', iss: 'ext', custom_claim: 'x', scope: 'api:read' } });
        const form = { grant_type: GT_JWT_BEARER, assertion };
        const { r, req } = await postToken(ctx, issuer, form);
        if (r.status !== 200) return ctx.fail('200', String(r.status || 'network error'), { req, raw: clip(r.text) });
        const j = r.json || {};
        if (!j.access_token) return ctx.fail('access_token present', 'absent', { req, raw: clip(r.text) });
        if (j.id_token || j.refresh_token) return ctx.fail('access only (no id/refresh)', `id:${!!j.id_token} refresh:${!!j.refresh_token}`, { req, raw: clip(r.text) });
        const dec = decodeSafe(ctx, j.access_token);
        if (!dec) return ctx.fail('decodable access_token', 'undecodable', { req, raw: clip(j.access_token) });
        if (dec.payload.sub !== 'obo-user') return ctx.fail("sub == 'obo-user'", String(dec.payload.sub), { req });
        if (dec.payload.custom_claim !== 'x') return ctx.fail("custom_claim == 'x'", String(dec.payload.custom_claim), { req });
        const wantIss = `${ctx.api.base}/${issuer}`;
        if (dec.payload.iss !== wantIss) return ctx.fail(`iss re-stamped to ${wantIss}`, String(dec.payload.iss), { req });
        return ctx.pass({ expected: 'access-only, sub/custom kept, iss re-stamped', actual: `sub=${dec.payload.sub} iss=${dec.payload.iss}`, detail: { req } });
      },
    },
    {
      name: 'jwt-bearer missing assertion → invalid_request',
      async run(ctx) {
        const issuer = ctx.cfg.issuer;
        const form = { grant_type: GT_JWT_BEARER };
        const { r, req } = await postToken(ctx, issuer, form);
        if (r.status !== 400) return ctx.fail('400', String(r.status), { req, raw: clip(r.text) });
        const err = (r.json || {}).error;
        if (err !== 'invalid_request') return ctx.fail('invalid_request', String(err), { req, raw: clip(r.text) });
        return ctx.pass({ expected: 'no assertion → 400 invalid_request', actual: '400 invalid_request', detail: { req } });
      },
    },
    {
      name: 'jwt-bearer no scope anywhere → invalid_request',
      async run(ctx) {
        const issuer = ctx.cfg.issuer;
        const assertion = ctx.jose.craft({ payload: { sub: 'obo-user', iss: 'ext' } }); // no scope claim
        const form = { grant_type: GT_JWT_BEARER, assertion }; // and no request scope
        const { r, req } = await postToken(ctx, issuer, form);
        if (r.status !== 400) return ctx.fail('400', String(r.status), { req, raw: clip(r.text) });
        const err = (r.json || {}).error;
        if (err !== 'invalid_request') return ctx.fail('invalid_request', String(err), { req, raw: clip(r.text) });
        return ctx.pass({ expected: 'no scope → 400 invalid_request', actual: '400 invalid_request', detail: { req } });
      },
    },
    {
      name: 'token-exchange → access-only, issued_token_type, aud, no scope',
      async run(ctx) {
        const issuer = ctx.cfg.issuer;
        const subject_token = ctx.jose.craft({ payload: { sub: 'subj', idp_marker: 1 } });
        const form = {
          grant_type: GT_EXCHANGE,
          subject_token,
          subject_token_type: TT_JWT,
          client_id: ctx.cfg.clientId,
          client_secret: 's3cret',
          audience: 'exchanged-aud',
        };
        const { r, req } = await postToken(ctx, issuer, form);
        if (r.status !== 200) return ctx.fail('200', String(r.status || 'network error'), { req, raw: clip(r.text) });
        const j = r.json || {};
        if (j.issued_token_type !== TT_ACCESS) return ctx.fail(`issued_token_type == ${TT_ACCESS}`, String(j.issued_token_type), { req, raw: clip(r.text) });
        if (!j.access_token) return ctx.fail('access_token present', 'absent', { req, raw: clip(r.text) });
        if (j.id_token || j.refresh_token) return ctx.fail('access only (no id/refresh)', `id:${!!j.id_token} refresh:${!!j.refresh_token}`, { req, raw: clip(r.text) });
        if (j.scope !== undefined) return ctx.fail('scope field absent', String(j.scope), { req, raw: clip(r.text) });
        const dec = decodeSafe(ctx, j.access_token);
        if (!dec) return ctx.fail('decodable access_token', 'undecodable', { req, raw: clip(j.access_token) });
        if (!audList(dec.payload.aud).includes('exchanged-aud')) {
          return ctx.fail("aud contains 'exchanged-aud'", JSON.stringify(dec.payload.aud), { req });
        }
        return ctx.pass({ expected: '200 access-only, issued_token_type, aud=exchanged-aud, no scope', actual: 'all satisfied', detail: { req } });
      },
    },
    {
      name: 'token-exchange without client auth → invalid_request (ClientAuthentication)',
      async run(ctx) {
        const issuer = ctx.cfg.issuer;
        const subject_token = ctx.jose.craft({ payload: { sub: 'subj' } });
        const form = { grant_type: GT_EXCHANGE, subject_token, subject_token_type: TT_JWT }; // no client_id/secret/basic
        const { r, req } = await postToken(ctx, issuer, form);
        if (r.status !== 400) return ctx.fail('400', String(r.status), { req, raw: clip(r.text) });
        const err = (r.json || {}).error;
        const desc = String((r.json || {}).error_description || '');
        if (err !== 'invalid_request') return ctx.fail('invalid_request', String(err), { req, raw: clip(r.text) });
        if (!/clientauthentication/i.test(desc)) return ctx.fail("description mentions ClientAuthentication", desc, { req, raw: clip(r.text) });
        return ctx.pass({ expected: '400 invalid_request / ClientAuthentication', actual: `400 ${err} (${desc})`, detail: { req } });
      },
    },
    {
      name: 'private_key_jwt structural rules (token-exchange)',
      async run(ctx) {
        const issuer = ctx.cfg.issuer;
        const cid = ctx.cfg.clientId;
        const aud = `${ctx.api.base}/${issuer}/token`;
        const subject_token = ctx.jose.craft({ payload: { sub: 'subj' } });
        // client_id is sent so the effective client_id is fixed to cfg.clientId and
        // each malformation triggers its own rule (iss/sub checks compare to it).
        const build = (assertion) => ({
          grant_type: GT_EXCHANGE,
          subject_token,
          subject_token_type: TT_JWT,
          client_id: cid,
          client_assertion_type: CLIENT_ASSERTION_TYPE,
          client_assertion: assertion,
        });
        const cases = [
          { name: 'valid (lifetime 60)', assertion: ctx.jose.craftPrivateKeyJwt({ clientId: cid, aud, lifetime: 60 }), want: 200 },
          { name: 'lifetime 3600', assertion: ctx.jose.craftPrivateKeyJwt({ clientId: cid, aud, lifetime: 3600 }), want: 400 },
          { name: 'iss != client_id', assertion: ctx.jose.craftPrivateKeyJwt({ clientId: cid, aud, lifetime: 60, iss: 'not-' + cid }), want: 400 },
          { name: 'sub != client_id', assertion: ctx.jose.craftPrivateKeyJwt({ clientId: cid, aud, lifetime: 60, sub: 'not-' + cid }), want: 400 },
          { name: 'aud empty', assertion: ctx.jose.craftPrivateKeyJwt({ clientId: cid, aud: [], lifetime: 60 }), want: 400 },
          { name: 'wrong aud', assertion: ctx.jose.craftPrivateKeyJwt({ clientId: cid, aud: 'https://wrong.example/token', lifetime: 60 }), want: 400 },
        ];
        const fails = [];
        let lastReq;
        let lastRaw;
        for (const c of cases) {
          // eslint-disable-next-line no-await-in-loop -- serial probes, order-independent
          const { r, req } = await postToken(ctx, issuer, build(c.assertion));
          lastReq = req;
          lastRaw = r.text;
          if (r.status !== c.want) {
            fails.push(`${c.name}: expected ${c.want} got ${r.status || 'network error'}`);
            continue;
          }
          if (c.want === 400 && (r.json || {}).error !== 'invalid_request') {
            fails.push(`${c.name}: expected invalid_request got ${(r.json || {}).error}`);
          }
        }
        if (fails.length) return ctx.fail('valid→200; 5 malformations→400 invalid_request', fails.join(' | '), { req: lastReq, raw: clip(lastRaw) });
        return ctx.pass({
          expected: 'valid→200; lifetime/iss/sub/empty-aud/wrong-aud → 400 invalid_request',
          actual: 'all 6 structural cases as expected',
          detail: { note: 'signature deliberately unverified; only structure enforced' },
        });
      },
    },
    {
      name: 'grant_type: blank → invalid_request, unknown → invalid_grant',
      async run(ctx) {
        const issuer = ctx.cfg.issuer;
        const blank = await postToken(ctx, issuer, { grant_type: '', client_id: ctx.cfg.clientId });
        if (blank.r.status !== 400 || (blank.r.json || {}).error !== 'invalid_request') {
          return ctx.fail('blank → 400 invalid_request', `${blank.r.status} ${(blank.r.json || {}).error}`, { req: blank.req, raw: clip(blank.r.text) });
        }
        const unknown = await postToken(ctx, issuer, { grant_type: 'foo', client_id: ctx.cfg.clientId });
        if (unknown.r.status !== 400 || (unknown.r.json || {}).error !== 'invalid_grant') {
          return ctx.fail('unknown → 400 invalid_grant', `${unknown.r.status} ${(unknown.r.json || {}).error}`, { req: unknown.req, raw: clip(unknown.r.text) });
        }
        return ctx.pass({ expected: 'blank→invalid_request; foo→invalid_grant', actual: 'both as expected', detail: { req: [blank.req, unknown.req] } });
      },
    },
    {
      name: 'client_secret_basic accepted (client_credentials)',
      async run(ctx) {
        const issuer = ctx.cfg.issuer;
        const form = { grant_type: GT_CC, scope: 'openid' }; // client_id comes from the Basic header
        const { r, req } = await postToken(ctx, issuer, form, { basicAuth: { user: ctx.cfg.clientId, pass: 'whatever' } });
        if (r.status !== 200) return ctx.fail('200', String(r.status || 'network error'), { req, raw: clip(r.text) });
        const dec = decodeSafe(ctx, (r.json || {}).access_token);
        if (!dec) return ctx.fail('decodable access_token', 'absent/undecodable', { req, raw: clip(r.text) });
        if (dec.payload.sub !== ctx.cfg.clientId) return ctx.fail(`sub == ${ctx.cfg.clientId}`, String(dec.payload.sub), { req });
        return ctx.pass({ expected: 'Basic auth → 200, sub==clientId', actual: `200 sub=${dec.payload.sub}`, detail: { req } });
      },
    },
  ],
};
