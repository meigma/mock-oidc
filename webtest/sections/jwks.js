// jwks.js — JWKS & Key Isolation.
//
// Verifies the per-issuer JWK set: well-formed public keys, no private members,
// lazy materialization of a fresh issuer, per-issuer key distinctness, real
// cross-issuer signature isolation (mint under one issuer, verify against both),
// and the reserved-prefix 404. Follows the discovery.js reference: each check is
// independent, never throws, and returns ctx.pass / ctx.fail / ctx.skip with a
// useful expected/actual plus detail.req (request line) and detail.raw on
// failures. See README.md for the module contract.

// Private JWK members that must NEVER appear on a published key (RSA CRT params
// and the private exponent).
const PRIVATE_MEMBERS = ['d', 'p', 'q', 'dp', 'dq', 'qi'];

// clip truncates raw response text to keep failure details readable (~500 chars).
function clip(text, n = 500) {
  const s = typeof text === 'string' ? text : String(text ?? '');
  return s.length <= n ? s : s.slice(0, n) + ' …';
}

// getJwks fetches an issuer's JWK set and returns { r, req } so any check can run
// alone. req is the request line surfaced in failure detail.
async function getJwks(ctx, issuer) {
  const r = await ctx.api.jwks(issuer);
  const req = { method: 'GET', url: `${ctx.api.base}/${issuer}/jwks` };
  return { r, req };
}

// keysOf returns the keys array of a jwks envelope, or null when the body is not
// a { keys: [...] } object.
function keysOf(r) {
  return r && r.json && Array.isArray(r.json.keys) ? r.json.keys : null;
}

export default {
  id: 'jwks',
  title: 'JWKS & Key Isolation',
  checks: [
    {
      name: 'jwks keys are well-formed (sig / RS256 / RSA, kid == issuer)',
      async run(ctx) {
        const { r, req } = await getJwks(ctx, ctx.cfg.issuer);
        if (r.status !== 200) return ctx.fail('200', String(r.status || 'network error'), { req, raw: clip(r.text) });
        const keys = keysOf(r);
        if (!keys || keys.length === 0) {
          return ctx.fail('non-empty keys[]', keys ? 'empty keys[]' : 'no keys array', { req, raw: clip(r.text) });
        }
        const bad = [];
        for (const k of keys) {
          if (k.use !== 'sig') bad.push(`kid=${k.kid}: use=${JSON.stringify(k.use)} (want "sig")`);
          if (k.alg !== 'RS256') bad.push(`kid=${k.kid}: alg=${JSON.stringify(k.alg)} (want "RS256")`);
          if (k.kty !== 'RSA') bad.push(`kid=${k.kid}: kty=${JSON.stringify(k.kty)} (want "RSA")`);
          if (k.kid !== ctx.cfg.issuer) bad.push(`kid=${JSON.stringify(k.kid)} (want ${JSON.stringify(ctx.cfg.issuer)})`);
        }
        if (bad.length) {
          return ctx.fail(`all keys sig/RS256/RSA, kid=${ctx.cfg.issuer}`, bad.join(' | '), { req, raw: clip(r.text) });
        }
        return ctx.pass({
          expected: `sig/RS256/RSA, kid=${ctx.cfg.issuer}`,
          actual: `${keys.length} key(s), all sig/RS256/RSA, kid=${ctx.cfg.issuer}`,
          detail: { req },
        });
      },
    },
    {
      name: 'no private JWK members (d, p, q, dp, dq, qi)',
      async run(ctx) {
        const { r, req } = await getJwks(ctx, ctx.cfg.issuer);
        const keys = keysOf(r);
        if (!keys) return ctx.fail('JWK set', `status ${r.status}, no keys array`, { req, raw: clip(r.text) });
        const leaks = [];
        for (const k of keys) {
          for (const m of PRIVATE_MEMBERS) {
            if (Object.prototype.hasOwnProperty.call(k, m)) leaks.push(`kid=${k.kid}.${m}`);
          }
        }
        if (leaks.length) {
          return ctx.fail('none of: ' + PRIVATE_MEMBERS.join(', '), 'present: ' + leaks.join(', '), { req, raw: clip(r.text) });
        }
        return ctx.pass({
          expected: 'no private members',
          actual: 'none present',
          detail: { req, note: 'checked: ' + PRIVATE_MEMBERS.join(', ') },
        });
      },
    },
    {
      name: 'fresh issuer materializes a key set on first hit',
      async run(ctx) {
        const fresh = 'mat-' + ctx.newState().slice(0, 12);
        const { r, req } = await getJwks(ctx, fresh);
        if (r.status !== 200) return ctx.fail('200', String(r.status || 'network error'), { req, raw: clip(r.text) });
        const keys = keysOf(r);
        if (!keys || keys.length === 0) {
          return ctx.fail('non-empty keys[] on first hit', keys ? 'empty keys[]' : 'no keys array', { req, raw: clip(r.text) });
        }
        if (keys[0].kid !== fresh) {
          return ctx.fail(`kid == ${fresh}`, String(keys[0].kid), { req, raw: clip(r.text) });
        }
        return ctx.pass({
          expected: `non-empty, kid=${fresh}`,
          actual: `${keys.length} key(s), kid=${fresh}`,
          detail: { req, note: `throwaway issuer ${fresh} materialized on demand` },
        });
      },
    },
    {
      name: 'distinct issuers → distinct kid and distinct modulus n',
      async run(ctx) {
        const a = await getJwks(ctx, ctx.cfg.issuer);
        const b = await getJwks(ctx, ctx.cfg.issuer2);
        const ka = keysOf(a.r);
        const kb = keysOf(b.r);
        if (!ka || !ka.length || !kb || !kb.length) {
          return ctx.fail('both issuers return a key', `${ctx.cfg.issuer}: ${a.r.status} / ${ctx.cfg.issuer2}: ${b.r.status}`, {
            req: [a.req, b.req],
            raw: clip(`${ctx.cfg.issuer}:\n${a.r.text}\n\n${ctx.cfg.issuer2}:\n${b.r.text}`),
          });
        }
        const k1 = ka[0];
        const k2 = kb[0];
        const problems = [];
        if (k1.kid === k2.kid) problems.push(`kid collision: both ${JSON.stringify(k1.kid)}`);
        if (k1.n === undefined || k2.n === undefined) problems.push('missing modulus n');
        else if (k1.n === k2.n) problems.push('modulus n identical across issuers');
        if (problems.length) {
          return ctx.fail('distinct kid and n', problems.join(' | '), { req: [a.req, b.req] });
        }
        return ctx.pass({
          expected: 'distinct kid and n',
          actual: `kid ${JSON.stringify(k1.kid)} vs ${JSON.stringify(k2.kid)}; moduli differ`,
          detail: { req: [a.req, b.req] },
        });
      },
    },
    {
      name: 'cross-issuer isolation: minted token verifies only under its issuer',
      async run(ctx) {
        const mintBody = { issuer: ctx.cfg.issuer, kind: 'access_token' };
        const m = await ctx.api.mint(mintBody);
        const mintReq = { method: 'POST', url: `${ctx.api.base}/_mock/mint`, body: mintBody };
        if (m.status !== 200 || !m.json || !m.json.token) {
          return ctx.fail('200 + { token }', String(m.status || 'network error'), { req: mintReq, raw: clip(m.text) });
        }
        const token = m.json.token;
        const a = await getJwks(ctx, ctx.cfg.issuer);
        const b = await getJwks(ctx, ctx.cfg.issuer2);
        if (!keysOf(a.r) || !keysOf(b.r)) {
          return ctx.fail('both JWK sets fetched', `${ctx.cfg.issuer}: ${a.r.status} / ${ctx.cfg.issuer2}: ${b.r.status}`, {
            req: [mintReq, a.req, b.req],
          });
        }
        const okOwn = await ctx.jwt.verify(token, a.r.json);
        const okOther = await ctx.jwt.verify(token, b.r.json);
        if (!okOwn || okOther) {
          return ctx.fail(
            `verify under ${ctx.cfg.issuer}=true, under ${ctx.cfg.issuer2}=false`,
            `under ${ctx.cfg.issuer}=${okOwn}, under ${ctx.cfg.issuer2}=${okOther}`,
            { req: [mintReq, a.req, b.req], raw: clip('token: ' + token) },
          );
        }
        return ctx.pass({
          expected: `true under ${ctx.cfg.issuer}, false under ${ctx.cfg.issuer2}`,
          actual: 'verified only under its own issuer',
          detail: { req: [mintReq, a.req, b.req], note: `kid=${m.json.kid}, alg=${m.json.algorithm}` },
        });
      },
    },
    {
      name: "reserved prefix: GET /_mock/jwks → 404 not_found (OAuth2 envelope)",
      async run(ctx) {
        // The '_mock' issuer segment is reserved, so the /{issuer}/jwks route
        // parses it into a not_found ProtocolError rendered as the OAuth2 error
        // envelope. (See NOTES: the task's literal /_mockx/jwks is NOT reserved —
        // ParseIssuerID reserves only exactly "_mock" or a "_mock/…" prefix — so
        // /_mockx materializes a valid issuer and returns 200. /_mock/jwks is the
        // path that genuinely exercises the reserved-prefix 404.)
        const path = '/_mock/jwks';
        const r = await ctx.api.raw('GET', path);
        const req = { method: 'GET', url: `${ctx.api.base}${path}` };
        if (r.status !== 404) {
          return ctx.fail('404', String(r.status || 'network error'), { req, raw: clip(r.text) });
        }
        const code = r.json && r.json.error;
        if (code !== 'not_found') {
          return ctx.fail("{ error: 'not_found' }", code === undefined ? 'no OAuth2 error envelope' : JSON.stringify(code), {
            req,
            raw: clip(r.text),
          });
        }
        return ctx.pass({
          expected: "404 + { error: 'not_found' }",
          actual: "404 + error='not_found'",
          detail: { req },
        });
      },
    },
  ],
};
