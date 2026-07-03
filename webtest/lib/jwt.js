// jwt.js — decode and (real) verify compact JWTs with WebCrypto.
//
// decode/typ never verify; verify imports the JWK selected by kid (falling back
// to the first key) and checks the signature for RSASSA-PKCS1-v1_5 (RS*),
// RSA-PSS (PS*), and ECDSA (ES256/ES384, JOSE raw r||s signatures).

function b64urlToBytes(seg) {
  let s = seg.replace(/-/g, '+').replace(/_/g, '/');
  while (s.length % 4) s += '=';
  const bin = atob(s);
  const bytes = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
  return bytes;
}

function b64urlToString(seg) {
  return new TextDecoder().decode(b64urlToBytes(seg));
}

// decode returns the parsed header and payload of a compact JWT. Throws on a
// malformed token; callers that must not throw should wrap it.
export function decode(compact) {
  const parts = String(compact).split('.');
  if (parts.length < 2) throw new Error('not a compact JWT');
  return {
    header: JSON.parse(b64urlToString(parts[0])),
    payload: JSON.parse(b64urlToString(parts[1])),
  };
}

// typ returns the JOSE header `typ` (e.g. 'at+jwt', 'JWT'), or undefined.
export function typ(compact) {
  try {
    return decode(compact).header.typ;
  } catch {
    return undefined;
  }
}

// algParams maps a JOSE alg to the WebCrypto import and verify parameters. An
// unknown alg returns null (verify then reports false).
function algParams(alg) {
  switch (alg) {
    case 'RS256': return { importAlg: { name: 'RSASSA-PKCS1-v1_5', hash: 'SHA-256' }, verifyAlg: { name: 'RSASSA-PKCS1-v1_5' } };
    case 'RS384': return { importAlg: { name: 'RSASSA-PKCS1-v1_5', hash: 'SHA-384' }, verifyAlg: { name: 'RSASSA-PKCS1-v1_5' } };
    case 'RS512': return { importAlg: { name: 'RSASSA-PKCS1-v1_5', hash: 'SHA-512' }, verifyAlg: { name: 'RSASSA-PKCS1-v1_5' } };
    case 'PS256': return { importAlg: { name: 'RSA-PSS', hash: 'SHA-256' }, verifyAlg: { name: 'RSA-PSS', saltLength: 32 } };
    case 'PS384': return { importAlg: { name: 'RSA-PSS', hash: 'SHA-384' }, verifyAlg: { name: 'RSA-PSS', saltLength: 48 } };
    case 'PS512': return { importAlg: { name: 'RSA-PSS', hash: 'SHA-512' }, verifyAlg: { name: 'RSA-PSS', saltLength: 64 } };
    case 'ES256': return { importAlg: { name: 'ECDSA', namedCurve: 'P-256' }, verifyAlg: { name: 'ECDSA', hash: 'SHA-256' } };
    case 'ES384': return { importAlg: { name: 'ECDSA', namedCurve: 'P-384' }, verifyAlg: { name: 'ECDSA', hash: 'SHA-384' } };
    default: return null;
  }
}

// verify returns true only when the token's signature checks against a key in
// jwksJson. Any structural problem, unknown alg, or crypto error returns false
// (never throws).
export async function verify(compact, jwksJson) {
  try {
    const parts = String(compact).split('.');
    if (parts.length !== 3) return false;
    const header = JSON.parse(b64urlToString(parts[0]));
    const params = algParams(header.alg);
    if (!params) return false;
    const keys = (jwksJson && Array.isArray(jwksJson.keys)) ? jwksJson.keys : [];
    if (!keys.length) return false;
    const jwk = keys.find((k) => k.kid && k.kid === header.kid) || keys[0];
    // Import a public verifying key. Strip members WebCrypto may reject for the
    // chosen algorithm (e.g. a jwk.alg that names a different family).
    const material = { ...jwk };
    delete material.alg;
    delete material.use;
    delete material.key_ops;
    const key = await crypto.subtle.importKey('jwk', material, params.importAlg, false, ['verify']);
    const data = new TextEncoder().encode(parts[0] + '.' + parts[1]);
    const sig = b64urlToBytes(parts[2]);
    return await crypto.subtle.verify(params.verifyAlg, key, sig, data);
  } catch {
    return false;
  }
}

export default { decode, typ, verify };
