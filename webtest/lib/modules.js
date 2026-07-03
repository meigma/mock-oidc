// modules.js — ordered section registry.
//
// The console ships nine section modules; only discovery.js exists in this
// foundation commit. The other sections are written by parallel agents against
// the frozen module contract and are guaranteed to appear before the console is
// next loaded. Each import is wrapped in try/catch so a not-yet-written section
// degrades gracefully (a console warning) instead of breaking the whole page.

const SECTION_PATHS = [
  '../sections/discovery.js',
  '../sections/jwks.js',
  '../sections/authorize.js',
  '../sections/grants.js',
  '../sections/tokenContent.js',
  '../sections/lifecycle.js',
  '../sections/controlPlane.js',
  '../sections/corsSec.js',
];

// loadModules imports each section in order, collecting the default exports that
// resolve. Missing modules are skipped with a warning.
export async function loadModules() {
  const mods = [];
  for (const path of SECTION_PATHS) {
    try {
      // eslint-disable-next-line no-await-in-loop -- ordered, one-time load
      const m = await import(path);
      if (m && m.default) mods.push(m.default);
    } catch (e) {
      console.warn('webtest: section not loaded (expected until it is written):', path, (e && e.message) || e);
    }
  }
  return mods;
}

export default { loadModules, SECTION_PATHS };
