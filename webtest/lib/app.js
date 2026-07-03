// app.js — console bootstrap: config panel, startup probe, module loading,
// section rendering, run-all, tally, and exports. Loaded by index.html as a
// module.

import { loadCfg, saveCfg, CFG_FIELDS } from './store.js';
import { makeApi, newState } from './api.js';
import * as jwt from './jwt.js';
import * as jose from './jose.js';
import * as pkce from './pkce.js';
import * as form from './form.js';
import { loadModules } from './modules.js';
import { createSectionCard, runAll, computeTally, buildExport, buildMarkdown } from './runner.js';

let cfg = loadCfg();
let cards = [];

// makeCtx builds a fresh ctx from the current config so config-panel edits take
// effect on the next run without a reload. See the module contract in README.md.
function makeCtx() {
  const api = makeApi(cfg);
  return {
    cfg,
    api,
    jwt,
    jose,
    pkce,
    form,
    newState,
    authCode: api.authCode,
    pass: (extra = {}) => ({ status: 'PASS', ...extra }),
    fail: (expected, actual, detail) => ({ status: 'FAIL', expected, actual, detail }),
    skip: (reason) => ({ status: 'SKIP', detail: { note: reason } }),
  };
}

const getCtx = () => makeCtx();

function bindConfig() {
  const panel = document.getElementById('config-panel');
  for (const field of CFG_FIELDS) {
    const input = panel.querySelector(`#cfg-${field.key}`);
    if (!input) continue;
    input.value = cfg[field.key] ?? '';
    input.addEventListener('input', () => {
      cfg = { ...cfg, [field.key]: input.value };
      saveCfg(cfg);
    });
    input.addEventListener('change', () => { probe(); });
  }
}

async function probe() {
  const banner = document.getElementById('status-banner');
  if (!banner) return;
  banner.className = 'banner probing';
  banner.textContent = 'Checking control plane…';
  const res = await getCtx().api.clockGet();
  if (res.status === 200) {
    banner.className = 'banner ok';
    banner.textContent = 'Control plane reachable — GET /_mock/clock → 200';
  } else {
    banner.className = 'banner err';
    const why = res.networkError ? 'network error' : `HTTP ${res.status}`;
    banner.textContent = `Control plane NOT reachable — GET /_mock/clock → ${why}. Check the Base URL and that the server is running.`;
  }
}

function updateTally() {
  const el = document.getElementById('tally');
  if (!el) return;
  const sections = cards.map((c) => c.collectChecks());
  const manual = cards.flatMap((c) => c.collectManual());
  const t = computeTally(sections, manual);
  el.innerHTML = '';
  el.append(
    tallyPill('pass', `PASS ${t.pass}`),
    tallyPill('fail', `FAIL ${t.fail}`),
    tallyPill('skip', `SKIP ${t.skip}`),
  );
  const of = document.createElement('span');
  of.className = 'muted';
  of.textContent = `of ${t.total}`;
  el.append(of);
}

function tallyPill(cls, text) {
  const span = document.createElement('span');
  span.className = 'pill ' + cls;
  span.textContent = text;
  return span;
}

function showExport(text) {
  const pre = document.getElementById('export-output');
  if (pre) pre.textContent = text;
}

function download(name, text, type) {
  const blob = new Blob([text], { type });
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = name;
  document.body.append(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

function wireControls() {
  const runAllBtn = document.getElementById('run-all');
  runAllBtn.addEventListener('click', async () => {
    runAllBtn.disabled = true;
    runAllBtn.textContent = 'Running…';
    try {
      await runAll(cards);
    } finally {
      runAllBtn.disabled = false;
      runAllBtn.textContent = 'Run all';
      updateTally();
    }
  });

  document.getElementById('export-json').addEventListener('click', () => {
    const text = JSON.stringify(buildExport(cfg, cards), null, 2);
    showExport(text);
    download('webtest-results.json', text, 'application/json');
  });

  document.getElementById('export-md').addEventListener('click', () => {
    const text = buildMarkdown(buildExport(cfg, cards));
    showExport(text);
    download('webtest-results.md', text, 'text/markdown');
  });
}

async function init() {
  bindConfig();
  wireControls();
  probe();

  const mods = await loadModules();
  const container = document.getElementById('sections');
  container.innerHTML = '';
  cards = mods.map((mod) => {
    const card = createSectionCard(mod, getCtx, updateTally);
    container.append(card.el);
    return card;
  });

  if (!mods.length) {
    const note = document.createElement('p');
    note.className = 'muted';
    note.textContent = 'No section modules loaded yet.';
    container.append(note);
  }

  updateTally();
}

init();
