// discovery.js — REFERENCE section (the copy-me pattern for other builders).
//
// A section default-exports { id, title, checks } (and optionally mount/manual).
// Each check has a name and an async run(ctx) that returns a CheckResult built
// via ctx.pass / ctx.fail / ctx.skip — never throwing (the runner would catch it,
// but an explicit fail carries a useful expected/actual). ctx.api is base-aware;
// ctx.cfg holds the console config. See README.md for the full contract.

const EXPECTED_ORDER = [
  'issuer',
  'authorization_endpoint',
  'end_session_endpoint',
  'revocation_endpoint',
  'token_endpoint',
  'userinfo_endpoint',
  'jwks_uri',
  'introspection_endpoint',
  'response_types_supported',
  'response_modes_supported',
  'subject_types_supported',
  'id_token_signing_alg_values_supported',
  'code_challenge_methods_supported',
];

const FORBIDDEN_KEYS = [
  'registration_endpoint',
  'scopes_supported',
  'grant_types_supported',
  'claims_supported',
  'token_endpoint_auth_methods_supported',
  '$schema',
];

const ENUMS = {
  response_types_supported: ['code', 'none', 'id_token', 'token'],
  response_modes_supported: ['query', 'fragment', 'form_post'],
  subject_types_supported: ['public'],
  id_token_signing_alg_values_supported: ['ES256', 'ES384', 'RS256', 'RS384', 'RS512', 'PS256', 'PS384', 'PS512'],
  code_challenge_methods_supported: ['plain', 'S256'],
};

function docUrl(ctx, oauth) {
  const doc = oauth ? '.well-known/oauth-authorization-server' : '.well-known/openid-configuration';
  return `${ctx.api.base}/${ctx.cfg.issuer}/${doc}`;
}

function arraysEqual(a, b) {
  if (!Array.isArray(a) || !Array.isArray(b) || a.length !== b.length) return false;
  return a.every((v, i) => v === b[i]);
}

// getDoc fetches the openid-configuration and returns { r, req } or a fail-able
// shape. Each check calls it independently so any check can be re-run alone.
async function getDoc(ctx, oauth = false) {
  const r = await ctx.api.discovery(ctx.cfg.issuer, { oauth });
  const req = { method: 'GET', url: docUrl(ctx, oauth) };
  return { r, req };
}

export default {
  id: 'discovery',
  title: 'Discovery / .well-known',
  checks: [
    {
      name: 'openid-configuration returns 200 JSON',
      async run(ctx) {
        const { r, req } = await getDoc(ctx);
        if (r.status !== 200) return ctx.fail('200', String(r.status || 'network error'), { req, raw: r.text });
        if (!r.json || typeof r.json !== 'object') return ctx.fail('valid JSON object', 'unparseable body', { req, raw: r.text });
        return ctx.pass({ expected: '200 + JSON', actual: `${r.status} + JSON`, detail: { req, raw: r.text } });
      },
    },
    {
      name: 'field order matches the 13-key contract',
      async run(ctx) {
        const { r, req } = await getDoc(ctx);
        if (!r.json) return ctx.fail('JSON doc', 'no JSON', { req, raw: r.text });
        const keys = Object.keys(r.json);
        if (!arraysEqual(keys, EXPECTED_ORDER)) {
          return ctx.fail(EXPECTED_ORDER.join(', '), keys.join(', '), { req, raw: JSON.stringify(keys, null, 2) });
        }
        return ctx.pass({ expected: EXPECTED_ORDER.join(', '), actual: keys.join(', '), detail: { req } });
      },
    },
    {
      name: 'forbidden keys are absent',
      async run(ctx) {
        const { r, req } = await getDoc(ctx);
        if (!r.json) return ctx.fail('JSON doc', 'no JSON', { req, raw: r.text });
        const present = FORBIDDEN_KEYS.filter((k) => Object.prototype.hasOwnProperty.call(r.json, k));
        if (present.length) return ctx.fail('none of: ' + FORBIDDEN_KEYS.join(', '), 'present: ' + present.join(', '), { req });
        return ctx.pass({ expected: 'none present', actual: 'none present', detail: { req, note: 'checked: ' + FORBIDDEN_KEYS.join(', ') } });
      },
    },
    {
      name: 'both .well-known paths are byte-identical',
      async run(ctx) {
        const oidc = await getDoc(ctx, false);
        const oauth = await getDoc(ctx, true);
        if (oidc.r.status !== 200 || oauth.r.status !== 200) {
          return ctx.fail('both 200', `oidc ${oidc.r.status} / oauth ${oauth.r.status}`, { req: [oidc.req, oauth.req] });
        }
        if (oidc.r.text !== oauth.r.text) {
          return ctx.fail('identical bodies', 'bodies differ', {
            req: [oidc.req, oauth.req],
            raw: 'openid-configuration:\n' + oidc.r.text + '\n\noauth-authorization-server:\n' + oauth.r.text,
          });
        }
        return ctx.pass({ expected: 'identical bodies', actual: 'identical', detail: { req: [oidc.req, oauth.req] } });
      },
    },
    {
      name: 'no Link response header',
      async run(ctx) {
        const { r, req } = await getDoc(ctx);
        const link = r.headers.get('Link');
        if (link) return ctx.fail('no Link header', link, { req });
        return ctx.pass({ expected: 'absent', actual: 'absent', detail: { req } });
      },
    },
    {
      name: 'issuer field equals base + / + issuer',
      async run(ctx) {
        const { r, req } = await getDoc(ctx);
        if (!r.json) return ctx.fail('JSON doc', 'no JSON', { req, raw: r.text });
        const normBase = String(ctx.cfg.base).replace(/\/+$/, '');
        const expected = `${normBase}/${ctx.cfg.issuer}`;
        if (r.json.issuer !== expected) return ctx.fail(expected, String(r.json.issuer), { req });
        return ctx.pass({ expected, actual: r.json.issuer, detail: { req } });
      },
    },
    {
      name: 'enum contents exactly match the contract',
      async run(ctx) {
        const { r, req } = await getDoc(ctx);
        if (!r.json) return ctx.fail('JSON doc', 'no JSON', { req, raw: r.text });
        const mismatches = [];
        for (const [key, want] of Object.entries(ENUMS)) {
          if (!arraysEqual(r.json[key], want)) {
            mismatches.push(`${key}: expected [${want.join(', ')}] got [${(r.json[key] || []).join(', ')}]`);
          }
        }
        if (mismatches.length) {
          return ctx.fail('all enums match', mismatches.join(' | '), { req, raw: JSON.stringify(
            Object.fromEntries(Object.keys(ENUMS).map((k) => [k, r.json[k]])), null, 2) });
        }
        return ctx.pass({ expected: 'all 5 enums match', actual: 'all match', detail: { req } });
      },
    },
    {
      name: 'every endpoint/jwks_uri starts with the issuer URL',
      async run(ctx) {
        const { r, req } = await getDoc(ctx);
        if (!r.json) return ctx.fail('JSON doc', 'no JSON', { req, raw: r.text });
        const prefix = r.json.issuer;
        const bad = [];
        for (const [k, v] of Object.entries(r.json)) {
          if (k === 'jwks_uri' || k.endsWith('_endpoint')) {
            if (typeof v !== 'string' || !v.startsWith(prefix)) bad.push(`${k}=${v}`);
          }
        }
        if (bad.length) return ctx.fail(`all start with ${prefix}`, bad.join(', '), { req });
        return ctx.pass({ expected: `all start with ${prefix}`, actual: 'all prefixed', detail: { req } });
      },
    },
  ],
};
