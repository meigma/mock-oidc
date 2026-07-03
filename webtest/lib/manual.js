// manual.js — render and persist manual (human-judged) checks.
//
// Each manual entry gets instructions, an optional Start button (driving
// entry.start(ctx) for setup, e.g. minting a token or opening a flow), and a
// PASS/FAIL/SKIP tri-toggle persisted under
//   webtest:manual:<sectionId>:<manualId>
// so a tester's verdict survives reloads and is included in exports.

const STATUSES = ['PASS', 'FAIL', 'SKIP'];

function key(sectionId, manualId) {
  return `webtest:manual:${sectionId}:${manualId}`;
}

export function getManualStatus(sectionId, manualId) {
  try {
    return localStorage.getItem(key(sectionId, manualId)) || '';
  } catch {
    return '';
  }
}

export function setManualStatus(sectionId, manualId, status) {
  try {
    if (status) localStorage.setItem(key(sectionId, manualId), status);
    else localStorage.removeItem(key(sectionId, manualId));
  } catch {
    // storage unavailable — verdict stays in the DOM only
  }
}

// renderManual builds the manual-checks block for a section. onChange fires after
// any verdict toggle so the caller can refresh the global tally.
export function renderManual(mod, getCtx, onChange) {
  const wrap = document.createElement('div');
  wrap.className = 'manual';
  const h = document.createElement('h3');
  h.textContent = 'Manual checks';
  wrap.append(h);

  for (const entry of mod.manual) {
    const item = document.createElement('div');
    item.className = 'manual-item';

    const name = document.createElement('div');
    name.className = 'manual-name';
    name.textContent = entry.name;
    item.append(name);

    const instr = document.createElement('div');
    instr.className = 'manual-instructions';
    instr.textContent = entry.instructions || '';
    item.append(instr);

    if (typeof entry.start === 'function') {
      const startBtn = document.createElement('button');
      startBtn.className = 'manual-start';
      startBtn.textContent = 'Start';
      const out = document.createElement('pre');
      out.className = 'manual-out';
      out.hidden = true;
      startBtn.addEventListener('click', async () => {
        out.hidden = false;
        out.textContent = 'running…';
        try {
          const res = await entry.start(getCtx());
          out.textContent = res === undefined ? 'ok' : (typeof res === 'string' ? res : JSON.stringify(res, null, 2));
        } catch (e) {
          out.textContent = 'error: ' + ((e && e.message) || e);
        }
      });
      item.append(startBtn, out);
    }

    const tri = document.createElement('div');
    tri.className = 'tri';
    const buttons = [];
    const refresh = () => {
      const cur = getManualStatus(mod.id, entry.id);
      for (const b of buttons) b.classList.toggle('active', b.dataset.status === cur);
    };
    for (const s of STATUSES) {
      const b = document.createElement('button');
      b.className = 'tri-btn ' + s.toLowerCase();
      b.dataset.status = s;
      b.textContent = s;
      b.addEventListener('click', () => {
        const cur = getManualStatus(mod.id, entry.id);
        setManualStatus(mod.id, entry.id, cur === s ? '' : s);
        refresh();
        if (onChange) onChange();
      });
      buttons.push(b);
      tri.append(b);
    }
    refresh();
    item.append(tri);

    wrap.append(item);
  }

  return wrap;
}

// collectManual snapshots a section's manual verdicts for export. An untouched
// entry reports SKIP.
export function collectManual(mod) {
  if (!mod.manual || !mod.manual.length) return [];
  return mod.manual.map((e) => ({
    sectionId: mod.id,
    id: e.id,
    name: e.name,
    instructions: e.instructions || '',
    status: getManualStatus(mod.id, e.id) || 'SKIP',
  }));
}

export default { renderManual, collectManual, getManualStatus, setManualStatus };
