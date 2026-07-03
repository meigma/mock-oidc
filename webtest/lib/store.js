// store.js — localStorage-backed console configuration.
//
// The console reads a single config object from localStorage and rebuilds the
// request layer (lib/api.js) from it on every run, so edits in the config panel
// take effect without a reload.

const KEY = 'webtest:cfg';

// CFG_FIELDS drives both the config-panel inputs (app.js renders one field per
// entry) and the defaults. `key` is the config property; the input element id is
// `cfg-<key>`.
export const CFG_FIELDS = [
  { key: 'base', label: 'Base URL', placeholder: 'http://localhost:18080' },
  { key: 'issuer', label: 'Issuer', placeholder: 'acme' },
  { key: 'issuer2', label: 'Issuer 2 (isolation)', placeholder: 'beta' },
  { key: 'configuredIssuer', label: 'Configured issuer', placeholder: 'configured' },
  { key: 'clientId', label: 'Client ID', placeholder: 'web-client' },
  { key: 'controlToken', label: 'Control token', placeholder: '(usually empty)' },
];

// defaults returns a fresh config with every field at its ground-truth default.
// base defaults to the page origin so a console served from the mock server just
// works.
export function defaults() {
  const origin = typeof location !== 'undefined' && location.origin ? location.origin : 'http://localhost:18080';
  return {
    base: origin,
    issuer: 'acme',
    issuer2: 'beta',
    configuredIssuer: 'configured',
    clientId: 'web-client',
    controlToken: '',
  };
}

// loadCfg merges any persisted overrides over the defaults. A corrupt or absent
// blob falls back to defaults so the console never fails to boot.
export function loadCfg() {
  const base = defaults();
  try {
    const raw = localStorage.getItem(KEY);
    if (raw) {
      const parsed = JSON.parse(raw);
      if (parsed && typeof parsed === 'object') return { ...base, ...parsed };
    }
  } catch {
    // ignore — fall through to defaults
  }
  return base;
}

// saveCfg persists the whole config object.
export function saveCfg(cfg) {
  try {
    localStorage.setItem(KEY, JSON.stringify(cfg));
  } catch {
    // storage may be unavailable (private mode); the console still runs from the
    // in-memory cfg.
  }
}
