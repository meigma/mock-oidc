// authorize.js — Authorization Endpoint section.
//
// Automated checks drive GET /authorize via ctx.api.authorizeFollow (fetch
// cannot read a 302 Location, so outcomes are read off the followed
// response.url). Manual checks open real popups so browser-only behaviors —
// fragment mode, form_post auto-submit, the login page, the debugger — are
// exercised the way a real client sees them. Style mirrors sections/discovery.js.

// trunc caps raw response text at ~500 chars for the failure detail pane.
function trunc(text, n = 500) {
  const s = typeof text === 'string' ? text : String(text ?? '');
  return s.length <= n ? s : s.slice(0, n) + ' …';
}

// authorizeUrl builds a GET /authorize URL from a flat param object (manual
// popups navigate to it directly).
function authorizeUrl(ctx, issuer, params) {
  return ctx.api.base + '/' + issuer + '/authorize?' + ctx.form.encode(params);
}

// doAuthorize runs authorizeFollow against the default issuer, defaulting
// redirect_uri to the callback page, and returns the envelope plus a request
// line (the requested URL and the followed finalUrl) for the detail pane.
async function doAuthorize(ctx, params) {
  const issuer = ctx.cfg.issuer;
  const p = { ...params };
  if (!p.redirect_uri) p.redirect_uri = ctx.api.base + '/static/callback.html';
  const url = ctx.api.base + '/' + issuer + '/authorize?' + ctx.form.encode(p);
  const res = await ctx.api.authorizeFollow(issuer, p);
  const req = { method: 'GET', url, finalUrl: res.finalUrl };
  return { res, req };
}

// awaitCallback resolves with the params a real navigation to callback.html
// posts back to this window (matched on state to ignore stale/other windows),
// or rejects after timeoutMs. Used by the popup-driven manual checks.
function awaitCallback({ state, timeoutMs = 60000 } = {}) {
  return new Promise((resolve, reject) => {
    let timer;
    function cleanup() {
      clearTimeout(timer);
      window.removeEventListener('message', onMessage);
    }
    function onMessage(ev) {
      const d = ev && ev.data;
      if (!d || d.type !== 'webtest-callback' || !d.params) return;
      if (state && d.params.state !== state) return; // ignore stale / other flows
      cleanup();
      resolve(d.params);
    }
    window.addEventListener('message', onMessage);
    timer = setTimeout(() => {
      cleanup();
      reject(new Error(
        'timed out after ' + timeoutMs + 'ms waiting for the callback postMessage ' +
        '(popup blocked, closed, or login not completed?)',
      ));
    }, timeoutMs);
  });
}

export default {
  id: 'authorize',
  title: 'Authorization Endpoint',
  checks: [
    {
      name: 'response_type=code with state → code + echoed state on callback',
      async run(ctx) {
        const state = ctx.newState();
        const { res, req } = await doAuthorize(ctx, {
          response_type: 'code',
          client_id: ctx.cfg.clientId,
          scope: 'openid profile',
          state,
        });
        const onCallback = String(res.finalUrl || '').includes('/static/callback.html');
        const code = res.params && res.params.code;
        if (!onCallback) {
          return ctx.fail('redirect to /static/callback.html', String(res.finalUrl), { req, raw: trunc(res.text) });
        }
        if (!code) {
          return ctx.fail('non-empty code param', 'no code (' + JSON.stringify(res.params) + ')', { req, raw: trunc(res.text) });
        }
        if (res.params.state !== state) {
          return ctx.fail('state === ' + state, 'state=' + String(res.params.state), { req, raw: trunc(res.text) });
        }
        return ctx.pass({ expected: 'code + state=' + state, actual: 'code=' + code.slice(0, 8) + '… state=' + res.params.state, detail: { req } });
      },
    },
    {
      name: 'no state param → code present, state omitted from callback',
      async run(ctx) {
        const { res, req } = await doAuthorize(ctx, {
          response_type: 'code',
          client_id: ctx.cfg.clientId,
          scope: 'openid profile',
        });
        const code = res.params && res.params.code;
        if (!code) {
          return ctx.fail('non-empty code param', 'no code (' + JSON.stringify(res.params) + ')', { req, raw: trunc(res.text) });
        }
        if (res.params.state !== undefined) {
          return ctx.fail('no state param', 'state=' + String(res.params.state), { req, raw: trunc(res.text) });
        }
        return ctx.pass({ expected: 'code present, state absent', actual: 'code=' + code.slice(0, 8) + '…, no state', detail: { req } });
      },
    },
    {
      name: 'response_type token/id_token/garbage → error at redirect_uri, no code',
      async run(ctx) {
        const types = ['token', 'id_token', 'garbage'];
        const reqs = [];
        const raws = [];
        const problems = [];
        for (const rt of types) {
          const state = ctx.newState();
          // eslint-disable-next-line no-await-in-loop -- serial keeps the code cache clean
          const { res, req } = await doAuthorize(ctx, {
            response_type: rt,
            client_id: ctx.cfg.clientId,
            scope: 'openid profile',
            state,
          });
          reqs.push(req);
          raws.push(rt + ' → ' + trunc(res.text, 150));
          const err = res.params && res.params.error;
          const code = res.params && res.params.code;
          if (!err) problems.push(rt + ': no error param (params=' + JSON.stringify(res.params) + ', status ' + res.status + ')');
          if (code) problems.push(rt + ': unexpected code present');
        }
        if (problems.length) {
          return ctx.fail('each non-code type → error, no code', problems.join(' | '), { req: reqs, raw: raws.join('\n\n') });
        }
        return ctx.pass({ expected: 'token/id_token/garbage → error, no code', actual: 'all 3 errored to redirect_uri, no code', detail: { req: reqs } });
      },
    },
    {
      name: 'POST /authorize login form without username → 400 invalid_request',
      async run(ctx) {
        // A usable redirect_uri would make the server route the error as a 302
        // into it (which fetch follows to 200), so redirect_uri is omitted here
        // to observe the direct 400 invalid_request page. See notes.
        const issuer = ctx.cfg.issuer;
        const query = ctx.form.encode({ response_type: 'code', client_id: ctx.cfg.clientId });
        const path = '/' + issuer + '/authorize?' + query;
        const res = await ctx.api.raw('POST', path, {
          headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
          body: ctx.form.encode({ claims: '{}' }), // no username field
        });
        const req = { method: 'POST', url: ctx.api.base + path, body: 'claims={} (no username)' };
        if (res.status !== 400) {
          return ctx.fail('400', String(res.status || 'network error'), { req, raw: trunc(res.text) });
        }
        if (!/invalid_request/.test(res.text || '')) {
          return ctx.fail('body contains invalid_request', 'error code not found in body', { req, raw: trunc(res.text) });
        }
        return ctx.pass({ expected: '400 + invalid_request', actual: '400 + invalid_request', detail: { req, raw: trunc(res.text, 200) } });
      },
    },
    {
      name: 'prompt=none → code without rendering a login page',
      async run(ctx) {
        const state = ctx.newState();
        const { res, req } = await doAuthorize(ctx, {
          response_type: 'code',
          client_id: ctx.cfg.clientId,
          scope: 'openid profile',
          state,
          prompt: 'none',
        });
        const code = res.params && res.params.code;
        const onCallback = String(res.finalUrl || '').includes('/static/callback.html');
        const looksLikeLogin = /name=["']?username/i.test(res.text || '');
        if (!code) {
          return ctx.fail('code auto-issued (no login)', 'no code (' + JSON.stringify(res.params) + ')', { req, raw: trunc(res.text) });
        }
        if (!onCallback) {
          return ctx.fail('final document = callback.html', String(res.finalUrl), { req, raw: trunc(res.text) });
        }
        if (looksLikeLogin) {
          return ctx.fail('no login form in final document', 'login form present', { req, raw: trunc(res.text) });
        }
        return ctx.pass({ expected: 'code + callback.html (no login page)', actual: 'code issued, landed on callback.html', detail: { req } });
      },
    },
    {
      name: 'login_hint=admin-alice + prompt=login → headless code; id_token carries the template identity',
      async run(ctx) {
        const state = ctx.newState();
        // prompt=login would normally force the login page; the template hint
        // must win and issue the code headlessly.
        const { res, req } = await doAuthorize(ctx, {
          response_type: 'code',
          client_id: ctx.cfg.clientId,
          scope: 'openid profile',
          state,
          prompt: 'login',
          login_hint: 'admin-alice',
        });
        const code = res.params && res.params.code;
        if (!code) {
          return ctx.fail('headless code despite prompt=login', 'no code (' + JSON.stringify(res.params) + ')', { req, raw: trunc(res.text) });
        }
        const tok = await ctx.api.token(ctx.cfg.issuer, {
          grant_type: 'authorization_code',
          code,
          redirect_uri: ctx.api.base + '/static/callback.html',
          client_id: ctx.cfg.clientId,
        });
        if (tok.status !== 200 || !tok.json || !tok.json.id_token) {
          return ctx.fail('200 + id_token', 'HTTP ' + tok.status, { req, raw: trunc(tok.text) });
        }
        let payload;
        try {
          payload = ctx.jwt.decode(tok.json.id_token).payload;
        } catch (e) {
          return ctx.fail('decodable id_token', String((e && e.message) || e), { req, raw: trunc(tok.text) });
        }
        if (payload.sub !== 'template-alice' || payload.template_marker !== 'from-template') {
          return ctx.fail(
            'sub=template-alice + template_marker=from-template',
            'sub=' + String(payload.sub) + ' template_marker=' + String(payload.template_marker),
            { req, raw: trunc(tok.text) },
          );
        }
        return ctx.pass({
          expected: 'template identity minted headlessly',
          actual: 'sub=' + payload.sub + ', email=' + String(payload.email) + ', marker=' + payload.template_marker,
          detail: { req },
        });
      },
    },
    {
      name: 'login_hint=unknown → error=invalid_request at redirect_uri, no code',
      async run(ctx) {
        const state = ctx.newState();
        const { res, req } = await doAuthorize(ctx, {
          response_type: 'code',
          client_id: ctx.cfg.clientId,
          scope: 'openid profile',
          state,
          login_hint: 'no-such-template',
        });
        const err = res.params && res.params.error;
        const code = res.params && res.params.code;
        if (code) {
          return ctx.fail('no code for an unknown template', 'code issued', { req, raw: trunc(res.text) });
        }
        if (err !== 'invalid_request') {
          return ctx.fail('error=invalid_request', 'error=' + String(err) + ' (params=' + JSON.stringify(res.params) + ')', { req, raw: trunc(res.text) });
        }
        return ctx.pass({ expected: 'invalid_request, no code', actual: 'error=invalid_request delivered to redirect_uri', detail: { req } });
      },
    },
    {
      name: 'GET /favicon.ico → 200',
      async run(ctx) {
        const res = await ctx.api.raw('GET', '/favicon.ico');
        const req = { method: 'GET', url: ctx.api.base + '/favicon.ico' };
        if (res.status !== 200) {
          return ctx.fail('200', String(res.status || 'network error'), { req, raw: trunc(res.text) });
        }
        return ctx.pass({ expected: '200', actual: '200', detail: { req } });
      },
    },
  ],

  manual: [
    {
      id: 'login-page',
      name: 'Interactive login page mints an id_token for the entered identity',
      instructions:
        'Click Start: a popup opens the login page at /{issuer}/authorize with prompt=login ' +
        '(scope openid profile, fresh state+nonce, redirect_uri=/static/callback.html). The page shows a ' +
        'Template dropdown (admin-alice / basic-bob) — pick one and confirm it PRE-FILLS the username and ' +
        'claims fields and that both stay editable. Then enter username alice and Additional claims ' +
        '{"email":"alice@example.com"} (overwriting any pre-fill), and Sign in. The popup lands on ' +
        'callback.html and posts the code back; Start exchanges it at /token and reports the id_token ' +
        'sub + email. PASS when sub is "alice" and email is "alice@example.com".',
      async start(ctx) {
        const base = ctx.api.base;
        const issuer = ctx.cfg.issuer;
        const redirectUri = base + '/static/callback.html';
        const state = ctx.newState();
        const nonce = ctx.newState();
        const url = authorizeUrl(ctx, issuer, {
          response_type: 'code',
          client_id: ctx.cfg.clientId,
          redirect_uri: redirectUri,
          scope: 'openid profile',
          state,
          nonce,
          prompt: 'login',
        });
        const win = window.open(url, 'webtest-authorize', 'width=540,height=680');
        if (!win) throw new Error('popup blocked — allow popups for ' + base + ' and click Start again');
        let params;
        try {
          params = await awaitCallback({ state, timeoutMs: 120000 });
        } finally {
          try { win.close(); } catch { /* ignore */ }
        }
        if (params.error) {
          throw new Error('authorize error: ' + params.error + (params.error_description ? ' — ' + params.error_description : ''));
        }
        if (!params.code) throw new Error('no code in callback params: ' + JSON.stringify(params));
        const tok = await ctx.api.token(issuer, {
          grant_type: 'authorization_code',
          code: params.code,
          redirect_uri: redirectUri,
          client_id: ctx.cfg.clientId,
        });
        if (tok.status !== 200 || !tok.json || !tok.json.id_token) {
          throw new Error('token exchange failed: HTTP ' + tok.status + ' — ' + trunc(tok.text, 300));
        }
        let payload;
        try {
          payload = ctx.jwt.decode(tok.json.id_token).payload;
        } catch (e) {
          throw new Error('could not decode id_token: ' + ((e && e.message) || e));
        }
        const ok = payload.sub === 'alice' && payload.email === 'alice@example.com';
        return {
          result: ok ? 'PASS — sub and email match' : 'REVIEW — compare values below',
          id_token_sub: payload.sub,
          sub_is_alice: payload.sub === 'alice',
          email_claim: payload.email,
          nonce_echoed: payload.nonce === nonce,
          state_echoed: params.state === state,
        };
      },
    },
    {
      id: 'fragment-mode',
      name: 'response_mode=fragment returns code+state in the URL fragment',
      instructions:
        'Click Start: a popup opens /{issuer}/authorize with response_mode=fragment and a fresh state. ' +
        'With interactive login off the server auto-issues the code in the URL fragment; callback.html ' +
        'reads location.hash and posts it back. PASS when Start reports code + matching state arrived via ' +
        'the fragment. (No form to fill.)',
      async start(ctx) {
        const base = ctx.api.base;
        const issuer = ctx.cfg.issuer;
        const redirectUri = base + '/static/callback.html';
        const state = ctx.newState();
        const url = authorizeUrl(ctx, issuer, {
          response_type: 'code',
          client_id: ctx.cfg.clientId,
          redirect_uri: redirectUri,
          scope: 'openid profile',
          state,
          response_mode: 'fragment',
        });
        const win = window.open(url, 'webtest-authorize', 'width=540,height=680');
        if (!win) throw new Error('popup blocked — allow popups for ' + base + ' and click Start again');
        let params;
        try {
          params = await awaitCallback({ state, timeoutMs: 30000 });
        } finally {
          try { win.close(); } catch { /* ignore */ }
        }
        if (!params.code || params.state !== state) {
          throw new Error('fragment params missing/mismatched: ' + JSON.stringify(params));
        }
        return {
          result: 'PASS — code + state arrived via the fragment',
          code: String(params.code).slice(0, 10) + '…',
          state_echoed: params.state === state,
        };
      },
    },
    {
      id: 'form-post-capture',
      name: 'response_mode=form_post self-submits code+state to redirect_uri',
      instructions:
        'Click Start: capture is cleared, then a popup opens /{issuer}/authorize with ' +
        'response_mode=form_post and redirect_uri set to /{issuer}/userinfo. The self-submitting form POSTs ' +
        'code+state to /userinfo, which the control plane captures. Start takes that request and inspects ' +
        'its body. PASS when the captured /userinfo POST body contains "code=" and the generated state.',
      async start(ctx) {
        const base = ctx.api.base;
        const issuer = ctx.cfg.issuer;
        const state = ctx.newState();
        const userinfoUri = base + '/' + issuer + '/userinfo';
        const url = authorizeUrl(ctx, issuer, {
          response_type: 'code',
          client_id: ctx.cfg.clientId,
          redirect_uri: userinfoUri,
          scope: 'openid',
          state,
          response_mode: 'form_post',
        });
        // Open the popup inside the click gesture (blank), THEN clear capture,
        // THEN navigate — so the DELETE lands before the form_post auto-submits.
        const win = window.open('', 'webtest-authorize', 'width=540,height=680');
        if (!win) throw new Error('popup blocked — allow popups for ' + base + ' and click Start again');
        await ctx.api.requestsClear();
        win.location.href = url;
        const taken = await ctx.api.requestsTake({ timeoutMs: 8000, issuer, endpoint: 'userinfo' });
        try { win.close(); } catch { /* ignore */ }
        if (taken.status !== 200 || !taken.json) {
          throw new Error('no captured /userinfo POST within 8s (status ' + taken.status + '). Popup blocked or form_post did not submit?');
        }
        let body = '';
        try { body = atob(taken.json.bodyBase64 || ''); } catch { body = ''; }
        const hasCode = body.includes('code=');
        const hasState = state !== '' && body.includes(state);
        if (!hasCode || !hasState) {
          throw new Error('captured body missing code/state — body=' + trunc(body, 200) + ' expected state=' + state);
        }
        return {
          result: 'PASS — form_post POSTed code+state to /userinfo',
          method: taken.json.method,
          path: taken.json.path,
          body: trunc(body, 200),
          state,
        };
      },
    },
    {
      id: 'debugger-roundtrip',
      name: 'Debugger completes a full authorize→token round-trip',
      instructions:
        'Click Start (opens /{issuer}/debugger in a new window) or open it yourself, then submit the ' +
        'pre-filled debugger form. PASS when the result page reads "Token exchange complete" and shows ' +
        'access_token / id_token / refresh_token.',
      async start(ctx) {
        const base = ctx.api.base;
        const issuer = ctx.cfg.issuer;
        const url = base + '/' + issuer + '/debugger';
        const win = window.open(url, 'webtest-debugger', 'width=760,height=820');
        if (!win) throw new Error('popup blocked — allow popups for ' + base + ' and click Start again');
        return 'Opened ' + url + ' — submit the pre-filled form and confirm the result page reads "Token exchange complete" with tokens.';
      },
    },
  ],
};
