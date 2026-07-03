// form.js — application/x-www-form-urlencoded encoding.
//
// encode turns a plain object into a urlencoded string. Arrays repeat the key
// (scope=a&scope=b); null/undefined values are dropped so callers can build
// sparse form bodies without pruning first.

export function encode(obj = {}) {
  const p = new URLSearchParams();
  for (const [k, v] of Object.entries(obj)) {
    if (v === undefined || v === null) continue;
    if (Array.isArray(v)) {
      for (const item of v) {
        if (item === undefined || item === null) continue;
        p.append(k, String(item));
      }
    } else {
      p.append(k, String(v));
    }
  }
  return p.toString();
}

export default { encode };
