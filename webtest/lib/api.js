// api.js — base-aware request layer for the mock OIDC server.
//
// Every helper returns a normalized envelope:
//   { status, headers(Headers), json?, text?, finalUrl?, params?, networkError? }
// so section checks never touch fetch directly and never have to unwrap a
// Response. Control-plane (/_mock/*) calls attach X-Mock-Control-Token when the
// config supplies one. Network failures resolve (status 0) instead of throwing.

import { encode } from './form.js';
import { verifier as pkceVerifier, challengeS256 } from './pkce.js';

// newState returns a random hex token, used for OAuth `state` and as a generic
// nonce/id source. Exported so app.js can expose the same generator on ctx.
export function newState() {
  const arr = new Uint8Array(16);
  crypto.getRandomValues(arr);
  return Array.from(arr, (b) => b.toString(16).padStart(2, '0')).join('');
}

// parseParamsFromUrl merges the query and fragment of a URL into one flat object.
// Used to read authorize/endsession outcomes off response.url after a followed
// redirect (fetch cannot read a 302 Location directly).
function parseParamsFromUrl(u) {
  const out = {};
  try {
    const url = new URL(u);
    for (const [k, v] of url.searchParams) out[k] = v;
    if (url.hash && url.hash.length > 1) {
      const frag = new URLSearchParams(url.hash.replace(/^#/, ''));
      for (const [k, v] of frag) out[k] = v;
    }
  } catch {
    // leave out empty on an unparseable URL
  }
  return out;
}

async function envelope(res) {
  const text = await res.text();
  let json;
  try {
    json = JSON.parse(text);
  } catch {
    json = undefined;
  }
  return { status: res.status, headers: res.headers, text, json, finalUrl: res.url };
}

async function send(url, init) {
  try {
    const res = await fetch(url, init);
    return await envelope(res);
  } catch (e) {
    return {
      status: 0,
      headers: new Headers(),
      text: String((e && e.message) || e),
      json: undefined,
      finalUrl: url,
      networkError: true,
    };
  }
}

async function sendFollow(url, init) {
  try {
    const res = await fetch(url, { ...init, redirect: 'follow' });
    const env = await envelope(res);
    env.finalUrl = res.url;
    env.params = parseParamsFromUrl(res.url);
    return env;
  } catch (e) {
    return {
      status: 0,
      headers: new Headers(),
      text: String((e && e.message) || e),
      json: undefined,
      finalUrl: url,
      params: {},
      networkError: true,
    };
  }
}

// makeApi builds the request layer bound to a config snapshot. app.js rebuilds it
// per run so config-panel edits take effect immediately.
export function makeApi(cfg) {
  const origin = typeof location !== 'undefined' && location.origin ? location.origin : 'http://localhost:18080';
  const base = String(cfg.base || origin).replace(/\/+$/, '');
  const controlToken = cfg.controlToken || '';

  function ctrlHeaders(json) {
    const h = {};
    if (json) h['Content-Type'] = 'application/json';
    if (controlToken) h['X-Mock-Control-Token'] = controlToken;
    return h;
  }

  const api = {
    base,

    discovery(issuer, { oauth = false } = {}) {
      const doc = oauth ? '.well-known/oauth-authorization-server' : '.well-known/openid-configuration';
      return send(`${base}/${issuer}/${doc}`, { method: 'GET' });
    },

    jwks(issuer) {
      return send(`${base}/${issuer}/jwks`, { method: 'GET' });
    },

    authorizeFollow(issuer, params = {}) {
      const p = { ...params };
      if (!p.redirect_uri) p.redirect_uri = `${base}/static/callback.html`;
      return sendFollow(`${base}/${issuer}/authorize?${encode(p)}`, { method: 'GET' });
    },

    token(issuer, formObj = {}, { basicAuth } = {}) {
      const headers = { 'Content-Type': 'application/x-www-form-urlencoded' };
      if (basicAuth) headers['Authorization'] = 'Basic ' + btoa(`${basicAuth.user}:${basicAuth.pass}`);
      return send(`${base}/${issuer}/token`, { method: 'POST', headers, body: encode(formObj) });
    },

    userinfo(issuer, bearer) {
      const headers = {};
      if (bearer) headers['Authorization'] = 'Bearer ' + bearer;
      return send(`${base}/${issuer}/userinfo`, { method: 'GET', headers });
    },

    introspect(issuer, token, { hint, auth = 'Bearer x' } = {}) {
      const headers = { 'Content-Type': 'application/x-www-form-urlencoded' };
      if (auth) headers['Authorization'] = auth;
      const body = { token };
      if (hint) body.token_type_hint = hint;
      return send(`${base}/${issuer}/introspect`, { method: 'POST', headers, body: encode(body) });
    },

    revoke(issuer, token, { hint } = {}) {
      const headers = { 'Content-Type': 'application/x-www-form-urlencoded' };
      const body = { token };
      if (hint) body.token_type_hint = hint;
      return send(`${base}/${issuer}/revoke`, { method: 'POST', headers, body: encode(body) });
    },

    endsessionFollow(issuer, params = {}) {
      const qs = encode(params);
      return sendFollow(`${base}/${issuer}/endsession${qs ? '?' + qs : ''}`, { method: 'GET' });
    },

    mint(body) {
      return send(`${base}/_mock/mint`, { method: 'POST', headers: ctrlHeaders(true), body: JSON.stringify(body || {}) });
    },
    scenarioEnqueue(body) {
      return send(`${base}/_mock/scenarios`, { method: 'POST', headers: ctrlHeaders(true), body: JSON.stringify(body || {}) });
    },
    scenarioList() {
      return send(`${base}/_mock/scenarios`, { method: 'GET', headers: ctrlHeaders(false) });
    },
    scenarioClear() {
      return send(`${base}/_mock/scenarios`, { method: 'DELETE', headers: ctrlHeaders(false) });
    },
    requestsTake(body) {
      return send(`${base}/_mock/requests/take`, { method: 'POST', headers: ctrlHeaders(true), body: JSON.stringify(body || {}) });
    },
    requestsList(query = {}) {
      const qs = encode(query);
      return send(`${base}/_mock/requests${qs ? '?' + qs : ''}`, { method: 'GET', headers: ctrlHeaders(false) });
    },
    requestsClear() {
      return send(`${base}/_mock/requests`, { method: 'DELETE', headers: ctrlHeaders(false) });
    },
    clockGet() {
      return send(`${base}/_mock/clock`, { method: 'GET', headers: ctrlHeaders(false) });
    },
    clockSet(body) {
      return send(`${base}/_mock/clock`, { method: 'PUT', headers: ctrlHeaders(true), body: JSON.stringify(body || {}) });
    },
    clockAdvance(duration) {
      return send(`${base}/_mock/clock/advance`, { method: 'POST', headers: ctrlHeaders(true), body: JSON.stringify({ duration }) });
    },
    reset() {
      return send(`${base}/_mock/reset`, { method: 'POST', headers: ctrlHeaders(true), body: '{}' });
    },

    raw(method, path, { headers = {}, body } = {}) {
      return send(`${base}${path}`, { method, headers, body });
    },

    // authCode drives a full authorize→code follow and returns a fresh code.
    // Throws a ctx-fail-able Error (with .detail) when no code comes back so
    // sections can surface the authorize outcome.
    async authCode(issuer, { scope = 'openid profile', nonce, state, pkce = false, extra = {} } = {}) {
      const st = state !== undefined ? state : newState();
      const params = { response_type: 'code', client_id: cfg.clientId, scope, ...extra };
      if (st) params.state = st;
      if (nonce) params.nonce = nonce;
      let verifier;
      if (pkce) {
        verifier = pkceVerifier();
        params.code_challenge = await challengeS256(verifier);
        params.code_challenge_method = 'S256';
      }
      const res = await api.authorizeFollow(issuer, params);
      const code = res.params && res.params.code;
      if (!code) {
        const err = new Error(
          'authCode: no authorization code (' + ((res.params && res.params.error) || ('status ' + res.status)) + ')',
        );
        err.detail = { req: { finalUrl: res.finalUrl }, raw: res.text, note: JSON.stringify(res.params || {}) };
        throw err;
      }
      return { code, state: res.params.state, verifier };
    },
  };

  return api;
}

export default { makeApi, newState };
