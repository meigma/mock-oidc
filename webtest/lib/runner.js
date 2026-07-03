// runner.js — render section cards, run checks, tally, and build exports.
//
// A section card shows an optional mount area, a results table (Check | Status |
// Expected | Actual | Detail), and any manual checks. Run-all runs sections
// sequentially, and checks within a section sequentially, so clock/scenario
// state set by one check never races another.

import { renderManual, collectManual } from './manual.js';

// fmt renders an expected/actual value: strings pass through, everything else is
// pretty JSON. undefined/null become ''.
function fmt(v) {
  if (v === undefined || v === null) return '';
  if (typeof v === 'string') return v;
  try {
    return JSON.stringify(v, null, 2);
  } catch {
    return String(v);
  }
}

function pill(status) {
  const span = document.createElement('span');
  span.className = 'pill ' + String(status || 'pending').toLowerCase();
  span.textContent = status || 'PENDING';
  return span;
}

function truncate(text, n = 240) {
  if (text.length <= n) return text;
  return text.slice(0, n) + ' …';
}

// normalizeResult guards against a check that returned nothing or a non-result.
function normalizeResult(result, err) {
  if (err) {
    return {
      status: 'FAIL',
      expected: 'run() to complete',
      actual: 'threw: ' + ((err && err.message) || err),
      detail: err && err.detail ? err.detail : { note: String((err && err.stack) || err) },
    };
  }
  if (!result || typeof result !== 'object' || !result.status) {
    return { status: 'FAIL', expected: 'a CheckResult', actual: fmt(result) };
  }
  return result;
}

// createSectionCard builds one section's DOM and returns handles the console uses
// to run it and collect results. onChange fires after every result/verdict so the
// caller can refresh the global tally.
export function createSectionCard(mod, getCtx, onChange) {
  const results = new Map(); // check name -> CheckResult

  const el = document.createElement('section');
  el.className = 'section-card';
  el.dataset.sectionId = mod.id;

  const head = document.createElement('div');
  head.className = 'section-head';
  const h2 = document.createElement('h2');
  h2.textContent = mod.title;
  const runBtn = document.createElement('button');
  runBtn.className = 'run-section';
  runBtn.textContent = 'Run section';
  const mini = document.createElement('span');
  mini.className = 'mini-tally';
  head.append(h2, runBtn, mini);
  el.append(head);

  if (typeof mod.mount === 'function') {
    const mountEl = document.createElement('div');
    mountEl.className = 'section-mount';
    try {
      mod.mount(mountEl, getCtx());
    } catch (e) {
      mountEl.textContent = 'mount error: ' + ((e && e.message) || e);
    }
    el.append(mountEl);
  }

  const checks = Array.isArray(mod.checks) ? mod.checks : [];
  const rowRefs = new Map(); // check name -> { statusCell, expectedCell, actualCell, detailPre, detailRow }

  const table = document.createElement('table');
  table.className = 'results';
  const thead = document.createElement('thead');
  thead.innerHTML = '<tr><th>Check</th><th>Status</th><th>Expected</th><th>Actual</th><th>Detail</th></tr>';
  table.append(thead);
  const tbody = document.createElement('tbody');
  table.append(tbody);

  for (const chk of checks) {
    const tr = document.createElement('tr');
    tr.className = 'check-row';

    const nameCell = document.createElement('td');
    nameCell.className = 'c-name';
    nameCell.textContent = chk.name;

    const statusCell = document.createElement('td');
    statusCell.className = 'c-status';
    statusCell.append(pill('PENDING'));

    const expectedCell = document.createElement('td');
    expectedCell.className = 'c-expected mono';

    const actualCell = document.createElement('td');
    actualCell.className = 'c-actual mono';

    const detailCell = document.createElement('td');
    detailCell.className = 'c-detail';
    const detailBtn = document.createElement('button');
    detailBtn.className = 'detail-toggle';
    detailBtn.textContent = 'detail';
    detailCell.append(detailBtn);

    tr.append(nameCell, statusCell, expectedCell, actualCell, detailCell);

    const detailRow = document.createElement('tr');
    detailRow.className = 'detail-row';
    detailRow.hidden = true;
    const detailTd = document.createElement('td');
    detailTd.colSpan = 5;
    const detailPre = document.createElement('pre');
    detailPre.className = 'detail-pre';
    detailPre.textContent = '(run the check to see details)';
    detailTd.append(detailPre);
    detailRow.append(detailTd);

    detailBtn.addEventListener('click', () => {
      detailRow.hidden = !detailRow.hidden;
    });

    tbody.append(tr, detailRow);
    rowRefs.set(chk.name, { statusCell, expectedCell, actualCell, detailPre });
  }
  el.append(table);

  let manualEl = null;
  if (mod.manual && mod.manual.length) {
    manualEl = renderManual(mod, getCtx, onChange);
    el.append(manualEl);
  }

  function renderRow(chk, result) {
    const refs = rowRefs.get(chk.name);
    if (!refs) return;
    refs.statusCell.textContent = '';
    refs.statusCell.append(pill(result.status));
    refs.expectedCell.textContent = truncate(fmt(result.expected));
    refs.expectedCell.title = fmt(result.expected);
    refs.actualCell.textContent = truncate(fmt(result.actual));
    refs.actualCell.title = fmt(result.actual);
    refs.detailPre.textContent = renderDetail(result);
  }

  function renderDetail(result) {
    const d = result.detail || {};
    const parts = [];
    if (result.expected !== undefined) parts.push('EXPECTED:\n' + fmt(result.expected));
    if (result.actual !== undefined) parts.push('ACTUAL:\n' + fmt(result.actual));
    if (d.note) parts.push('NOTE:\n' + fmt(d.note));
    if (d.req) parts.push('REQUEST:\n' + fmt(d.req));
    if (d.raw !== undefined) parts.push('RAW:\n' + fmt(d.raw));
    return parts.length ? parts.join('\n\n') : '(no detail)';
  }

  function updateMini() {
    const t = tallyChecks(collectChecks().checks);
    mini.textContent = `${t.pass}✓ ${t.fail}✗ ${t.skip}∅`;
    mini.className = 'mini-tally' + (t.fail ? ' has-fail' : '');
  }

  async function runOne(chk) {
    const ctx = getCtx();
    let result;
    try {
      result = normalizeResult(await chk.run(ctx));
    } catch (e) {
      result = normalizeResult(null, e);
    }
    results.set(chk.name, result);
    renderRow(chk, result);
    updateMini();
    if (onChange) onChange();
    return result;
  }

  async function run() {
    runBtn.disabled = true;
    try {
      for (const chk of checks) {
        // eslint-disable-next-line no-await-in-loop -- checks are intentionally serial
        await runOne(chk);
      }
    } finally {
      runBtn.disabled = false;
    }
    return collectChecks();
  }

  function collectChecks() {
    return {
      id: mod.id,
      title: mod.title,
      checks: checks.map((c) => {
        const r = results.get(c.name) || { status: 'SKIP', detail: { note: 'not run' } };
        return { name: c.name, status: r.status, expected: r.expected, actual: r.actual, detail: r.detail };
      }),
    };
  }

  runBtn.addEventListener('click', () => { run(); });
  updateMini();

  return {
    el,
    mod,
    run,
    collectChecks,
    collectManual: () => collectManual(mod),
  };
}

// tallyChecks counts statuses over a list of check results.
export function tallyChecks(checks) {
  const t = { pass: 0, fail: 0, skip: 0, total: 0 };
  for (const c of checks || []) {
    t.total++;
    if (c.status === 'PASS') t.pass++;
    else if (c.status === 'FAIL') t.fail++;
    else t.skip++;
  }
  return t;
}

// computeTally aggregates over every section's checks plus every manual verdict.
export function computeTally(sections, manual) {
  const t = { pass: 0, fail: 0, skip: 0, total: 0 };
  const bump = (status) => {
    t.total++;
    if (status === 'PASS') t.pass++;
    else if (status === 'FAIL') t.fail++;
    else t.skip++;
  };
  for (const s of sections || []) for (const c of s.checks || []) bump(c.status);
  for (const m of manual || []) bump(m.status);
  return t;
}

// runAll runs every section (and its checks) sequentially.
export async function runAll(cards) {
  for (const card of cards) {
    // eslint-disable-next-line no-await-in-loop -- sections are intentionally serial
    await card.run();
  }
}

// buildExport assembles the JSON export payload.
export function buildExport(cfg, cards) {
  return {
    startedAt: new Date().toISOString(),
    cfg,
    sections: cards.map((c) => c.collectChecks()),
    manual: cards.flatMap((c) => c.collectManual()),
  };
}

function mdCell(v) {
  return String(fmt(v)).replace(/\|/g, '\\|').replace(/\n+/g, ' ').trim();
}

// buildMarkdown renders the export object as Markdown: an H2 + table + tally per
// section, a manual-checks section, and an overall tally.
export function buildMarkdown(data) {
  const lines = [];
  lines.push('# webtest acceptance results');
  lines.push('');
  lines.push(`Started: ${data.startedAt}`);
  lines.push('');
  lines.push('Config: ' + '`' + JSON.stringify(data.cfg) + '`');
  lines.push('');

  for (const s of data.sections) {
    lines.push(`## ${s.title}`);
    lines.push('');
    lines.push('| Check | Status | Expected | Actual |');
    lines.push('| --- | --- | --- | --- |');
    for (const c of s.checks) {
      lines.push(`| ${mdCell(c.name)} | ${c.status} | ${mdCell(c.expected)} | ${mdCell(c.actual)} |`);
    }
    const t = tallyChecks(s.checks);
    lines.push('');
    lines.push(`Tally: ${t.pass} PASS / ${t.fail} FAIL / ${t.skip} SKIP`);
    lines.push('');
  }

  if (data.manual && data.manual.length) {
    lines.push('## Manual checks');
    lines.push('');
    lines.push('| Section | Check | Status |');
    lines.push('| --- | --- | --- |');
    for (const m of data.manual) {
      lines.push(`| ${mdCell(m.sectionId)} | ${mdCell(m.name)} | ${m.status} |`);
    }
    lines.push('');
  }

  const overall = computeTally(data.sections, data.manual);
  lines.push('## Overall');
  lines.push('');
  lines.push(`${overall.pass} PASS / ${overall.fail} FAIL / ${overall.skip} SKIP (of ${overall.total})`);
  lines.push('');

  return lines.join('\n');
}

export default { createSectionCard, tallyChecks, computeTally, runAll, buildExport, buildMarkdown };
