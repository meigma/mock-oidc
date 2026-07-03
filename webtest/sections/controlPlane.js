// controlPlane.js — the /_mock control plane (mint, scenarios, capture, clock,
// reset). Follows the discovery.js reference: each check has a name and an async
// run(ctx) returning a CheckResult via ctx.pass / ctx.fail / ctx.skip, never
// throwing. Every check is base-aware through ctx.api, self-contained (it sets up
// the state it needs), and leaves the server clean — scenario queue and capture
// log cleared, and the clock always returned to real time.

// FROZEN_INSTANT is the fixed 'now' used by the clock-freeze checks; FROZEN_EPOCH
// is its Unix seconds (2030-01-01T00:00:00Z === 1893456000), the iat every token
// minted while frozen must carry.
const FROZEN_INSTANT = '2030-01-01T00:00:00Z';
const FROZEN_EPOCH = Math.floor(Date.parse(FROZEN_INSTANT) / 1000);

// clip trims a raw response body to ~500 chars for the failure detail pane.
function clip(text, n = 500) {
  if (text === undefined || text === null) return text;
  const s = String(text);
  return s.length <= n ? s : s.slice(0, n) + ' …';
}

// mockUrl / issuerUrl build the request-line URLs shown in failure detail.
function mockUrl(ctx, sub) {
  return `${ctx.api.base}/_mock/${sub}`;
}
function issuerUrl(ctx, issuer, path) {
  return `${ctx.api.base}/${issuer}/${path}`;
}

// ccForm is a minimal, valid client_credentials request body (secret is never
// validated). Grants consult the scenario queue and stamp default claims, so this
// is the simplest way to drive a real /token issuance for a given issuer.
function ccForm(ctx) {
  return { grant_type: 'client_credentials', client_id: ctx.cfg.clientId, client_secret: 'test-secret' };
}

// safeDecode decodes a compact JWT without throwing (jwt.decode throws on a
// malformed token); returns null when the token is absent or unparseable.
function safeDecode(ctx, token) {
  if (!token) return null;
  try {
    return ctx.jwt.decode(token);
  } catch {
    return null;
  }
}

// restoreClock unfreezes the clock and resolves to the passed-through result, so a
// clock check returns clean on every path (pass or early fail).
async function restoreClock(ctx, result) {
  await ctx.api.clockSet({ frozen: false });
  return result;
}

export default {
  id: 'controlPlane',
  title: 'Control Plane (/_mock)',
  checks: [
    {
      name: 'mint ≡ grant: minted token verifies and userinfo echoes sub',
      async run(ctx) {
        const body = { issuer: ctx.cfg.issuer, subject: 'm1' };
        const req = { method: 'POST', url: mockUrl(ctx, 'mint'), body };
        const r = await ctx.api.mint(body);
        if (r.status !== 200 || !r.json) {
          return ctx.fail('200 + mint JSON', String(r.status || 'network error'), { req, raw: clip(r.text) });
        }
        const missing = ['token', 'kid', 'algorithm', 'expiresAt'].filter((k) => !r.json[k]);
        if (missing.length) {
          return ctx.fail('token/kid/algorithm/expiresAt present', 'missing: ' + missing.join(', '), { req, raw: clip(r.text) });
        }
        // The minted token must verify against the issuer's published JWKS.
        const jwks = await ctx.api.jwks(ctx.cfg.issuer);
        const jwksReq = { method: 'GET', url: issuerUrl(ctx, ctx.cfg.issuer, 'jwks') };
        if (jwks.status !== 200 || !jwks.json) {
          return ctx.fail('jwks 200', String(jwks.status), { req: [req, jwksReq], raw: clip(jwks.text) });
        }
        const ok = await ctx.jwt.verify(r.json.token, jwks.json);
        if (!ok) {
          return ctx.fail('jwt.verify true', 'signature did not verify', { req: [req, jwksReq], raw: clip(r.text) });
        }
        // userinfo must accept the minted access token and echo sub = m1.
        const u = await ctx.api.userinfo(ctx.cfg.issuer, r.json.token);
        const uReq = { method: 'GET', url: issuerUrl(ctx, ctx.cfg.issuer, 'userinfo'), note: 'Bearer <minted>' };
        if (u.status !== 200 || !u.json) {
          return ctx.fail('userinfo 200', String(u.status), { req: [req, uReq], raw: clip(u.text) });
        }
        if (u.json.sub !== 'm1') {
          return ctx.fail('userinfo sub = m1', String(u.json.sub), { req: [req, uReq], raw: clip(u.text) });
        }
        return ctx.pass({
          expected: 'verifies + userinfo sub m1',
          actual: `kid=${r.json.kid} alg=${r.json.algorithm} sub=${u.json.sub}`,
          detail: { req: [req, jwksReq, uReq] },
        });
      },
    },
    {
      name: "mint reserved issuer ('_mock') → 4xx problem",
      async run(ctx) {
        // Ground truth: ParseIssuerID reserves EXACTLY the '_mock' segment (or a
        // '_mock/…' prefix) — a bare '_mockthing' is a legal issuer that mints 200.
        // The meaningful reserved-issuer probe therefore uses '_mock' itself.
        const body = { issuer: '_mock', subject: 'x' };
        const req = { method: 'POST', url: mockUrl(ctx, 'mint'), body };
        const r = await ctx.api.mint(body);
        if (r.status < 400) {
          return ctx.fail('status >= 400 (problem)', String(r.status || 'network error'), { req, raw: clip(r.text) });
        }
        if (r.json && r.json.token) {
          return ctx.fail('no minted token', 'a token was returned', { req, raw: clip(r.text) });
        }
        return ctx.pass({ expected: 'status >= 400 problem', actual: `${r.status} (no token)`, detail: { req, raw: clip(r.text) } });
      },
    },
    {
      name: 'scenario is one-shot AND issuer-matched',
      async run(ctx) {
        await ctx.api.scenarioClear();
        const enqBody = { issuer: ctx.cfg.issuer2, claims: { iso_marker: 'b' } };
        const enqReq = { method: 'POST', url: mockUrl(ctx, 'scenarios'), body: enqBody };
        const enq = await ctx.api.scenarioEnqueue(enqBody);
        if (enq.status !== 200 || !enq.json || enq.json.queueDepth !== 1) {
          return ctx.fail('enqueue queueDepth 1', `${enq.status} depth ${enq.json && enq.json.queueDepth}`, { req: enqReq, raw: clip(enq.text) });
        }

        // A token for a DIFFERENT issuer must NOT consume the head-blocked scenario.
        const otherReq = { method: 'POST', url: issuerUrl(ctx, ctx.cfg.issuer, 'token'), body: ccForm(ctx) };
        const other = await ctx.api.token(ctx.cfg.issuer, ccForm(ctx));
        const otherDec = safeDecode(ctx, other.json && other.json.access_token);
        if (!otherDec) {
          return ctx.fail(`${ctx.cfg.issuer} token issued`, `status ${other.status}`, { req: otherReq, raw: clip(other.text) });
        }
        if (otherDec.payload.iso_marker !== undefined) {
          return ctx.fail(`${ctx.cfg.issuer} token WITHOUT iso_marker`, `iso_marker=${otherDec.payload.iso_marker}`, { req: otherReq, raw: clip(other.text) });
        }
        const stillList = await ctx.api.scenarioList();
        if (!stillList.json || stillList.json.queueDepth !== 1) {
          return ctx.fail('scenario still pending (depth 1)', `depth ${stillList.json && stillList.json.queueDepth}`, { req: { method: 'GET', url: mockUrl(ctx, 'scenarios') }, raw: clip(stillList.text) });
        }

        // A token for the MATCHING issuer consumes it and carries iso_marker.
        const matchReq = { method: 'POST', url: issuerUrl(ctx, ctx.cfg.issuer2, 'token'), body: ccForm(ctx) };
        const match = await ctx.api.token(ctx.cfg.issuer2, ccForm(ctx));
        const matchDec = safeDecode(ctx, match.json && match.json.access_token);
        if (!matchDec) {
          return ctx.fail(`${ctx.cfg.issuer2} token issued`, `status ${match.status}`, { req: matchReq, raw: clip(match.text) });
        }
        if (matchDec.payload.iso_marker !== 'b') {
          return ctx.fail(`${ctx.cfg.issuer2} token WITH iso_marker=b`, `iso_marker=${matchDec.payload.iso_marker}`, { req: matchReq, raw: clip(match.text) });
        }
        const drained = await ctx.api.scenarioList();
        if (!drained.json || drained.json.queueDepth !== 0) {
          return ctx.fail('queueDepth 0 after consume', `depth ${drained.json && drained.json.queueDepth}`, { req: { method: 'GET', url: mockUrl(ctx, 'scenarios') }, raw: clip(drained.text) });
        }
        return ctx.pass({
          expected: `${ctx.cfg.issuer} skips, ${ctx.cfg.issuer2} consumes; depth 1→0`,
          actual: 'issuer-matched, single-use as expected',
          detail: { req: [enqReq, otherReq, matchReq] },
        });
      },
    },
    {
      name: 'scenario list/clear: depth 2 with issuer fields, DELETE → 0',
      async run(ctx) {
        await ctx.api.scenarioClear();
        const b1 = { issuer: ctx.cfg.issuer, claims: { k: 1 } };
        const b2 = { issuer: ctx.cfg.issuer2, claims: { k: 2 } };
        const e1 = await ctx.api.scenarioEnqueue(b1);
        const e2 = await ctx.api.scenarioEnqueue(b2);
        const req = { method: 'POST', url: mockUrl(ctx, 'scenarios'), body: [b1, b2] };
        if (!e1.json || e1.json.queueDepth !== 1 || !e2.json || e2.json.queueDepth !== 2) {
          return ctx.fail('depths 1 then 2', `${e1.json && e1.json.queueDepth} then ${e2.json && e2.json.queueDepth}`, { req, raw: clip(e1.text + '\n' + e2.text) });
        }
        const list = await ctx.api.scenarioList();
        const listReq = { method: 'GET', url: mockUrl(ctx, 'scenarios') };
        if (!list.json || list.json.queueDepth !== 2 || !Array.isArray(list.json.scenarios) || list.json.scenarios.length !== 2) {
          return ctx.fail('list depth 2 with 2 entries', clip(list.text), { req: listReq, raw: clip(list.text) });
        }
        const issuers = list.json.scenarios.map((s) => s.issuer);
        if (issuers[0] !== ctx.cfg.issuer || issuers[1] !== ctx.cfg.issuer2) {
          return ctx.fail(`issuer fields [${ctx.cfg.issuer}, ${ctx.cfg.issuer2}]`, JSON.stringify(issuers), { req: listReq, raw: clip(list.text) });
        }
        const cleared = await ctx.api.scenarioClear();
        if (!cleared.json || cleared.json.queueDepth !== 0) {
          return ctx.fail('DELETE → depth 0', `depth ${cleared.json && cleared.json.queueDepth}`, { req: { method: 'DELETE', url: mockUrl(ctx, 'scenarios') }, raw: clip(cleared.text) });
        }
        return ctx.pass({ expected: 'depth 2 (issuer fields) → 0', actual: `[${issuers.join(', ')}] → 0`, detail: { req: [req, listReq] } });
      },
    },
    {
      name: 'capture list: token request appears with method/path',
      async run(ctx) {
        await ctx.api.requestsClear();
        const form = { grant_type: 'client_credentials', client_id: 'capture-list-probe', client_secret: 'x' };
        const tokReq = { method: 'POST', url: issuerUrl(ctx, ctx.cfg.issuer, 'token'), body: form };
        await ctx.api.token(ctx.cfg.issuer, form);
        const list = await ctx.api.requestsList({ issuer: ctx.cfg.issuer, endpoint: 'token' });
        const listReq = { method: 'GET', url: `${ctx.api.base}/_mock/requests?issuer=${ctx.cfg.issuer}&endpoint=token` };
        if (!list.json || typeof list.json.count !== 'number' || list.json.count < 1) {
          return ctx.fail('count >= 1', `count ${list.json && list.json.count}`, { req: [tokReq, listReq], raw: clip(list.text) });
        }
        const wantPath = `/${ctx.cfg.issuer}/token`;
        const entry = (list.json.requests || []).find((e) => e.method === 'POST' && e.path === wantPath);
        if (!entry) {
          const got = (list.json.requests || []).map((e) => `${e.method} ${e.path}`).join(', ');
          return ctx.fail(`POST ${wantPath}`, got || '(no entries)', { req: [tokReq, listReq], raw: clip(list.text) });
        }
        // Leave the log clean for the next check.
        await ctx.api.requestsClear();
        return ctx.pass({ expected: `count>=1, POST ${wantPath}`, actual: `count=${list.json.count}, POST ${entry.path}`, detail: { req: [tokReq, listReq] } });
      },
    },
    {
      name: 'capture take: bodyBase64 is byte-exact',
      async run(ctx) {
        await ctx.api.requestsClear();
        // Raw body with a caller-controlled byte order, sent via api.raw so no
        // helper re-encodes it; the capture must round-trip these exact bytes.
        const exact = 'grant_type=client_credentials&client_id=take-probe&x_marker=exact-bytes-9f3a&scope=alpha+beta';
        const path = `/${ctx.cfg.issuer}/token`;
        const rawReq = { method: 'POST', url: `${ctx.api.base}${path}`, body: exact };
        await ctx.api.raw('POST', path, { headers: { 'Content-Type': 'application/x-www-form-urlencoded' }, body: exact });
        const takeBody = { timeoutMs: 3000, issuer: ctx.cfg.issuer, endpoint: 'token' };
        const takeReq = { method: 'POST', url: mockUrl(ctx, 'requests/take'), body: takeBody };
        const t = await ctx.api.requestsTake(takeBody);
        if (t.status !== 200 || !t.json || !t.json.bodyBase64) {
          return ctx.fail('take 200 with bodyBase64', String(t.status), { req: [rawReq, takeReq], raw: clip(t.text) });
        }
        let decoded;
        try {
          decoded = atob(t.json.bodyBase64);
        } catch (e) {
          return ctx.fail('decodable base64 body', 'atob failed: ' + ((e && e.message) || e), { req: [rawReq, takeReq], raw: clip(t.text) });
        }
        if (decoded !== exact) {
          return ctx.fail(exact, decoded, { req: [rawReq, takeReq], raw: clip(t.text) });
        }
        return ctx.pass({ expected: 'atob(bodyBase64) === exact body', actual: 'byte-exact match', detail: { req: [rawReq, takeReq], note: exact } });
      },
    },
    {
      name: 'capture take on empty → 404',
      async run(ctx) {
        await ctx.api.requestsClear();
        const emptyIssuer = ctx.cfg.issuer + '-empty';
        const body = { timeoutMs: 500, issuer: emptyIssuer, endpoint: 'revoke' };
        const req = { method: 'POST', url: mockUrl(ctx, 'requests/take'), body };
        const t = await ctx.api.requestsTake(body);
        if (t.status !== 404) {
          return ctx.fail('404 (clean miss)', String(t.status || 'network error'), { req, raw: clip(t.text) });
        }
        return ctx.pass({ expected: '404 on empty match', actual: '404', detail: { req } });
      },
    },
    {
      name: 'self-isolation: control-plane calls are never captured',
      async run(ctx) {
        await ctx.api.requestsClear();
        // Exercise the control plane; none of these must show up in the log.
        await ctx.api.clockGet();
        await ctx.api.scenarioList();
        const list = await ctx.api.requestsList({});
        const req = { method: 'GET', url: mockUrl(ctx, 'requests') };
        if (!list.json || !Array.isArray(list.json.requests)) {
          return ctx.fail('requests array', clip(list.text), { req, raw: clip(list.text) });
        }
        const leaked = list.json.requests.filter((e) => typeof e.path === 'string' && e.path.startsWith('/_mock'));
        if (leaked.length) {
          return ctx.fail('no /_mock paths captured', leaked.map((e) => e.path).join(', '), { req, raw: clip(list.text) });
        }
        return ctx.pass({ expected: 'no /_mock entries', actual: `${list.json.count} entries, 0 under /_mock`, detail: { req } });
      },
    },
    {
      name: 'clock freeze: token iat pins to the frozen instant',
      async run(ctx) {
        const setReq = { method: 'PUT', url: mockUrl(ctx, 'clock'), body: { frozen: true, instant: FROZEN_INSTANT } };
        const set = await ctx.api.clockSet({ frozen: true, instant: FROZEN_INSTANT });
        if (set.status !== 200 || !set.json || set.json.frozen !== true) {
          return restoreClock(ctx, ctx.fail('clock frozen', `${set.status} frozen=${set.json && set.json.frozen}`, { req: setReq, raw: clip(set.text) }));
        }
        const mintBody = { issuer: ctx.cfg.issuer, subject: 'frozen' };
        const mintReq = { method: 'POST', url: mockUrl(ctx, 'mint'), body: mintBody };
        const m = await ctx.api.mint(mintBody);
        const dec = safeDecode(ctx, m.json && m.json.token);
        if (!dec) {
          return restoreClock(ctx, ctx.fail('minted token', `status ${m.status}`, { req: [setReq, mintReq], raw: clip(m.text) }));
        }
        if (dec.payload.iat !== FROZEN_EPOCH) {
          return restoreClock(ctx, ctx.fail(String(FROZEN_EPOCH), String(dec.payload.iat), { req: [setReq, mintReq], raw: clip(m.text) }));
        }
        return restoreClock(ctx, ctx.pass({ expected: `iat === ${FROZEN_EPOCH}`, actual: `iat === ${dec.payload.iat}`, detail: { req: [setReq, mintReq] } }));
      },
    },
    {
      name: 'clock advance flips token expiry (introspect + userinfo)',
      async run(ctx) {
        const setReq = { method: 'PUT', url: mockUrl(ctx, 'clock'), body: { frozen: true, instant: FROZEN_INSTANT } };
        const set = await ctx.api.clockSet({ frozen: true, instant: FROZEN_INSTANT });
        if (set.status !== 200 || set.json.frozen !== true) {
          return restoreClock(ctx, ctx.fail('clock frozen', String(set.status), { req: setReq, raw: clip(set.text) }));
        }
        // Fresh token, default 3600s lifetime → exp = frozen + 3600.
        const mintBody = { issuer: ctx.cfg.issuer, subject: 'expiry-flip' };
        const m = await ctx.api.mint(mintBody);
        const token = m.json && m.json.token;
        const mintReq = { method: 'POST', url: mockUrl(ctx, 'mint'), body: mintBody };
        if (!token) {
          return restoreClock(ctx, ctx.fail('minted token', `status ${m.status}`, { req: [setReq, mintReq], raw: clip(m.text) }));
        }
        const before = await ctx.api.introspect(ctx.cfg.issuer, token);
        const introReq = { method: 'POST', url: issuerUrl(ctx, ctx.cfg.issuer, 'introspect'), body: 'token=<minted>' };
        if (!before.json || before.json.active !== true) {
          return restoreClock(ctx, ctx.fail('introspect active:true before advance', `active=${before.json && before.json.active}`, { req: [mintReq, introReq], raw: clip(before.text) }));
        }
        // Advance past the 3600s lifetime.
        const advReq = { method: 'POST', url: mockUrl(ctx, 'clock/advance'), body: { duration: '2h' } };
        const adv = await ctx.api.clockAdvance('2h');
        if (adv.status !== 200) {
          return restoreClock(ctx, ctx.fail('advance 200', String(adv.status), { req: advReq, raw: clip(adv.text) }));
        }
        const after = await ctx.api.introspect(ctx.cfg.issuer, token);
        if (!after.json || after.json.active !== false) {
          return restoreClock(ctx, ctx.fail('introspect active:false after advance', `active=${after.json && after.json.active}`, { req: [advReq, introReq], raw: clip(after.text) }));
        }
        const ui = await ctx.api.userinfo(ctx.cfg.issuer, token);
        const uiReq = { method: 'GET', url: issuerUrl(ctx, ctx.cfg.issuer, 'userinfo'), note: 'Bearer <expired>' };
        if (ui.status !== 401) {
          return restoreClock(ctx, ctx.fail('userinfo 401 after advance', String(ui.status), { req: uiReq, raw: clip(ui.text) }));
        }
        return restoreClock(ctx, ctx.pass({
          expected: 'active true→false, userinfo 401',
          actual: 'expiry flipped as expected',
          detail: { req: [mintReq, introReq, advReq, uiReq] },
        }));
      },
    },
    {
      name: 'clock unfreeze: frozen=false and a fresh token verifies',
      async run(ctx) {
        const setReq = { method: 'PUT', url: mockUrl(ctx, 'clock'), body: { frozen: false } };
        const set = await ctx.api.clockSet({ frozen: false });
        if (set.status !== 200 || !set.json || set.json.frozen !== false) {
          return ctx.fail('set frozen false', `${set.status} frozen=${set.json && set.json.frozen}`, { req: setReq, raw: clip(set.text) });
        }
        const get = await ctx.api.clockGet();
        const getReq = { method: 'GET', url: mockUrl(ctx, 'clock') };
        if (!get.json || get.json.frozen !== false) {
          return ctx.fail('clockGet frozen false', `frozen=${get.json && get.json.frozen}`, { req: getReq, raw: clip(get.text) });
        }
        // Sanity: a token minted at real time still verifies against JWKS.
        const m = await ctx.api.mint({ issuer: ctx.cfg.issuer, subject: 'unfrozen' });
        const jwks = await ctx.api.jwks(ctx.cfg.issuer);
        const ok = m.json && jwks.json && (await ctx.jwt.verify(m.json.token, jwks.json));
        if (!ok) {
          return ctx.fail('fresh token verifies', 'did not verify', { req: [getReq, { method: 'POST', url: mockUrl(ctx, 'mint') }], raw: clip(m.text) });
        }
        return ctx.pass({ expected: 'frozen false + token verifies', actual: `frozen=${get.json.frozen}, verified`, detail: { req: [setReq, getReq] } });
      },
    },
    {
      name: 'reset: clears scenarios+captures, unfreezes, KEEPS keys',
      async run(ctx) {
        await ctx.api.scenarioClear();
        await ctx.api.requestsClear();
        // Freeze so the post-reset unfreeze is observable.
        await ctx.api.clockSet({ frozen: true, instant: FROZEN_INSTANT });
        // A pending scenario for issuer2 (head-blocked, not consumed below) and a
        // captured request for the working issuer.
        await ctx.api.scenarioEnqueue({ issuer: ctx.cfg.issuer2, claims: { reset_marker: 'x' } });
        await ctx.api.token(ctx.cfg.issuer, ccForm(ctx));
        // Snapshot JWKS BEFORE the reset — keys must survive it.
        const jwksBefore = await ctx.api.jwks(ctx.cfg.issuer);
        if (!jwksBefore.json || !Array.isArray(jwksBefore.json.keys) || !jwksBefore.json.keys.length) {
          return restoreClock(ctx, ctx.fail('pre-reset JWKS present', clip(jwksBefore.text), { req: { method: 'GET', url: issuerUrl(ctx, ctx.cfg.issuer, 'jwks') }, raw: clip(jwksBefore.text) }));
        }

        const resetReq = { method: 'POST', url: mockUrl(ctx, 'reset') };
        const reset = await ctx.api.reset();
        if (reset.status !== 200) {
          return restoreClock(ctx, ctx.fail('reset 200', String(reset.status), { req: resetReq, raw: clip(reset.text) }));
        }
        const scenarios = await ctx.api.scenarioList();
        if (!scenarios.json || scenarios.json.queueDepth !== 0) {
          return restoreClock(ctx, ctx.fail('scenarios depth 0', `depth ${scenarios.json && scenarios.json.queueDepth}`, { req: { method: 'GET', url: mockUrl(ctx, 'scenarios') }, raw: clip(scenarios.text) }));
        }
        const requests = await ctx.api.requestsList({});
        if (!requests.json || requests.json.count !== 0) {
          return restoreClock(ctx, ctx.fail('requests count 0', `count ${requests.json && requests.json.count}`, { req: { method: 'GET', url: mockUrl(ctx, 'requests') }, raw: clip(requests.text) }));
        }
        const clock = await ctx.api.clockGet();
        if (!clock.json || clock.json.frozen !== false) {
          return restoreClock(ctx, ctx.fail('clock unfrozen', `frozen=${clock.json && clock.json.frozen}`, { req: { method: 'GET', url: mockUrl(ctx, 'clock') }, raw: clip(clock.text) }));
        }
        // A NEW token verifies against the PRE-reset JWKS copy → keys preserved.
        const m = await ctx.api.mint({ issuer: ctx.cfg.issuer, subject: 'post-reset' });
        const ok = m.json && (await ctx.jwt.verify(m.json.token, jwksBefore.json));
        if (!ok) {
          return restoreClock(ctx, ctx.fail('new token verifies vs pre-reset JWKS', 'did not verify (keys rotated?)', { req: [resetReq, { method: 'POST', url: mockUrl(ctx, 'mint') }], raw: clip(m.text) }));
        }
        return restoreClock(ctx, ctx.pass({
          expected: 'scenarios 0, requests 0, unfrozen, keys kept',
          actual: 'reset cleared state and preserved keys',
          detail: { req: [resetReq] },
        }));
      },
    },
  ],
};
